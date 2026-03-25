package config

import (
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

//go:embed defaults/*
var defaultFiles embed.FS

// BootstrapHomeVixDir writes default config, agent, and prompt files into
// homeVixDir when settings.json is absent (first run). Existing files are
// never overwritten.
func BootstrapHomeVixDir(homeVixDir string) error {
	configPath := filepath.Join(homeVixDir, "settings.json")
	if _, err := os.Stat(configPath); err == nil {
		return nil // already bootstrapped
	}

	return fs.WalkDir(defaultFiles, "defaults", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Strip the "defaults/" prefix to get the target relative path.
		rel, _ := filepath.Rel("defaults", path)
		target := filepath.Join(homeVixDir, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		data, err := defaultFiles.ReadFile(path)
		if err != nil {
			return err
		}

		// O_CREATE|O_EXCL: create only if it doesn't already exist.
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if os.IsExist(err) {
				return nil // skip existing files
			}
			return err
		}
		defer f.Close()

		if _, err := f.Write(data); err != nil {
			return err
		}

		log.Printf("[config] bootstrap: wrote %s", target)
		return nil
	})
}
