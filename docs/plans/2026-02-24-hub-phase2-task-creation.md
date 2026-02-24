# Hub Phase 2: Task Creation & Input — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add write endpoints (POST/PATCH/DELETE) to the hub API and chat UI to the dashboard, enabling task creation, updates, input, and forking.

**Architecture:** Extend existing `handlers_hub.go` with method dispatch (GET/POST/PATCH/DELETE), add sub-path routing for `/api/tasks/{id}/input` and `/api/tasks/{id}/fork`. Frontend adds a "New Task" modal and chat input bar in the detail panel. All mutations trigger SSE notifications via existing `notifyTaskChanged()`.

**Tech Stack:** Go `net/http` handlers, filesystem JSON (`TaskStore`), vanilla JS (safe DOM construction pattern from Phase 1).

**Prerequisites:** Phase 1 (Dashboard MVP) merged. All existing tests passing.

---

## Task 1: Add request/response types for mutations

**Files:**
- Modify: `internal/web/handlers_hub.go`

**Step 1: Add types at the bottom of handlers_hub.go (after existing response types)**

```go
type createTaskRequest struct {
	Project     string `json:"project"`
	Description string `json:"description"`
	Phase       string `json:"phase,omitempty"`
	Branch      string `json:"branch,omitempty"`
}

type updateTaskRequest struct {
	Description *string `json:"description,omitempty"`
	Phase       *string `json:"phase,omitempty"`
	Status      *string `json:"status,omitempty"`
	Branch      *string `json:"branch,omitempty"`
}

type taskInputRequest struct {
	Input string `json:"input"`
}

type taskInputResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}
```

**Step 2: Run existing tests to verify no breakage**

Run: `go test ./internal/web/... -count=1 -v`
Expected: All existing tests PASS (types are unused so far).

**Step 3: Commit**

```bash
git add internal/web/handlers_hub.go
git commit -m "feat(hub): add request/response types for task mutation endpoints"
```

---

## Task 2: POST /api/tasks — create task

**Files:**
- Modify: `internal/web/handlers_hub.go`
- Modify: `internal/web/handlers_hub_test.go`

**Step 1: Write failing tests**

Add to `internal/web/handlers_hub_test.go` (add `"encoding/json"` to imports):

```go
func TestCreateTask(t *testing.T) {
	srv := newTestServerWithHub(t)

	body := `{"project":"web-app","description":"Add dark mode"}`
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
	if resp.Task.Project != "web-app" {
		t.Fatalf("expected project web-app, got %s", resp.Task.Project)
	}
	if resp.Task.Description != "Add dark mode" {
		t.Fatalf("expected description 'Add dark mode', got %s", resp.Task.Description)
	}
	if resp.Task.ID == "" {
		t.Fatal("expected auto-generated ID")
	}
	if resp.Task.Status != hub.TaskStatusIdle {
		t.Fatalf("expected status idle, got %s", resp.Task.Status)
	}
	if resp.Task.Phase != hub.PhaseExecute {
		t.Fatalf("expected default phase execute, got %s", resp.Task.Phase)
	}
}

func TestCreateTaskWithPhase(t *testing.T) {
	srv := newTestServerWithHub(t)

	body := `{"project":"web-app","description":"Research auth options","phase":"brainstorm"}`
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
	if resp.Task.Phase != hub.PhaseBrainstorm {
		t.Fatalf("expected phase brainstorm, got %s", resp.Task.Phase)
	}
}

func TestCreateTaskMissingProject(t *testing.T) {
	srv := newTestServerWithHub(t)

	body := `{"description":"Add dark mode"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d: %s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
}

