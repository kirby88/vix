package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewGrepRunnerDefault(t *testing.T) {
	runner := newGrepRunner("")
	if _, ok := runner.(*systemGrepBackend); !ok {
		t.Errorf("expected *systemGrepBackend, got %T", runner)
	}
}

func TestNewGrepRunnerGrep(t *testing.T) {
	runner := newGrepRunner("grep")
	if _, ok := runner.(*systemGrepBackend); !ok {
		t.Errorf("expected *systemGrepBackend, got %T", runner)
	}
}

func TestNewGrepRunnerRg(t *testing.T) {
	runner := newGrepRunner("rg")
	// Result depends on whether rg is installed; just verify it returns a valid runner
	if runner == nil {
		t.Error("expected non-nil runner")
	}
}

func TestNewGlobRunnerDefault(t *testing.T) {
	runner := newGlobRunner("")
	if _, ok := runner.(*builtinGlobBackend); !ok {
		t.Errorf("expected *builtinGlobBackend, got %T", runner)
	}
}

func TestNewGlobRunnerBuiltin(t *testing.T) {
	runner := newGlobRunner("builtin")
	if _, ok := runner.(*builtinGlobBackend); !ok {
		t.Errorf("expected *builtinGlobBackend, got %T", runner)
	}
}

func TestNewGlobRunnerFd(t *testing.T) {
	runner := newGlobRunner("fd")
	// Result depends on whether fd is installed; just verify it returns a valid runner
	if runner == nil {
		t.Error("expected non-nil runner")
	}
}

func TestLoadToolsConfigMissing(t *testing.T) {
	cfg := loadToolsConfig("/nonexistent/path")
	if cfg.Grep.Backend != "" || cfg.Glob.Backend != "" {
		t.Errorf("expected empty defaults, got grep=%q glob=%q", cfg.Grep.Backend, cfg.Glob.Backend)
	}
}

func TestLoadToolsConfigValid(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".vix")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configJSON := `{"tools": {"grep": {"backend": "rg"}, "glob": {"backend": "fd"}}}`
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := loadToolsConfig(dir)
	if cfg.Grep.Backend != "rg" {
		t.Errorf("expected grep backend 'rg', got %q", cfg.Grep.Backend)
	}
	if cfg.Glob.Backend != "fd" {
		t.Errorf("expected glob backend 'fd', got %q", cfg.Glob.Backend)
	}
}

func TestLoadToolsConfigNoToolsSection(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".vix")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configJSON := `{"lsp": {}}`
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := loadToolsConfig(dir)
	if cfg.Grep.Backend != "" || cfg.Glob.Backend != "" {
		t.Errorf("expected empty defaults, got grep=%q glob=%q", cfg.Grep.Backend, cfg.Glob.Backend)
	}
}

func TestSystemGrepBackendArgs(t *testing.T) {
	backend := &systemGrepBackend{}
	// Test with a pattern that won't match anything in a temp dir
	dir := t.TempDir()
	result, err := backend.Run("nonexistent_pattern_xyz", ".", "", dir)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != "(no matches)" {
		t.Errorf("expected '(no matches)', got %q", result)
	}
}

func TestBuiltinGlobBackendNoMatches(t *testing.T) {
	backend := &builtinGlobBackend{}
	dir := t.TempDir()
	result, err := backend.Run("*.nonexistent_ext_xyz", "", dir)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != "(no matches)" {
		t.Errorf("expected '(no matches)', got %q", result)
	}
}

func TestBuiltinGlobBackendWithMatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	backend := &builtinGlobBackend{}
	result, err := backend.Run("*.txt", "", dir)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == "(no matches)" {
		t.Error("expected matches, got '(no matches)'")
	}
}
