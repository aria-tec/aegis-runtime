package domain

import "time"

type WorkflowStatus string

const (
	StatusPending       WorkflowStatus = "PENDING"
	StatusRunning       WorkflowStatus = "RUNNING"
	StatusStepExecuting WorkflowStatus = "STEP_EXECUTING"
	StatusToolExecuting WorkflowStatus = "TOOL_EXECUTING"
	StatusCompleted     WorkflowStatus = "COMPLETED"
	StatusFailed        WorkflowStatus = "FAILED"
	StatusPaused        WorkflowStatus = "PAUSED"
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
	WorkflowID   string          `json:"workflow_id"`
	Goal         string          `json:"goal"`
	StepNumber   int             `json:"step_number"`
	EventHistory []WorkflowEvent `json:"event_history"`
	AllowedTools []string        `json:"allowed_tools"`
}

type StepPromptResponse struct {
	Thought     string     `json:"thought"`
	IsComplete  bool       `json:"is_complete"`
	FinalResult string     `json:"final_result,omitempty"`
	ToolCalls   []ToolCall `json:"tool_calls,omitempty"`
	TokensUsed  int        `json:"tokens_used"`
}

type Workflow struct {
	ID           string         `json:"id"`
	Goal         string         `json:"goal"`
	Status       WorkflowStatus `json:"status"`
	CurrentStep  int            `json:"current_step"`
	TotalTokens  int            `json:"total_tokens"`
	MaxSteps     int            `json:"max_steps"`
	TokenBudget  int            `json:"token_budget"`
	Result       string         `json:"result,omitempty"`
	ErrorMessage string         `json:"error_message,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
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
