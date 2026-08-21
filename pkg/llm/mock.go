package llm

import (
	"context"
	"fmt"
	"sync"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

// MockDriver provides deterministic, pre-scripted LLM step responses for offline testing and verification.
type MockDriver struct {
	mu    sync.RWMutex
	steps map[int]domain.StepPromptResponse
}

// NewMockDriver creates a new empty MockDriver.
func NewMockDriver() *MockDriver {
	return &MockDriver{
		steps: make(map[int]domain.StepPromptResponse),
	}
}

// RegisterStep records a predefined StepPromptResponse for a specific step number.
func (m *MockDriver) RegisterStep(stepNumber int, resp domain.StepPromptResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps[stepNumber] = resp
}

// GenerateStep returns the pre-registered StepPromptResponse for req.StepNumber or a sensible default fallback.
func (m *MockDriver) GenerateStep(ctx context.Context, req domain.StepPromptRequest) (*domain.StepPromptResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	resp, ok := m.steps[req.StepNumber]
	if !ok {
		// Default completion fallback response for unscripted steps
		return &domain.StepPromptResponse{
			Thought:     fmt.Sprintf("Auto-generated default response for step %d", req.StepNumber),
			IsComplete:  true,
			FinalResult: "Default task completed",
			TokensUsed:  50,
		}, nil
	}

	// Return a copy of the registered response to prevent caller mutation of stored state
	respCopy := resp
	if resp.ToolCalls != nil {
		respCopy.ToolCalls = make([]domain.ToolCall, len(resp.ToolCalls))
		copy(respCopy.ToolCalls, resp.ToolCalls)
	}
	return &respCopy, nil
}
