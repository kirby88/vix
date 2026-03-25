package daemon

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	configpkg "github.com/kirby88/vix/internal/config"
	promptloader "github.com/kirby88/vix/internal/daemon/prompt"
)

// SubagentConfig defines how a subagent behaves.
type SubagentConfig struct {
	Name         string
	Description  string   // short description for LLM tool listing
	Model        string   // empty = inherit parent model
	Tools        []string // tool name filter; nil = all tools
	MaxTurns     int      // 0 = default (20)
	SystemPrompt string
}

// SubagentResult holds the output of a completed subagent run.
type SubagentResult struct {
	Output              string
	IsError             bool
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	Elapsed             time.Duration
}

// TurnHooks provides typed callbacks for streaming events between LLM turns.
// All fields are optional — nil callbacks are skipped.
type TurnHooks struct {
	OnStreamDelta func(delta string)
	OnStreamDone  func(inputTokens, outputTokens, cacheCreation, cacheRead, elapsedMs int64)
	OnToolCall    func(toolID, name, summary, reason string)
	OnToolResult  func(toolID, name, output string, isError bool)
}

// BackgroundTask tracks an in-flight or completed background subagent.
type BackgroundTask struct {
	ID     string
	Name   string
	Done   chan struct{}
	Result *SubagentResult
}

// taskCounter generates unique task IDs.
var taskCounter atomic.Int64

func nextTaskID() string {
	return fmt.Sprintf("task_%d", taskCounter.Add(1))
}

// RunSubagent executes a subagent with its own conversation, tools, and LLM instance.
// It blocks until the subagent completes or the context is cancelled.
// executeTool is called directly (in-process, no socket round-trip).
func RunSubagent(
	ctx context.Context,
	config SubagentConfig,
	prompt string,
	apiKey string,
	parentModel string,
	executeTool func(name string, params map[string]any, cwd string) (*ToolResult, error),
	cwd string,
	hooks *TurnHooks,
) (*SubagentResult, error) {
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

	// Apply full template resolution ($(), $(file:), $(call:)) to the system prompt
	projectVix := filepath.Join(cwd, ".vix")
	sysPrompt := promptloader.GetLoader().Resolve(
		config.SystemPrompt,
		map[string]string{"working_directory": cwd},
		promptloader.JoinSearchDirs(projectVix, configpkg.HomeVixDir()),
		nil,
	)
	system := []anthropic.TextBlockParam{{Text: sysPrompt}}

	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
	}

	tracker := newReadTracker()

	var totalInputTokens, totalOutputTokens, totalCacheCreation, totalCacheRead int64
	var totalElapsed time.Duration

	for turn := 0; turn < maxTurns; turn++ {
		if ctx.Err() != nil {
			return &SubagentResult{Output: "Cancelled", IsError: true}, ctx.Err()
		}

		var onDelta func(string)
		if hooks != nil && hooks.OnStreamDelta != nil {
			onDelta = hooks.OnStreamDelta
		}

		msg, elapsed, err := llm.StreamMessage(ctx, system, messages, tools, onDelta)
		if err != nil {
			return &SubagentResult{Output: err.Error(), IsError: true}, err
		}

		totalInputTokens += msg.Usage.InputTokens
		totalOutputTokens += msg.Usage.OutputTokens
		totalCacheCreation += msg.Usage.CacheCreationInputTokens
		totalCacheRead += msg.Usage.CacheReadInputTokens
		totalElapsed += elapsed

		if hooks != nil && hooks.OnStreamDone != nil {
			hooks.OnStreamDone(msg.Usage.InputTokens, msg.Usage.OutputTokens, msg.Usage.CacheCreationInputTokens, msg.Usage.CacheReadInputTokens, elapsed.Milliseconds())
		}

		messages = append(messages, msg.ToParam())

		if msg.StopReason == "end_turn" {
			text := extractTextFromMessage(msg)
			return &SubagentResult{
				Output:              text,
				InputTokens:         totalInputTokens,
				OutputTokens:        totalOutputTokens,
				CacheCreationTokens: totalCacheCreation,
				CacheReadTokens:     totalCacheRead,
				Elapsed:             totalElapsed,
			}, nil
		}

		if msg.StopReason == "tool_use" {
			toolResults := subagentDispatchToolCalls(ctx, msg, executeTool, cwd, tracker, hooks)
			if ctx.Err() != nil {
				return &SubagentResult{Output: "Cancelled", IsError: true}, ctx.Err()
			}
			messages = append(messages, anthropic.NewUserMessage(toolResults...))
			continue
		}

		if msg.StopReason == "max_tokens" {
			// Continue the conversation — the assistant message is already appended above
			messages = append(messages, anthropic.NewUserMessage(
				anthropic.NewTextBlock("Continue from where you left off."),
			))
			continue
		}

		return &SubagentResult{
			Output:              fmt.Sprintf("unexpected stop reason: %s", msg.StopReason),
			IsError:             true,
			InputTokens:         totalInputTokens,
			OutputTokens:        totalOutputTokens,
			CacheCreationTokens: totalCacheCreation,
			CacheReadTokens:     totalCacheRead,
			Elapsed:             totalElapsed,
		}, nil
	}

	lastText := ""
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		for _, block := range last.Content {
			if block.OfText != nil {
				lastText += block.OfText.Text
			}
		}
	}
	if lastText == "" {
		lastText = fmt.Sprintf("Subagent '%s' reached max turns (%d) without completing.", config.Name, maxTurns)
	}
	return &SubagentResult{
		Output:              lastText,
		InputTokens:         totalInputTokens,
		OutputTokens:        totalOutputTokens,
		CacheCreationTokens: totalCacheCreation,
		CacheReadTokens:     totalCacheRead,
		Elapsed:             totalElapsed,
	}, nil
}

