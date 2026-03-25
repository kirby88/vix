package brain

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WriteMarkdown writes a markdown file within the brain directory.
func WriteMarkdown(brainDir, relativePath, content string) error {
	target := filepath.Join(brainDir, relativePath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return err
	}
	LogInfo("Wrote %s (%d bytes)", relativePath, len(content))
	return nil
}

// ReadMarkdown reads a markdown file from the brain directory. Returns "" if missing.
func ReadMarkdown(brainDir, relativePath string) string {
	target := filepath.Join(brainDir, relativePath)
	data, err := os.ReadFile(target)
	if err != nil {
		return ""
	}
	return string(data)
}

// DeepDiveMeta holds the frontmatter metadata from a deep dive file.
type DeepDiveMeta struct {
	Name        string
	Description string
}

// ReadMarkdownFrontmatter reads a markdown file and parses YAML frontmatter.
// Returns the parsed meta and the body (everything after the closing ---).
// If no frontmatter is present, returns empty meta and the full content as body.
func ReadMarkdownFrontmatter(brainDir, relativePath string) (DeepDiveMeta, string) {
	content := ReadMarkdown(brainDir, relativePath)
	if content == "" {
		return DeepDiveMeta{}, ""
	}

	// Check for frontmatter delimiter
	if !strings.HasPrefix(content, "---\n") {
		return DeepDiveMeta{}, content
	}

	// Find closing ---
	rest := content[4:] // skip opening "---\n"
	closeIdx := strings.Index(rest, "\n---\n")
	if closeIdx < 0 {
		// No closing delimiter, treat as no frontmatter
		return DeepDiveMeta{}, content
	}

	fmBlock := rest[:closeIdx]
	body := rest[closeIdx+4:] // skip "\n---\n"

	var meta DeepDiveMeta
	for _, line := range strings.Split(fmBlock, "\n") {
		if key, val, ok := strings.Cut(line, ": "); ok {
			switch strings.TrimSpace(key) {
			case "name":
				meta.Name = strings.TrimSpace(val)
			case "description":
				meta.Description = strings.TrimSpace(val)
			}
		}
	}

	return meta, body
}

// ListMarkdownFiles lists all .md files in a subdirectory of the brain dir.
func ListMarkdownFiles(brainDir, subdir string) []string {
	target := filepath.Join(brainDir, subdir)
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil
	}
	var result []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			result = append(result, filepath.Join(subdir, e.Name()))
		}
	}
	sort.Strings(result)
	return result
}
