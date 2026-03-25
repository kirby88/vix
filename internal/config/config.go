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
	ForceInit  bool
	SocketPath string
}

// Load reads configuration from environment variables.
// The API key is no longer needed on the client side — the daemon handles it.
func Load(forceInit bool) (*Config, error) {
	model := os.Getenv("VIX_MODEL")
	if model == "" {
		model = "claude-sonnet-4-6"
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cannot determine working directory: %w", err)
	}

	return &Config{
		Model:      model,
		CWD:        cwd,
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
	ProjectPath string
	HomeVixDir  string
	Sandbox     SandboxConfig
}

// SandboxConfig holds sandbox settings.
type SandboxConfig struct {
	Enabled    bool
	Filesystem FilesystemConfig
	Network    NetworkConfig
}

// FilesystemConfig holds filesystem access rules.
type FilesystemConfig struct {
	AllowWrite []string
	DenyRead   []string
}

// NetworkConfig holds network access rules (reserved for future use).
type NetworkConfig struct {
	AllowedDomains []string
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
// If projectPath is non-empty, it is used instead of os.Getwd().
func LoadDaemonConfig(projectPath string) (*DaemonConfig, error) {
	if projectPath == "" {
		var err error
		projectPath, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("cannot determine working directory: %w", err)
		}
	}

	homeDir := HomeVixDir()
	if homeDir != "" {
		os.MkdirAll(homeDir, 0o755)
		if err := BootstrapHomeVixDir(homeDir); err != nil {
			log.Printf("[config] bootstrap failed: %v", err)
		}
	}

	return &DaemonConfig{
		ProjectPath: projectPath,
		HomeVixDir:  homeDir,
		Sandbox: SandboxConfig{
			Enabled: false,
			Filesystem: FilesystemConfig{
				AllowWrite: []string{".", "/tmp"},
				DenyRead:   []string{},
			},
			Network: NetworkConfig{
				AllowedDomains: []string{},
			},
		},
	}, nil
}

// ThemeConfig holds user-configurable brand colors.
type ThemeConfig struct {
	Primary   string `json:"primary"`   // hex color like "#E07060"
	Secondary string `json:"secondary"` // hex color like "#50B0E0"
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
