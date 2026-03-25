package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIntegration_ResolveSubagentPrompt verifies that Resolve() correctly
// processes $(), $(file:), and $(call:) placeholders in subagent system prompts.
func TestIntegration_ResolveSubagentPrompt(t *testing.T) {
	// Set up a temp brainDir with a file to include via $(file:)
	brainDir := t.TempDir()
	includePath := filepath.Join(brainDir, "coding-style.md")
	if err := os.WriteFile(includePath, []byte("Use short variable names."), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := GetLoader()
	loader.ClearCache()

	template := strings.Join([]string{
		"You are an agent working in $(working_directory).",
		"",
		"## Coding Style",
		"$(file:coding-style.md)",
		"",
		"## Context",
		"$(call:git_status)",
	}, "\n")

	callCount := 0
	funcs := map[string]func() string{
		"git_status": func() string {
			callCount++
			return "On branch main, nothing to commit"
		},
	}

	result := loader.Resolve(
		template,
		map[string]string{"working_directory": "/home/user/project"},
		brainDir,
		funcs,
	)

	// $(working_directory) resolved
	if !strings.Contains(result, "/home/user/project") {
		t.Errorf("Variable not resolved: %s", result)
	}
	if strings.Contains(result, "$(working_directory)") {
		t.Error("Variable placeholder still present")
	}

	// $(file:coding-style.md) resolved
	if !strings.Contains(result, "Use short variable names.") {
		t.Errorf("File not resolved: %s", result)
	}
	if strings.Contains(result, "$(file:coding-style.md)") {
		t.Error("File placeholder still present")
	}

	// $(call:git_status) resolved
	if !strings.Contains(result, "On branch main, nothing to commit") {
		t.Errorf("Call not resolved: %s", result)
	}
	if strings.Contains(result, "$(call:git_status)") {
		t.Error("Call placeholder still present")
	}
	if callCount != 1 {
		t.Errorf("expected git_status called once, got %d", callCount)
	}

	// Missing file gracefully degrades (error message inserted)
	resultMissing := loader.Resolve(
		"Include $(file:nonexistent.md) here.",
		nil, brainDir, nil,
	)
	if !strings.Contains(resultMissing, "[Error: file 'nonexistent.md' doesn't exist]") {
		t.Errorf("missing file should show error, got: %s", resultMissing)
	}

	// Missing variable gracefully degrades (placeholder kept)
	resultNoVar := loader.Resolve(
		"Dir is $(unknown_var).",
		nil, brainDir, nil,
	)
	if !strings.Contains(resultNoVar, "$(unknown_var)") {
		t.Errorf("missing variable should keep placeholder, got: %s", resultNoVar)
	}

	// Missing call gracefully degrades (empty string)
	resultNoCall := loader.Resolve(
		"Status: $(call:missing_fn).",
		nil, brainDir, nil,
	)
	if strings.Contains(resultNoCall, "$(call:missing_fn)") {
		t.Errorf("missing call should be removed, got: %s", resultNoCall)
	}
	if resultNoCall != "Status: ." {
		t.Errorf("expected 'Status: .', got: %s", resultNoCall)
	}
}
