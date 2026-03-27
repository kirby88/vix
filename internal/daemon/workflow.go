package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	configpkg "github.com/kirby88/vix/internal/config"
	promptloader "github.com/kirby88/vix/internal/daemon/prompt"
	"github.com/kirby88/vix/internal/protocol"
)

// ErrMaxTokens is returned when the LLM response was truncated due to the output token limit.
var ErrMaxTokens = errors.New("max_tokens")

// InputDef declares an expected input parameter.
type InputDef struct {
	Description string `json:"description"`
}

// StepRef is a structured reference to a workflow step with optional parameter mappings.
type StepRef struct {
	ID     string            `json:"id"`
	Params map[string]string `json:"params,omitempty"`
}

// WorkflowDef is the parsed config for a workflow.
type WorkflowDef struct {
	Name       string                      `json:"name"`
	EntryPoint StepRef                     `json:"entry_point"`
	Steps      map[string]WorkflowStepDef  `json:"steps"`
	Summary    string                      `json:"summary,omitempty"`
}

// StepOption is a structured option for tool steps using ask_question_to_user.
type StepOption struct {
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Steps        []StepRef `json:"steps,omitempty"`
	HasUserInput bool     `json:"has_user_input,omitempty"`
}

// WorkflowStepDef defines one step in the workflow.
type WorkflowStepDef struct {
	Type        string              `json:"type"`                    // "agent", "tool", or "bash" (required)
	NextSteps   []StepRef           `json:"next_steps,omitempty"`    // next steps to execute (empty = end workflow)
	InputParams map[string]InputDef `json:"input_params,omitempty"`  // declared input parameters for this step
	Tool        string              `json:"tool,omitempty"`          // tool name for type="tool"
	Agent       string              `json:"agent,omitempty"`         // agent name (loaded from .vix/agents/)
	ForkFrom    string              `json:"fork_from,omitempty"`     // fork from a prior step's agent
	Prompt      string              `json:"prompt,omitempty"`        // template, supports $() syntax
	Command     string              `json:"command,omitempty"`       // bash command for type="bash"
	Input       string              `json:"input,omitempty"`         // piped to stdin (supports $() expansion)
	Output      string              `json:"output,omitempty"`        // file path to write step text output
	DenyTools   []string            `json:"deny_tools,omitempty"`    // tools blocked from executing
	Stream      *bool               `json:"stream,omitempty"`        // nil defaults to true
	JSONOutput  bool                `json:"json_output,omitempty"`   // parse LLM output as JSON for variable expansion
	DisplayKey  string              `json:"display_key,omitempty"`   // JSON key to extract as per-step display text
	Explanation string              `json:"explanation,omitempty"`   // user-facing explanation shown at step start
	Question    string              `json:"question,omitempty"`      // question text for tool steps
	Options     []StepOption        `json:"options,omitempty"`       // structured options for ask_question_to_user
	Category    string              `json:"category,omitempty"`      // tab/category label for ask_question_to_user
}

// IsStreamVisible returns whether streaming output should be shown for this step.
func (s *WorkflowStepDef) IsStreamVisible() bool {
	return s.Stream == nil || *s.Stream
}

// StepResult holds output from a completed workflow step.
type StepResult struct {
	Output string
	Parsed map[string]any    // nil if json_output was false or parse failed
	Params map[string]string // input params received by this step
}

// AgentRunner is a persistent agent with maintained history.
type AgentRunner struct {
	Config   SubagentConfig
	LLM      *LLM
	Messages []anthropic.MessageParam
	System   []anthropic.TextBlockParam
	Tools    []anthropic.ToolUnionParam
	Tracker  *readTracker
	MaxTurns int

	// Per-Send() accumulated usage (reset at start of each Send call)
	LastInputTokens         int64
	LastOutputTokens        int64
	LastCacheCreationTokens int64
	LastCacheReadTokens     int64
	LastElapsed             time.Duration
}

// WorkflowRun tracks a running workflow.
type WorkflowRun struct {
	Def         *WorkflowDef
	StepAgents  map[string]*AgentRunner  // step_id -> runner used
	StepResults map[string]*StepResult   // step_id -> result
}

// FeatureToolOrchestrator is the feature flag name for the tool orchestrator mode.
const FeatureToolOrchestrator = "tool_orchestrator"

// FeatureReadClaudeMD enables loading CLAUDE.md files into the system prompt.
const FeatureReadClaudeMD = "read_claude_md"

// FeatureReadAgentsMD enables loading AGENTS.md files into the system prompt.
const FeatureReadAgentsMD = "read_agents_md"

// CurrentConfigVersion is the expected version number for settings.json files.
// Bump this when the config format changes in a breaking way.
const CurrentConfigVersion = 1

// configFile represents the top-level settings.json structure.
type configFile struct {
	Version   int             `json:"version,omitempty"`
	Agent     string          `json:"agent,omitempty"`
	Workflows []WorkflowDef   `json:"workflows"`
	Features  map[string]bool `json:"features,omitempty"`
}

// ProjectConfig holds parsed values from settings.json.
type ProjectConfig struct {
	Agent     string
	Workflows []*WorkflowDef
	Features  map[string]bool
}

// HasFeature returns whether the named feature flag is enabled.
func (c ProjectConfig) HasFeature(name string) bool {
	return c.Features[name]
}

// LoadProjectConfig reads config from one or more paths (applied in order, later overrides earlier)
// and returns agent name, workflows, and features.
func LoadProjectConfig(configPaths ...string) ProjectConfig {
	result := ProjectConfig{
		Agent: "general", // default
	}

	// Track which config path each workflow came from for disambiguation.
	type workflowOrigin struct {
		wf   *WorkflowDef
		path string
	}
	var allWorkflows []workflowOrigin

	for _, configPath := range configPaths {
		if configPath == "" {
			continue
		}
		data, err := os.ReadFile(configPath)
		if err != nil {
			continue
		}

		var cfg configFile
		if err := json.Unmarshal(data, &cfg); err != nil {
			log.Printf("[config] failed to parse config %s: %v", configPath, err)
			continue
		}

		if cfg.Version != CurrentConfigVersion {
			log.Printf("[config] %s: config version %d does not match expected version %d — please update your config file", configPath, cfg.Version, CurrentConfigVersion)
			continue
		}

		if cfg.Agent != "" {
			result.Agent = cfg.Agent
		}
		if len(cfg.Features) > 0 {
			if result.Features == nil {
				result.Features = make(map[string]bool)
			}
			for k, v := range cfg.Features {
				result.Features[k] = v
			}
		}
		for i := range cfg.Workflows {
			pf := cfg.Workflows[i]
			if err := validateWorkflow(&pf); err != nil {
				LogError("[workflow] invalid workflow '%s': %v", pf.Name, err)
				continue
			}
			allWorkflows = append(allWorkflows, workflowOrigin{wf: &pf, path: configPath})
		}
	}

	// Disambiguate duplicate workflow names by appending the origin.
	nameCount := make(map[string]int)
	for _, wo := range allWorkflows {
		nameCount[wo.wf.Name]++
	}
	for i := range allWorkflows {
		if nameCount[allWorkflows[i].wf.Name] > 1 {
			allWorkflows[i].wf.Name += " (" + configOriginLabel(allWorkflows[i].path) + ")"
		}
	}

	for _, wo := range allWorkflows {
		result.Workflows = append(result.Workflows, wo.wf)
	}

	return result
}

