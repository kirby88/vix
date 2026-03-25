package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// ── parseAgentFile ──

func TestParseAgentFile(t *testing.T) {
	t.Run("complete frontmatter", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "reviewer.md")
		os.WriteFile(path, []byte(`---
name: code-reviewer
model: claude-opus-4-6
tools: read_file, grep, glob_files
max_turns: 10
---
You are a code reviewer. Review the code carefully.
`), 0644)

		cfg, err := parseAgentFile(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Name != "code-reviewer" {
			t.Errorf("name = %q, want 'code-reviewer'", cfg.Name)
		}
		if cfg.Model != "claude-opus-4-6" {
			t.Errorf("model = %q, want 'claude-opus-4-6'", cfg.Model)
		}
		if len(cfg.Tools) != 3 {
			t.Fatalf("expected 3 tools, got %d: %v", len(cfg.Tools), cfg.Tools)
		}
		if cfg.Tools[0] != "read_file" || cfg.Tools[1] != "grep" || cfg.Tools[2] != "glob_files" {
			t.Errorf("tools = %v", cfg.Tools)
		}
		if cfg.MaxTurns != 10 {
			t.Errorf("max_turns = %d, want 10", cfg.MaxTurns)
		}
		if cfg.SystemPrompt != "You are a code reviewer. Review the code carefully." {
			t.Errorf("system prompt = %q", cfg.SystemPrompt)
		}
	})

	t.Run("minimal frontmatter", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "simple.md")
		os.WriteFile(path, []byte(`---
name: simple
---
Do the thing.
`), 0644)

		cfg, err := parseAgentFile(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Name != "simple" {
			t.Errorf("name = %q, want 'simple'", cfg.Name)
		}
		if cfg.Model != "" {
			t.Errorf("model should be empty, got %q", cfg.Model)
		}
		if cfg.Tools != nil {
			t.Errorf("tools should be nil, got %v", cfg.Tools)
		}
		if cfg.MaxTurns != 0 {
			t.Errorf("max_turns = %d, want 0", cfg.MaxTurns)
		}
	})

	t.Run("no frontmatter", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nofm.md")
		os.WriteFile(path, []byte("Just a prompt without frontmatter.\n"), 0644)

		cfg, err := parseAgentFile(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Name derived from filename
		if cfg.Name != "nofm" {
			t.Errorf("name = %q, want 'nofm'", cfg.Name)
		}
		// With no frontmatter, body is empty (state never transitions to 2)
		// so the default system prompt should be used
		if cfg.SystemPrompt == "" {
			t.Error("expected non-empty system prompt (default fallback)")
		}
	})

	t.Run("empty body gets default prompt", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "empty.md")
		os.WriteFile(path, []byte("---\nname: empty\n---\n"), 0644)

		cfg, err := parseAgentFile(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.SystemPrompt == "" {
			t.Error("expected non-empty system prompt for empty body")
		}
	})

	t.Run("tools with commas and spaces", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "spaced.md")
		os.WriteFile(path, []byte("---\nname: spaced\ntools: read_file , grep , bash\n---\nPrompt\n"), 0644)

		cfg, err := parseAgentFile(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cfg.Tools) != 3 {
			t.Fatalf("expected 3 tools, got %d: %v", len(cfg.Tools), cfg.Tools)
		}
		for _, tool := range cfg.Tools {
			if tool != "read_file" && tool != "grep" && tool != "bash" {
				t.Errorf("unexpected tool %q (not trimmed?)", tool)
			}
		}
	})

	t.Run("name from filename", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "my-agent.md")
		os.WriteFile(path, []byte("---\nmodel: claude-sonnet-4\n---\nDo stuff.\n"), 0644)

		cfg, err := parseAgentFile(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Name != "my-agent" {
			t.Errorf("name = %q, want 'my-agent'", cfg.Name)
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		_, err := parseAgentFile("/nonexistent/agent.md")
		if err == nil {
			t.Error("expected error for nonexistent file")
		}
	})
}

// ── LoadCustomAgents ──

func TestLoadCustomAgents(t *testing.T) {
	t.Run("multiple files", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "a.md"), []byte("---\nname: alpha\n---\nAlpha prompt\n"), 0644)
		os.WriteFile(filepath.Join(dir, "b.md"), []byte("---\nname: beta\n---\nBeta prompt\n"), 0644)

		agents := LoadCustomAgents(dir)
		if len(agents) != 2 {
			t.Fatalf("expected 2 agents, got %d", len(agents))
		}
		if _, ok := agents["alpha"]; !ok {
			t.Error("missing agent 'alpha'")
		}
		if _, ok := agents["beta"]; !ok {
			t.Error("missing agent 'beta'")
		}
	})

	t.Run("skips non-markdown", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "agent.md"), []byte("---\nname: good\n---\nOK\n"), 0644)
		os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not an agent"), 0644)
		os.WriteFile(filepath.Join(dir, "data.json"), []byte("{}"), 0644)

		agents := LoadCustomAgents(dir)
		if len(agents) != 1 {
			t.Errorf("expected 1 agent, got %d", len(agents))
		}
	})

	t.Run("skips directories", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "subdir.md"), 0755) // dir with .md suffix
		os.WriteFile(filepath.Join(dir, "real.md"), []byte("---\nname: real\n---\nYep\n"), 0644)

		agents := LoadCustomAgents(dir)
		if len(agents) != 1 {
			t.Errorf("expected 1 agent, got %d", len(agents))
		}
	})

	t.Run("empty dir", func(t *testing.T) {
		dir := t.TempDir()
		agents := LoadCustomAgents(dir)
		if len(agents) != 0 {
			t.Errorf("expected 0 agents, got %d", len(agents))
		}
	})

	t.Run("nonexistent dir", func(t *testing.T) {
		agents := LoadCustomAgents("/nonexistent/agents/dir")
		if len(agents) != 0 {
			t.Errorf("expected 0 agents, got %d", len(agents))
		}
	})
}
