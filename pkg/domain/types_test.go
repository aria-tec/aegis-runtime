package domain_test

import (
	"testing"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

func TestNewWorkflow(t *testing.T) {
	t.Run("creates workflow with specified parameters", func(t *testing.T) {
		wf := domain.NewWorkflow("wf-1", "Automate Inventory", 10, 8000)
		if wf.ID != "wf-1" {
			t.Fatalf("expected id wf-1, got %s", wf.ID)
		}
		if wf.Goal != "Automate Inventory" {
			t.Fatalf("expected goal 'Automate Inventory', got '%s'", wf.Goal)
		}
		if wf.Status != domain.StatusPending {
			t.Fatalf("expected status PENDING, got %s", wf.Status)
		}
		if wf.CurrentStep != 0 {
			t.Fatalf("expected current_step 0, got %d", wf.CurrentStep)
		}
		if wf.MaxSteps != 10 {
			t.Fatalf("expected max_steps 10, got %d", wf.MaxSteps)
		}
		if wf.TokenBudget != 8000 {
			t.Fatalf("expected token_budget 8000, got %d", wf.TokenBudget)
		}
		if wf.CreatedAt.IsZero() || wf.UpdatedAt.IsZero() {
			t.Fatalf("expected non-zero timestamps: created=%v, updated=%v", wf.CreatedAt, wf.UpdatedAt)
		}
	})

	t.Run("uses default values for non-positive limits", func(t *testing.T) {
		wf := domain.NewWorkflow("wf-default", "Default limits", 0, -5)
		if wf.MaxSteps != 10 {
			t.Fatalf("expected default max_steps 10, got %d", wf.MaxSteps)
		}
		if wf.TokenBudget != 8000 {
			t.Fatalf("expected default token_budget 8000, got %d", wf.TokenBudget)
		}
	})
}

func TestDomainTypesInitialization(t *testing.T) {
	t.Run("ToolExecutionRequest and Result", func(t *testing.T) {
		req := domain.ToolExecutionRequest{
			WorkflowID:  "wf-1",
			StepNumber:  1,
			ToolName:    "bash",
			Command:     "ls",
			Args:        []string{"-la"},
			Stdin:       "",
			Env:         map[string]string{"FOO": "BAR"},
			TimeoutSecs: 30,
		}
		if req.ToolName != "bash" || req.TimeoutSecs != 30 {
			t.Fatalf("unexpected request: %+v", req)
		}

		res := domain.ToolExecutionResult{
			ToolName: "bash",
			ExitCode: 0,
			Stdout:   "total 0",
			Stderr:   "",
			Duration: 50 * time.Millisecond,
		}
		if res.ExitCode != 0 || res.Stdout != "total 0" {
			t.Fatalf("unexpected result: %+v", res)
		}
	})

	t.Run("StepPromptRequest and Response", func(t *testing.T) {
		evt := domain.WorkflowEvent{
			ID:          "evt-1",
			WorkflowID:  "wf-1",
			StepNumber:  1,
			EventType:   domain.EventStepStarted,
			PayloadJSON: `{"step": 1}`,
			TokensUsed:  100,
			DurationMs:  50,
			CreatedAt:   time.Now().UTC(),
		}

		req := domain.StepPromptRequest{
			WorkflowID:   "wf-1",
			Goal:         "Test Goal",
			StepNumber:   1,
			EventHistory: []domain.WorkflowEvent{evt},
			AllowedTools: []string{"bash"},
		}
		if len(req.EventHistory) != 1 || req.AllowedTools[0] != "bash" {
			t.Fatalf("unexpected prompt request: %+v", req)
		}

		resp := domain.StepPromptResponse{
			Thought:    "Executing tool",
			IsComplete: false,
			ToolCalls: []domain.ToolCall{
				{
					ID:        "tc-1",
					ToolName:  "bash",
					Arguments: `{"command":"ls"}`,
				},
			},
			TokensUsed: 120,
		}
		if resp.IsComplete || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ToolName != "bash" {
			t.Fatalf("unexpected prompt response: %+v", resp)
		}
	})

	t.Run("WorkflowStatus and EventType constants", func(t *testing.T) {
		statuses := []domain.WorkflowStatus{
			domain.StatusPending,
			domain.StatusRunning,
			domain.StatusStepExecuting,
			domain.StatusToolExecuting,
			domain.StatusCompleted,
			domain.StatusFailed,
			domain.StatusPaused,
		}
		for _, s := range statuses {
			if string(s) == "" {
				t.Fatalf("status constant should not be empty")
			}
		}

		events := []domain.EventType{
			domain.EventWorkflowStarted,
			domain.EventStepStarted,
			domain.EventLLMPrompted,
			domain.EventToolCalled,
			domain.EventToolCompleted,
			domain.EventStepCompleted,
			domain.EventWorkflowCompleted,
			domain.EventWorkflowFailed,
		}
		for _, e := range events {
			if string(e) == "" {
				t.Fatalf("event type constant should not be empty")
			}
		}
	})
}
