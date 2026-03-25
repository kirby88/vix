package daemon

import (
	"os"
	"path/filepath"
)

// RegisterBuiltinHandlers registers ping, init, and force_init handlers.
func RegisterBuiltinHandlers(s *Server) {
	s.RegisterHandler("ping", func(data map[string]any) (map[string]any, error) {
		return map[string]any{"status": "ok", "message": "pong"}, nil
	})

	s.RegisterHandler("init", func(data map[string]any) (map[string]any, error) {
		path, _ := data["path"].(string)
		if path == "" {
			return map[string]any{"status": "error", "message": "missing 'path'"}, nil
		}
		handler := s.GetHandler("brain.init")
		if handler == nil {
			return map[string]any{"status": "error", "message": "brain.init handler not registered"}, nil
		}
		return handler(map[string]any{"params": map[string]any{"project_path": path}})
	})

	s.RegisterHandler("force_init", func(data map[string]any) (map[string]any, error) {
		path, _ := data["path"].(string)
		if path == "" {
			return map[string]any{"status": "error", "message": "missing 'path'"}, nil
		}
		brainDir := filepath.Join(path, ".vix")

		// Only remove generated artifacts, preserve user config (settings.json, etc.)
		os.RemoveAll(filepath.Join(brainDir, "context"))

		handler := s.GetHandler("brain.init")
		if handler == nil {
			return map[string]any{"status": "error", "message": "brain.init handler not registered"}, nil
		}
		return handler(map[string]any{"params": map[string]any{"project_path": path}})
	})
}
