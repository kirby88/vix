package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/kirby88/vix/internal/protocol"
)

// capitalizeFirst returns s with its first letter uppercased.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// sectionTitle returns a consistently spaced section heading.
func sectionTitle(text string) string {
	return "\n" + planTitleStyle.Render(text) + "\n"
}

// ChatMessageType identifies the kind of chat message.
type ChatMessageType int

const (
	MsgUser ChatMessageType = iota
	MsgAssistant
	MsgToolCall
	MsgToolResult
	MsgError
	MsgSystem
	MsgPlanProposal
	MsgPlanTaskStart
	MsgPlanTaskDone
	MsgPlanSummary
	MsgWorkflowStart
	MsgWorkflowStepStart
	MsgWorkflowStepDone
	MsgWorkflowComplete
)

// ChatMessage represents a single rendered message in the chat.
type ChatMessage struct {
	Type       ChatMessageType
	Text       string    // raw text
	Rendered   string    // cached lipgloss/glamour output
	Timestamp  time.Time // when the message was created
	ToolName   string
	IsError    bool
	Detail     string // optional rich detail (e.g. edit diff)
	FilePath   string // for grouping file operations
	IsGrouped  bool   // true if this is part of a file group
	GroupIndex int    // index within the group (0 = header, >0 = sub-items)
}

// renderUserMessage creates a rendered user message.
// width is the total terminal width used for wrapping long lines.
func renderUserMessage(text string, width int) ChatMessage {
	now := time.Now()
	bar := userPromptIcon.Render("▎")
	ts := userTimestampStyle.Render(now.Format("3:04 PM"))

	// bar(1) + 2 spaces = 3 columns of prefix per visual line
	const prefix = 3
	contentWidth := width - prefix
	if contentWidth < 20 {
		contentWidth = 20
	}

	lines := strings.Split(text, "\n")
	var sb strings.Builder
	sb.WriteString("\n")
	for i, line := range lines {
		wrapped := wrapLine(line, contentWidth)
		for j, wl := range wrapped {
			if i == len(lines)-1 && j == len(wrapped)-1 {
				sb.WriteString(fmt.Sprintf("%s  %s  %s\n", bar, userPromptStyle.Render(wl), ts))
			} else {
				sb.WriteString(fmt.Sprintf("%s  %s\n", bar, userPromptStyle.Render(wl)))
			}
		}
	}
	rendered := sb.String()
	return ChatMessage{
		Type:      MsgUser,
		Text:      text,
		Timestamp: now,
		Rendered:  rendered,
	}
}

// wrapLine splits a single line into multiple lines that fit within maxWidth columns.
func wrapLine(line string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{line}
	}
	if utf8.RuneCountInString(line) == 0 {
		return []string{""}
	}

	var result []string
	runes := []rune(line)
	start := 0
	col := 0
	lastSpace := -1

	for i, r := range runes {
		w := 1
		if r >= 0x1100 { // rough check for wide chars
			w = 2
		}
		if r == ' ' || r == '\t' {
			lastSpace = i
		}
		if col+w > maxWidth {
			// wrap at last space if available, otherwise hard-wrap
			end := i
			if lastSpace > start {
				end = lastSpace + 1
			}
			result = append(result, string(runes[start:end]))
			start = end
			// skip leading spaces on the new line
			for start < len(runes) && runes[start] == ' ' {
				start++
			}
			col = 0
			lastSpace = -1
			// recount from start to current position
			for k := start; k <= i && k < len(runes); k++ {
				kw := 1
				if runes[k] >= 0x1100 {
					kw = 2
				}
				col += kw
				if runes[k] == ' ' || runes[k] == '\t' {
					lastSpace = k
				}
			}
			continue
		}
		col += w
	}
	if start < len(runes) {
		result = append(result, string(runes[start:]))
	}
	if len(result) == 0 {
		result = []string{""}
	}
	return result
}

// renderToolCall creates a rendered tool call indicator.
func renderToolCall(name, summary, reason string, s Styles) ChatMessage {
	dot := toolCallDot.Render("●")
	text := toolCallStyle.Render(fmt.Sprintf("🔨 %s  %s", name, summary))
	rendered := fmt.Sprintf("  %s %s\n", dot, text)
	if reason != "" {
		rendered += s.ToolCallReasonStyle.Render("    ↳ "+reason) + "\n"
	}

	// Extract file path for grouping
	filePath := extractFilePathFromSummary(name, summary)

	return ChatMessage{
		Type:     MsgToolCall,
		Text:     summary,
		Rendered: rendered,
		ToolName: name,
		FilePath: filePath,
	}
}

