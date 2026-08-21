package llm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/llm"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newMockHTTPClient(fn roundTripFunc) *http.Client {
	return &http.Client{
		Transport: fn,
		Timeout:   10 * time.Second,
	}
}

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

	// Step 1: should return registered step 1
	res1, err := mock.GenerateStep(ctx, domain.StepPromptRequest{StepNumber: 1, Goal: "Balance inventory"})
	if err != nil {
		t.Fatalf("unexpected error on step 1: %v", err)
	}
	if len(res1.ToolCalls) != 1 || res1.ToolCalls[0].ToolName != "query_stock" {
		t.Fatalf("expected tool call query_stock, got %+v", res1.ToolCalls)
	}
	if res1.TokensUsed != 120 {
		t.Fatalf("expected 120 tokens used, got %d", res1.TokensUsed)
	}

	// Mutating the returned ToolCalls slice shouldn't mutate internal state
	res1.ToolCalls[0].ToolName = "mutated"
	res1Again, err := mock.GenerateStep(ctx, domain.StepPromptRequest{StepNumber: 1})
	if err != nil {
		t.Fatalf("unexpected error re-fetching step 1: %v", err)
	}
	if res1Again.ToolCalls[0].ToolName != "query_stock" {
		t.Fatalf("internal state was corrupted by caller mutation: %+v", res1Again.ToolCalls)
	}

	// Step 2: should return registered step 2
	res2, err := mock.GenerateStep(ctx, domain.StepPromptRequest{StepNumber: 2, Goal: "Balance inventory"})
	if err != nil {
		t.Fatalf("unexpected error on step 2: %v", err)
	}
	if !res2.IsComplete || res2.FinalResult != "Inventory rebalanced successfully." {
		t.Fatalf("expected completion, got %+v", res2)
	}
	if res2.TokensUsed != 80 {
		t.Fatalf("expected 80 tokens used, got %d", res2.TokensUsed)
	}

	// Step 3: unregistered step should return sensible default fallback completion
	res3, err := mock.GenerateStep(ctx, domain.StepPromptRequest{StepNumber: 3, Goal: "Balance inventory"})
	if err != nil {
		t.Fatalf("unexpected error on unregistered step: %v", err)
	}
	if !res3.IsComplete {
		t.Fatalf("expected unregistered step to default to is_complete=true, got %+v", res3)
	}
	if res3.FinalResult == "" {
		t.Fatalf("expected non-empty default final result, got %+v", res3)
	}
}

func TestMockDriver_ContextCancellation(t *testing.T) {
	mock := llm.NewMockDriver()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := mock.GenerateStep(ctx, domain.StepPromptRequest{StepNumber: 1})
	if err == nil {
		t.Fatalf("expected error on cancelled context, got nil")
	}
}

func TestMockDriver_Concurrency(t *testing.T) {
	mock := llm.NewMockDriver()
	ctx := context.Background()
	var wg sync.WaitGroup

	// Concurrently register and generate steps
	for i := 1; i <= 50; i++ {
		wg.Add(2)
		stepNum := i
		go func() {
			defer wg.Done()
			mock.RegisterStep(stepNum, domain.StepPromptResponse{
				Thought:    "Concurrent thought",
				IsComplete: true,
				TokensUsed: stepNum * 10,
			})
		}()
		go func() {
			defer wg.Done()
			_, _ = mock.GenerateStep(ctx, domain.StepPromptRequest{StepNumber: stepNum})
		}()
	}

	wg.Wait()

	// Verify step 25 was registered and can be read safely
	res, err := mock.GenerateStep(ctx, domain.StepPromptRequest{StepNumber: 25})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TokensUsed != 250 {
		t.Fatalf("expected 250 tokens, got %d", res.TokensUsed)
	}
}

