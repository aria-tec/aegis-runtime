package storage

import (
	"context"
	"embed"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

//go:embed migrations/*.sql
var MigrationsFS embed.FS

type Store interface {
	CreateWorkflow(ctx context.Context, wf *domain.Workflow) error
	UpdateWorkflow(ctx context.Context, wf *domain.Workflow) error
	GetWorkflow(ctx context.Context, id string) (*domain.Workflow, error)
	AppendEvent(ctx context.Context, evt *domain.WorkflowEvent) error
	GetEvents(ctx context.Context, workflowID string) ([]domain.WorkflowEvent, error)
	Close() error
}
