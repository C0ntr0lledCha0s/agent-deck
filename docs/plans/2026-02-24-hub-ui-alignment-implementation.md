# Hub UI Alignment Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Align the Hub web UI to the approved design documents by implementing three parallel tracks: data model refactor, agents view redesign, and context-aware chat input.

**Architecture:** Three parallel tracks merged in order. Track A refactors the Go backend data model (separating TaskStatus from AgentStatus, adding Session/DiffInfo models, adding migration). Track B rewrites the frontend (dark theme, two-panel layout, sidebar, embedded xterm.js). Track C adds a context-aware chat input component. Tracks A and B can be worked in parallel; Track C depends on Track B's layout.

**Tech Stack:** Go 1.21+ (backend), vanilla JS/CSS/HTML (frontend), xterm.js 5.5.0 (terminal embedding)

---

## Track A: Data Model Refactor

### Task A1: Add AgentStatus type and DiffInfo/Session models

**Files:**
- Modify: `internal/hub/models.go`
- Test: `internal/hub/store_test.go`

**Step 1: Write the failing test for new model fields**

Add a test in `store_test.go` that creates a Task with the new fields and round-trips it through Save/Get:

```go
func TestSaveAndGetNewFields(t *testing.T) {
	store := newTestStore(t)

	task := &Task{
		Project:     "web-app",
		Description: "Test new fields",
		Phase:       PhaseExecute,
		Status:      TaskStatusRunning,
		AgentStatus: AgentStatusThinking,
		Skills:      []string{"git", "docker"},
		MCPs:        []string{"filesystem"},
		Diff:        &DiffInfo{Files: 3, Add: 42, Del: 7},
		Container:   "sandbox-web",
		AskQuestion: "Which auth method?",
		Sessions: []Session{
			{ID: "s-1", Phase: PhasePlan, Status: "complete", Duration: "5m", Summary: "Planned approach"},
			{ID: "s-2", Phase: PhaseExecute, Status: "active", Duration: "12m"},
		},
	}

	if err := store.Save(task); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AgentStatus != AgentStatusThinking {
		t.Fatalf("expected agentStatus thinking, got %s", got.AgentStatus)
	}
	if len(got.Skills) != 2 || got.Skills[0] != "git" {
		t.Fatalf("expected skills [git docker], got %v", got.Skills)
	}
	if got.Diff == nil || got.Diff.Files != 3 {
		t.Fatalf("expected diff with 3 files, got %v", got.Diff)
	}
	if got.Container != "sandbox-web" {
		t.Fatalf("expected container sandbox-web, got %s", got.Container)
	}
	if got.AskQuestion != "Which auth method?" {
		t.Fatalf("expected askQuestion, got %s", got.AskQuestion)
	}
	if len(got.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(got.Sessions))
	}
	if got.Sessions[0].Summary != "Planned approach" {
		t.Fatalf("expected session summary, got %s", got.Sessions[0].Summary)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/hub/ -run TestSaveAndGetNewFields -v`
Expected: FAIL — `AgentStatus`, `AgentStatusThinking`, `DiffInfo`, `Session` undefined

**Step 3: Add new types to models.go**

Add `AgentStatus` type, `DiffInfo` struct, `Session` struct, and new fields to `Task` struct in `internal/hub/models.go`:

```go
// AgentStatus represents what Claude is doing right now.
type AgentStatus string

const (
	AgentStatusThinking AgentStatus = "thinking"
	AgentStatusWaiting  AgentStatus = "waiting"
	AgentStatusRunning  AgentStatus = "running"
	AgentStatusIdle     AgentStatus = "idle"
	AgentStatusError    AgentStatus = "error"
	AgentStatusComplete AgentStatus = "complete"
)

// DiffInfo tracks file change statistics for a task.
type DiffInfo struct {
	Files int `json:"files"`
	Add   int `json:"add"`
	Del   int `json:"del"`
}

// Session represents one phase-session within a task's lifecycle.
type Session struct {
	ID              string `json:"id"`
	Phase           Phase  `json:"phase"`
	Status          string `json:"status"` // "active" | "complete"
	Duration        string `json:"duration"`
	Artifact        string `json:"artifact,omitempty"`
	Summary         string `json:"summary,omitempty"`
	ClaudeSessionID string `json:"claudeSessionId,omitempty"`
}
```

And add new fields to the existing `Task` struct (after `Branch`):

```go
	Skills      []string    `json:"skills,omitempty"`
	MCPs        []string    `json:"mcps,omitempty"`
	Diff        *DiffInfo   `json:"diff,omitempty"`
	Container   string      `json:"container,omitempty"`
	AskQuestion string      `json:"askQuestion,omitempty"`
	AgentStatus AgentStatus `json:"agentStatus"`
	Sessions    []Session   `json:"sessions,omitempty"`
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/hub/ -run TestSaveAndGetNewFields -v`
Expected: PASS

**Step 5: Run full test suite to check for regressions**

Run: `go test ./internal/hub/ -v`
Expected: All existing tests PASS (new fields have zero values, existing JSON deserializes fine)

**Step 6: Commit**

```bash
git add internal/hub/models.go internal/hub/store_test.go
git commit -m "feat(hub): add AgentStatus, DiffInfo, Session types and new Task fields"
```

---

### Task A2: Refactor TaskStatus to workflow stages

**Files:**
- Modify: `internal/hub/models.go`
- Test: `internal/hub/store_test.go`

**Step 1: Write the failing test for new TaskStatus values**

```go
func TestNewTaskStatusValues(t *testing.T) {
	store := newTestStore(t)

	for _, tc := range []struct {
		status TaskStatus
		agent  AgentStatus
	}{
		{TaskStatusBacklog, AgentStatusIdle},
		{TaskStatusPlanning, AgentStatusWaiting},
		{TaskStatusRunning, AgentStatusRunning},
		{TaskStatusReview, AgentStatusThinking},
		{TaskStatusDone, AgentStatusComplete},
	} {
		task := &Task{
			Project:     "test",
			Description: "status " + string(tc.status),
			Phase:       PhaseExecute,
			Status:      tc.status,
			AgentStatus: tc.agent,
		}
		if err := store.Save(task); err != nil {
			t.Fatalf("Save %s: %v", tc.status, err)
		}
		got, err := store.Get(task.ID)
		if err != nil {
			t.Fatalf("Get %s: %v", tc.status, err)
		}
		if got.Status != tc.status {
			t.Fatalf("expected status %s, got %s", tc.status, got.Status)
		}
		if got.AgentStatus != tc.agent {
			t.Fatalf("expected agentStatus %s, got %s", tc.agent, got.AgentStatus)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/hub/ -run TestNewTaskStatusValues -v`