func TestOpenAICompatibleDriver_DefaultConfig(t *testing.T) {
	driver := llm.NewOpenAICompatibleDriver("", "", "")
	if driver == nil {
		t.Fatalf("expected non-nil driver")
	}
	if driver.BaseURL() != llm.DefaultBaseURL {
		t.Fatalf("expected default baseURL %s, got %s", llm.DefaultBaseURL, driver.BaseURL())
	}
	if driver.Model() != llm.DefaultModel {
		t.Fatalf("expected default model %s, got %s", llm.DefaultModel, driver.Model())
	}
}

func TestOpenAICompatibleDriver_ToolCallResponse(t *testing.T) {
	client := newMockHTTPClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST method, got %s", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected /v1/chat/completions path, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("expected Authorization header Bearer test-api-key, got %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}

		var reqBody struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if reqBody.Model != "custom-model" {
			t.Errorf("expected model custom-model, got %v", reqBody.Model)
		}

		foundHistory := false
		for _, msg := range reqBody.Messages {
			if msg.Role == "user" && len(msg.Content) > 0 {
				foundHistory = true
			}
		}
		if !foundHistory {
			t.Errorf("expected user message content to be populated")
		}

		resp := map[string]interface{}{
			"id": "chatcmpl-test",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "Let me run the query tool.",
						"tool_calls": []map[string]interface{}{
							{
								"id":   "call-123",
								"type": "function",
								"function": map[string]interface{}{
									"name":      "query_db",
									"arguments": `{"table":"users"}`,
								},
							},
							{
								"id":   "call-124",
								"type": "function",
								"function": map[string]interface{}{
									"name":      "audit_log",
									"arguments": `{"action":"query"}`,
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     40,
				"completion_tokens": 60,
				"total_tokens":      100,
			},
		}

		respBytes, _ := json.Marshal(resp)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(respBytes)),
			Header:     make(http.Header),
		}, nil
	})

	driver := llm.NewOpenAICompatibleDriver("http://localhost:8080/v1", "test-api-key", "custom-model", llm.WithHTTPClient(client))
	ctx := context.Background()

	stepResp, err := driver.GenerateStep(ctx, domain.StepPromptRequest{
		WorkflowID:   "wf-1",
		Goal:         "Fetch user count",
		StepNumber:   1,
		AllowedTools: []string{"query_db", "audit_log"},
		EventHistory: []domain.WorkflowEvent{
			{
				ID:          "evt-1",
				WorkflowID:  "wf-1",
				StepNumber:  0,
				EventType:   domain.EventWorkflowStarted,
				PayloadJSON: `{"started":true}`,
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stepResp.IsComplete {
		t.Fatalf("expected step not complete due to tool call")
	}
	if len(stepResp.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(stepResp.ToolCalls))
	}
	if stepResp.ToolCalls[0].ID != "call-123" || stepResp.ToolCalls[0].ToolName != "query_db" {
		t.Fatalf("unexpected first tool call: %+v", stepResp.ToolCalls[0])
	}
	if stepResp.ToolCalls[1].ID != "call-124" || stepResp.ToolCalls[1].ToolName != "audit_log" {
		t.Fatalf("unexpected second tool call: %+v", stepResp.ToolCalls[1])
	}
	if stepResp.TokensUsed != 100 {
		t.Fatalf("expected 100 tokens used, got %d", stepResp.TokensUsed)
	}
}

func TestOpenAICompatibleDriver_FinalCompletion(t *testing.T) {
	client := newMockHTTPClient(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("expected empty Authorization header when no apiKey is passed, got %s", r.Header.Get("Authorization"))
		}
		resp := map[string]interface{}{
			"id": "chatcmpl-test-2",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "The total user count is 42.",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     50,
				"completion_tokens": 20,
				"total_tokens":      70,
			},
		}
		respBytes, _ := json.Marshal(resp)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(respBytes)),
			Header:     make(http.Header),
		}, nil
	})

	driver := llm.NewOpenAICompatibleDriver("http://localhost:8080", "", "", llm.WithHTTPClient(client))
	ctx := context.Background()

	stepResp, err := driver.GenerateStep(ctx, domain.StepPromptRequest{
		WorkflowID: "wf-1",
		Goal:       "Fetch user count",
		StepNumber: 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !stepResp.IsComplete {
		t.Fatalf("expected step to be complete")
	}
	if stepResp.FinalResult != "The total user count is 42." {
		t.Fatalf("expected final result 'The total user count is 42.', got '%s'", stepResp.FinalResult)
	}
	if stepResp.TokensUsed != 70 {
		t.Fatalf("expected 70 tokens used, got %d", stepResp.TokensUsed)
	}
}

func TestOpenAICompatibleDriver_EmptyChoices(t *testing.T) {
	client := newMockHTTPClient(func(r *http.Request) (*http.Response, error) {
		resp := map[string]interface{}{
			"id":      "chatcmpl-empty",
			"choices": []map[string]interface{}{},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": 0,
				"total_tokens":      10,
			},
		}
		respBytes, _ := json.Marshal(resp)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(respBytes)),
			Header:     make(http.Header),
		}, nil
	})

	driver := llm.NewOpenAICompatibleDriver("http://localhost:8080", "key", "model", llm.WithHTTPClient(client))
	ctx := context.Background()

	stepResp, err := driver.GenerateStep(ctx, domain.StepPromptRequest{
		WorkflowID: "wf-empty",
		Goal:       "Empty test",
		StepNumber: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error on empty choices: %v", err)
	}
	if !stepResp.IsComplete {
		t.Fatalf("expected empty choices to be marked complete")
	}
	if stepResp.TokensUsed != 10 {
		t.Fatalf("expected 10 tokens used, got %d", stepResp.TokensUsed)
	}
}

