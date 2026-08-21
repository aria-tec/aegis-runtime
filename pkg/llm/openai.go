package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

const (
	DefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta/openai/"
	DefaultModel   = "gemini-2.5-flash"
	DefaultTimeout = 30 * time.Second
)

// DriverOption configures an OpenAICompatibleDriver.
type DriverOption func(*OpenAICompatibleDriver)

// WithHTTPClient sets a custom http.Client for the driver.
func WithHTTPClient(client *http.Client) DriverOption {
	return func(d *OpenAICompatibleDriver) {
		if client != nil {
			d.httpClient = client
		}
	}
}

// OpenAICompatibleDriver interacts with any OpenAI-compatible Chat Completions endpoint.
type OpenAICompatibleDriver struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewOpenAICompatibleDriver constructs an OpenAICompatibleDriver with given parameters or defaults.
func NewOpenAICompatibleDriver(baseURL, apiKey, model string, opts ...DriverOption) *OpenAICompatibleDriver {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if model == "" {
		model = DefaultModel
	}
	d := &OpenAICompatibleDriver{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// BaseURL returns the configured base URL.
func (d *OpenAICompatibleDriver) BaseURL() string {
	return d.baseURL
}

// Model returns the configured model identifier.
func (d *OpenAICompatibleDriver) Model() string {
	return d.model
}

type openAIChatMessage struct {
	Role       string               `json:"role"`
	Content    string               `json:"content,omitempty"`
	ToolCalls  []openAIToolCallItem `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIToolCallItem struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIFunctionCall `json:"function"`
}

type openAIChoice struct {
	Index        int               `json:"index"`
	Message      openAIChatMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIChatCompletionResponse struct {
	ID      string         `json:"id"`
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
}

// GenerateStep executes an inference request against the OpenAI chat completions endpoint.
func (d *OpenAICompatibleDriver) GenerateStep(ctx context.Context, req domain.StepPromptRequest) (*domain.StepPromptResponse, error) {
	systemPrompt := "You are an autonomous AI Agent execution engine. Reason step-by-step and output standard structured tool calls or final results."

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Goal: %s\nStep: %d", req.Goal, req.StepNumber))
	if len(req.AllowedTools) > 0 {
		sb.WriteString(fmt.Sprintf("\nAllowed Tools: %s", strings.Join(req.AllowedTools, ", ")))
	}
	if len(req.EventHistory) > 0 {
		sb.WriteString("\nEvent History:\n")
		for _, evt := range req.EventHistory {
			sb.WriteString(fmt.Sprintf("- [Step %d] %s: %s\n", evt.StepNumber, evt.EventType, evt.PayloadJSON))
		}
	}

	messages := []openAIChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: sb.String()},
	}

	payload := map[string]interface{}{
		"model":    d.model,
		"messages": messages,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request payload: %w", err)
	}

	endpoint := strings.TrimRight(d.baseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if d.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+d.apiKey)
	}

	resp, err := d.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("llm api error (%d): %s", resp.StatusCode, string(respBody))
	}

	var chatResp openAIChatCompletionResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode response json: %w", err)
	}

	result := &domain.StepPromptResponse{
		TokensUsed: chatResp.Usage.TotalTokens,
	}

	if len(chatResp.Choices) == 0 {
		result.Thought = "Empty choices received from provider"
		result.IsComplete = true
		return result, nil
	}

	choice := chatResp.Choices[0]
	result.Thought = choice.Message.Content

	if len(choice.Message.ToolCalls) > 0 {
		result.IsComplete = false
		for _, tc := range choice.Message.ToolCalls {
			result.ToolCalls = append(result.ToolCalls, domain.ToolCall{
				ID:        tc.ID,
				ToolName:  tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
	} else {
		result.IsComplete = true
		result.FinalResult = choice.Message.Content
	}

	return result, nil
}
