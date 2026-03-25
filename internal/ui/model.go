package ui

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/screen"

	"github.com/kirby88/vix/internal/config"
	"github.com/kirby88/vix/internal/daemon"
	"github.com/kirby88/vix/internal/protocol"
)

// teaProgram holds the Bubble Tea program reference for event injection via Send().
var teaProgram *tea.Program

// SetProgram stores the tea.Program reference. Call before p.Run().
func SetProgram(p *tea.Program) { teaProgram = p }

// resumeFromSleepMsg is sent when the process receives SIGCONT after a sleep/wake cycle.
type resumeFromSleepMsg struct{}

// clearModeWarningMsg clears the temporary mode switch warning.
type clearModeWarningMsg struct{}

// startCursorBlinkMsg triggers cursor blink from Update (pointer receiver)
// since Init has a value receiver and can't properly initialize the blink chain.
type startCursorBlinkMsg struct{}

// waitForResume blocks until the process receives SIGCONT (e.g. after laptop sleep/wake).
func waitForResume() tea.Msg {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGCONT)
	<-sigCh
	signal.Stop(sigCh)
	return resumeFromSleepMsg{}
}

// reconnectSuccessMsg is sent when reconnection succeeds.
type reconnectSuccessMsg struct {
	session *daemon.SessionClient
}

// reconnectFailedMsg is sent when reconnection fails.
type reconnectFailedMsg struct{}

// attemptReconnect tries to reconnect to the daemon.
func attemptReconnect(socketPath, cwd, model string, forceInit bool) tea.Cmd {
	return func() tea.Msg {
		// First check if daemon is responding
		client := daemon.NewClient(socketPath)
		if !client.Ping() {
			// Daemon not ready yet, schedule another attempt
			time.Sleep(2 * time.Second)
			return reconnectFailedMsg{}
		}

		// Daemon is up, try to establish session
		session := daemon.NewSessionClient(socketPath)
		if err := session.Connect(cwd, model, forceInit); err != nil {
			time.Sleep(2 * time.Second)
			return reconnectFailedMsg{}
		}

		return reconnectSuccessMsg{session: session}
	}
}

// AppState represents the current state of the application.
type AppState int

const (
	StateWaitingForInput AppState = iota
	StateStreaming
	StateToolExecuting
	StateConfirmPending
	StatePlanReview
	StatePlanExecuting
	StateUserQuestion
)

// Model is the root Bubble Tea model.
type Model struct {
	width, height int
	state          AppState
	activeWorkflow string // empty = chat, non-empty = workflow trigger
	workflows      []protocol.WorkflowInfo

	// Components
	input          textarea.Model
	spinner        spinner.Model
	commandPalette CommandPalette
	fileCompleter  FileCompleter

	// Focus
	focus FocusState

	// Chat messages
	chatMessages     []ChatMessage
	chatScrollOffset int // 0 = bottom (auto-scroll), >0 = lines from bottom

	// Streaming state
	assistantBuf      string
	assistantRendered string

	// Session client for daemon communication
	session *daemon.SessionClient

	// Plan state
	activePlan *protocol.Plan

	// Display state
	modelName           string
	inputTokens         int64
	outputTokens        int64
	cacheCreationTokens int64
	cacheReadTokens     int64
	lastOutputTokens    int64
	elapsed             time.Duration
	connected           bool
	reconnecting        bool
	initState           protocol.InitState

	// Confirm state
	confirmToolName string

	// User question state
	questionPanel QuestionPanel

	// Parallel tool tracking: ToolID → index in chatMessages
	pendingTools map[string]int

	// History panel
	historyPanel HistoryPanel

	// Attachment panel
	attachmentPanel AttachmentPanel

	// Keyboard
	kittySupported bool // true if terminal supports kitty keyboard protocol

	// Mode switch warning
	modeWarning string

	// Sidebar visibility
	sidebarHidden bool

	// Theme
	hasDarkBG bool
	styles    Styles

	// Thinking animation
	thinkingAnim ThinkingAnim

	// Helpers
	history    *History
	mdRenderer *MarkdownRenderer

	// Config
	cfg      *config.Config
	testMode bool

	// Connection parameters for reconnection
	socketPath string
	cwd        string

}

// NewModel creates a new root Model.
func NewModel(cfg *config.Config, session *daemon.SessionClient, testMode bool) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(colorPrimary)

	m := Model{
		state:          StateWaitingForInput,
		input:          newInput(),
		spinner:        s,
		thinkingAnim:   NewThinkingAnim(),
		commandPalette: NewCommandPalette(),
		focus:          FocusEditor,
		testMode:       testMode,
		session:        session,
		connected:      session != nil,
		modelName:      cfg.Model,
		hasDarkBG:      true,
		styles:         NewStyles(true),
		history:        NewHistory(),
		mdRenderer:     NewMarkdownRenderer(80, true),
		cfg:            cfg,
		socketPath:     cfg.SocketPath,
		cwd:            cfg.CWD,
		sidebarHidden:  true,
	}

	if testMode {
		m.fillTestData()
	}

	return m
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	m.input.Reset()
	if m.testMode {
		return m.spinner.Tick
	}
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg { return startCursorBlinkMsg{} },
		m.startEventLoop(),
		waitForResume,
		tea.RequestBackgroundColor,
	)
}

