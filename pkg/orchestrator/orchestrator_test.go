package orchestrator_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/llm"
	"github.com/aria-tec/aegis-runtime/pkg/orchestrator"
	"github.com/aria-tec/aegis-runtime/pkg/sandbox"
	"github.com/aria-tec/aegis-runtime/pkg/storage"
)

// errDriver is a mock LLM driver that simulates an error
type errDriver struct{}

func (e *errDriver) GenerateStep(ctx context.Context, req domain.StepPromptRequest) (*domain.StepPromptResponse, error) {
	return nil, errors.New("upstream LLM rate limit exceeded")
}

// errRunner is a mock sandbox runner that simulates an execution failure
type errRunner struct{}

func (r *errRunner) Execute(ctx context.Context, req domain.ToolExecutionRequest) (*domain.ToolExecutionResult, error) {
	return nil, errors.New("sandbox process fork failed")
}

func TestOrchestrator_SingleStepCompletion(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_single_step.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	mockLLM := llm.NewMockDriver()
	mockLLM.RegisterStep(1, domain.StepPromptResponse{
		Thought:     "Goal is simple, completing immediately.",
		IsComplete:  true,
		FinalResult: "Order processed successfully",
		TokensUsed:  150,
	})

	runner := sandbox.NewProcessRunner(filepath.Join(tempDir, "scratch"))

	engine := orchestrator.NewEngine(store, mockLLM, runner)
	ctx := context.Background()

	wf, err := engine.StartWorkflow(ctx, "wf-single-1", "Process Order #123", 10, 8000)
	if err != nil {
		t.Fatalf("failed to start workflow: %v", err)
	}

	if wf.Status != domain.StatusCompleted {
		t.Fatalf("expected COMPLETED, got %s (error: %s)", wf.Status, wf.ErrorMessage)
	}
	if wf.Result != "Order processed successfully" {
		t.Fatalf("expected result 'Order processed successfully', got '%s'", wf.Result)
	}
	if wf.TotalTokens != 150 {
		t.Fatalf("expected total tokens 150, got %d", wf.TotalTokens)
	}
	if wf.CurrentStep != 1 {
		t.Fatalf("expected current step 1, got %d", wf.CurrentStep)
	}

	// Verify events persisted
	events, err := store.GetEvents(ctx, "wf-single-1")
	if err != nil {
		t.Fatalf("failed to get events: %v", err)
	}

	expectedTypes := []domain.EventType{
		domain.EventWorkflowStarted,
		domain.EventStepStarted,
		domain.EventLLMPrompted,
		domain.EventWorkflowCompleted,
	}

	if len(events) != len(expectedTypes) {
		t.Fatalf("expected %d events, got %d", len(expectedTypes), len(events))
	}
	for i, exp := range expectedTypes {
		if events[i].EventType != exp {
			t.Errorf("event[%d] expected %s, got %s", i, exp, events[i].EventType)
		}
	}
}

