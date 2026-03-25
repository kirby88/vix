package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── validateWorkflow ──

func TestValidateWorkflow(t *testing.T) {
	tests := []struct {
		name    string
		pf      WorkflowDef
		wantErr string // empty = no error expected
	}{
		{
			name: "valid workflow",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1": {Type: "agent", Agent: "planner", Prompt: "do something"},
				},
			},
			wantErr: "",
		},
		{
			name: "missing name",
			pf: WorkflowDef{
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1": {Type: "agent", Agent: "planner", Prompt: "do something"},
				},
			},
			wantErr: "missing name",
		},
		{
			name: "no steps",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
			},
			wantErr: "no steps defined",
		},
		{
			name: "empty step id key",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: ""},
				Steps: map[string]WorkflowStepDef{
					"": {Type: "agent", Agent: "planner", Prompt: "do something"},
				},
			},
			wantErr: "empty id",
		},
		{
			name: "missing type",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1": {Agent: "planner", Prompt: "do something"},
				},
			},
			wantErr: "missing type",
		},
		{
			name: "unknown type",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1": {Type: "banana", Agent: "planner", Prompt: "do something"},
				},
			},
			wantErr: "unknown type 'banana'",
		},
		{
			name: "no agent or fork",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1": {Type: "agent", Prompt: "do something"},
				},
			},
			wantErr: "must have either",
		},
		{
			name: "both agent and fork",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step0": {Type: "agent", Agent: "planner", Prompt: "first"},
					"step1": {Type: "agent", Agent: "planner", ForkFrom: "step0", Prompt: "do something", NextSteps: []StepRef{{ID: "step0"}}},
				},
			},
			wantErr: "cannot have both",
		},
		{
			name: "fork from unknown step",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1": {Type: "agent", ForkFrom: "nope", Prompt: "do something"},
				},
			},
			wantErr: "references unknown step",
		},
		{
			name: "fork from existing step is valid",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1": {Type: "agent", Agent: "planner", Prompt: "first", NextSteps: []StepRef{{ID: "step2"}}},
					"step2": {Type: "agent", ForkFrom: "step1", Prompt: "second"},
				},
			},
			wantErr: "",
		},
		{
			name: "missing prompt",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1": {Type: "agent", Agent: "planner"},
				},
			},
			wantErr: "missing prompt",
		},
		{
			name: "valid tool step with options",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1":  {Type: "agent", Agent: "planner", Prompt: "do something", NextSteps: []StepRef{{ID: "review"}}},
					"review": {Type: "tool", Tool: "ask_question_to_user", Options: []StepOption{
						{Title: "Accept", Description: "Approve", Steps: []StepRef{{ID: "step1"}}},
						{Title: "Reject", Description: "Reject", Steps: []StepRef{{ID: "stop"}}},
					}},
				},
			},
			wantErr: "",
		},
		{
			name: "tool step missing tool field",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1": {Type: "tool"},
				},
			},
			wantErr: "requires 'tool' field",
		},
		{
			name: "option step references unknown step",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1":  {Type: "agent", Agent: "planner", Prompt: "do something", NextSteps: []StepRef{{ID: "review"}}},
					"review": {Type: "tool", Tool: "ask_question_to_user", Options: []StepOption{
						{Title: "Accept", Description: "Approve", Steps: []StepRef{{ID: "nonexistent"}}},
					}},
				},
			},
			wantErr: "references unknown step",
		},
		{
			name: "option step stop is valid",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1":  {Type: "agent", Agent: "planner", Prompt: "do something", NextSteps: []StepRef{{ID: "review"}}},
					"review": {Type: "tool", Tool: "ask_question_to_user", Options: []StepOption{
						{Title: "Reject", Description: "Reject the plan", Steps: []StepRef{{ID: "stop"}}},
					}},
				},
			},
			wantErr: "",
		},
		{
			name: "option with has_user_input is valid",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1":  {Type: "agent", Agent: "planner", Prompt: "do something", NextSteps: []StepRef{{ID: "review"}}},
					"review": {Type: "tool", Tool: "ask_question_to_user", Options: []StepOption{
						{Title: "Accept", Description: "Approve", Steps: []StepRef{{ID: "step1"}}},
						{Title: "Modify", Description: "Provide feedback", Steps: []StepRef{{ID: "step1"}}, HasUserInput: true},
					}},
				},
			},
			wantErr: "",
		},
		{
			name: "missing entry_point",
			pf: WorkflowDef{
				Name: "Test Plan",
				Steps: map[string]WorkflowStepDef{
					"step1": {Type: "agent", Agent: "planner", Prompt: "do something"},
				},
			},
			wantErr: "missing entry_point",
		},
		{
			name: "entry_point references unknown step",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "nonexistent"},
				Steps: map[string]WorkflowStepDef{
					"step1": {Type: "agent", Agent: "planner", Prompt: "do something"},
				},
			},
			wantErr: "entry_point 'nonexistent' references unknown step",
		},
		{
			name: "next_step references unknown step",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1": {Type: "agent", Agent: "planner", Prompt: "do something", NextSteps: []StepRef{{ID: "nonexistent"}}},
				},
			},
			wantErr: "next_step 'nonexistent' references unknown step",
		},
		{
			name: "unreachable step",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1":    {Type: "agent", Agent: "planner", Prompt: "do something"},
					"orphaned": {Type: "agent", Agent: "planner", Prompt: "never reached"},
				},
			},
			wantErr: "unreachable",
		},
		{
			name: "next_step stop is valid",
			pf: WorkflowDef{
				Name:       "Test Plan",
				EntryPoint: StepRef{ID: "step1"},
				Steps: map[string]WorkflowStepDef{
					"step1": {Type: "agent", Agent: "planner", Prompt: "do something", NextSteps: []StepRef{{ID: "stop"}}},
				},
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWorkflow(&tt.pf)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

// ── resolveParams ──

func TestResolveParams(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]string
		vars   map[string]string
		want   map[string]string
	}{
		{
			name:   "nil params returns nil",
			params: nil,
			vars:   map[string]string{"x": "y"},
			want:   nil,
		},
		{
			name:   "empty params returns nil",
			params: map[string]string{},
			vars:   map[string]string{"x": "y"},
			want:   nil,
		},
		{
			name:   "literal values passed through",
			params: map[string]string{"key": "hello"},
			vars:   map[string]string{},
			want:   map[string]string{"key": "hello"},
		},
		{
			name:   "$() wrapper resolves from vars",
			params: map[string]string{"prompt": "$(user_prompt)"},
			vars:   map[string]string{"user_prompt": "build a thing"},
			want:   map[string]string{"prompt": "build a thing"},
		},
		{
			name:   "$() with missing var left as-is",
			params: map[string]string{"prompt": "$(missing)"},
			vars:   map[string]string{"other": "value"},
			want:   map[string]string{"prompt": "$(missing)"},
		},
		{
			name:   "mixed literal and $() values",
			params: map[string]string{"a": "literal", "b": "$(x)"},
			vars:   map[string]string{"x": "resolved"},
			want:   map[string]string{"a": "literal", "b": "resolved"},
		},
		{
			name:   "embedded $() within longer string",
			params: map[string]string{"feedback": "The user said: $(user_text). Please address it."},
			vars:   map[string]string{"user_text": "make it faster"},
			want:   map[string]string{"feedback": "The user said: make it faster. Please address it."},
		},
		{
			name:   "multiple embedded $() in one value",
			params: map[string]string{"msg": "$(greeting) $(name)!"},
			vars:   map[string]string{"greeting": "Hello", "name": "World"},
			want:   map[string]string{"msg": "Hello World!"},
		},
		{
			name:   "embedded $() with missing var left as-is",
			params: map[string]string{"msg": "prefix $(unknown) suffix"},
			vars:   map[string]string{"other": "val"},
			want:   map[string]string{"msg": "prefix $(unknown) suffix"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveParams(tt.params, tt.vars)
			if tt.want == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("length mismatch: got %d, want %d", len(got), len(tt.want))
			}
			for k, wantV := range tt.want {
				if got[k] != wantV {
					t.Errorf("key %q: got %q, want %q", k, got[k], wantV)
				}
			}
		})
	}
}

