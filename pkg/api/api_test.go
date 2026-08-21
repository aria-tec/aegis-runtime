package api_test

import (
	"bytes"
	"context"
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

func setupTestServer(t *testing.T) (*api.Server, storage.Store, *llm.MockDriver, func()) {
	t.Helper()
	dbPath := "test_api_" + t.Name() + ".db"
	_ = os.Remove(dbPath)

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}

	mockLLM := llm.NewMockDriver()
	scratchDir := "scratch_api_" + t.Name()
	runner := sandbox.NewProcessRunner(scratchDir)

	engine := orchestrator.NewEngine(store, mockLLM, runner)
	server := api.NewServer(engine, store)

	cleanup := func() {
		store.Close()
		_ = os.Remove(dbPath)
		_ = os.RemoveAll(scratchDir)
	}

	return server, store, mockLLM, cleanup
}

func TestAPI_Healthz(t *testing.T) {
	server, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "healthy" {
		t.Errorf("expected status 'healthy', got '%s'", resp["status"])
	}
	if resp["service"] != "aegis-runtime" {
		t.Errorf("expected service 'aegis-runtime', got '%s'", resp["service"])
	}
}

func TestAPI_ExecuteAgent(t *testing.T) {
	server, _, mockLLM, cleanup := setupTestServer(t)
	defer cleanup()

	mockLLM.RegisterStep(1, domain.StepPromptResponse{
		Thought:     "Goal fulfilled immediately",
		IsComplete:  true,
		FinalResult: "Calculated tax: $45.00",
		TokensUsed:  100,
	})

	payload := map[string]interface{}{
		"id":           "wf-exec-1",
		"goal":         "Calculate sales tax for order #123",
		"max_steps":    10,
		"token_budget": 8000,
	}
	bodyBytes, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/execute", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var wf domain.Workflow
	if err := json.NewDecoder(rec.Body).Decode(&wf); err != nil {
		t.Fatalf("failed to decode workflow: %v", err)
	}

	if wf.ID != "wf-exec-1" {
		t.Errorf("expected id 'wf-exec-1', got '%s'", wf.ID)
	}
	if wf.Status != domain.StatusCompleted {
		t.Errorf("expected status 'COMPLETED', got '%s'", wf.Status)
	}
	if wf.Result != "Calculated tax: $45.00" {
		t.Errorf("expected result 'Calculated tax: $45.00', got '%s'", wf.Result)
	}
}

func TestAPI_ExecuteAgent_BadRequest(t *testing.T) {
	server, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	// Missing goal
	payload := map[string]interface{}{
		"id": "wf-invalid",
	}
	bodyBytes, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/execute", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d", rec.Code)
	}

	// Malformed JSON
	reqBadJSON := httptest.NewRequest(http.MethodPost, "/api/v1/agents/execute", bytes.NewReader([]byte("{invalid-json")))
	recBadJSON := httptest.NewRecorder()
	server.Handler().ServeHTTP(recBadJSON, reqBadJSON)
	if recBadJSON.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request for malformed json, got %d", recBadJSON.Code)
	}
}

func TestAPI_GetWorkflow(t *testing.T) {
	server, store, _, cleanup := setupTestServer(t)
	defer cleanup()

	wf := domain.NewWorkflow("wf-get-1", "Query database", 5, 5000)
	wf.Status = domain.StatusRunning
	if err := store.CreateWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("failed to create workflow: %v", err)
	}

	// 1. Success case
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/wf-get-1", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var fetched domain.Workflow
	if err := json.NewDecoder(rec.Body).Decode(&fetched); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if fetched.ID != "wf-get-1" || fetched.Goal != "Query database" {
		t.Errorf("unexpected fetched workflow: %+v", fetched)
	}

	// 2. Not found case
	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/wf-nonexistent", nil)
	rec404 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec404, req404)

	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec404.Code)
	}
}

