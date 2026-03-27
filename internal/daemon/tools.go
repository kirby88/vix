package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const maxOutput = 20_000

// --- Permission model ---
// _DANGEROUS_TOOLS is currently empty (no tools require confirmation by default)
var dangerousTools = map[string]bool{}

// --- Brain update helpers ---

var sourceExtensions = map[string]bool{
	".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
	".md": true, ".txt": true, ".yml": true, ".yaml": true, ".json": true,
	".toml": true, ".cfg": true, ".ini": true,
}

var toolSkipDirs = map[string]bool{
	"node_modules": true, "__pycache__": true, "build": true,
	"dist": true, "target": true, ".git": true,
}

func shouldTriggerUpdate(filePath string) bool {
	parts := strings.Split(filePath, string(filepath.Separator))
	for _, part := range parts {
		if strings.HasPrefix(part, ".") {
			return false
		}
		if toolSkipDirs[part] {
			return false
		}
	}
	ext := filepath.Ext(filePath)
	return sourceExtensions[ext]
}

// flushBrainUpdate triggers an immediate brain update for the given files.
func flushBrainUpdate(s *Server, files []string) {
	handler := s.GetHandler("brain.update_files")
	if handler == nil {
		LogWarn("brain.update_files handler not registered, skipping update")
		return
	}

	LogInfo("Auto brain update for %d file(s): %v", len(files), files)
	response, err := handler(map[string]any{
		"command": "brain.update_files",
		"params": map[string]any{
			"files":        files,
			"project_path": ".",
		},
	})
	if err != nil {
		LogError("Brain update error: %v", err)
		return
	}
	status, _ := response["status"].(string)
	if status == "ok" {
		data, _ := response["data"].(map[string]any)
		LogInfo("Brain update complete (%v)", data["duration_seconds"])
	} else {
		msg, _ := response["message"].(string)
		LogError("Brain update failed: %s", msg)
	}
}

// resolvePathInCwd resolves a path relative to a given working directory.
// Absolute paths outside cwd are remapped by suffix-matching against cwd.
func resolvePathInCwd(cwd, path string) string {
	if !filepath.IsAbs(path) {
		return filepath.Join(cwd, path)
	}
	// Already under cwd — use as-is
	if strings.HasPrefix(path, cwd+string(filepath.Separator)) || path == cwd {
		return path
	}
	// Absolute path outside cwd — try to remap by finding a matching
	// suffix under cwd (handles LLM using wrong project root).
	// Require ≥2 suffix segments to avoid false positives on system paths.
	sep := string(filepath.Separator)
	parts := strings.Split(path, sep)
	for i := 2; i < len(parts)-1; i++ {
		suffix := strings.Join(parts[i:], sep)
		candidate := filepath.Join(cwd, suffix)
		dir := filepath.Dir(candidate)
		if _, err := os.Stat(dir); err == nil {
			log.Printf("[tools] remapped path outside workdir: %s → %s", path, candidate)
			return candidate
		}
	}
	// Can't remap (system path like /tmp, /etc) — return as-is
	return path
}

// --- Tool implementations ---

func toolOK(output string, isError bool) map[string]any {
	return map[string]any{
		"status": "ok",
		"data": map[string]any{
			"output":   output,
			"is_error": isError,
		},
	}
}

func toolConfirmResponse(tool string, params map[string]any) map[string]any {
	return map[string]any{
		"status": "ok",
		"data": map[string]any{
			"confirm": true,
			"tool":    tool,
			"params":  params,
		},
	}
}

func needsConfirmation(toolName string, params map[string]any) bool {
	if !dangerousTools[toolName] {
		return false
	}
	confirmed, _ := params["confirmed"].(bool)
	return !confirmed
}

