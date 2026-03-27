package daemon

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/kirby88/vix/internal/config"
	"github.com/kirby88/vix/internal/protocol"
)

// HandlerFunc is the type for daemon request handlers.
type HandlerFunc func(data map[string]any) (map[string]any, error)

// Server is the Unix socket daemon server with a handler registry.
type Server struct {
	mu        sync.RWMutex
	handlers  map[string]HandlerFunc
	sockPath  string
	accessDB  *sql.DB // Access stats database (nil if init failed)
	sessionID string  // Unique ID for this daemon session

	// Agent session support
	apiKey    string
	model     string
	sessions  map[string]*Session
	sessionMu sync.Mutex
	serverCtx context.Context

	// User-level config directory (~/.vix/)
	homeVixDir string
}

// NewServer creates a new daemon server.
func NewServer(sockPath, apiKey, model string, daemonConfig *config.DaemonConfig) *Server {
	sid := generateSessionID()
	s := &Server{
		handlers:   make(map[string]HandlerFunc),
		sockPath:   sockPath,
		sessionID:  sid,
		apiKey:     apiKey,
		model:      model,
		sessions:   make(map[string]*Session),
		homeVixDir: daemonConfig.HomeVixDir,
	}

	// Set LLM log directory to ~/.vix/logs/
	if s.homeVixDir != "" {
		SetLLMLogDir(filepath.Join(s.homeVixDir, "logs"))
	}

	return s
}

// LogAccess logs a tool access event. Safe to call even if accessDB is nil.
func (s *Server) LogAccess(toolName string, params map[string]any) {
	if s.accessDB == nil {
		return
	}
	logToolAccess(s.accessDB, s.sessionID, toolName, params)
}

// RegisterHandler registers a handler for the given command.
func (s *Server) RegisterHandler(command string, handler HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[command] = handler
	LogInfo("Registered handler: %s", command)
}

// GetHandler returns the handler for the given command, or nil.
func (s *Server) GetHandler(command string) HandlerFunc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.handlers[command]
}

// ListenAndServe starts the Unix socket server and blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	s.serverCtx = ctx

	// Remove stale socket file
	if _, err := os.Stat(s.sockPath); err == nil {
		os.Remove(s.sockPath)
	}

	listener, err := net.Listen("unix", s.sockPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer func() {
		listener.Close()
		os.Remove(s.sockPath)
	}()

	LogInfo("Daemon listening on %s", s.sockPath)

	// Accept loop with context cancellation
	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				LogInfo("Daemon shutting down.")
				return nil
			default:
				LogError("Accept error: %v", err)
				continue
			}
		}
		go s.handleClient(conn)
	}
}

func (s *Server) handleClient(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB buffer
	if !scanner.Scan() {
		s.writeError(conn, "empty request")
		return
	}
	line := scanner.Bytes()

	// Check if this is a session.start message — upgrade to persistent session
	var cmd protocol.SessionCommand
	if err := json.Unmarshal(line, &cmd); err == nil && cmd.Type == "session.start" {
		s.handleSession(conn, scanner, cmd)
		return
	}

	var request map[string]any
	if err := json.Unmarshal(line, &request); err != nil {
		s.writeError(conn, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	// Route by action or command field
	action, _ := request["action"].(string)
	if action == "" {
		action, _ = request["command"].(string)
	}

	LogInfo("Received action=%s", action)

	handler := s.GetHandler(action)
	if handler == nil {
		s.writeResponse(conn, map[string]any{
			"status":  "error",
			"message": fmt.Sprintf("unknown action: %s", action),
		})
		return
	}

	response, err := handler(request)
	if err != nil {
		LogError("Handler error for %s: %v", action, err)
		s.writeResponse(conn, map[string]any{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	LogInfo("Completed action=%s status=%v", action, response["status"])
	s.writeResponse(conn, response)
}

// handleSession upgrades a connection to a persistent bidirectional session.
func (s *Server) handleSession(conn net.Conn, scanner *bufio.Scanner, startCmd protocol.SessionCommand) {
	// Parse session start data
	var startData protocol.SessionStartData
	json.Unmarshal(startCmd.Data, &startData)

	cwd := startData.CWD
	if cwd == "" {
		cwd2, _ := os.Getwd()
		cwd = cwd2
	}

	model := startData.Model
	if model == "" {
		model = s.model
	}

	// Create LLM with daemon's API key
	llm := NewLLM(s.apiKey, model)

	// Initialize access stats database for this session's project
	db, err := initAccessStatsDB(cwd)
	if err != nil {
		LogError("Failed to initialize access stats DB (continuing without stats): %v", err)
	} else {
		s.accessDB = db
	}

	sessionID := generateSessionID()
	session := NewSession(sessionID, s, llm, model, cwd, s.homeVixDir, startData.ForceInit, startData.DisableAutomaticWritePermission, startData.Headless, s.serverCtx)
	s.sessionMu.Lock()
	s.sessions[sessionID] = session
	s.sessionMu.Unlock()

	LogInfo("Session %s started (cwd=%s, model=%s)", sessionID, cwd, model)

	// Send session started event
	s.writeEvent(conn, protocol.SessionEvent{
		Type: "event.session_started",
		Data: protocol.EventSessionStarted{SessionID: sessionID},
	})

	// Writer goroutine: reads from session.eventChan, writes NDJSON to socket
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case event, ok := <-session.eventChan:
				if !ok {
					return
				}
				s.writeEvent(conn, event)
			case <-session.ctx.Done():
				// Drain remaining events
				for {
					select {
					case event := <-session.eventChan:
						s.writeEvent(conn, event)
					default:
						return
					}
				}
			}
		}
	}()

	// Reader goroutine: reads NDJSON from socket, feeds session.commandChan
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for scanner.Scan() {
			var cmd protocol.SessionCommand
			if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
				LogError("Session %s: invalid command JSON: %v", sessionID, err)
				continue
			}

			if cmd.Type == "session.cancel" {
				// Cancel the active stream immediately
				if session.cancelStream != nil {
					session.cancelStream()
				}
			}

			select {
			case session.commandChan <- cmd:
			case <-session.ctx.Done():
				return
			}

			if cmd.Type == "session.close" {
				return
			}
		}
		// Socket closed — cancel session
		session.cancel()
	}()

	// Run the agent loop (blocking)
	session.Run()

	// Wait for reader/writer to finish
	session.cancel()
	<-readerDone
	<-writerDone

	// Remove session from map
	s.sessionMu.Lock()
	delete(s.sessions, sessionID)
	s.sessionMu.Unlock()

	LogInfo("Session %s ended", sessionID)
}

func (s *Server) writeEvent(conn net.Conn, event protocol.SessionEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		LogError("Marshal event error: %v", err)
		return
	}
	data = append(data, '\n')
	conn.Write(data)
}

func (s *Server) writeResponse(conn net.Conn, resp map[string]any) {
	data, err := json.Marshal(resp)
	if err != nil {
		LogError("Marshal response error: %v", err)
		return
	}
	data = append(data, '\n')
	conn.Write(data)
}

func (s *Server) writeError(conn net.Conn, msg string) {
	s.writeResponse(conn, map[string]any{
		"status":  "error",
		"message": msg,
	})
}

// Shutdown gracefully closes all server resources.
func (s *Server) Shutdown() {
	if s.accessDB != nil {
		if err := s.accessDB.Close(); err != nil {
			LogError("Error closing access stats DB: %v", err)
		} else {
			LogInfo("Access stats database closed")
		}
	}
}