func TestAPI_GetWorkflowHistory(t *testing.T) {
	server, store, _, cleanup := setupTestServer(t)
	defer cleanup()

	wf := domain.NewWorkflow("wf-hist-1", "Run diagnostic", 5, 5000)
	_ = store.CreateWorkflow(context.Background(), wf)

	evt := domain.WorkflowEvent{
		ID:          "evt-h-1",
		WorkflowID:  "wf-hist-1",
		StepNumber:  1,
		EventType:   domain.EventStepStarted,
		PayloadJSON: `{"step":1}`,
		TokensUsed:  50,
		DurationMs:  10,
	}
	_ = store.AppendEvent(context.Background(), &evt)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/wf-hist-1/history", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		WorkflowID string                 `json:"workflow_id"`
		Events     []domain.WorkflowEvent `json:"events"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode history: %v", err)
	}

	if resp.WorkflowID != "wf-hist-1" {
		t.Errorf("expected workflow_id 'wf-hist-1', got '%s'", resp.WorkflowID)
	}
	if len(resp.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(resp.Events))
	}
	if resp.Events[0].ID != "evt-h-1" {
		t.Errorf("expected event id 'evt-h-1', got '%s'", resp.Events[0].ID)
	}
}

func TestAPI_ResumeWorkflow(t *testing.T) {
	server, store, mockLLM, cleanup := setupTestServer(t)
	defer cleanup()

	wf := domain.NewWorkflow("wf-resume-1", "Resume interrupted workflow", 5, 5000)
	wf.Status = domain.StatusPaused
	wf.CurrentStep = 1
	_ = store.CreateWorkflow(context.Background(), wf)

	mockLLM.RegisterStep(2, domain.StepPromptResponse{
		Thought:     "Resuming and finishing task",
		IsComplete:  true,
		FinalResult: "Finished after resumption",
		TokensUsed:  90,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/wf-resume-1/resume", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resumed domain.Workflow
	if err := json.NewDecoder(rec.Body).Decode(&resumed); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resumed.Status != domain.StatusCompleted {
		t.Errorf("expected status 'COMPLETED', got '%s'", resumed.Status)
	}
	if resumed.Result != "Finished after resumption" {
		t.Errorf("expected result 'Finished after resumption', got '%s'", resumed.Result)
	}

	// Resume non-existent workflow
	req404 := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/wf-ghost/resume", nil)
	rec404 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec404, req404)
	if rec404.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 for non-existent resume, got %d", rec404.Code)
	}
}

func TestAPI_MCP_InitializeAndPing(t *testing.T) {
	server, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. initialize
	initReqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]interface{}{},
	}
	b, _ := json.Marshal(initReqBody)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var initResp map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&initResp)
	if initResp["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %v", initResp["jsonrpc"])
	}

	// 2. ping
	pingReqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "ping",
	}
	b, _ = json.Marshal(pingReqBody)
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	// 3. Unknown method
	unknownReqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "invalid/method",
	}
	b, _ = json.Marshal(unknownReqBody)
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	var unkResp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&unkResp)
	if unkResp.Error == nil || unkResp.Error.Code != -32601 {
		t.Errorf("expected method not found error code -32601, got %+v", unkResp.Error)
	}

	// 4. Malformed JSON
	reqBad := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte("{bad-json")))
	recBad := httptest.NewRecorder()
	server.Handler().ServeHTTP(recBad, reqBad)
	var badResp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(recBad.Body).Decode(&badResp)
	if badResp.Error == nil || badResp.Error.Code != -32700 {
		t.Errorf("expected parse error code -32700, got %+v", badResp.Error)
	}
}

func TestAPI_MCP_ToolsListAndCall(t *testing.T) {
	server, store, mockLLM, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. tools/list
	listReqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "req-tools-list",
		"method":  "tools/list",
		"params":  map[string]interface{}{},
	}
	b, _ := json.Marshal(listReqBody)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var listResp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatalf("failed to decode tools/list response: %v", err)
	}

	if len(listResp.Result.Tools) != 3 {
		t.Fatalf("expected 3 tools in MCP response, got %d", len(listResp.Result.Tools))
	}

	// 2. tools/call -> execute_agent
	mockLLM.RegisterStep(1, domain.StepPromptResponse{
		Thought:     "MCP tool executed",
		IsComplete:  true,
		FinalResult: "MCP agent execution complete",
		TokensUsed:  60,
	})

	callReqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "req-call-1",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "execute_agent",
			"arguments": map[string]interface{}{
				"id":           "wf-mcp-1",
				"goal":         "Process order via MCP",
				"max_steps":    5,
				"token_budget": 4000,
			},
		},
	}
	b, _ = json.Marshal(callReqBody)
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var callResp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&callResp); err != nil {
		t.Fatalf("failed to decode tools/call response: %v", err)
	}

	if callResp.Result.IsError {
		t.Errorf("expected isError false, got true")
	}
	if len(callResp.Result.Content) == 0 {
		t.Fatalf("expected content in result")
	}

	// 3. tools/call -> get_workflow
	callGetBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "req-call-get",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "get_workflow",
			"arguments": map[string]interface{}{
				"id": "wf-mcp-1",
			},
		},
	}
	b, _ = json.Marshal(callGetBody)
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	// 4. tools/call -> resume_workflow
	wfPaused := domain.NewWorkflow("wf-mcp-paused", "Resume via MCP", 5, 5000)
	wfPaused.Status = domain.StatusPaused
	wfPaused.CurrentStep = 1
	_ = store.CreateWorkflow(context.Background(), wfPaused)
	mockLLM.RegisterStep(2, domain.StepPromptResponse{
		Thought:     "Resume done via MCP",
		IsComplete:  true,
		FinalResult: "MCP resume success",
		TokensUsed:  50,
	})

	callResumeBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "req-call-resume",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "resume_workflow",
			"arguments": map[string]interface{}{
				"id": "wf-mcp-paused",
			},
		},
	}
	b, _ = json.Marshal(callResumeBody)
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	// 5. tools/call -> unknown tool
	callUnknownBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "req-call-unk",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "unknown_tool",
		},
	}
	b, _ = json.Marshal(callUnknownBody)
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	var unkToolResp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&unkToolResp)
	if !unkToolResp.Result.IsError {
		t.Errorf("expected isError true for unknown tool")
	}

	// 6. tools/call -> invalid arguments json
	callBadArgsBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "req-call-bad-args",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "get_workflow",
			"arguments": "invalid-arg-json",
		},
	}
	b, _ = json.Marshal(callBadArgsBody)
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	var badArgsResp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&badArgsResp)
	if badArgsResp.Error == nil || badArgsResp.Error.Code != -32602 {
		t.Errorf("expected -32602 invalid params error")
	}

	// 7. tools/call -> invalid params
	reqBadParams := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":"1","method":"tools/call","params":"not-an-object"}`)))
	recBadParams := httptest.NewRecorder()
	server.Handler().ServeHTTP(recBadParams, reqBadParams)
	var badPResp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(recBadParams.Body).Decode(&badPResp)
	if badPResp.Error == nil || badPResp.Error.Code != -32602 {
		t.Errorf("expected -32602 invalid params error")
	}

	// 8. tools/call -> execute_agent with bad args
	callBadExecArgs := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "req-call-bad-exec",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "execute_agent",
			"arguments": "not-an-object",
		},
	}
	b, _ = json.Marshal(callBadExecArgs)
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	var badExecResp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&badExecResp)
	if badExecResp.Error == nil || badExecResp.Error.Code != -32602 {
		t.Errorf("expected -32602 invalid params error")
	}

	// 9. tools/call -> resume_workflow with bad args
	callBadResumeArgs := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "req-call-bad-resume",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "resume_workflow",
			"arguments": "not-an-object",
		},
	}
	b, _ = json.Marshal(callBadResumeArgs)
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	var badResumeResp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&badResumeResp)
	if badResumeResp.Error == nil || badResumeResp.Error.Code != -32602 {
		t.Errorf("expected -32602 invalid params error")
	}

	// 10. tools/call -> get_workflow not found
	callGetNotFound := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "req-call-get-nf",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "get_workflow",
			"arguments": map[string]interface{}{
				"id": "wf-ghost",
			},
		},
	}
	b, _ = json.Marshal(callGetNotFound)
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	var getNFResp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&getNFResp)
	if !getNFResp.Result.IsError {
		t.Errorf("expected isError true for non-existent get_workflow")
	}

	// 11. tools/call -> resume_workflow not found
	callResumeNotFound := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "req-call-resume-nf",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "resume_workflow",
			"arguments": map[string]interface{}{
				"id": "wf-ghost",
			},
		},
	}
	b, _ = json.Marshal(callResumeNotFound)
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	var resumeNFResp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resumeNFResp)
	if !resumeNFResp.Result.IsError {
		t.Errorf("expected isError true for non-existent resume_workflow")
	}
}