// extractFilePathFromSummary extracts the file path from a tool summary.
// Returns empty string if not a file operation or path cannot be determined.
func extractFilePathFromSummary(toolName, summary string) string {
	if toolName != "edit_file" && toolName != "read_file" && toolName != "write_file" {
		return ""
	}

	// Summary format examples:
	// "path/to/file.go (5 lines changed)"
	// "path/to/file.go (100 chars)"
	// "path/to/file.go:10-20"
	// "path/to/file.go"

	// Find first space or colon to isolate the path
	for i, ch := range summary {
		if ch == ' ' || ch == ':' {
			return summary[:i]
		}
	}
	return summary
}

// summarizeToolOutput returns a compact one-line summary for known tool outputs.
// It returns "" for tools whose output is already compact and should be shown as-is.
func summarizeToolOutput(name, output string) string {
	lines := strings.Count(output, "\n")
	if output != "" && !strings.HasSuffix(output, "\n") {
		lines++
	}

	switch name {
	case "read_file":
		return fmt.Sprintf("%d lines read", lines)
	case "grep":
		if lines == 0 || output == "" {
			return "no matches"
		}
		return fmt.Sprintf("%d results", lines)
	case "glob_files":
		if lines == 0 || output == "" {
			return "no matches"
		}
		return fmt.Sprintf("%d files", lines)
	case "lsp_query":
		if lines == 0 || output == "" {
			return "no results"
		}
		return fmt.Sprintf("%d results", lines)
	case "bash":
		if lines == 0 || output == "" {
			return "no output"
		}
		return fmt.Sprintf("%d lines of output", lines)
	default:
		return ""
	}
}

// renderToolResult creates a rendered tool result.
func renderToolResult(name, output string, isError bool, s Styles) ChatMessage {
	return renderToolResultWithContext(name, output, isError, false, "", s)
}

// renderToolResultWithContext creates a rendered tool result, optionally prefixing
// with the tool name when multiple tools are executing concurrently.
// detail is an optional rich string (e.g. edit diff) shown below the summary.
func renderToolResultWithContext(name, output string, isError bool, showToolName bool, detail string, s Styles) ChatMessage {
	// Suppress tool_orchestrator preview entirely
	if name == "tool_orchestrator" {
		return ChatMessage{
			Type:     MsgToolResult,
			ToolName: name,
		}
	}

	prefix := "    ↳ "
	if showToolName {
		prefix = fmt.Sprintf("    ↳ [%s] ", name)
	}

	if isError {
		short := output
		if len(short) > 1000 {
			short = short[:1000] + "..."
		}
		return ChatMessage{
			Type:     MsgToolResult,
			Text:     output,
			Rendered: "  " + errorStyle.Render("ERROR: "+short) + "\n",
			ToolName: name,
			IsError:  true,
		}
	}

	var rendered string
	if summary := summarizeToolOutput(name, output); summary != "" {
		rendered = s.ToolResultStyle.Render(prefix+summary) + "\n"
	} else {
		short := output
		if len(short) > 1000 {
			short = short[:1000] + "..."
		}
		rendered = s.ToolResultStyle.Render(prefix+short) + "\n"
	}

	if detail != "" {
		rendered += renderDiffDetail(detail, s)
	}

	return ChatMessage{
		Type:     MsgToolResult,
		Text:     output,
		Rendered: rendered,
		ToolName: name,
		Detail:   detail,
	}
}

// renderDiffDetail formats a diff detail block for display below a tool result.
func renderDiffDetail(detail string, s Styles) string {
	var sb strings.Builder
	for _, line := range strings.Split(strings.TrimRight(detail, "\n"), "\n") {
		styled := "      " // 6-space indent
		if matched, _ := regexp.MatchString(`^\d+ - `, line); matched {
			styled += diffRemoveStyle.Render(line)
		} else if matched, _ := regexp.MatchString(`^\d+ \+ `, line); matched {
			styled += diffAddStyle.Render(line)
		} else {
			styled += s.ToolResultStyle.Render(line)
		}
		sb.WriteString(styled + "\n")
	}
	return sb.String()
}