func TestOrchestrator_MultiStepWithToolExecution(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_multi_step.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	mockLLM := llm.NewMockDriver()
	mockLLM.RegisterStep(1, domain.StepPromptResponse{
		Thought:    "Need to check directory contents first.",
		IsComplete: false,
		ToolCalls: []domain.ToolCall{
			{
				ID:        "tool-1",
				ToolName:  "echo",
				Arguments: "hello from tool",
			},
			{
				ID:        "tool-2",
				ToolName:  "sh",
				Arguments: "echo shell test",
			},
		},
		TokensUsed: 100,
	})
	mockLLM.RegisterStep(2, domain.StepPromptResponse{
		Thought:     "Tool execution received. Goal fulfilled.",
		IsComplete:  true,
		FinalResult: "Workflow multi-step finished",
		TokensUsed:  120,
	})

	runner := sandbox.NewProcessRunner(filepath.Join(tempDir, "scratch"))

	engine := orchestrator.NewEngine(store, mockLLM, runner)
	ctx := context.Background()

	wf, err := engine.StartWorkflow(ctx, "wf-multi-1", "Execute multi-step task", 5, 5000)
	if err != nil {
		t.Fatalf("failed to start workflow: %v", err)
	}

	if wf.Status != domain.StatusCompleted {
		t.Fatalf("expected COMPLETED, got %s (error: %s)", wf.Status, wf.ErrorMessage)
	}
	if wf.Result != "Workflow multi-step finished" {
		t.Fatalf("expected result 'Workflow multi-step finished', got '%s'", wf.Result)
	}
	if wf.TotalTokens != 220 {
		t.Fatalf("expected total tokens 220, got %d", wf.TotalTokens)
	}
	if wf.CurrentStep != 2 {
		t.Fatalf("expected current step 2, got %d", wf.CurrentStep)
	}

	// Verify events in order
	events, err := store.GetEvents(ctx, "wf-multi-1")
	if err != nil {
		t.Fatalf("failed to get events: %v", err)
	}

	expectedTypes := []domain.EventType{
		domain.EventWorkflowStarted,
		// Step 1
		domain.EventStepStarted,
		domain.EventLLMPrompted,
		domain.EventToolCalled,
		domain.EventToolCompleted,
		domain.EventToolCalled,
		domain.EventToolCompleted,
		domain.EventStepCompleted,
		// Step 2
		domain.EventStepStarted,
		domain.EventLLMPrompted,
		domain.EventWorkflowCompleted,
	}

	if len(events) != len(expectedTypes) {
		t.Fatalf("expected %d events, got %d", len(expectedTypes), len(events))
	}
	for i, exp := range expectedTypes {
		if events[i].EventType != exp {
			t.Errorf("event[%d] expected %s, got %s", i, exp, events[i].EventType)
		}
	}
}

func TestOrchestrator_ToolExecutionFailure(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_tool_fail.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	mockLLM := llm.NewMockDriver()
	mockLLM.RegisterStep(1, domain.StepPromptResponse{
		Thought:    "Calling failing runner",
		IsComplete: false,
		ToolCalls: []domain.ToolCall{
			{
				ID:        "tool-1",
				ToolName:  "custom_tool",
				Arguments: "arg",
			},
		},
		TokensUsed: 50,
	})
	mockLLM.RegisterStep(2, domain.StepPromptResponse{
		Thought:     "Handled failure",
		IsComplete:  true,
		FinalResult: "Completed after tool error",
		TokensUsed:  50,
	})

	errRun := &errRunner{}
	engine := orchestrator.NewEngine(store, mockLLM, errRun)
	ctx := context.Background()

	wf, err := engine.StartWorkflow(ctx, "wf-tool-err-1", "Tool error task", 5, 5000)
	if err != nil {
		t.Fatalf("unexpected error starting workflow: %v", err)
	}

	if wf.Status != domain.StatusCompleted {
		t.Fatalf("expected COMPLETED, got %s", wf.Status)
	}
	if wf.Result != "Completed after tool error" {
		t.Fatalf("expected result 'Completed after tool error', got '%s'", wf.Result)
	}
}

