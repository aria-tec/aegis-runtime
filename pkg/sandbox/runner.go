package sandbox

import (
	"context"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

type Runner interface {
	Execute(ctx context.Context, req domain.ToolExecutionRequest) (*domain.ToolExecutionResult, error)
}