// renderAssistantMessage creates a rendered assistant message using Glamour.
func renderAssistantMessage(text string, md *MarkdownRenderer) ChatMessage {
	rendered := md.Render(text)
	return ChatMessage{
		Type:     MsgAssistant,
		Text:     text,
		Rendered: rendered,
	}
}

// renderErrorMessage creates a rendered error message.
func renderErrorMessage(err error) ChatMessage {
	rendered := "  " + errorStyle.Render(fmt.Sprintf("Error: %s", err)) + "\n"
	return ChatMessage{
		Type:     MsgError,
		Text:     err.Error(),
		Rendered: rendered,
		IsError:  true,
	}
}

// renderRetryMessage creates a rendered retry status message.
func renderRetryMessage(retry protocol.EventRetry) ChatMessage {
	text := fmt.Sprintf("%s — retrying in %ds (attempt %d/%d)",
		retry.Reason, retry.WaitSecs, retry.Attempt, retry.MaxRetries)
	rendered := "  " + retryStyle.Render(text) + "\n"
	return ChatMessage{
		Type:     MsgSystem,
		Text:     text,
		Rendered: rendered,
	}
}

// renderSystemMessage creates a rendered system message.
func renderSystemMessage(text string, s Styles) ChatMessage {
	rendered := "  " + s.SystemStyle.Render(text) + "\n"
	return ChatMessage{
		Type:     MsgSystem,
		Text:     text,
		Rendered: rendered,
	}
}

// renderSystemSuccessMessage creates a rendered system success message (in green).
func renderSystemSuccessMessage(text string) ChatMessage {
	rendered := "  " + systemSuccessStyle.Render(text) + "\n"
	return ChatMessage{
		Type:     MsgSystem,
		Text:     text,
		Rendered: rendered,
	}
}

// groupFileOperations groups consecutive file operations on the same file.
// It identifies sequences of tool calls/results for the same file and marks them
// for grouped rendering.
func groupFileOperations(messages []ChatMessage) []ChatMessage {
	if len(messages) == 0 {
		return messages
	}

	result := make([]ChatMessage, 0, len(messages))
	i := 0

	for i < len(messages) {
		msg := messages[i]

		// Only group file operations (edit_file, write_file, read_file)
		if msg.Type != MsgToolCall || msg.FilePath == "" {
			result = append(result, msg)
			i++
			continue
		}

		// Look ahead to find consecutive operations on the same file
		groupPath := msg.FilePath
		groupItems := []ChatMessage{msg}

		// Collect the result for this call (if it exists and is next)
		if i+1 < len(messages) && messages[i+1].Type == MsgToolResult && messages[i+1].ToolName == msg.ToolName {
			groupItems = append(groupItems, messages[i+1])
			i++
		}
		i++

		// Look for more operations on the same file
		for i < len(messages) {
			nextMsg := messages[i]

			// Stop if not a tool call or different file
			if nextMsg.Type != MsgToolCall || nextMsg.FilePath != groupPath {
				break
			}

			groupItems = append(groupItems, nextMsg)

			// Include the result if it follows
			if i+1 < len(messages) && messages[i+1].Type == MsgToolResult && messages[i+1].ToolName == nextMsg.ToolName {
				groupItems = append(groupItems, messages[i+1])
				i++
			}
			i++
		}

		// If we found multiple operations on the same file, create a group
		// Group if we have at least 2 tool calls (with or without results)
		callCount := 0
		for _, item := range groupItems {
			if item.Type == MsgToolCall {
				callCount++
			}
		}

		if callCount >= 2 {
			// Create group header
			header := createFileGroupHeader(groupPath, groupItems)
			result = append(result, header)

			// Add sub-items
			for idx, item := range groupItems {
				subItem := item
				subItem.IsGrouped = true
				subItem.GroupIndex = idx + 1
				result = append(result, subItem)
			}
		} else {
			// Not enough items to group, add them normally
			result = append(result, groupItems...)
		}
	}

	return result
}

// createFileGroupHeader creates a header message for a group of file operations.
func createFileGroupHeader(filePath string, items []ChatMessage) ChatMessage {
	// Get the tool name from the first operation
	toolName := "edit_file"
	for _, item := range items {
		if item.Type == MsgToolCall {
			toolName = item.ToolName
			break
		}
	}

	dot := toolCallDot.Render("●")
	text := toolCallStyle.Render(fmt.Sprintf("🔨 %s  %s", toolName, filePath))
	rendered := fmt.Sprintf("  %s %s\n", dot, text)

	return ChatMessage{
		Type:       MsgToolCall,
		Text:       filePath,
		Rendered:   rendered,
		ToolName:   toolName,
		FilePath:   filePath,
		IsGrouped:  true,
		GroupIndex: 0, // 0 indicates this is the group header
	}
}