// stripLineNumbers removes the line number prefix from numbered content
func stripLineNumbers(numberedContent string) string {
	lines := strings.Split(numberedContent, "\n")
	var cleaned []string
	for _, line := range lines {
		// Simple approach: find the first tab and remove everything before it (line number format is "  123\t")
		if idx := strings.Index(line, "\t"); idx != -1 {
			cleaned = append(cleaned, line[idx+1:])
		} else {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

func readFileImpl(cwd, path string, offset, limit *int, mode string) (string, error) {
	p := resolvePathInCwd(cwd, path)

	if _, err := os.Stat(p); os.IsNotExist(err) {
		return "", fmt.Errorf("file not found: %s", path)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}

	text := string(raw)
	lines := strings.Split(text, "\n")

	start := 0
	if offset != nil && *offset >= 1 {
		start = *offset - 1
	}
	end := len(lines)
	if limit != nil {
		end = start + *limit
	}
	if start > len(lines) {
		start = len(lines)
	}
	if end > len(lines) {
		end = len(lines)
	}

	var numbered []string
	for i, line := range lines[start:end] {
		numbered = append(numbered, fmt.Sprintf("%5d\t%s", i+start+1, line))
	}

	numberedOutput := strings.Join(numbered, "\n")

	// Apply compression if requested
	if mode == "compress" {
		// First strip line numbers
		contentWithoutNumbers := stripLineNumbers(numberedOutput)

		// HTML: collapse all whitespace (tabs, newlines, multiple spaces) into single spaces
		if strings.ToLower(filepath.Ext(path)) == ".html" {
			return strings.Join(strings.Fields(contentWithoutNumbers), " "), nil
		}

		// Then compress with Tree-sitter
		compressed, err := compressWithTreeSitter(contentWithoutNumbers, path)
		if err != nil || compressed == "" {
			// Fallback: just return content without line numbers if Tree-sitter fails
			LogWarn("Tree-sitter compression failed for %s, falling back to line number removal only", path)
			return contentWithoutNumbers, nil
		}
		return compressed, nil
	}

	return numberedOutput, nil
}

// extToLang maps a file extension (e.g. ".go") to a Markdown language hint.
func extToLang(ext string) string {
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".mjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".rs":
		return "rust"
	case ".rb":
		return "ruby"
	case ".sh", ".bash":
		return "bash"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".md":
		return "markdown"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	default:
		return ""
	}
}

// formatWritePreview reads the written file and returns a markdown fenced code
// block containing the first 10 lines, suitable for use as a Detail field in
// EventToolResult. The language hint is derived from the file extension.
// Returns "" if the file cannot be read.
func formatWritePreview(filePath string) string {
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(raw), "\n")
	// Remove trailing empty line produced by Split when file ends with \n
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	n := len(lines)
	truncated := n > 10
	if truncated {
		n = 10
	}
	lang := extToLang(filepath.Ext(filePath))
	var b strings.Builder
	b.WriteString("```" + lang + "\n")
	for _, line := range lines[:n] {
		b.WriteString(line + "\n")
	}
	if truncated {
		b.WriteString("// ...\n")
	}
	b.WriteString("```")
	return b.String()
}

func writeFileImpl(cwd, path, content string) (string, error) {
	p := resolvePathInCwd(cwd, path)
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	encoded := []byte(content)
	if err := os.WriteFile(p, encoded, 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(encoded), path), nil
}

func editFileImpl(cwd, path, oldString, newString string) (string, int, error) {
	p := resolvePathInCwd(cwd, path)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return "", 0, fmt.Errorf("file not found: %s", path)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		return "", 0, err
	}

	text := string(raw)
	count := strings.Count(text, oldString)
	if count == 0 {
		return "", 0, fmt.Errorf("old_string not found in file")
	}
	if count > 1 {
		return "", 0, fmt.Errorf("old_string found %d times (must be unique)", count)
	}

	// Compute line offset before writing so the diff preview shows real file line numbers.
	lineOffset := 0
	if idx := strings.Index(text, oldString); idx >= 0 {
		lineOffset = strings.Count(text[:idx], "\n")
	}

	newText := strings.Replace(text, oldString, newString, 1)
	newRaw := []byte(newText)
	if err := os.WriteFile(p, newRaw, 0o644); err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("Edited %s (replaced 1 occurrence).", path), lineOffset, nil
}