// subagentDispatchToolCalls executes tool calls for a subagent or workflow agent
// using the unified dispatcher. No confirmation prompts, no interactive tool
// handlers — tools run directly with confirmed=true.
func subagentDispatchToolCalls(
	ctx context.Context,
	msg *anthropic.Message,
	executeTool func(name string, params map[string]any, cwd string) (*ToolResult, error),
	cwd string,
	tracker *readTracker,
	hooks *TurnHooks,
) []anthropic.ContentBlockParamUnion {
	opts := dispatchOptions{
		cwd:     cwd,
		tracker: tracker,
		executeTool: func(name string, input map[string]any) *ToolResult {
			input["confirmed"] = true
			result, err := executeTool(name, input, cwd)
			if err != nil {
				return &ToolResult{Output: err.Error(), IsError: true}
			}
			return result
		},
		emitToolCall: func(toolID, name, summary, reason string) {
			if hooks != nil && hooks.OnToolCall != nil {
				hooks.OnToolCall(toolID, name, summary, reason)
			}
		},
		emitToolResult: func(toolID, name string, _ map[string]any, output string, isError bool) {
			if hooks != nil && hooks.OnToolResult != nil {
				hooks.OnToolResult(toolID, name, output, isError)
			}
		},
	}
	return dispatchToolCalls(ctx, msg, opts)
}

// LoadCustomAgents parses .vix/agents/*.md files into SubagentConfig entries.
func LoadCustomAgents(dir string) map[string]SubagentConfig {
	agents := make(map[string]SubagentConfig)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return agents
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		config, err := parseAgentFile(path)
		if err != nil {
			log.Printf("[subagent] failed to parse %s: %v", path, err)
			continue
		}

		agents[config.Name] = config
	}

	return agents
}

// parseAgentFile reads a markdown file with YAML-like frontmatter.
func parseAgentFile(path string) (SubagentConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return SubagentConfig{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var config SubagentConfig
	var body strings.Builder

	state := 0

	for scanner.Scan() {
		line := scanner.Text()

		switch state {
		case 0:
			if strings.TrimSpace(line) == "---" {
				state = 1
			}
		case 1:
			if strings.TrimSpace(line) == "---" {
				state = 2
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])

			switch key {
			case "name":
				config.Name = val
			case "description":
				config.Description = val
			case "model":
				config.Model = val
			case "tools":
				for _, t := range strings.Split(val, ",") {
					t = strings.TrimSpace(t)
					if t != "" {
						config.Tools = append(config.Tools, t)
					}
				}
			case "max_turns":
				fmt.Sscanf(val, "%d", &config.MaxTurns)
			}
		case 2:
			body.WriteString(line)
			body.WriteString("\n")
		}
	}

	if config.Name == "" {
		base := filepath.Base(path)
		config.Name = strings.TrimSuffix(base, ".md")
	}

	config.SystemPrompt = strings.TrimSpace(body.String())
	if config.SystemPrompt == "" {
		config.SystemPrompt = fmt.Sprintf("You are the '%s' agent. Complete the given task.", config.Name)
	}

	return config, scanner.Err()
}

// BackgroundTaskRegistry manages background subagent tasks.
type BackgroundTaskRegistry struct {
	tasks sync.Map
}

func (r *BackgroundTaskRegistry) Store(task *BackgroundTask) {
	r.tasks.Store(task.ID, task)
}

func (r *BackgroundTaskRegistry) Load(id string) (*BackgroundTask, bool) {
	v, ok := r.tasks.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*BackgroundTask), true
}

// SpawnBackground launches a subagent in a goroutine and returns a task ID.
func (r *BackgroundTaskRegistry) SpawnBackground(
	ctx context.Context,
	config SubagentConfig,
	prompt string,
	apiKey string,
	parentModel string,
	executeTool func(name string, params map[string]any, cwd string) (*ToolResult, error),
	cwd string,
) string {
	id := nextTaskID()
	task := &BackgroundTask{
		ID:   id,
		Name: config.Name,
		Done: make(chan struct{}),
	}
	r.Store(task)

	go func() {
		defer close(task.Done)

		t0 := time.Now()
		result, err := RunSubagent(ctx, config, prompt, apiKey, parentModel, executeTool, cwd, nil)
		elapsed := time.Since(t0)

		if err != nil && result == nil {
			result = &SubagentResult{Output: err.Error(), IsError: true}
		}

		log.Printf("[subagent] background task %s (%s) completed in %v", id, config.Name, elapsed)
		task.Result = result
	}()

	return id
}

// WaitForTask blocks until the task completes or the context is cancelled.
func (r *BackgroundTaskRegistry) WaitForTask(ctx context.Context, id string, timeout time.Duration) (*SubagentResult, error) {
	task, ok := r.Load(id)
	if !ok {
		return nil, fmt.Errorf("unknown task ID: %s", id)
	}

	select {
	case <-task.Done:
		return task.Result, nil
	default:
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-task.Done:
		return task.Result, nil
	case <-timer.C:
		return &SubagentResult{
			Output: fmt.Sprintf("Task %s (%s) is still running. Try again later.", id, task.Name),
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// extractTextFromMessage pulls the text content from an Anthropic message.
func extractTextFromMessage(msg *anthropic.Message) string {
	var parts []string
	for _, block := range msg.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			parts = append(parts, tb.Text)
		}
	}
	return strings.Join(parts, " ")
}
