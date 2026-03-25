package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ServerConfig describes how to launch an LSP server for a language.
type ServerConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

const failCooldown = 30 * time.Second

// Pool manages long-lived LSP server subprocesses, one per language.
type Pool struct {
	mu       sync.Mutex
	clients  map[string]*Client
	configs  map[string]*ServerConfig
	failedAt map[string]time.Time // languages that failed to start + when
	rootDir  string
	ctx      context.Context
}

var (
	globalPool   *Pool
	globalPoolMu sync.Mutex
)

// InitPool reads settings.json from homeVixDir and rootDir/.vix/ and creates the global pool.
// Servers are started lazily on first GetClient call.
// homeVixDir may be empty; rootDir is the project root.
func InitPool(ctx context.Context, rootDir string, homeVixDir ...string) {
	globalPoolMu.Lock()
	defer globalPoolMu.Unlock()

	// Load home config first as base, then project overrides
	configs := make(map[string]*ServerConfig)
	if len(homeVixDir) > 0 && homeVixDir[0] != "" {
		for k, v := range loadConfigs2(homeVixDir[0]) {
			configs[k] = v
		}
	}
	for k, v := range loadConfigs(rootDir) {
		configs[k] = v
	}
	LogInfo("LSP pool: %d language(s) configured", len(configs))
	for lang := range configs {
		LogInfo("LSP pool: %s → %s", lang, configs[lang].Command)
	}

	p := &Pool{
		clients:  make(map[string]*Client),
		configs:  configs,
		failedAt: make(map[string]time.Time),
		rootDir:  rootDir,
		ctx:      ctx,
	}
	globalPool = p

	go func() {
		<-ctx.Done()
		p.Shutdown()
	}()
}

// GetPool returns the global pool (nil if not initialized).
func GetPool() *Pool {
	globalPoolMu.Lock()
	defer globalPoolMu.Unlock()
	return globalPool
}

// GetClient returns a running LSP client for the given language.
// Returns (nil, nil) if the language has no config.
func (p *Pool) GetClient(language string) (*Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check failure cooldown — allow retry after 30s
	if t, ok := p.failedAt[language]; ok {
		if time.Since(t) < failCooldown {
			return nil, nil
		}
		delete(p.failedAt, language)
	}

	// Return cached client if alive
	if c, ok := p.clients[language]; ok && c.ready {
		if c.Alive() {
			return c, nil
		}
		// Server died — remove stale client and restart below
		LogInfo("LSP pool: %s server died, restarting", language)
		delete(p.clients, language)
	}

	cfg, ok := p.configs[language]
	if !ok {
		return nil, nil
	}

	c, err := p.startClient(language, cfg)
	if err != nil {
		p.failedAt[language] = time.Now()
		return nil, err
	}
	return c, nil
}

// HasLSP returns whether a language has an LSP config entry.
func (p *Pool) HasLSP(language string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.configs[language]
	return ok
}

// RootDir returns the project root directory this pool was initialized with.
func (p *Pool) RootDir() string {
	return p.rootDir
}

// ConfiguredLanguages returns the list of languages that have LSP configs.
func (p *Pool) ConfiguredLanguages() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	langs := make([]string, 0, len(p.configs))
	for lang := range p.configs {
		langs = append(langs, lang)
	}
	return langs
}

// Shutdown closes all running LSP clients.
func (p *Pool) Shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for lang, c := range p.clients {
		LogInfo("LSP pool: shutting down %s", lang)
		c.Close()
	}
	p.clients = make(map[string]*Client)
}

func (p *Pool) startClient(language string, cfg *ServerConfig) (*Client, error) {
	LogInfo("LSP pool: starting %s (%s %v)", language, cfg.Command, cfg.Args)

	c, err := NewClient(p.ctx, language, p.rootDir, cfg.Command, cfg.Args...)
	if err != nil {
		return nil, err
	}

	if err := c.Initialize(); err != nil {
		c.Close()
		return nil, err
	}

	p.clients[language] = c
	LogInfo("LSP pool: %s ready", language)
	return c, nil
}

type configFile struct {
	LSP map[string]*ServerConfig `json:"lsp"`
}

// loadConfigs2 loads LSP configs from a vix dir directly (e.g. ~/.vix/).
func loadConfigs2(vixDir string) map[string]*ServerConfig {
	if vixDir == "" {
		return make(map[string]*ServerConfig)
	}
	path := filepath.Join(vixDir, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[string]*ServerConfig)
	}

	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return make(map[string]*ServerConfig)
	}

	if cfg.LSP == nil {
		return make(map[string]*ServerConfig)
	}
	return cfg.LSP
}

func loadConfigs(rootDir string) map[string]*ServerConfig {
	path := filepath.Join(rootDir, ".vix", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[string]*ServerConfig)
	}

	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		LogError("LSP pool: failed to parse %s: %v", path, err)
		return make(map[string]*ServerConfig)
	}

	if cfg.LSP == nil {
		return make(map[string]*ServerConfig)
	}
	return cfg.LSP
}
