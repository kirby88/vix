package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/kirby88/vix/internal/agent"
	"github.com/kirby88/vix/internal/daemon/brain/lsp"
	"github.com/kirby88/vix/internal/daemon/prompt"
	"github.com/kirby88/vix/internal/protocol"
)

// Session manages a single agent session over a persistent socket connection.
type Session struct {
	id          string
	server      *Server
	llm         *LLM
	model       string
	cwd         string
	homeVixDir  string
	forceInit   bool
	eventChan   chan protocol.SessionEvent
	commandChan chan protocol.SessionCommand
	ctx         context.Context
	cancel      context.CancelFunc

	// Agent state
	messages        []anthropic.MessageParam
	tools           []anthropic.ToolUnionParam
	activePlan      *protocol.Plan
	backgroundTasks BackgroundTaskRegistry
	customAgents    map[string]SubagentConfig

	// Read dedup tracker
	readTracker *readTracker

	// Workflows loaded from config
	workflows []*WorkflowDef

	// Skills registry
	skills *agent.SkillRegistry

	// Chat agent name from config
	chatAgent string

	// Project config (feature flags, etc.)
	projectConfig ProjectConfig

	// Active LLM call cancellation
	cancelStream context.CancelFunc
}

// NewSession creates a new agent session.
func NewSession(id string, server *Server, llm *LLM, model, cwd, homeVixDir string, forceInit bool, parentCtx context.Context) *Session {
	ctx, cancel := context.WithCancel(parentCtx)
	return &Session{
		id:          id,
		server:      server,
		llm:         llm,
		model:       model,
		cwd:         cwd,
		homeVixDir:  homeVixDir,
		forceInit:   forceInit,
		eventChan:   make(chan protocol.SessionEvent, 256),
		commandChan: make(chan protocol.SessionCommand, 16),
		ctx:         ctx,
		cancel:      cancel,
		tools:       ToolSchemas(),
		readTracker: newReadTracker(),
	}
}

// emit sends an event to the client.
func (s *Session) emit(eventType string, data any) {
	select {
	case s.eventChan <- protocol.SessionEvent{Type: eventType, Data: data}:
	case <-s.ctx.Done():
	}
}

// emitHooks returns a TurnHooks wired to s.emit() for streaming events to the UI.
func (s *Session) emitHooks() *TurnHooks {
	return &TurnHooks{
		OnStreamDelta: func(delta string) {
			s.emit("event.stream_chunk", protocol.EventStreamChunk{Text: delta})
		},
		OnStreamDone: func(inputTokens, outputTokens, cacheCreation, cacheRead, elapsedMs int64) {
			s.emit("event.stream_done", protocol.EventStreamDone{
				InputTokens:         inputTokens,
				OutputTokens:        outputTokens,
				CacheCreationTokens: cacheCreation,
				CacheReadTokens:     cacheRead,
				ElapsedMs:           elapsedMs,
			})
		},
		OnToolCall: func(toolID, name, summary, reason string) {
			s.emit("event.tool_call", protocol.EventToolCall{ToolID: toolID, Name: name, Summary: summary, Reason: reason})
		},
		OnToolResult: func(toolID, name, output string, isError bool) {
			s.emit("event.tool_result", protocol.EventToolResult{ToolID: toolID, Name: name, Output: output, IsError: isError})
		},
	}
}

// emitToolResult emits an event.tool_result, enriching it with diff detail for edit_file.
func (s *Session) emitToolResult(toolID, name string, input map[string]any, output string, isError bool) {
	ev := protocol.EventToolResult{
		ToolID: toolID, Name: name, Output: output, IsError: isError,
	}
	switch name {
	case "edit_file":
		if !isError && input != nil {
			oldStr, _ := input["old_string"].(string)
			newStr, _ := input["new_string"].(string)
			if oldStr != "" || newStr != "" {
				pathStr, _ := input["path"].(string)
				cwdStr, _ := input["cwd"].(string)
				resolvedPath := resolvePathInCwd(cwdStr, pathStr)
				ev.Detail = FormatEditDiff(resolvedPath, oldStr, newStr)
			}
		}
	case "tool_orchestrator":
		ev.Output = ""
	}
	s.emit("event.tool_result", ev)
}

// waitForCommand blocks until a command of the specified type is received, or ctx is cancelled.
func (s *Session) waitForCommand(ctx context.Context, types ...string) (protocol.SessionCommand, bool) {
	typeSet := make(map[string]bool, len(types))
	for _, t := range types {
		typeSet[t] = true
	}

	for {
		select {
		case cmd := <-s.commandChan:
			if typeSet[cmd.Type] {
				return cmd, true
			}
			// Handle cancel at any time
			if cmd.Type == "session.cancel" {
				return cmd, false
			}
			// Ignore unmatched commands
		case <-ctx.Done():
			return protocol.SessionCommand{}, false
		}
	}
}

// Run is the main session loop. It initializes the brain, then waits for input.
func (s *Session) Run() {
	defer func() {
		if r := recover(); r != nil {
			s.emit("event.error", protocol.EventError{Message: fmt.Sprintf("session panic: %v", r)})
		}
		s.cancel()
	}()

	s.initBrain()

	for {
		select {
		case <-s.ctx.Done():
			return
		case cmd := <-s.commandChan:
			switch cmd.Type {
			case "session.input":
				var data protocol.SessionInputData
				json.Unmarshal(cmd.Data, &data)
				s.handleInput(data.Text, data.Attachments)
			case "session.workflow":
				var data protocol.SessionWorkflowData
				json.Unmarshal(cmd.Data, &data)
				s.handleWorkflowCommand(data.Name, data.Text)
			case "session.close":
				return
			}
		}
	}
}