// ── LoadWorkflows ──

func TestLoadWorkflows(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")

		cfg := configFile{
			Version: CurrentConfigVersion,
			Workflows: []WorkflowDef{
				{
					Name:       "My Plan",
					EntryPoint: StepRef{ID: "s1"},
					Steps: map[string]WorkflowStepDef{
						"s1": {Type: "agent", Agent: "planner", Prompt: "plan it"},
					},
				},
			},
		}
		data, _ := json.Marshal(cfg)
		os.WriteFile(path, data, 0644)

		result := LoadWorkflows(path)
		if len(result) != 1 {
			t.Fatalf("expected 1 workflow, got %d", len(result))
		}
		if result[0].Name != "My Plan" {
			t.Errorf("expected name 'My Plan', got %q", result[0].Name)
		}
	})

	t.Run("missing file returns empty", func(t *testing.T) {
		result := LoadWorkflows("/nonexistent/path/settings.json")
		if len(result) != 0 {
			t.Errorf("expected empty result for missing file, got %d", len(result))
		}
	})

	t.Run("invalid JSON returns empty", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")
		os.WriteFile(path, []byte("not json"), 0644)

		result := LoadWorkflows(path)
		if len(result) != 0 {
			t.Errorf("expected empty result for invalid JSON, got %d", len(result))
		}
	})

	t.Run("invalid workflow skipped", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")

		cfg := configFile{
			Version: CurrentConfigVersion,
			Workflows: []WorkflowDef{
				{
					// Missing name → invalid
					EntryPoint: StepRef{ID: "s1"},
					Steps: map[string]WorkflowStepDef{
						"s1": {Type: "agent", Agent: "x", Prompt: "y"},
					},
				},
			},
		}
		data, _ := json.Marshal(cfg)
		os.WriteFile(path, data, 0644)

		result := LoadWorkflows(path)
		if len(result) != 0 {
			t.Errorf("expected 0 workflows (invalid skipped), got %d", len(result))
		}
	})

	t.Run("preserves config order", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")

		cfg := configFile{
			Version: CurrentConfigVersion,
			Workflows: []WorkflowDef{
				{
					Name:       "Workflow B",
					EntryPoint: StepRef{ID: "s1"},
					Steps: map[string]WorkflowStepDef{
						"s1": {Type: "agent", Agent: "a", Prompt: "do it"},
					},
				},
				{
					Name:       "Workflow A",
					EntryPoint: StepRef{ID: "s1"},
					Steps: map[string]WorkflowStepDef{
						"s1": {Type: "agent", Agent: "a", Prompt: "do it"},
					},
				},
			},
		}
		data, _ := json.Marshal(cfg)
		os.WriteFile(path, data, 0644)

		result := LoadWorkflows(path)
		if len(result) != 2 {
			t.Fatalf("expected 2 workflows, got %d", len(result))
		}
		if result[0].Name != "Workflow B" {
			t.Errorf("expected first workflow 'Workflow B', got %q", result[0].Name)
		}
		if result[1].Name != "Workflow A" {
			t.Errorf("expected second workflow 'Workflow A', got %q", result[1].Name)
		}
	})
}

