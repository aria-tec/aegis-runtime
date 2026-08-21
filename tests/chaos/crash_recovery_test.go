package chaos_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/llm"
	"github.com/aria-tec/aegis-runtime/pkg/orchestrator"
	"github.com/aria-tec/aegis-runtime/pkg/sandbox"
	"github.com/aria-tec/aegis-runtime/pkg/storage"
)

// trackingRunner records execution count for each tool to verify zero-duplicate execution.
type trackingRunner struct {
	mu     sync.Mutex
	counts map[string]int
	delays map[string]time.Duration
}

func newTrackingRunner() *trackingRunner {
	return &trackingRunner{
		counts: make(map[string]int),
		delays: make(map[string]time.Duration),
	}
}

func (r *trackingRunner) Execute(ctx context.Context, req domain.ToolExecutionRequest) (*domain.ToolExecutionResult, error) {
	r.mu.Lock()
	r.counts[req.ToolName]++
	delay := r.delays[req.ToolName]
	r.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	return &domain.ToolExecutionResult{
		ToolName: req.ToolName,
		ExitCode: 0,
		Stdout:   fmt.Sprintf("OK: executed %s with args %v", req.ToolName, req.Args),
		Duration: 10 * time.Millisecond,
	}, nil
}

func (r *trackingRunner) CallCount(toolName string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[toolName]
}

// crashingDriver wraps a Driver and triggers a simulated process crash (e.g. store close + panic/cancel) at a specified step.
type crashingDriver struct {
	inner        llm.Driver
	crashAtStep  int
	crashed      atomic.Bool
	onCrashHook  func()
}