func deleteFileImpl(cwd, path string) (string, error) {
	p := resolvePathInCwd(cwd, path)
	info, err := os.Stat(p)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("file not found: %s", path)
	}
	if info.IsDir() {
		return "", fmt.Errorf("path is a directory, not a file: %s", path)
	}
	fileSize := info.Size()
	if err := os.Remove(p); err != nil {
		return "", err
	}
	return fmt.Sprintf("Deleted %s (%d bytes)", path, fileSize), nil
}

func bashImpl(server *Server, command, cwd string) (string, error) {
	LogInfo("[tool.bash] cwd=%s command=%s", cwd, command)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = cwd

	output, err := cmd.CombinedOutput()
	result := string(output)
	if len(result) > maxOutput {
		result = result[:maxOutput] + fmt.Sprintf("\n... (truncated at %d chars)", maxOutput)
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "Error: command timed out after 120 seconds", nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			result += fmt.Sprintf("\n[exit code: %d]", exitErr.ExitCode())
		}
	}
	if result == "" {
		result = "(no output)"
	}
	return result, nil
}


// FormatEditDiff builds a structured side-by-side diff by running the system
// `diff -U3` command on the old and new strings and parsing its unified output.
// The result uses tagged rows consumed by renderDiffDetail in chat.go:
//
//	"H <text>\n"                  — header line (summary)
//	"C <leftN> <rightN> <text>\n" — context line present on both sides
//	"R <leftN> <text>\n"          — removed line (left side only)
//	"A <rightN> <text>\n"         — added line (right side only)
//
// lineOffset is the 0-based line index of oldStr's first line within the real
// file, computed before the write so the numbers reflect actual file positions.
func FormatEditDiff(oldStr, newStr string, lineOffset int) string {
	// Write old and new content to temp files so diff can compare them.
	oldTmp, err := os.CreateTemp("", "vix-diff-old-*")
	if err != nil {
		return formatEditDiffFallback(oldStr, newStr, lineOffset)
	}
	defer os.Remove(oldTmp.Name())
	if _, err := oldTmp.WriteString(oldStr); err != nil {
		oldTmp.Close()
		return formatEditDiffFallback(oldStr, newStr, lineOffset)
	}
	oldTmp.Close()

	newTmp, err := os.CreateTemp("", "vix-diff-new-*")
	if err != nil {
		return formatEditDiffFallback(oldStr, newStr, lineOffset)
	}
	defer os.Remove(newTmp.Name())
	if _, err := newTmp.WriteString(newStr); err != nil {
		newTmp.Close()
		return formatEditDiffFallback(oldStr, newStr, lineOffset)
	}
	newTmp.Close()

	// Run diff -U3 (unified, 3 context lines). Exit code 1 means differences
	// found (normal); only exit code >1 signals a real error.
	cmd := exec.Command("diff", "-U3", "--label", "old", "--label", "new", oldTmp.Name(), newTmp.Name())
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() > 1 {
			return formatEditDiffFallback(oldStr, newStr, lineOffset)
		}
	}

	return parseUnifiedDiff(string(out), oldStr, newStr, lineOffset)
}