// buildRenderedChat concatenates all rendered messages into a single string.
// It applies grouping to file operations before rendering.
func buildRenderedChat(messages []ChatMessage, s Styles) string {
	grouped := groupFileOperations(messages)

	var sb strings.Builder
	for _, msg := range grouped {
		if msg.Rendered == "" {
			continue
		}

		// Skip rendering grouped items' original format, render them as sub-items instead
		if msg.IsGrouped && msg.GroupIndex > 0 {
			rendered := renderGroupedItem(msg, s)
			sb.WriteString(rendered)
		} else {
			sb.WriteString(msg.Rendered)
		}
	}
	return sb.String()
}

// renderGroupedItem renders a tool call or result as a sub-item in a file group.
func renderGroupedItem(msg ChatMessage, s Styles) string {
	switch msg.Type {
	case MsgToolCall:
		// Extract the operation details from the summary (everything after the file path)
		// The summary format is like "path/to/file (details)" or just "path/to/file"
		details := msg.Text
		if msg.FilePath != "" && strings.HasPrefix(details, msg.FilePath) {
			// Remove the file path part, keep only the details
			remainder := strings.TrimPrefix(details, msg.FilePath)
			remainder = strings.TrimSpace(remainder)
			if remainder != "" {
				details = remainder
			}
		}
		return toolCallStyle.Render(fmt.Sprintf("    ↳ %s", details)) + "\n"

	case MsgToolResult:
		// Show result with proper indentation, prefixed with tool name
		if msg.IsError {
			short := msg.Text
			if len(short) > 1000 {
				short = short[:1000] + "..."
			}
			return "      " + errorStyle.Render("ERROR: "+short) + "\n"
		}

		// Mirror the ungrouped rendering: summary line + optional diff detail
		prefix := fmt.Sprintf("    ↳ [%s] ", msg.ToolName)
		var line string
		if summary := summarizeToolOutput(msg.ToolName, msg.Text); summary != "" {
			line = s.ToolResultStyle.Render(prefix+summary) + "\n"
		} else {
			short := msg.Text
			if len(short) > 1000 {
				short = short[:1000] + "..."
			}
			line = s.ToolResultStyle.Render(prefix+short) + "\n"
		}
		if msg.Detail != "" {
			line += renderDiffDetail(msg.Detail, s)
		}
		return line
	default:
		return msg.Rendered
	}
}

// renderQuestionAnswer renders answered Q&A pairs into the chat scrollback.
func renderQuestionAnswer(pairs []QAPair, s Styles) ChatMessage {
	var sb strings.Builder
	for _, p := range pairs {
		sb.WriteString("\n  " + s.QuestionTextStyle.Render(p.Question) + "\n")
		sb.WriteString(confirmStyle.Render("  → User selection: "+p.Answer) + "\n\n")
	}
	return ChatMessage{
		Type:     MsgSystem,
		Rendered: sb.String(),
	}
}