// configOriginLabel returns a short label for a config path.
// ~/.vix/settings.json becomes "~/.vix/settings.json".
// /some/path/myproject/.vix/settings.json becomes "myproject/.vix/settings.json".
func configOriginLabel(configPath string) string {
	homeDir, _ := os.UserHomeDir()
	if homeDir != "" && strings.HasPrefix(configPath, homeDir+string(filepath.Separator)) {
		return "~" + configPath[len(homeDir):]
	}
	// Use parent-of-.vix as project name: .../project/.vix/settings.json → project/.vix/settings.json
	dir := filepath.Dir(configPath)                // .../project/.vix
	vixDir := filepath.Base(dir)                   // .vix
	projectDir := filepath.Base(filepath.Dir(dir)) // project
	return projectDir + "/" + vixDir + "/" + filepath.Base(configPath)
}

// LoadWorkflows reads settings.json and returns the workflow list.
// Deprecated: Use LoadProjectConfig instead.
func LoadWorkflows(configPath string) []*WorkflowDef {
	cfg := LoadProjectConfig(configPath)
	return cfg.Workflows
}

// validateWorkflow checks that a workflow definition is consistent.
func validateWorkflow(pf *WorkflowDef) error {
	if pf.Name == "" {
		return fmt.Errorf("missing name")
	}
	if len(pf.Steps) == 0 {
		return fmt.Errorf("no steps defined")
	}

	for stepID := range pf.Steps {
		if stepID == "" {
			return fmt.Errorf("step has empty id")
		}
	}

	if pf.EntryPoint.ID == "" {
		return fmt.Errorf("missing entry_point")
	}
	if _, ok := pf.Steps[pf.EntryPoint.ID]; !ok {
		return fmt.Errorf("entry_point '%s' references unknown step", pf.EntryPoint.ID)
	}

	for stepID, step := range pf.Steps {
		if step.Type == "" {
			return fmt.Errorf("step '%s': missing type", stepID)
		}
		if step.Type != "agent" && step.Type != "tool" && step.Type != "bash" {
			return fmt.Errorf("step '%s': unknown type '%s' (must be 'agent', 'tool', or 'bash')", stepID, step.Type)
		}

		for _, ns := range step.NextSteps {
			if ns.ID != "" && ns.ID != "stop" {
				if _, ok := pf.Steps[ns.ID]; !ok {
					return fmt.Errorf("step '%s': next_step '%s' references unknown step", stepID, ns.ID)
				}
			}
		}

		if step.Type == "tool" {
			if step.Tool == "" {
				return fmt.Errorf("step '%s': type 'tool' requires 'tool' field", stepID)
			}
			for _, opt := range step.Options {
				for _, s := range opt.Steps {
					if s.ID != "" && s.ID != "stop" {
						if _, ok := pf.Steps[s.ID]; !ok {
							return fmt.Errorf("step '%s' option '%s' step references unknown step '%s'", stepID, opt.Title, s.ID)
						}
					}
				}
			}
			continue
		}

		if step.Type == "bash" {
			if step.Command == "" {
				return fmt.Errorf("step '%s': type 'bash' requires 'command' field", stepID)
			}
			if step.Agent != "" || step.ForkFrom != "" || step.Prompt != "" {
				return fmt.Errorf("step '%s': type 'bash' cannot have 'agent', 'fork_from', or 'prompt'", stepID)
			}
			continue
		}

		// Agent step validation
		hasAgent := step.Agent != ""
		hasFork := step.ForkFrom != ""

		if !hasAgent && !hasFork {
			return fmt.Errorf("step '%s': must have either 'agent' or 'fork_from'", stepID)
		}
		if hasAgent && hasFork {
			return fmt.Errorf("step '%s': cannot have both 'agent' and 'fork_from'", stepID)
		}

		if hasFork {
			if _, ok := pf.Steps[step.ForkFrom]; !ok {
				return fmt.Errorf("step '%s': fork_from '%s' references unknown step", stepID, step.ForkFrom)
			}
		}

		if step.Prompt == "" {
			return fmt.Errorf("step '%s': missing prompt", stepID)
		}
	}

	// Reachability check
	reachable := make(map[string]bool)
	var walk func(id string)
	walk = func(id string) {
		if id == "" || id == "stop" || reachable[id] {
			return
		}
		reachable[id] = true
		step := pf.Steps[id]
		for _, ns := range step.NextSteps {
			walk(ns.ID)
		}
		for _, opt := range step.Options {
			for _, s := range opt.Steps {
				walk(s.ID)
			}
		}
	}
	walk(pf.EntryPoint.ID)

	for stepID := range pf.Steps {
		if !reachable[stepID] {
			return fmt.Errorf("step '%s' is unreachable from entry_point '%s'", stepID, pf.EntryPoint.ID)
		}
	}

	return nil
}

// envVars returns template variables describing the runtime environment.
func envVars(cwd, model string) map[string]string {
	vars := map[string]string{
		"working_directory": cwd,
		"platform":          runtime.GOOS,
		"model":             model,
	}

	// Shell
	if sh := os.Getenv("SHELL"); sh != "" {
		vars["shell"] = filepath.Base(sh)
	} else {
		vars["shell"] = "sh"
	}

	// OS version (best-effort)
	if out, err := osexec.Command("uname", "-r").Output(); err == nil {
		vars["os_version"] = strings.TrimSpace(string(out))
	}

	// Git repo check
	if _, err := os.Stat(filepath.Join(cwd, ".git")); err == nil {
		vars["is_git_repo"] = "Yes"
	} else {
		vars["is_git_repo"] = "No"
	}

	return vars
}

