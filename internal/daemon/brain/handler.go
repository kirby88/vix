package brain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kirby88/vix/internal/config"
	"github.com/kirby88/vix/internal/daemon/brain/lsp"
)

var daemonCtx context.Context

// RegisterBrainHandlers registers brain.* command handlers with the daemon.
func RegisterBrainHandlers(register func(string, func(map[string]any) (map[string]any, error)), apiKey string, ctx context.Context) {
	daemonCtx = ctx
	register("brain.init", func(data map[string]any) (map[string]any, error) {
		return doBrainInit(data, apiKey)
	})
	register("brain.update_files", func(data map[string]any) (map[string]any, error) {
		return doBrainUpdateFiles(data, apiKey)
	})
}

func doBrainInit(data map[string]any, apiKey string) (map[string]any, error) {
	params, _ := data["params"].(map[string]any)
	projectPath, _ := params["project_path"].(string)
	if projectPath == "" {
		projectPath = "."
	}
	root, _ := filepath.Abs(projectPath)

	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return map[string]any{"status": "error", "message": fmt.Sprintf("Not a directory: %s", root)}, nil
	}

	start := time.Now()
	brainDir := filepath.Join(root, ".vix")
	os.MkdirAll(brainDir, 0o755)

	LogInfo("Brain init starting for %s", root)

	// === Phase 1: Static Analysis ===
	LogInfo("Phase 1: Static analysis")

	// 1. Scan files
	files := ScanProject(root)

	// 2. Parse external dependencies
	externalDeps := ParseDependencies(root)

	// 3. Detect frameworks
	detection := DetectFrameworks(root, files, externalDeps)

	// 4. Initialize LSP pool and parse symbols
	lsp.InitPool(daemonCtx, root, config.HomeVixDir())

	// Probe LSP servers — logs warning + caches failure so per-file calls skip silently
	pool := lsp.GetPool()
	if pool != nil {
		for _, lang := range pool.ConfiguredLanguages() {
			if _, err := pool.GetClient(lang); err != nil {
				LogError("LSP server for %s failed to start: %v", lang, err)
				if lang == "go" {
					LogError("  Install gopls: go install golang.org/x/tools/gopls@latest")
					LogError("  Then ensure ~/go/bin is in your PATH")
				}
			}
		}
	}

	var allSymbols []SymbolInfo
	var allCalls []CallInfo
	for _, fi := range files {
		if fi.Language != "" {
			symbols, calls := ParseFile(fi.Path, root, fi.Language)
			allSymbols = append(allSymbols, symbols...)
			if calls != nil {
				allCalls = append(allCalls, calls...)
			}
		}
	}
	LogInfo("Parsed %d symbols, %d calls", len(allSymbols), len(allCalls))

	// 5. Build import graph
	imports := BuildImportGraph(root, files)
	hubFiles := FindHubFiles(imports, 20)

	// 6. Aggregate project metadata
	languages := make(map[string]int)
	totalLines := 0
	for _, f := range files {
		if f.Language != "" {
			languages[f.Language]++
		}
		totalLines += f.LineCount
	}

	entryPoints := make([]string, 0)
	configFilesList := make([]string, 0)
	for _, f := range files {
		if f.IsEntryPoint {
			entryPoints = append(entryPoints, f.Path)
		}
		if f.IsConfig {
			configFilesList = append(configFilesList, f.Path)
		}
	}

	project := ProjectMeta{
		Name:              filepath.Base(root),
		RootPath:          root,
		TotalFiles:        len(files),
		TotalLines:        totalLines,
		Languages:         languages,
		EntryPoints:       entryPoints,
		ConfigFiles:       configFilesList,
		ExternalDeps:      externalDeps,
		Frameworks:        detection.Frameworks,
		Patterns:          detection.Patterns,
		TestingFrameworks: detection.Testing,
		CICD:              detection.CICD,
	}

	// Ensure non-nil slices for JSON
	if allSymbols == nil {
		allSymbols = []SymbolInfo{}
	}
	if imports == nil {
		imports = []ImportInfo{}
	}
	if allCalls == nil {
		allCalls = []CallInfo{}
	}
	if hubFiles == nil {
		hubFiles = []HubFile{}
	}

	// 8. Build index
	index := &BrainIndex{
		Project:  project,
		Files:    files,
		Symbols:  allSymbols,
		Imports:  imports,
		Calls:    allCalls,
		HubFiles: hubFiles,
	}

	phase1Duration := time.Since(start)
	LogInfo("Phase 1 complete in %.1fs", phase1Duration.Seconds())

	// === Phase 2: Semantic Analysis ===
	if apiKey != "" && len(allSymbols) > 0 {
		LogInfo("Phase 2: Semantic analysis")
		if err := RunPhase2(context.Background(), apiKey, root, index, brainDir); err != nil {
			LogError("Phase 2 failed: %v", err)
		}
	} else if apiKey != "" {
		LogWarn("Skipping Phase 2: no symbols extracted (LSP may not be available)")
	} else {
		LogWarn("No API key available, skipping Phase 2 semantic analysis")
	}

	duration := time.Since(start)
	LogInfo("Brain init complete in %.1fs", duration.Seconds())

	return map[string]any{
		"status": "ok",
		"data": map[string]any{
			"project_name":        project.Name,
			"files_analyzed":      len(files),
			"symbols_extracted":   len(allSymbols),
			"hub_files":           len(hubFiles),
			"frameworks_detected": detection.Frameworks,
			"brain_path":          brainDir,
			"init_duration_seconds": fmt.Sprintf("%.1f", duration.Seconds()),
		},
	}, nil
}

func doBrainUpdateFiles(data map[string]any, apiKey string) (map[string]any, error) {
	return doBrainInit(data, apiKey)
}