// parseUnifiedDiff converts unified diff output into the H/C/R/A tag format
// expected by renderDiffDetail in chat.go.
// lineOffset is the 0-based line index of oldStr's first line within the real file,
// used to convert snippet-relative line numbers to actual file line numbers.
func parseUnifiedDiff(unified, oldStr, newStr string, lineOffset int) string {
	addedLines := strings.Count(newStr, "\n") + 1
	removedLines := strings.Count(oldStr, "\n") + 1

	var b strings.Builder
	b.WriteString(fmt.Sprintf("H +%d -%d lines\n", addedLines, removedLines))

	lines := strings.Split(unified, "\n")

	// Track current line numbers on each side as we walk hunks.
	var leftN, rightN int

	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		// Unified diff hunk header: @@ -l,s +l,s @@
		if strings.HasPrefix(line, "@@") {
			// Parse left and right starting line numbers.
			// Format: @@ -<l>[,<s>] +<l>[,<s>] @@
			// Note: Go's fmt.Sscanf does not support %*d (C-style suppression),
			// so we parse by scanning tokens for the "-" and "+" fields.
			var ls, rs int
			for _, tok := range strings.Fields(line) {
				if strings.HasPrefix(tok, "-") {
					fmt.Sscanf(strings.TrimPrefix(tok, "-"), "%d", &ls)
				} else if strings.HasPrefix(tok, "+") {
					fmt.Sscanf(strings.TrimPrefix(tok, "+"), "%d", &rs)
				}
			}
			// Apply offset so numbers reflect real file positions.
			leftN = ls + lineOffset
			rightN = rs + lineOffset
			continue
		}
		// Skip the --- / +++ file header lines.
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			continue
		}

		switch line[0] {
		case ' ':
			// Context line — present on both sides.
			b.WriteString(fmt.Sprintf("C %d %d %s\n", leftN, rightN, line[1:]))
			leftN++
			rightN++
		case '-':
			// Removed line.
			b.WriteString(fmt.Sprintf("R %d %s\n", leftN, line[1:]))
			leftN++
		case '+':
			// Added line.
			b.WriteString(fmt.Sprintf("A %d %s\n", rightN, line[1:]))
			rightN++
		}
	}

	return b.String()
}

// formatEditDiffFallback is used when diff is unavailable or temp file creation
// fails. It emits the same H/R/A tag format without context lines.
// lineOffset is the 0-based line index of oldStr's first line within the real file.
func formatEditDiffFallback(oldStr, newStr string, lineOffset int) string {
	oldLines := strings.Split(oldStr, "\n")
	newLines := strings.Split(newStr, "\n")
	var b strings.Builder
	b.WriteString(fmt.Sprintf("H +%d -%d lines\n", len(newLines), len(oldLines)))
	maxPairs := len(oldLines)
	if len(newLines) > maxPairs {
		maxPairs = len(newLines)
	}
	for i := 0; i < maxPairs; i++ {
		if i < len(oldLines) {
			b.WriteString(fmt.Sprintf("R %d %s\n", i+1+lineOffset, oldLines[i]))
		}
		if i < len(newLines) {
			b.WriteString(fmt.Sprintf("A %d %s\n", i+1+lineOffset, newLines[i]))
		}
	}
	return b.String()
}

// --- Async handler wrappers ---

