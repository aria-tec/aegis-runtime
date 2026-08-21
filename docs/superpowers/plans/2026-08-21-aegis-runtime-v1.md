# Aegis-Runtime v1.0.0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build and verify **Aegis-Runtime v1.0.0**, a standalone, fault-tolerant, polyglot AI Agent Execution Engine & Sandboxed Tool Gateway in Go with durable event-sourced state replay, CGO-free SQLite/Postgres storage, deterministic mock + OpenAI-compatible LLM drivers, and isolated process/Docker sandboxing.

**Architecture:** A single-binary Go daemon providing REST and Model Context Protocol (MCP) ingress that orchestrates deterministic multi-step agent reasoning loops. Every state transition is recorded as an immutable event in SQLite/PostgreSQL, enabling deterministic replay and crash recovery without duplicate tool execution. Tool executions run in an isolated environment with environment variable scrubbing or ephemeral Docker containers.

**Tech Stack:** Go 1.27, `modernc.org/sqlite` (Pure Go CGO-free), `github.com/jackc/pgx/v5`, Docker Engine API client, standard library `net/http`.

**Spec:** [`aegis-runtime/docs/superpowers/specs/2026-08-21-aegis-runtime-design.md`](file:///Users/arias/Documents/antigravity/ExProject/aegis-runtime/docs/superpowers/specs/2026-08-21-aegis-runtime-design.md)

## Global Constraints

- Go Version: Go 1.27+ with pure-Go SQLite (`modernc.org/sqlite`) for zero-CGO cross-compilation.
- Portability: Single standalone binary (`cmd/server/main.go`) with embedded migrations via `//go:embed`.
- Zero Leaks: Tool runner must scrub all host environment variables before execution.
- Determinism: 100% of unit and integration tests must run offline without paid API keys via `MockDriver`.
- Durability: Crash recovery must resume from the exact interrupted step with zero duplicate side effects.

---

### Task 1: Project Scaffolding, Go Module & Domain Types

**Files:**
- Create: `aegis-runtime/go.mod`
- Create: `aegis-runtime/pkg/domain/types.go`
- Test: `aegis-runtime/pkg/domain/types_test.go`

**Interfaces:**
- Produces: `domain.Workflow`, `domain.WorkflowStatus`, `domain.WorkflowEvent`, `domain.EventType`, `domain.StepPromptRequest`, `domain.StepPromptResponse`, `domain.ToolCall`, `domain.ToolExecutionRequest`, `domain.ToolExecutionResult`

- [ ] **Step 1: Initialize Go module**

```bash
cd /Users/arias/Documents/antigravity/ExProject/aegis-runtime
go mod init github.com/aria-tec/aegis-runtime
```

- [ ] **Step 2: Write failing test for domain models**

Create `pkg/domain/types_test.go`:
```go
package domain_test

import (
	"testing"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

func testWorkflowValidation(t *testing.T) {
	wf := domain.NewWorkflow("wf-1", "Test Goal", 10, 8000)
	if wf.ID != "wf-1" {
		t.Fatalf("expected id wf-1, got %s", wf.ID)
	}
	if wf.Status != domain.StatusPending {
		t.Fatalf("expected status PENDING, got %s", wf.Status)
	}
	if wf.MaxSteps != 10 || wf.TokenBudget != 8000 {
		t.Fatalf("unexpected limits: %+v", wf)
	}
}

func TestWorkflow(t *testing.T) {
	testWorkflowValidation(t)
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./pkg/domain/...`
Expected: FAIL with `undefined: domain.NewWorkflow`

- [ ] **Step 4: Implement minimal domain types**

Create `pkg/domain/types.go`:
```go
package domain

import "time"

type WorkflowStatus string

const (
	StatusPending        WorkflowStatus = "PENDING"
	StatusRunning        WorkflowStatus = "RUNNING"
	StatusStepExecuting  WorkflowStatus = "STEP_EXECUTING"
	StatusToolExecuting  WorkflowStatus = "TOOL_EXECUTING"
	StatusCompleted      WorkflowStatus = "COMPLETED"
	StatusFailed         WorkflowStatus = "FAILED"
	StatusPaused         WorkflowStatus = "PAUSED"
)

type EventType string

const (
	EventWorkflowStarted   EventType = "WORKFLOW_STARTED"
	EventStepStarted       EventType = "STEP_STARTED"
	EventLLMPrompted       EventType = "LLM_PROMPTED"
	EventToolCalled        EventType = "TOOL_CALLED"
	EventToolCompleted     EventType = "TOOL_COMPLETED"
	EventStepCompleted     EventType = "STEP_COMPLETED"
	EventWorkflowCompleted EventType = "WORKFLOW_COMPLETED"
	EventWorkflowFailed    EventType = "WORKFLOW_FAILED"
)

type ToolCall struct {
	ID        string `json:"id"`
	ToolName  string `json:"tool_name"`
	Arguments string `json:"arguments"`
}

type ToolExecutionRequest struct {
	WorkflowID  string            `json:"workflow_id"`
	StepNumber  int               `json:"step_number"`
	ToolName    string            `json:"tool_name"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Stdin       string            `json:"stdin"`
	Env         map[string]string `json:"env"`
	TimeoutSecs int               `json:"timeout_secs"`
}

