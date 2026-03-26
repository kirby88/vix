package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/kirby88/vix/internal/protocol"
)

// ToolResult holds the result of a tool execution from the daemon.
type ToolResult struct {
	Output            string
	IsError           bool
	NeedsConfirmation bool
	ToolName          string
	Params            map[string]any
	LineOffset        int
}

// Client communicates with the vix daemon over a Unix socket.
// Used for one-shot commands (ping, brain context, etc.)
type Client struct {
	socketPath string
}

// NewClient creates a new daemon client.
func NewClient(path string) *Client {
	return &Client{socketPath: path}
}

// sendRequest sends a JSON request to the daemon and returns the parsed response.
func (c *Client) sendRequest(data map[string]any) (map[string]any, error) {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("daemon connect: %w", err)
	}
	defer conn.Close()

	// Set deadline to prevent permanent hangs (bash tool has 120s timeout + margin)
	if err := conn.SetDeadline(time.Now().Add(150 * time.Second)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	// Write JSON + newline
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	payload = append(payload, '\n')
	if _, err := conn.Write(payload); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Shutdown write side so daemon sees EOF
	if uc, ok := conn.(*net.UnixConn); ok {
		if err := uc.CloseWrite(); err != nil {
			return nil, fmt.Errorf("close write: %w", err)
		}
	}

	// Read full response
	buf, err := io.ReadAll(conn)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(buf, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return resp, nil
}

// Ping checks if the daemon is running.
func (c *Client) Ping() bool {
	resp, err := c.sendRequest(map[string]any{"action": "ping"})
	if err != nil {
		return false
	}
	return resp["status"] == "ok"
}

// ExecuteTool sends a tool execution request to the daemon.
func (c *Client) ExecuteTool(name string, params map[string]any, cwd string) (*ToolResult, error) {
	p := make(map[string]any, len(params)+1)
	for k, v := range params {
		p[k] = v
	}
	if name == "bash" || name == "grep" || name == "glob_files" || name == "lsp_query" {
		p["cwd"] = cwd
	}

	resp, err := c.sendRequest(map[string]any{
		"command": "tool." + name,
		"params":  p,
	})
	if err != nil {
		return nil, err
	}

	if resp["status"] != "ok" {
		msg, _ := resp["message"].(string)
		return &ToolResult{Output: fmt.Sprintf("Daemon error: %s", msg), IsError: true}, nil
	}

	data, _ := resp["data"].(map[string]any)

	if confirm, ok := data["confirm"].(bool); ok && confirm {
		return &ToolResult{
			NeedsConfirmation: true,
			ToolName:          name,
			Params:            params,
		}, nil
	}

	output, _ := data["output"].(string)
	isError, _ := data["is_error"].(bool)
	return &ToolResult{Output: output, IsError: isError}, nil
}

// ExecuteToolConfirmed re-sends a tool request with the confirmed flag.
func (c *Client) ExecuteToolConfirmed(name string, params map[string]any, cwd string) (*ToolResult, error) {
	p := make(map[string]any, len(params)+2)
	for k, v := range params {
		p[k] = v
	}
	p["confirmed"] = true
	if name == "bash" || name == "grep" || name == "glob_files" || name == "lsp_query" {
		p["cwd"] = cwd
	}

	resp, err := c.sendRequest(map[string]any{
		"command": "tool." + name,
		"params":  p,
	})
	if err != nil {
		return nil, err
	}

	if resp["status"] != "ok" {
		msg, _ := resp["message"].(string)
		return &ToolResult{Output: fmt.Sprintf("Daemon error: %s", msg), IsError: true}, nil
	}

	data, _ := resp["data"].(map[string]any)
	output, _ := data["output"].(string)
	isError, _ := data["is_error"].(bool)
	return &ToolResult{Output: output, IsError: isError}, nil
}

// --- SessionClient: persistent bidirectional connection for agent sessions ---

// SessionClient manages a persistent connection to the daemon for agent sessions.
type SessionClient struct {
	socketPath string
	conn       net.Conn
	scanner    *bufio.Scanner
	mu         sync.Mutex // protects writes
	sessionID  string
}

// NewSessionClient creates a new session client (does not connect yet).
func NewSessionClient(socketPath string) *SessionClient {
	return &SessionClient{socketPath: socketPath}
}

// SessionID returns the session ID assigned by the daemon.
func (sc *SessionClient) SessionID() string {
	return sc.sessionID
}

// Connect establishes a persistent connection and starts an agent session.
func (sc *SessionClient) Connect(cwd, model string, forceInit bool) error {
	conn, err := net.Dial("unix", sc.socketPath)
	if err != nil {
		return fmt.Errorf("daemon connect: %w", err)
	}
	sc.conn = conn
	sc.scanner = bufio.NewScanner(conn)
	sc.scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB buffer

	// Send session.start command
	startData, _ := json.Marshal(protocol.SessionStartData{
		CWD:       cwd,
		Model:     model,
		ForceInit: forceInit,
	})
	if err := sc.sendCommand(protocol.SessionCommand{
		Type: "session.start",
		Data: startData,
	}); err != nil {
		conn.Close()
		return fmt.Errorf("send session.start: %w", err)
	}

	// Read session_started event
	event, err := sc.ReadEvent()
	if err != nil {
		conn.Close()
		return fmt.Errorf("read session_started: %w", err)
	}
	if event.Type == "event.error" {
		conn.Close()
		data, _ := json.Marshal(event.Data)
		return fmt.Errorf("session start failed: %s", string(data))
	}
	if event.Type == "event.session_started" {
		data, _ := json.Marshal(event.Data)
		var started protocol.EventSessionStarted
		json.Unmarshal(data, &started)
		sc.sessionID = started.SessionID
	}

	return nil
}

// SendInput sends user chat input with optional attachments.
func (sc *SessionClient) SendInput(text string, attachments []protocol.Attachment) error {
	data, _ := json.Marshal(protocol.SessionInputData{Text: text, Attachments: attachments})
	return sc.sendCommand(protocol.SessionCommand{
		Type: "session.input",
		Data: data,
	})
}

// SendWorkflow sends a workflow execution request with a prompt.
func (sc *SessionClient) SendWorkflow(name, text string) error {
	data, _ := json.Marshal(protocol.SessionWorkflowData{Name: name, Text: text})
	return sc.sendCommand(protocol.SessionCommand{
		Type: "session.workflow",
		Data: data,
	})
}

// SendConfirm sends tool approval/denial.
func (sc *SessionClient) SendConfirm(approved bool) error {
	data, _ := json.Marshal(protocol.SessionConfirmData{Approved: approved})
	return sc.sendCommand(protocol.SessionCommand{
		Type: "session.confirm",
		Data: data,
	})
}

// SendPlanAction sends a plan review decision.
func (sc *SessionClient) SendPlanAction(action string, text string) error {
	data, _ := json.Marshal(protocol.SessionPlanActionData{Action: action, Text: text})
	return sc.sendCommand(protocol.SessionCommand{
		Type: "session.plan_action",
		Data: data,
	})
}

// SendUserAnswer sends the user's answer to a question.
// The text parameter carries additional user input when has_user_input is used.
func (sc *SessionClient) SendUserAnswer(answer string, text string) error {
	data, _ := json.Marshal(protocol.SessionUserAnswerData{Answer: answer, Text: text})
	return sc.sendCommand(protocol.SessionCommand{
		Type: "session.user_answer",
		Data: data,
	})
}

// SendUserAnswerBatch sends batch answers (question ID → answer) for multi-question mode.
func (sc *SessionClient) SendUserAnswerBatch(answers map[string]string) error {
	data, _ := json.Marshal(protocol.SessionUserAnswerData{Answers: answers})
	return sc.sendCommand(protocol.SessionCommand{
		Type: "session.user_answer",
		Data: data,
	})
}

// SendCancel cancels the current work.
func (sc *SessionClient) SendCancel() error {
	return sc.sendCommand(protocol.SessionCommand{
		Type: "session.cancel",
	})
}

// SendClose ends the session.
func (sc *SessionClient) SendClose() error {
	err := sc.sendCommand(protocol.SessionCommand{
		Type: "session.close",
	})
	if sc.conn != nil {
		sc.conn.Close()
	}
	return err
}

// ReadEvent reads the next event from the daemon.
func (sc *SessionClient) ReadEvent() (protocol.SessionEvent, error) {
	if !sc.scanner.Scan() {
		err := sc.scanner.Err()
		if err == nil {
			err = fmt.Errorf("connection closed")
		}
		return protocol.SessionEvent{}, err
	}

	var event protocol.SessionEvent
	if err := json.Unmarshal(sc.scanner.Bytes(), &event); err != nil {
		return protocol.SessionEvent{}, fmt.Errorf("parse event: %w", err)
	}
	return event, nil
}

// Close closes the underlying connection.
func (sc *SessionClient) Close() {
	if sc.conn != nil {
		sc.conn.Close()
	}
}

func (sc *SessionClient) sendCommand(cmd protocol.SessionCommand) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.conn == nil {
		return fmt.Errorf("not connected")
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = sc.conn.Write(data)
	return err
}