// initBrain ensures the brain index exists (running brain.init if needed),
// then loads memory, custom agents, and workflows.
func (s *Session) initBrain() {
	s.emit("event.init_state", protocol.EventInitState{State: int(protocol.InitInProgress)})

	// Check if context/ dir exists; if not, run brain.init to generate .vix/context/
	contextDir := filepath.Join(s.cwd, ".vix", "context")
	if _, err := os.Stat(contextDir); os.IsNotExist(err) || s.forceInit {
		handler := s.server.GetHandler("brain.init")
		if handler != nil {
			resp, err := handler(map[string]any{
				"params": map[string]any{
					"project_path": s.cwd,
				},
			})
			if err != nil || resp["status"] != "ok" {
				log.Printf("[session] brain.init failed, continuing without brain context")
			}
		}
	} else {
		// brain.init already ran previously — ensure LSP pool is initialized
		// so lsp_query tool works without requiring a full brain.init
		if lsp.GetPool() == nil {
			lsp.InitPool(s.ctx, s.cwd, s.homeVixDir)
		}
	}

	// Load agents: home first, project overrides
	s.customAgents = LoadCustomAgents(filepath.Join(s.homeVixDir, "agents"))
	for k, v := range LoadCustomAgents(filepath.Join(s.cwd, ".vix", "agents")) {
		s.customAgents[k] = v
	}

	// Load skills from project and user directories
	homeDir, _ := os.UserHomeDir()
	s.skills = agent.LoadSkills(
		filepath.Join(s.cwd, ".vix", "skills"),
		filepath.Join(homeDir, ".vix", "skills"),
	)
	if s.skills.Count() > 0 {
		log.Printf("[session] loaded %d skill(s)", s.skills.Count())
	}
	if len(s.customAgents) > 0 {
		log.Printf("[session] loaded %d custom agent(s) from .vix/agents/", len(s.customAgents))
	}
	PatchSpawnAgentDescription(s.tools, s.customAgents)

	projectConfig := LoadProjectConfig(
		filepath.Join(s.homeVixDir, "settings.json"),
		filepath.Join(s.cwd, ".vix", "settings.json"),
	)
	s.projectConfig = projectConfig
	s.chatAgent = projectConfig.Agent
	s.workflows = projectConfig.Workflows

	if projectConfig.HasFeature(FeatureToolOrchestrator) {
		s.tools = FilterToolSchemas([]string{
			"tool_orchestrator",
			"ask_question_to_user",
			"spawn_agent",
			"task_output",
		})
		log.Printf("[session] tool_orchestrator feature enabled: %d tools exposed", len(s.tools))
	}
	if len(s.workflows) > 0 {
		log.Printf("[session] loaded %d workflow(s) from config", len(s.workflows))
	}
	log.Printf("[session] chat agent: %s", s.chatAgent)

	s.emit("event.init_state", protocol.EventInitState{State: int(protocol.InitDone)})
	s.emit("event.workflows_available", protocol.EventWorkflowsAvailable{
		Workflows: s.workflowInfoList(),
	})
}

// workflowInfoList returns the list of WorkflowInfo in config order.
func (s *Session) workflowInfoList() []protocol.WorkflowInfo {
	if len(s.workflows) == 0 {
		return nil
	}
	infos := make([]protocol.WorkflowInfo, len(s.workflows))
	for i, wf := range s.workflows {
		infos[i] = protocol.WorkflowInfo{Name: wf.Name}
	}
	return infos
}

// brainDir returns the path to the .vix directory
func (s *Session) brainDir() string {
	return filepath.Join(s.cwd, ".vix")
}

// searchDirs returns the brainDir string with project first, then home.
func (s *Session) searchDirs() string {
	return prompt.JoinSearchDirs(s.brainDir(), s.homeVixDir)
}

// resolveAgentPath checks project .vix/agents/ first, then ~/.vix/agents/.
func (s *Session) resolveAgentPath(filename string) string {
	projectPath := filepath.Join(s.cwd, ".vix", "agents", filename)
	if _, err := os.Stat(projectPath); err == nil {
		return projectPath
	}
	if s.homeVixDir != "" {
		homePath := filepath.Join(s.homeVixDir, "agents", filename)
		if _, err := os.Stat(homePath); err == nil {
			return homePath
		}
	}
	return projectPath // fallback to project path (will error naturally)
}

// instructionFile holds a discovered instruction file path and its content.
type instructionFile struct {
	Path    string
	Content string
}

// discoverInstructionFiles finds CLAUDE.md and AGENTS.md files based on feature flags.
func (s *Session) discoverInstructionFiles() []instructionFile {
	var files []instructionFile

	if s.projectConfig.HasFeature(FeatureReadClaudeMD) {
		candidates := []string{
			filepath.Join(s.homeVixDir, "CLAUDE.md"),
			filepath.Join(s.cwd, "CLAUDE.md"),
		}
		for _, path := range candidates {
			if data, err := os.ReadFile(path); err == nil {
				files = append(files, instructionFile{Path: path, Content: string(data)})
			}
		}
	}

	if s.projectConfig.HasFeature(FeatureReadAgentsMD) {
		path := filepath.Join(s.cwd, "AGENTS.md")
		if data, err := os.ReadFile(path); err == nil {
			files = append(files, instructionFile{Path: path, Content: string(data)})
		}
	}

	return files
}