// renderPlanProposal renders a plan for user review.
func renderPlanProposal(plan *protocol.Plan, s Styles) ChatMessage {
	var sb strings.Builder

	header := planHeaderStyle.Render(" " + plan.Name + " ")
	sb.WriteString("\n" + header + "\n")
	sb.WriteString(s.PlanDescStyle.Render("  "+plan.Context) + "\n")

	if plan.Architecture != "" {
		sb.WriteString(sectionTitle("  Architecture"))
		sb.WriteString(s.PlanDescStyle.Render("  "+plan.Architecture) + "\n")
	}

	if len(plan.Files) > 0 {
		sb.WriteString(sectionTitle("  Files"))
		for _, f := range plan.Files {
			sb.WriteString(s.PlanDescStyle.Render("    "+f) + "\n")
		}
	}

	sb.WriteString("\n")
	for _, task := range plan.Tasks {
		bullet := s.PlanBulletStyle.Render(fmt.Sprintf("  [ ] %d.", task.ID))
		title := planTitleStyle.Render(task.Title)
		sb.WriteString(fmt.Sprintf("%s %s\n", bullet, title))
		if task.Description != "" {
			desc := s.PlanDescStyle.Render("      " + task.Description)
			sb.WriteString(desc + "\n")
		}
		for _, sub := range task.Substeps {
			sb.WriteString(s.PlanDescStyle.Render("        - "+sub) + "\n")
		}
	}

	if plan.Risks != "" {
		sb.WriteString(sectionTitle("  Risks"))
		for _, line := range strings.Split(plan.Risks, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				sb.WriteString(s.PlanDescStyle.Render("  "+line) + "\n")
			}
		}
	}

	separator := s.PlanPromptDimStyle.Render("  ──────────────────────────────────────────")
	sb.WriteString("\n" + separator + "\n\n")
	sb.WriteString("  " + planPromptActionStyle.Render("Accept") + s.PlanPromptDimStyle.Render(" — ") + planPromptKeyStyle.Render("press Enter or y") + "\n")
	sb.WriteString("  " + planPromptActionStyle.Render("Modify") + s.PlanPromptDimStyle.Render(" — ") + planPromptKeyStyle.Render("type feedback + Enter") + "\n")
	sb.WriteString("  " + planPromptActionStyle.Render("Reject") + s.PlanPromptDimStyle.Render(" — ") + planPromptKeyStyle.Render("press n or Esc") + "\n")

	return ChatMessage{
		Type:     MsgPlanProposal,
		Text:     plan.Name,
		Rendered: sb.String(),
	}
}

// renderPlanTaskStart renders a task-starting indicator.
func renderPlanTaskStart(taskIdx int, title string, total int) ChatMessage {
	indicator := planRunningStyle.Render(fmt.Sprintf(">>> Task %d/%d:", taskIdx, total))
	rendered := fmt.Sprintf("\n%s %s\n", indicator, planTitleStyle.Render(title))
	return ChatMessage{
		Type:     MsgPlanTaskStart,
		Text:     title,
		Rendered: rendered,
	}
}

// renderPlanTaskDone renders a task-completion indicator.
func renderPlanTaskDone(taskIdx int, title string, success bool, summary string, s Styles) ChatMessage {
	var marker string
	if success {
		marker = planDoneStyle.Render(fmt.Sprintf("[x] Task %d:", taskIdx))
	} else {
		marker = planFailStyle.Render(fmt.Sprintf("[!] Task %d:", taskIdx))
	}

	short := summary
	if len(short) > 120 {
		short = short[:120] + "..."
	}

	rendered := fmt.Sprintf("%s %s\n", marker, title)
	if short != "" {
		rendered += s.PlanDescStyle.Render("    "+short) + "\n"
	}

	return ChatMessage{
		Type:     MsgPlanTaskDone,
		Text:     title,
		Rendered: rendered,
	}
}

// renderPlanSummary renders the final plan summary.
func renderPlanSummary(plan *protocol.Plan) ChatMessage {
	succeeded := 0
	for _, t := range plan.Tasks {
		if t.Status == protocol.TaskCompleted {
			succeeded++
		}
	}

	header := planDoneHeaderStyle.Render(" DONE ")
	summary := fmt.Sprintf("%s %d/%d tasks succeeded\n", header, succeeded, len(plan.Tasks))

	return ChatMessage{
		Type:     MsgPlanSummary,
		Text:     summary,
		Rendered: "\n" + summary,
	}
}

// renderWorkflowStart renders a workflow-starting indicator.
func renderWorkflowStart(name string, totalSteps int, s Styles) ChatMessage {
	header := planHeaderStyle.Render(fmt.Sprintf(" Workflow: %s ", name))
	rendered := fmt.Sprintf("\n%s %s\n", header, s.PlanDescStyle.Render(fmt.Sprintf("(%d steps)", totalSteps)))
	return ChatMessage{
		Type:     MsgWorkflowStart,
		Text:     name,
		Rendered: rendered,
	}
}

// renderWorkflowStepStart renders a workflow step starting indicator.
func renderWorkflowStepStart(stepID string, stepIdx, total int, explanation string) ChatMessage {
	var indicator string
	if total > 0 {
		indicator = planRunningStyle.Render(fmt.Sprintf(">>> Step %d/%d:", stepIdx, total))
	} else {
		indicator = planRunningStyle.Render(fmt.Sprintf(">>> Step %d:", stepIdx))
	}
	title := capitalizeFirst(explanation)
	rendered := fmt.Sprintf("\n%s %s\n", indicator, planRunningStyle.Render(title))
	return ChatMessage{
		Type:     MsgWorkflowStepStart,
		Text:     stepID,
		Rendered: rendered,
	}
}