// NewAgentRunner creates a persistent agent for a workflow.
func NewAgentRunner(config SubagentConfig, apiKey, parentModel, cwd string) *AgentRunner {
	model := config.Model
	if model == "" {
		model = parentModel
	}

	maxTurns := config.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 20
	}

	llm := NewLLM(apiKey, model)
	tools := FilterToolSchemas(config.Tools)

	projectVix := filepath.Join(cwd, ".vix")
	sysPrompt := promptloader.GetLoader().Resolve(
		config.SystemPrompt,
		envVars(cwd, model),
		promptloader.JoinSearchDirs(projectVix, configpkg.HomeVixDir()),
		nil,
	)

	return &AgentRunner{
		Config:   config,
		LLM:     llm,
		Messages: nil,
		System:   []anthropic.TextBlockParam{{Text: sysPrompt}},
		Tools:    tools,
		Tracker:  newReadTracker(),
		MaxTurns: maxTurns,
	}
}

// Clone creates a deep copy of the agent runner (for fork_from).
func (a *AgentRunner) Clone(apiKey string) *AgentRunner {
	msgs := make([]anthropic.MessageParam, len(a.Messages))
	copy(msgs, a.Messages)

	sys := make([]anthropic.TextBlockParam, len(a.System))
	copy(sys, a.System)

	tools := make([]anthropic.ToolUnionParam, len(a.Tools))
	copy(tools, a.Tools)

	return &AgentRunner{
		Config:   a.Config,
		LLM:     NewLLM(apiKey, a.LLM.model),
		Messages: msgs,
		System:   sys,
		Tools:    tools,
		Tracker:  newReadTracker(),
		MaxTurns: a.MaxTurns,
	}
}

// Send sends a message to the agent, runs the LLM loop with tool dispatch,
// and returns the text output. Conversation history is preserved across calls.
func (a *AgentRunner) Send(
	ctx context.Context,
	userPrompt string,
	executeTool func(name string, params map[string]any, cwd string) (*ToolResult, error),
	streamCallback func(delta string),
	cwd string,
	hooks *TurnHooks,
) (string, error) {
	a.LastInputTokens = 0
	a.LastOutputTokens = 0
	a.LastCacheCreationTokens = 0
	a.LastCacheReadTokens = 0
	a.LastElapsed = 0

	a.Messages = append(a.Messages, anthropic.NewUserMessage(
		anthropic.NewTextBlock(userPrompt),
	))

	for turn := 0; turn < a.MaxTurns; turn++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		msg, elapsed, err := a.LLM.StreamMessage(ctx, a.System, a.Messages, a.Tools, streamCallback)
		if err != nil {
			return "", err
		}

		a.LastInputTokens += msg.Usage.InputTokens
		a.LastOutputTokens += msg.Usage.OutputTokens
		a.LastCacheCreationTokens += msg.Usage.CacheCreationInputTokens
		a.LastCacheReadTokens += msg.Usage.CacheReadInputTokens
		a.LastElapsed += elapsed

		if hooks != nil && hooks.OnStreamDone != nil {
			hooks.OnStreamDone(msg.Usage.InputTokens, msg.Usage.OutputTokens, msg.Usage.CacheCreationInputTokens, msg.Usage.CacheReadInputTokens, elapsed.Milliseconds())
		}

		a.Messages = append(a.Messages, msg.ToParam())

		if msg.StopReason == "end_turn" {
			text := extractTextFromMessage(msg)
			return text, nil
		}

		if msg.StopReason == "tool_use" {
			toolResults := subagentDispatchToolCalls(ctx, msg, executeTool, cwd, a.Tracker, hooks)
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			a.Messages = append(a.Messages, anthropic.NewUserMessage(toolResults...))
			continue
		}

		if msg.StopReason == "max_tokens" {
			return extractTextFromMessage(msg), ErrMaxTokens
		}

		return "", fmt.Errorf("unexpected stop reason: %s", msg.StopReason)
	}

	lastText := ""
	for i := len(a.Messages) - 1; i >= 0; i-- {
		for _, block := range a.Messages[i].Content {
			if block.OfText != nil {
				lastText += block.OfText.Text
			}
		}
		if lastText != "" {
			break
		}
	}
	if lastText == "" {
		lastText = fmt.Sprintf("Workflow agent '%s' reached max turns (%d) without completing.", a.Config.Name, a.MaxTurns)
	}
	return lastText, nil
}

// stripMarkdownFence removes optional markdown code fences from a string.
// It searches for the first ```json or ``` fence anywhere in the string,
// so preamble text before the fence is handled correctly.
func stripMarkdownFence(s string) string {
	s = strings.TrimSpace(s)
	// Find a ```json fence anywhere in the string
	if idx := strings.Index(s, "```json"); idx >= 0 {
		inner := s[idx+len("```json"):]
		if end := strings.LastIndex(inner, "```"); end >= 0 {
			return strings.TrimSpace(inner[:end])
		}
	}
	// Fall back to a generic ``` fence
	if idx := strings.Index(s, "```"); idx >= 0 {
		inner := s[idx+len("```"):]
		if end := strings.LastIndex(inner, "```"); end >= 0 {
			return strings.TrimSpace(inner[:end])
		}
	}
	return s
}

// buildStepVars builds a variable map from step results.
// For each step, it sets "step.<id>" to the raw output and includes input params
// as "step.<id>.<param>". If the step had json_output and parsing succeeded,
// each JSON key becomes "step.<id>.<key>".
func buildStepVars(results map[string]*StepResult) map[string]string {
	vars := make(map[string]string)
	for sid, r := range results {
		vars["step."+sid] = r.Output
		// Include step input params
		for k, v := range r.Params {
			vars["step."+sid+"."+k] = v
		}
		// Include parsed JSON fields (only when json_output was true and parse succeeded)
		if r.Parsed != nil {
			for k, v := range r.Parsed {
				switch val := v.(type) {
				case string:
					vars["step."+sid+"."+k] = val
				default:
					_ = val
					if b, err := json.MarshalIndent(v, "", "  "); err == nil {
						vars["step."+sid+"."+k] = string(b)
					}
				}
			}
		}
	}
	return vars
}

