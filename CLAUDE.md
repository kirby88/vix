# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Vix is an AI coding agent built in Go. It consists of a daemon backend that handles LLM interactions, tool execution, and code analysis, paired with a TUI client for user interaction.

## Architecture

```
cmd/
  vix/            # TUI client entry point
  vix-daemon/     # Daemon server entry point
internal/
  agent/          # Agent loop, LLM streaming, tool schemas
  config/         # API key and configuration loading
  daemon/         # Unix socket server, session management, tool handlers
    brain/        # Code analysis engine (scanner, parser, semantic analysis)
      lsp/        # Language server protocol integration
  headless/       # Headless mode (no TUI)
  protocol/       # Shared types between client and daemon
  ui/             # Bubble Tea TUI components
```

The daemon listens on a Unix socket (`/tmp/vix_daemon.sock`). The TUI client connects to it and exchanges JSON events.

## Development Commands

```bash
# Build everything
go build ./...

# Build binaries
go build -o bin/vix ./cmd/vix
go build -o bin/vix-daemon ./cmd/vix-daemon

# Run tests
go test ./...

# Run a specific test
go test ./internal/daemon/... -run TestSessionHandlePlan -v
```

## Running

Start the daemon and client in separate terminals:

```bash
./bin/vix-daemon
./bin/vix
```

## Key Conventions

- **Go style** - follow standard Go conventions, use `gofmt`.
- **Error handling** - return errors, don't panic. Log with `log.Printf` in the daemon.
- **UI events** - the daemon emits events via `s.emit("event.name", data)` which the TUI consumes.
- **No over-engineering** - keep changes minimal and focused. Don't add abstractions for one-time operations.
- **Security** - sanitize all user inputs before shell execution. Be careful with tool execution paths.

## Environment

- **Go 1.26+** required
- **ANTHROPIC_API_KEY** environment variable or `.env` file for LLM access
- **LSP servers** (optional): gopls, pylsp, typescript-language-server for code intelligence
- **LSP config**: `.vix/settings.json` in project root