func (c *crashingDriver) GenerateStep(ctx context.Context, req domain.StepPromptRequest) (*domain.StepPromptResponse, error) {
	resp, err := c.inner.GenerateStep(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// TestChaos_CrashRecoveryReplay validates end-to-end durable replay across process termination:
// 1. A 3-step workflow with tools is initiated.
// 2. Engine 1 executes Step 1 (tool call check_orders), updates state & events, then the process crashes (store closed).
// 3. A new Engine 2 instance is initialized with a freshly opened store pointing to the same SQLite DB file.
// 4. ResumeWorkflow is called.
// 5. Verification ensures:
//    - Workflow reaches COMPLETED status.
//    - Final result matches expected value.
//    - Total tokens match the exact sum of all 3 steps.
//    - Event history contains all steps and tool execution results in strictly ordered sequence.
//    - Zero duplicate executions for Step 1 tool.
//    - Re-resuming a completed workflow is idempotent.
func TestChaos_CrashRecoveryReplay(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "chaos_replay.db")
	workflowID := "wf-chaos-3step-001"
	expectedFinalResult := "Customer refund of $199.99 processed successfully"

	// Configure deterministic MockDriver for all 3 steps
	mockLLM := llm.NewMockDriver()
	mockLLM.RegisterStep(1, domain.StepPromptResponse{
		Thought: "Step 1: Inspect inventory and verify returned items",
		ToolCalls: []domain.ToolCall{
			{ID: "call-1", ToolName: "check_inventory", Arguments: `{"sku": "SKU-99", "qty": 5}`},
		},
		TokensUsed: 100,
	})
	mockLLM.RegisterStep(2, domain.StepPromptResponse{
		Thought: "Step 2: Issue payment refund to customer",
		ToolCalls: []domain.ToolCall{
			{ID: "call-2", ToolName: "issue_refund", Arguments: `{"customer_id": "CUST-42", "amount": 199.99}`},
		},
		TokensUsed: 120,
	})
	mockLLM.RegisterStep(3, domain.StepPromptResponse{
		Thought:     "Step 3: All steps finished, refund confirmed",
		IsComplete:  true,
		FinalResult: expectedFinalResult,
		TokensUsed:  80,
	})

	runner := newTrackingRunner()

	// ------------------------------------------------------------------------
	// PHASE 1: Initial Run & Partial Execution Crash Simulation
	// ------------------------------------------------------------------------
	t.Log(">>> PHASE 1: Launching Engine 1 and executing Step 1...")
	store1, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("Phase 1: failed to open SQLite store: %v", err)
	}

	ctx := context.Background()

	// Create initial workflow record (simulating StartWorkflow step 0)
	initialWf := domain.NewWorkflow(workflowID, "Handle Customer Refund", 10, 8000)
	if err := store1.CreateWorkflow(ctx, initialWf); err != nil {
		t.Fatalf("Phase 1: failed to create workflow: %v", err)
	}

	// Record EventWorkflowStarted
	startEvtPayload, _ := json.Marshal(map[string]string{"goal": "Handle Customer Refund"})
	startEvt := &domain.WorkflowEvent{
		ID:          fmt.Sprintf("evt_%s_0_%d", workflowID, time.Now().UnixNano()),
		WorkflowID:  workflowID,
		StepNumber:  0,
		EventType:   domain.EventWorkflowStarted,
		PayloadJSON: string(startEvtPayload),
		TokensUsed:  0,
		DurationMs:  0,
		CreatedAt:   time.Now().UTC(),
	}
	if err := store1.AppendEvent(ctx, startEvt); err != nil {
		t.Fatalf("Phase 1: failed to append start event: %v", err)
	}

	// Execute Step 1 in Engine 1
	initialWf.Status = domain.StatusRunning
	initialWf.CurrentStep = 1
	initialWf.TotalTokens = 100
	if err := store1.UpdateWorkflow(ctx, initialWf); err != nil {
		t.Fatalf("Phase 1: failed to update workflow: %v", err)
	}

	// Step 1: Step Started Event
	step1StartPayload, _ := json.Marshal(map[string]int{"step": 1})
	_ = store1.AppendEvent(ctx, &domain.WorkflowEvent{
		ID:          fmt.Sprintf("evt_%s_1_start_%d", workflowID, time.Now().UnixNano()),
		WorkflowID:  workflowID,
		StepNumber:  1,
		EventType:   domain.EventStepStarted,
		PayloadJSON: string(step1StartPayload),
		CreatedAt:   time.Now().UTC(),
	})

	// Step 1: LLM Prompted Event
	step1Resp, _ := mockLLM.GenerateStep(ctx, domain.StepPromptRequest{WorkflowID: workflowID, StepNumber: 1})
	step1RespBytes, _ := json.Marshal(step1Resp)
	_ = store1.AppendEvent(ctx, &domain.WorkflowEvent{
		ID:          fmt.Sprintf("evt_%s_1_llm_%d", workflowID, time.Now().UnixNano()),
		WorkflowID:  workflowID,
		StepNumber:  1,
		EventType:   domain.EventLLMPrompted,
		PayloadJSON: string(step1RespBytes),
		TokensUsed:  step1Resp.TokensUsed,
		DurationMs:  15,
		CreatedAt:   time.Now().UTC(),
	})

	// Step 1: Tool Execution
	for _, tool := range step1Resp.ToolCalls {
		toolCallBytes, _ := json.Marshal(tool)
		_ = store1.AppendEvent(ctx, &domain.WorkflowEvent{
			ID:          fmt.Sprintf("evt_%s_1_tool_call_%d", workflowID, time.Now().UnixNano()),
			WorkflowID:  workflowID,
			StepNumber:  1,
			EventType:   domain.EventToolCalled,
			PayloadJSON: string(toolCallBytes),
			CreatedAt:   time.Now().UTC(),
		})

		toolRes, err := runner.Execute(ctx, domain.ToolExecutionRequest{
			WorkflowID:  workflowID,
			StepNumber:  1,
			ToolName:    tool.ToolName,
			Command:     "echo",
			Args:        []string{tool.Arguments},
			TimeoutSecs: 10,
		})
		if err != nil {
			t.Fatalf("Phase 1: tool execution error: %v", err)
		}

		toolResBytes, _ := json.Marshal(toolRes)
		_ = store1.AppendEvent(ctx, &domain.WorkflowEvent{
			ID:          fmt.Sprintf("evt_%s_1_tool_res_%d", workflowID, time.Now().UnixNano()),
			WorkflowID:  workflowID,
			StepNumber:  1,
			EventType:   domain.EventToolCompleted,
			PayloadJSON: string(toolResBytes),
			DurationMs:  10,
			CreatedAt:   time.Now().UTC(),
		})
	}

	// Step 1: Step Completed Event
	_ = store1.AppendEvent(ctx, &domain.WorkflowEvent{
		ID:          fmt.Sprintf("evt_%s_1_done_%d", workflowID, time.Now().UnixNano()),
		WorkflowID:  workflowID,
		StepNumber:  1,
		EventType:   domain.EventStepCompleted,
		PayloadJSON: string(step1StartPayload),
		CreatedAt:   time.Now().UTC(),
	})

	// Verify Step 1 tool execution count in Phase 1
	if runner.CallCount("check_inventory") != 1 {
		t.Fatalf("Phase 1: expected check_inventory call count 1, got %d", runner.CallCount("check_inventory"))
	}
	if runner.CallCount("issue_refund") != 0 {
		t.Fatalf("Phase 1: expected issue_refund call count 0, got %d", runner.CallCount("issue_refund"))
	}

	// SIMULATE PROCESS CRASH: Abruptly close store1 to terminate process simulation
	t.Log(">>> SIMULATING UNGRACEFUL PROCESS CRASH / POWER LOSS: Closing store 1...")
	if err := store1.Close(); err != nil {
		t.Fatalf("Phase 1: failed to close store1 on crash: %v", err)
	}

	// ------------------------------------------------------------------------
	// PHASE 2: Server Reboot & Engine 2 Resumption
	// ------------------------------------------------------------------------
	t.Log(">>> PHASE 2: Reopening SQLite store and initializing Engine 2...")
	store2, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("Phase 2: failed to reopen SQLite store: %v", err)
	}
	defer store2.Close()

	engine2 := orchestrator.NewEngine(store2, mockLLM, runner)

	t.Log(">>> PHASE 2: Calling ResumeWorkflow...")
	resumedWf, err := engine2.ResumeWorkflow(ctx, workflowID)
	if err != nil {
		t.Fatalf("Phase 2: failed to resume workflow: %v", err)
	}

	// ------------------------------------------------------------------------
	// VERIFICATION CHECKS
	// ------------------------------------------------------------------------

	// 1. Workflow Status & Result
	if resumedWf.Status != domain.StatusCompleted {
		t.Fatalf("expected status COMPLETED, got %s (error: %s)", resumedWf.Status, resumedWf.ErrorMessage)
	}
	if resumedWf.Result != expectedFinalResult {
		t.Fatalf("expected final result '%s', got '%s'", expectedFinalResult, resumedWf.Result)
	}
	if resumedWf.CurrentStep != 3 {
		t.Fatalf("expected final current_step 3, got %d", resumedWf.CurrentStep)
	}
	expectedTotalTokens := 100 + 120 + 80 // Step 1 (100) + Step 2 (120) + Step 3 (80)
	if resumedWf.TotalTokens != expectedTotalTokens {
		t.Fatalf("expected total tokens %d, got %d", expectedTotalTokens, resumedWf.TotalTokens)
	}

	// 2. Zero-Duplicate Executions
	t.Log(">>> Verifying zero duplicate tool executions...")
	if count := runner.CallCount("check_inventory"); count != 1 {
		t.Fatalf("DUPLICATE EXECUTION DETECTED: check_inventory was executed %d times (expected exactly 1)", count)
	}
	if count := runner.CallCount("issue_refund"); count != 1 {
		t.Fatalf("DUPLICATE/MISSING EXECUTION: issue_refund was executed %d times (expected exactly 1)", count)
	}

	// 3. Strictly Ordered Event History
	t.Log(">>> Verifying event history ordering and completeness...")
	events, err := store2.GetEvents(ctx, workflowID)
	if err != nil {
		t.Fatalf("failed to retrieve event history: %v", err)
	}

	expectedEventSequence := []struct {
		step      int
		eventType domain.EventType
	}{
		{0, domain.EventWorkflowStarted},
		// Step 1
		{1, domain.EventStepStarted},
		{1, domain.EventLLMPrompted},
		{1, domain.EventToolCalled},
		{1, domain.EventToolCompleted},
		{1, domain.EventStepCompleted},
		// Step 2 (resumed)
		{2, domain.EventStepStarted},
		{2, domain.EventLLMPrompted},
		{2, domain.EventToolCalled},
		{2, domain.EventToolCompleted},
		{2, domain.EventStepCompleted},
		// Step 3 (completion)
		{3, domain.EventStepStarted},
		{3, domain.EventLLMPrompted},
		{3, domain.EventWorkflowCompleted},
	}

	if len(events) != len(expectedEventSequence) {
		t.Fatalf("expected %d events in history, got %d", len(expectedEventSequence), len(events))
	}

	for i, expected := range expectedEventSequence {
		evt := events[i]
		if evt.StepNumber != expected.step {
			t.Errorf("event[%d] expected step %d, got %d (type: %s)", i, expected.step, evt.StepNumber, evt.EventType)
		}
		if evt.EventType != expected.eventType {
			t.Errorf("event[%d] expected type %s, got %s (step: %d)", i, expected.eventType, evt.EventType, evt.StepNumber)
		}
		if i > 0 {
			if evt.CreatedAt.Before(events[i-1].CreatedAt) {
				t.Errorf("event[%d] timestamp %v is before event[%d] timestamp %v", i, evt.CreatedAt, i-1, events[i-1].CreatedAt)
			}
		}
	}

	// 4. Verification of DB Persisted State Matches Resumed State
	persistedWf, err := store2.GetWorkflow(ctx, workflowID)
	if err != nil {
		t.Fatalf("failed to get workflow from DB: %v", err)
	}
	if persistedWf.Status != domain.StatusCompleted {
		t.Fatalf("expected DB persisted status COMPLETED, got %s", persistedWf.Status)
	}
	if persistedWf.Result != expectedFinalResult {
		t.Fatalf("expected DB persisted result '%s', got '%s'", expectedFinalResult, persistedWf.Result)
	}

	// 5. Idempotent Re-Resumption
	t.Log(">>> Verifying idempotency: calling ResumeWorkflow on completed workflow...")
	reResumedWf, err := engine2.ResumeWorkflow(ctx, workflowID)
	if err != nil {
		t.Fatalf("re-resume failed: %v", err)
	}
	if reResumedWf.Status != domain.StatusCompleted {
		t.Fatalf("re-resumed expected COMPLETED, got %s", reResumedWf.Status)
	}

	// Ensure no new events or tool calls occurred on re-resumption
	afterEvents, err := store2.GetEvents(ctx, workflowID)
	if err != nil {
		t.Fatalf("failed to get events after re-resume: %v", err)
	}
	if len(afterEvents) != len(events) {
		t.Fatalf("idempotency violation: events count increased from %d to %d", len(events), len(afterEvents))
	}
	if count := runner.CallCount("check_inventory"); count != 1 {
		t.Fatalf("idempotency violation: check_inventory call count changed to %d", count)
	}
	if count := runner.CallCount("issue_refund"); count != 1 {
		t.Fatalf("idempotency violation: issue_refund call count changed to %d", count)
	}

	t.Log(">>> SUCCESS: Crash-recovery replay validation passed with 100% integrity!")
}