// startEventLoop launches a goroutine that reads daemon events and injects them
// into the Bubble Tea event loop via program.Send(). This matches crush's pattern
// and ensures each event triggers an immediate Update+View cycle.
func (m Model) startEventLoop() tea.Cmd {
	return func() tea.Msg {
		if m.session == nil || teaProgram == nil {
			return protocol.DaemonDisconnectedMsg{}
		}
		session := m.session
		go func() {
			for {
				event, err := session.ReadEvent()
				if err != nil {
					teaProgram.Send(protocol.DaemonDisconnectedMsg{})
					return
				}
				teaProgram.Send(protocol.DaemonEventMsg{Event: event})
			}
		}()
		return nil
	}
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(m.width - 4)
		m.questionPanel.SetWidth(m.width)
		m.mdRenderer.UpdateWidth(m.width - 4)


		return m, nil

	case tea.KeyPressMsg:
		// Ctrl+C/D always quits, regardless of focus or panel state
		if msg.String() == "ctrl+c" || msg.String() == "ctrl+d" {
			if m.session != nil {
				m.session.SendCancel()
				m.session.SendClose()
			}
			return m, tea.Quit
		}

		// History panel intercepts all keys when visible
		if m.historyPanel.IsVisible() {
			switch msg.String() {
			case "up", "k":
				m.historyPanel.MoveUp()
			case "down", "j":
				m.historyPanel.MoveDown(len(m.history.entries))
			case "enter":
				if m.historyPanel.selected >= 0 && m.historyPanel.selected < len(m.history.entries) {
					m.input.Reset()
					m.input.InsertString(m.history.entries[m.historyPanel.selected])
					m.input.SetHeight(m.visualLineCount())
				}
				m.historyPanel.Close()
			case "esc":
				m.historyPanel.Close()
			default:
				m.historyPanel.Close()
			}
			return m, nil
		}

		// Command palette intercepts all keys when visible
		if m.commandPalette.IsVisible() {
			action, _ := m.commandPalette.Update(msg)
			switch action {
			case "clear":
				if m.session != nil {
					m.session.SendCancel()
				}
				m.chatMessages = nil
				m.assistantBuf = ""
				m.assistantRendered = ""
		
			case "history":
				if len(m.history.entries) > 0 {
					m.historyPanel.Open(len(m.history.entries), m.height)
				}
			case "scroll_top":
				m.chatScrollOffset = m.maxScrollOffset()
				m.focus = FocusChat
			case "scroll_bottom":
				m.chatScrollOffset = 0
				m.focus = FocusChat
			case "toggle_sidebar":
				m.sidebarHidden = !m.sidebarHidden
			case "quit":
				if m.session != nil {
					m.session.SendCancel()
					m.session.SendClose()
				}
				return m, tea.Quit
			}
			if !m.commandPalette.IsVisible() {
				m.input.Focus()
				m.focus = FocusEditor
			}
	
			return m, nil
		}

		// Attachment panel intercepts keys when focused
		if m.attachmentPanel.IsFocused() {
			switch msg.String() {
			case "up", "k":
				m.attachmentPanel.MoveUp()
			case "down", "j":
				m.attachmentPanel.MoveDown()
			case "delete", "backspace":
				m.attachmentPanel.Remove(m.attachmentPanel.selected)
			case "enter":
				// Do nothing — prevent submit while panel is focused
			case "tab":
				m.attachmentPanel.Unfocus()
				m.focus = FocusChat
				m.input.Blur()
			case "esc":
				m.attachmentPanel.Unfocus()
				m.focus = FocusEditor
				m.input.Focus()
			default:
				m.attachmentPanel.Unfocus()
				m.input.Focus()
				// Fall through to let the key be processed by textarea below
				goto processKey
			}
			return m, nil
		}
	processKey:

		// File completer intercepts navigation and selection keys when visible
		if m.fileCompleter.IsVisible() {
			switch msg.String() {
			case "up":
				m.fileCompleter.MoveUp()
				return m, nil
			case "down":
				m.fileCompleter.MoveDown()
				return m, nil
			case "esc":
				m.fileCompleter.Close()
				return m, nil
			case "enter", "tab":
				entry := m.fileCompleter.SelectedEntry()
				if entry == nil {
					m.fileCompleter.Close()
					return m, nil
				}
				if entry.IsDir() {
					m.fileCompleter.Descend(entry)
					// Keep the @ prefix so extractAtQuery still finds an active token
					// on the next keypress and the completer stays open.
					newPath := "@" + m.fileCompleter.currentDir + "/"
					m.input.SetValue(replaceAtToken(m.input.Value(), newPath))
					m.input.MoveToEnd()
				} else {
					path := m.fileCompleter.SelectedPath()
					m.input.SetValue(replaceAtToken(m.input.Value(), path))
					m.input.MoveToEnd()
					m.fileCompleter.Close()
				}
				newHeight := m.visualLineCount()
				if newHeight != m.input.Height() {
					m.input.SetHeight(newHeight)
				}
				return m, nil
			}
			// Any other key falls through to textarea update below
		}

		// Tab always handles focus switching, before any panel intercepts
		if msg.String() == "tab" {
			if m.state == StateWaitingForInput || m.state == StatePlanReview || m.state == StateUserQuestion ||
				m.state == StateStreaming || m.state == StateToolExecuting {
				switch m.focus {
				case FocusEditor:
					if m.attachmentPanel.IsVisible() {
						m.attachmentPanel.Focus()
						m.input.Blur()
					} else {
						m.focus = FocusChat
						m.input.Blur()
					}
				case FocusChat:
					m.focus = FocusEditor
					m.input.Focus()
				}
			}
			return m, nil
		}

		// Question panel intercepts keys only when it has focus (editor focus)
		if m.state == StateUserQuestion && m.questionPanel.IsVisible() && m.focus == FocusEditor {
			result, answer, batchAnswers := m.questionPanel.HandleKey(msg)
			switch result {
			case QPSubmitted:
				if batchAnswers != nil {
					pairs := m.questionPanel.GetAnsweredPairs()
					m.chatMessages = append(m.chatMessages, renderQuestionAnswer(pairs, m.styles))
					if m.session != nil {
						m.session.SendUserAnswerBatch(batchAnswers)
					}
				} else {
					answerText := m.questionPanel.CurrentAnswerText()
					tab := m.questionPanel.CurrentTab()
					displayAnswer := answer
					if answerText != "" {
						displayAnswer = answer + ": " + answerText
					}
					pairs := []QAPair{{Category: tab.Category, Question: tab.Question, Answer: displayAnswer}}
					m.chatMessages = append(m.chatMessages, renderQuestionAnswer(pairs, m.styles))
					if m.session != nil {
						m.session.SendUserAnswer(answer, answerText)
					}
				}
				m.questionPanel.Close()
				m.state = StateStreaming
		
				return m, nil
			case QPCancelled:
				if m.session != nil {
					m.session.SendUserAnswer("", "")
				}
				m.questionPanel.Close()
				m.state = StateStreaming
			}
			return m, nil
		}

		// Shift+Enter inserts a newline; ctrl+j is what iTerm2 sends for shift+enter;
		// alt+enter is a universal fallback
		if msg.String() == "shift+enter" || msg.String() == "alt+enter" || msg.String() == "ctrl+j" {
			if m.state == StateWaitingForInput || m.state == StatePlanReview {
				m.input.InsertString("\n")
				newHeight := m.visualLineCount()
				if newHeight != m.input.Height() {
					m.input.SetHeight(newHeight)
				}
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+shift+u":
			if m.state == StateWaitingForInput || m.state == StatePlanReview {
				m.input.SetValue("")
				m.input.SetHeight(1)
			}
			return m, nil

		case "ctrl+r":
			if m.state == StateWaitingForInput && len(m.history.entries) > 0 {
				m.historyPanel.Open(len(m.history.entries), m.height)
			}
			return m, nil

		case "ctrl+p":
			if m.state == StateWaitingForInput || m.state == StatePlanReview {
				m.commandPalette.Open()
				m.input.Blur()
			}
			return m, nil

		case "shift+tab":
			if m.state == StateWaitingForInput && len(m.workflows) > 0 {
				m.activeWorkflow = m.nextWorkflow()
				m.input.Placeholder = m.placeholderForMode()
				m.updateInputPromptColor()
				m.modeWarning = "Context is not shared between Chat and workflows"
				return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearModeWarningMsg{} })
			}
			return m, nil

		case "ctrl+c", "ctrl+d":
			if m.session != nil {
				m.session.SendCancel()
				m.session.SendClose()
			}
			return m, tea.Quit

		case "enter":
			if m.state == StateConfirmPending {
				if m.session != nil {
					m.session.SendConfirm(true)
				}
				m.state = StateToolExecuting
				return m, nil
			}

			if m.state == StatePlanReview {
				text := strings.TrimSpace(m.input.Value())
				if text == "" {
					if m.session != nil {
						m.session.SendPlanAction("approve", "")
					}
					m.state = StateStreaming
				} else {
					m.input.Reset()
					m.input.SetHeight(1)
					if m.session != nil {
						m.session.SendPlanAction("modify", text)
					}
					m.state = StateStreaming
				}
		
				return m, nil
			}

			if m.state == StateWaitingForInput {
				text := strings.TrimSpace(m.input.Value())
				if text == "" && m.attachmentPanel.Count() == 0 {
					return m, nil
				}
				if text != "" {
					m.history.Save(text)
				}
				m.input.Reset()
				m.input.SetHeight(1)

				// Collect panel attachments first
				panelAtts := m.attachmentPanel.Clear()

				displayText, textAtts, imgErrs := extractImageAttachments(text)
				for _, e := range imgErrs {
					m.chatMessages = append(m.chatMessages, renderErrorMessage(fmt.Errorf("%s", e)))
				}

				// Combine: panel attachments come first, then any from text
				attachments := append(panelAtts, textAtts...)

				chatContentWidth := m.computeLayoutWithSidebar(m.visualLineCount()).ChatWidth - 2
				m.chatMessages = append(m.chatMessages, renderUserMessage(displayText, chatContentWidth))
				m.chatScrollOffset = 0 // auto-scroll to bottom

				m.state = StateStreaming
				animCmd := m.thinkingAnim.Start()

				if m.session != nil {
					if m.activeWorkflow != "" {
						m.session.SendWorkflow(m.activeWorkflow, displayText)
					} else {
						m.session.SendInput(displayText, attachments)
					}
				}
				return m, animCmd
			}
			return m, nil

		case "y", "Y":
			if m.state == StateConfirmPending {
				if m.session != nil {
					m.session.SendConfirm(true)
				}
				m.state = StateToolExecuting
				return m, nil
			}
			if m.state == StatePlanReview && m.input.Value() == "" {
				if m.session != nil {
					m.session.SendPlanAction("approve", "")
				}
				m.state = StateStreaming
		
				return m, nil
			}

		case "esc":
			if m.state == StateStreaming || m.state == StateToolExecuting || m.state == StatePlanExecuting {
				m.thinkingAnim.Stop()
				if m.session != nil {
					m.session.SendCancel()
				}
				if m.assistantBuf != "" {
					m.chatMessages = append(m.chatMessages, renderAssistantMessage(m.assistantBuf, m.mdRenderer))
					m.assistantBuf = ""
					m.assistantRendered = ""
				}
				m.chatMessages = append(m.chatMessages, renderSystemMessage("Cancelled.", m.styles))
		
				return m, nil
			}
			if m.state == StateConfirmPending {
				if m.session != nil {
					m.session.SendConfirm(false)
				}
				m.state = StateToolExecuting
				return m, nil
			}
			if m.state == StatePlanReview && m.input.Value() == "" {
				if m.session != nil {
					m.session.SendPlanAction("reject", "")
				}
				m.state = StateWaitingForInput
				m.input.Focus()
		
				return m, nil
			}

		case "n", "N":
			if m.state == StateConfirmPending {
				if m.session != nil {
					m.session.SendConfirm(false)
				}
				m.state = StateToolExecuting
				return m, nil
			}
			if m.state == StatePlanReview && m.input.Value() == "" {
				if m.session != nil {
					m.session.SendPlanAction("reject", "")
				}
				m.state = StateWaitingForInput
				m.input.Focus()
		
				return m, nil
			}
		}

		if m.state == StateConfirmPending {
			return m, nil
		}

		// Chat viewport focus: forward navigation keys
		if m.focus == FocusChat {
			switch msg.String() {
			case "up", "k":
				m.chatScrollOffset += 3
			case "down", "j":
				m.chatScrollOffset -= 3
			case "pgup", "b":
				m.chatScrollOffset += 20
			case "pgdown", "f":
				m.chatScrollOffset -= 20
			case "home", "g":
				m.chatScrollOffset = m.maxScrollOffset()
			case "end", "G":
				m.chatScrollOffset = 0
			}
			m.clampScrollOffset()
			return m, nil
		}

		if m.state == StateWaitingForInput || m.state == StatePlanReview {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)

			// Detect @ file-path query and drive the file completer popup
			query, found := extractAtQuery(m.input.Value())
			if found {
				dir, prefix := resolveAtDir(query, m.cwd)
				if m.fileCompleter.IsVisible() && dir == m.fileCompleter.currentDir {
					m.fileCompleter.Refresh(prefix)
				} else {
					m.fileCompleter.Open(dir, prefix)
				}
			} else {
				m.fileCompleter.Close()
			}

			newHeight := m.visualLineCount()
			if newHeight != m.input.Height() {
				m.input.SetHeight(newHeight)
			}
			return m, cmd
		}

		return m, nil

	// Daemon events from the session
	case protocol.DaemonEventMsg:
		return m.handleDaemonEvent(msg.Event)

	case protocol.DaemonDisconnectedMsg:
		m.connected = false
		m.reconnecting = true
		m.chatMessages = append(m.chatMessages, renderErrorMessage(fmt.Errorf("daemon connection lost")))
		m.state = StateWaitingForInput

		return m, attemptReconnect(m.socketPath, m.cwd, m.cfg.Model, m.cfg.ForceInit)

	case reconnectSuccessMsg:
		m.session = msg.session
		m.connected = true
		m.reconnecting = false
		m.chatMessages = append(m.chatMessages, renderSystemSuccessMessage("Reconnected to daemon."))

		return m, m.startEventLoop()

	case reconnectFailedMsg:
		if m.reconnecting {
			return m, attemptReconnect(m.socketPath, m.cwd, m.cfg.Model, m.cfg.ForceInit)
		}
		return m, nil

	case tea.PasteMsg:
		if m.state == StateWaitingForInput || m.state == StatePlanReview {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)

			// Intercept image paths from drag-and-drop
			val := m.input.Value()
			_, atts, _ := extractImageAttachments(val)
			if len(atts) > 0 {
				for i := range atts {
					m.attachmentPanel.Add(atts[i])
				}
				// Remove matched paths from input (without [Image #N] placeholders)
				stripped := imagePathPattern.ReplaceAllString(val, "")
				stripped = strings.TrimSpace(stripped)
				m.input.SetValue(stripped)
			}

			newHeight := m.visualLineCount()
			if newHeight != m.input.Height() {
				m.input.SetHeight(newHeight)
			}
			m.input.MoveToBegin()
			m.input.MoveToEnd()
			return m, cmd
		}

	case tea.KeyboardEnhancementsMsg:
		m.kittySupported = msg.SupportsKeyDisambiguation()

	case tea.BackgroundColorMsg:
		m.hasDarkBG = msg.IsDark()
		m.styles = NewStyles(m.hasDarkBG)
		m.mdRenderer = NewMarkdownRenderer(m.mdRenderer.width, m.hasDarkBG)
		return m, nil

	case resumeFromSleepMsg:
		return m, tea.Batch(tea.ClearScreen, tea.RequestWindowSize, waitForResume)

	case clearModeWarningMsg:
		m.modeWarning = ""
		return m, nil

	case startCursorBlinkMsg:
		blinkCmd := m.input.Focus()
		return m, blinkCmd

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case animStepMsg:
		cmd := m.thinkingAnim.Advance()
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}

	// Forward unhandled messages to textarea for cursor blink
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if cmd != nil {
		return m, cmd
	}
	return m, nil
}