// resolveParams resolves parameter values against a variable pool.
// All $(...) references within values are replaced with their corresponding vars.
func resolveParams(params map[string]string, vars map[string]string) map[string]string {
	if len(params) == 0 {
		return nil
	}
	resolved := make(map[string]string, len(params))
	for k, v := range params {
		result := v
		for varName, varVal := range vars {
			result = strings.ReplaceAll(result, "$("+varName+")", varVal)
		}
		resolved[k] = result
	}
	return resolved
}

// resolveTemplateString replaces all $(key) occurrences in a string with values from vars.
func resolveTemplateString(tmpl string, vars map[string]string) string {
	result := tmpl
	for varName, varVal := range vars {
		result = strings.ReplaceAll(result, "$("+varName+")", varVal)
	}
	return result
}

// extractStepSummary extracts a display summary from JSON output using the given key.
func extractStepSummary(raw string, key string) string {
	if key == "" {
		return ""
	}
	stripped := stripMarkdownFence(raw)
	var obj map[string]any
	if err := json.Unmarshal([]byte(stripped), &obj); err != nil {
		return ""
	}
	if s, ok := obj[key].(string); ok {
		return s
	}
	return ""
}

// stepToolTracker counts tool calls and accumulates output line counts per tool.
type stepToolTracker struct {
	calls map[string]*toolCallAcc
	order []string
}

type toolCallAcc struct {
	Count     int
	LineCount int
}

func newStepToolTracker() *stepToolTracker {
	return &stepToolTracker{calls: make(map[string]*toolCallAcc)}
}

func (t *stepToolTracker) RecordCall(name string) {
	acc, ok := t.calls[name]
	if !ok {
		acc = &toolCallAcc{}
		t.calls[name] = acc
		t.order = append(t.order, name)
	}
	acc.Count++
}

func (t *stepToolTracker) RecordResult(name, output string) {
	acc, ok := t.calls[name]
	if !ok {
		acc = &toolCallAcc{}
		t.calls[name] = acc
		t.order = append(t.order, name)
	}
	lines := strings.Count(output, "\n")
	if output != "" && !strings.HasSuffix(output, "\n") {
		lines++
	}
	acc.LineCount += lines
}

func (t *stepToolTracker) Stats() []protocol.ToolStat {
	var stats []protocol.ToolStat
	for _, name := range t.order {
		acc := t.calls[name]
		stats = append(stats, protocol.ToolStat{
			Name:    name,
			Calls:   acc.Count,
			Summary: aggregateToolSummary(name, acc),
		})
	}
	return stats
}

func aggregateToolSummary(name string, acc *toolCallAcc) string {
	switch name {
	case "read_file":
		return fmt.Sprintf("%d lines read", acc.LineCount)
	case "grep":
		if acc.LineCount == 0 {
			return "no matches"
		}
		return fmt.Sprintf("%d results", acc.LineCount)
	case "glob_files":
		if acc.LineCount == 0 {
			return "no matches"
		}
		return fmt.Sprintf("%d files", acc.LineCount)
	case "bash":
		return fmt.Sprintf("%d lines of output", acc.LineCount)
	case "write_file":
		return fmt.Sprintf("%d files written", acc.Count)
	case "edit_file":
		return fmt.Sprintf("%d edits", acc.Count)
	default:
		return ""
	}
}

// executeToolStep runs a tool-type step and returns the next step refs and output text.
func (s *Session) executeToolStep(step WorkflowStepDef, baseVars map[string]string) (nextRefs []StepRef, output string, err error) {
	switch step.Tool {
	case "ask_question_to_user":
		question := step.Question
		if question == "" {
			question = "Review the output and provide feedback."
		}
		category := step.Category
		if category == "" {
			category = "Review"
		}

		var richOptions []protocol.EventQuestionOption
		for _, opt := range step.Options {
			richOptions = append(richOptions, protocol.EventQuestionOption{
				Title:        opt.Title,
				Description:  opt.Description,
				HasUserInput: opt.HasUserInput,
			})
		}

		s.emit("event.user_question", protocol.EventUserQuestion{
			Question:    question,
			RichOptions: richOptions,
			Category:    category,
		})

		cmd, ok := s.waitForCommand(s.ctx, "session.user_answer")
		if !ok {
			return nil, "", s.ctx.Err()
		}

		var answerData protocol.SessionUserAnswerData
		json.Unmarshal(cmd.Data, &answerData)
		answer := strings.TrimSpace(answerData.Answer)

		for _, opt := range step.Options {
			if strings.EqualFold(answer, opt.Title) {
				outputText := "User selected: " + opt.Title
				if opt.HasUserInput && strings.TrimSpace(answerData.Text) != "" {
					outputText = strings.TrimSpace(answerData.Text)
				}

				// Resolve option params against base vars + user_text
				if len(opt.Steps) > 0 {
					resolveVars := make(map[string]string, len(baseVars)+1)
					for k, v := range baseVars {
						resolveVars[k] = v
					}
					if opt.HasUserInput {
						resolveVars["user_text"] = strings.TrimSpace(answerData.Text)
					}
					var resolved []StepRef
					for _, s := range opt.Steps {
						resolved = append(resolved, StepRef{
							ID:     s.ID,
							Params: resolveParams(s.Params, resolveVars),
						})
					}
					return resolved, outputText, nil
				}
				return nil, outputText, nil
			}
		}

		// No match — fallback to NextSteps
		if len(step.NextSteps) > 0 {
			return step.NextSteps, "User selected: " + answer, nil
		}
		return nil, "User selected: " + answer, nil

	default:
		result := s.executeToolConfirmed(step.Tool, map[string]any{})
		if result.IsError {
			return nil, "", fmt.Errorf("tool '%s' failed: %s", step.Tool, result.Output)
		}
		return nil, result.Output, nil
	}
}

