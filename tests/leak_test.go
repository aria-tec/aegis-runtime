package tests_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/api"
	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/llm"
	"github.com/aria-tec/aegis-runtime/pkg/orchestrator"
	"github.com/aria-tec/aegis-runtime/pkg/sandbox"
	"github.com/aria-tec/aegis-runtime/pkg/storage"
	"go.uber.org/goleak"
)

func TestGoroutineLeak_MultiStepExecution(t *testing.T) {
	// Verify zero lingering goroutines after execution
	defer goleak.VerifyNone(t)

	tempDir := t.TempDir()
	store, err := storage.NewSQLiteStore(tempDir + "/leak_test.db")
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}
	defer store.Close()

	mockDriver := llm.NewMockDriver()
	mockDriver.RegisterStep(1, domain.StepPromptResponse{
		Thought: "Executing check",
		ToolCalls: []domain.ToolCall{
			{ID: "tc-1", ToolName: "echo", Arguments: "leak-check-step-1"},
		},
		TokensUsed: 50,
	})
	mockDriver.RegisterStep(2, domain.StepPromptResponse{
		Thought:     "Finished",
		IsComplete:  true,
		FinalResult: "all clean",
		TokensUsed:  30,
	})

	procRunner := sandbox.NewProcessRunner(tempDir)
	engine := orchestrator.NewEngine(store, mockDriver, procRunner)

	ctx := context.Background()
	wf, err := engine.StartWorkflow(ctx, "wf-leak-1", "Run multi-step leak invariant", 5, 4000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wf.Status != domain.StatusCompleted {
		t.Fatalf("expected status COMPLETED, got %s", wf.Status)
	}
}

func TestGoroutineLeak_ContextCancelled(t *testing.T) {
	defer goleak.VerifyNone(t)

	tempDir := t.TempDir()
	store, err := storage.NewSQLiteStore(tempDir + "/leak_cancel.db")
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}
	defer store.Close()

	mockDriver := llm.NewMockDriver()
	mockDriver.RegisterStep(1, domain.StepPromptResponse{
		Thought: "Running sleep command",
		ToolCalls: []domain.ToolCall{
			{ID: "tc-sleep", ToolName: "sleep", Arguments: "2"},
		},
		TokensUsed: 50,
	})

	procRunner := sandbox.NewProcessRunner(tempDir)
	engine := orchestrator.NewEngine(store, mockDriver, procRunner)

	// Cancel context during tool execution
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, _ = engine.StartWorkflow(ctx, "wf-leak-cancel", "Cancel mid execution", 5, 4000)

	// Small pause to let cancelled subprocesses exit
	time.Sleep(50 * time.Millisecond)
}

func TestGoroutineLeak_APIServerRequests(t *testing.T) {
	defer goleak.VerifyNone(t)

	tempDir := t.TempDir()
	store, err := storage.NewSQLiteStore(tempDir + "/leak_api.db")
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}
	defer store.Close()

	mockDriver := llm.NewMockDriver()
	procRunner := sandbox.NewProcessRunner(tempDir)
	engine := orchestrator.NewEngine(store, mockDriver, procRunner)
	server := api.NewServer(engine, store)

	// Test multiple HTTP requests
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
	}

	mcpReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","method":"tools/list","id":1}`)))
	mcpRec := httptest.NewRecorder()
	server.ServeHTTP(mcpRec, mcpReq)
	if mcpRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from MCP, got %d", mcpRec.Code)
	}
}
