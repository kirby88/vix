package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/kirby88/vix/internal/config"
)

// --- Interfaces ---

type grepRunner interface {
	Run(pattern, path, include, cwd string) (string, error)
}

type globRunner interface {
	Run(pattern, path, cwd string) (string, error)
}

// --- Grep backends ---

type systemGrepBackend struct{}

func (b *systemGrepBackend) Run(pattern, path, include, cwd string) (string, error) {
	LogInfo("[tool.grep] backend=grep cwd=%s pattern=%s path=%s include=%s", cwd, pattern, path, include)
	args := []string{"-rn", "-S"} // -S: follow symlinks (BSD/macOS grep); harmless on GNU grep
	if include != "" {
		args = append(args, fmt.Sprintf("--include=%s", include))
	}
	args = append(args, pattern)
	if path != "" {
		args = append(args, path)
	} else {
		args = append(args, ".")
	}

	cmd := exec.Command("grep", args...)
	cmd.Dir = cwd

	output, _ := cmd.CombinedOutput()
	result := string(output)
	if len(result) > maxOutput {
		result = result[:maxOutput] + fmt.Sprintf("\n... (truncated at %d chars)", maxOutput)
	}
	if result == "" {
		result = "(no matches)"
	}
	return result, nil
}

type rgBackend struct{}

func (b *rgBackend) Run(pattern, path, include, cwd string) (string, error) {
	LogInfo("[tool.grep] backend=rg cwd=%s pattern=%s path=%s include=%s", cwd, pattern, path, include)
	args := []string{"-n", "--follow"} // --follow: follow symlinks
	if include != "" {
		args = append(args, fmt.Sprintf("--glob=%s", include))
	}
	args = append(args, pattern)
	if path != "" {
		args = append(args, path)
	} else {
		args = append(args, ".")
	}

	cmd := exec.Command("rg", args...)
	cmd.Dir = cwd

	output, _ := cmd.CombinedOutput()
	result := string(output)
	if len(result) > maxOutput {
		result = result[:maxOutput] + fmt.Sprintf("\n... (truncated at %d chars)", maxOutput)
	}
	if result == "" {
		result = "(no matches)"
	}
	return result, nil
}

// --- Glob backends ---

type builtinGlobBackend struct{}

func (b *builtinGlobBackend) Run(pattern, path, cwd string) (string, error) {
	LogInfo("[tool.glob] backend=builtin cwd=%s pattern=%s path=%s", cwd, pattern, path)
	base := cwd
	if path != "" {
		base = path
	}
	fullPattern := filepath.Join(base, pattern)

	matches, err := doublestar.FilepathGlob(fullPattern)
	if err != nil {
		return "", err
	}

	sort.Strings(matches)
	if len(matches) > 200 {
		matches = matches[:200]
	}

	if len(matches) == 0 {
		return "(no matches)", nil
	}
	result := strings.Join(matches, "\n")
	if len(matches) == 200 {
		result += "\n... (capped at 200 results)"
	}
	return result, nil
}

type fdGlobBackend struct{}

func (b *fdGlobBackend) Run(pattern, path, cwd string) (string, error) {
	LogInfo("[tool.glob] backend=fd cwd=%s pattern=%s path=%s", cwd, pattern, path)
	searchPath := "."
	if path != "" {
		searchPath = path
	}

	args := []string{"--glob", "--follow", "--max-results", "200", pattern, searchPath} // --follow: follow symlinks

	cmd := exec.Command("fd", args...)
	cmd.Dir = cwd

	output, _ := cmd.CombinedOutput()
	result := string(output)
	if len(result) > maxOutput {
		result = result[:maxOutput] + fmt.Sprintf("\n... (truncated at %d chars)", maxOutput)
	}
	if result == "" {
		result = "(no matches)"
	}
	return result, nil
}

// --- Factory functions ---

func logToolFound(name string)    { log.Printf("\033[32m[tools] ✓ %s found\033[0m", name) }
func logToolMissing(name string)  { log.Printf("\033[31m[tools] ✗ %s not found in PATH\033[0m", name) }

func newGrepRunner(backend string) grepRunner {
	switch backend {
	case "rg":
		if _, err := exec.LookPath("rg"); err != nil {
			logToolMissing("rg (ripgrep)")
			LogWarn("falling back to system grep")
			return &systemGrepBackend{}
		}
		logToolFound("rg (ripgrep)")
		return &rgBackend{}
	default:
		return &systemGrepBackend{}
	}
}

func newGlobRunner(backend string) globRunner {
	switch backend {
	case "fd":
		if _, err := exec.LookPath("fd"); err != nil {
			logToolMissing("fd")
			LogWarn("falling back to builtin glob")
			return &builtinGlobBackend{}
		}
		logToolFound("fd")
		return &fdGlobBackend{}
	default:
		return &builtinGlobBackend{}
	}
}

// --- Config loader ---

// toolsConfigFile is the structure for loading the tools section from settings.json.
type toolsConfigFile struct {
	Tools config.ToolsConfig `json:"tools"`
}

func loadToolsConfig(projectPath string) config.ToolsConfig {
	var result config.ToolsConfig

	// Try home config first as base
	homeDir := config.HomeVixDir()
	for _, dir := range []string{homeDir, filepath.Join(projectPath, ".vix")} {
		if dir == "" {
			continue
		}
		configPath := filepath.Join(dir, "settings.json")
		data, err := os.ReadFile(configPath)
		if err != nil {
			continue
		}
		var cf toolsConfigFile
		if err := json.Unmarshal(data, &cf); err != nil {
			LogWarn("Failed to parse tools config from %s: %v", configPath, err)
			continue
		}
		// Override with non-empty values
		if cf.Tools.Grep.Backend != "" {
			result.Grep = cf.Tools.Grep
		}
		if cf.Tools.Glob.Backend != "" {
			result.Glob = cf.Tools.Glob
		}
	}

	return result
}