func (s *Session) buildSystemPrompt() []anthropic.TextBlockParam {
	var blocks []anthropic.TextBlockParam

	// Load base system prompt from template
	funcs := map[string]func() string{
		"frequently_accessed_files": s.frequentlyAccessedFilesText,
	}
	agentFile := s.resolveAgentPath(s.chatAgent + ".md")
	basePrompt := prompt.GetLoader().Load(agentFile, map[string]string{
		"working_directory": s.cwd,
	}, s.searchDirs(), funcs)

	baseBlock := anthropic.TextBlockParam{
		Text: basePrompt,
	}
	blocks = append(blocks, baseBlock)

	// Inject frequently accessed files
	if filesText := s.frequentlyAccessedFilesText(); filesText != "" {
		blocks = append(blocks, anthropic.TextBlockParam{
			Text: filesText,
		})
	}

	// Inject project instruction files (CLAUDE.md, AGENTS.md)
	if instrFiles := s.discoverInstructionFiles(); len(instrFiles) > 0 {
		for _, f := range instrFiles {
			text := fmt.Sprintf("<system-reminder>\nContents of %s (project instructions):\n\n%s\n</system-reminder>", f.Path, f.Content)
			blocks = append(blocks, anthropic.TextBlockParam{
				Text: text,
			})
		}
		log.Printf("[session] loaded %d instruction file(s)", len(instrFiles))
	}

	if s.skills != nil {
		if skillsText := s.skills.FormatForSystemPrompt(); skillsText != "" {
			blocks = append(blocks, anthropic.TextBlockParam{
				Text: "\n\n" + skillsText,
			})
		}
	}

	return blocks
}

// AddUserMessage appends a user message to the conversation, optionally with image attachments.
func (s *Session) AddUserMessage(text string, attachments ...protocol.Attachment) {
	var contentBlocks []anthropic.ContentBlockParamUnion

	// Build text with image references
	textContent := text
	if len(attachments) > 0 {
		var refs strings.Builder
		for _, att := range attachments {
			refs.WriteString(fmt.Sprintf("[Image: %s]\n", att.Path))
		}
		if text == "" {
			textContent = "[Image attachment]"
		} else {
			textContent = refs.String() + "\n" + text
		}
	}
	contentBlocks = append(contentBlocks, anthropic.NewTextBlock(textContent))

	// Add image blocks
	for _, att := range attachments {
		imgBlock := anthropic.NewImageBlock(anthropic.Base64ImageSourceParam{
			MediaType: anthropic.Base64ImageSourceMediaType(att.MediaType),
			Data:      att.Data,
		})
		contentBlocks = append(contentBlocks, imgBlock)
	}

	s.messages = append(s.messages, anthropic.NewUserMessage(contentBlocks...))
}

// frequentlyAccessedFilesText returns a markdown-formatted string of the top 10
// frequently accessed files, or an empty string if none are available.
func (s *Session) frequentlyAccessedFilesText() string {
	resp, err := doGetTopFiles(s.server, map[string]any{"count": 10})
	if err != nil {
		return ""
	}
	resultMap, _ := resp["data"].(map[string]any)
	if resultMap == nil {
		return ""
	}

	filesInterface, ok := resultMap["files"]
	if !ok {
		return ""
	}

	files, ok := filesInterface.([]any)
	if !ok || len(files) == 0 {
		return ""
	}

	log.Printf("Injecting %d frequently accessed files into system prompt", len(files))

	var filesContent strings.Builder
	filesContent.WriteString("\n\n# Frequently Accessed Files\n\n")

	for _, fileInterface := range files {
		if fileMap, ok := fileInterface.(map[string]any); ok {
			path, _ := fileMap["path"].(string)
			content, _ := fileMap["content"].(string)
			filesContent.WriteString(fmt.Sprintf("## %s\n```\n%s\n```\n\n", path, content))
		}
	}

	return filesContent.String()
}

// executeToolDirect calls a tool handler directly (in-process, no socket).
func (s *Session) executeToolDirect(name string, params map[string]any) *ToolResult {
	// Clone params and add cwd
	p := make(map[string]any, len(params)+1)
	for k, v := range params {
		p[k] = v
	}
	p["cwd"] = s.cwd
	handler := s.server.GetHandler("tool." + name)
	if handler == nil {
		return &ToolResult{Output: fmt.Sprintf("unknown tool: %s", name), IsError: true}
	}

	resp, err := handler(map[string]any{
		"command": "tool." + name,
		"params":  p,
	})
	if err != nil {
		return &ToolResult{Output: err.Error(), IsError: true}
	}

	if resp["status"] != "ok" {
		msg, _ := resp["message"].(string)
		return &ToolResult{Output: fmt.Sprintf("Tool error: %s", msg), IsError: true}
	}

	data, _ := resp["data"].(map[string]any)

	// Check if tool requests confirmation
	if confirm, ok := data["confirm"].(bool); ok && confirm {
		return &ToolResult{
			NeedsConfirmation: true,
			ToolName:          name,
			Params:            params,
		}
	}

	output, _ := data["output"].(string)
	isError, _ := data["is_error"].(bool)
	return &ToolResult{Output: output, IsError: isError}
}

// executeToolConfirmed calls a tool handler with confirmation bypassed.
func (s *Session) executeToolConfirmed(name string, params map[string]any) *ToolResult {
	p := make(map[string]any, len(params)+2)
	for k, v := range params {
		p[k] = v
	}
	p["confirmed"] = true
	p["cwd"] = s.cwd

	handler := s.server.GetHandler("tool." + name)
	if handler == nil {
		return &ToolResult{Output: fmt.Sprintf("unknown tool: %s", name), IsError: true}
	}

	resp, err := handler(map[string]any{
		"command": "tool." + name,
		"params":  p,
	})
	if err != nil {
		return &ToolResult{Output: err.Error(), IsError: true}
	}

	if resp["status"] != "ok" {
		msg, _ := resp["message"].(string)
		return &ToolResult{Output: fmt.Sprintf("Tool error: %s", msg), IsError: true}
	}

	data, _ := resp["data"].(map[string]any)
	output, _ := data["output"].(string)
	isError, _ := data["is_error"].(bool)
	return &ToolResult{Output: output, IsError: isError}
}