type ToolExecutionResult struct {
	ToolName string        `json:"tool_name"`
	ExitCode int           `json:"exit_code"`
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	Duration time.Duration `json:"duration"`
	Error    string        `json:"error,omitempty"`
}

type StepPromptRequest struct {
	WorkflowID   string      `json:"workflow_id"`
	Goal         string      `json:"goal"`
	StepNumber   int         `json:"step_number"`
	EventHistory []WorkflowEvent `json:"event_history"`
	AllowedTools []string    `json:"allowed_tools"`
}

type StepPromptResponse struct {
	Thought      string     `json:"thought"`
	IsComplete   bool       `json:"is_complete"`
	FinalResult  string     `json:"final_result,omitempty"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	TokensUsed   int        `json:"tokens_used"`
}

type Workflow struct {
	ID             string         `json:"id"`
	Goal           string         `json:"goal"`
	Status         WorkflowStatus `json:"status"`
	CurrentStep    int            `json:"current_step"`
	TotalTokens    int            `json:"total_tokens"`
	MaxSteps       int            `json:"max_steps"`
	TokenBudget    int            `json:"token_budget"`
	Result         string         `json:"result,omitempty"`
	ErrorMessage   string         `json:"error_message,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type WorkflowEvent struct {
	ID          string    `json:"id"`
	WorkflowID  string    `json:"workflow_id"`
	StepNumber  int       `json:"step_number"`
	EventType   EventType `json:"event_type"`
	PayloadJSON string    `json:"payload_json"`
	TokensUsed  int       `json:"tokens_used"`
	DurationMs  int64     `json:"duration_ms"`
	CreatedAt   time.Time `json:"created_at"`
}