func RegisterToolHandlers(s *Server) {
	// Load tool backend config from home config (project config loaded per-session)
	toolsCfg := loadToolsConfig("")
	grepBackend := newGrepRunner(toolsCfg.Grep.Backend)
	globBackend := newGlobRunner(toolsCfg.Glob.Backend)

	s.RegisterHandler("tool.read_file", func(data map[string]any) (map[string]any, error) {
		params, _ := data["params"].(map[string]any)
		if needsConfirmation("read_file", params) {
			return toolConfirmResponse("read_file", params), nil
		}
		path, _ := params["path"].(string)
		var offset, limit *int
		if v, ok := params["offset"].(float64); ok {
			i := int(v)
			offset = &i
		}
		if v, ok := params["limit"].(float64); ok {
			i := int(v)
			limit = &i
		}
		mode, _ := params["mode"].(string)
		if mode == "" {
			mode = "original"
		}
		cwd, _ := params["cwd"].(string)
		output, err := readFileImpl(cwd, path, offset, limit, mode)
		if err != nil {
			return toolOK(fmt.Sprintf("Error: %v", err), true), nil
		}
		s.LogAccess("read_file", params)
		return toolOK(output, false), nil
	})

	s.RegisterHandler("tool.write_file", func(data map[string]any) (map[string]any, error) {
		params, _ := data["params"].(map[string]any)
		if needsConfirmation("write_file", params) {
			return toolConfirmResponse("write_file", params), nil
		}
		path, _ := params["path"].(string)
		content, _ := params["content"].(string)
		cwd, _ := params["cwd"].(string)
		output, err := writeFileImpl(cwd, path, content)
		if err != nil {
			return toolOK(fmt.Sprintf("Error: %v", err), true), nil
		}
		if shouldTriggerUpdate(path) {
			go flushBrainUpdate(s, []string{path})
		}
		s.LogAccess("write_file", params)
		return toolOK(output, false), nil
	})

	s.RegisterHandler("tool.edit_file", func(data map[string]any) (map[string]any, error) {
		params, _ := data["params"].(map[string]any)
		if needsConfirmation("edit_file", params) {
			return toolConfirmResponse("edit_file", params), nil
		}
		path, _ := params["path"].(string)
		oldString, _ := params["old_string"].(string)
		newString, _ := params["new_string"].(string)
		cwd, _ := params["cwd"].(string)
		output, lineOffset, err := editFileImpl(cwd, path, oldString, newString)
		if err != nil {
			return toolOK(fmt.Sprintf("Error: %v", err), true), nil
		}
		if shouldTriggerUpdate(path) {
			go flushBrainUpdate(s, []string{path})
		}
		s.LogAccess("edit_file", params)
		result := toolOK(output, false)
		result["data"].(map[string]any)["line_offset"] = lineOffset
		return result, nil
	})

	s.RegisterHandler("tool.delete_file", func(data map[string]any) (map[string]any, error) {
		params, _ := data["params"].(map[string]any)
		if needsConfirmation("delete_file", params) {
			return toolConfirmResponse("delete_file", params), nil
		}
		path, _ := params["path"].(string)
		cwd, _ := params["cwd"].(string)
		output, err := deleteFileImpl(cwd, path)
		if err != nil {
			return toolOK(fmt.Sprintf("Error: %v", err), true), nil
		}
		if shouldTriggerUpdate(path) {
			go flushBrainUpdate(s, []string{path})
		}
		s.LogAccess("delete_file", params)
		return toolOK(output, false), nil
	})

	s.RegisterHandler("tool.bash", func(data map[string]any) (map[string]any, error) {
		params, _ := data["params"].(map[string]any)
		if needsConfirmation("bash", params) {
			return toolConfirmResponse("bash", params), nil
		}
		command, _ := params["command"].(string)
		cwd, _ := params["cwd"].(string)
		if cwd == "" {
			cwd = "."
		}
		output, err := bashImpl(s, command, cwd)
		if err != nil {
			return toolOK(fmt.Sprintf("Error: %v", err), true), nil
		}
		return toolOK(output, false), nil
	})

	s.RegisterHandler("tool.grep", func(data map[string]any) (map[string]any, error) {
		params, _ := data["params"].(map[string]any)
		if needsConfirmation("grep", params) {
			return toolConfirmResponse("grep", params), nil
		}
		pattern, _ := params["pattern"].(string)
		path, _ := params["path"].(string)
		include, _ := params["include"].(string)
		cwd, _ := params["cwd"].(string)
		if cwd == "" {
			cwd = "."
		}
		output, err := grepBackend.Run(pattern, path, include, cwd)
		if err != nil {
			return toolOK(fmt.Sprintf("Error: %v", err), true), nil
		}
		return toolOK(output, false), nil
	})

	s.RegisterHandler("tool.glob_files", func(data map[string]any) (map[string]any, error) {
		params, _ := data["params"].(map[string]any)
		if needsConfirmation("glob_files", params) {
			return toolConfirmResponse("glob_files", params), nil
		}
		pattern, _ := params["pattern"].(string)
		path, _ := params["path"].(string)
		cwd, _ := params["cwd"].(string)
		if cwd == "" {
			cwd = "."
		}
		output, err := globBackend.Run(pattern, path, cwd)
		if err != nil {
			return toolOK(fmt.Sprintf("Error: %v", err), true), nil
		}
		return toolOK(output, false), nil
	})

	s.RegisterHandler("tool.tool_orchestrator", func(data map[string]any) (map[string]any, error) {
		params, _ := data["params"].(map[string]any)
		workflow, _ := params["workflow"].(string)
		cwd, _ := params["cwd"].(string)
		if cwd == "" {
			cwd = "."
		}
		output, err := toolOrchestratorImpl(s, workflow, cwd)
		if err != nil {
			return toolOK(fmt.Sprintf("Error: %v", err), true), nil
		}
		return toolOK(output, false), nil
	})

	s.RegisterHandler("tool.web_fetch", func(data map[string]any) (map[string]any, error) {
		params, _ := data["params"].(map[string]any)
		rawURL, _ := params["url"].(string)
		selector, _ := params["selector"].(string)
		output, err := webFetchImpl(rawURL, selector)
		if err != nil {
			return toolOK(fmt.Sprintf("Error: %v", err), true), nil
		}
		return toolOK(output, false), nil
	})

	s.RegisterHandler("tool.web_search", func(data map[string]any) (map[string]any, error) {
		params, _ := data["params"].(map[string]any)
		query, _ := params["query"].(string)
		count := 5
		if v, ok := params["count"].(float64); ok {
			count = int(v)
		}
		output, err := webSearchImpl(query, count)
		if err != nil {
			return toolOK(fmt.Sprintf("Error: %v", err), true), nil
		}
		return toolOK(output, false), nil
	})

	s.RegisterHandler("tool.lsp_query", func(data map[string]any) (map[string]any, error) {
		params, _ := data["params"].(map[string]any)
		operation, _ := params["operation"].(string)
		file, _ := params["file"].(string)
		query, _ := params["query"].(string)
		cwd, _ := params["cwd"].(string)
		if cwd == "" {
			cwd = "."
		}

		line := 0
		if v, ok := params["line"].(float64); ok {
			line = int(v)
		}
		character := 0
		if v, ok := params["character"].(float64); ok {
			character = int(v)
		}
		includeDecl := true
		if v, ok := params["include_declaration"].(bool); ok {
			includeDecl = v
		}

		output, err := lspQueryImpl(operation, file, query, line, character, includeDecl, cwd)
		if err != nil {
			return toolOK(fmt.Sprintf("Error: %v", err), true), nil
		}
		s.LogAccess("lsp_query", params)
		return toolOK(output, false), nil
	})
}