// renderWorkflowStepDone renders a workflow step completion indicator with tool summaries.
func renderWorkflowStepDone(stepID string, stepIdx, total int, success bool, display string, toolStats []protocol.ToolStat, md *MarkdownRenderer, s Styles) ChatMessage {
	var sb strings.Builder

	// Summary section
	if display != "" {
		sb.WriteString(sectionTitle("  Summary"))
	}
	if display != "" {
		sb.WriteString(md.Render(display))
	}
	if len(toolStats) > 0 {
		sb.WriteString(s.PlanDescStyle.Render("  Tool usage") + "\n\n")

		// Compute column widths
		colTool, colCalls, colSummary := len("Tool"), len("Calls"), len("Summary")
		for _, ts := range toolStats {
			if len(ts.Name) > colTool {
				colTool = len(ts.Name)
			}
			c := fmt.Sprintf("%d", ts.Calls)
			if len(c) > colCalls {
				colCalls = len(c)
			}
			if len(ts.Summary) > colSummary {
				colSummary = len(ts.Summary)
			}
		}

		hLine := fmt.Sprintf("  ├─%s─┼─%s─┼─%s─┤",
			strings.Repeat("─", colTool), strings.Repeat("─", colCalls), strings.Repeat("─", colSummary))
		top := fmt.Sprintf("  ┌─%s─┬─%s─┬─%s─┐",
			strings.Repeat("─", colTool), strings.Repeat("─", colCalls), strings.Repeat("─", colSummary))
		bottom := fmt.Sprintf("  └─%s─┴─%s─┴─%s─┘",
			strings.Repeat("─", colTool), strings.Repeat("─", colCalls), strings.Repeat("─", colSummary))
		row := func(a, b, c string) string {
			return fmt.Sprintf("  │ %-*s │ %-*s │ %-*s │", colTool, a, colCalls, b, colSummary, c)
		}

		sb.WriteString(s.PlanDescStyle.Render(top) + "\n")
		sb.WriteString(s.PlanDescStyle.Render(row("Tool", "Calls", "Summary")) + "\n")
		sb.WriteString(s.PlanDescStyle.Render(hLine) + "\n")
		for i, ts := range toolStats {
			sb.WriteString(s.PlanDescStyle.Render(row(ts.Name, fmt.Sprintf("%d", ts.Calls), ts.Summary)) + "\n")
			if i < len(toolStats)-1 {
				sb.WriteString(s.PlanDescStyle.Render(hLine) + "\n")
			}
		}
		sb.WriteString(s.PlanDescStyle.Render(bottom) + "\n")
	}

	// Completion marker
	var marker string
	if success {
		if total > 0 {
			marker = planDoneStyle.Render(fmt.Sprintf("[x] Step %d/%d:", stepIdx, total))
		} else {
			marker = planDoneStyle.Render(fmt.Sprintf("[x] Step %d:", stepIdx))
		}
		sb.WriteString(fmt.Sprintf("%s %s\n", marker, planDoneStyle.Render(capitalizeFirst(stepID))))
	} else {
		if total > 0 {
			marker = planFailStyle.Render(fmt.Sprintf("[!] Step %d/%d:", stepIdx, total))
		} else {
			marker = planFailStyle.Render(fmt.Sprintf("[!] Step %d:", stepIdx))
		}
		sb.WriteString(fmt.Sprintf("%s %s\n", marker, planFailStyle.Render(capitalizeFirst(stepID))))
	}

	return ChatMessage{
		Type:     MsgWorkflowStepDone,
		Text:     stepID,
		Rendered: sb.String(),
	}
}

