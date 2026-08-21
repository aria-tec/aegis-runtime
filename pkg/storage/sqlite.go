package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect sqlite: %w", err)
	}

	// Enable WAL mode and foreign keys
	if _, err := db.Exec("PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set pragmas: %w", err)
	}

	// Auto-run embedded migrations
	schema, err := MigrationsFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to read migrations: %w", err)
	}

	if _, err := db.Exec(string(schema)); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to execute migrations: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) CreateWorkflow(ctx context.Context, wf *domain.Workflow) error {
	now := time.Now().UTC()
	if wf.CreatedAt.IsZero() {
		wf.CreatedAt = now
	}
	if wf.UpdatedAt.IsZero() {
		wf.UpdatedAt = now
	}

	query := `INSERT INTO workflows (id, goal, status, current_step, total_tokens_used, max_steps, token_budget, result, error_message, created_at, updated_at) 
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query,
		wf.ID,
		wf.Goal,
		string(wf.Status),
		wf.CurrentStep,
		wf.TotalTokens,
		wf.MaxSteps,
		wf.TokenBudget,
		wf.Result,
		wf.ErrorMessage,
		wf.CreatedAt,
		wf.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert workflow: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateWorkflow(ctx context.Context, wf *domain.Workflow) error {
	wf.UpdatedAt = time.Now().UTC()
	query := `UPDATE workflows SET status = ?, current_step = ?, total_tokens_used = ?, max_steps = ?, token_budget = ?, result = ?, error_message = ?, updated_at = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, query,
		string(wf.Status),
		wf.CurrentStep,
		wf.TotalTokens,
		wf.MaxSteps,
		wf.TokenBudget,
		wf.Result,
		wf.ErrorMessage,
		wf.UpdatedAt,
		wf.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update workflow: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("workflow not found: %s", wf.ID)
	}
	return nil
}

func (s *SQLiteStore) GetWorkflow(ctx context.Context, id string) (*domain.Workflow, error) {
	query := `SELECT id, goal, status, current_step, total_tokens_used, max_steps, token_budget, COALESCE(result, ''), COALESCE(error_message, ''), created_at, updated_at FROM workflows WHERE id = ?`
	row := s.db.QueryRowContext(ctx, query, id)

	var wf domain.Workflow
	var statusStr string
	if err := row.Scan(
		&wf.ID,
		&wf.Goal,
		&statusStr,
		&wf.CurrentStep,
		&wf.TotalTokens,
		&wf.MaxSteps,
		&wf.TokenBudget,
		&wf.Result,
		&wf.ErrorMessage,
		&wf.CreatedAt,
		&wf.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("workflow not found: %s", id)
		}
		return nil, fmt.Errorf("failed to scan workflow: %w", err)
	}
	wf.Status = domain.WorkflowStatus(statusStr)
	return &wf, nil
}

func (s *SQLiteStore) AppendEvent(ctx context.Context, evt *domain.WorkflowEvent) error {
	if evt.CreatedAt.IsZero() {
		evt.CreatedAt = time.Now().UTC()
	}
	query := `INSERT INTO workflow_events (id, workflow_id, step_number, event_type, payload_json, tokens_used, duration_ms, created_at) 
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query,
		evt.ID,
		evt.WorkflowID,
		evt.StepNumber,
		string(evt.EventType),
		evt.PayloadJSON,
		evt.TokensUsed,
		evt.DurationMs,
		evt.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to append event: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetEvents(ctx context.Context, workflowID string) ([]domain.WorkflowEvent, error) {
	query := `SELECT id, workflow_id, step_number, event_type, payload_json, tokens_used, duration_ms, created_at FROM workflow_events WHERE workflow_id = ? ORDER BY step_number ASC, created_at ASC`
	rows, err := s.db.QueryContext(ctx, query, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to query events: %w", err)
	}
	defer rows.Close()

	events := make([]domain.WorkflowEvent, 0)
	for rows.Next() {
		var e domain.WorkflowEvent
		var typeStr string
		if err := rows.Scan(
			&e.ID,
			&e.WorkflowID,
			&e.StepNumber,
			&typeStr,
			&e.PayloadJSON,
			&e.TokensUsed,
			&e.DurationMs,
			&e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}
		e.EventType = domain.EventType(typeStr)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating events: %w", err)
	}
	return events, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