// doGetTopFiles retrieves the top N most accessed files with their content.
func doGetTopFiles(s *Server, input map[string]any) (map[string]any, error) {
	// Handle case where accessDB is nil
	if s.accessDB == nil {
		return map[string]any{
			"status": "ok",
			"data": map[string]any{
				"files": []map[string]any{},
			},
		}, nil
	}

	// Extract count parameter (default to 10)
	count := 10
	if countParam, ok := input["count"].(float64); ok {
		count = int(countParam)
	} else if countParam, ok := input["count"].(int); ok {
		count = countParam
	}

	// Get top accessed files
	filePaths, err := getTopAccessedFiles(s.accessDB, count)
	if err != nil {
		return map[string]any{
			"status":  "error",
			"message": fmt.Sprintf("Failed to get top accessed files: %v", err),
		}, nil
	}

	// Read content for each file
	var files []map[string]any
	for _, path := range filePaths {
		content, err := readFileForTopFiles(path)
		if err != nil {
			// Skip files that can't be read (may have been deleted)
			continue
		}

		files = append(files, map[string]any{
			"path":    path,
			"content": content,
		})
	}

	return map[string]any{
		"status": "ok",
		"data": map[string]any{
			"files": files,
		},
	}, nil
}

// readFileForTopFiles reads a file and truncates it if too large (>500 lines).
func readFileForTopFiles(path string) (string, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(p); os.IsNotExist(err) {
		return "", fmt.Errorf("file not found: %s", path)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}

	text := string(raw)
	lines := strings.Split(text, "\n")

	if len(lines) > 500 {
		truncatedLines := lines[:400]
		truncatedLines = append(truncatedLines, "... (truncated)")
		return strings.Join(truncatedLines, "\n"), nil
	}

	return text, nil
}