// renderWorkflowComplete renders a workflow completion summary with a cost table.
func renderWorkflowComplete(name string, success bool, summary string, stepCosts []protocol.StepCost, durationMs int64, s Styles) ChatMessage {
	var sb strings.Builder

	// Workflow summary header
	sb.WriteString("\n" + planRunningStyle.Render(">>> Workflow summary") + "\n\n")

	// Config-driven summary (replaces hardcoded "Steps executed:" list)
	if summary != "" {
		sb.WriteString(s.PlanDescStyle.Render("    "+summary) + "\n")
	}

	// Cost summary table
	if len(stepCosts) > 0 {
		sb.WriteString("\n" + s.PlanDescStyle.Render("    Cost & Time breakdown") + "\n")

		var totalInput, totalOutput, totalCacheWrite, totalCacheRead, totalDurationMs int64
		var totalCost float64
		for _, sc := range stepCosts {
			totalInput += sc.InputTokens
			totalOutput += sc.OutputTokens
			totalCacheWrite += sc.CacheCreationTokens
			totalCacheRead += sc.CacheReadTokens
			totalCost += sc.Cost
			totalDurationMs += sc.DurationMs
		}

		// Compute column widths from headers and data
		colStep := len("Steps")
		colInput := len("Input Tokens")
		colCacheW := len("Cache Writes")
		colCacheR := len("Cache Hits")
		colOutput := len("Output Tokens")
		colCost := len("Cost")
		colPct := len("% (Cost)")
		colTime := len("Time")
		colTimePct := len("% (Time)")

		type rowData struct {
			step, input, cacheW, cacheR, output, cost, pct, time, timePct string
		}
		rows := make([]rowData, 0, len(stepCosts))
		for i, sc := range stepCosts {
			pct := "0%"
			if totalCost > 0 {
				pct = fmt.Sprintf("%.0f%%", sc.Cost/totalCost*100)
			}
			timePct := "0%"
			if totalDurationMs > 0 {
				timePct = fmt.Sprintf("%.0f%%", float64(sc.DurationMs)/float64(totalDurationMs)*100)
			}
			r := rowData{
				step:    fmt.Sprintf("%d. %s", i+1, capitalizeFirst(sc.StepID)),
				input:   formatTokenCount(sc.InputTokens),
				cacheW:  formatTokenCount(sc.CacheCreationTokens),
				cacheR:  formatTokenCount(sc.CacheReadTokens),
				output:  formatTokenCount(sc.OutputTokens),
				cost:    fmt.Sprintf("$%.2f", sc.Cost),
				pct:     pct,
				time:    fmt.Sprintf("%.1fs", float64(sc.DurationMs)/1000.0),
				timePct: timePct,
			}
			rows = append(rows, r)
			if len(r.step) > colStep {
				colStep = len(r.step)
			}
			if len(r.input) > colInput {
				colInput = len(r.input)
			}
			if len(r.cacheW) > colCacheW {
				colCacheW = len(r.cacheW)
			}
			if len(r.cacheR) > colCacheR {
				colCacheR = len(r.cacheR)
			}
			if len(r.output) > colOutput {
				colOutput = len(r.output)
			}
			if len(r.cost) > colCost {
				colCost = len(r.cost)
			}
			if len(r.pct) > colPct {
				colPct = len(r.pct)
			}
			if len(r.time) > colTime {
				colTime = len(r.time)
			}
			if len(r.timePct) > colTimePct {
				colTimePct = len(r.timePct)
			}
		}
		totalRow := rowData{
			step:    "Total",
			input:   formatTokenCount(totalInput),
			cacheW:  formatTokenCount(totalCacheWrite),
			cacheR:  formatTokenCount(totalCacheRead),
			output:  formatTokenCount(totalOutput),
			cost:    fmt.Sprintf("$%.2f", totalCost),
			pct:     "100%",
			time:    fmt.Sprintf("%.1fs", float64(durationMs)/1000.0),
			timePct: "100%",
		}
		if len(totalRow.step) > colStep {
			colStep = len(totalRow.step)
		}
		if len(totalRow.input) > colInput {
			colInput = len(totalRow.input)
		}
		if len(totalRow.cacheW) > colCacheW {
			colCacheW = len(totalRow.cacheW)
		}
		if len(totalRow.cacheR) > colCacheR {
			colCacheR = len(totalRow.cacheR)
		}
		if len(totalRow.output) > colOutput {
			colOutput = len(totalRow.output)
		}
		if len(totalRow.cost) > colCost {
			colCost = len(totalRow.cost)
		}
		if len(totalRow.pct) > colPct {
			colPct = len(totalRow.pct)
		}
		if len(totalRow.time) > colTime {
			colTime = len(totalRow.time)
		}
		if len(totalRow.timePct) > colTimePct {
			colTimePct = len(totalRow.timePct)
		}

		cols := []int{colStep, colInput, colCacheW, colCacheR, colOutput, colCost, colPct, colTime, colTimePct}
		borderLine := func(left, mid, right string) string {
			parts := make([]string, len(cols))
			for i, w := range cols {
				parts[i] = strings.Repeat("─", w+2)
			}
			return "    " + left + strings.Join(parts, mid) + right
		}
		row := func(r rowData) string {
			return fmt.Sprintf("    │ %-*s │ %-*s │ %-*s │ %-*s │ %-*s │ %*s │ %*s │ %*s │ %*s │",
				colStep, r.step, colInput, r.input, colCacheW, r.cacheW,
				colCacheR, r.cacheR, colOutput, r.output, colCost, r.cost, colPct, r.pct,
				colTime, r.time, colTimePct, r.timePct)
		}

		sb.WriteString(s.PlanDescStyle.Render(borderLine("┌", "┬", "┐")) + "\n")
		sb.WriteString(s.PlanDescStyle.Render(row(rowData{step: "Steps", input: "Input Tokens", cacheW: "Cache Writes", cacheR: "Cache Hits", output: "Output Tokens", cost: "Cost", pct: "% (Cost)", time: "Time", timePct: "% (Time)"})) + "\n")
		sb.WriteString(s.PlanDescStyle.Render(borderLine("├", "┼", "┤")) + "\n")
		for _, r := range rows {
			sb.WriteString(s.PlanDescStyle.Render(row(r)) + "\n")
		}
		sb.WriteString(s.PlanDescStyle.Render(borderLine("├", "┼", "┤")) + "\n")
		sb.WriteString(s.PlanDescStyle.Bold(true).Render(row(totalRow)) + "\n")
		sb.WriteString(s.PlanDescStyle.Render(borderLine("└", "┴", "┘")) + "\n")
	}

	// DONE / FAILED header at the end
	if success {
		header := planDoneHeaderStyle.Render(" DONE ")
		sb.WriteString(fmt.Sprintf("\n%s Workflow '%s' completed\n", header, name))
	} else {
		header := planFailStyle.Render(" FAILED ")
		sb.WriteString(fmt.Sprintf("\n%s Workflow '%s' failed\n", header, name))
	}

	return ChatMessage{
		Type:     MsgWorkflowComplete,
		Text:     name,
		Rendered: sb.String(),
	}
}