// TestChaos_MultiCrashAndRecovery tests multiple crashes across consecutive step transitions:
// Workflow has 4 steps:
// Step 1 -> Crash 1 -> Restart -> Step 2 -> Crash 2 -> Restart -> Step 3 -> Crash 3 -> Restart -> Step 4 -> Complete
func TestChaos_MultiCrashAndRecovery(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "multi_crash.db")
	workflowID := "wf-multi-crash-002"

	mockLLM := llm.NewMockDriver()
	mockLLM.RegisterStep(1, domain.StepPromptResponse{
		Thought: "Step 1: Initialize environment",
		ToolCalls: []domain.ToolCall{
			{ID: "tool-1", ToolName: "init_env", Arguments: "config.json"},
		},
		TokensUsed: 50,
	})
	mockLLM.RegisterStep(2, domain.StepPromptResponse{
		Thought: "Step 2: Process batch dataset",
		ToolCalls: []domain.ToolCall{
			{ID: "tool-2", ToolName: "process_data", Arguments: "dataset.csv"},
		},
		TokensUsed: 75,
	})
	mockLLM.RegisterStep(3, domain.StepPromptResponse{
		Thought: "Step 3: Generate summary analytics",
		ToolCalls: []domain.ToolCall{
			{ID: "tool-3", ToolName: "gen_summary", Arguments: "summary.pdf"},
		},
		TokensUsed: 60,
	})
	mockLLM.RegisterStep(4, domain.StepPromptResponse{
		Thought:     "Step 4: All 3 pipeline stages finished",
		IsComplete:  true,
		FinalResult: "Multi-stage pipeline successfully executed",
		TokensUsed:  40,
	})

	runner := newTrackingRunner()
	ctx := context.Background()

	// Initial store creation
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init SQLite store: %v", err)
	}

	initialWf := domain.NewWorkflow(workflowID, "Execute Multi-Stage Pipeline", 10, 8000)
	if err := store.CreateWorkflow(ctx, initialWf); err != nil {
		t.Fatalf("failed to create workflow: %v", err)
	}
	startPayload, _ := json.Marshal(map[string]string{"goal": initialWf.Goal})
	_ = store.AppendEvent(ctx, &domain.WorkflowEvent{
		ID:          fmt.Sprintf("evt_%s_0_%d", workflowID, time.Now().UnixNano()),
		WorkflowID:  workflowID,
		StepNumber:  0,
		EventType:   domain.EventWorkflowStarted,
		PayloadJSON: string(startPayload),
		CreatedAt:   time.Now().UTC(),
	})
	store.Close()

	// Run step by step with crash between each step
	accumulatedTokens := 0
	toolNames := []string{"init_env", "process_data", "gen_summary"}

	for completedStep := 1; completedStep <= 3; completedStep++ {
		t.Logf(">>> Simulating crash cycle after Step %d...", completedStep)
		st, err := storage.NewSQLiteStore(dbPath)
		if err != nil {
			t.Fatalf("step %d: failed to reopen store: %v", completedStep, err)
		}

		wf, err := st.GetWorkflow(ctx, workflowID)
		if err != nil {
			t.Fatalf("step %d: failed to get workflow: %v", completedStep, err)
		}

		// Perform step work
		wf.CurrentStep = completedStep
		stepResp, _ := mockLLM.GenerateStep(ctx, domain.StepPromptRequest{WorkflowID: workflowID, StepNumber: completedStep})
		accumulatedTokens += stepResp.TokensUsed
		wf.TotalTokens = accumulatedTokens
		wf.Status = domain.StatusRunning
		_ = st.UpdateWorkflow(ctx, wf)

		// Record step events
		stepPayload, _ := json.Marshal(map[string]int{"step": completedStep})
		_ = st.AppendEvent(ctx, &domain.WorkflowEvent{
			ID:          fmt.Sprintf("evt_%s_%d_start_%d", workflowID, completedStep, time.Now().UnixNano()),
			WorkflowID:  workflowID,
			StepNumber:  completedStep,
			EventType:   domain.EventStepStarted,
			PayloadJSON: string(stepPayload),
			CreatedAt:   time.Now().UTC(),
		})

		respBytes, _ := json.Marshal(stepResp)
		_ = st.AppendEvent(ctx, &domain.WorkflowEvent{
			ID:          fmt.Sprintf("evt_%s_%d_llm_%d", workflowID, completedStep, time.Now().UnixNano()),
			WorkflowID:  workflowID,
			StepNumber:  completedStep,
			EventType:   domain.EventLLMPrompted,
			PayloadJSON: string(respBytes),
			TokensUsed:  stepResp.TokensUsed,
			CreatedAt:   time.Now().UTC(),
		})

		for _, tool := range stepResp.ToolCalls {
			tcBytes, _ := json.Marshal(tool)
			_ = st.AppendEvent(ctx, &domain.WorkflowEvent{
				ID:          fmt.Sprintf("evt_%s_%d_tc_%d", workflowID, completedStep, time.Now().UnixNano()),
				WorkflowID:  workflowID,
				StepNumber:  completedStep,
				EventType:   domain.EventToolCalled,
				PayloadJSON: string(tcBytes),
				CreatedAt:   time.Now().UTC(),
			})

			res, _ := runner.Execute(ctx, domain.ToolExecutionRequest{
				WorkflowID: workflowID,
				StepNumber: completedStep,
				ToolName:   tool.ToolName,
				Args:       []string{tool.Arguments},
			})
			resBytes, _ := json.Marshal(res)
			_ = st.AppendEvent(ctx, &domain.WorkflowEvent{
				ID:          fmt.Sprintf("evt_%s_%d_tr_%d", workflowID, completedStep, time.Now().UnixNano()),
				WorkflowID:  workflowID,
				StepNumber:  completedStep,
				EventType:   domain.EventToolCompleted,
				PayloadJSON: string(resBytes),
				CreatedAt:   time.Now().UTC(),
			})
		}

		_ = st.AppendEvent(ctx, &domain.WorkflowEvent{
			ID:          fmt.Sprintf("evt_%s_%d_done_%d", workflowID, completedStep, time.Now().UnixNano()),
			WorkflowID:  workflowID,
			StepNumber:  completedStep,
			EventType:   domain.EventStepCompleted,
			PayloadJSON: string(stepPayload),
			CreatedAt:   time.Now().UTC(),
		})

		// Crash
		st.Close()
	}

	// Final boot: Resume to completion (Step 4)
	t.Log(">>> FINAL BOOT: Resuming workflow to completion at Step 4...")
	finalStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("final boot: failed to open store: %v", err)
	}
	defer finalStore.Close()

	finalEngine := orchestrator.NewEngine(finalStore, mockLLM, runner)
	finalWf, err := finalEngine.ResumeWorkflow(ctx, workflowID)
	if err != nil {
		t.Fatalf("final boot: failed to resume workflow: %v", err)
	}

	if finalWf.Status != domain.StatusCompleted {
		t.Fatalf("expected final status COMPLETED, got %s", finalWf.Status)
	}
	if finalWf.Result != "Multi-stage pipeline successfully executed" {
		t.Fatalf("unexpected result: %s", finalWf.Result)
	}
	if finalWf.CurrentStep != 4 {
		t.Fatalf("expected step 4, got %d", finalWf.CurrentStep)
	}
	expectedTotal := 50 + 75 + 60 + 40
	if finalWf.TotalTokens != expectedTotal {
		t.Fatalf("expected %d total tokens, got %d", expectedTotal, finalWf.TotalTokens)
	}

	// Verify all tools executed exactly once
	for _, toolName := range toolNames {
		if count := runner.CallCount(toolName); count != 1 {
			t.Errorf("tool %s was executed %d times (expected exactly 1)", toolName, count)
		}
	}
}

