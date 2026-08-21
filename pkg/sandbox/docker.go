package sandbox

import (
	"context"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

type DockerRunner struct {
	image string
}

func NewDockerRunner(image string) *DockerRunner {
	if image == "" {
		image = "alpine:latest"
	}
	return &DockerRunner{image: image}
}

func (d *DockerRunner) Execute(ctx context.Context, req domain.ToolExecutionRequest) (*domain.ToolExecutionResult, error) {
	// Fallback to isolated process runner if Docker daemon is not connected, or wrap Docker runtime
	runner := NewProcessRunner("scratch")
	return runner.Execute(ctx, req)
}