const maxRetries = 10

// streamWithRetry calls StreamMessage with automatic retry and exponential
// backoff for transient errors (rate limits, server errors, network issues).
// Non-retryable errors (auth, bad request) fail immediately with a friendly message.
func (s *Session) streamWithRetry(
	system []anthropic.TextBlockParam,
	onDelta func(string),
) (*anthropic.Message, time.Duration, error) {
	var lastReason string
	for attempt := range maxRetries {
		streamCtx, streamCancel := context.WithCancel(s.ctx)
		s.cancelStream = streamCancel

		msg, elapsed, err := s.llm.StreamMessage(streamCtx, system, s.messages, s.tools, onDelta)
		if err == nil {
			return msg, elapsed, nil
		}
		streamCancel()

		if errors.Is(err, context.Canceled) {
			return nil, 0, err
		}

		retryable, reason := classifyError(err)
		lastReason = reason
		log.Printf("\033[31m[session] API error (attempt %d/%d): %s — %v\033[0m", attempt+1, maxRetries, reason, err)

		if !retryable {
			return nil, 0, fmt.Errorf("%s", reason)
		}

		// Flush any partial streaming content in the UI
		s.emit("event.stream_done", protocol.EventStreamDone{})

		// Calculate backoff: min(1s * 2^attempt, 60s) + jitter
		delaySec := math.Min(math.Pow(2, float64(attempt)), 60)
		jitter := rand.Float64() * 0.5
		wait := time.Duration((delaySec + jitter) * float64(time.Second))
		waitSecs := int(math.Ceil(delaySec + jitter))

		s.emit("event.retry", protocol.EventRetry{
			Attempt:    attempt + 1,
			MaxRetries: maxRetries,
			WaitSecs:   waitSecs,
			Reason:     reason,
		})

		select {
		case <-time.After(wait):
		case <-s.ctx.Done():
			return nil, 0, context.Canceled
		}
	}
	return nil, 0, fmt.Errorf("API request failed after %d attempts: %s", maxRetries, lastReason)
}

func (s *Session) handleInput(text string, attachments []protocol.Attachment) {
	if text == "/exit" {
		s.emit("event.quit", nil)
		return
	}

	if text == "/clear" {
		s.messages = nil
		s.emit("event.clear", nil)
		s.emit("event.agent_done", nil)
		return
	}

	if text == "/sandbox" {
		s.server.sandboxMu.Lock()
		s.server.sandboxEnabled = !s.server.sandboxEnabled
		enabled := s.server.sandboxEnabled
		s.server.sandboxMu.Unlock()

		status := "disabled"
		if enabled {
			status = "enabled"
		}
		LogInfo("Sandbox toggled: %s", status)
		s.emit("event.stream_chunk", protocol.EventStreamChunk{Text: fmt.Sprintf("Sandbox %s\n", status)})
		s.emit("event.agent_done", nil)
		return
	}

	// /skills — list all loaded skills
	if text == "/skills" {
		s.emit("event.stream_chunk", protocol.EventStreamChunk{Text: s.skills.FormatSkillsList()})
		s.emit("event.agent_done", nil)
		return
	}

	// Check for skill invocation: /skill-name [args]
	if strings.HasPrefix(text, "/") {
		parts := strings.SplitN(text[1:], " ", 2)
		skillName := parts[0]
		skillArgs := ""
		if len(parts) > 1 {
			skillArgs = parts[1]
		}

		if skill := s.skills.Get(skillName); skill != nil {
			s.handleSkill(skill, skillArgs, attachments)
			return
		}
	}

	if strings.HasPrefix(text, "/plan ") {
		desc := strings.TrimSpace(strings.TrimPrefix(text, "/plan "))
		if desc != "" {
			s.handlePlan(desc)
			return
		}
	}

	// Validate attachments before adding
	for _, att := range attachments {
		if err := protocol.ValidateAttachment(att); err != nil {
			s.emit("event.error", protocol.EventError{Message: fmt.Sprintf("Invalid attachment: %v", err)})
			s.emit("event.agent_done", nil)
			return
		}
	}
	
	s.AddUserMessage(text, attachments...)

	// Inner loop: agent turns
	for {
		system := s.buildSystemPrompt()

		msg, elapsed, err := s.streamWithRetry(system, func(delta string) {
			s.emit("event.stream_chunk", protocol.EventStreamChunk{Text: delta})
		})

		if err != nil {
			if errors.Is(err, context.Canceled) {
				s.emit("event.stream_done", protocol.EventStreamDone{})
				break
			}
			s.emit("event.error", protocol.EventError{Message: err.Error()})
			break
		}

		s.emit("event.stream_done", protocol.EventStreamDone{
			InputTokens:         msg.Usage.InputTokens,
			OutputTokens:        msg.Usage.OutputTokens,
			CacheCreationTokens: msg.Usage.CacheCreationInputTokens,
			CacheReadTokens:     msg.Usage.CacheReadInputTokens,
			ElapsedMs:           elapsed.Milliseconds(),
		})

		LogLLMCall(s.model, system, s.messages, s.tools, msg)
		s.messages = append(s.messages, msg.ToParam())

		if msg.StopReason == "end_turn" {
			log.Printf("\033[34m[session] end of turn detected\033[0m")
			break
		}

		if msg.StopReason == "tool_use" {
			streamCtx, streamCancel := context.WithCancel(s.ctx)
			toolResults := s.sessionDispatchToolCalls(streamCtx, msg)
			cancelled := streamCtx.Err() != nil
			streamCancel()
			if cancelled {
				s.emit("event.stream_done", protocol.EventStreamDone{})
				break
			}
			s.messages = append(s.messages, anthropic.NewUserMessage(toolResults...))
			continue
		}

		break
	}

	s.emit("event.agent_done", nil)
}