func TestOrchestrator_TokenBudgetCircuitBreaker(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_token_budget.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	mockLLM := llm.NewMockDriver()
	// Step 1 consumes 300 tokens with budget 200
	mockLLM.RegisterStep(1, domain.StepPromptResponse{
		Thought:    "Consuming lots of tokens...",
		IsComplete: false,
		ToolCalls: []domain.ToolCall{
			{
				ID:        "tool-1",
				ToolName:  "echo",
				Arguments: "ok",
			},
		},
		TokensUsed: 300,
	})
	// Step 2 should not be executed because budget is exceeded
	mockLLM.RegisterStep(2, domain.StepPromptResponse{
		Thought:     "Step 2 should not run",
		IsComplete:  true,
		FinalResult: "Should not reach here",
		TokensUsed:  50,
	})

	runner := sandbox.NewProcessRunner(filepath.Join(tempDir, "scratch"))

	engine := orchestrator.NewEngine(store, mockLLM, runner)
	ctx := context.Background()

	wf, err := engine.StartWorkflow(ctx, "wf-token-breaker", "Token heavy task", 10, 200)
	if err != nil {
		t.Fatalf("unexpected error starting workflow: %v", err)
	}

	if wf.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED, got %s", wf.Status)
	}
	if wf.ErrorMessage != "Token budget exceeded" {
		t.Fatalf("expected error message 'Token budget exceeded', got '%s'", wf.ErrorMessage)
	}

	// Verify database record
	savedWf, err := store.GetWorkflow(ctx, "wf-token-breaker")
	if err != nil {
		t.Fatalf("failed to fetch workflow: %v", err)
	}
	if savedWf.Status != domain.StatusFailed {
		t.Fatalf("expected saved status FAILED, got %s", savedWf.Status)
	}

	events, err := store.GetEvents(ctx, "wf-token-breaker")
	if err != nil {
		t.Fatalf("failed to get events: %v", err)
	}
	lastEvent := events[len(events)-1]
	if lastEvent.EventType != domain.EventWorkflowFailed {
		t.Fatalf("expected last event WORKFLOW_FAILED, got %s", lastEvent.EventType)
	}
}

func TestOrchestrator_MaxStepsLimitReached(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_max_steps.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	mockLLM := llm.NewMockDriver()
	// 2 steps, neither completes
	mockLLM.RegisterStep(1, domain.StepPromptResponse{
		Thought:    "Step 1 not complete",
		IsComplete: false,
		TokensUsed: 10,
	})
	mockLLM.RegisterStep(2, domain.StepPromptResponse{
		Thought:    "Step 2 not complete",
		IsComplete: false,
		TokensUsed: 10,
	})

	runner := sandbox.NewProcessRunner(filepath.Join(tempDir, "scratch"))

	engine := orchestrator.NewEngine(store, mockLLM, runner)
	ctx := context.Background()

	// MaxSteps = 2
	wf, err := engine.StartWorkflow(ctx, "wf-max-steps", "Never ending task", 2, 5000)
	if err != nil {
		t.Fatalf("unexpected start error: %v", err)
	}

	if wf.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED, got %s", wf.Status)
	}
	if wf.ErrorMessage != "Max step limit reached without completion" {
		t.Fatalf("expected 'Max step limit reached without completion', got '%s'", wf.ErrorMessage)
	}
	if wf.CurrentStep != 2 {
		t.Fatalf("expected current step 2, got %d", wf.CurrentStep)
	}
}

func TestOrchestrator_ResumeWorkflowIntermediateState(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_resume.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	// Simulate crashed workflow at step 1
	wf := domain.NewWorkflow("wf-resume-1", "Crash recovery test", 5, 5000)
	wf.Status = domain.StatusRunning
	wf.CurrentStep = 1
	wf.TotalTokens = 100
	if err := store.CreateWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("failed to create workflow: %v", err)
	}

	mockLLM := llm.NewMockDriver()
	mockLLM.RegisterStep(2, domain.StepPromptResponse{
		Thought:     "Resumed at step 2, now completing.",
		IsComplete:  true,
		FinalResult: "Recovered and completed",
		TokensUsed:  150,
	})

	runner := sandbox.NewProcessRunner(filepath.Join(tempDir, "scratch"))
	engine := orchestrator.NewEngine(store, mockLLM, runner)
	ctx := context.Background()

	resumedWf, err := engine.ResumeWorkflow(ctx, "wf-resume-1")
	if err != nil {
		t.Fatalf("failed to resume workflow: %v", err)
	}

	if resumedWf.Status != domain.StatusCompleted {
		t.Fatalf("expected COMPLETED, got %s (error: %s)", resumedWf.Status, resumedWf.ErrorMessage)
	}
	if resumedWf.Result != "Recovered and completed" {
		t.Fatalf("expected result 'Recovered and completed', got '%s'", resumedWf.Result)
	}
	if resumedWf.CurrentStep != 2 {
		t.Fatalf("expected current step 2, got %d", resumedWf.CurrentStep)
	}
	if resumedWf.TotalTokens != 250 {
		t.Fatalf("expected total tokens 250 (100 + 150), got %d", resumedWf.TotalTokens)
	}
}

