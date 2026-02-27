# Workspace Integration Design

**Date:** 2026-02-26
**Branch:** feature/Hub-Visual-Alignment
**Status:** Approved

## Problem

The hub dashboard is fully wired to live session data (SSE, WebSocket terminal streaming, task CRUD) but appears disconnected because of a cold-start problem:

1. No projects exist and there's no UI to create them from the dashboard
2. `/api/workspaces` doesn't exist — the frontend falls back to empty project-derived list
3. `phasePrompt()` is implemented but never sent — bridge sessions start as blank Claude Code terminals
4. Container lifecycle (create/start/stop) isn't exposed through the dashboard

## Approach

Monolith extension (Approach A). The `Project` model gains container config fields. A new `internal/hub/workspace/` package wraps the Docker Go SDK for container CRUD. No separate `Workspace` type — Project **is** the workspace.

A simple `ContainerRuntime` interface allows future Coder provider integration.

## Data Model

The `Project` struct gains container configuration fields. Existing fields unchanged.

```go
type Project struct {
    // Existing fields
    Name        string    `json:"name"`
    Repo        string    `json:"repo,omitempty"`
    Path        string    `json:"path"`
    Keywords    []string  `json:"keywords"`
    Container   string    `json:"container,omitempty"`
    DefaultMCPs []string  `json:"defaultMcps,omitempty"`
    CreatedAt   time.Time `json:"createdAt"`
    UpdatedAt   time.Time `json:"updatedAt"`

    // New: container provisioning config
    Image       string            `json:"image,omitempty"`       // e.g. "sandbox-image:latest"
    CPULimit    float64           `json:"cpuLimit,omitempty"`    // cores, e.g. 2.0
    MemoryLimit int64             `json:"memoryLimit,omitempty"` // bytes
    Volumes     []VolumeMount     `json:"volumes,omitempty"`
    Env         map[string]string `json:"env,omitempty"`

    // Runtime state (not persisted, populated on List/Get)
    ContainerStatus string `json:"containerStatus,omitempty"` // "running"|"stopped"|"not_created"
}

type VolumeMount struct {
    Host      string `json:"host"`
    Container string `json:"container"`
    ReadOnly  bool   `json:"readOnly,omitempty"`
}
```

The existing `Container` field (string) works for "reference existing" mode. When `Image` is set, auto-provisioning kicks in — container name is auto-derived as `agentdeck-{project-name}`.

## Container Runtime Interface

New package `internal/hub/workspace/` with Docker Go SDK implementation.

```go
// internal/hub/workspace/runtime.go

type ContainerRuntime interface {
    Create(ctx context.Context, opts CreateOpts) (containerID string, err error)
    Start(ctx context.Context, containerID string) error
    Stop(ctx context.Context, containerID string, timeout time.Duration) error
    Remove(ctx context.Context, containerID string) error
    Status(ctx context.Context, containerID string) (ContainerState, error)
    Stats(ctx context.Context, containerID string) (*ContainerStats, error)
    Exec(ctx context.Context, containerID string, cmd []string) (string, error)
}

type CreateOpts struct {
    Name        string
    Image       string
    WorkDir     string
    Mounts      []Mount
    Env         map[string]string
    CPULimit    float64
    MemoryLimit int64
    Labels      map[string]string // e.g. {"agentdeck.project": "myapp"}
}

type ContainerState struct {
    Status    string // "running"|"stopped"|"not_found"
    StartedAt time.Time
}

type ContainerStats struct {
    CPUPercent float64
    MemUsage   int64
    MemLimit   int64
}
```

```go
// internal/hub/workspace/docker.go

type DockerRuntime struct {
    client *client.Client // github.com/docker/docker/client
}

func NewDockerRuntime() (*DockerRuntime, error) {
    cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
    // ...
}
```

Replaces the existing `ContainerExecutor` CLI interface. The old `DockerExecutor` and `SessionLauncher` get refactored to use `ContainerRuntime`.

## API Changes

### New endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `GET /api/workspaces` | GET | List projects enriched with container status/stats |
| `POST /api/workspaces/{name}/start` | POST | Start a project's container (create if needed) |
| `POST /api/workspaces/{name}/stop` | POST | Stop a project's container |
| `POST /api/workspaces/{name}/remove` | POST | Remove a project's container |
| `GET /api/workspaces/{name}/stats` | GET | Live CPU/memory stats for a running container |

### Modified endpoints

- `POST /api/projects` — accepts new fields (`image`, `cpuLimit`, `memoryLimit`, `volumes`, `env`). When `image` is set, auto-creates and starts the container.
- `DELETE /api/projects/{name}` — optionally removes the associated container (`?removeContainer=true`).
- `POST /api/tasks` — if project has an auto-provisioned container that's stopped, auto-starts it before launching the session.

### `GET /api/workspaces` response

