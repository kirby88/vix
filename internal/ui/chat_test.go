package ui

import (
	"strings"
	"testing"
)

func TestExtractFilePathFromSummary(t *testing.T) {
	tests := []struct {
		toolName string
		summary  string
		expected string
	}{
		{"edit_file", "internal/daemon/prompt/README.md (7 lines changed, +2)", "internal/daemon/prompt/README.md"},
		{"edit_file", "path/to/file.go (5 lines changed)", "path/to/file.go"},
		{"write_file", "config.yaml (100 chars)", "config.yaml"},
		{"read_file", "main.go:10-20", "main.go"},
		{"read_file", "test.txt", "test.txt"},
		{"bash", "ls -la", ""}, // Not a file operation
		{"grep", "pattern", ""}, // Not a file operation
	}

	for _, tt := range tests {
		result := extractFilePathFromSummary(tt.toolName, tt.summary)
		if result != tt.expected {
			t.Errorf("extractFilePathFromSummary(%q, %q) = %q, want %q",
				tt.toolName, tt.summary, result, tt.expected)
		}
	}
}

func TestGroupFileOperations(t *testing.T) {
	// Test case: multiple edit_file operations on the same file
	messages := []ChatMessage{
		{Type: MsgToolCall, ToolName: "edit_file", Text: "internal/ui/chat.go (7 lines changed, +2)", FilePath: "internal/ui/chat.go"},
		{Type: MsgToolResult, ToolName: "edit_file", Text: "Edited internal/ui/chat.go (replaced 1 occurrence)."},
		{Type: MsgToolCall, ToolName: "edit_file", Text: "internal/ui/chat.go (11 lines changed)", FilePath: "internal/ui/chat.go"},
		{Type: MsgToolResult, ToolName: "edit_file", Text: "Edited internal/ui/chat.go (replaced 1 occurrence)."},
		{Type: MsgToolCall, ToolName: "edit_file", Text: "internal/ui/chat.go (8 lines changed)", FilePath: "internal/ui/chat.go"},
		{Type: MsgToolResult, ToolName: "edit_file", Text: "Edited internal/ui/chat.go (replaced 1 occurrence)."},
	}

	grouped := groupFileOperations(messages)

	// Should have: 1 header + 6 grouped items = 7 total
	if len(grouped) != 7 {
		t.Errorf("Expected 7 messages after grouping, got %d", len(grouped))
	}

	// First message should be the group header
	if !grouped[0].IsGrouped || grouped[0].GroupIndex != 0 {
		t.Errorf("First message should be a group header (IsGrouped=true, GroupIndex=0)")
	}

	// Check that the header has the file path
	if grouped[0].FilePath != "internal/ui/chat.go" {
		t.Errorf("Group header should have FilePath=internal/ui/chat.go, got %q", grouped[0].FilePath)
	}

	// All subsequent messages should be marked as grouped
	for i := 1; i < len(grouped); i++ {
		if !grouped[i].IsGrouped {
			t.Errorf("Message %d should be marked as grouped", i)
		}
		if grouped[i].GroupIndex != i {
			t.Errorf("Message %d should have GroupIndex=%d, got %d", i, i, grouped[i].GroupIndex)
		}
	}
}

func TestGroupFileOperations_DifferentFiles(t *testing.T) {
	// Test case: operations on different files should not be grouped
	messages := []ChatMessage{
		{Type: MsgToolCall, ToolName: "edit_file", Text: "file1.go (5 lines changed)", FilePath: "file1.go"},
		{Type: MsgToolResult, ToolName: "edit_file", Text: "Edited file1.go"},
		{Type: MsgToolCall, ToolName: "edit_file", Text: "file2.go (3 lines changed)", FilePath: "file2.go"},
		{Type: MsgToolResult, ToolName: "edit_file", Text: "Edited file2.go"},
	}

	grouped := groupFileOperations(messages)

	// Should remain ungrouped (4 messages)
	if len(grouped) != 4 {
		t.Errorf("Expected 4 messages (no grouping), got %d", len(grouped))
	}

	// None should be marked as grouped
	for i, msg := range grouped {
		if msg.IsGrouped {
			t.Errorf("Message %d should not be grouped", i)
		}
	}
}