Expected: FAIL — `TaskStatusBacklog`, `TaskStatusPlanning`, `TaskStatusReview`, `TaskStatusDone` undefined

**Step 3: Replace TaskStatus constants in models.go**

Replace the existing TaskStatus constants with workflow-stage values:

```go
// TaskStatus represents the workflow stage of a task (kanban column).
type TaskStatus string

const (
	TaskStatusBacklog  TaskStatus = "backlog"
	TaskStatusPlanning TaskStatus = "planning"
	TaskStatusRunning  TaskStatus = "running"
	TaskStatusReview   TaskStatus = "review"
	TaskStatusDone     TaskStatus = "done"
)
```

Remove the old constants: `TaskStatusThinking`, `TaskStatusWaiting`, `TaskStatusIdle`, `TaskStatusError`, `TaskStatusComplete`.

**Step 4: Run new test to verify it passes**

Run: `go test ./internal/hub/ -run TestNewTaskStatusValues -v`
Expected: PASS

**Step 5: Update existing store_test.go to use new status values**

Update all references to old TaskStatus constants in `internal/hub/store_test.go`:

| Old | New |
|-----|-----|
| `TaskStatusRunning` | `TaskStatusRunning` (unchanged) |
| `TaskStatusIdle` | `TaskStatusBacklog` |
| `TaskStatusComplete` | `TaskStatusDone` |
| `TaskStatusThinking` | `TaskStatusRunning` (with `AgentStatus: AgentStatusThinking`) |

Specific changes:
- `TestSaveAndGet`: `TaskStatusRunning` stays (add `AgentStatus: AgentStatusRunning`)
- `TestSavePreservesExistingID`: `TaskStatusIdle` to `TaskStatusBacklog`
- `TestSaveUpdatesExistingTask`: `TaskStatusThinking` to `TaskStatusRunning` + `AgentStatus: AgentStatusThinking`
- `TestList`: `TaskStatusRunning` stays
- `TestDelete`: `TaskStatusComplete` to `TaskStatusDone`
- `TestNextIDSequence`: `TaskStatusRunning` stays

**Step 6: Run full store test suite**

Run: `go test ./internal/hub/ -v`
Expected: All PASS

**Step 7: Commit**

```bash
git add internal/hub/models.go internal/hub/store_test.go
git commit -m "refactor(hub): change TaskStatus to workflow stages (backlog/planning/running/review/done)"
```

---

### Task A3: Add migration logic for old-format task JSON

**Files:**
- Modify: `internal/hub/store.go`
- Test: `internal/hub/store_test.go`

**Step 1: Write the failing migration test**

```go
func TestMigrateOldStatusOnRead(t *testing.T) {
	store := newTestStore(t)

	// Write old-format JSON directly to simulate legacy data.
	oldJSON := `{
		"id": "t-001",
		"sessionId": "",
		"status": "thinking",
		"project": "api-service",
		"description": "Legacy task",
		"phase": "execute",
		"createdAt": "2026-01-01T00:00:00Z",
		"updatedAt": "2026-01-01T00:00:00Z"
	}`
	taskFile := filepath.Join(store.taskDir, "t-001.json")
	if err := os.WriteFile(taskFile, []byte(oldJSON), 0o644); err != nil {
		t.Fatalf("write old task: %v", err)
	}

	task, err := store.Get("t-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if task.Status != TaskStatusRunning {
		t.Fatalf("expected migrated status 'running', got %s", task.Status)
	}
	if task.AgentStatus != AgentStatusThinking {
		t.Fatalf("expected migrated agentStatus 'thinking', got %s", task.AgentStatus)
	}
}

func TestMigrateAllOldStatuses(t *testing.T) {
	store := newTestStore(t)

	cases := []struct {
		oldStatus       string
		wantTaskStatus  TaskStatus
		wantAgentStatus AgentStatus
	}{
		{"thinking", TaskStatusRunning, AgentStatusThinking},
		{"waiting", TaskStatusPlanning, AgentStatusWaiting},
		{"running", TaskStatusRunning, AgentStatusRunning},
		{"idle", TaskStatusBacklog, AgentStatusIdle},
		{"error", TaskStatusRunning, AgentStatusError},
		{"complete", TaskStatusDone, AgentStatusComplete},
	}

	for i, tc := range cases {
		id := fmt.Sprintf("t-%03d", i+1)
		raw := fmt.Sprintf(`{"id":"%s","status":"%s","project":"test","description":"test","phase":"execute","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}`, id, tc.oldStatus)
		if err := os.WriteFile(filepath.Join(store.taskDir, id+".json"), []byte(raw), 0o644); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}

		task, err := store.Get(id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if task.Status != tc.wantTaskStatus {
			t.Fatalf("%s: expected status %s, got %s", tc.oldStatus, tc.wantTaskStatus, task.Status)
		}
		if task.AgentStatus != tc.wantAgentStatus {
			t.Fatalf("%s: expected agentStatus %s, got %s", tc.oldStatus, tc.wantAgentStatus, task.AgentStatus)
		}
	}
}

