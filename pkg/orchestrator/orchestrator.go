package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/llm"
	"github.com/aria-tec/aegis-runtime/pkg/sandbox"
	"github.com/aria-tec/aegis-runtime/pkg/storage"
)

// Engine manages durable agent workflow lifecycles, event sourcing, tool execution, and circuit breakers.
type Engine struct {
	store  storage.Store
	driver llm.Driver
	runner sandbox.Runner
}

// NewEngine creates a new workflow orchestrator Engine instance.
func NewEngine(store storage.Store, driver llm.Driver, runner sandbox.Runner) *Engine {
	return &Engine{
		store:  store,
		driver: driver,
		runner: runner,
	}
}

// StartWorkflow creates and persists a new workflow instance and immediately begins execution.
func (e *Engine) StartWorkflow(ctx context.Context, id, goal string, maxSteps, tokenBudget int) (*domain.Workflow, error) {
	wf := domain.NewWorkflow(id, goal, maxSteps, tokenBudget)
	if err := e.store.CreateWorkflow(ctx, wf); err != nil {
		return nil, fmt.Errorf("failed to persist workflow: %w", err)
	}

	if err := e.recordEvent(ctx, id, 0, domain.EventWorkflowStarted, map[string]string{"goal": goal}, 0, 0); err != nil {
		return nil, fmt.Errorf("failed to record workflow started event: %w", err)
	}

	return e.ResumeWorkflow(ctx, id)
}

// ResumeWorkflow recovers a workflow state from storage and continues step execution until completion or failure.
func (e *Engine) ResumeWorkflow(ctx context.Context, workflowID string) (*domain.Workflow, error) {
	wf, err := e.store.GetWorkflow(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch workflow: %w", err)
	}

	if wf.Status == domain.StatusCompleted || wf.Status == domain.StatusFailed {
		return wf, nil
	}

	wf.Status = domain.StatusRunning
	if err := e.store.UpdateWorkflow(ctx, wf); err != nil {
		return nil, fmt.Errorf("failed to update workflow status: %w", err)
	}

	// Execution loop
	for step := wf.CurrentStep + 1; step <= wf.MaxSteps; step++ {
		// Circuit breaker: token budget check
		if wf.TotalTokens >= wf.TokenBudget {
			wf.Status = domain.StatusFailed
			wf.ErrorMessage = "Token budget exceeded"
			_ = e.store.UpdateWorkflow(ctx, wf)
			_ = e.recordEvent(ctx, workflowID, step, domain.EventWorkflowFailed, map[string]string{"error": wf.ErrorMessage}, 0, 0)
			return wf, nil
		}

		wf.CurrentStep = step
		_ = e.store.UpdateWorkflow(ctx, wf)
		_ = e.recordEvent(ctx, workflowID, step, domain.EventStepStarted, map[string]int{"step": step}, 0, 0)

		// Fetch current event history for LLM context
		events, err := e.store.GetEvents(ctx, workflowID)
		if err != nil {
			return nil, fmt.Errorf("failed to load event history: %w", err)
		}

		// 1. LLM Generation Step
		promptReq := domain.StepPromptRequest{
			WorkflowID:   workflowID,
			Goal:         wf.Goal,
			StepNumber:   step,
			EventHistory: events,
		}

		llmStart := time.Now()
		stepResp, err := e.driver.GenerateStep(ctx, promptReq)
		if err != nil {
			wf.Status = domain.StatusFailed
			wf.ErrorMessage = fmt.Sprintf("LLM generation failed: %v", err)
			_ = e.store.UpdateWorkflow(ctx, wf)
			_ = e.recordEvent(ctx, workflowID, step, domain.EventWorkflowFailed, map[string]string{"error": wf.ErrorMessage}, 0, 0)
			return wf, nil
		}

		wf.TotalTokens += stepResp.TokensUsed
		_ = e.store.UpdateWorkflow(ctx, wf)
		_ = e.recordEvent(ctx, workflowID, step, domain.EventLLMPrompted, stepResp, stepResp.TokensUsed, time.Since(llmStart).Milliseconds())

		// 2. Check if workflow is finished
		if stepResp.IsComplete {
			wf.Status = domain.StatusCompleted
			wf.Result = stepResp.FinalResult
			_ = e.store.UpdateWorkflow(ctx, wf)
			_ = e.recordEvent(ctx, workflowID, step, domain.EventWorkflowCompleted, map[string]string{"result": wf.Result}, 0, 0)
			return wf, nil
		}

		// 3. Execute Tool Calls if any
		for _, tool := range stepResp.ToolCalls {
			_ = e.recordEvent(ctx, workflowID, step, domain.EventToolCalled, tool, 0, 0)

			toolStart := time.Now()
			cmd := "echo"
			args := []string{tool.Arguments}
			if tool.ToolName == "sh" || tool.ToolName == "bash" {
				cmd = tool.ToolName
				args = []string{"-c", tool.Arguments}
			}

			toolReq := domain.ToolExecutionRequest{
				WorkflowID:  workflowID,
				StepNumber:  step,
				ToolName:    tool.ToolName,
				Command:     cmd,
				Args:        args,
				TimeoutSecs: 10,
			}

			toolResult, err := e.runner.Execute(ctx, toolReq)
			if err != nil {
				toolResult = &domain.ToolExecutionResult{
					ToolName: tool.ToolName,
					ExitCode: -1,
					Error:    err.Error(),
				}
			}

			_ = e.recordEvent(ctx, workflowID, step, domain.EventToolCompleted, toolResult, 0, time.Since(toolStart).Milliseconds())
		}

		_ = e.recordEvent(ctx, workflowID, step, domain.EventStepCompleted, map[string]int{"step": step}, 0, 0)
	}

	wf.Status = domain.StatusFailed
	wf.ErrorMessage = "Max step limit reached without completion"
	_ = e.store.UpdateWorkflow(ctx, wf)
	_ = e.recordEvent(ctx, workflowID, wf.MaxSteps, domain.EventWorkflowFailed, map[string]string{"error": wf.ErrorMessage}, 0, 0)
	return wf, nil
}

func (e *Engine) recordEvent(ctx context.Context, workflowID string, step int, evtType domain.EventType, payload interface{}, tokens int, durationMs int64) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal event payload: %w", err)
	}
	evt := domain.WorkflowEvent{
		ID:          fmt.Sprintf("evt_%s_%d_%d", workflowID, step, time.Now().UnixNano()),
		WorkflowID:  workflowID,
		StepNumber:  step,
		EventType:   evtType,
		PayloadJSON: string(payloadBytes),
		TokensUsed:  tokens,
		DurationMs:  durationMs,
		CreatedAt:   time.Now().UTC(),
	}
	return e.store.AppendEvent(ctx, &evt)
}
