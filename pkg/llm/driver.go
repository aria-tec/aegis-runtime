package llm

import (
	"context"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

// Driver is the core abstraction for LLM inference providers.
type Driver interface {
	GenerateStep(ctx context.Context, req domain.StepPromptRequest) (*domain.StepPromptResponse, error)
}