// executeParallelSteps launches multiple terminal steps in parallel goroutines.
func (s *Session) executeParallelSteps(
	refs []StepRef,
	pf *WorkflowDef,
	exec *WorkflowRun,
	baseVars map[string]string,
	stepCosts *[]protocol.StepCost,
	logicalStep *int,
	workflowStart time.Time,
	apiKey, parentModel string,
	prompt string,
	executeTool func(name string, params map[string]any, cwd string) (*ToolResult, error),
) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	errs := make([]error, len(refs))

	for i, ref := range refs {
		wg.Add(1)
		go func(idx int, ref StepRef) {
			defer wg.Done()
			step := pf.Steps[ref.ID]
			stepID := ref.ID
			stepParams := ref.Params

			mu.Lock()
			*logicalStep++
			myLogicalStep := *logicalStep
			mu.Unlock()

			s.emit("event.workflow_step_start", protocol.EventWorkflowStepStart{
				StepID:      stepID,
				StepIdx:     myLogicalStep,
				Total:       0,
				Explanation: step.Explanation,
			})

			stepStart := time.Now()

			switch step.Type {
			case "bash":
				vars := make(map[string]string, len(baseVars))
				for k, v := range baseVars {
					vars[k] = v
				}
				mu.Lock()
				for k, v := range buildStepVars(exec.StepResults) {
					vars[k] = v
				}
				mu.Unlock()
				for k, v := range stepParams {
					vars[k] = v
				}
				resolvedCmd := resolveTemplateString(step.Command, vars)
				resolvedInput := resolveTemplateString(step.Input, vars)

				cmd := osexec.Command("bash", "-c", resolvedCmd)
				if resolvedInput != "" {
					cmd.Stdin = strings.NewReader(resolvedInput)
				}
				output, err := cmd.CombinedOutput()
				stepElapsed := time.Since(stepStart).Milliseconds()

				mu.Lock()
				exec.StepResults[stepID] = &StepResult{Output: string(output), Params: stepParams}
				*stepCosts = append(*stepCosts, protocol.StepCost{
					StepID:     stepID,
					Explanation: step.Explanation,
					DurationMs: stepElapsed,
				})
				mu.Unlock()

				if err != nil {
					s.emit("event.workflow_step_done", protocol.EventWorkflowStepDone{
						StepID: stepID, StepIdx: myLogicalStep, Success: false, DurationMs: stepElapsed,
					})
					errs[idx] = fmt.Errorf("step '%s' bash failed: %w (output: %s)", stepID, err, string(output))
					return
				}
				s.emit("event.workflow_step_done", protocol.EventWorkflowStepDone{
					StepID: stepID, StepIdx: myLogicalStep, Success: true, DurationMs: stepElapsed,
				})

			case "agent":
				var agent *AgentRunner
				if step.Agent != "" {
					config, ok := s.customAgents[step.Agent]
					if !ok {
						errs[idx] = fmt.Errorf("step '%s': agent '%s' not found", stepID, step.Agent)
						return
					}
					agent = NewAgentRunner(config, apiKey, parentModel, s.cwd)
					if s.headless {
						agent.Tools = ExcludeTools(agent.Tools, "ask_question_to_user")
					}
				} else if step.ForkFrom != "" {
					mu.Lock()
					source, ok := exec.StepAgents[step.ForkFrom]
					mu.Unlock()
					if !ok {
						errs[idx] = fmt.Errorf("step '%s': fork_from '%s' has no agent instance", stepID, step.ForkFrom)
						return
					}
					agent = source.Clone(apiKey)
				}

				vars := envVars(s.cwd, s.model)
				vars["workflow.prompt"] = prompt
				mu.Lock()
				for k, v := range buildStepVars(exec.StepResults) {
					vars[k] = v
				}
				mu.Unlock()
				for k, v := range stepParams {
					vars[k] = v
				}

				resolvedMessage := promptloader.GetLoader().Resolve(
					step.Prompt, vars, s.searchDirs(), nil,
				)

				streamCb := func(delta string) {
					if step.IsStreamVisible() {
						s.emit("event.stream_chunk", protocol.EventStreamChunk{Text: delta})
					}
				}

				stepExecuteTool := func(name string, params map[string]any, cwd string) (*ToolResult, error) {
					for _, t := range step.DenyTools {
						if t == name {
							return &ToolResult{Output: fmt.Sprintf("tool '%s' is denied in step '%s'", name, stepID), IsError: true}, nil
						}
					}
					return executeTool(name, params, cwd)
				}

				output, err := agent.Send(s.ctx, resolvedMessage, stepExecuteTool, streamCb, s.cwd, s.emitHooks())
				stepElapsed := time.Since(stepStart).Milliseconds()

				mu.Lock()
				exec.StepResults[stepID] = &StepResult{Output: output, Params: stepParams}
				exec.StepAgents[stepID] = agent
				*stepCosts = append(*stepCosts, protocol.StepCost{
					StepID:              stepID,
					Explanation:         step.Explanation,
					Model:               agent.LLM.Model(),
					InputTokens:         agent.LastInputTokens,
					OutputTokens:        agent.LastOutputTokens,
					CacheCreationTokens: agent.LastCacheCreationTokens,
					CacheReadTokens:     agent.LastCacheReadTokens,
					Cost:                protocol.CalculateCost(agent.LLM.Model(), agent.LastInputTokens, agent.LastOutputTokens, agent.LastCacheCreationTokens, agent.LastCacheReadTokens),
					DurationMs:          stepElapsed,
				})
				mu.Unlock()

				if err != nil {
					s.emit("event.workflow_step_done", protocol.EventWorkflowStepDone{
						StepID: stepID, StepIdx: myLogicalStep, Success: false, DurationMs: stepElapsed,
					})
					errs[idx] = fmt.Errorf("step '%s' failed: %w", stepID, err)
					return
				}
				s.emit("event.workflow_step_done", protocol.EventWorkflowStepDone{
					StepID: stepID, StepIdx: myLogicalStep, Success: true, DurationMs: stepElapsed,
				})

			case "tool":
				toolVars := make(map[string]string, len(baseVars))
				for k, v := range baseVars {
					toolVars[k] = v
				}
				mu.Lock()
				for k, v := range buildStepVars(exec.StepResults) {
					toolVars[k] = v
				}
				mu.Unlock()
				for k, v := range stepParams {
					toolVars[k] = v
				}

				_, output, err := s.executeToolStep(step, toolVars)
				stepElapsed := time.Since(stepStart).Milliseconds()

				mu.Lock()
				exec.StepResults[stepID] = &StepResult{Output: output, Params: stepParams}
				*stepCosts = append(*stepCosts, protocol.StepCost{
					StepID: stepID, Explanation: step.Explanation, DurationMs: stepElapsed,
				})
				mu.Unlock()

				if err != nil {
					s.emit("event.workflow_step_done", protocol.EventWorkflowStepDone{
						StepID: stepID, StepIdx: myLogicalStep, Success: false, DurationMs: stepElapsed,
					})
					errs[idx] = fmt.Errorf("step '%s' failed: %w", stepID, err)
					return
				}
				s.emit("event.workflow_step_done", protocol.EventWorkflowStepDone{
					StepID: stepID, StepIdx: myLogicalStep, Success: true, DurationMs: stepElapsed,
				})
			}
		}(i, ref)
	}

	wg.Wait()

	// Collect errors
	var errMsgs []string
	for _, e := range errs {
		if e != nil {
			errMsgs = append(errMsgs, e.Error())
		}
	}
	if len(errMsgs) > 0 {
		return fmt.Errorf("parallel steps failed: %s", strings.Join(errMsgs, "; "))
	}
	return nil
}