func TestGroupFileOperations_NonFileOperations(t *testing.T) {
	// Test case: non-file operations should not be grouped
	messages := []ChatMessage{
		{Type: MsgToolCall, ToolName: "grep", Text: "pattern", FilePath: ""},
		{Type: MsgToolResult, ToolName: "grep", Text: "3 matches"},
		{Type: MsgToolCall, ToolName: "bash", Text: "$ ls", FilePath: ""},
		{Type: MsgToolResult, ToolName: "bash", Text: "output"},
	}

	grouped := groupFileOperations(messages)

	// Should remain unchanged (4 messages)
	if len(grouped) != 4 {
		t.Errorf("Expected 4 messages (no grouping), got %d", len(grouped))
	}
}

func TestGroupFileOperations_InterleavedFiles(t *testing.T) {
	// Test case: operations on file A, then file B, then file A again
	// Should NOT group (operations are not consecutive)
	messages := []ChatMessage{
		{Type: MsgToolCall, ToolName: "edit_file", Text: "fileA.go (5 lines changed)", FilePath: "fileA.go"},
		{Type: MsgToolResult, ToolName: "edit_file", Text: "Edited fileA.go"},
		{Type: MsgToolCall, ToolName: "edit_file", Text: "fileB.go (3 lines changed)", FilePath: "fileB.go"},
		{Type: MsgToolResult, ToolName: "edit_file", Text: "Edited fileB.go"},
		{Type: MsgToolCall, ToolName: "edit_file", Text: "fileA.go (2 lines changed)", FilePath: "fileA.go"},
		{Type: MsgToolResult, ToolName: "edit_file", Text: "Edited fileA.go"},
	}

	grouped := groupFileOperations(messages)

	// Should remain ungrouped (6 messages)
	if len(grouped) != 6 {
		t.Errorf("Expected 6 messages (no grouping for interleaved files), got %d", len(grouped))
	}

	// None should be marked as grouped
	for i, msg := range grouped {
		if msg.IsGrouped {
			t.Errorf("Message %d should not be grouped (interleaved operations)", i)
		}
	}
}

func TestGroupFileOperations_MixedWithOtherTools(t *testing.T) {
	// Test case: file operations mixed with other tool calls
	// File operations should still be grouped if consecutive
	messages := []ChatMessage{
		{Type: MsgToolCall, ToolName: "grep", Text: "pattern", FilePath: ""},
		{Type: MsgToolResult, ToolName: "grep", Text: "3 matches"},
		{Type: MsgToolCall, ToolName: "edit_file", Text: "test.go (5 lines changed)", FilePath: "test.go"},
		{Type: MsgToolResult, ToolName: "edit_file", Text: "Edited test.go"},
		{Type: MsgToolCall, ToolName: "edit_file", Text: "test.go (3 lines changed)", FilePath: "test.go"},
		{Type: MsgToolResult, ToolName: "edit_file", Text: "Edited test.go"},
		{Type: MsgToolCall, ToolName: "bash", Text: "$ ls", FilePath: ""},
		{Type: MsgToolResult, ToolName: "bash", Text: "output"},
	}

	grouped := groupFileOperations(messages)

	// Should have: 2 (grep) + 1 header + 4 grouped items (edit_file) + 2 (bash) = 9 total
	if len(grouped) != 9 {
		t.Errorf("Expected 9 messages after grouping, got %d", len(grouped))
	}

	// First 2 messages (grep) should not be grouped
	if grouped[0].IsGrouped || grouped[1].IsGrouped {
		t.Errorf("Grep operations should not be grouped")
	}

	// Message at index 2 should be the group header
	if !grouped[2].IsGrouped || grouped[2].GroupIndex != 0 {
		t.Errorf("Message at index 2 should be group header")
	}

	// Last 2 messages (bash) should not be grouped
	if grouped[7].IsGrouped || grouped[8].IsGrouped {
		t.Errorf("Bash operations should not be grouped")
	}
}