// handleSkill processes a skill invocation: renders the prompt template and
// sends it through the agent loop, optionally restricting available tools.
func (s *Session) handleSkill(skill *agent.Skill, rawArgs string, attachments []protocol.Attachment) {
	rendered := skill.RenderPrompt(rawArgs)

	// Save original tools and apply skill's tool restriction if specified
	originalTools := s.tools
	if skill.AllowedTools != nil {
		s.tools = FilterToolSchemas(skill.AllowedTools)
	}

	// Save original model and apply skill's model override if specified
	originalModel := s.model
	if skill.Model != "" {
		s.model = skill.Model
		s.llm = NewLLM(s.server.apiKey, s.model)
	}

	// Send the rendered prompt as a user message through the normal agent loop
	s.AddUserMessage(rendered, attachments...)

	// Inner loop: agent turns (same as handleInput)
	for {
		system := s.buildSystemPrompt()

		msg, elapsed, err := s.streamWithRetry(system, func(delta string) {
			s.emit("event.stream_chunk", protocol.EventStreamChunk{Text: delta})
		})

		if err != nil {
			if errors.Is(err, context.Canceled) {
				s.emit("event.stream_done", protocol.EventStreamDone{})
				break
			}
			s.emit("event.error", protocol.EventError{Message: err.Error()})
			break
		}

		s.emit("event.stream_done", protocol.EventStreamDone{
			InputTokens:         msg.Usage.InputTokens,
			OutputTokens:        msg.Usage.OutputTokens,
			CacheCreationTokens: msg.Usage.CacheCreationInputTokens,
			CacheReadTokens:     msg.Usage.CacheReadInputTokens,
			ElapsedMs:           elapsed.Milliseconds(),
		})

		LogLLMCall(s.model, system, s.messages, s.tools, msg)
		s.messages = append(s.messages, msg.ToParam())

		if msg.StopReason == "end_turn" {
			break
		}

		if msg.StopReason == "tool_use" {
			streamCtx, streamCancel := context.WithCancel(s.ctx)
			toolResults := s.sessionDispatchToolCalls(streamCtx, msg)
			cancelled := streamCtx.Err() != nil
			streamCancel()
			if cancelled {
				s.emit("event.stream_done", protocol.EventStreamDone{})
				break
			}
			s.messages = append(s.messages, anthropic.NewUserMessage(toolResults...))
			continue
		}

		break
	}

	// Restore original tools and model
	s.tools = originalTools
	if skill.Model != "" {
		s.model = originalModel
		s.llm = NewLLM(s.server.apiKey, s.model)
	}

	s.emit("event.agent_done", nil)
}

// interactiveTools are tools that require sequential execution (user interaction, blocking waits).
var interactiveTools = map[string]bool{
	"ask_question_to_user": true,
	"spawn_agent":          true,
	"task_output":          true,
	"tool_orchestrator":    true,
}

// writeTools are tools that mutate files — their presence forces sequential execution.
var writeTools = map[string]bool{
	"write_file":  true,
	"edit_file":   true,
	"delete_file": true,
}

// toolTask holds parsed info for a single tool call in a batch.
type toolTask struct {
	toolUse     anthropic.ToolUseBlock
	input       map[string]any
	summary     string
	reason      string
	interactive bool
	dedupResult *dedupResult
	result      *ToolResult
	apiResult   anthropic.ContentBlockParamUnion
}

// dispatchOptions configures the unified tool dispatcher.
// All callback fields are optional (nil = disabled).
type dispatchOptions struct {
	// cwd is the working directory passed to executeTool.
	cwd string
	// tracker is used for read dedup and invalidation.
	tracker *readTracker
	// executeTool runs the named tool with the given input. It must be non-nil.
	// The implementation is responsible for setting confirmed=true when needed.
	executeTool func(name string, input map[string]any) *ToolResult
	// handleSpecial handles tools that need session-level logic (ask_question_to_user,
	// spawn_agent, task_output). Returns (result, true) when it handles the tool,
	// (nil, false) when it doesn't recognise it.
	handleSpecial func(ctx context.Context, name string, input map[string]any) (*ToolResult, bool)
	// confirmFn is called when executeTool returns NeedsConfirmation=true.
	// Returns approved=true to proceed, or cancelled=true to abort.
	// When nil, NeedsConfirmation results are treated as denied.
	confirmFn func(ctx context.Context, name string, input map[string]any) (approved, cancelled bool)
	// emitToolCall is called once per tool, before execution, with summary and reason.
	emitToolCall func(toolID, name, summary, reason string)
	// emitToolResult is called after each tool completes.
	emitToolResult func(toolID, name string, input map[string]any, output string, isError bool)
}