func TestCreateTaskMissingDescription(t *testing.T) {
	srv := newTestServerWithHub(t)

	body := `{"project":"web-app"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d: %s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
}

func TestCreateTaskInvalidJSON(t *testing.T) {
	srv := newTestServerWithHub(t)

	req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d: %s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
}
```

**Step 2: Run new tests to verify they fail**

Run: `go test ./internal/web/... -count=1 -run TestCreateTask -v`
Expected: FAIL — POST currently returns 405 (method not allowed).

**Step 3: Refactor handleTasks to dispatch GET/POST and implement handleTasksCreate**

Replace the existing `handleTasks` function in `internal/web/handlers_hub.go` with:

```go
// handleTasks dispatches GET /api/tasks and POST /api/tasks.
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleTasksList(w, r)
	case http.MethodPost:
		s.handleTasksCreate(w, r)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

// handleTasksList serves GET /api/tasks with optional ?status= and ?project= filters.
func (s *Server) handleTasksList(w http.ResponseWriter, r *http.Request) {
	if s.hubTasks == nil {
		writeJSON(w, http.StatusOK, tasksListResponse{Tasks: []*hub.Task{}})
		return
	}

	tasks, err := s.hubTasks.List()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load tasks")
		return
	}

	statusFilter := r.URL.Query().Get("status")
	projectFilter := r.URL.Query().Get("project")

	if statusFilter != "" || projectFilter != "" {
		filtered := make([]*hub.Task, 0, len(tasks))
		for _, t := range tasks {
			if statusFilter != "" && string(t.Status) != statusFilter {
				continue
			}
			if projectFilter != "" && t.Project != projectFilter {
				continue
			}
			filtered = append(filtered, t)
		}
		tasks = filtered
	}

	if tasks == nil {
		tasks = []*hub.Task{}
	}

	writeJSON(w, http.StatusOK, tasksListResponse{Tasks: tasks})
}

// handleTasksCreate serves POST /api/tasks.
func (s *Server) handleTasksCreate(w http.ResponseWriter, r *http.Request) {
	if s.hubTasks == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "hub not initialized")
		return
	}

	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body")
		return
	}

	if req.Project == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "project is required")
		return
	}
	if req.Description == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "description is required")
		return
	}

	phase := hub.PhaseExecute
	if req.Phase != "" {
		phase = hub.Phase(req.Phase)
	}

	task := &hub.Task{
		Project:     req.Project,
		Description: req.Description,
		Phase:       phase,
		Branch:      req.Branch,
		Status:      hub.TaskStatusIdle,
	}

	if err := s.hubTasks.Save(task); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create task")
		return
	}

	s.notifyTaskChanged()
	writeJSON(w, http.StatusCreated, taskDetailResponse{Task: task})
}
```

Add `"encoding/json"` to the import block in `handlers_hub.go`.

**Step 4: Update TestTasksEndpointMethodNotAllowed to use PUT**

POST is now a valid method, so update the test to use a method that's still not allowed:

```go
func TestTasksEndpointMethodNotAllowed(t *testing.T) {
	srv := newTestServerWithHub(t)

	req := httptest.NewRequest(http.MethodPut, "/api/tasks", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
}
```

**Step 5: Run all tests**

Run: `go test ./internal/web/... -count=1 -v`
Expected: ALL PASS (existing + new).

**Step 6: Commit**

```bash
git add internal/web/handlers_hub.go internal/web/handlers_hub_test.go
git commit -m "feat(hub): add POST /api/tasks endpoint for task creation"
```

---

## Task 3: Refactor handleTaskByID for method dispatch + sub-paths

**Files:**
- Modify: `internal/web/handlers_hub.go`

**Step 1: Refactor handleTaskByID to parse sub-paths**

Replace the existing `handleTaskByID` function:

```go
// handleTaskByID dispatches /api/tasks/{id}, /api/tasks/{id}/input, /api/tasks/{id}/fork.
func (s *Server) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	const prefix = "/api/tasks/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "route not found")
		return
	}

	remaining := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.SplitN(remaining, "/", 2)
	taskID := parts[0]
	subPath := ""
	if len(parts) > 1 {
		subPath = parts[1]
	}

	if taskID == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "task id is required")
		return
	}

	switch subPath {
	case "":
		switch r.Method {
		case http.MethodGet:
			s.handleTaskGet(w, taskID)
		case http.MethodPatch:
			s.handleTaskUpdate(w, r, taskID)
		case http.MethodDelete:
			s.handleTaskDelete(w, taskID)
		default:
			writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	case "input":
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		s.handleTaskInput(w, r, taskID)
	case "fork":
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		s.handleTaskFork(w, r, taskID)
	default:
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "route not found")
	}
}

// handleTaskGet serves GET /api/tasks/{id}.
func (s *Server) handleTaskGet(w http.ResponseWriter, taskID string) {
	if s.hubTasks == nil {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}

	task, err := s.hubTasks.Get(taskID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}

	writeJSON(w, http.StatusOK, taskDetailResponse{Task: task})
}
```

Add stub handlers so the code compiles (these will be filled in subsequent tasks):

```go
func (s *Server) handleTaskUpdate(w http.ResponseWriter, r *http.Request, taskID string) {
	writeAPIError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "not implemented")
}

func (s *Server) handleTaskDelete(w http.ResponseWriter, taskID string) {
	writeAPIError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "not implemented")
}

func (s *Server) handleTaskInput(w http.ResponseWriter, r *http.Request, taskID string) {
	writeAPIError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "not implemented")
}

func (s *Server) handleTaskFork(w http.ResponseWriter, r *http.Request, taskID string) {
	writeAPIError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "not implemented")
}
```

**Step 2: Run all existing tests to verify no regression**

Run: `go test ./internal/web/... -count=1 -v`
Expected: ALL PASS. The GET-based tests still work because the refactored handleTaskByID routes GET to handleTaskGet which has the same logic.

**Step 3: Commit**

```bash
git add internal/web/handlers_hub.go
git commit -m "refactor(hub): restructure handleTaskByID for method dispatch and sub-paths"
```

---

## Task 4: PATCH /api/tasks/{id} — update task

**Files:**
- Modify: `internal/web/handlers_hub.go`
- Modify: `internal/web/handlers_hub_test.go`

**Step 1: Write failing tests**

Add to `internal/web/handlers_hub_test.go`:

```go
func TestUpdateTaskPhase(t *testing.T) {
	srv := newTestServerWithHub(t)

	task := &hub.Task{
		Project:     "api-service",
		Description: "Fix auth bug",
		Phase:       hub.PhaseExecute,
		Status:      hub.TaskStatusRunning,
	}
	if err := srv.hubTasks.Save(task); err != nil {
		t.Fatalf("Save: %v", err)
	}

	body := `{"phase":"review"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+task.ID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp taskDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Task.Phase != hub.PhaseReview {
		t.Fatalf("expected phase review, got %s", resp.Task.Phase)
	}
	if resp.Task.Description != "Fix auth bug" {
		t.Fatalf("description should be unchanged, got %s", resp.Task.Description)
	}
}

func TestUpdateTaskStatus(t *testing.T) {
	srv := newTestServerWithHub(t)

	task := &hub.Task{
		Project: "api-service",
		Description: "Fix auth bug",
		Phase:   hub.PhaseExecute,
		Status:  hub.TaskStatusRunning,
	}
	if err := srv.hubTasks.Save(task); err != nil {
		t.Fatalf("Save: %v", err)
	}

	body := `{"status":"complete"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+task.ID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp taskDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Task.Status != hub.TaskStatusComplete {
		t.Fatalf("expected status complete, got %s", resp.Task.Status)
	}
}

func TestUpdateTaskNotFound(t *testing.T) {
	srv := newTestServerWithHub(t)

	body := `{"phase":"review"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/tasks/t-nonexistent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected %d, got %d", http.StatusNotFound, rr.Code)
	}
}
```

**Step 2: Run new tests to verify they fail**

Run: `go test ./internal/web/... -count=1 -run "TestUpdateTask" -v`
Expected: FAIL — stub returns 501.

**Step 3: Implement handleTaskUpdate**

Replace the stub in `internal/web/handlers_hub.go`:

```go
// handleTaskUpdate serves PATCH /api/tasks/{id}.
func (s *Server) handleTaskUpdate(w http.ResponseWriter, r *http.Request, taskID string) {
	if s.hubTasks == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "hub not initialized")
		return
	}

	task, err := s.hubTasks.Get(taskID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}

	var req updateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body")
		return
	}

	if req.Description != nil {
		task.Description = *req.Description
	}
	if req.Phase != nil {
		task.Phase = hub.Phase(*req.Phase)
	}
	if req.Status != nil {
		task.Status = hub.TaskStatus(*req.Status)
	}
	if req.Branch != nil {
		task.Branch = *req.Branch
	}

	if err := s.hubTasks.Save(task); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update task")
		return
	}

	s.notifyTaskChanged()
	writeJSON(w, http.StatusOK, taskDetailResponse{Task: task})
}
```

**Step 4: Run all tests**

Run: `go test ./internal/web/... -count=1 -v`
Expected: ALL PASS.

**Step 5: Commit**

```bash
git add internal/web/handlers_hub.go internal/web/handlers_hub_test.go
git commit -m "feat(hub): add PATCH /api/tasks/{id} for task updates"
```

---

## Task 5: DELETE /api/tasks/{id}

**Files:**
- Modify: `internal/web/handlers_hub.go`
- Modify: `internal/web/handlers_hub_test.go`

**Step 1: Write failing tests**

```go
func TestDeleteTask(t *testing.T) {
	srv := newTestServerWithHub(t)

	task := &hub.Task{
		Project:     "api-service",
		Description: "Fix auth bug",
		Phase:       hub.PhaseExecute,
		Status:      hub.TaskStatusComplete,
	}
	if err := srv.hubTasks.Save(task); err != nil {
		t.Fatalf("Save: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/tasks/"+task.ID, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d: %s", http.StatusNoContent, rr.Code, rr.Body.String())
	}

	// Verify task is gone.
	getReq := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID, nil)
	getRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusNotFound {
		t.Fatalf("expected deleted task to return 404, got %d", getRR.Code)
	}
}

