package protocol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// SessionCommand is a message sent from client to daemon.
type SessionCommand struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// SessionEvent is a message sent from daemon to client.
type SessionEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// --- Client → Daemon command payloads ---

// SessionStartData is sent to start a new agent session.
type SessionStartData struct {
	CWD       string `json:"cwd"`
	Model     string `json:"model"`
	ForceInit bool   `json:"force_init"`
}

// SessionInputData carries user chat input.
type SessionInputData struct {
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment represents a file attachment (e.g., image) sent with user input.
type Attachment struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	Path      string `json:"path,omitempty"`
}

// ValidateAttachment checks if an attachment is valid.
func ValidateAttachment(att Attachment) error {
	if att.Type != "image" {
		return fmt.Errorf("invalid attachment type: %s (only 'image' supported)", att.Type)
	}
	if !strings.HasPrefix(att.MediaType, "image/") {
		return fmt.Errorf("invalid media type: %s (must start with 'image/')", att.MediaType)
	}
	if _, err := base64.StdEncoding.DecodeString(att.Data); err != nil {
		return fmt.Errorf("invalid base64 data: %w", err)
	}
	return nil
}

// SessionConfirmData carries tool approval/denial.
type SessionConfirmData struct {
	Approved bool `json:"approved"`
}

// SessionPlanActionData carries plan review decisions.
type SessionPlanActionData struct {
	Action string `json:"action"` // "approve", "reject", "modify"
	Text   string `json:"text,omitempty"`
}

// SessionUserAnswerData carries the user's response to a question.
type SessionUserAnswerData struct {
	Answer  string            `json:"answer"`
	Text    string            `json:"text,omitempty"`    // user input when has_user_input
	Answers map[string]string `json:"answers,omitempty"` // question ID → answer (batch mode)
}

// SessionWorkflowData carries a workflow execution request.
type SessionWorkflowData struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// --- Daemon → Client event payloads ---

// EventSessionStarted acknowledges session creation.
type EventSessionStarted struct {
	SessionID string `json:"session_id"`
}

// EventInitState carries brain init progress.
type EventInitState struct {
	State int `json:"state"`
}

// EventStreamChunk carries an LLM text delta.
type EventStreamChunk struct {
	Text string `json:"text"`
}

// EventStreamDone signals LLM turn completion with token stats.
type EventStreamDone struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	ElapsedMs           int64 `json:"elapsed_ms"`
}

// EventToolCall indicates a tool call is starting.
type EventToolCall struct {
	ToolID  string `json:"tool_id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Reason  string `json:"reason,omitempty"`
}

// EventToolResult carries the result of a tool execution.
type EventToolResult struct {
	ToolID  string `json:"tool_id"`
	Name    string `json:"name"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error"`
	Detail  string `json:"detail,omitempty"` // optional rich detail (e.g. edit diff)
}

// EventConfirmRequest asks the user to approve a tool execution.
type EventConfirmRequest struct {
	ToolName string         `json:"tool_name"`
	Params   map[string]any `json:"params"`
}

// EventPlanProposed carries a plan for user review.
type EventPlanProposed struct {
	Plan *Plan `json:"plan"`
}

// EventPlanTaskStart signals a plan task is starting.
type EventPlanTaskStart struct {
	TaskIdx int    `json:"task_idx"`
	Title   string `json:"title"`
	Total   int    `json:"total"`
}

// EventPlanTaskDone signals a plan task has finished.
type EventPlanTaskDone struct {
	TaskIdx int    `json:"task_idx"`
	Title   string `json:"title"`
	Success bool   `json:"success"`
	Summary string `json:"summary"`
}

// EventPlanComplete signals all plan tasks are done.
type EventPlanComplete struct {
	Plan *Plan `json:"plan"`
}

// QuestionDef defines a single question in a batch.
type QuestionDef struct {
	ID       string   `json:"id"`
	Category string   `json:"category"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

// EventQuestionOption is a structured option for workflow tool steps.
type EventQuestionOption struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	HasUserInput bool   `json:"has_user_input,omitempty"`
}

// EventUserQuestion asks the user a question with options.
type EventUserQuestion struct {
	// Single-question fields (backward compatible)
	Question    string                `json:"question"`
	Options     []string              `json:"options"`
	RichOptions []EventQuestionOption `json:"rich_options,omitempty"` // structured options (workflow tool steps)
	Placeholder string                `json:"placeholder,omitempty"`
	Category    string                `json:"category,omitempty"`

	// Multi-question batch (if set, overrides single fields)
	Questions []QuestionDef `json:"questions,omitempty"`
}

// EventError carries an error message.
type EventError struct {
	Message string `json:"message"`
}

// EventRetry notifies the UI about an API retry attempt.
type EventRetry struct {
	Attempt    int    `json:"attempt"`
	MaxRetries int    `json:"max_retries"`
	WaitSecs   int    `json:"wait_secs"`
	Reason     string `json:"reason"`
}

// --- Workflow info (sent to UI for mode cycling) ---

// WorkflowInfo describes a workflow available for UI mode cycling.
type WorkflowInfo struct {
	Name string `json:"name"`
}

// EventWorkflowsAvailable carries the list of configured workflows to the UI.
type EventWorkflowsAvailable struct {
	Workflows []WorkflowInfo `json:"workflows"`
}

// --- Workflow events ---

// EventWorkflowStart signals a workflow has started.
type EventWorkflowStart struct {
	WorkflowName string `json:"workflow_name"`
	TotalSteps   int    `json:"total_steps"`
}

// EventWorkflowStepStart signals a workflow step is starting.
type EventWorkflowStepStart struct {
	StepID      string `json:"step_id"`
	StepIdx     int    `json:"step_idx"`
	Total       int    `json:"total"`
	Agent       string `json:"agent"`
	Explanation string `json:"explanation,omitempty"`
}

// ToolStat summarizes tool usage within a workflow step.
type ToolStat struct {
	Name    string `json:"name"`
	Calls   int    `json:"calls"`
	Summary string `json:"summary"`
}

// EventWorkflowStepDone signals a workflow step has finished.
type EventWorkflowStepDone struct {
	StepID              string     `json:"step_id"`
	StepIdx             int        `json:"step_idx"`
	Total               int        `json:"total"`
	Success             bool       `json:"success"`
	Display             string     `json:"display,omitempty"`
	Model               string     `json:"model,omitempty"`
	InputTokens         int64      `json:"input_tokens,omitempty"`
	OutputTokens        int64      `json:"output_tokens,omitempty"`
	CacheCreationTokens int64      `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int64      `json:"cache_read_tokens,omitempty"`
	ToolStats           []ToolStat `json:"tool_stats,omitempty"`
	DurationMs          int64      `json:"duration_ms,omitempty"`
}

// StepCost summarizes token usage and cost for a single workflow step.
type StepCost struct {
	StepID              string  `json:"step_id"`
	Explanation         string  `json:"explanation,omitempty"`
	Model               string  `json:"model"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	Cost                float64 `json:"cost"`
	DurationMs          int64   `json:"duration_ms,omitempty"`
}

// EventWorkflowComplete signals a workflow has finished.
type EventWorkflowComplete struct {
	WorkflowName string     `json:"workflow_name"`
	Success      bool       `json:"success"`
	Summary      string     `json:"summary,omitempty"`
	StepCosts    []StepCost `json:"step_costs,omitempty"`
	DurationMs   int64      `json:"duration_ms,omitempty"`
}