// dispatchToolCalls is the single, unified tool dispatcher for both the main agent
// and subagents/workflow agents. Session-specific behaviour (confirmation prompts,
// interactive tools, UI events) is injected via dispatchOptions.
func dispatchToolCalls(ctx context.Context, msg *anthropic.Message, opts dispatchOptions) []anthropic.ContentBlockParamUnion {
	// --- Stage 1: Parse & classify ---
	var readCalls []readFileCall
	var tasks []*toolTask
	hasInteractive := false
	hasWrite := false

	for i, block := range msg.Content {
		toolUse, ok := block.AsAny().(anthropic.ToolUseBlock)
		if !ok {
			continue
		}

		var input map[string]any
		inputBytes, _ := json.Marshal(toolUse.Input)
		json.Unmarshal(inputBytes, &input)

		reason, _ := input["reason"].(string)
		summary := SummarizeToolInput(toolUse.Name, input)

		t := &toolTask{
			toolUse:     toolUse,
			input:       input,
			summary:     summary,
			reason:      reason,
			interactive: interactiveTools[toolUse.Name] && opts.handleSpecial != nil,
		}

		if t.interactive {
			hasInteractive = true
		}
		if writeTools[toolUse.Name] {
			hasWrite = true
		}

		// Collect read_file calls for dedup
		if toolUse.Name == "read_file" {
			path, _ := input["path"].(string)
			if path != "" {
				absPath, r := normalizeRange(opts.cwd, path, input)
				readCalls = append(readCalls, readFileCall{
					ToolID:  toolUse.ID,
					AbsPath: absPath,
					Range:   r,
					Index:   i,
				})
			}
		}

		tasks = append(tasks, t)
	}

	if len(tasks) == 0 {
		return nil
	}

	// Run dedup check (log only, don't block the read)
	dedupResults := processReadFileDedup(opts.tracker, readCalls)
	for _, t := range tasks {
		if t.toolUse.Name == "read_file" {
			if dr, ok := dedupResults[t.toolUse.ID]; ok {
				log.Printf("[dispatch] duplicate read detected (allowing): %s", dr.Output)
				_ = dr
			}
		}
	}

	// Emit ALL tool_call events upfront
	for _, t := range tasks {
		log.Printf("[dispatch] tool call: %s %s", t.toolUse.Name, t.summary)
		if opts.emitToolCall != nil {
			opts.emitToolCall(t.toolUse.ID, t.toolUse.Name, t.summary, t.reason)
		}
	}

	// --- Stage 2: Execute ---
	canParallelize := !hasInteractive && !hasWrite && len(tasks) > 1

	if canParallelize {
		executeToolsParallel(ctx, tasks, opts)
	} else {
		executeToolsSequential(ctx, tasks, opts)
	}

	// --- Stage 3: Collect results & update tracker ---
	toolResults := make([]anthropic.ContentBlockParamUnion, 0, len(tasks))

	for _, t := range tasks {
		if t.result == nil {
			continue
		}

		// Record successful read_file calls
		if t.toolUse.Name == "read_file" && !t.result.IsError {
			path, _ := t.input["path"].(string)
			absPath, r := normalizeRange(opts.cwd, path, t.input)
			actualEnd := countLines(t.result.Output)
			opts.tracker.Record(absPath, r, actualEnd)
		}

		// Invalidate read tracker after write/edit/delete
		if !t.result.IsError {
			switch t.toolUse.Name {
			case "write_file", "edit_file", "delete_file":
				path, _ := t.input["path"].(string)
				if path != "" {
					absPath := path
					if !filepath.IsAbs(path) {
						absPath = filepath.Join(opts.cwd, path)
					}
					opts.tracker.Invalidate(absPath)
				}
			}
		}

		toolResults = append(toolResults, t.apiResult)
	}

	return toolResults
}

// executeToolsParallel runs all tools concurrently, emitting results as they complete.
func executeToolsParallel(ctx context.Context, tasks []*toolTask, opts dispatchOptions) {
	var wg sync.WaitGroup

	for _, t := range tasks {
		if ctx.Err() != nil {
			break
		}

		// Handle dedup hits inline (no goroutine needed)
		if t.dedupResult != nil {
			t.result = &ToolResult{Output: t.dedupResult.Output, IsError: t.dedupResult.IsError}
			t.apiResult = anthropic.NewToolResultBlock(t.toolUse.ID, t.dedupResult.Output, t.dedupResult.IsError)
			if opts.emitToolResult != nil {
				opts.emitToolResult(t.toolUse.ID, t.toolUse.Name, t.input, t.dedupResult.Output, t.dedupResult.IsError)
			}
			continue
		}

		// Launch goroutine for tool execution
		wg.Add(1)
		go func(t *toolTask) {
			defer wg.Done()

			result := opts.executeTool(t.toolUse.Name, t.input)
			if result == nil {
				result = &ToolResult{Output: "tool returned nil", IsError: true}
			}

			// If tool needs confirmation, defer to post-parallel sequential handling.
			if result.NeedsConfirmation {
				t.result = result
				return
			}

			t.result = result
			t.apiResult = anthropic.NewToolResultBlock(t.toolUse.ID, result.Output, result.IsError)
			if opts.emitToolResult != nil {
				opts.emitToolResult(t.toolUse.ID, t.toolUse.Name, t.input, result.Output, result.IsError)
			}
		}(t)
	}

	wg.Wait()

	// Handle any tools that returned NeedsConfirmation (sequentially)
	for _, t := range tasks {
		if t.result == nil || !t.result.NeedsConfirmation {
			continue
		}
		result := resolveConfirmation(ctx, t, opts)
		t.result = result
		t.apiResult = anthropic.NewToolResultBlock(t.toolUse.ID, result.Output, result.IsError)
		if opts.emitToolResult != nil {
			opts.emitToolResult(t.toolUse.ID, t.toolUse.Name, t.input, result.Output, result.IsError)
		}
	}
}

