package protocol

import "time"

// InitState represents the brain initialization state.
type InitState int

const (
	InitNotNeeded InitState = iota
	InitInProgress
	InitDone
	InitError
	InitNoDaemon
)

// String returns a human-readable state description.
func (s InitState) String() string {
	switch s {
	case InitNotNeeded:
		return "Ready"
	case InitInProgress:
		return "Initializing..."
	case InitDone:
		return "Ready"
	case InitError:
		return "Init failed"
	case InitNoDaemon:
		return "Daemon not running"
	default:
		return "Unknown"
	}
}

// --- UI message types (sent from session/agent to UI via tea.Program.Send or events) ---

// StreamChunkMsg carries a text delta from the streaming API.
type StreamChunkMsg struct{ Text string }

// StreamDoneMsg signals that streaming is complete.
type StreamDoneMsg struct {
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	Elapsed             time.Duration
}

// ToolCallMsg indicates a tool call is starting.
type ToolCallMsg struct {
	Name    string
	Summary string
}

// ToolResultMsg carries the result of a tool execution.
type ToolResultMsg struct {
	Name    string
	Output  string
	IsError bool
}

// ConfirmRequestMsg asks the user to approve a tool execution.
type ConfirmRequestMsg struct {
	ToolName string
	Params   map[string]any
}

// ErrorMsg carries an error from the agent.
type ErrorMsg struct{ Err error }

// AgentDoneMsg signals the agent has finished its turn.
type AgentDoneMsg struct{}

// ClearMsg signals that the conversation should be cleared.
type ClearMsg struct{}

// QuitMsg signals the program should exit.
type QuitMsg struct{}

// InitStateMsg carries the brain init state to the UI.
type InitStateMsg struct{ State int }

// PlanProposedMsg signals a new plan is ready for review.
type PlanProposedMsg struct{ Plan *Plan }

// PlanTaskStartMsg signals a plan task is starting execution.
type PlanTaskStartMsg struct {
	TaskIdx int
	Title   string
	Total   int
}

// PlanTaskDoneMsg signals a plan task has finished.
type PlanTaskDoneMsg struct {
	TaskIdx int
	Title   string
	Success bool
	Summary string
}

// PlanCompleteMsg signals all plan tasks are done.
type PlanCompleteMsg struct{ Plan *Plan }

// SubagentStartMsg signals that a subagent is starting.
type SubagentStartMsg struct {
	Name       string
	Background bool
	TaskID     string
}

// SubagentDoneMsg signals that a subagent has finished.
type SubagentDoneMsg struct {
	Name   string
	TaskID string
}

// DaemonEventMsg wraps a session event received from the daemon.
type DaemonEventMsg struct {
	Event SessionEvent
}

// DaemonDisconnectedMsg signals the daemon connection was lost.
type DaemonDisconnectedMsg struct{}

// DaemonStatusMsg carries daemon connection status.
type DaemonStatusMsg struct {
	Connected bool
}
