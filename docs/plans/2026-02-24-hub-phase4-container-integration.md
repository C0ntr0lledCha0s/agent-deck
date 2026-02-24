# Hub Phase 4: Container Integration — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Wire the hub's task creation and input endpoints to real container tmux sessions via `docker exec`, add container health checks, and stream terminal output to the browser via SSE.

**Architecture:** New `container.go` in the hub package provides a `ContainerExecutor` interface that wraps `os/exec` calls to `docker exec`. The web layer wires `POST /api/tasks` to launch Claude Code in a container tmux session, `POST /api/tasks/{id}/input` to `tmux send-keys`, and adds `GET /api/tasks/{id}/preview` as an SSE endpoint streaming `tail -f` output from tmux `pipe-pane` log files. An interface-based design enables mock testing without Docker.

**Tech Stack:** Go `os/exec` (docker CLI), `bufio.Scanner` for SSE streaming, interface-based mocking for tests.

**Prerequisites:** Phase 2 (Task Creation endpoints), Phase 3 (Project Routing). Container infrastructure from Phase 0.6 (tmux inside containers, `pipe-pane` configured).

**Important:** This phase depends on actual Docker containers with tmux installed. Tests use a mock executor that simulates docker exec responses. Integration testing against real containers is out of scope for this plan — validate manually per the ROADMAP Phase 1 test protocol.

---

## Task 1: ContainerExecutor interface and mock

**Files:**
- Create: `internal/hub/container.go`
- Create: `internal/hub/container_test.go`

**Step 1: Write failing test for executor interface**

Create `internal/hub/container_test.go`:

```go
package hub

import (
	"context"
	"errors"
	"testing"
)

// mockExecutor implements ContainerExecutor for testing.
type mockExecutor struct {
	healthy    bool
	execOutput string
	execErr    error
}

func (m *mockExecutor) IsHealthy(ctx context.Context, container string) bool {
	return m.healthy
}

func (m *mockExecutor) Exec(ctx context.Context, container string, args ...string) (string, error) {
	return m.execOutput, m.execErr
}

func TestContainerExecutorInterface(t *testing.T) {
	var exec ContainerExecutor = &mockExecutor{healthy: true}
	if !exec.IsHealthy(context.Background(), "test-container") {
		t.Fatal("expected healthy")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/hub/... -count=1 -run "TestContainerExecutor" -v`
Expected: FAIL — `ContainerExecutor` not defined.

**Step 3: Implement ContainerExecutor interface**

Create `internal/hub/container.go`:

```go
package hub

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ContainerExecutor abstracts docker exec operations for testability.
type ContainerExecutor interface {
	// IsHealthy returns true if the container is running.
	IsHealthy(ctx context.Context, container string) bool
	// Exec runs a command inside the container and returns stdout.
	Exec(ctx context.Context, container string, args ...string) (string, error)
}

// DockerExecutor implements ContainerExecutor via the docker CLI.
type DockerExecutor struct{}

// IsHealthy checks if a container is running via docker inspect.
func (d *DockerExecutor) IsHealthy(ctx context.Context, container string) bool {
	cmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", container)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// Exec runs a command inside a container via docker exec.
func (d *DockerExecutor) Exec(ctx context.Context, container string, args ...string) (string, error) {
	cmdArgs := append([]string{"exec", container}, args...)
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker exec %s: %w (stderr: %s)", container, err, stderr.String())
	}
	return stdout.String(), nil
}
```

**Step 4: Run tests**

Run: `go test ./internal/hub/... -count=1 -run "TestContainerExecutor" -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/hub/container.go internal/hub/container_test.go
git commit -m "feat(hub): add ContainerExecutor interface with Docker implementation"
```

---

## Task 2: Container health check endpoint

**Files:**
- Modify: `internal/web/handlers_hub.go`
- Modify: `internal/web/handlers_hub_test.go`
- Modify: `internal/web/server.go`

**Step 1: Add executor field to Server**

In `internal/web/server.go`, add to the `Server` struct:

```go
containerExec hub.ContainerExecutor
```

**Step 2: Write failing tests**

Add to `internal/web/handlers_hub_test.go`:

```go
type testExecutor struct {
	healthy    bool
	execOutput string
	execErr    error
}

func (e *testExecutor) IsHealthy(_ context.Context, _ string) bool {
	return e.healthy
}

func (e *testExecutor) Exec(_ context.Context, _ string, _ ...string) (string, error) {
	return e.execOutput, e.execErr
}

func TestTaskHealthCheckHealthy(t *testing.T) {
	srv := newTestServerWithHub(t)
	srv.containerExec = &testExecutor{healthy: true}

	task := &hub.Task{
		Project:     "api-service",
		Description: "Fix auth bug",
		Phase:       hub.PhaseExecute,
		Status:      hub.TaskStatusRunning,
	}
	if err := srv.hubTasks.Save(task); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Write projects.yaml with container field.
	hubDir := filepath.Dir(srv.hubProjects.FilePath())
	yaml := `projects:
  - name: api-service
    path: /home/user/code/api
    keywords: [api]
    container: sandbox-api
`
	if err := os.WriteFile(filepath.Join(hubDir, "projects.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write projects.yaml: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID+"/health", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"healthy":true`) {
		t.Fatalf("expected healthy:true, got: %s", rr.Body.String())
	}
}

func TestTaskHealthCheckNoContainer(t *testing.T) {
	srv := newTestServerWithHub(t)
	srv.containerExec = &testExecutor{healthy: false}

	task := &hub.Task{
		Project:     "api-service",
		Description: "Fix auth bug",
		Phase:       hub.PhaseExecute,
		Status:      hub.TaskStatusRunning,
	}
	if err := srv.hubTasks.Save(task); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// No projects.yaml — project has no container configured.
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID+"/health", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"healthy":false`) {
		t.Fatalf("expected healthy:false, got: %s", rr.Body.String())
	}
}
```

**Step 2b: Run tests to verify they fail**

Run: `go test ./internal/web/... -count=1 -run "TestTaskHealthCheck" -v`
Expected: FAIL — `/api/tasks/{id}/health` returns 404.

**Step 3: Add health sub-path to handleTaskByID and implement handler**

In `internal/web/handlers_hub.go`, add `"health"` case to the sub-path switch in `handleTaskByID`:

```go
case "health":
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	s.handleTaskHealth(w, taskID)
```

Add the handler:

```go
type taskHealthResponse struct {
	Healthy   bool   `json:"healthy"`
	Container string `json:"container,omitempty"`
	Message   string `json:"message,omitempty"`
}

// handleTaskHealth serves GET /api/tasks/{id}/health.
func (s *Server) handleTaskHealth(w http.ResponseWriter, taskID string) {
	if s.hubTasks == nil {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}

	task, err := s.hubTasks.Get(taskID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}

	container := s.containerForProject(task.Project)
	if container == "" {
		writeJSON(w, http.StatusOK, taskHealthResponse{
			Healthy: false,
			Message: "no container configured for project",
		})
		return
	}

	if s.containerExec == nil {
		writeJSON(w, http.StatusOK, taskHealthResponse{
			Healthy:   false,
			Container: container,
			Message:   "container executor not configured",
		})
		return
	}

	healthy := s.containerExec.IsHealthy(context.Background(), container)
	resp := taskHealthResponse{
		Healthy:   healthy,
		Container: container,
	}
	if !healthy {
		resp.Message = "container not running"
	}

	writeJSON(w, http.StatusOK, resp)
}

// containerForProject looks up the container name for a project from the registry.
func (s *Server) containerForProject(projectName string) string {
	if s.hubProjects == nil {
		return ""
	}
	projects, err := s.hubProjects.List()
	if err != nil {
		return ""
	}
	for _, p := range projects {
		if p.Name == projectName {
			return p.Container
		}
	}
	return ""
}
```

Add `"context"` to the import block in `handlers_hub.go`.

**Step 4: Run all tests**

Run: `go test ./internal/web/... -count=1 -v`
Expected: ALL PASS.

**Step 5: Commit**

```bash
git add internal/web/handlers_hub.go internal/web/handlers_hub_test.go internal/web/server.go
git commit -m "feat(hub): add GET /api/tasks/{id}/health for container health checks"
```

---

## Task 3: Session launcher — create tmux session in container

**Files:**
- Create: `internal/hub/session.go`
- Create: `internal/hub/session_test.go`

**Step 1: Write failing tests**

Create `internal/hub/session_test.go`:

```go
package hub

import (
	"context"
	"testing"
)

func TestLaunchSessionCreatesSession(t *testing.T) {
	exec := &mockExecutor{healthy: true, execOutput: ""}
	launcher := &SessionLauncher{Executor: exec}

	sessionName, err := launcher.Launch(context.Background(), "sandbox-api", "t-001")
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if sessionName == "" {
		t.Fatal("expected non-empty session name")
	}
}

func TestLaunchSessionUnhealthyContainer(t *testing.T) {
	exec := &mockExecutor{healthy: false}
	launcher := &SessionLauncher{Executor: exec}

	_, err := launcher.Launch(context.Background(), "sandbox-api", "t-001")
	if err == nil {
		t.Fatal("expected error for unhealthy container")
	}
}

func TestSendInputToSession(t *testing.T) {
	exec := &mockExecutor{healthy: true, execOutput: ""}
	launcher := &SessionLauncher{Executor: exec}

	err := launcher.SendInput(context.Background(), "sandbox-api", "agent-t-001", "Fix the auth bug")
	if err != nil {
		t.Fatalf("SendInput: %v", err)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/hub/... -count=1 -run "TestLaunchSession|TestSendInput" -v`
Expected: FAIL — `SessionLauncher` not defined.

**Step 3: Implement SessionLauncher**

Create `internal/hub/session.go`:

```go
package hub

import (
	"context"
	"fmt"
)

// SessionLauncher manages tmux sessions inside containers.
type SessionLauncher struct {
	Executor ContainerExecutor
}

// Launch creates a new tmux session inside a container and starts Claude Code.
// Returns the tmux session name (e.g. "agent-t-001").
func (l *SessionLauncher) Launch(ctx context.Context, container, taskID string) (string, error) {
	if !l.Executor.IsHealthy(ctx, container) {
		return "", fmt.Errorf("container %s is not running", container)
	}

	sessionName := "agent-" + taskID

	// Create tmux session with Claude Code.
	_, err := l.Executor.Exec(ctx, container,
		"tmux", "new-session", "-d", "-s", sessionName,
		"claude", "--dangerously-skip-permissions",
	)
	if err != nil {
		return "", fmt.Errorf("create tmux session: %w", err)
	}

	// Enable pipe-pane for streaming output to a log file.
	logFile := fmt.Sprintf("/tmp/%s.log", sessionName)
	_, err = l.Executor.Exec(ctx, container,
		"tmux", "pipe-pane", "-o", "-t", sessionName,
		fmt.Sprintf("cat >> %s", logFile),
	)
	if err != nil {
		return "", fmt.Errorf("configure pipe-pane: %w", err)
	}

	return sessionName, nil
}

// SendInput sends text to a tmux session via send-keys.
func (l *SessionLauncher) SendInput(ctx context.Context, container, sessionName, input string) error {
	_, err := l.Executor.Exec(ctx, container,
		"tmux", "send-keys", "-t", sessionName, input, "Enter",
	)
	if err != nil {
		return fmt.Errorf("send-keys to %s: %w", sessionName, err)
	}
	return nil
}
```

**Step 4: Run tests**

Run: `go test ./internal/hub/... -count=1 -run "TestLaunchSession|TestSendInput" -v`
Expected: ALL PASS.

**Step 5: Commit**

```bash
git add internal/hub/session.go internal/hub/session_test.go
git commit -m "feat(hub): add SessionLauncher for tmux sessions inside containers"
```

---

## Task 4: Wire task creation to session launch

**Files:**
- Modify: `internal/web/server.go`
- Modify: `internal/web/handlers_hub.go`
- Modify: `internal/web/handlers_hub_test.go`

**Step 1: Add SessionLauncher to Server**

In `internal/web/server.go`, add to the `Server` struct:

```go
sessionLauncher *hub.SessionLauncher
```

In `NewServer()`, after the container executor init, add:

```go
if s.containerExec != nil {
	s.sessionLauncher = &hub.SessionLauncher{Executor: s.containerExec}
}
```

**Step 2: Write failing test — task creation with container launch**

```go
func TestCreateTaskLaunchesSession(t *testing.T) {
	srv := newTestServerWithHub(t)
	exec := &testExecutor{healthy: true, execOutput: ""}
	srv.containerExec = exec
	srv.sessionLauncher = &hub.SessionLauncher{Executor: exec}

	// Write projects.yaml with container field.
	hubDir := filepath.Dir(srv.hubProjects.FilePath())
	yaml := `projects:
  - name: api-service
    path: /home/user/code/api
    keywords: [api]
    container: sandbox-api
`
	if err := os.WriteFile(filepath.Join(hubDir, "projects.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write projects.yaml: %v", err)
	}

	body := `{"project":"api-service","description":"Fix auth bug"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected %d, got %d: %s", http.StatusCreated, rr.Code, rr.Body.String())
	}

	var resp taskDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Task.TmuxSession == "" {
		t.Fatal("expected tmuxSession to be set when container is available")
	}
	if resp.Task.Status != hub.TaskStatusThinking {
		t.Fatalf("expected status thinking after launch, got %s", resp.Task.Status)
	}
}
```

**Step 3: Run test to verify it fails**

Run: `go test ./internal/web/... -count=1 -run "TestCreateTaskLaunches" -v`
Expected: FAIL — tmuxSession is empty because handleTasksCreate doesn't launch sessions yet.

**Step 4: Update handleTasksCreate to optionally launch a session**

After `if err := s.hubTasks.Save(task); err != nil { ... }` in `handleTasksCreate`, add:

```go
// Attempt to launch tmux session if container is configured.
if s.sessionLauncher != nil {
	container := s.containerForProject(task.Project)
	if container != "" {
		sessionName, launchErr := s.sessionLauncher.Launch(r.Context(), container, task.ID)
		if launchErr == nil {
			task.TmuxSession = sessionName
			task.Status = hub.TaskStatusThinking
			_ = s.hubTasks.Save(task) // Update with session info.
		}
	}
}
```

**Step 5: Run all tests**

Run: `go test ./internal/web/... -count=1 -v`
Expected: ALL PASS. Existing `TestCreateTask` still passes (no executor configured, so session launch is skipped).

**Step 6: Commit**

```bash
git add internal/web/server.go internal/web/handlers_hub.go internal/web/handlers_hub_test.go
git commit -m "feat(hub): wire task creation to container session launch"
```

---

## Task 5: Wire task input to container tmux

**Files:**
- Modify: `internal/web/handlers_hub.go`
- Modify: `internal/web/handlers_hub_test.go`

**Step 1: Write failing test**

```go
func TestTaskInputSendsToContainer(t *testing.T) {
	srv := newTestServerWithHub(t)
	exec := &testExecutor{healthy: true, execOutput: ""}
	srv.containerExec = exec
	srv.sessionLauncher = &hub.SessionLauncher{Executor: exec}

	// Write projects.yaml with container.
	hubDir := filepath.Dir(srv.hubProjects.FilePath())
	yaml := `projects:
  - name: api-service
    path: /home/user/code/api
    keywords: [api]
    container: sandbox-api
`
	if err := os.WriteFile(filepath.Join(hubDir, "projects.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write projects.yaml: %v", err)
	}

	task := &hub.Task{
		Project:     "api-service",
		Description: "Fix auth bug",
		Phase:       hub.PhaseExecute,
		Status:      hub.TaskStatusWaiting,
		TmuxSession: "agent-t-001",
	}
	if err := srv.hubTasks.Save(task); err != nil {
		t.Fatalf("Save: %v", err)
	}

	body := `{"input":"Use JWT tokens"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/input", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"delivered"`) {
		t.Fatalf("expected 'delivered' status, got: %s", rr.Body.String())
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/web/... -count=1 -run "TestTaskInputSendsToContainer" -v`
Expected: FAIL — stub returns "queued" not "delivered".

**Step 3: Update handleTaskInput to send via container when available**

Replace the TODO comment in `handleTaskInput` with:

```go
// Attempt to deliver input to container tmux session.
if s.sessionLauncher != nil && task.TmuxSession != "" {
	container := s.containerForProject(task.Project)
	if container != "" {
		if sendErr := s.sessionLauncher.SendInput(r.Context(), container, task.TmuxSession, req.Input); sendErr == nil {
			writeJSON(w, http.StatusOK, taskInputResponse{
				Status:  "delivered",
				Message: "input sent to session",
			})
			return
		}
	}
}

// Fallback: no container/session available.
writeJSON(w, http.StatusOK, taskInputResponse{
	Status:  "queued",
	Message: "input accepted (session not connected)",
})
```

Note: This requires reading the task to get `TmuxSession`. Move the `hubTasks.Get()` call up and store the result in a `task` variable (the current code uses `_` for the task — change it to `task`).

**Step 4: Run all tests**

Run: `go test ./internal/web/... -count=1 -v`
Expected: ALL PASS. Existing `TestTaskInputAccepted` still passes (no executor, returns "queued").

**Step 5: Commit**

```bash
git add internal/web/handlers_hub.go internal/web/handlers_hub_test.go
git commit -m "feat(hub): wire task input to container tmux via send-keys"
```

---

## Task 6: Terminal preview SSE endpoint

**Files:**
- Modify: `internal/web/handlers_hub.go`
- Modify: `internal/web/server.go`

**Step 1: Add preview sub-path to handleTaskByID**

In the sub-path switch in `handleTaskByID`, add:

```go
case "preview":
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	s.handleTaskPreview(w, r, taskID)
```

**Step 2: Implement handleTaskPreview**

```go
// handleTaskPreview serves GET /api/tasks/{id}/preview as an SSE stream.
// Streams tmux output from the container's pipe-pane log file.
func (s *Server) handleTaskPreview(w http.ResponseWriter, r *http.Request, taskID string) {
	if s.hubTasks == nil {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}

	task, err := s.hubTasks.Get(taskID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}

	if task.TmuxSession == "" || s.containerExec == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "no active session")
		return
	}

	container := s.containerForProject(task.Project)
	if container == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "no container configured")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Stream the last N lines and then follow.
	logFile := fmt.Sprintf("/tmp/%s.log", task.TmuxSession)
	ctx := r.Context()

	// Poll-based approach: read tail of log file periodically.
	// A future improvement could use docker exec tail -f with streaming stdout.
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastLen int
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			output, execErr := s.containerExec.Exec(ctx, container, "tail", "-n", "50", logFile)
			if execErr != nil {
				continue
			}
			if len(output) != lastLen {
				lastLen = len(output)
				if writeErr := writeSSEEvent(w, flusher, "preview", map[string]string{
					"taskId": taskID,
					"output": output,
				}); writeErr != nil {
					return
				}
			}
		}
	}
}
```

Add `"time"` to the import block if not already present.

**Step 3: Run all tests**

Run: `go test ./internal/web/... -count=1 -v`
Expected: ALL PASS (no test for SSE stream yet — manual integration test needed).

**Step 4: Commit**

```bash
git add internal/web/handlers_hub.go
git commit -m "feat(hub): add GET /api/tasks/{id}/preview SSE endpoint for terminal streaming"
```

---

## Summary of endpoints after Phase 4

| Endpoint | Method | Status |
|----------|--------|--------|
| `GET /api/tasks/{id}/health` | GET | **Phase 4** (new) |
| `GET /api/tasks/{id}/preview` | GET (SSE) | **Phase 4** (new) |
| `POST /api/tasks` | POST | Phase 2 (updated: now launches container session) |
| `POST /api/tasks/{id}/input` | POST | Phase 2 (updated: now sends to container tmux) |

Plus all previous endpoints unchanged.

---

## Manual integration test protocol

After deploying to a NUC with Docker containers:

1. Create a container with tmux: `docker run -d --name sandbox-api <image>`
2. Configure `projects.yaml` with container field pointing to `sandbox-api`
3. Create a task via `POST /api/tasks` — verify tmux session is created
4. Send input via `POST /api/tasks/{id}/input` — verify tmux receives it
5. Open `GET /api/tasks/{id}/preview` in browser — verify SSE output streams
6. Check health via `GET /api/tasks/{id}/health` — verify healthy status
7. Stop container, check health again — verify unhealthy status
8. Verify disconnecting SSE stream doesn't kill the tmux session