// ── LoadProjectConfig ──

func TestLoadProjectConfig(t *testing.T) {
	t.Run("loads agent and workflows", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")

		cfg := configFile{
			Version: CurrentConfigVersion,
			Agent:   "custom",
			Workflows: []WorkflowDef{
				{
					Name:       "My Workflow",
					EntryPoint: StepRef{ID: "s1"},
					Steps: map[string]WorkflowStepDef{
						"s1": {Type: "agent", Agent: "planner", Prompt: "plan it"},
					},
				},
			},
		}
		data, _ := json.Marshal(cfg)
		os.WriteFile(path, data, 0644)

		result := LoadProjectConfig(path)
		if result.Agent != "custom" {
			t.Errorf("expected agent 'custom', got %q", result.Agent)
		}
		if len(result.Workflows) != 1 {
			t.Fatalf("expected 1 workflow, got %d", len(result.Workflows))
		}
	})

	t.Run("defaults to general agent", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")

		cfg := configFile{
			Version:   CurrentConfigVersion,
			Workflows: []WorkflowDef{},
		}
		data, _ := json.Marshal(cfg)
		os.WriteFile(path, data, 0644)

		result := LoadProjectConfig(path)
		if result.Agent != "general" {
			t.Errorf("expected default agent 'general', got %q", result.Agent)
		}
	})

	t.Run("missing file returns defaults", func(t *testing.T) {
		result := LoadProjectConfig("/nonexistent/path/settings.json")
		if result.Agent != "general" {
			t.Errorf("expected default agent 'general', got %q", result.Agent)
		}
		if len(result.Workflows) != 0 {
			t.Errorf("expected empty workflows, got %d", len(result.Workflows))
		}
	})

	t.Run("version mismatch skips config", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")

		cfg := configFile{
			Version: 999,
			Agent:   "custom",
			Workflows: []WorkflowDef{
				{
					Name:       "Skipped",
					EntryPoint: StepRef{ID: "s1"},
					Steps: map[string]WorkflowStepDef{
						"s1": {Type: "agent", Agent: "planner", Prompt: "plan it"},
					},
				},
			},
		}
		data, _ := json.Marshal(cfg)
		os.WriteFile(path, data, 0644)

		result := LoadProjectConfig(path)
		if result.Agent != "general" {
			t.Errorf("expected default agent 'general' (config skipped), got %q", result.Agent)
		}
		if len(result.Workflows) != 0 {
			t.Errorf("expected 0 workflows (config skipped), got %d", len(result.Workflows))
		}
	})

	t.Run("missing version (zero) skips config", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")

		cfg := configFile{
			Agent: "custom",
		}
		data, _ := json.Marshal(cfg)
		os.WriteFile(path, data, 0644)

		result := LoadProjectConfig(path)
		if result.Agent != "general" {
			t.Errorf("expected default agent 'general' (config skipped), got %q", result.Agent)
		}
	})
}