// handleDaemonEvent processes a session event from the daemon.
func (m Model) handleDaemonEvent(event protocol.SessionEvent) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch event.Type {
	case "event.session_started":
		m.connected = true

	case "event.init_state":
		data := marshalData(event.Data)
		var state protocol.EventInitState
		json.Unmarshal(data, &state)
		m.initState = protocol.InitState(state.State)

	case "event.workflows_available":
		data := marshalData(event.Data)
		var wa protocol.EventWorkflowsAvailable
		json.Unmarshal(data, &wa)
		m.workflows = wa.Workflows
		// Reset activeWorkflow if it's no longer valid
		if m.activeWorkflow != "" {
			found := false
			for _, w := range m.workflows {
				if w.Name == m.activeWorkflow {
					found = true
					break
				}
			}
			if !found {
				m.activeWorkflow = ""
			}
		}

	case "event.stream_chunk":
		m.thinkingAnim.Stop()
		data := marshalData(event.Data)
		var chunk protocol.EventStreamChunk
		json.Unmarshal(data, &chunk)
		m.assistantBuf += chunk.Text
		m.assistantRendered = m.mdRenderer.Render(m.assistantBuf)

	case "event.stream_done":
		// Don't flush assistantBuf here — keep content visible via
		// assistantRendered until a logical boundary (agent_done,
		// workflow_step_done) or until a tool_call needs proper ordering.
		// This preserves the streaming display and avoids gap/duplication.
		data := marshalData(event.Data)
		var done protocol.EventStreamDone
		json.Unmarshal(data, &done)
		m.inputTokens += done.InputTokens
		m.outputTokens += done.OutputTokens
		m.cacheCreationTokens += done.CacheCreationTokens
		m.cacheReadTokens += done.CacheReadTokens
		if done.ElapsedMs > 0 {
			m.lastOutputTokens = done.OutputTokens
			m.elapsed = time.Duration(done.ElapsedMs) * time.Millisecond
		}

	case "event.tool_call":
		m.thinkingAnim.Stop()
		// Flush streaming buffer before adding tool call to preserve message ordering
		if m.assistantBuf != "" {
			m.chatMessages = append(m.chatMessages, renderAssistantMessage(m.assistantBuf, m.mdRenderer))
			m.assistantBuf = ""
			m.assistantRendered = ""
		}
		m.state = StateToolExecuting
		data := marshalData(event.Data)
		var tc protocol.EventToolCall
		json.Unmarshal(data, &tc)
		idx := len(m.chatMessages)
		m.chatMessages = append(m.chatMessages, renderToolCall(tc.Name, tc.Summary, tc.Reason, m.styles))
		if tc.ToolID != "" {
			if m.pendingTools == nil {
				m.pendingTools = make(map[string]int)
			}
			m.pendingTools[tc.ToolID] = idx
		}
		// Keep in live area (shows spinner) — committed when result arrives

	case "event.tool_result":
		data := marshalData(event.Data)
		var tr protocol.EventToolResult
		json.Unmarshal(data, &tr)
		hasPending := len(m.pendingTools) > 1
		result := renderToolResultWithContext(tr.Name, tr.Output, tr.IsError, hasPending, tr.Detail, m.styles)

		if tr.ToolID != "" && m.pendingTools != nil {
			if callIdx, ok := m.pendingTools[tr.ToolID]; ok {
				insertIdx := callIdx + 1
				m.chatMessages = append(m.chatMessages, ChatMessage{})
				copy(m.chatMessages[insertIdx+1:], m.chatMessages[insertIdx:])
				m.chatMessages[insertIdx] = result
				delete(m.pendingTools, tr.ToolID)
				for id, idx := range m.pendingTools {
					if idx >= insertIdx {
						m.pendingTools[id] = idx + 1
					}
				}
			} else {
				m.chatMessages = append(m.chatMessages, result)
			}
		} else {
			m.chatMessages = append(m.chatMessages, result)
		}
		// Viewport will be rebuilt in View()

	case "event.confirm_request":
		m.state = StateConfirmPending
		data := marshalData(event.Data)
		var cr protocol.EventConfirmRequest
		json.Unmarshal(data, &cr)
		m.confirmToolName = cr.ToolName

	case "event.user_question":
		data := marshalData(event.Data)
		var uq protocol.EventUserQuestion
		json.Unmarshal(data, &uq)
		m.questionPanel.Open(uq, m.width)
		m.state = StateUserQuestion

	case "event.plan_proposed":
		data := marshalData(event.Data)
		var pp protocol.EventPlanProposed
		json.Unmarshal(data, &pp)
		m.activePlan = pp.Plan
		m.state = StatePlanReview
		m.chatMessages = append(m.chatMessages, renderPlanProposal(pp.Plan, m.styles))

		m.input.Focus()
		m.input.Placeholder = "Type modifications (Enter to send, Shift+Enter or Alt+Enter for new line) or press y/n..."

	case "event.plan_task_start":
		m.state = StatePlanExecuting
		data := marshalData(event.Data)
		var pts protocol.EventPlanTaskStart
		json.Unmarshal(data, &pts)
		m.chatMessages = append(m.chatMessages, renderPlanTaskStart(pts.TaskIdx, pts.Title, pts.Total))
		// Stays live until plan_complete

	case "event.plan_task_done":
		data := marshalData(event.Data)
		var ptd protocol.EventPlanTaskDone
		json.Unmarshal(data, &ptd)
		m.chatMessages = append(m.chatMessages, renderPlanTaskDone(ptd.TaskIdx, ptd.Title, ptd.Success, ptd.Summary, m.styles))
		// Stays live until plan_complete

	case "event.plan_complete":
		data := marshalData(event.Data)
		var pc protocol.EventPlanComplete
		json.Unmarshal(data, &pc)
		m.activePlan = nil
		m.chatMessages = append(m.chatMessages, renderPlanSummary(pc.Plan))


	case "event.workflow_start":
		data := marshalData(event.Data)
		var ps protocol.EventWorkflowStart
		json.Unmarshal(data, &ps)
		m.chatMessages = append(m.chatMessages, renderWorkflowStart(ps.WorkflowName, ps.TotalSteps, m.styles))
		// Stays live until step_done

	case "event.workflow_step_start":
		m.state = StateStreaming
		data := marshalData(event.Data)
		var pss protocol.EventWorkflowStepStart
		json.Unmarshal(data, &pss)
		m.chatMessages = append(m.chatMessages, renderWorkflowStepStart(pss.StepID, pss.StepIdx, pss.Total, pss.Explanation))
		// Stays live until step_done

	case "event.workflow_step_done":
		// Flush streaming buffer so content is rendered with markdown and committed
		if m.assistantBuf != "" {
			m.chatMessages = append(m.chatMessages, renderAssistantMessage(m.assistantBuf, m.mdRenderer))
			m.assistantBuf = ""
			m.assistantRendered = ""
		}
		data := marshalData(event.Data)
		var psd protocol.EventWorkflowStepDone
		json.Unmarshal(data, &psd)
		m.chatMessages = append(m.chatMessages, renderWorkflowStepDone(psd.StepID, psd.StepIdx, psd.Total, psd.Success, psd.Display, psd.ToolStats, m.mdRenderer, m.styles))


	case "event.workflow_complete":
		data := marshalData(event.Data)
		var pc protocol.EventWorkflowComplete
		json.Unmarshal(data, &pc)
		m.chatMessages = append(m.chatMessages, renderWorkflowComplete(pc.WorkflowName, pc.Success, pc.Summary, pc.StepCosts, pc.DurationMs, m.styles))


	case "event.agent_done":
		m.thinkingAnim.Stop()
		// Flush streaming buffer before committing
		if m.assistantBuf != "" {
			m.chatMessages = append(m.chatMessages, renderAssistantMessage(m.assistantBuf, m.mdRenderer))
			m.assistantBuf = ""
			m.assistantRendered = ""
		}
		// Append turn info line
		cost := protocol.CalculateCost(m.modelName, m.inputTokens, m.outputTokens, m.cacheCreationTokens, m.cacheReadTokens)
		m.chatMessages = append(m.chatMessages, renderTurnInfo(m.modelName, m.elapsed, cost, m.width, m.styles))
		m.state = StateWaitingForInput
		m.input.Focus()
		m.input.Placeholder = "Ask the agent anything... (Enter to send, Shift+Enter or Alt+Enter for new line)"


	case "event.clear":
		m.chatMessages = nil
		m.assistantBuf = ""
		m.assistantRendered = ""
		m.inputTokens = 0
		m.outputTokens = 0
		m.cacheCreationTokens = 0
		m.cacheReadTokens = 0
		m.elapsed = 0
		m.chatMessages = append(m.chatMessages, renderSystemMessage("Conversation cleared.", m.styles))


	case "event.retry":
		data := marshalData(event.Data)
		var retry protocol.EventRetry
		json.Unmarshal(data, &retry)
		if m.assistantBuf != "" {
			m.chatMessages = append(m.chatMessages, renderAssistantMessage(m.assistantBuf, m.mdRenderer))
			m.assistantBuf = ""
			m.assistantRendered = ""
		}
		m.chatMessages = append(m.chatMessages, renderRetryMessage(retry))

	case "event.error":
		data := marshalData(event.Data)
		var errEvent protocol.EventError
		json.Unmarshal(data, &errEvent)
		m.chatMessages = append(m.chatMessages, renderErrorMessage(fmt.Errorf("%s", errEvent.Message)))
		// Stays live until agent_done or disconnect

	case "event.quit":
		return m, tea.Quit
	}

	return m, tea.Batch(cmds...)
}