func TestNewStatusNotMigrated(t *testing.T) {
	store := newTestStore(t)

	// New-format JSON should not be modified.
	newJSON := `{
		"id": "t-001",
		"status": "review",
		"agentStatus": "thinking",
		"project": "test",
		"description": "New format",
		"phase": "review",
		"createdAt": "2026-01-01T00:00:00Z",
		"updatedAt": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(filepath.Join(store.taskDir, "t-001.json"), []byte(newJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	task, err := store.Get("t-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.Status != TaskStatusReview {
		t.Fatalf("expected status review, got %s", task.Status)
	}
	if task.AgentStatus != AgentStatusThinking {
		t.Fatalf("expected agentStatus thinking, got %s", task.AgentStatus)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/hub/ -run "TestMigrate|TestNewStatusNotMigrated" -v`
Expected: FAIL — `TestMigrateOldStatusOnRead` gets `status: "thinking"` instead of `"running"`

**Step 3: Add migration logic to store.go**

Add a `migrateTask` function and call it from `readTaskFile`:

```go
// oldStatusMigration maps legacy agent-level status values to the new
// separated TaskStatus + AgentStatus pair.
var oldStatusMigration = map[string]struct {
	taskStatus  TaskStatus
	agentStatus AgentStatus
}{
	"thinking": {TaskStatusRunning, AgentStatusThinking},
	"waiting":  {TaskStatusPlanning, AgentStatusWaiting},
	"running":  {TaskStatusRunning, AgentStatusRunning},
	"idle":     {TaskStatusBacklog, AgentStatusIdle},
	"error":    {TaskStatusRunning, AgentStatusError},
	"complete": {TaskStatusDone, AgentStatusComplete},
}

// migrateTask detects old-format status values and migrates them.
// Returns true if migration was applied.
func migrateTask(task *Task) bool {
	m, ok := oldStatusMigration[string(task.Status)]
	if !ok {
		return false
	}
	// Only migrate if AgentStatus is empty (old format didn't have it).
	if task.AgentStatus != "" {
		return false
	}
	task.Status = m.taskStatus
	task.AgentStatus = m.agentStatus
	return true
}
```

Update `readTaskFile` to call `migrateTask` after unmarshalling:

```go
func (s *TaskStore) readTaskFile(filename string) (*Task, error) {
	data, err := os.ReadFile(filepath.Join(s.taskDir, filename))
	if err != nil {
		return nil, fmt.Errorf("read task file %s: %w", filename, err)
	}
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("unmarshal task %s: %w", filename, err)
	}
	migrateTask(&task)
	return &task, nil
}
```

**Step 4: Run migration tests to verify they pass**

Run: `go test ./internal/hub/ -run "TestMigrate|TestNewStatusNotMigrated" -v`
Expected: All PASS

**Step 5: Run full store test suite**

Run: `go test ./internal/hub/ -v`
Expected: All PASS

**Step 6: Commit**

```bash
git add internal/hub/store.go internal/hub/store_test.go
git commit -m "feat(hub): add transparent migration for old-format task status values"
```

---

### Task A4: Update handlers for new status model

**Files:**
- Modify: `internal/web/handlers_hub.go`
- Test: `internal/web/handlers_hub_test.go`

**Step 1: Update isValidStatus to accept new TaskStatus values**

Replace `isValidStatus` in `handlers_hub.go`:

```go
func isValidStatus(s string) bool {
	switch hub.TaskStatus(s) {
	case hub.TaskStatusBacklog, hub.TaskStatusPlanning, hub.TaskStatusRunning,
		hub.TaskStatusReview, hub.TaskStatusDone:
		return true
	}
	return false
}
```

**Step 2: Add isValidAgentStatus validator**

```go
func isValidAgentStatus(s string) bool {
	switch hub.AgentStatus(s) {
	case hub.AgentStatusThinking, hub.AgentStatusWaiting, hub.AgentStatusRunning,
		hub.AgentStatusIdle, hub.AgentStatusError, hub.AgentStatusComplete:
		return true
	}
	return false
}
```

**Step 3: Update updateTaskRequest to include agentStatus**

```go
type updateTaskRequest struct {
	Description *string `json:"description,omitempty"`
	Phase       *string `json:"phase,omitempty"`
	Status      *string `json:"status,omitempty"`
	AgentStatus *string `json:"agentStatus,omitempty"`
	Branch      *string `json:"branch,omitempty"`
	AskQuestion *string `json:"askQuestion,omitempty"`
}
```

**Step 4: Update handleTaskUpdate to handle agentStatus and askQuestion**

Add validation and assignment for the new fields in `handleTaskUpdate`:

```go
	if req.AgentStatus != nil && !isValidAgentStatus(*req.AgentStatus) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid agentStatus value")
		return
	}
```

And in the assignment block:

```go
	if req.AgentStatus != nil {
		task.AgentStatus = hub.AgentStatus(*req.AgentStatus)
	}
	if req.AskQuestion != nil {
		task.AskQuestion = *req.AskQuestion
	}
```

**Step 5: Update handleTasksCreate to use new status values**

Change initial task creation status:
- `hub.TaskStatusIdle` to `hub.TaskStatusBacklog`
- After successful session launch: set `task.Status = hub.TaskStatusRunning` and `task.AgentStatus = hub.AgentStatusThinking`

```go
	task := &hub.Task{
		Project:     req.Project,
		Description: req.Description,
		Phase:       phase,
		Branch:      req.Branch,
		Status:      hub.TaskStatusBacklog,
		AgentStatus: hub.AgentStatusIdle,
	}
```

And after successful launch:

```go
			if launchErr == nil {
				task.TmuxSession = sessionName
				task.Status = hub.TaskStatusRunning
				task.AgentStatus = hub.AgentStatusThinking
				_ = s.hubTasks.Save(task)
			}
```

**Step 6: Update handleTaskFork to use new status values**

```go
	child := &hub.Task{
		Project:      parent.Project,
		Description:  description,
		Phase:        parent.Phase,
		Branch:       parent.Branch,
		Status:       hub.TaskStatusBacklog,
		AgentStatus:  hub.AgentStatusIdle,
		ParentTaskID: parent.ID,
	}
```

**Step 7: Run existing tests — expect failures**

Run: `go test ./internal/web/ -run TestHub -v`
Expected: Multiple FAIL — tests reference old status values

**Step 8: Update all handler tests**

Apply these changes throughout `handlers_hub_test.go`:

| Old code | New code |
|----------|----------|
| `Status: hub.TaskStatusRunning` | `Status: hub.TaskStatusRunning, AgentStatus: hub.AgentStatusRunning` |
| `Status: hub.TaskStatusComplete` | `Status: hub.TaskStatusDone, AgentStatus: hub.AgentStatusComplete` |
| `Status: hub.TaskStatusIdle` | `Status: hub.TaskStatusBacklog, AgentStatus: hub.AgentStatusIdle` |
| `Status: hub.TaskStatusWaiting` | `Status: hub.TaskStatusPlanning, AgentStatus: hub.AgentStatusWaiting` |
| `Status: hub.TaskStatusThinking` | `Status: hub.TaskStatusRunning, AgentStatus: hub.AgentStatusThinking` |
| `resp.Task.Status != hub.TaskStatusIdle` | `resp.Task.Status != hub.TaskStatusBacklog` |
| `resp.Task.Status != hub.TaskStatusThinking` | `resp.Task.Status != hub.TaskStatusRunning` (and check `resp.Task.AgentStatus`) |
| `resp.Task.Status != hub.TaskStatusComplete` | `resp.Task.Status != hub.TaskStatusDone` |
| `"status":"complete"` in JSON string checks | `"status":"done"` |
| `body := '{"status":"complete"}'` in TestUpdateTaskStatus | `body := '{"status":"done"}'` |

Specific test updates:

**TestCreateTask:** Check `resp.Task.Status != hub.TaskStatusBacklog` and `resp.Task.AgentStatus != hub.AgentStatusIdle`

**TestCreateTaskLaunchesSession:** Check `resp.Task.Status != hub.TaskStatusRunning` and `resp.Task.AgentStatus != hub.AgentStatusThinking`

**TestUpdateTaskStatus:** Change request body to `{"status":"done"}` and check `resp.Task.Status != hub.TaskStatusDone`

**TestUpdateTaskInvalidStatus:** Keep as-is (tests invalid value)

**TestTasksEndpointFilterByStatus:** Change statuses from `hub.TaskStatusRunning, hub.TaskStatusComplete, hub.TaskStatusRunning` to `hub.TaskStatusRunning, hub.TaskStatusDone, hub.TaskStatusRunning`, and filter check from `"status":"complete"` to `"status":"done"`

Add a new test for agentStatus update:

```go
func TestUpdateTaskAgentStatus(t *testing.T) {
	srv := newTestServerWithHub(t)

	task := &hub.Task{
		Project:     "api-service",
		Description: "Fix auth bug",
		Phase:       hub.PhaseExecute,
		Status:      hub.TaskStatusRunning,
		AgentStatus: hub.AgentStatusRunning,
	}
	if err := srv.hubTasks.Save(task); err != nil {
		t.Fatalf("Save: %v", err)
	}

	body := `{"agentStatus":"waiting","askQuestion":"Which auth method?"}`
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
	if resp.Task.AgentStatus != hub.AgentStatusWaiting {
		t.Fatalf("expected agentStatus waiting, got %s", resp.Task.AgentStatus)
	}
	if resp.Task.AskQuestion != "Which auth method?" {
		t.Fatalf("expected askQuestion, got %s", resp.Task.AskQuestion)
	}
}

func TestUpdateTaskInvalidAgentStatus(t *testing.T) {
	srv := newTestServerWithHub(t)

	task := &hub.Task{
		Project:     "api-service",
		Description: "Fix auth bug",
		Phase:       hub.PhaseExecute,
		Status:      hub.TaskStatusRunning,
		AgentStatus: hub.AgentStatusRunning,
	}
	if err := srv.hubTasks.Save(task); err != nil {
		t.Fatalf("Save: %v", err)
	}

	body := `{"agentStatus":"notreal"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+task.ID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d: %s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
}
```

**Step 9: Run all handler tests**

Run: `go test ./internal/web/ -v`
Expected: All PASS

**Step 10: Run full project test suite**

Run: `go test ./... 2>&1 | tail -30`
Expected: All PASS

**Step 11: Commit**

```bash
git add internal/web/handlers_hub.go internal/web/handlers_hub_test.go
git commit -m "refactor(hub): update handlers and tests for new TaskStatus/AgentStatus model"
```

---

## Track B: Agents View Redesign

### Task B1: Update CSS variables to dark theme

**Files:**
- Modify: `internal/web/static/styles.css`
- Modify: `internal/web/static/dashboard.css`

**Step 1: Replace :root CSS variables in styles.css**

Replace the current `:root` block in `styles.css` with the dark theme values:

```css
:root {
    --bg: #0b0d11;
    --bg-card: #10131a;
    --bg-panel: #0e1018;
    --panel: #10131a;
    --text: #dce4f0;
    --text-mid: #8b95aa;
    --text-dim: #4a5368;
    --muted: #8b95aa;
    --border: #1a1e2a;
    --border-light: #222838;
    --accent: #e8a932;
    --green: #2dd4a0;
    --red: #f06060;
    --purple: #8b8cf8;
    --blue: #4ca8e8;
    --orange: #f59e0b;
    --term-bg: #080a0e;
    --term-text: #c8d0dc;
    --font-mono: 'JetBrains Mono', 'Fira Code', 'IBM Plex Mono', monospace;
    --font-sans: 'IBM Plex Sans', -apple-system, sans-serif;
    --topbar-height: 48px;
    --sidebar-width: 56px;
    --app-height: 100vh;
    --keyboard-inset: 0px;

    /* Phase colors */
    --phase-brainstorm: #c084fc;
    --phase-plan: #8b8cf8;
    --phase-execute: #e8a932;
    --phase-review: #4ca8e8;
    --phase-done: #2dd4a0;
}
```

**Step 2: Update body and shared component styles for dark theme**

Update `body` background, `.topbar` background, `.meta` pill states, and all hardcoded light-theme colors (e.g., `#fff`, `rgba(255,255,255,...)`, `#f8fbff`) to use CSS variables.

Key changes in `styles.css`:
- `body` background: `var(--bg)` instead of gradient
- `.topbar` background: `var(--bg-panel)` instead of `var(--panel)`
- `.meta` states: update colors to dark-theme equivalents
- `.menu-panel` background: `var(--bg-panel)`
- `.terminal-shell` already dark — leave mostly alone
- Font family: add `var(--font-sans)` to body

Key changes in `dashboard.css`:
- `.hub-toolbar` background: `var(--bg-panel)` instead of `rgba(255,255,255,0.7)`
- `.hub-select` background: `var(--bg-card)` instead of `#fff`
- `.task-card` uses `var(--bg-card)` instead of `var(--panel)`
- `.detail-chat-input` background: `var(--bg-card)` instead of `#fff`
- `.modal` background: `var(--bg-card)`
- `.modal-textarea` background: `var(--bg-card)` instead of `#fff`
- All `#fff` backgrounds to `var(--bg-card)` or `var(--bg-panel)`
- `.phase-dot` background: `var(--bg-card)` instead of `#fff`

**Step 3: Update status colors to use new palette**

Update status dot colors in `dashboard.css`:

```css
.task-status--thinking { background: var(--orange); }
.task-status--waiting  { background: var(--orange); animation: pulse-dot 1.5s ease-in-out infinite; }
.task-status--running  { background: var(--blue); }
.task-status--idle     { background: var(--text-dim); }
.task-status--error    { background: var(--red); }
.task-status--complete { background: var(--green); }
```

**Step 4: Update button styles for dark theme**

`.hub-btn-primary`: background `var(--accent)`, hover darker amber.
`.hub-btn`: border `var(--border)`, hover `var(--bg-panel)`.
`.nav-tab--active`: background `var(--accent)`, hover slightly darker.

**Step 5: Verify visually**

Open `http://localhost:8420/` in a browser. The entire dashboard should now be dark-themed. Cards, modals, filters, and the topbar should all use the dark palette.

**Step 6: Commit**

```bash
git add internal/web/static/styles.css internal/web/static/dashboard.css
git commit -m "feat(hub): apply dark theme CSS variables from design spec"
```

---

### Task B2: Restructure HTML for two-panel layout with sidebar

**Files:**
- Modify: `internal/web/static/dashboard.html`

**Step 1: Replace the dashboard HTML body**

Replace the content inside `<body>` with the new two-panel layout. The new structure includes:

- **Sidebar** (`nav.sidebar`): 5 view icon buttons stacked vertically, connection status dot and active agent count at bottom
- **Main content** (`div.main-content`): contains panels container + chat bar
- **Panel left** (`div.panel-left`): filter bar with project select, scrollable task list
- **Panel right** (`div.panel-right`): empty state or detail content (header, session chain, preview header, terminal container)
- **Chat bar** (`div.chat-bar`): mode button, text input, send button — pinned to bottom
- **New Task modal**: kept for the "+" flow
- **Mobile bottom nav**: hidden on desktop, shown at < 768px

Add xterm.js CSS and JS via CDN links (same versions as index.html: `@xterm/xterm@5.5.0` and `@xterm/addon-fit@0.10.0`).

Update `<meta name="theme-color">` to `#0b0d11`.

**Step 2: Verify HTML loads without errors**

Open `http://localhost:8420/` in browser. The new layout structure should render (unstyled portions are expected).

**Step 3: Commit**

```bash
git add internal/web/static/dashboard.html
git commit -m "feat(hub): restructure dashboard HTML for two-panel layout with sidebar"
```

---

### Task B3: Write new dashboard CSS for two-panel layout

**Files:**
- Modify: `internal/web/static/dashboard.css`

**Step 1: Replace dashboard.css with new layout styles**

Replace the full content of `dashboard.css` with styles for:

1. **Sidebar** (56px fixed left): icon buttons stacked vertically, bottom status
2. **Main content** (flex, fills remaining): panels container + chat bar
3. **Panel left** (280px): filter bar, scrollable task list with sections (Active/Completed)
4. **Panel right** (flex): detail header, session chain, preview header, terminal container
5. **Agent cards**: left border colored by status, agent status badge, mini session chain
6. **Session chain**: horizontal phase pips with duration labels
7. **Modal**: dark-themed modal for new task
8. **Chat bar**: fixed bottom, full width
9. **Responsive**: at 768px, sidebar becomes mobile bottom nav, panels stack

The full CSS is large. Key structural rules:

```css
/* App layout with sidebar */
.app {
  height: var(--app-height);
  display: flex;
  overflow: hidden;
}

/* Sidebar */
.sidebar {
  width: var(--sidebar-width);
  background: var(--bg);
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  padding: 12px 0;
  flex-shrink: 0;
}

.sidebar-icon {
  width: 40px;
  height: 40px;
  margin: 2px auto;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--text-dim);
  font-size: 1.1rem;
  cursor: pointer;
}

.sidebar-icon:hover { background: var(--bg-panel); color: var(--text-mid); }
.sidebar-icon--active { background: var(--bg-card); color: var(--accent); }

/* Main content area */
.main-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  overflow: hidden;
}

/* Two-panel layout */
.panels {
  flex: 1;
  display: flex;
  min-height: 0;
  overflow: hidden;
}

.panel-left {
  width: 280px;
  flex-shrink: 0;
  border-right: 1px solid var(--border);
  background: var(--bg-panel);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.panel-right {
  flex: 1;
  background: var(--bg);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* Chat bar */
.chat-bar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 16px;
  border-top: 1px solid var(--border);
  background: var(--bg-panel);
}
```

Include styles for: `.sidebar-icon`, `.filter-bar`, `.filter-select`, `.task-section`, `.task-section-header`, `.agent-card`, `.agent-card-status-border`, `.agent-status-badge`, `.mini-session-chain`, `.detail-header`, `.session-chain-pip`, `.preview-header`, `.terminal-container`, `.empty-state`, `.chat-mode-btn`, `.chat-input`, `.chat-send-btn`, `.btn-primary`, `.btn-secondary`, `.ask-banner`, modal overrides, responsive breakpoint.

**Step 2: Verify layout renders correctly**

Open browser. Should see: dark sidebar on left, task list panel, right detail panel with "Select an agent" empty state, and chat bar at bottom.

**Step 3: Commit**

```bash
git add internal/web/static/dashboard.css
git commit -m "feat(hub): add two-panel layout CSS with dark theme"
```

---

### Task B4: Rewrite dashboard.js with component functions

**Files:**
- Modify: `internal/web/static/dashboard.js`

**Step 1: Plan the component structure**

Replace the current `dashboard.js` with component-oriented functions matching hub-v3.jsx:

| Function | Responsibility |
|----------|---------------|
| `renderSidebar(view)` | Highlight active view icon |
| `renderFilterBar(filter, projects)` | Project filter pills |
| `renderTaskList(tasks, selectedId)` | Split tasks into Active/Completed sections |
| `createAgentCard(task, isSelected)` | Card with status border, badge, mini session chain |
| `createAgentStatusBadge(agentStatus)` | Status icon + label badge |
| `renderSessionChain(task)` | Phase pip navigator with duration/artifact labels |
| `renderPreviewHeader(task)` | Project name, agent status, workspace path |
| `renderDetailHeader(task)` | Description, status badge, action buttons |
| `renderEmptyState()` | "Select an agent to preview" |

State object:

```js
var state = {
  tasks: [],
  projects: [],
  selectedTaskId: null,
  activeView: "agents",
  projectFilter: "",
  authToken: readAuthTokenFromURL(),
  menuEvents: null,
  terminal: null,
  terminalWs: null,
  fitAddon: null,
}
```

**Step 2: Write the new dashboard.js**

Rewrite the file with all component functions. Key changes from current code:

1. **Task list rendering** uses `renderTaskList()` which groups tasks into Active (status not "done") and Completed (status "done") sections
2. **Agent cards** use `createAgentCard()` with colored left border based on `task.status` and `createAgentStatusBadge()` for `task.agentStatus`
3. **Detail panel** is inline (not slide-over). Selecting a card shows detail in right panel
4. **Session chain** renders phase pips from `task.sessions` array (or single pip from `task.phase` if no sessions)
5. **Terminal embedding**: when a task is selected and has a `tmuxSession`, create xterm.js terminal and connect via WebSocket to `/ws/session/{tmuxSession}`
6. **Sidebar** view switching (only "agents" is functional, others show stub)

Terminal management — use the existing `clearChildren()` helper to clear the container safely:

```js
function connectTerminal(task) {
  disconnectTerminal()
  if (!task.tmuxSession) return

  var container = document.getElementById("terminal-container")
  if (!container) return
  clearChildren(container)

  var term = new Terminal({
    cursorBlink: true,
    fontSize: 13,
    fontFamily: "var(--font-mono)",
    theme: {
      background: "#080a0e",
      foreground: "#c8d0dc",
      cursor: "#e8a932",
    },
  })
  var fitAddon = new FitAddon.FitAddon()
  term.loadAddon(fitAddon)
  term.open(container)
  fitAddon.fit()

  state.terminal = term
  state.fitAddon = fitAddon

  var protocol = window.location.protocol === "https:" ? "wss:" : "ws:"
  var wsUrl = protocol + "//" + window.location.host + "/ws/session/" + encodeURIComponent(task.tmuxSession)
  if (state.authToken) wsUrl += "?token=" + encodeURIComponent(state.authToken)
  var ws = new WebSocket(wsUrl)
  state.terminalWs = ws

  ws.binaryType = "arraybuffer"
  ws.onmessage = function (e) {
    if (e.data instanceof ArrayBuffer) {
      term.write(new Uint8Array(e.data))
    } else {
      term.write(e.data)
    }
  }
  ws.onclose = function () { state.terminalWs = null }
  term.onData(function (data) {
    if (ws.readyState === WebSocket.OPEN) ws.send(data)
  })

  window.addEventListener("resize", function () {
    if (state.fitAddon) state.fitAddon.fit()
  })
}

function disconnectTerminal() {
  if (state.terminalWs) {
    state.terminalWs.close()
    state.terminalWs = null
  }
  if (state.terminal) {
    state.terminal.dispose()
    state.terminal = null
  }
  state.fitAddon = null
}
```

Keep existing: auth helpers, SSE connection, fetch functions, new task modal, route suggestion.

**Step 3: Update STATUS_META for new agentStatus values**

```js
var AGENT_STATUS_META = {
  thinking: { icon: "\u25CF", label: "Thinking", color: "var(--orange)" },
  waiting:  { icon: "\u25D0", label: "Input needed", color: "var(--orange)" },
  running:  { icon: "\u27F3", label: "Running", color: "var(--blue)" },
  idle:     { icon: "\u25CB", label: "Idle", color: "var(--text-dim)" },
  error:    { icon: "\u2715", label: "Error", color: "var(--red)" },
  complete: { icon: "\u2713", label: "Complete", color: "var(--green)" },
}

var TASK_STATUS_COLORS = {
  backlog:  "var(--text-dim)",
  planning: "var(--phase-plan)",
  running:  "var(--phase-execute)",
  review:   "var(--phase-review)",
  done:     "var(--phase-done)",
}
```

**Step 4: Verify everything works**

Open browser. Verify:
- Dark theme renders
- Sidebar icons are visible (only "agents" is active)
- Task list loads and groups into Active/Completed
- Clicking a task shows detail in right panel
- Terminal connects if task has a tmux session
- New task modal still works
- Mobile view shows bottom nav

**Step 5: Commit**

```bash
git add internal/web/static/dashboard.js
git commit -m "feat(hub): rewrite dashboard.js with component functions and embedded xterm.js"
```

---

### Task B5: Add responsive design and mobile layout

**Files:**
- Modify: `internal/web/static/dashboard.css`
- Modify: `internal/web/static/dashboard.js`

**Step 1: Add responsive CSS at 768px breakpoint**

```css
@media (max-width: 768px) {
  .sidebar { display: none; }

  .main-content { margin-left: 0; }

  .panels { flex-direction: column; }

  .panel-left {
    width: 100%;
    border-right: none;
    border-bottom: 1px solid var(--border);
  }

  .panel-right { display: none; }

  /* When a task is selected on mobile, show detail and hide list */
  .panels.detail-active .panel-left { display: none; }
  .panels.detail-active .panel-right { display: flex; }

  .mobile-nav {
    display: flex;
    border-top: 1px solid var(--border);
    background: var(--bg-panel);
    padding: 6px 0;
    padding-bottom: max(6px, env(safe-area-inset-bottom));
  }

  /* Back button visible on mobile detail */
  .detail-back-btn { display: inline-flex; }
}
```

**Step 2: Add mobile back button handling in JS**

Add a back button to the detail header that returns to the task list on mobile:

```js
function handleMobileBack() {
  state.selectedTaskId = null
  disconnectTerminal()
  var panels = document.querySelector(".panels")
  if (panels) panels.classList.remove("detail-active")
  renderTaskList()
  renderRightPanel()
}
```

When selecting a task on mobile, add `.detail-active` class to `.panels`.

**Step 3: Add filter bar horizontal scrolling**

```css
.filter-bar {
  display: flex;
  gap: 6px;
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
  scrollbar-width: none;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
}
.filter-bar::-webkit-scrollbar { display: none; }
```

**Step 4: Test on narrow viewport**

Resize browser to < 768px. Verify:
- Sidebar hidden, bottom nav visible
- Task list fills width
- Clicking task shows detail (list hidden)
- Back button returns to list
- Chat bar visible at bottom

**Step 5: Commit**

```bash
git add internal/web/static/dashboard.css internal/web/static/dashboard.js
git commit -m "feat(hub): add responsive design with mobile layout at 768px breakpoint"
```

---

## Track C: Context-Aware Chat Input

### Task C1: Implement chat mode auto-detection

**Files:**
- Modify: `internal/web/static/dashboard.js`
- Modify: `internal/web/static/dashboard.css`

**Step 1: Add chat mode state and detection logic**

Add to state:

```js
  chatMode: null,        // { mode: "reply"|"new"|"conductor", label: string, icon: string, color: string }
  chatModeOverride: null, // manual override, cleared on navigation change
```

Add mode detection function:

```js
function detectChatMode() {
  if (state.chatModeOverride) return state.chatModeOverride

  var task = state.selectedTaskId ? findTask(state.selectedTaskId) : null

  if (state.activeView === "agents" && task && task.agentStatus !== "complete" && task.agentStatus !== "idle") {
    return {
      mode: "reply",
      label: "\u21A9 " + task.id + "/" + task.phase,
      icon: "\u21A9",
      color: "var(--accent)",
    }
  }

  var project = ""
  if (task) project = task.project
  else if (state.projectFilter) project = state.projectFilter

  return {
    mode: "new",
    label: project ? "+ " + project : "+ auto-route",
    icon: "+",
    color: "var(--blue)",
  }
}
```

**Step 2: Update renderChatBar to show detected mode**

```js
function renderChatBar() {
  var mode = detectChatMode()
  state.chatMode = mode

  var modeBtn = document.getElementById("chat-mode-btn")
  var modeIcon = document.getElementById("chat-mode-icon")
  var modeLabel = document.getElementById("chat-mode-label")
  var input = document.getElementById("chat-input")

  if (modeBtn) modeBtn.style.borderColor = mode.color
  if (modeIcon) { modeIcon.textContent = mode.icon; modeIcon.style.color = mode.color }
  if (modeLabel) modeLabel.textContent = mode.label

  if (mode.mode === "reply") {
    if (input) input.placeholder = "Reply to " + state.selectedTaskId + "..."
  } else {
    if (input) input.placeholder = "Describe a new task..."
  }
}
```

**Step 3: Update send logic to use detected mode**

```js
function sendChatMessage() {
  var input = document.getElementById("chat-input")
  if (!input) return
  var text = input.value.trim()
  if (!text) return

  var mode = state.chatMode || detectChatMode()

  if (mode.mode === "reply" && state.selectedTaskId) {
    // Send input to existing task
    sendTaskInput(state.selectedTaskId, text)
  } else {
    // Create new task
    openNewTaskModalWithDescription(text, mode)
  }
  input.value = ""
}
```

**Step 4: Call renderChatBar whenever selection or navigation changes**

Add `renderChatBar()` calls in:
- `selectTask(id)` after updating state
- `handleMobileBack()` after clearing selection
- View change handler
- Filter change handler

**Step 5: Verify auto-detection works**

Test in browser:
- No task selected: chat shows "+ auto-route" or "+ {project}" if filtered
- Select active task: chat shows "arrow-return t-XXX/execute"
- Select completed task: chat shows "+ {project}"

**Step 6: Commit**

```bash
git add internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(hub): add chat mode auto-detection (reply/new based on context)"
```

---

### Task C2: Add mode override menu

**Files:**
- Modify: `internal/web/static/dashboard.js`
- Modify: `internal/web/static/dashboard.css`

**Step 1: Add CSS for mode override dropdown**

```css
.chat-mode-menu {
  position: absolute;
  bottom: 100%;
  left: 0;
  margin-bottom: 4px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 -4px 16px rgba(0, 0, 0, 0.3);
  min-width: 220px;
  padding: 4px 0;
  z-index: 70;
  display: none;
}

.chat-mode-menu.open { display: block; }

.chat-mode-option {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 8px 12px;
  border: none;
  background: transparent;
  color: var(--text);
  font: inherit;
  font-size: 0.85rem;
  cursor: pointer;
  text-align: left;
}

.chat-mode-option:hover { background: var(--bg-panel); }

.chat-mode-option-icon {
  width: 20px;
  text-align: center;
  flex-shrink: 0;
}
```

**Step 2: Add mode menu rendering and toggle**

```js
function renderModeMenu() {
  var existing = document.querySelector(".chat-mode-menu")
  if (existing) existing.remove()

  var menu = el("div", "chat-mode-menu open")
  var task = state.selectedTaskId ? findTask(state.selectedTaskId) : null

  // "New in {project}" options
  for (var i = 0; i < state.projects.length; i++) {
    var proj = state.projects[i]
    var opt = el("button", "chat-mode-option")
    var icon = el("span", "chat-mode-option-icon", "+")
    icon.style.color = "var(--blue)"
    opt.appendChild(icon)
    opt.appendChild(document.createTextNode("New in " + proj.name))
    opt.dataset.mode = "new"
    opt.dataset.project = proj.name
    opt.addEventListener("click", handleModeSelect)
    menu.appendChild(opt)
  }

  // "New (auto-route)"
  var autoOpt = el("button", "chat-mode-option")
  var autoIcon = el("span", "chat-mode-option-icon", "+")
  autoIcon.style.color = "var(--blue)"
  autoOpt.appendChild(autoIcon)
  autoOpt.appendChild(document.createTextNode("New (auto-route)"))
  autoOpt.dataset.mode = "new"
  autoOpt.dataset.project = ""
  autoOpt.addEventListener("click", handleModeSelect)
  menu.appendChild(autoOpt)

  // "Back to auto" if overridden
  if (state.chatModeOverride) {
    var backOpt = el("button", "chat-mode-option")
    var backIcon = el("span", "chat-mode-option-icon", "\u2190")
    backOpt.appendChild(backIcon)
    backOpt.appendChild(document.createTextNode("Back to: auto"))
    backOpt.dataset.mode = "auto"
    backOpt.addEventListener("click", handleModeSelect)
    menu.appendChild(backOpt)
  }

  var chatBar = document.getElementById("chat-bar")
  if (chatBar) {
    chatBar.style.position = "relative"
    chatBar.appendChild(menu)
  }

  // Close on outside click
  setTimeout(function () {
    document.addEventListener("click", closeModeMenu)
  }, 0)

  return menu
}

function handleModeSelect(e) {
  var btn = e.currentTarget
  if (btn.dataset.mode === "auto") {
    state.chatModeOverride = null
  } else {
    state.chatModeOverride = {
      mode: btn.dataset.mode,
      label: btn.dataset.project ? "+ " + btn.dataset.project : "+ auto-route",
      icon: "+",
      color: "var(--blue)",
      project: btn.dataset.project,
    }
  }
  closeModeMenu()
  renderChatBar()
}

function closeModeMenu() {
  var menu = document.querySelector(".chat-mode-menu")
  if (menu) menu.remove()
  document.removeEventListener("click", closeModeMenu)
}
```

**Step 3: Wire mode button click to toggle menu**

```js
var modeBtnEl = document.getElementById("chat-mode-btn")
if (modeBtnEl) {
  modeBtnEl.addEventListener("click", function (e) {
    e.stopPropagation()
    var existing = document.querySelector(".chat-mode-menu")
    if (existing) { closeModeMenu(); return }
    renderModeMenu()
  })
}
```

**Step 4: Clear override on navigation change**

In the view change handler and selectTask:

```js
state.chatModeOverride = null
renderChatBar()
```

**Step 5: Verify mode menu works**

Test in browser:
- Click mode button: dropdown appears with project options
- Select "New in {project}": mode label updates
- Select "Back to: auto": returns to auto-detection
- Change task selection: override clears

**Step 6: Commit**

```bash
git add internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(hub): add chat mode override menu with project selection"
```

---

### Task C3: Add AskUserQuestion surfacing

**Files:**
- Modify: `internal/web/static/dashboard.js`
- Modify: `internal/web/static/dashboard.css`

**Step 1: Add CSS for ask-question banner and badge**

```css
.ask-banner {
  padding: 8px 12px;
  background: rgba(245, 158, 11, 0.1);
  border-top: 1px solid rgba(245, 158, 11, 0.3);
  color: var(--orange);
  font-size: 0.85rem;
  display: flex;
  align-items: center;
  gap: 8px;
}

.ask-banner-icon {
  animation: pulse-dot 1.5s ease-in-out infinite;
}

.ask-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--orange);
  border: 1px solid rgba(245, 158, 11, 0.4);
  border-radius: 999px;
  padding: 1px 6px;
  animation: pulse-dot 1.5s ease-in-out infinite;
}
```

**Step 2: Add ask-question banner rendering**

```js
function renderAskBanner() {
  var existing = document.querySelector(".ask-banner")
  if (existing) existing.remove()

  var task = state.selectedTaskId ? findTask(state.selectedTaskId) : null
  if (!task || task.agentStatus !== "waiting" || !task.askQuestion) return

  var banner = el("div", "ask-banner")
  var icon = el("span", "ask-banner-icon", "\u25D0")
  banner.appendChild(icon)
  banner.appendChild(document.createTextNode("Agent is asking: " + task.askQuestion))

  var chatBar = document.getElementById("chat-bar")
  if (chatBar && chatBar.parentNode) {
    chatBar.parentNode.insertBefore(banner, chatBar)
  }

  // Update placeholder
  var input = document.getElementById("chat-input")
  if (input) input.placeholder = "Answer: " + task.askQuestion
}
```

**Step 3: Add pulsing INPUT badge to agent cards**

In `createAgentCard`, when `task.agentStatus === "waiting"` and `task.askQuestion`:

```js
if (task.agentStatus === "waiting" && task.askQuestion) {
  var askBadge = el("span", "ask-badge")
  askBadge.appendChild(document.createTextNode("\u25D0 INPUT"))
  // Append to card header area
  cardHeader.appendChild(askBadge)
}
```

**Step 4: Call renderAskBanner on task selection and data updates**

Add `renderAskBanner()` calls after `renderChatBar()` in `selectTask()` and in the SSE data update handler.

**Step 5: Verify AskUserQuestion works**

To test: update a task via API with `agentStatus: "waiting"` and `askQuestion`:

```bash
curl -X PATCH http://localhost:8420/api/tasks/t-001 \
  -H "Content-Type: application/json" \
  -d '{"agentStatus":"waiting","askQuestion":"Which auth method?"}'
```

Verify:
- Task card shows pulsing "INPUT" badge
- Selecting that task shows orange banner above chat input
- Placeholder text changes to "Answer: {question}"

**Step 6: Commit**

```bash
git add internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(hub): add AskUserQuestion surfacing with banner and pulsing badge"
```

---

## Merge Order

After all tracks are complete:

1. **Merge Track A** into `feature/hub-interface` — data model is the foundation
2. **Rebase Track B onto Track A** — update any JS that references old status values to use new `status`/`agentStatus` fields
3. **Merge Track B** into `feature/hub-interface`
4. **Rebase Track C onto Track B** — Track C builds on Track B's chat bar HTML
5. **Merge Track C** into `feature/hub-interface`
6. **Final verification**: run `go test ./...` and manual browser test
7. **PR to main**

---

## Quick Reference

| Track | Branch | Files | Tests |
|-------|--------|-------|-------|
| A | `refactor/task-data-model` | `models.go`, `store.go`, `handlers_hub.go` | `store_test.go`, `handlers_hub_test.go` |
| B | `feature/agents-view-redesign` | `dashboard.html`, `dashboard.css`, `dashboard.js`, `styles.css` | Manual browser testing |
| C | `feature/smart-chat-input` | `dashboard.js`, `dashboard.css` | Manual browser testing |