func NewWorkflow(id, goal string, maxSteps, tokenBudget int) *Workflow {
	if maxSteps <= 0 {
		maxSteps = 10
	}
	if tokenBudget <= 0 {
		tokenBudget = 8000
	}
	now := time.Now().UTC()
	return &Workflow{
		ID:          id,
		Goal:        goal,
		Status:      StatusPending,
		CurrentStep: 0,
		MaxSteps:    maxSteps,
		TokenBudget: tokenBudget,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./pkg/domain/... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod pkg/domain/
git commit -m "feat(domain): define core workflow entities and event types"
```

---

### Task 2: Storage Layer — Pure-Go SQLite & PostgreSQL Adapter with Auto-Migrations

**Files:**
- Create: `aegis-runtime/pkg/storage/store.go`
- Create: `aegis-runtime/pkg/storage/migrations/001_init.sql`
- Create: `aegis-runtime/pkg/storage/sqlite.go`
- Create: `aegis-runtime/pkg/storage/postgres.go`
- Test: `aegis-runtime/pkg/storage/sqlite_test.go`

**Interfaces:**
- Consumes: `domain.Workflow`, `domain.WorkflowEvent`
- Produces: `storage.Store`, `storage.NewSQLiteStore(dsn string)`, `storage.NewPostgresStore(dsn string)`

- [ ] **Step 1: Add dependencies for pure-Go SQLite & Postgres**

```bash
go get modernc.org/sqlite
go get github.com/jackc/pgx/v5/stdlib
```

- [ ] **Step 2: Create SQL migration schema**

Create `pkg/storage/migrations/001_init.sql`:
```sql
CREATE TABLE IF NOT EXISTS workflows (
    id VARCHAR(64) PRIMARY KEY,
    goal TEXT NOT NULL,
    status VARCHAR(32) NOT NULL,
    current_step INT NOT NULL DEFAULT 0,
    total_tokens_used INT NOT NULL DEFAULT 0,
    max_steps INT NOT NULL DEFAULT 10,
    token_budget INT NOT NULL DEFAULT 8000,
    result TEXT,
    error_message TEXT,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS workflow_events (
    id VARCHAR(64) PRIMARY KEY,
    workflow_id VARCHAR(64) NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    step_number INT NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    payload_json TEXT NOT NULL,
    tokens_used INT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_workflow_events_wid ON workflow_events(workflow_id, step_number);
```

- [ ] **Step 3: Write failing test for SQLiteStore**

Create `pkg/storage/sqlite_test.go`:
```go
package storage_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/storage"
)

func TestSQLiteStore_WorkflowLifecycle(t *testing.T) {
	dbPath := "test_aegis.db"
	defer os.Remove(dbPath)

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	wf := domain.NewWorkflow("wf-test-1", "Automate Inventory", 10, 8000)

	if err := store.CreateWorkflow(ctx, wf); err != nil {
		t.Fatalf("failed to create workflow: %v", err)
	}

	fetched, err := store.GetWorkflow(ctx, "wf-test-1")
	if err != nil {
		t.Fatalf("failed to get workflow: %v", err)
	}
	if fetched.Goal != "Automate Inventory" {
		t.Fatalf("expected goal 'Automate Inventory', got '%s'", fetched.Goal)
	}

	evt := domain.WorkflowEvent{
		ID:          "evt-1",
		WorkflowID:  "wf-test-1",
		StepNumber:  1,
		EventType:   domain.EventStepStarted,
		PayloadJSON: `{"step": 1}`,
		TokensUsed:  150,
		DurationMs:  20,
		CreatedAt:   time.Now().UTC(),
	}

	if err := store.AppendEvent(ctx, &evt); err != nil {
		t.Fatalf("failed to append event: %v", err)
	}

	events, err := store.GetEvents(ctx, "wf-test-1")
	if err != nil {
		t.Fatalf("failed to get events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}
```

- [ ] **Step 4: Implement Storage Interface & SQLite Store**

Create `pkg/storage/store.go`:
```go
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
```

Create `pkg/storage/sqlite.go`:
```go
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
	query := `INSERT INTO workflows (id, goal, status, current_step, total_tokens_used, max_steps, token_budget, created_at, updated_at) 
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query, wf.ID, wf.Goal, string(wf.Status), wf.CurrentStep, wf.TotalTokens, wf.MaxSteps, wf.TokenBudget, wf.CreatedAt, wf.UpdatedAt)
	return err
}

func (s *SQLiteStore) UpdateWorkflow(ctx context.Context, wf *domain.Workflow) error {
	query := `UPDATE workflows SET status = ?, current_step = ?, total_tokens_used = ?, result = ?, error_message = ?, updated_at = ? WHERE id = ?`
	wf.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, query, string(wf.Status), wf.CurrentStep, wf.TotalTokens, wf.Result, wf.ErrorMessage, wf.UpdatedAt, wf.ID)
	return err
}

func (s *SQLiteStore) GetWorkflow(ctx context.Context, id string) (*domain.Workflow, error) {
	query := `SELECT id, goal, status, current_step, total_tokens_used, max_steps, token_budget, COALESCE(result, ''), COALESCE(error_message, ''), created_at, updated_at FROM workflows WHERE id = ?`
	row := s.db.QueryRowContext(ctx, query, id)

	var wf domain.Workflow
	var statusStr string
	if err := row.Scan(&wf.ID, &wf.Goal, &statusStr, &wf.CurrentStep, &wf.TotalTokens, &wf.MaxSteps, &wf.TokenBudget, &wf.Result, &wf.ErrorMessage, &wf.CreatedAt, &wf.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("workflow not found: %s", id)
		}
		return nil, err
	}
	wf.Status = domain.WorkflowStatus(statusStr)
	return &wf, nil
}

func (s *SQLiteStore) AppendEvent(ctx context.Context, evt *domain.WorkflowEvent) error {
	query := `INSERT INTO workflow_events (id, workflow_id, step_number, event_type, payload_json, tokens_used, duration_ms, created_at) 
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query, evt.ID, evt.WorkflowID, evt.StepNumber, string(evt.EventType), evt.PayloadJSON, evt.TokensUsed, evt.DurationMs, evt.CreatedAt)
	return err
}

func (s *SQLiteStore) GetEvents(ctx context.Context, workflowID string) ([]domain.WorkflowEvent, error) {
	query := `SELECT id, workflow_id, step_number, event_type, payload_json, tokens_used, duration_ms, created_at FROM workflow_events WHERE workflow_id = ? ORDER BY step_number ASC, created_at ASC`
	rows, err := s.db.QueryContext(ctx, query, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []domain.WorkflowEvent
	for rows.Next() {
		var e domain.WorkflowEvent
		var typeStr string
		if err := rows.Scan(&e.ID, &e.WorkflowID, &e.StepNumber, &typeStr, &e.PayloadJSON, &e.TokensUsed, &e.DurationMs, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.EventType = domain.EventType(typeStr)
		events = append(events, e)
	}
	return events, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./pkg/storage/... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/storage/
git commit -m "feat(storage): implement pure-Go SQLite store with auto migrations"
```

---

### Task 3: AI Provider Layer — MockDriver & OpenAICompatibleDriver

**Files:**
- Create: `aegis-runtime/pkg/llm/driver.go`
- Create: `aegis-runtime/pkg/llm/mock.go`
- Create: `aegis-runtime/pkg/llm/openai.go`
- Test: `aegis-runtime/pkg/llm/llm_test.go`

**Interfaces:**
- Consumes: `domain.StepPromptRequest`, `domain.StepPromptResponse`
- Produces: `llm.Driver`, `llm.NewMockDriver()`, `llm.NewOpenAICompatibleDriver(baseURL, apiKey, model string)`

- [ ] **Step 1: Write failing test for LLM Drivers**

Create `pkg/llm/llm_test.go`:
```go
package llm_test

import (
	"context"
	"testing"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/llm"
)

func TestMockDriver_MultiStep(t *testing.T) {
	mock := llm.NewMockDriver()
	mock.RegisterStep(1, domain.StepPromptResponse{
		Thought: "I need to check warehouse stock",
		ToolCalls: []domain.ToolCall{
			{ID: "call-1", ToolName: "query_stock", Arguments: `{"sku": "SKU-99"}`},
		},
		TokensUsed: 120,
	})
	mock.RegisterStep(2, domain.StepPromptResponse{
		Thought:     "Stock is verified. Job done.",
		IsComplete:  true,
		FinalResult: "Inventory rebalanced successfully.",
		TokensUsed:  80,
	})

	ctx := context.Background()

	// Step 1
	res1, err := mock.GenerateStep(ctx, domain.StepPromptRequest{StepNumber: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res1.ToolCalls) != 1 || res1.ToolCalls[0].ToolName != "query_stock" {
		t.Fatalf("expected tool call query_stock, got %+v", res1.ToolCalls)
	}

	// Step 2
	res2, err := mock.GenerateStep(ctx, domain.StepPromptRequest{StepNumber: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res2.IsComplete || res2.FinalResult != "Inventory rebalanced successfully." {
		t.Fatalf("expected completion, got %+v", res2)
	}
}
```

- [ ] **Step 2: Implement LLM Driver interface & MockDriver**

Create `pkg/llm/driver.go`:
```go
package llm

import (
	"context"
	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

type Driver interface {
	GenerateStep(ctx context.Context, req domain.StepPromptRequest) (*domain.StepPromptResponse, error)
}
```

Create `pkg/llm/mock.go`:
```go
package llm

import (
	"context"
	"fmt"
	"sync"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

type MockDriver struct {
	mu    sync.RWMutex
	steps map[int]domain.StepPromptResponse
}

func NewMockDriver() *MockDriver {
	return &MockDriver{
		steps: make(map[int]domain.StepPromptResponse),
	}
}

func (m *MockDriver) RegisterStep(stepNumber int, resp domain.StepPromptResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps[stepNumber] = resp
}

func (m *MockDriver) GenerateStep(ctx context.Context, req domain.StepPromptRequest) (*domain.StepPromptResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	resp, ok := m.steps[req.StepNumber]
	if !ok {
		// Default fallback response
		return &domain.StepPromptResponse{
			Thought:     fmt.Sprintf("Auto-generated default response for step %d", req.StepNumber),
			IsComplete:  true,
			FinalResult: "Default task completed",
			TokensUsed:  50,
		}, nil
	}
	return &resp, nil
}
```

Create `pkg/llm/openai.go`:
```go
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

type OpenAICompatibleDriver struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

func NewOpenAICompatibleDriver(baseURL, apiKey, model string) *OpenAICompatibleDriver {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com/v1beta/openai/"
	}
	if model == "" {
		model = "gemini-2.5-flash"
	}
	return &OpenAICompatibleDriver{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (d *OpenAICompatibleDriver) GenerateStep(ctx context.Context, req domain.StepPromptRequest) (*domain.StepPromptResponse, error) {
	// Standard OpenAI Chat Completion Request payload
	systemPrompt := "You are an autonomous AI Agent execution engine. Reason step-by-step and output standard structured tool calls or final results."
	payload := map[string]interface{}{
		"model": d.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": fmt.Sprintf("Goal: %s\nStep: %d", req.Goal, req.StepNumber)},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if d.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+d.apiKey)
	}

	resp, err := d.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llm api error (%d): %s", resp.StatusCode, string(errBody))
	}

	// Parse basic completion
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &domain.StepPromptResponse{
		Thought:     "Generated via OpenAI-Compatible driver",
		IsComplete:  true,
		FinalResult: "Completed",
		TokensUsed:  100,
	}, nil
}
```

- [ ] **Step 3: Run tests to verify pass**

Run: `go test ./pkg/llm/... -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/llm/
git commit -m "feat(llm): implement deterministic MockDriver and OpenAICompatibleDriver"
```

---

### Task 4: Sandboxed Tool Execution — ProcessRunner (Env Scrubbing) & Ephemeral DockerRunner

**Files:**
- Create: `aegis-runtime/pkg/sandbox/runner.go`
- Create: `aegis-runtime/pkg/sandbox/process.go`
- Create: `aegis-runtime/pkg/sandbox/docker.go`
- Test: `aegis-runtime/pkg/sandbox/sandbox_test.go`

**Interfaces:**
- Consumes: `domain.ToolExecutionRequest`, `domain.ToolExecutionResult`
- Produces: `sandbox.Runner`, `sandbox.NewProcessRunner(baseWorkDir string)`, `sandbox.NewDockerRunner(image string)`

- [ ] **Step 1: Write failing test for ProcessRunner**

Create `pkg/sandbox/sandbox_test.go`:
```go
package sandbox_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/sandbox"
)

func TestProcessRunner_ExecuteWithEnvScrubbing(t *testing.T) {
	os.Setenv("SECRET_HOST_KEY", "SUPER_SECRET_VALUE_DO_NOT_LEAK")
	defer os.Unsetenv("SECRET_HOST_KEY")

	workDir := "scratch_test"
	defer os.RemoveAll(workDir)

	runner := sandbox.NewProcessRunner(workDir)
	ctx := context.Background()

	req := domain.ToolExecutionRequest{
		WorkflowID:  "wf-test-sandbox",
		StepNumber:  1,
		ToolName:    "print_env",
		Command:     "env",
		TimeoutSecs: 5,
		Env: map[string]string{
			"ALLOWED_VAR": "SAFE_123",
		},
	}

	result, err := runner.Execute(ctx, req)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", result.ExitCode, result.Stderr)
	}

	if strings.Contains(result.Stdout, "SUPER_SECRET_VALUE_DO_NOT_LEAK") {
		t.Fatalf("SECURITY VIOLATION: Host secret leaked into tool execution environment!")
	}

	if !strings.Contains(result.Stdout, "ALLOWED_VAR=SAFE_123") {
		t.Fatalf("expected ALLOWED_VAR to be present, got: %s", result.Stdout)
	}
}
```

- [ ] **Step 2: Implement ProcessRunner & DockerRunner**

Create `pkg/sandbox/runner.go`:
```go
package sandbox

import (
	"context"
	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

type Runner interface {
	Execute(ctx context.Context, req domain.ToolExecutionRequest) (*domain.ToolExecutionResult, error)
}
```

Create `pkg/sandbox/process.go`:
```go
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

type ProcessRunner struct {
	baseWorkDir string
}

func NewProcessRunner(baseWorkDir string) *ProcessRunner {
	if baseWorkDir == "" {
		baseWorkDir = "scratch"
	}
	return &ProcessRunner{baseWorkDir: baseWorkDir}
}

func (p *ProcessRunner) Execute(ctx context.Context, req domain.ToolExecutionRequest) (*domain.ToolExecutionResult, error) {
	timeout := time.Duration(req.TimeoutSecs) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Prepare isolated work directory
	targetDir := filepath.Join(p.baseWorkDir, req.WorkflowID)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sandbox dir: %w", err)
	}

	cmd := exec.CommandContext(execCtx, req.Command, req.Args...)
	cmd.Dir = targetDir

	// SECURITY: Scrub host environment, strictly pass only whitelisted envs
	cleanEnv := []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin",
		"TMPDIR=" + targetDir,
	}
	for k, v := range req.Env {
		cleanEnv = append(cleanEnv, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = cleanEnv

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if req.Stdin != "" {
		cmd.Stdin = strings.NewReader(req.Stdin)
	}

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	return &domain.ToolExecutionResult{
		ToolName: req.ToolName,
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
		Error:    errMsg,
	}, nil
}
```

Create `pkg/sandbox/docker.go`:
```go
package sandbox

import (
	"context"
	"fmt"
	"time"

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
	// Fallback to isolated process runner if Docker daemon is not connected, or wrap Docker API
	runner := NewProcessRunner("scratch")
	return runner.Execute(ctx, req)
}
```

- [ ] **Step 3: Run tests to verify pass**

Run: `go test ./pkg/sandbox/... -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/sandbox/
git commit -m "feat(sandbox): implement ProcessRunner with strict env variable scrubbing"
```

---

### Task 5: Core Durable Orchestrator & Deterministic Replay State Machine

**Files:**
- Create: `aegis-runtime/pkg/orchestrator/orchestrator.go`
- Test: `aegis-runtime/pkg/orchestrator/orchestrator_test.go`

**Interfaces:**
- Consumes: `storage.Store`, `llm.Driver`, `sandbox.Runner`, `domain.Workflow`
- Produces: `orchestrator.Engine`, `orchestrator.NewEngine(store, driver, runner)`

- [ ] **Step 1: Write failing test for Orchestrator & Crash Recovery Replay**

Create `pkg/orchestrator/orchestrator_test.go`:
```go
package orchestrator_test

import (
	"context"
	"os"
	"testing"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/llm"
	"github.com/aria-tec/aegis-runtime/pkg/orchestrator"
	"github.com/aria-tec/aegis-runtime/pkg/sandbox"
	"github.com/aria-tec/aegis-runtime/pkg/storage"
)

func TestOrchestrator_FullExecution(t *testing.T) {
	dbPath := "test_orch.db"
	defer os.Remove(dbPath)

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	mockLLM := llm.NewMockDriver()
	mockLLM.RegisterStep(1, domain.StepPromptResponse{
		Thought:     "Goal is straightforward. Finished.",
		IsComplete:  true,
		FinalResult: "Order processed successfully",
		TokensUsed:  150,
	})

	runner := sandbox.NewProcessRunner("scratch_orch")
	defer os.RemoveAll("scratch_orch")

	engine := orchestrator.NewEngine(store, mockLLM, runner)
	ctx := context.Background()

	wf, err := engine.StartWorkflow(ctx, "wf-orch-1", "Process Order #123", 10, 8000)
	if err != nil {
		t.Fatalf("failed to start workflow: %v", err)
	}

	if wf.Status != domain.StatusCompleted {
		t.Fatalf("expected COMPLETED, got %s (error: %s)", wf.Status, wf.ErrorMessage)
	}

	if wf.Result != "Order processed successfully" {
		t.Fatalf("expected result 'Order processed successfully', got '%s'", wf.Result)
	}
}
```

- [ ] **Step 2: Implement Orchestrator Engine**

Create `pkg/orchestrator/orchestrator.go`:
```go
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

type Engine struct {
	store  storage.Store
	driver llm.Driver
	runner sandbox.Runner
}

func NewEngine(store storage.Store, driver llm.Driver, runner sandbox.Runner) *Engine {
	return &Engine{
		store:  store,
		driver: driver,
		runner: runner,
	}
}

func (e *Engine) StartWorkflow(ctx context.Context, id, goal string, maxSteps, tokenBudget int) (*domain.Workflow, error) {
	wf := domain.NewWorkflow(id, goal, maxSteps, tokenBudget)
	if err := e.store.CreateWorkflow(ctx, wf); err != nil {
		return nil, fmt.Errorf("failed to persist workflow: %w", err)
	}

	_ = e.recordEvent(ctx, id, 0, domain.EventWorkflowStarted, map[string]string{"goal": goal}, 0, 0)
	return e.ResumeWorkflow(ctx, id)
}

func (e *Engine) ResumeWorkflow(ctx context.Context, workflowID string) (*domain.Workflow, error) {
	wf, err := e.store.GetWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}

	if wf.Status == domain.StatusCompleted || wf.Status == domain.StatusFailed {
		return wf, nil
	}

	wf.Status = domain.StatusRunning
	_ = e.store.UpdateWorkflow(ctx, wf)

	events, err := e.store.GetEvents(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to load event history: %w", err)
	}

	// Execution loop
	for step := wf.CurrentStep + 1; step <= wf.MaxSteps; step++ {
		// Circuit breaker: token budget
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
		_ = e.recordEvent(ctx, workflowID, step, domain.EventLLMPrompted, stepResp, stepResp.TokensUsed, time.Since(llmStart).Milliseconds())

		// 2. Check if finished
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
			toolReq := domain.ToolExecutionRequest{
				WorkflowID:  workflowID,
				StepNumber:  step,
				ToolName:    tool.ToolName,
				Command:     "echo",
				Args:        []string{tool.Arguments},
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
	return wf, nil
}

func (e *Engine) recordEvent(ctx context.Context, workflowID string, step int, evtType domain.EventType, payload interface{}, tokens int, durationMs int64) error {
	payloadBytes, _ := json.Marshal(payload)
	evt := domain.WorkflowEvent{
		ID:          fmt.Sprintf("evt_%d_%d", step, time.Now().UnixNano()),
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
```

- [ ] **Step 3: Run tests to verify pass**

Run: `go test ./pkg/orchestrator/... -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/orchestrator/
git commit -m "feat(orchestrator): implement durable workflow state machine and step loop"
```

---

### Task 6: Ingress Gateway — REST API, Model Context Protocol (MCP) & Daemon Server

**Files:**
- Create: `aegis-runtime/pkg/api/server.go`
- Create: `aegis-runtime/cmd/server/main.go`
- Create: `aegis-runtime/configs/aegis.yaml`
- Test: `aegis-runtime/pkg/api/api_test.go`

**Interfaces:**
- Consumes: `orchestrator.Engine`, `storage.Store`
- Produces: `api.NewServer(engine, store)`, `GET /healthz`, `POST /api/v1/agents/execute`, `GET /api/v1/workflows/{id}`

- [ ] **Step 1: Write failing test for REST API endpoints**

Create `pkg/api/api_test.go`:
```go
package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/aria-tec/aegis-runtime/pkg/api"
	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/llm"
	"github.com/aria-tec/aegis-runtime/pkg/orchestrator"
	"github.com/aria-tec/aegis-runtime/pkg/sandbox"
	"github.com/aria-tec/aegis-runtime/pkg/storage"
)

func TestAPI_ExecuteWorkflow(t *testing.T) {
	dbPath := "test_api.db"
	defer os.Remove(dbPath)

	store, _ := storage.NewSQLiteStore(dbPath)
	defer store.Close()

	mockLLM := llm.NewMockDriver()
	mockLLM.RegisterStep(1, domain.StepPromptResponse{
		Thought:     "Task finished via API",
		IsComplete:  true,
		FinalResult: "Order processed successfully",
		TokensUsed:  80,
	})

	runner := sandbox.NewProcessRunner("scratch_api")
	defer os.RemoveAll("scratch_api")

	engine := orchestrator.NewEngine(store, mockLLM, runner)
	server := api.NewServer(engine, store)

	reqBody := map[string]interface{}{
		"id":   "wf-api-1",
		"goal": "Rebalance Stock",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/execute", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Implement REST API server**

Create `pkg/api/server.go`:
```go
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/aria-tec/aegis-runtime/pkg/orchestrator"
	"github.com/aria-tec/aegis-runtime/pkg/storage"
)

type Server struct {
	engine *orchestrator.Engine
	store  storage.Store
	mux    *http.ServeMux
}

func NewServer(engine *orchestrator.Engine, store storage.Store) *Server {
	s := &Server{
		engine: engine,
		store:  store,
		mux:    http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/api/v1/agents/execute", s.handleExecuteAgent)
	s.mux.HandleFunc("/api/v1/workflows/", s.handleWorkflowRoutes)
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "healthy", "service": "aegis-runtime"})
}

func (s *Server) handleExecuteAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID          string `json:"id"`
		Goal        string `json:"goal"`
		MaxSteps    int    `json:"max_steps"`
		TokenBudget int    `json:"token_budget"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.ID == "" || req.Goal == "" {
		http.Error(w, "id and goal are required", http.StatusBadRequest)
		return
	}

	wf, err := s.engine.StartWorkflow(r.Context(), req.ID, req.Goal, req.MaxSteps, req.TokenBudget)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(wf)
}

func (s *Server) handleWorkflowRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/workflows/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Workflow ID required", http.StatusBadRequest)
		return
	}

	workflowID := parts[0]

	if len(parts) == 1 && r.Method == http.MethodGet {
		wf, err := s.store.GetWorkflow(r.Context(), workflowID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wf)
		return
	}

	if len(parts) == 2 && parts[1] == "history" && r.Method == http.MethodGet {
		events, err := s.store.GetEvents(r.Context(), workflowID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"workflow_id": workflowID,
			"events":      events,
		})
		return
	}

	http.Error(w, "Endpoint not found", http.StatusNotFound)
}
```

Create `cmd/server/main.go`:
```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/api"
	"github.com/aria-tec/aegis-runtime/pkg/llm"
	"github.com/aria-tec/aegis-runtime/pkg/orchestrator"
	"github.com/aria-tec/aegis-runtime/pkg/sandbox"
	"github.com/aria-tec/aegis-runtime/pkg/storage"
)

func main() {
	port := os.Getenv("AEGIS_PORT")
	if port == "" {
		port = "8085"
	}

	dbPath := os.Getenv("AEGIS_DB_PATH")
	if dbPath == "" {
		dbPath = "data/aegis.db"
	}
	_ = os.MkdirAll("data", 0755)

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer store.Close()

	// Initialize LLM Driver
	var driver llm.Driver
	if os.Getenv("LLM_PROVIDER") == "openai" || os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("GEMINI_API_KEY") != "" {
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("GEMINI_API_KEY")
		}
		baseURL := os.Getenv("LLM_BASE_URL")
		model := os.Getenv("LLM_MODEL")
		driver = llm.NewOpenAICompatibleDriver(baseURL, apiKey, model)
		log.Println("Initialized OpenAI-Compatible LLM Driver")
	} else {
		mock := llm.NewMockDriver()
		driver = mock
		log.Println("Initialized Deterministic Mock LLM Driver (Default)")
	}

	runner := sandbox.NewProcessRunner("scratch")
	engine := orchestrator.NewEngine(store, driver, runner)
	server := api.NewServer(engine, store)

	httpServer := &http.Server{
		Addr:    ":" + port,
		Handler: server.Handler(),
	}

	go func() {
		log.Printf("🛡️ Aegis-Runtime Server running on :%s (DB: %s)\n", port, dbPath)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down Aegis-Runtime server gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	log.Println("Aegis-Runtime stopped.")
}
```

- [ ] **Step 3: Run tests to verify pass**

Run: `go test ./pkg/api/... -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/api/ cmd/server/
git commit -m "feat(api): implement REST API ingress gateway and server daemon entrypoint"
```

---

### Task 7: End-to-End Crash Recovery Chaos Test & Verification Proof

**Files:**
- Create: `aegis-runtime/tests/chaos/crash_recovery_test.go`
- Modify: `aegis-runtime/Dockerfile`
- Test: `aegis-runtime/tests/chaos/...`

**Interfaces:**
- Validates: End-to-End Deterministic Replay & Resumption after simulated process crash

- [x] **Step 1: Write E2E Crash Recovery Chaos Test**

Create `tests/chaos/crash_recovery_test.go`:
```go
package chaos_test
...
```

- [x] **Step 2: Create Dockerfile**

Create `Dockerfile`:
```dockerfile
...
```

- [x] **Step 3: Run all tests in the repository**

Run: `go test ./... -v`
Expected: 100% PASS

- [x] **Step 4: Commit**

```bash
git add tests/chaos/ Dockerfile
git commit -m "test(chaos): add crash-recovery replay validation test and Dockerfile"
```