func TestDeleteTaskNotFound(t *testing.T) {
	srv := newTestServerWithHub(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/tasks/t-nonexistent", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected %d, got %d", http.StatusNotFound, rr.Code)
	}
}
```

**Step 2: Run new tests to verify they fail**

Run: `go test ./internal/web/... -count=1 -run "TestDeleteTask" -v`
Expected: FAIL — stub returns 501.

**Step 3: Implement handleTaskDelete**

Replace the stub:

```go
// handleTaskDelete serves DELETE /api/tasks/{id}.
func (s *Server) handleTaskDelete(w http.ResponseWriter, taskID string) {
	if s.hubTasks == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "hub not initialized")
		return
	}

	if err := s.hubTasks.Delete(taskID); err != nil {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}

	s.notifyTaskChanged()
	w.WriteHeader(http.StatusNoContent)
}
```

**Step 4: Run all tests**

Run: `go test ./internal/web/... -count=1 -v`
Expected: ALL PASS.

**Step 5: Commit**

```bash
git add internal/web/handlers_hub.go internal/web/handlers_hub_test.go
git commit -m "feat(hub): add DELETE /api/tasks/{id} endpoint"
```

---

## Task 6: POST /api/tasks/{id}/input — stub

**Files:**
- Modify: `internal/web/handlers_hub.go`
- Modify: `internal/web/handlers_hub_test.go`

**Step 1: Write failing tests**

```go
func TestTaskInputAccepted(t *testing.T) {
	srv := newTestServerWithHub(t)

	task := &hub.Task{
		Project:     "api-service",
		Description: "Fix auth bug",
		Phase:       hub.PhaseExecute,
		Status:      hub.TaskStatusWaiting,
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

	if !strings.Contains(rr.Body.String(), `"status"`) {
		t.Fatalf("expected status in response, got: %s", rr.Body.String())
	}
}

func TestTaskInputNotFound(t *testing.T) {
	srv := newTestServerWithHub(t)

	body := `{"input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/t-nonexistent/input", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected %d, got %d", http.StatusNotFound, rr.Code)
	}
}

func TestTaskInputEmptyInput(t *testing.T) {
	srv := newTestServerWithHub(t)

	task := &hub.Task{
		Project:     "api-service",
		Description: "Fix auth bug",
		Phase:       hub.PhaseExecute,
		Status:      hub.TaskStatusWaiting,
	}
	if err := srv.hubTasks.Save(task); err != nil {
		t.Fatalf("Save: %v", err)
	}

	body := `{"input":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/input", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d: %s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
}
```

**Step 2: Run new tests to verify they fail**

Run: `go test ./internal/web/... -count=1 -run "TestTaskInput" -v`
Expected: FAIL — stub returns 501.

**Step 3: Implement handleTaskInput**

Replace the stub:

```go
// handleTaskInput serves POST /api/tasks/{id}/input.
// Stub: accepts input, returns queued status. Phase 4 will wire this to docker exec tmux send-keys.
func (s *Server) handleTaskInput(w http.ResponseWriter, r *http.Request, taskID string) {
	if s.hubTasks == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "hub not initialized")
		return
	}

	if _, err := s.hubTasks.Get(taskID); err != nil {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}

	var req taskInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body")
		return
	}

	if req.Input == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "input is required")
		return
	}

	// TODO: Phase 4 — send input to container tmux session via docker exec.
	writeJSON(w, http.StatusOK, taskInputResponse{
		Status:  "queued",
		Message: "input accepted (session not connected)",
	})
}
```

**Step 4: Run all tests**

Run: `go test ./internal/web/... -count=1 -v`
Expected: ALL PASS.

**Step 5: Commit**

```bash
git add internal/web/handlers_hub.go internal/web/handlers_hub_test.go
git commit -m "feat(hub): add POST /api/tasks/{id}/input endpoint (stub)"
```

---

## Task 7: POST /api/tasks/{id}/fork — create child task

**Files:**
- Modify: `internal/web/handlers_hub.go`
- Modify: `internal/web/handlers_hub_test.go`

**Step 1: Write failing tests**

```go
func TestForkTask(t *testing.T) {
	srv := newTestServerWithHub(t)

	parent := &hub.Task{
		Project:     "api-service",
		Description: "Fix auth bug",
		Phase:       hub.PhaseExecute,
		Status:      hub.TaskStatusRunning,
		Branch:      "feat/auth",
	}
	if err := srv.hubTasks.Save(parent); err != nil {
		t.Fatalf("Save: %v", err)
	}

	body := `{"description":"Try JWT approach"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+parent.ID+"/fork", strings.NewReader(body))
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
	if resp.Task.ParentTaskID != parent.ID {
		t.Fatalf("expected parentTaskId %s, got %s", parent.ID, resp.Task.ParentTaskID)
	}
	if resp.Task.Project != parent.Project {
		t.Fatalf("expected project %s inherited from parent, got %s", parent.Project, resp.Task.Project)
	}
	if resp.Task.Description != "Try JWT approach" {
		t.Fatalf("expected description 'Try JWT approach', got %s", resp.Task.Description)
	}
	if resp.Task.ID == parent.ID {
		t.Fatal("child should have a different ID than parent")
	}
}

func TestForkTaskDefaultDescription(t *testing.T) {
	srv := newTestServerWithHub(t)

	parent := &hub.Task{
		Project:     "api-service",
		Description: "Fix auth bug",
		Phase:       hub.PhaseExecute,
		Status:      hub.TaskStatusRunning,
	}
	if err := srv.hubTasks.Save(parent); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Empty body — should use default description.
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+parent.ID+"/fork", strings.NewReader("{}"))
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
	if resp.Task.Description != "Fix auth bug (fork)" {
		t.Fatalf("expected default fork description, got %s", resp.Task.Description)
	}
}

func TestForkTaskNotFound(t *testing.T) {
	srv := newTestServerWithHub(t)

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/t-nonexistent/fork", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected %d, got %d", http.StatusNotFound, rr.Code)
	}
}
```

**Step 2: Run new tests to verify they fail**

Run: `go test ./internal/web/... -count=1 -run "TestForkTask" -v`
Expected: FAIL — stub returns 501.

**Step 3: Implement handleTaskFork**

Replace the stub:

```go
// handleTaskFork serves POST /api/tasks/{id}/fork.
func (s *Server) handleTaskFork(w http.ResponseWriter, r *http.Request, taskID string) {
	if s.hubTasks == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "hub not initialized")
		return
	}

	parent, err := s.hubTasks.Get(taskID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "parent task not found")
		return
	}

	var req createTaskRequest
	if decodeErr := json.NewDecoder(r.Body).Decode(&req); decodeErr != nil {
		req = createTaskRequest{}
	}

	description := req.Description
	if description == "" {
		description = parent.Description + " (fork)"
	}

	child := &hub.Task{
		Project:      parent.Project,
		Description:  description,
		Phase:        parent.Phase,
		Branch:       parent.Branch,
		Status:       hub.TaskStatusIdle,
		ParentTaskID: parent.ID,
	}

	if err := s.hubTasks.Save(child); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create fork")
		return
	}

	s.notifyTaskChanged()
	writeJSON(w, http.StatusCreated, taskDetailResponse{Task: child})
}
```

**Step 4: Run all tests**

Run: `go test ./internal/web/... -count=1 -v`
Expected: ALL PASS.

**Step 5: Commit**

```bash
git add internal/web/handlers_hub.go internal/web/handlers_hub_test.go
git commit -m "feat(hub): add POST /api/tasks/{id}/fork endpoint"
```

---

## Task 8: Frontend — New Task button and modal

**Files:**
- Modify: `internal/web/static/dashboard.html`
- Modify: `internal/web/static/dashboard.css`
- Modify: `internal/web/static/dashboard.js`

**Step 1: Add New Task button to toolbar and modal HTML**

In `dashboard.html`, add the button inside `.hub-filters` div:

```html
<div class="hub-filters">
  <select id="filter-status" class="hub-select" aria-label="Filter by status">
    <!-- existing options -->
  </select>
  <select id="filter-project" class="hub-select" aria-label="Filter by project">
    <option value="">All projects</option>
  </select>
  <button id="new-task-btn" class="hub-btn-primary" type="button">+ New Task</button>
</div>
```

Add the modal before the closing `</div>` of `.app`:

```html
<!-- New Task modal -->
<div class="modal-backdrop" id="new-task-backdrop"></div>
<div class="modal" id="new-task-modal" role="dialog" aria-label="Create new task" aria-hidden="true">
  <div class="modal-header">
    <span class="modal-title">New Task</span>
    <button class="modal-close" id="new-task-close" type="button" aria-label="Close">&times;</button>
  </div>
  <div class="modal-body">
    <label class="modal-label" for="new-task-project">Project</label>
    <select id="new-task-project" class="hub-select modal-field"></select>
    <label class="modal-label" for="new-task-desc">Description</label>
    <textarea id="new-task-desc" class="modal-textarea" rows="3" placeholder="What should the agent do?"></textarea>
    <label class="modal-label" for="new-task-phase">Phase</label>
    <select id="new-task-phase" class="hub-select modal-field">
      <option value="execute">Execute</option>
      <option value="brainstorm">Brainstorm</option>
      <option value="plan">Plan</option>
      <option value="review">Review</option>
    </select>
  </div>
  <div class="modal-footer">
    <button id="new-task-cancel" class="hub-btn" type="button">Cancel</button>
    <button id="new-task-submit" class="hub-btn-primary" type="button">Create</button>
  </div>
</div>
```

**Step 2: Add modal CSS**

Append to `dashboard.css`:

```css
/* ── Primary button ────────────────────────────────────────────── */

.hub-btn-primary {
  height: 36px;
  padding: 0 14px;
  border: 1px solid var(--accent);
  border-radius: 8px;
  background: var(--accent);
  color: #fff;
  font: inherit;
  font-size: 0.88rem;
  font-weight: 500;
  cursor: pointer;
  white-space: nowrap;
}

.hub-btn-primary:hover {
  background: #0e6b63;
}

.hub-btn {
  height: 36px;
  padding: 0 14px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: transparent;
  color: var(--text);
  font: inherit;
  font-size: 0.88rem;
  cursor: pointer;
}

.hub-btn:hover {
  background: var(--bg);
}

/* ── Modal ──────────────────────────────────────────────────────── */

.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 60;
  background: rgba(15, 23, 42, 0.5);
  opacity: 0;
  pointer-events: none;
  transition: opacity 200ms;
}

.modal-backdrop.open {
  opacity: 1;
  pointer-events: auto;
}

.modal {
  position: fixed;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%) scale(0.95);
  z-index: 65;
  width: min(440px, calc(100vw - 32px));
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
  box-shadow: 0 8px 32px rgba(15, 23, 42, 0.2);
  opacity: 0;
  pointer-events: none;
  transition: opacity 200ms, transform 200ms;
}

.modal.open {
  opacity: 1;
  pointer-events: auto;
  transform: translate(-50%, -50%) scale(1);
}

.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 16px;
  border-bottom: 1px solid var(--border);
}

.modal-title {
  font-size: 1rem;
  font-weight: 600;
}

.modal-close {
  background: none;
  border: none;
  font-size: 1.3rem;
  color: var(--muted);
  cursor: pointer;
  line-height: 1;
  padding: 0 4px;
}

.modal-close:hover {
  color: var(--text);
}

.modal-body {
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.modal-label {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--muted);
}

.modal-field {
  width: 100%;
}

.modal-textarea {
  width: 100%;
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 8px 10px;
  font: inherit;
  font-size: 0.9rem;
  color: var(--text);
  background: #fff;
  resize: vertical;
}

.modal-textarea:focus {
  outline: 2px solid rgba(15, 118, 110, 0.2);
  border-color: var(--accent);
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px 16px;
  border-top: 1px solid var(--border);
}
```

**Step 3: Add modal JS logic**

Add DOM references at the top of `dashboard.js` (inside the IIFE, after existing references):

```javascript
var newTaskBtn = document.getElementById("new-task-btn")
var newTaskModal = document.getElementById("new-task-modal")
var newTaskBackdrop = document.getElementById("new-task-backdrop")
var newTaskClose = document.getElementById("new-task-close")
var newTaskCancel = document.getElementById("new-task-cancel")
var newTaskSubmit = document.getElementById("new-task-submit")
var newTaskProject = document.getElementById("new-task-project")
var newTaskDesc = document.getElementById("new-task-desc")
var newTaskPhase = document.getElementById("new-task-phase")
```

Add modal functions (before the `// ── Event listeners` section):

```javascript
// ── New Task modal ───────────────────────────────────────────────
function openNewTaskModal() {
  // Populate project selector from loaded projects.
  clearChildren(newTaskProject)
  for (var i = 0; i < state.projects.length; i++) {
    var opt = document.createElement("option")
    opt.value = state.projects[i].name
    opt.textContent = state.projects[i].name
    newTaskProject.appendChild(opt)
  }
  if (newTaskDesc) newTaskDesc.value = ""
  if (newTaskPhase) newTaskPhase.value = "execute"

  if (newTaskModal) newTaskModal.classList.add("open")
  if (newTaskBackdrop) newTaskBackdrop.classList.add("open")
  if (newTaskModal) newTaskModal.setAttribute("aria-hidden", "false")
  if (newTaskDesc) newTaskDesc.focus()
}

function closeNewTaskModal() {
  if (newTaskModal) newTaskModal.classList.remove("open")
  if (newTaskBackdrop) newTaskBackdrop.classList.remove("open")
  if (newTaskModal) newTaskModal.setAttribute("aria-hidden", "true")
}

function submitNewTask() {
  var project = newTaskProject ? newTaskProject.value : ""
  var description = newTaskDesc ? newTaskDesc.value.trim() : ""
  var phase = newTaskPhase ? newTaskPhase.value : "execute"

  if (!project || !description) return

  var body = JSON.stringify({ project: project, description: description, phase: phase })
  var headers = authHeaders()
  headers["Content-Type"] = "application/json"

  fetch(apiPathWithToken("/api/tasks"), {
    method: "POST",
    headers: headers,
    body: body,
  })
    .then(function (r) {
      if (!r.ok) throw new Error("create failed: " + r.status)
      return r.json()
    })
    .then(function (data) {
      closeNewTaskModal()
      fetchTasks()
      if (data.task && data.task.id) openDetail(data.task.id)
    })
    .catch(function (err) {
      console.error("submitNewTask:", err)
    })
}
```

Add event listeners (in the event listeners section):

```javascript
if (newTaskBtn) {
  newTaskBtn.addEventListener("click", openNewTaskModal)
}
if (newTaskClose) {
  newTaskClose.addEventListener("click", closeNewTaskModal)
}
if (newTaskCancel) {
  newTaskCancel.addEventListener("click", closeNewTaskModal)
}
if (newTaskBackdrop) {
  newTaskBackdrop.addEventListener("click", closeNewTaskModal)
}
if (newTaskSubmit) {
  newTaskSubmit.addEventListener("click", submitNewTask)
}
```

**Step 4: Verify by running Go tests (static files embed correctly)**

Run: `go test ./internal/web/... -count=1 -v`
Expected: ALL PASS (HTML/CSS/JS changes don't break Go tests).

**Step 5: Commit**

```bash
git add internal/web/static/dashboard.html internal/web/static/dashboard.css internal/web/static/dashboard.js
git commit -m "feat(hub): add New Task button and creation modal to dashboard"
```

---

## Task 9: Frontend — Chat input in detail panel

**Files:**
- Modify: `internal/web/static/dashboard.html`
- Modify: `internal/web/static/dashboard.js`

**Step 1: Add chat input HTML to detail panel**

In `dashboard.html`, add the chat bar inside the `<aside class="detail-panel">`, after `detail-body` and before the closing `</aside>`:

```html
<div class="detail-chat" id="detail-chat">
  <input type="text" class="detail-chat-input" id="detail-chat-input" placeholder="Send message..." aria-label="Send message to task" />
  <button class="detail-chat-send" id="detail-chat-send" type="button">Send</button>
</div>
```

Note: CSS for `.detail-chat`, `.detail-chat-input`, and `.detail-chat-send` already exists in `dashboard.css` (lines 397-439).

**Step 2: Add chat input JS**

Add DOM references at the top of `dashboard.js`:

```javascript
var chatInput = document.getElementById("detail-chat-input")
var chatSend = document.getElementById("detail-chat-send")
```

Add send function (before the event listeners section):

```javascript
// ── Chat input ──────────────────────────────────────────────────
function sendChatInput() {
  if (!state.selectedTaskId || !chatInput) return
  var input = chatInput.value.trim()
  if (!input) return

  var headers = authHeaders()
  headers["Content-Type"] = "application/json"

  fetch(apiPathWithToken("/api/tasks/" + state.selectedTaskId + "/input"), {
    method: "POST",
    headers: headers,
    body: JSON.stringify({ input: input }),
  })
    .then(function (r) {
      if (!r.ok) throw new Error("send failed: " + r.status)
      chatInput.value = ""
    })
    .catch(function (err) {
      console.error("sendChatInput:", err)
    })
}
```

Add event listeners:

```javascript
if (chatSend) {
  chatSend.addEventListener("click", sendChatInput)
}
if (chatInput) {
  chatInput.addEventListener("keydown", function (e) {
    if (e.key === "Enter") {
      e.preventDefault()
      sendChatInput()
    }
  })
}
```

**Step 3: Run Go tests**

Run: `go test ./internal/web/... -count=1 -v`
Expected: ALL PASS.

**Step 4: Commit**

```bash
git add internal/web/static/dashboard.html internal/web/static/dashboard.js
git commit -m "feat(hub): add chat input bar to task detail panel"
```

---

## Summary of endpoints after Phase 2

| Endpoint | Method | Status |
|----------|--------|--------|
| `GET /api/tasks` | GET | Phase 1 (existing) |
| `POST /api/tasks` | POST | **Phase 2** (new) |
| `GET /api/tasks/{id}` | GET | Phase 1 (existing) |
| `PATCH /api/tasks/{id}` | PATCH | **Phase 2** (new) |
| `DELETE /api/tasks/{id}` | DELETE | **Phase 2** (new) |
| `POST /api/tasks/{id}/input` | POST | **Phase 2** (stub — wired in Phase 4) |
| `POST /api/tasks/{id}/fork` | POST | **Phase 2** (new) |
| `GET /api/projects` | GET | Phase 1 (existing) |