// marshalData converts event.Data (which may be a map[string]any after JSON round-trip) back to bytes.
func marshalData(data any) []byte {
	b, _ := json.Marshal(data)
	return b
}

// visualLineCount returns the total number of display lines the input text
// occupies, accounting for soft-wrapping by the textarea component.
func (m *Model) visualLineCount() int {
	val := m.input.Value()
	if val == "" {
		return 1
	}
	availWidth := m.width - 4 - 2 // -4 border/padding, -2 prompt
	if availWidth <= 0 {
		return m.input.LineCount()
	}
	total := 0
	for _, line := range strings.Split(val, "\n") {
		w := lipgloss.Width(line)
		if w <= availWidth {
			total++
		} else {
			total += (w + availWidth - 1) / availWidth
		}
	}
	if total < 1 {
		total = 1
	}
	if m.input.MaxHeight > 0 && total > m.input.MaxHeight {
		total = m.input.MaxHeight
	}
	return total
}

// computeLayoutWithSidebar calls computeLayout and suppresses the sidebar when sidebarHidden is set.
func (m *Model) computeLayoutWithSidebar(inputLineCount int, panelHeights ...int) Layout {
	layout := computeLayout(m.width, m.height, inputLineCount, panelHeights...)
	if m.sidebarHidden && layout.SidebarWidth > 0 {
		layout.ChatWidth += layout.SidebarWidth
		layout.SidebarWidth = 0
	}
	return layout
}

