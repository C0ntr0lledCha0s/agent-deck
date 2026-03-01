# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Agent Deck is a mission control system for AI coding agents (Claude Code, Gemini, OpenCode, Cursor). It provides a unified TUI (terminal) and web dashboard for managing multiple simultaneous AI agent sessions running in tmux panes, with smart status detection, MCP management, git worktree isolation, and multi-agent orchestration.

Single binary, Go 1.24+, terminal-first with optional web dashboard.

## Build & Development Commands

```bash
make build              # Build binary to ./build/agent-deck
make test               # Run all tests: go test -race -v ./...
make fmt                # Format code: go fmt ./...
make lint               # Lint (requires golangci-lint)
make ci                 # Run local CI via lefthook: lint + test + build in parallel
make dev                # Auto-reload dev server (requires 'air')
make run                # Run directly without reload
make docker-build       # Build Docker image (agent-deck:latest)
make docker-run         # Build + run Docker container (port 8420)
make docker-up          # Start docker-compose stack (with Traefik labels)
make docker-down        # Stop docker-compose stack
```

### Running a Single Test

```bash
go test -race -v ./internal/session -run TestNewInstance
go test -race -v ./internal/tmux -run TestPromptDetection
```

### Debug Mode

```bash
AGENTDECK_DEBUG=1 agent-deck
```

## Architecture

### Entry Point & CLI

`cmd/agent-deck/main.go` — CLI entry point with subcommand dispatch (version, web, session, mcp, conductor, worktree, etc.). Version constant is hardcoded here. Each subcommand is in its own file (e.g., `web_cmd.go`, `mcp_cmd.go`).

### Internal Packages

| Package | Purpose |
|---------|---------|
| `session` | Core session/group management, MCP catalog, skills catalog, tool detection (Claude/Gemini/OpenCode), notifications, forking, analytics. Largest package. |
| `tmux` | tmux integration: control pipes, PTY management, smart status polling, prompt detection patterns per tool type |
| `ui` | Bubble Tea TUI: home screen, dialogs (new session, fork, MCP, skills, settings), search, analytics panel |
| `web` | HTTP server (port 8420): REST API, WebSocket PTY streaming, SSE status events, push notifications |
| `hub` | Hub dashboard: task store, project registry with keyword-based routing, container integration (docker exec) |
| `git` | Git worktree operations (create/cleanup), status detection |
| `statedb` | SQLite-based state persistence |
| `mcppool` | MCP socket pooling — shares MCP processes across sessions |
| `profile` | Multi-profile support: detects Claude vs Gemini vs OpenCode |
| `logging` | Structured logging (slog) with ring buffer and log aggregation |
| `platform` | Cross-platform utilities |
| `update` | Auto-update mechanism |
| `experiments` | Feature flags for experimental features |
| `clipboard` | Clipboard operations |

### Key Patterns

- **Bubble Tea model**: TUI uses the Elm architecture via charmbracelet/bubbletea. Models implement `Init()`, `Update()`, `View()`.
- **Status detection**: `internal/tmux` polls tmux panes via control pipes and matches tool-specific prompt patterns to determine agent state (running/waiting/idle/error).
- **Session abstraction**: Sessions wrap tmux panes with metadata (title, group, tool type, status). Stored in SQLite via `statedb`.
- **MCP pooling**: `mcppool` shares MCP server processes across sessions via Unix socket proxying, reducing memory ~85-90%.
- **Multi-tool support**: Tool-specific adapters in `session` package handle Claude, Gemini, OpenCode differently (config paths, MCP format, prompt patterns).

### Web Architecture

The web dashboard (`internal/web`) runs on port 8420:
- REST API for session/task CRUD
- WebSocket for live PTY streaming to browser
- SSE (Server-Sent Events) for real-time status updates
- Hub dashboard routes tasks to projects via keyword matching

## Testing Conventions

- Tests use `stretchr/testify` for assertions
- **Critical**: All test packages with `TestMain` force `AGENTDECK_PROFILE=_test` to isolate from production data. Any new package needing tmux/session access must do the same.
- Many tests require a running tmux server and skip gracefully with `skipIfNoTmuxServer(t)` when unavailable
- Tests should use `defer Kill()` on any tmux sessions they create for cleanup
- Test session cleanup in `TestMain` only matches specific known test artifacts — never use broad patterns that could kill real user sessions

## Verifying Visual Elements (Web UI)