// ── stripMarkdownFence ──

func TestStripMarkdownFence(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no fence returns as-is",
			input: `{"key":"value"}`,
			want:  `{"key":"value"}`,
		},
		{
			name:  "json fence at start",
			input: "```json\n{\"key\":\"value\"}\n```",
			want:  `{"key":"value"}`,
		},
		{
			name:  "generic fence at start",
			input: "```\n{\"key\":\"value\"}\n```",
			want:  `{"key":"value"}`,
		},
		{
			name:  "preamble before json fence",
			input: "Some preamble text\n\n```json\n{\"display\":\"summary\",\"result\":\"details\"}\n```",
			want:  `{"display":"summary","result":"details"}`,
		},
		{
			name:  "preamble before generic fence",
			input: "Here is the result:\n```\n{\"key\":\"value\"}\n```",
			want:  `{"key":"value"}`,
		},
		{
			name:  "whitespace around input",
			input: "  \n```json\n{\"a\":1}\n```\n  ",
			want:  `{"a":1}`,
		},
		{
			name:  "nested fences inside JSON string",
			input: "```json\n{\"display\":\"summary\",\"result\":\"```go\\nfunc main(){}\\n```\"}\n```",
			want:  `{"display":"summary","result":"` + "```go\\nfunc main(){}\\n```" + `"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripMarkdownFence(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ── buildStepVars ──

func TestBuildStepVars(t *testing.T) {
	t.Run("raw output only", func(t *testing.T) {
		results := map[string]*StepResult{
			"explore": {Output: "some text"},
		}
		vars := buildStepVars(results)
		if vars["step.explore"] != "some text" {
			t.Errorf("expected raw output, got %q", vars["step.explore"])
		}
	})

	t.Run("parsed JSON fields included", func(t *testing.T) {
		results := map[string]*StepResult{
			"plan": {
				Output: `{"display":"plan summary","result":"details"}`,
				Parsed: map[string]any{"display": "plan summary", "result": "details"},
			},
		}
		vars := buildStepVars(results)
		if vars["step.plan.display"] != "plan summary" {
			t.Errorf("expected 'plan summary', got %q", vars["step.plan.display"])
		}
	})

	t.Run("step params included", func(t *testing.T) {
		results := map[string]*StepResult{
			"explore": {
				Output: "text",
				Params: map[string]string{"prompt": "user request"},
			},
		}
		vars := buildStepVars(results)
		if vars["step.explore.prompt"] != "user request" {
			t.Errorf("expected 'user request', got %q", vars["step.explore.prompt"])
		}
	})

	t.Run("nil parsed means no JSON vars", func(t *testing.T) {
		results := map[string]*StepResult{
			"plan": {Output: "not json", Parsed: nil},
		}
		vars := buildStepVars(results)
		if _, ok := vars["step.plan.display"]; ok {
			t.Error("expected no step.plan.display when Parsed is nil")
		}
	})
}
