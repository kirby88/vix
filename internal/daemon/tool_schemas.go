package daemon

import (
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// ToolSchemas returns the tool definitions for the Anthropic API.
func ToolSchemas() []anthropic.ToolUnionParam {
	return []anthropic.ToolUnionParam{
		{OfTool: &anthropic.ToolParam{
			Name:        "read_file",
			Description: anthropic.String("Read a file from disk. Default mode returns content with line numbers (use for editing). Compress mode returns token-efficient Tree-sitter compressed output without line numbers (use for understanding code structure). Use offset/limit for large files."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"path":   map[string]any{"type": "string", "description": "The absolute path to the file."},
					"offset": map[string]any{"type": "integer", "description": "Start reading from this line (1-based). Optional."},
					"limit":  map[string]any{"type": "integer", "description": "Max number of lines to return. Optional."},
					"mode": map[string]any{
						"type":        "string",
						"enum":        []string{"original", "compress"},
						"description": "Reading mode. 'original' (default): full content with line numbers, use for editing. 'compress': token-efficient, no line numbers, Tree-sitter compressed, use for understanding code structure.",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Explain: (1) why you chose this specific file/pattern, (2) what information you expect to find, and (3) how that information will help you accomplish your current goal.",
					},
				},
				Required: []string{"path", "reason"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "write_file",
			Description: anthropic.String("Write content to a file. Creates parent directories if needed."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"path":    map[string]any{"type": "string", "description": "The absolute path to the file."},
					"content": map[string]any{"type": "string", "description": "The full content to write."},
				},
				Required: []string{"path", "content"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "edit_file",
			Description: anthropic.String("Edit a file by replacing an exact string match. old_string must appear exactly once in the file."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"path":       map[string]any{"type": "string", "description": "The absolute path to the file."},
					"old_string": map[string]any{"type": "string", "description": "The exact text to find (must be unique in the file)."},
					"new_string": map[string]any{"type": "string", "description": "The replacement text."},
				},
				Required: []string{"path", "old_string", "new_string"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "delete_file",
			Description: anthropic.String("Delete a file from disk."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"path": map[string]any{"type": "string", "description": "The absolute path to the file."},
				},
				Required: []string{"path"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "bash",
			Description: anthropic.String("Run a shell command and return stdout+stderr. Times out after 120 seconds. For finding files by pattern, use glob_files instead — it's much faster."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"command": map[string]any{"type": "string", "description": "The shell command to execute."},
					"reason": map[string]any{
						"type":        "string",
						"description": "Explain why this command needs to be run.",
					},
				},
				Required: []string{"command", "reason"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "grep",
			Description: anthropic.String("Search file contents with grep -rn. Returns matching lines."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"pattern": map[string]any{"type": "string", "description": "Regex pattern to search for."},
					"path":    map[string]any{"type": "string", "description": "Directory or file to search in. Defaults to cwd."},
					"include": map[string]any{"type": "string", "description": "File glob filter, e.g. '*.py'. Optional."},
					"reason": map[string]any{
						"type":        "string",
						"description": "Explain: (1) why you chose this specific file/pattern, (2) what information you expect to find, and (3) how that information will help you accomplish your current goal.",
					},
				},
				Required: []string{"pattern", "reason"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "glob_files",
			Description: anthropic.String("Find files matching a glob pattern. Returns up to 200 paths."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"pattern": map[string]any{"type": "string", "description": "Glob pattern, e.g. '**/*.py'."},
					"path":    map[string]any{"type": "string", "description": "Base directory. Defaults to cwd."},
					"reason": map[string]any{
						"type":        "string",
						"description": "Explain: (1) why you chose this specific file/pattern, (2) what information you expect to find, and (3) how that information will help you accomplish your current goal.",
					},
				},
				Required: []string{"pattern", "reason"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "lsp_query",
			Description: anthropic.String("Query LSP servers for code intelligence. Use for precise code navigation: finding definitions, references, type info, compile errors, and interface implementations. Prefer over grep for structural code queries."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"operation": map[string]any{
						"type":        "string",
						"enum":        []string{"go_to_definition", "find_references", "hover", "document_symbols", "workspace_symbols", "find_implementations", "diagnostics"},
						"description": "The LSP operation to perform.",
					},
					"file": map[string]any{
						"type":        "string",
						"description": "The absolute file path. Required for all operations except workspace_symbols.",
					},
					"line": map[string]any{
						"type":        "integer",
						"description": "Line number (1-based). Required for go_to_definition, find_references, hover, find_implementations.",
					},
					"character": map[string]any{
						"type":        "integer",
						"description": "Character offset (1-based). Required for go_to_definition, find_references, hover, find_implementations.",
					},
					"query": map[string]any{
						"type":        "string",
						"description": "Search query for workspace_symbols. Required for workspace_symbols operation.",
					},
					"include_declaration": map[string]any{
						"type":        "boolean",
						"description": "Include the declaration in find_references results. Defaults to true.",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Explain: (1) why you chose this specific file/pattern, (2) what information you expect to find, and (3) how that information will help you accomplish your current goal.",
					},
				},
				Required: []string{"operation", "reason"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "spawn_agent",
			Description: anthropic.String("Spawn a subagent to handle a task autonomously. The subagent gets its own conversation, tools, and LLM. Use background=true to run in parallel and retrieve results later with task_output."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"agent_type": map[string]any{
						"type":        "string",
						"description": "The agent type to spawn. See tool description for available types.",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "The task or question for the subagent. Be specific and self-contained — the subagent has no access to the parent conversation.",
					},
					"background": map[string]any{
						"type":        "boolean",
						"description": "If true, run in the background and return a task ID immediately. Retrieve results later with task_output. Defaults to false.",
					},
				},
				Required: []string{"prompt"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "ask_question_to_user",
			Description: anthropic.String("Ask the user one or more questions and wait for their response. Pass a single-element array for one question, or multiple elements to present a tabbed interface. Each question has its own category, text, and optional suggested options."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"questions": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id":       map[string]any{"type": "string", "description": "Unique identifier for this question."},
								"category": map[string]any{"type": "string", "description": "Short tab label, e.g. 'Language'."},
								"question": map[string]any{"type": "string", "description": "The question text."},
								"options": map[string]any{
									"type":        "array",
									"items":       map[string]any{"type": "string"},
									"description": "Suggested options for the user to choose from.",
								},
								"default_text": map[string]any{
									"type":        "string",
									"description": "Suggestion/placeholder for the free-text input.",
								},
							},
							"required": []string{"id", "category", "question"},
						},
						"description": "Array of questions to present. Use one element for a single question, multiple for a tabbed interface.",
					},
				},
				Required: []string{"questions"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "task_output",
			Description: anthropic.String("Retrieve the result of a background subagent task. If the task is still running, waits up to 30 seconds before returning a 'still running' message."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "The task ID returned by spawn_agent with background=true.",
					},
				},
				Required: []string{"task_id"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "web_fetch",
			Description: anthropic.String("Fetch a web page and return its content as text. HTML is automatically converted to readable text. Supports JSON and plain text responses too."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"url":      map[string]any{"type": "string", "description": "The URL to fetch (must be http or https)."},
					"selector": map[string]any{"type": "string", "description": "Content selector hint: 'main', 'article', or 'body'. If omitted, auto-detects <main> or <article>, falling back to <body>."},
				},
				Required: []string{"url"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "web_search",
			Description: anthropic.String("Search the web using Brave Search API. Returns a numbered list of results with titles, URLs, and descriptions. Requires BRAVE_SEARCH_API_KEY environment variable."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"query": map[string]any{"type": "string", "description": "The search query."},
					"count": map[string]any{"type": "integer", "description": "Number of results to return (1-20, default 5)."},
				},
				Required: []string{"query"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "tool_orchestrator",
			Description: anthropic.String("Execute a Python workflow that chains multiple tool calls (read_file, grep, glob_files, lsp_query, bash, edit_file, write_file, delete_file) in a single round-trip. The workflow script has access to tool functions and must return a dict with results. A CWD variable is available with the project root path. Use relative paths (resolved against CWD) or os.path.join(CWD, ...) for file operations."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"workflow": map[string]any{
						"type":        "string",
						"description": "Python script body (without def/indent). Has access to: read_file(), grep(), glob_files(), lsp_query(), bash(), edit_file(), write_file(), delete_file(). Must return a dict.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Short summary of what the workflow does.",
					},
				},
				Required: []string{"workflow", "description"},
			},
		}},
	}
}

// readOnlyToolNames is the set of tools safe to use during plan exploration.
var readOnlyToolNames = map[string]bool{
	"read_file":  true,
	"grep":       true,
	"glob_files": true,
	"lsp_query":  true,
	"web_fetch":   true,
	"web_search":  true,
}

// ReadOnlyToolSchemas returns only the read-only tool schemas (for plan exploration).
func ReadOnlyToolSchemas() []anthropic.ToolUnionParam {
	all := ToolSchemas()
	var readonly []anthropic.ToolUnionParam
	for _, t := range all {
		if t.OfTool != nil && readOnlyToolNames[t.OfTool.Name] {
			readonly = append(readonly, t)
		}
	}
	return readonly
}

// IsReadOnlyTool returns true if the tool name is read-only (safe for planning).
func IsReadOnlyTool(name string) bool {
	return readOnlyToolNames[name]
}

// SummarizeToolInput returns a one-line human summary of tool input.
func SummarizeToolInput(name string, input map[string]any) string {
	switch name {
	case "read_file":
		p, _ := input["path"].(string)
		mode, _ := input["mode"].(string)
		prefix := ""
		if mode == "compress" {
			prefix = "[compress] "
		}
		offset, hasOffset := input["offset"].(float64)
		limit, hasLimit := input["limit"].(float64)
		if hasOffset && offset > 0 {
			start := int(offset)
			if hasLimit && limit > 0 {
				end := start + int(limit) - 1
				return fmt.Sprintf("%s%s:%d-%d", prefix, p, start, end)
			}
			return fmt.Sprintf("%s%s:%d-", prefix, p, start)
		}
		return prefix + p
	case "write_file":
		p, _ := input["path"].(string)
		c, _ := input["content"].(string)
		return fmt.Sprintf("%s (%d chars)", p, len(c))
	case "edit_file":
		p, _ := input["path"].(string)
		oldStr, _ := input["old_string"].(string)
		newStr, _ := input["new_string"].(string)
		oldLines := strings.Count(oldStr, "\n") + 1
		newLines := strings.Count(newStr, "\n") + 1
		diff := newLines - oldLines
		if diff == 0 {
			return fmt.Sprintf("%s (%d lines changed)", p, oldLines)
		} else if diff > 0 {
			return fmt.Sprintf("%s (%d lines changed, +%d)", p, oldLines, diff)
		}
		return fmt.Sprintf("%s (%d lines changed, %d)", p, oldLines, diff)
	case "delete_file":
		p, _ := input["path"].(string)
		return p
	case "bash":
		cmd, _ := input["command"].(string)
		if len(cmd) > 80 {
			cmd = cmd[:80] + "..."
		}
		return "$ " + cmd
	case "grep":
		p, _ := input["pattern"].(string)
		return p
	case "glob_files":
		p, _ := input["pattern"].(string)
		return p
	case "lsp_query":
		op, _ := input["operation"].(string)
		if f, ok := input["file"].(string); ok && f != "" {
			return fmt.Sprintf("%s %s", op, f)
		}
		if q, ok := input["query"].(string); ok && q != "" {
			return fmt.Sprintf("%s '%s'", op, q)
		}
		return op
	case "spawn_agent":
		agentType, _ := input["agent_type"].(string)
		if agentType == "" {
			agentType = "general"
		}
		bg := ""
		if b, ok := input["background"].(bool); ok && b {
			bg = " (background)"
		}
		prompt, _ := input["prompt"].(string)
		if len(prompt) > 60 {
			prompt = prompt[:60] + "..."
		}
		return fmt.Sprintf("%s%s: %s", agentType, bg, prompt)
	case "ask_question_to_user":
		if qs, ok := input["questions"].([]any); ok {
			if len(qs) == 1 {
				if qMap, ok := qs[0].(map[string]any); ok {
					q, _ := qMap["question"].(string)
					if len(q) > 60 {
						q = q[:60] + "..."
					}
					return q
				}
			}
			return fmt.Sprintf("%d questions", len(qs))
		}
		return "question"
	case "task_output":
		id, _ := input["task_id"].(string)
		return id
	case "web_fetch":
		u, _ := input["url"].(string)
		return u
	case "web_search":
		q, _ := input["query"].(string)
		return q
	case "tool_orchestrator":
		desc, _ := input["description"].(string)
		if len(desc) > 80 {
			desc = desc[:80] + "..."
		}
		return desc
	default:
		return ""
	}
}

// PatchSpawnAgentDescription updates the spawn_agent tool description in the given
// tool list based entirely on the loaded agent definitions.
func PatchSpawnAgentDescription(tools []anthropic.ToolUnionParam, customAgents map[string]SubagentConfig) {
	for i, t := range tools {
		if t.OfTool == nil || t.OfTool.Name != "spawn_agent" {
			continue
		}

		// Build agent list from loaded configs
		var agentEntries []string
		for _, ag := range customAgents {
			entry := "'" + ag.Name + "'"
			if ag.Description != "" {
				entry += " — " + ag.Description
			}
			agentEntries = append(agentEntries, entry)
		}

		// Build agent_type property description
		agentTypeDesc := "The agent type to spawn."
		if len(agentEntries) > 0 {
			agentTypeDesc += " Available: " + strings.Join(agentEntries, ", ") + "."
		}

		if props, ok := t.OfTool.InputSchema.Properties.(map[string]any); ok {
			if atProp, ok := props["agent_type"].(map[string]any); ok {
				atProp["description"] = agentTypeDesc
			}
		}

		// Build top-level tool description
		topDesc := "Spawn a subagent to handle a task autonomously. The subagent gets its own conversation, tools, and LLM."
		if len(agentEntries) > 0 {
			topDesc += " Available agents: " + strings.Join(agentEntries, ", ") + "."
		}
		topDesc += " Use background=true to run in parallel and retrieve results later with task_output."
		tools[i].OfTool.Description = anthropic.String(topDesc)

		break
	}
}

// GetToolSchema returns a single tool schema by name, or nil if not found.
func GetToolSchema(name string) *anthropic.ToolUnionParam {
	for _, t := range ToolSchemas() {
		if t.OfTool != nil && t.OfTool.Name == name {
			return &t
		}
	}
	return nil
}

// ExcludeTools removes tools with the given names from a tool list.
func ExcludeTools(tools []anthropic.ToolUnionParam, names ...string) []anthropic.ToolUnionParam {
	if len(names) == 0 {
		return tools
	}
	exclude := make(map[string]bool, len(names))
	for _, n := range names {
		exclude[n] = true
	}
	var filtered []anthropic.ToolUnionParam
	for _, t := range tools {
		if t.OfTool != nil && exclude[t.OfTool.Name] {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// FilterToolSchemas returns only the tool schemas whose names appear in the allowed list.
// If allowed is nil, returns all tools.
func FilterToolSchemas(allowed []string) []anthropic.ToolUnionParam {
	if allowed == nil {
		return ToolSchemas()
	}
	allowSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowSet[name] = true
	}
	all := ToolSchemas()
	var filtered []anthropic.ToolUnionParam
	for _, t := range all {
		if t.OfTool != nil && allowSet[t.OfTool.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}