func TestOrchestrator_ResumeAlreadyFinishedWorkflow(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_already_finished.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	// 1. Completed workflow
	wfComp := domain.NewWorkflow("wf-finished-1", "Already finished test", 5, 5000)
	wfComp.Status = domain.StatusCompleted
	wfComp.Result = "Original Result"
	if err := store.CreateWorkflow(context.Background(), wfComp); err != nil {
		t.Fatalf("failed to create workflow: %v", err)
	}

	// 2. Failed workflow
	wfFail := domain.NewWorkflow("wf-failed-1", "Already failed test", 5, 5000)
	wfFail.Status = domain.StatusFailed
	wfFail.ErrorMessage = "Prior Failure Reason"
	if err := store.CreateWorkflow(context.Background(), wfFail); err != nil {
		t.Fatalf("failed to create workflow: %v", err)
	}

	mockLLM := llm.NewMockDriver()
	runner := sandbox.NewProcessRunner(filepath.Join(tempDir, "scratch"))
	engine := orchestrator.NewEngine(store, mockLLM, runner)
	ctx := context.Background()

	resumedWf, err := engine.ResumeWorkflow(ctx, "wf-finished-1")
	if err != nil {
		t.Fatalf("failed to resume workflow: %v", err)
	}
	if resumedWf.Status != domain.StatusCompleted {
		t.Fatalf("expected COMPLETED, got %s", resumedWf.Status)
	}
	if resumedWf.Result != "Original Result" {
		t.Fatalf("expected 'Original Result', got '%s'", resumedWf.Result)
	}

	resumedFailedWf, err := engine.ResumeWorkflow(ctx, "wf-failed-1")
	if err != nil {
		t.Fatalf("failed to resume failed workflow: %v", err)
	}
	if resumedFailedWf.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED, got %s", resumedFailedWf.Status)
	}
}

func TestOrchestrator_WorkflowNotFound(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_not_found.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	mockLLM := llm.NewMockDriver()
	runner := sandbox.NewProcessRunner(filepath.Join(tempDir, "scratch"))
	engine := orchestrator.NewEngine(store, mockLLM, runner)
	ctx := context.Background()

	_, err = engine.ResumeWorkflow(ctx, "nonexistent-workflow-id")
	if err == nil {
		t.Fatalf("expected error for nonexistent workflow, got nil")
	}
}

func TestOrchestrator_LLMGenerationError(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_llm_error.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	errDrv := &errDriver{}
	runner := sandbox.NewProcessRunner(filepath.Join(tempDir, "scratch"))
	engine := orchestrator.NewEngine(store, errDrv, runner)
	ctx := context.Background()

	wf, err := engine.StartWorkflow(ctx, "wf-error-1", "Error prone task", 5, 5000)
	if err != nil {
		t.Fatalf("unexpected error starting workflow: %v", err)
	}

	if wf.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED, got %s", wf.Status)
	}
	if wf.ErrorMessage == "" {
		t.Fatalf("expected non-empty ErrorMessage")
	}

	events, err := store.GetEvents(ctx, "wf-error-1")
	if err != nil {
		t.Fatalf("failed to get events: %v", err)
	}
	lastEvent := events[len(events)-1]
	if lastEvent.EventType != domain.EventWorkflowFailed {
		t.Fatalf("expected last event WORKFLOW_FAILED, got %s", lastEvent.EventType)
	}
}