// formatModelName converts a model ID like "claude-sonnet-4-6" to "Claude Sonnet 4.6".
func formatModelName(model string) string {
	model = strings.TrimPrefix(model, "claude-")
	parts := strings.Split(model, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = string(unicode.ToUpper(rune(p[0]))) + p[1:]
		}
	}
	// Join version numbers with dots: "Sonnet 4 6" → "Sonnet 4.6"
	result := strings.Join(parts, " ")
	// Find last space-separated number pair and join with dot
	words := strings.Fields(result)
	if len(words) >= 2 {
		last := words[len(words)-1]
		prev := words[len(words)-2]
		if len(last) <= 2 && last[0] >= '0' && last[0] <= '9' && len(prev) <= 2 && prev[0] >= '0' && prev[0] <= '9' {
			words = append(words[:len(words)-2], prev+"."+last)
			result = strings.Join(words, " ")
		}
	}
	return "Claude " + result
}

// renderTurnInfo renders a turn-end info line with model, elapsed time, cost, and a dim separator.
func renderTurnInfo(model string, elapsed time.Duration, cost float64, width int, s Styles) ChatMessage {
	dimStyle := lipgloss.NewStyle().Foreground(s.ColorDimGray)

	secs := int(elapsed.Seconds())
	info := fmt.Sprintf("◇ %s · %ds · $%.2f ", formatModelName(model), secs, cost)
	infoRendered := dimStyle.Render(info)

	// Fill remaining width with ─
	// Content is inside a bordered viewport: width - 2 (border) - 2 (padding) - 2 (indent)
	contentWidth := width - 6
	dashCount := contentWidth - lipgloss.Width(info)
	if dashCount < 1 {
		dashCount = 1
	}
	dashes := dimStyle.Render(strings.Repeat("─", dashCount))

	rendered := "  " + infoRendered + dashes + "\n\n"
	return ChatMessage{
		Type:     MsgSystem,
		Text:     info,
		Rendered: rendered,
	}
}