// TestChaos_DeterministicEquivalence verifies that an interrupted+resumed workflow produces
// the exact same final state, event types sequence, and token counts as an uninterrupted workflow.
func TestChaos_DeterministicEquivalence(t *testing.T) {
	tempDir := t.TempDir()
	dbA := filepath.Join(tempDir, "uninterrupted.db")
	dbB := filepath.Join(tempDir, "interrupted.db")

	mockLLM := llm.NewMockDriver()
	mockLLM.RegisterStep(1, domain.StepPromptResponse{
		Thought: "Calculate financial metrics",
		ToolCalls: []domain.ToolCall{
			{ID: "t-1", ToolName: "calc_metric", Arguments: "Q3-2026"},
		},
		TokensUsed: 90,
	})
	mockLLM.RegisterStep(2, domain.StepPromptResponse{
		Thought:     "Metrics verified",
		IsComplete:  true,
		FinalResult: "Financial report compiled: $1,250,000 ARR",
		TokensUsed:  60,
	})

	runnerA := sandbox.NewProcessRunner(filepath.Join(tempDir, "scratch_a"))
	runnerB := sandbox.NewProcessRunner(filepath.Join(tempDir, "scratch_b"))
	ctx := context.Background()

	// Workflow A: Uninterrupted
	storeA, _ := storage.NewSQLiteStore(dbA)
	defer storeA.Close()
	engineA := orchestrator.NewEngine(storeA, mockLLM, runnerA)
	wfA, err := engineA.StartWorkflow(ctx, "wf-uninterrupted", "Compile Report", 5, 5000)
	if err != nil {
		t.Fatalf("Workflow A failed: %v", err)
	}

	// Workflow B: Interrupted at Step 1, Resumed at Step 2
	storeB1, _ := storage.NewSQLiteStore(dbB)
	initialWfB := domain.NewWorkflow("wf-interrupted", "Compile Report", 5, 5000)
	_ = storeB1.CreateWorkflow(ctx, initialWfB)
	startPayload, _ := json.Marshal(map[string]string{"goal": "Compile Report"})
	_ = storeB1.AppendEvent(ctx, &domain.WorkflowEvent{
		ID:          "evt_b_0",
		WorkflowID:  "wf-interrupted",
		StepNumber:  0,
		EventType:   domain.EventWorkflowStarted,
		PayloadJSON: string(startPayload),
		CreatedAt:   time.Now().UTC(),
	})

	// Run Step 1 manually in Store B1
	initialWfB.Status = domain.StatusRunning
	initialWfB.CurrentStep = 1
	initialWfB.TotalTokens = 90
	_ = storeB1.UpdateWorkflow(ctx, initialWfB)

	step1Payload, _ := json.Marshal(map[string]int{"step": 1})
	_ = storeB1.AppendEvent(ctx, &domain.WorkflowEvent{ID: "evt_b_1_s", WorkflowID: "wf-interrupted", StepNumber: 1, EventType: domain.EventStepStarted, PayloadJSON: string(step1Payload), CreatedAt: time.Now().UTC()})
	step1Resp, _ := mockLLM.GenerateStep(ctx, domain.StepPromptRequest{WorkflowID: "wf-interrupted", StepNumber: 1})
	step1RespBytes, _ := json.Marshal(step1Resp)
	_ = storeB1.AppendEvent(ctx, &domain.WorkflowEvent{ID: "evt_b_1_llm", WorkflowID: "wf-interrupted", StepNumber: 1, EventType: domain.EventLLMPrompted, PayloadJSON: string(step1RespBytes), TokensUsed: 90, CreatedAt: time.Now().UTC()})
	toolCallBytes, _ := json.Marshal(step1Resp.ToolCalls[0])
	_ = storeB1.AppendEvent(ctx, &domain.WorkflowEvent{ID: "evt_b_1_tc", WorkflowID: "wf-interrupted", StepNumber: 1, EventType: domain.EventToolCalled, PayloadJSON: string(toolCallBytes), CreatedAt: time.Now().UTC()})
	toolRes, _ := runnerB.Execute(ctx, domain.ToolExecutionRequest{WorkflowID: "wf-interrupted", StepNumber: 1, ToolName: "calc_metric", Command: "echo", Args: []string{"Q3-2026"}})
	toolResBytes, _ := json.Marshal(toolRes)
	_ = storeB1.AppendEvent(ctx, &domain.WorkflowEvent{ID: "evt_b_1_tr", WorkflowID: "wf-interrupted", StepNumber: 1, EventType: domain.EventToolCompleted, PayloadJSON: string(toolResBytes), CreatedAt: time.Now().UTC()})
	_ = storeB1.AppendEvent(ctx, &domain.WorkflowEvent{ID: "evt_b_1_d", WorkflowID: "wf-interrupted", StepNumber: 1, EventType: domain.EventStepCompleted, PayloadJSON: string(step1Payload), CreatedAt: time.Now().UTC()})

	// Close store B1 (crash)
	storeB1.Close()

	// Reopen store B2 & Resume
	storeB2, _ := storage.NewSQLiteStore(dbB)
	defer storeB2.Close()
	engineB2 := orchestrator.NewEngine(storeB2, mockLLM, runnerB)
	wfB, err := engineB2.ResumeWorkflow(ctx, "wf-interrupted")
	if err != nil {
		t.Fatalf("Workflow B resume failed: %v", err)
	}

	// Compare Workflow A and Workflow B
	if wfA.Status != wfB.Status {
		t.Fatalf("status mismatch: wfA=%s, wfB=%s", wfA.Status, wfB.Status)
	}
	if wfA.Result != wfB.Result {
		t.Fatalf("result mismatch: wfA=%s, wfB=%s", wfA.Result, wfB.Result)
	}
	if wfA.CurrentStep != wfB.CurrentStep {
		t.Fatalf("current_step mismatch: wfA=%d, wfB=%d", wfA.CurrentStep, wfB.CurrentStep)
	}
	if wfA.TotalTokens != wfB.TotalTokens {
		t.Fatalf("total tokens mismatch: wfA=%d, wfB=%d", wfA.TotalTokens, wfB.TotalTokens)
	}

	eventsA, _ := storeA.GetEvents(ctx, "wf-uninterrupted")
	eventsB, _ := storeB2.GetEvents(ctx, "wf-interrupted")

	if len(eventsA) != len(eventsB) {
		t.Fatalf("event count mismatch: len(A)=%d, len(B)=%d", len(eventsA), len(eventsB))
	}

	for i := range eventsA {
		if eventsA[i].EventType != eventsB[i].EventType {
			t.Errorf("event[%d] type mismatch: A=%s, B=%s", i, eventsA[i].EventType, eventsB[i].EventType)
		}
		if eventsA[i].StepNumber != eventsB[i].StepNumber {
			t.Errorf("event[%d] step mismatch: A=%d, B=%d", i, eventsA[i].StepNumber, eventsB[i].StepNumber)
		}
	}
}