// executeToolsSequential runs tools one at a time.
func executeToolsSequential(ctx context.Context, tasks []*toolTask, opts dispatchOptions) {
	for _, t := range tasks {
		if ctx.Err() != nil {
			break
		}

		// Handle dedup hits
		if t.dedupResult != nil {
			t.result = &ToolResult{Output: t.dedupResult.Output, IsError: t.dedupResult.IsError}
			t.apiResult = anthropic.NewToolResultBlock(t.toolUse.ID, t.dedupResult.Output, t.dedupResult.IsError)
			if opts.emitToolResult != nil {
				opts.emitToolResult(t.toolUse.ID, t.toolUse.Name, t.input, t.dedupResult.Output, t.dedupResult.IsError)
			}
			continue
		}

		// Delegate to session-level handler if available (ask_question_to_user, spawn_agent, task_output)
		if opts.handleSpecial != nil {
			if result, handled := opts.handleSpecial(ctx, t.toolUse.Name, t.input); handled {
				t.result = result
				t.apiResult = anthropic.NewToolResultBlock(t.toolUse.ID, result.Output, result.IsError)
				if result.IsError && opts.emitToolResult != nil {
					opts.emitToolResult(t.toolUse.ID, t.toolUse.Name, t.input, result.Output, true)
				}
				continue
			}
		}

		result := opts.executeTool(t.toolUse.Name, t.input)
		if result == nil {
			result = &ToolResult{Output: "tool returned nil", IsError: true}
		}

		if result.NeedsConfirmation {
			result = resolveConfirmation(ctx, t, opts)
		}

		t.result = result
		t.apiResult = anthropic.NewToolResultBlock(t.toolUse.ID, result.Output, result.IsError)
		if opts.emitToolResult != nil {
			opts.emitToolResult(t.toolUse.ID, t.toolUse.Name, t.input, result.Output, result.IsError)
		}
	}
}

// resolveConfirmation handles a NeedsConfirmation result by calling opts.confirmFn.
// When confirmFn is nil, the tool is treated as denied.
func resolveConfirmation(ctx context.Context, t *toolTask, opts dispatchOptions) *ToolResult {
	if opts.confirmFn == nil {
		return &ToolResult{Output: "Permission denied.", IsError: true}
	}
	approved, cancelled := opts.confirmFn(ctx, t.toolUse.Name, t.input)
	if cancelled {
		return &ToolResult{Output: "Cancelled", IsError: true}
	}
	if !approved {
		return &ToolResult{Output: "Permission denied by user.", IsError: true}
	}
	// Re-run with confirmed flag set
	p := make(map[string]any, len(t.input)+1)
	for k, v := range t.input {
		p[k] = v
	}
	p["confirmed"] = true
	return opts.executeTool(t.toolUse.Name, p)
}

// sessionDispatchToolCalls is the Session-specific wrapper around the unified dispatcher.
func (s *Session) sessionDispatchToolCalls(ctx context.Context, msg *anthropic.Message) []anthropic.ContentBlockParamUnion {
	opts := dispatchOptions{
		cwd:     s.cwd,
		tracker: s.readTracker,
		executeTool: func(name string, input map[string]any) *ToolResult {
			return s.executeToolDirect(name, input)
		},
		handleSpecial: func(ctx context.Context, name string, input map[string]any) (*ToolResult, bool) {
			switch name {
			case "ask_question_to_user":
				result, err := s.handleAskQuestionsBatch(ctx, input)
				if err != nil {
					return &ToolResult{Output: "Cancelled", IsError: true}, true
				}
				return result, true
			case "spawn_agent":
				output, isErr := s.handleSpawnAgent(ctx, input)
				return &ToolResult{Output: output, IsError: isErr}, true
			case "task_output":
				output, isErr := s.handleTaskOutput(ctx, input)
				return &ToolResult{Output: output, IsError: isErr}, true
			}
			return nil, false
		},
		confirmFn: func(ctx context.Context, name string, input map[string]any) (approved, cancelled bool) {
			s.emit("event.confirm_request", protocol.EventConfirmRequest{ToolName: name, Params: input})
			cmd, ok := s.waitForCommand(s.ctx, "session.confirm")
			if !ok {
				return false, true
			}
			var confirmData protocol.SessionConfirmData
			json.Unmarshal(cmd.Data, &confirmData)
			return confirmData.Approved, false
		},
		emitToolCall: func(toolID, name, summary, reason string) {
			s.emit("event.tool_call", protocol.EventToolCall{
				ToolID:  toolID,
				Name:    name,
				Summary: summary,
				Reason:  reason,
			})
		},
		emitToolResult: func(toolID, name string, input map[string]any, output string, isError bool) {
			s.emitToolResult(toolID, name, input, output, isError)
		},
	}
	return dispatchToolCalls(ctx, msg, opts)
}

func (s *Session) handlePlan(description string) {
	s.emit("event.stream_done", protocol.EventStreamDone{})

	// Find a workflow whose name contains "plan" (case-insensitive)
	var pf *WorkflowDef
	for _, wf := range s.workflows {
		if strings.Contains(strings.ToLower(wf.Name), "plan") {
			pf = wf
			break
		}
	}
	if pf == nil {
		msg := "no workflow with 'plan' in its name is configured — cannot handle /plan request"
		log.Printf("[session] error: %s", msg)
		s.emit("event.error", protocol.EventError{Message: msg})
		s.emit("event.agent_done", nil)
		return
	}

	err := s.executeWorkflow(pf, description)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.emit("event.error", protocol.EventError{Message: fmt.Sprintf("workflow failed: %v", err)})
	}
	s.emit("event.agent_done", nil)
}

