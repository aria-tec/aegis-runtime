package tests_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"unicode/utf8"

	"github.com/aria-tec/aegis-runtime/pkg/api"
	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/llm"
	"github.com/aria-tec/aegis-runtime/pkg/orchestrator"
	"github.com/aria-tec/aegis-runtime/pkg/sandbox"
	"github.com/aria-tec/aegis-runtime/pkg/storage"
)

func FuzzMCPHandler(f *testing.F) {
	// Seed corpus with valid and edge-case JSON-RPC payloads
	f.Add([]byte(`{"jsonrpc":"2.0","method":"initialize","id":1}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"tools/list","id":"2"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"execute_agent","arguments":{"id":"wf-1","goal":"test"}},"id":3}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"unknown_method","id":4}`))
	f.Add([]byte(`{malformed json`))
	f.Add([]byte(`{}`))
	f.Add([]byte(``))
	f.Add([]byte(`{"jsonrpc":"1.0"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"tools/call","params":null,"id":5}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":12345},"id":6}`))

	tempDir := f.TempDir()
	store, err := storage.NewSQLiteStore(tempDir + "/fuzz_mcp.db")
	if err != nil {
		f.Fatalf("failed to initialize store: %v", err)
	}
	defer store.Close()

	mockDriver := llm.NewMockDriver()
	procRunner := sandbox.NewProcessRunner(tempDir)
	engine := orchestrator.NewEngine(store, mockDriver, procRunner)
	server := api.NewServer(engine, store)

	f.Fuzz(func(t *testing.T, payload []byte) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		// The server must handle all inputs gracefully with HTTP response, never panic
		server.ServeHTTP(rec, req)

		if rec.Code == 0 {
			t.Fatalf("unexpected empty status code for payload: %q", string(payload))
		}
	})
}

func FuzzDomainEventJSON(f *testing.F) {
	// Seed corpus with event payloads
	f.Add([]byte(`{"goal":"Reconcile inventory"}`))
	f.Add([]byte(`{"step":1}`))
	f.Add([]byte(`{"thought":"reasoning","is_complete":true,"final_result":"done","tokens_used":50}`))
	f.Add([]byte(`{"tool_name":"sh","exit_code":0,"stdout":"ok"}`))
	f.Add([]byte(`{"result":"all tasks finished"}`))
	f.Add([]byte(`{corrupted json payload`))
	f.Add([]byte(``))

	f.Fuzz(func(t *testing.T, payload []byte) {
		// If input is not valid UTF-8, JSON encoding replaces bytes, skip roundtrip equality
		if !utf8.Valid(payload) {
			return
		}

		evt := domain.WorkflowEvent{
			ID:          "evt-fuzz-1",
			WorkflowID:  "wf-fuzz-1",
			StepNumber:  1,
			EventType:   domain.EventLLMPrompted,
			PayloadJSON: string(payload),
		}

		// JSON Marshal/Unmarshal invariant check
		marshaled, err := json.Marshal(evt)
		if err != nil {
			t.Fatalf("unexpected marshal failure: %v", err)
		}

		var unmarshaled domain.WorkflowEvent
		if err := json.Unmarshal(marshaled, &unmarshaled); err != nil {
			t.Fatalf("unexpected unmarshal failure: %v", err)
		}

		if unmarshaled.ID != evt.ID || unmarshaled.PayloadJSON != evt.PayloadJSON {
			t.Fatalf("roundtrip mismatch: original %+v vs unmarshaled %+v", evt, unmarshaled)
		}
	})
}

func FuzzExecuteAgentAPI(f *testing.F) {
	f.Add([]byte(`{"id":"wf-fuzz-1","goal":"optimize database","max_steps":5,"token_budget":4000}`))
	f.Add([]byte(`{"id":"","goal":""}`))
	f.Add([]byte(`{"max_steps":-1,"token_budget":-500}`))
	f.Add([]byte(`invalid json string`))
	f.Add([]byte(`{}`))

	tempDir := f.TempDir()
	store, err := storage.NewSQLiteStore(tempDir + "/fuzz_api.db")
	if err != nil {
		f.Fatalf("failed to initialize store: %v", err)
	}
	defer store.Close()

	mockDriver := llm.NewMockDriver()
	procRunner := sandbox.NewProcessRunner(tempDir)
	engine := orchestrator.NewEngine(store, mockDriver, procRunner)
	server := api.NewServer(engine, store)

	f.Fuzz(func(t *testing.T, payload []byte) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/execute", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		// Server must never panic on arbitrary POST bodies
		server.ServeHTTP(rec, req)

		if rec.Code == 0 {
			t.Fatalf("response code is 0 for payload: %q", string(payload))
		}
	})
}
