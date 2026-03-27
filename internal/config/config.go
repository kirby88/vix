package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// Config holds client configuration.
type Config struct {
	Model      string
	CWD        string
	Workdir    string
	ForceInit  bool
	SocketPath string
}

// Load reads configuration from environment variables.
// The API key is no longer needed on the client side — the daemon handles it.
// If workdir is non-empty, it is resolved to an absolute path and used as the
// session working directory instead of os.Getwd().
func Load(forceInit bool, workdir string) (*Config, error) {
	model := os.Getenv("VIX_MODEL")
	if model == "" {
		model = "claude-sonnet-4-6"
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cannot determine working directory: %w", err)
	}

	if workdir != "" {
		abs, err := filepath.Abs(workdir)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve workdir %q: %w", workdir, err)
		}
		cwd = abs
	}

	return &Config{
		Model:      model,
		CWD:        cwd,
		Workdir:    workdir,
		ForceInit:  forceInit,
		SocketPath: "/tmp/vix_daemon.sock",
	}, nil
}

// HomeVixDir returns the path to ~/.vix/.
func HomeVixDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".vix")
}

// DaemonConfig holds daemon-side configuration.
type DaemonConfig struct {
	HomeVixDir string
}

// ToolsConfig holds tool backend configuration.
type ToolsConfig struct {
	Grep ToolBackendConfig `json:"grep"`
	Glob ToolBackendConfig `json:"glob"`
}

// ToolBackendConfig holds a single tool's backend selection.
type ToolBackendConfig struct {
	Backend string `json:"backend"`
}

// LoadDaemonConfig loads daemon configuration with defaults.
func LoadDaemonConfig() (*DaemonConfig, error) {
	homeDir := HomeVixDir()
	if homeDir != "" {
		os.MkdirAll(homeDir, 0o755)
		if err := BootstrapHomeVixDir(homeDir); err != nil {
			log.Printf("[config] bootstrap failed: %v", err)
		}
	}

	return &DaemonConfig{
		HomeVixDir: homeDir,
	}, nil
}

// ThemeConfig holds user-configurable brand colors.
type ThemeConfig struct {
	Primary   string `json:"primary"`   // hex color like "#BC63FC"
	Secondary string `json:"secondary"` // hex color like "#A3FC63"
}

// LoadThemeConfig reads theme colors from settings.json files.
// Home config (~/.vix/settings.json) is loaded first, then project config
// (.vix/settings.json) overrides per-field.
func LoadThemeConfig(cwd string) ThemeConfig {
	var tc ThemeConfig

	paths := []string{
		filepath.Join(HomeVixDir(), "settings.json"),
		filepath.Join(cwd, ".vix", "settings.json"),
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var wrapper struct {
			Theme ThemeConfig `json:"theme"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			log.Printf("[config] failed to parse theme from %s: %v", p, err)
			continue
		}
		if wrapper.Theme.Primary != "" {
			tc.Primary = wrapper.Theme.Primary
		}
		if wrapper.Theme.Secondary != "" {
			tc.Secondary = wrapper.Theme.Secondary
		}
	}

	return tc
}