// handleWorkflowCommand handles a session.workflow command by looking up and executing
// the workflow matching the given name.
func (s *Session) handleWorkflowCommand(name, text string) {
	var wf *WorkflowDef
	for _, w := range s.workflows {
		if w.Name == name {
			wf = w
			break
		}
	}
	if wf == nil {
		msg := fmt.Sprintf("workflow %q not found", name)
		log.Printf("[session] %s", msg)
		s.emit("event.error", protocol.EventError{Message: msg})
		s.emit("event.agent_done", nil)
		return
	}

	err := s.executeWorkflow(wf, text)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.emit("event.error", protocol.EventError{Message: fmt.Sprintf("workflow failed: %v", err)})
	}
	s.emit("event.agent_done", nil)
}

// handleSpawnAgent resolves and runs a subagent.
func (s *Session) handleSpawnAgent(ctx context.Context, input map[string]any) (string, bool) {
	prompt, _ := input["prompt"].(string)
	if prompt == "" {
		return "spawn_agent requires a 'prompt' parameter", true
	}

	agentType, _ := input["agent_type"].(string)
	if agentType == "" {
		// Default to "general" if it exists, otherwise first available agent
		if _, ok := s.customAgents["general"]; ok {
			agentType = "general"
		} else {
			for k := range s.customAgents {
				agentType = k
				break
			}
		}
		if agentType == "" {
			return "No agents available. Define agents in .vix/agents/", true
		}
	}
	background, _ := input["background"].(bool)

	config, ok := s.customAgents[agentType]
	if !ok {
		available := make([]string, 0)
		for k := range s.customAgents {
			available = append(available, k)
		}
		return fmt.Sprintf("Unknown agent type '%s'. Available: %s", agentType, strings.Join(available, ", ")), true
	}

	apiKey := s.llm.APIKey()
	parentModel := s.model

	// Create an in-process tool executor for subagents
	executeTool := func(name string, params map[string]any, cwd string) (*ToolResult, error) {
		return s.executeToolConfirmed(name, params), nil
	}

	if background {
		taskID := s.backgroundTasks.SpawnBackground(ctx, config, prompt, apiKey, parentModel, executeTool, s.cwd)
		s.emit("event.tool_result", protocol.EventToolResult{
			Name:   "spawn_agent",
			Output: fmt.Sprintf("Background task started. Task ID: %s", taskID),
		})
		log.Printf("[subagent] spawned background task %s (type=%s)", taskID, config.Name)
		return fmt.Sprintf("Background task started. Task ID: %s\nUse task_output to retrieve the result when ready.", taskID), false
	}

	log.Printf("[subagent] spawning foreground agent (type=%s)", config.Name)
	result, err := RunSubagent(ctx, config, prompt, apiKey, parentModel, executeTool, s.cwd, s.emitHooks())

	if err != nil {
		return fmt.Sprintf("Subagent error: %v", err), true
	}

	return result.Output, result.IsError
}

func (s *Session) handleTaskOutput(ctx context.Context, input map[string]any) (string, bool) {
	taskID, _ := input["task_id"].(string)
	if taskID == "" {
		return "task_output requires a 'task_id' parameter", true
	}

	result, err := s.backgroundTasks.WaitForTask(ctx, taskID, 30*time.Second)
	if err != nil {
		return fmt.Sprintf("Error waiting for task: %v", err), true
	}

	if result.InputTokens > 0 || result.OutputTokens > 0 {
		s.emit("event.stream_done", protocol.EventStreamDone{
			InputTokens:         result.InputTokens,
			OutputTokens:        result.OutputTokens,
			CacheCreationTokens: result.CacheCreationTokens,
			CacheReadTokens:     result.CacheReadTokens,
			ElapsedMs:           result.Elapsed.Milliseconds(),
		})
	}

	return result.Output, result.IsError
}

// handleAskQuestionsBatch emits a user question event and waits for all answers.
func (s *Session) handleAskQuestionsBatch(ctx context.Context, input map[string]any) (*ToolResult, error) {
	questionsRaw, ok := input["questions"].([]any)
	if !ok || len(questionsRaw) == 0 {
		return &ToolResult{Output: "ask_questions_batch requires a non-empty 'questions' array", IsError: true}, nil
	}

	var questions []protocol.QuestionDef
	for _, qRaw := range questionsRaw {
		qMap, ok := qRaw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := qMap["id"].(string)
		category, _ := qMap["category"].(string)
		question, _ := qMap["question"].(string)
		var options []string
		if raw, ok := qMap["options"].([]any); ok {
			for _, o := range raw {
				if str, ok := o.(string); ok {
					options = append(options, str)
				}
			}
		}
		questions = append(questions, protocol.QuestionDef{
			ID:       id,
			Category: category,
			Question: question,
			Options:  options,
		})
	}

	s.emit("event.user_question", protocol.EventUserQuestion{
		Questions: questions,
	})

	cmd, ok2 := s.waitForCommand(ctx, "session.user_answer")
	if !ok2 {
		return nil, ctx.Err()
	}

	var answerData protocol.SessionUserAnswerData
	json.Unmarshal(cmd.Data, &answerData)

	// For a single question, return the answer directly.
	if len(questions) == 1 {
		if answerData.Answers != nil {
			if ans, ok := answerData.Answers[questions[0].ID]; ok {
				return &ToolResult{Output: ans}, nil
			}
		}
		return &ToolResult{Output: answerData.Answer}, nil
	}

	// For multiple questions, format answers as readable text for the LLM.
	if answerData.Answers != nil {
		var sb strings.Builder
		for _, q := range questions {
			ans, exists := answerData.Answers[q.ID]
			if exists {
				sb.WriteString(fmt.Sprintf("%s: %s\n", q.Category, ans))
			}
		}
		return &ToolResult{Output: sb.String()}, nil
	}

	return &ToolResult{Output: answerData.Answer}, nil
}