// executeWorkflow runs a full workflow to completion.
func (s *Session) executeWorkflow(pf *WorkflowDef, prompt string) error {
	exec := &WorkflowRun{
		Def:         pf,
		StepAgents:  make(map[string]*AgentRunner),
		StepResults: make(map[string]*StepResult),
	}

	apiKey := s.llm.APIKey()
	parentModel := s.model

	executeTool := func(name string, params map[string]any, cwd string) (*ToolResult, error) {
		return s.executeToolConfirmed(name, params), nil
	}

	// Emit workflow start
	s.emit("event.workflow_start", protocol.EventWorkflowStart{
		WorkflowName: pf.Name,
		TotalSteps:   len(pf.Steps),
	})

	var stepCosts []protocol.StepCost
	workflowStart := time.Now()
	var stopped bool

	// Base vars: workflow.prompt is the magic variable
	baseVars := map[string]string{
		"workflow.prompt": prompt,
	}

	// Resolve entry point params
	currentRef := &StepRef{
		ID:     pf.EntryPoint.ID,
		Params: resolveParams(pf.EntryPoint.Params, baseVars),
	}
	var routedFrom string
	var logicalStep int
	const maxIterations = 100

	for iteration := 0; currentRef != nil && currentRef.ID != "" && currentRef.ID != "stop" && iteration < maxIterations; iteration++ {
		step := pf.Steps[currentRef.ID]
		stepID := currentRef.ID
		stepParams := currentRef.Params
		logicalStep++

		if s.ctx.Err() != nil {
			s.emit("event.workflow_complete", protocol.EventWorkflowComplete{
				WorkflowName: pf.Name,
				Success:      false,
				DurationMs:   time.Since(workflowStart).Milliseconds(),
			})
			s.activePlan = nil
			return s.ctx.Err()
		}

		s.emit("event.workflow_step_start", protocol.EventWorkflowStepStart{
			StepID:      stepID,
			StepIdx:     logicalStep,
			Total:       0,
			Explanation: step.Explanation,
		})

		stepStart := time.Now()

		switch step.Type {
		case "bash":
			vars := make(map[string]string, len(baseVars))
			for k, v := range baseVars {
				vars[k] = v
			}
			for k, v := range buildStepVars(exec.StepResults) {
				vars[k] = v
			}
			for k, v := range stepParams {
				vars[k] = v
			}
			resolvedCmd := resolveTemplateString(step.Command, vars)
			resolvedInput := resolveTemplateString(step.Input, vars)

			cmd := osexec.Command("bash", "-c", resolvedCmd)
			if resolvedInput != "" {
				cmd.Stdin = strings.NewReader(resolvedInput)
			}
			cmdOutput, cmdErr := cmd.CombinedOutput()
			exec.StepResults[stepID] = &StepResult{Output: string(cmdOutput), Params: stepParams}
			stepElapsed := time.Since(stepStart).Milliseconds()
			stepCosts = append(stepCosts, protocol.StepCost{
				StepID:      stepID,
				Explanation: step.Explanation,
				DurationMs:  stepElapsed,
			})
			if cmdErr != nil {
				s.emit("event.workflow_step_done", protocol.EventWorkflowStepDone{
					StepID: stepID, StepIdx: logicalStep, Success: false, DurationMs: stepElapsed,
				})
				s.emit("event.workflow_complete", protocol.EventWorkflowComplete{
					WorkflowName: pf.Name, Success: false, StepCosts: stepCosts,
					DurationMs: time.Since(workflowStart).Milliseconds(),
				})
				s.activePlan = nil
				return fmt.Errorf("step '%s' bash failed: %w (output: %s)", stepID, cmdErr, string(cmdOutput))
			}
			s.emit("event.workflow_step_done", protocol.EventWorkflowStepDone{
				StepID: stepID, StepIdx: logicalStep, Success: true, DurationMs: stepElapsed,
			})

			if len(step.NextSteps) == 1 {
				currentRef = &StepRef{
					ID:     step.NextSteps[0].ID,
					Params: resolveParams(step.NextSteps[0].Params, vars),
				}
			} else {
				currentRef = nil
			}

		case "tool":
			toolVars := make(map[string]string, len(baseVars))
			for k, v := range baseVars {
				toolVars[k] = v
			}
			for k, v := range buildStepVars(exec.StepResults) {
				toolVars[k] = v
			}
			for k, v := range stepParams {
				toolVars[k] = v
			}

			nextRefs, output, err := s.executeToolStep(step, toolVars)
			if err != nil {
				s.activePlan = nil
				s.emit("event.workflow_step_done", protocol.EventWorkflowStepDone{
					StepID:     stepID,
					StepIdx:    logicalStep,
					Success:    false,
					DurationMs: time.Since(stepStart).Milliseconds(),
				})
				s.emit("event.workflow_complete", protocol.EventWorkflowComplete{
					WorkflowName: pf.Name,
					Success:      false,
					StepCosts:    stepCosts,
					DurationMs:   time.Since(workflowStart).Milliseconds(),
				})
				return fmt.Errorf("step '%s' failed: %w", stepID, err)
			}
			exec.StepResults[stepID] = &StepResult{Output: output}
			stepElapsed := time.Since(stepStart).Milliseconds()
			stepCosts = append(stepCosts, protocol.StepCost{
				StepID:      stepID,
				Explanation: step.Explanation,
				DurationMs:  stepElapsed,
			})
			s.emit("event.workflow_step_done", protocol.EventWorkflowStepDone{
				StepID:     stepID,
				StepIdx:    logicalStep,
				Success:    true,
				DurationMs: stepElapsed,
			})

			if len(nextRefs) > 0 {
				// Check for stop
				for _, nr := range nextRefs {
					if nr.ID == "stop" {
						stopped = true
						goto done
					}
				}
				if len(nextRefs) == 1 {
					routedFrom = stepID
					currentRef = &nextRefs[0]
					continue
				}
				// Multiple next refs — parallel execution
				if err := s.executeParallelSteps(nextRefs, pf, exec, baseVars, &stepCosts, &logicalStep, workflowStart, apiKey, parentModel, prompt, executeTool); err != nil {
					s.activePlan = nil
					s.emit("event.workflow_complete", protocol.EventWorkflowComplete{
						WorkflowName: pf.Name, Success: false, StepCosts: stepCosts,
						DurationMs: time.Since(workflowStart).Milliseconds(),
					})
					return err
				}
				currentRef = nil
				continue
			}
			if len(step.NextSteps) > 0 {
				if len(step.NextSteps) == 1 {
					currentRef = &StepRef{
						ID:     step.NextSteps[0].ID,
						Params: resolveParams(step.NextSteps[0].Params, toolVars),
					}
				} else {
					// Parallel next steps from tool step
					resolved := make([]StepRef, len(step.NextSteps))
					for i, ns := range step.NextSteps {
						resolved[i] = StepRef{ID: ns.ID, Params: resolveParams(ns.Params, toolVars)}
					}
					if err := s.executeParallelSteps(resolved, pf, exec, baseVars, &stepCosts, &logicalStep, workflowStart, apiKey, parentModel, prompt, executeTool); err != nil {
						s.activePlan = nil
						s.emit("event.workflow_complete", protocol.EventWorkflowComplete{
							WorkflowName: pf.Name, Success: false, StepCosts: stepCosts,
							DurationMs: time.Since(workflowStart).Milliseconds(),
						})
						return err
					}
					currentRef = nil
					continue
				}
			} else {
				currentRef = nil
			}

		case "agent":
			var agent *AgentRunner
			var agentLabel string

			if existing, ok := exec.StepAgents[stepID]; ok && routedFrom != "" {
				// Loop-back: reuse existing agent instance
				agent = existing
				agentLabel = stepID + " (resumed)"
			} else if step.Agent != "" {
				config, ok := s.customAgents[step.Agent]
				if !ok {
					s.activePlan = nil
					s.emit("event.workflow_complete", protocol.EventWorkflowComplete{
						WorkflowName: pf.Name,
						Success:      false,
						DurationMs:   time.Since(workflowStart).Milliseconds(),
					})
					return fmt.Errorf("step '%s': agent '%s' not found in custom agents", stepID, step.Agent)
				}
				agent = NewAgentRunner(config, apiKey, parentModel, s.cwd)
				if s.headless {
					agent.Tools = ExcludeTools(agent.Tools, "ask_question_to_user")
				}
				agentLabel = step.Agent
			} else if step.ForkFrom != "" {
				source, ok := exec.StepAgents[step.ForkFrom]
				if !ok {
					s.activePlan = nil
					s.emit("event.workflow_complete", protocol.EventWorkflowComplete{
						WorkflowName: pf.Name,
						Success:      false,
						DurationMs:   time.Since(workflowStart).Milliseconds(),
					})
					return fmt.Errorf("step '%s': fork_from '%s' has no agent instance", stepID, step.ForkFrom)
				}
				agent = source.Clone(apiKey)
				agentLabel = stepID + " (from " + step.ForkFrom + ")"
			}
			_ = agentLabel

			// Resolve prompt message
			var resolvedMessage string
			if routedFrom != "" && exec.StepAgents[stepID] != nil {
				// Loop-back: use step params or previous step output
				if len(stepParams) > 0 {
					keys := make([]string, 0, len(stepParams))
					for k := range stepParams {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					var parts []string
					for _, k := range keys {
						if v := stepParams[k]; v != "" {
							parts = append(parts, v)
						}
					}
					if len(parts) > 0 {
						resolvedMessage = strings.Join(parts, "\n")
					} else if prev := exec.StepResults[routedFrom]; prev != nil {
						resolvedMessage = prev.Output
					}
				} else if prev := exec.StepResults[routedFrom]; prev != nil {
					resolvedMessage = prev.Output
				}
				routedFrom = ""
			} else {
				vars := envVars(s.cwd, s.model)
				vars["workflow.prompt"] = prompt
				for k, v := range buildStepVars(exec.StepResults) {
					vars[k] = v
				}
				for k, v := range stepParams {
					vars[k] = v
				}

				resolvedMessage = promptloader.GetLoader().Resolve(
					step.Prompt,
					vars,
					s.searchDirs(),
					nil,
				)
				routedFrom = ""
			}

			// Tool executor with deny_tools enforcement
			stepExecuteTool := func(name string, params map[string]any, cwd string) (*ToolResult, error) {
				if len(step.DenyTools) > 0 {
					for _, t := range step.DenyTools {
						if t == name {
							return &ToolResult{
								Output:  fmt.Sprintf("tool '%s' is denied in step '%s'", name, stepID),
								IsError: true,
							}, nil
						}
					}
				}
				if name == "ask_question_to_user" {
					return s.handleAskQuestionsBatch(s.ctx, params)
				}
				return executeTool(name, params, cwd)
			}

			streamCb := func(delta string) {
				if step.IsStreamVisible() {
					s.emit("event.stream_chunk", protocol.EventStreamChunk{Text: delta})
				}
			}

			tracker := newStepToolTracker()
			baseHooks := s.emitHooks()
			stepHooks := &TurnHooks{
				OnStreamDelta: baseHooks.OnStreamDelta,
				OnStreamDone:  baseHooks.OnStreamDone,
				OnToolCall: func(toolID, name, summary, reason string) {
					tracker.RecordCall(name)
					baseHooks.OnToolCall(toolID, name, summary, reason)
				},
				OnToolResult: func(toolID, name string, input map[string]any, output string, isError bool) {
					if !isError {
						tracker.RecordResult(name, output)
					}
					baseHooks.OnToolResult(toolID, name, input, output, isError)
				},
			}

			output, err := agent.Send(s.ctx, resolvedMessage, stepExecuteTool, streamCb, s.cwd, stepHooks)

			// Handle max_tokens
			if errors.Is(err, ErrMaxTokens) {
				result, askErr := s.handleAskQuestionsBatch(s.ctx, map[string]any{
					"questions": []any{map[string]any{
						"id":       "continue",
						"category": "Output limit",
						"question": "The AI reached its maximum output length for this step. This can happen with large or complex tasks. Would you like to let it continue from where it stopped?",
						"options":  []any{"Continue", "Stop"},
					}},
				})
				if askErr == nil && result != nil && result.Output == "Continue" {
					output, err = agent.Send(s.ctx, "Continue from where you left off.", stepExecuteTool, streamCb, s.cwd, stepHooks)
				}
			}

			if err != nil {
				stepElapsed := time.Since(stepStart).Milliseconds()
				stepCosts = append(stepCosts, protocol.StepCost{
					StepID:              stepID,
					Explanation:         step.Explanation,
					Model:               agent.LLM.Model(),
					InputTokens:         agent.LastInputTokens,
					OutputTokens:        agent.LastOutputTokens,
					CacheCreationTokens: agent.LastCacheCreationTokens,
					CacheReadTokens:     agent.LastCacheReadTokens,
					Cost:                protocol.CalculateCost(agent.LLM.Model(), agent.LastInputTokens, agent.LastOutputTokens, agent.LastCacheCreationTokens, agent.LastCacheReadTokens),
					DurationMs:          stepElapsed,
				})
				s.emit("event.workflow_step_done", protocol.EventWorkflowStepDone{
					StepID:              stepID,
					StepIdx:             logicalStep,
					Success:             false,
					Model:               agent.LLM.Model(),
					InputTokens:         agent.LastInputTokens,
					OutputTokens:        agent.LastOutputTokens,
					CacheCreationTokens: agent.LastCacheCreationTokens,
					CacheReadTokens:     agent.LastCacheReadTokens,
					ToolStats:           tracker.Stats(),
					DurationMs:          stepElapsed,
				})
				s.emit("event.workflow_complete", protocol.EventWorkflowComplete{
					WorkflowName: pf.Name,
					Success:      false,
					StepCosts:    stepCosts,
					DurationMs:   time.Since(workflowStart).Milliseconds(),
				})
				s.activePlan = nil
				return fmt.Errorf("step '%s' failed: %w", stepID, err)
			}

			// Parse JSON if json_output is set
			var parsed map[string]any
			if step.JSONOutput {
				stripped := stripMarkdownFence(output)
				var obj map[string]any
				if err := json.Unmarshal([]byte(stripped), &obj); err == nil {
					parsed = obj
				}
			}

			exec.StepResults[stepID] = &StepResult{
				Output: output,
				Parsed: parsed,
				Params: stepParams,
			}

			displayText := extractStepSummary(output, step.DisplayKey)
			if step.DisplayKey != "" {
				sf := stripMarkdownFence(output)
				if len(sf) > 200 {
					sf = sf[:200]
				}
				log.Printf("[DEBUG] step=%q display_key=%q output_len=%d stripped_fence=%q display_text=%q",
					stepID, step.DisplayKey, len(output), sf, displayText)
			}
			exec.StepAgents[stepID] = agent

			// Write step output to file if Output path is set
			if step.Output != "" {
				outPath := step.Output
				if !filepath.IsAbs(outPath) {
					outPath = filepath.Join(s.cwd, outPath)
				}
				os.MkdirAll(filepath.Dir(outPath), 0o755)
				os.WriteFile(outPath, []byte(output), 0o644)
			}

			stepElapsed := time.Since(stepStart).Milliseconds()
			stepCosts = append(stepCosts, protocol.StepCost{
				StepID:              stepID,
				Explanation:         step.Explanation,
				Model:               agent.LLM.Model(),
				InputTokens:         agent.LastInputTokens,
				OutputTokens:        agent.LastOutputTokens,
				CacheCreationTokens: agent.LastCacheCreationTokens,
				CacheReadTokens:     agent.LastCacheReadTokens,
				Cost:                protocol.CalculateCost(agent.LLM.Model(), agent.LastInputTokens, agent.LastOutputTokens, agent.LastCacheCreationTokens, agent.LastCacheReadTokens),
				DurationMs:          stepElapsed,
			})

			s.emit("event.workflow_step_done", protocol.EventWorkflowStepDone{
				StepID:              stepID,
				StepIdx:             logicalStep,
				Success:             true,
				Display:             displayText,
				Model:               agent.LLM.Model(),
				InputTokens:         agent.LastInputTokens,
				OutputTokens:        agent.LastOutputTokens,
				CacheCreationTokens: agent.LastCacheCreationTokens,
				CacheReadTokens:     agent.LastCacheReadTokens,
				ToolStats:           tracker.Stats(),
				DurationMs:          stepElapsed,
			})

			// Advance to next step(s)
			if len(step.NextSteps) > 0 {
				advanceVars := make(map[string]string, len(baseVars))
				for k, v := range baseVars {
					advanceVars[k] = v
				}
				for k, v := range buildStepVars(exec.StepResults) {
					advanceVars[k] = v
				}
				if len(step.NextSteps) == 1 {
					currentRef = &StepRef{
						ID:     step.NextSteps[0].ID,
						Params: resolveParams(step.NextSteps[0].Params, advanceVars),
					}
				} else {
					// Parallel next steps
					resolved := make([]StepRef, len(step.NextSteps))
					for i, ns := range step.NextSteps {
						resolved[i] = StepRef{ID: ns.ID, Params: resolveParams(ns.Params, advanceVars)}
					}
					if err := s.executeParallelSteps(resolved, pf, exec, baseVars, &stepCosts, &logicalStep, workflowStart, apiKey, parentModel, prompt, executeTool); err != nil {
						s.activePlan = nil
						s.emit("event.workflow_complete", protocol.EventWorkflowComplete{
							WorkflowName: pf.Name, Success: false, StepCosts: stepCosts,
							DurationMs: time.Since(workflowStart).Milliseconds(),
						})
						return err
					}
					currentRef = nil
				}
			} else {
				currentRef = nil
			}
		}
	}

done:
	var summary string
	if pf.Summary != "" {
		summaryVars := buildStepVars(exec.StepResults)
		resolved := promptloader.GetLoader().Resolve(
			pf.Summary, summaryVars, s.searchDirs(), nil,
		)
		if !strings.Contains(resolved, "$(") {
			summary = resolved
		}
	}

	s.emit("event.workflow_complete", protocol.EventWorkflowComplete{
		WorkflowName: pf.Name,
		Success:      true,
		Summary:      summary,
		StepCosts:    stepCosts,
		DurationMs:   time.Since(workflowStart).Milliseconds(),
	})

	// Mark plan complete if there's an active plan and workflow wasn't stopped
	if s.activePlan != nil && !stopped {
		for _, t := range s.activePlan.Tasks {
			t.Status = protocol.TaskCompleted
		}
		s.emit("event.plan_complete", protocol.EventPlanComplete{Plan: s.activePlan})
		s.activePlan = nil
	}

	return nil
}