func TestOpenAICompatibleDriver_ServerError(t *testing.T) {
	client := newMockHTTPClient(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"error": "internal rate limit exceeded"}`))),
			Header:     make(http.Header),
		}, nil
	})

	driver := llm.NewOpenAICompatibleDriver("http://localhost:8080", "key", "model", llm.WithHTTPClient(client))
	ctx := context.Background()

	_, err := driver.GenerateStep(ctx, domain.StepPromptRequest{
		WorkflowID: "wf-1",
		Goal:       "Test error",
		StepNumber: 1,
	})
	if err == nil {
		t.Fatalf("expected error from 500 response, got nil")
	}
}

func TestOpenAICompatibleDriver_MalformedJSON(t *testing.T) {
	client := newMockHTTPClient(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(`not-json`))),
			Header:     make(http.Header),
		}, nil
	})

	driver := llm.NewOpenAICompatibleDriver("http://localhost:8080", "key", "model", llm.WithHTTPClient(client))
	ctx := context.Background()

	_, err := driver.GenerateStep(ctx, domain.StepPromptRequest{
		WorkflowID: "wf-1",
		Goal:       "Test malformed JSON",
		StepNumber: 1,
	})
	if err == nil {
		t.Fatalf("expected error from malformed json response, got nil")
	}
}

func TestOpenAICompatibleDriver_TransportError(t *testing.T) {
	client := newMockHTTPClient(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("network unreachable")
	})

	driver := llm.NewOpenAICompatibleDriver("http://localhost:8080", "key", "model", llm.WithHTTPClient(client))
	ctx := context.Background()

	_, err := driver.GenerateStep(ctx, domain.StepPromptRequest{
		WorkflowID: "wf-1",
		Goal:       "Test transport error",
		StepNumber: 1,
	})
	if err == nil {
		t.Fatalf("expected error from transport error, got nil")
	}
}

func TestOpenAICompatibleDriver_ContextTimeout(t *testing.T) {
	client := newMockHTTPClient(func(r *http.Request) (*http.Response, error) {
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(100 * time.Millisecond):
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
			}, nil
		}
	})

	driver := llm.NewOpenAICompatibleDriver("http://localhost:8080", "key", "model", llm.WithHTTPClient(client))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := driver.GenerateStep(ctx, domain.StepPromptRequest{
		WorkflowID: "wf-1",
		Goal:       "Test timeout",
		StepNumber: 1,
	})
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
}