// maxScrollOffset returns the maximum valid scroll offset based on current content.
func (m *Model) maxScrollOffset() int {
	layout := m.computeLayoutWithSidebar(m.visualLineCount())
	contentHeight := layout.ChatHeight - 2
	chatContent := buildRenderedChat(m.chatMessages, m.styles)
	if m.assistantRendered != "" {
		chatContent += m.assistantRendered
	}
	if chatContent == "" && !m.testMode {
		chatContent = renderWelcomeInline(layout.ChatWidth-2, contentHeight, m.styles)
	}
	totalLines := strings.Count(chatContent, "\n") + 1
	maxOff := totalLines - contentHeight
	if maxOff < 0 {
		return 0
	}
	return maxOff
}

// clampScrollOffset ensures chatScrollOffset is within valid bounds.
func (m *Model) clampScrollOffset() {
	if m.chatScrollOffset < 0 {
		m.chatScrollOffset = 0
	}
	if max := m.maxScrollOffset(); m.chatScrollOffset > max {
		m.chatScrollOffset = max
	}
}

// View implements tea.Model — builds all content fresh each frame using UV canvas.
func (m Model) View() tea.View {
	if m.width == 0 {
		v := tea.NewView("Initializing...")
		v.AltScreen = true
		return v
	}

	// Collect panel heights for layout calculation
	var panelHeights []int
	if m.attachmentPanel.IsVisible() {
		panelHeights = append(panelHeights, m.attachmentPanel.Count()+3)
	}
	if m.historyPanel.IsVisible() {
		panelHeights = append(panelHeights, m.historyPanel.maxHeight+2)
	}
	inputLines := m.visualLineCount()
	if m.state == StateUserQuestion && m.questionPanel.IsVisible() {
		inputLines = m.questionPanel.Height()
	}
	layout := m.computeLayoutWithSidebar(inputLines, panelHeights...)

	// Create UV canvas (like crush)
	canvas := uv.NewScreenBuffer(m.width, m.height)
	screen.Clear(canvas)

	y := 0

	// 1. Chat area — build content fresh from model fields every frame
	chatContent := buildRenderedChat(m.chatMessages, m.styles)
	if m.assistantRendered != "" {
		chatContent += m.assistantRendered
	} else if animFrame := m.thinkingAnim.View(); animFrame != "" {
		chatContent += "\n" + animFrame + "\n"
	}
	if chatContent == "" && !m.testMode {
		chatContent = renderWelcomeInline(layout.ChatWidth-2, layout.ChatHeight-2, m.styles)
	}

	// Split into lines, apply scroll offset
	contentHeight := layout.ChatHeight - 2 // subtract border top+bottom
	allLines := strings.Split(chatContent, "\n")

	// Scroll: offset 0 = bottom, offset N = N lines from bottom
	endIdx := len(allLines) - m.chatScrollOffset
	if endIdx > len(allLines) {
		endIdx = len(allLines)
	}
	if endIdx < contentHeight {
		endIdx = contentHeight
		if endIdx > len(allLines) {
			endIdx = len(allLines)
		}
	}
	startIdx := endIdx - contentHeight
	if startIdx < 0 {
		startIdx = 0
	}

	chatLines := allLines[startIdx:endIdx]
	for len(chatLines) < contentHeight {
		chatLines = append(chatLines, "")
	}
	if len(chatLines) > contentHeight {
		chatLines = chatLines[:contentHeight]
	}

	// Render bordered chat box (occupies ChatWidth, leaving room for sidebar)
	var chatBorderStyle lipgloss.Style
	if m.focus == FocusChat {
		chatBorderStyle = m.styles.ViewportFocusedStyle
	} else {
		chatBorderStyle = m.styles.ViewportBlurredStyle
	}
	chatBox := chatBorderStyle.Width(layout.ChatWidth).Height(layout.ChatHeight).
		Render(strings.Join(chatLines, "\n"))
	uv.NewStyledString(chatBox).Draw(canvas, image.Rect(0, y, layout.ChatWidth, y+layout.ChatHeight))

	// Render right info panel if there is room
	if layout.SidebarWidth > 0 {
		infoPanel := renderInfoPanel(m.cwd, m.modelName, layout.SidebarWidth, layout.ChatHeight, m.styles)
		uv.NewStyledString(infoPanel).Draw(canvas, image.Rect(layout.ChatWidth, y, m.width, y+layout.ChatHeight))
	}

	y += layout.ChatHeight

	// 2. Panels between chat and input
	if m.attachmentPanel.IsVisible() {
		panel := renderAttachmentPanel(&m.attachmentPanel, m.width, m.styles)
		ph := m.attachmentPanel.Count() + 3 // entries + help text + top border + bottom border
		uv.NewStyledString(panel).Draw(canvas, image.Rect(0, y, m.width, y+ph))
		y += ph
	}
	if m.historyPanel.IsVisible() {
		panel := renderHistoryPanel(m.history.entries, m.history.times, &m.historyPanel, m.width, true, m.styles)
		ph := m.historyPanel.maxHeight + 2 // entries + top border + bottom border
		uv.NewStyledString(panel).Draw(canvas, image.Rect(0, y, m.width, y+ph))
		y += ph
	}

	// 3. Input section
	var inputSection string
	if m.state == StateUserQuestion && m.questionPanel.IsVisible() {
		inputSection = m.questionPanel.Render(m.styles, m.focus == FocusEditor)
	} else if m.state == StateConfirmPending {
		inputArea := renderConfirmPrompt(m.confirmToolName, m.width)
		inputSection = renderInputBox(m.currentModeName(), m.activeWorkflow != "", inputArea, m.width, m.focus == FocusEditor, m.styles.ColorBlurBorder)
	} else {
		inputSection = renderInputBox(m.currentModeName(), m.activeWorkflow != "", m.input.View(), m.width, m.focus == FocusEditor, m.styles.ColorBlurBorder)
	}
	inputHeight := layout.InputHeight
	uv.NewStyledString(inputSection).Draw(canvas, image.Rect(0, y, m.width, y+inputHeight))
	y += inputHeight

	// 5. Status bar
	spinning := m.state == StateStreaming || m.state == StateToolExecuting || m.state == StatePlanExecuting
	statusBar := renderStatusBar(
		m.width,
		m.connected,
		m.reconnecting,
		spinning,
		m.spinner.View(),
		m.modeWarning,
		m.styles,
	)
	uv.NewStyledString(statusBar).Draw(canvas, image.Rect(0, y, m.width, m.height))

	// 6. Command palette overlay — drawn last (like crush's dialog overlay)
	if m.commandPalette.IsVisible() {
		overlay := m.commandPalette.View(m.width, m.height, m.styles)
		w, h := lipgloss.Size(overlay)
		center := centerRect(canvas.Bounds(), w, h)
		uv.NewStyledString(overlay).Draw(canvas, center)
	}

	// 6b. File completer overlay — drawn above the input box
	if m.fileCompleter.IsVisible() {
		popupWidth := 40
		if popupWidth > m.width-4 {
			popupWidth = m.width - 4
		}
		overlay := m.fileCompleter.View(popupWidth, 8, m.styles)
		if overlay != "" {
			_, h := lipgloss.Size(overlay)
			// Position just above the input box, left-aligned with a small indent
			inputTop := m.height - layout.StatusBarHeight - layout.InputHeight
			popupY := inputTop - h
			if popupY < 0 {
				popupY = 0
			}
			uv.NewStyledString(overlay).Draw(canvas, image.Rect(2, popupY, 2+popupWidth, popupY+h))
		}
	}

	// 7. Render canvas to string (like crush)
	content := strings.ReplaceAll(canvas.Render(), "\r\n", "\n")

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// nextWorkflow cycles through: "" → workflows[0].Name → ... → ""
func (m *Model) nextWorkflow() string {
	if m.activeWorkflow == "" {
		return m.workflows[0].Name
	}
	for i, w := range m.workflows {
		if w.Name == m.activeWorkflow {
			if i+1 < len(m.workflows) {
				return m.workflows[i+1].Name
			}
			return "" // wrap back to chat
		}
	}
	return ""
}

// currentModeName returns "Chat" or the workflow's display Name.
func (m *Model) currentModeName() string {
	if m.activeWorkflow == "" {
		return "Chat"
	}
	for _, w := range m.workflows {
		if w.Name == m.activeWorkflow {
			return w.Name
		}
	}
	return "Chat"
}

// placeholderForMode returns mode-specific placeholder text.
func (m *Model) placeholderForMode() string {
	if m.activeWorkflow == "" {
		return "Ask the agent anything... (Enter to send, Shift+Enter or Alt+Enter for new line)"
	}
	for _, w := range m.workflows {
		if w.Name == m.activeWorkflow {
			return fmt.Sprintf("Describe your %s... (Enter to send, Shift+Enter or Alt+Enter for new line)", w.Name)
		}
	}
	return "Enter your request... (Enter to send, Shift+Enter or Alt+Enter for new line)"
}

// updateInputPromptColor sets the textarea text style to match the current mode.
// Focused state uses the mode color; blurred state uses dim grey.
// Prompt indicator and cursor are left unstyled (default white).
func (m *Model) updateInputPromptColor() {
	whiteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	s := m.input.Styles()
	s.Focused.Text = whiteStyle
	s.Focused.CursorLine = whiteStyle
	s.Blurred.Text = lipgloss.NewStyle().Foreground(colorDim)
	m.input.SetStyles(s)
}

// fillTestData populates the chat with fake messages for UI testing.
func (m *Model) fillTestData() {
	m.chatMessages = append(m.chatMessages,
		renderSystemMessage("Test mode -- fake data for UI scroll testing", m.styles),
		renderUserMessage("Can you help me refactor the authentication module?", m.width-sidebarWidth-2),
		renderAssistantMessage("Sure! Let me start by reading the current auth implementation.", m.mdRenderer),
		renderToolCall("read_file", "internal/auth/handler.go", "", m.styles),
		renderToolResult("read_file", "package auth\n\n// handler code...", false, m.styles),
		renderAssistantMessage("I can see the auth module. Here's what I'd suggest for the refactor.", m.mdRenderer),
	)
}