func TestRenderGroupedItem(t *testing.T) {
	// Test rendering of a grouped tool call
	callMsg := ChatMessage{
		Type:       MsgToolCall,
		ToolName:   "edit_file",
		Text:       "internal/ui/chat.go (7 lines changed, +2)",
		FilePath:   "internal/ui/chat.go",
		IsGrouped:  true,
		GroupIndex: 1,
	}

	rendered := renderGroupedItem(callMsg, NewStyles(true), 120)
	if !strings.Contains(rendered, "↳") {
		t.Errorf("Grouped item should contain arrow symbol")
	}
	if !strings.Contains(rendered, "(7 lines changed, +2)") {
		t.Errorf("Grouped item should contain operation details")
	}

	// Test rendering of a grouped result
	resultMsg := ChatMessage{
		Type:       MsgToolResult,
		ToolName:   "edit_file",
		Text:       "Edited internal/ui/chat.go (replaced 1 occurrence).",
		IsGrouped:  true,
		GroupIndex: 2,
	}

	rendered = renderGroupedItem(resultMsg, NewStyles(true), 120)
	if !strings.Contains(rendered, "[edit_file]") {
		t.Errorf("Grouped result should contain tool name")
	}
	if !strings.Contains(rendered, "Edited") {
		t.Errorf("Grouped result should contain the output message")
	}
}

func TestRenderDiffDetailSideBySide(t *testing.T) {
	// Build a detail string in the structured tag format produced by FormatEditDiff.
	// One removed line and one added line should appear on the same output line.
	detail := "H Added 1 line, removed 1 line\n" +
		"C 1 1 package main\n" +
		"R 2 oldFunction()\n" +
		"A 2 newFunction()\n" +
		"C 3 3 \n"

	rendered := renderDiffDetail(detail, NewStyles(true), 120)

	// Strip ANSI escape sequences before text matching: intra-line diff splits
	// words across multiple styled spans, so raw Contains on ANSI output fails.
	strip := func(s string) string { return ansiRe.ReplaceAllString(s, "") }

	lines := strings.Split(rendered, "\n")

	// Find the line containing the removed text (ANSI-stripped).
	removedLineIdx := -1
	for i, l := range lines {
		if strings.Contains(strip(l), "oldFunction") {
			removedLineIdx = i
			break
		}
	}
	if removedLineIdx == -1 {
		t.Fatal("rendered diff does not contain removed text 'oldFunction'")
	}

	// The added text must appear on the same line as the removed text (side-by-side).
	if !strings.Contains(strip(lines[removedLineIdx]), "newFunction") {
		t.Errorf("expected removed and added text on the same line for side-by-side rendering;\ngot: %q", lines[removedLineIdx])
	}

	// Header should be rendered as a plain line.
	foundHeader := false
	for _, l := range lines {
		if strings.Contains(strip(l), "Added 1 line, removed 1 line") {
			foundHeader = true
			break
		}
	}
	if !foundHeader {
		t.Error("rendered diff does not contain the header line")
	}

	// Intra-line diff: the changed part ("old" vs "new") should be bold/highlighted,
	// while the shared suffix "Function()" should be present in the plain (non-bold) spans.
	// Verify by checking that the raw rendered line contains both changed and equal spans.
	rawLine := lines[removedLineIdx]
	if !strings.Contains(rawLine, "old") {
		t.Error("expected 'old' to appear in the rendered diff line")
	}
	if !strings.Contains(rawLine, "new") {
		t.Error("expected 'new' to appear in the rendered diff line")
	}
	if !strings.Contains(strip(rawLine), "Function()") {
		t.Error("expected shared 'Function()' suffix to appear in the rendered diff line")
	}
}