When verifying visual changes or testing web UI on a build:

```bash
# 1. Build the binary
make build

# 2. Start TUI + web server (default: 127.0.0.1:8420)
./build/agent-deck web

# 3. Open in browser
#    Dashboard:        http://127.0.0.1:8420
#    Session view:     http://127.0.0.1:8420/s/<session-id>
#    Terminal view:    http://127.0.0.1:8420/terminal
#    Health check:     http://127.0.0.1:8420/healthz

# Custom listen address:
./build/agent-deck web --listen 127.0.0.1:9000

# Read-only mode (disables input):
./build/agent-deck web --read-only
```

**Note:** The `web` subcommand starts the TUI alongside the web server — both run together. A running tmux server is required for sessions to appear. Static assets (HTML, CSS, JS) are embedded in the binary at build time via `//go:embed` in `internal/web/static_files.go`, so changes to files under `internal/web/static/` require a rebuild to take effect.

**Important:** `agent-deck web` (TUI mode) cannot run inside an agent-deck session (recursion guard). Use headless mode instead:

**Recommended (headless mode — full backend):**
```bash
./build/agent-deck web --headless
# Open: http://127.0.0.1:8420
# Full API + dashboard, works inside agent-deck sessions
```

**Testing without conflicting with a running instance:**
```bash
./build/agent-deck web --headless --listen 127.0.0.1:9000
# Open: http://127.0.0.1:9000
# Uses a different port so it won't conflict with any existing agent-deck on 8420
```

**Fallback (static files only — no APIs):**
```bash
cd internal/web && python3 -m http.server 8422 --bind 127.0.0.1
# Open: http://127.0.0.1:8422/static/dashboard.html
# API calls will 404 — this is expected, only the UI renders.
```

## Docker Deployment

Agent Deck can run as a Docker container for headless/server deployments (e.g., SaltBox). The container runs `agent-deck web --headless` and provisions sibling containers via the Docker socket.

### Docker Files

| File | Purpose |
|------|---------|
| `Dockerfile` | Multi-stage production build: `golang:1.24-alpine` (builder) → `ubuntu:24.04` (runtime with tmux) |
| `docker-compose.yml` | Compose stack with Docker socket mount, named volume, Traefik labels |
| `.env.example` | Template for `DOMAIN` (Traefik routing) and `DOCKER_GID` (socket permissions) |
| `.dockerignore` | Excludes .git, build/, docs/, etc. from Docker context |

### Running in Docker

```bash
# Standalone
make docker-build && make docker-run
# Dashboard: http://127.0.0.1:8420

# With docker-compose (includes Traefik labels for SaltBox)
cp .env.example .env
# Edit .env: set DOMAIN, DOCKER_GID (run: stat -c '%g' /var/run/docker.sock)
make docker-up
```

### Container Architecture

- **Socket mount**: `/var/run/docker.sock` is mounted read-write for sibling container provisioning
- **Non-root user**: runs as `agentdeck` (UID 1000); `group_add` in docker-compose.yml grants Docker socket access
- **Persistence**: Named volume at `/home/agentdeck/.agent-deck` (config + SQLite state.db)
- **Port**: 8420 (web dashboard, REST API, WebSocket PTY, SSE status)
- **Entrypoint**: `agent-deck web --headless --listen 0.0.0.0:8420`

### Container Runtime Interface

`internal/hub/workspace/` provides the `ContainerRuntime` interface for provisioning sibling containers:

- `runtime.go` — `ContainerRuntime` interface, `CreateOpts` (with `SecurityOpts`, `CapAdd`, `CapDrop`, `NetworkMode`, `AutoRemove`)
- `docker.go` — Docker Engine API implementation; `SelfNetworks()` discovers the container's own networks for sibling communication
- Used for MCP server containers (`docker.io/mcp/*`) and sandbox containers (isolated agent sessions)

## Git Hooks (lefthook)

- **pre-commit**: `gofmt` check + `go vet` (parallel)
- **pre-push**: lint + test + build (parallel)

## Conventions

- Branch naming: `feature/`, `fix/`, `perf/`, `docs/`, `refactor/`
- Commit messages: conventional commits (`feat:`, `fix:`, `docs:`, `refactor:`)
- Go module path: `github.com/asheshgoplani/agent-deck`
- Config dir: `~/.agent-deck/` (profile-aware via `AGENTDECK_PROFILE` env var)