```json
{
  "workspaces": [
    {
      "name": "agent-deck",
      "repo": "C0ntr0lledCha0s/agent-deck",
      "path": "/workspace/agent-deck",
      "image": "sandbox-image:latest",
      "container": "agentdeck-agent-deck",
      "containerStatus": "running",
      "cpuPercent": 12.4,
      "memUsage": 524288000,
      "memLimit": 2147483648,
      "activeTasks": 2
    }
  ]
}
```

This is a view endpoint — reads from `ProjectStore` and enriches with live container state from `ContainerRuntime`. No separate workspace storage.

## Dashboard UI Changes

### Add Project Modal

Triggered from: filter bar "Add Project" button, or "+ Add Project" option in New Task modal's project dropdown.

Fields:
- **GitHub Repo** (text) — primary input
- **Name** (text) — auto-derived from repo, editable
- **Path** (text) — auto-filled as `~/projects/{name}`, editable
- **Keywords** (text) — comma-separated
- **Container mode** (radio) — "None (local)", "Existing container", "Auto-provision"
  - Existing: shows container name text input
  - Auto-provision: shows image, CPU limit, memory limit fields

### Workspaces Sidebar View

Fetches from `/api/workspaces` (now real). Each card shows: name, status badge, CPU/mem bars, active task count. Action buttons: Start/Stop/Remove. "Provision new workspace" opens Add Project modal with auto-provision pre-selected. Polls `/api/workspaces/{name}/stats` every 5s for running containers.

### Manage Projects View

Accessible from filter bar. Lists all projects with edit/delete actions. Edit opens pre-filled modal. Delete shows confirmation with optional container removal.

### phasePrompt Wiring

When `StartPhase` creates a session, send the phase prompt to the tmux session via `SendKeys`. Tasks start working immediately instead of blank terminals.

## End-to-End Flow

```
User clicks "Add Project"
  → POST /api/projects (creates JSON + auto-provisions container)
    → ProjectStore.Save()
    → ContainerRuntime.Create() + Start()
    → SSE "tasks" event fires

User creates task: "Fix the auth bug"
  → POST /api/tasks
    → TaskStore.Save()
    → HubSessionBridge.StartPhase("t-003", "brainstorm")
      → Creates session.Instance in SQLite
      → Links task.Sessions[0].ClaudeSessionID → instance.ID
      → tmux new-session inside container via ContainerRuntime.Exec()
      → tmux send-keys: phasePrompt() → "/brainstorm Fix the auth bug"
    → SSE events fire

Dashboard receives SSE
  → sessionMap rebuilt
  → Task card shows live status
  → Terminal connects via /ws/session/{tmuxSession}

Phase transition
  → POST /api/tasks/t-003/transition { nextPhase: "plan" }
    → Current session marked "complete"
    → New session created with plan prompt
    → Dashboard auto-switches terminal
```

## Testing

### Unit tests
- `internal/hub/workspace/docker_test.go` — mock Docker client
- `internal/hub/workspace/runtime_test.go` — interface contract tests
- `internal/web/hub_session_bridge_test.go` — verify phasePrompt sent via SendKeys
- `internal/web/handlers_hub_test.go` — workspace endpoints, project CRUD with auto-provision

### Integration tests
- `internal/hub/workspace/integration_test.go` — real Docker lifecycle, `skipIfNoDocker(t)`
- Hub session bridge end-to-end: project → task → session → phasePrompt

### Frontend
- Manual verification via `make build && ./build/agent-deck web`
- All tests use `AGENTDECK_PROFILE=_test`, `defer Kill()`, graceful skips

## Files to Create/Modify

| File | Action | Purpose |
|------|--------|---------|
| `internal/hub/workspace/runtime.go` | Create | ContainerRuntime interface + types |
| `internal/hub/workspace/docker.go` | Create | Docker SDK implementation |
| `internal/hub/workspace/docker_test.go` | Create | Unit tests with mock client |
| `internal/hub/workspace/integration_test.go` | Create | Real Docker integration tests |
| `internal/hub/models.go` | Modify | Add Image, CPULimit, MemoryLimit, Volumes, Env to Project |
| `internal/hub/container.go` | Modify | Refactor to use ContainerRuntime |
| `internal/hub/session.go` | Modify | Refactor SessionLauncher to use ContainerRuntime |
| `internal/web/server.go` | Modify | Initialize DockerRuntime, register workspace routes |
| `internal/web/handlers_hub.go` | Modify | Add workspace handlers, update project/task handlers |
| `internal/web/handlers_hub_test.go` | Modify | Test workspace endpoints |
| `internal/web/hub_session_bridge.go` | Modify | Wire phasePrompt via SendKeys |
| `internal/web/hub_session_bridge_test.go` | Modify | Test phasePrompt wiring |
| `internal/web/static/dashboard.html` | Modify | Add Project modal, Manage Projects view |
| `internal/web/static/dashboard.js` | Modify | Project CRUD, workspace view, container controls |
| `internal/web/static/dashboard.css` | Modify | Modal and workspace card styles |
| `go.mod` | Modify | Add github.com/docker/docker dependency |
