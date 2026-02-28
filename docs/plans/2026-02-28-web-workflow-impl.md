# Web Workflow UI Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add session lifecycle operations, status filtering, search, analytics, and notification features to the web dashboard, closing the gap with the TUI.

**Architecture:** Evolve the existing `dashboard.html` / `dashboard.js` / `dashboard.css` with new components. Two new Go API endpoints (analytics, restart) follow the existing `handlers_hub.go` pattern. All frontend is vanilla JS using the existing `el()` helper and modal patterns.

**Tech Stack:** Go 1.24+ (backend), vanilla JS/CSS (frontend), xterm.js (terminal), httptest (Go tests)

**Design doc:** `docs/plans/2026-02-28-web-workflow-design.md`

---

### Task 1: Backend — Analytics API Endpoint

**Files:**
- Modify: `internal/web/handlers_hub.go` (add route + handler)
- Modify: `internal/web/server.go:152` (route already dispatched via `/api/tasks/`)
- Test: `internal/web/handlers_hub_test.go`

**Step 1: Write the failing test**

Add to `internal/web/handlers_hub_test.go`:

```go
func TestTaskAnalyticsEndpoint(t *testing.T) {
	srv := newTestServerWithHub(t)

	task := &hub.Task{
		Project:     "test-proj",
		Description: "Analytics test",
		Phase:       hub.PhaseExecute,
		Status:      hub.TaskStatusRunning,
	}
	require.NoError(t, srv.hubTasks.Save(task))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID+"/analytics", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Contains(t, resp, "analytics")
}

func TestTaskAnalyticsNotFound(t *testing.T) {
	srv := newTestServerWithHub(t)

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/nonexistent/analytics", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race -v ./internal/web -run TestTaskAnalytics`
Expected: FAIL — route not handled, likely 405 or falls through

**Step 3: Add the analytics response struct and handler**

In `internal/web/handlers_hub.go`, add the response struct near the other response types (~line 916):

```go
type analyticsResponse struct {
	Analytics *taskAnalyticsData `json:"analytics"`
}

type taskAnalyticsData struct {
	InputTokens         int       `json:"inputTokens"`
	OutputTokens        int       `json:"outputTokens"`
	CacheReadTokens     int       `json:"cacheReadTokens"`
	CacheWriteTokens    int       `json:"cacheWriteTokens"`
	CurrentContextTokens int      `json:"currentContextTokens"`
	ContextPercent      float64   `json:"contextPercent"`
	TotalTurns          int       `json:"totalTurns"`
	DurationSeconds     float64   `json:"durationSeconds"`
	EstimatedCost       float64   `json:"estimatedCost"`
	ToolCalls           []toolCallData `json:"toolCalls"`
	Model               string    `json:"model,omitempty"`
}

type toolCallData struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}
```

Add the handler function:

```go
func (s *Server) handleTaskAnalytics(w http.ResponseWriter, r *http.Request, taskID string) {
	if s.hubTasks == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "hub not initialized")
		return
	}

	task, err := s.hubTasks.Get(taskID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}

	// Find active session's JSONL file for analytics
	data := &taskAnalyticsData{}
	sessionID := getActiveClaudeSessionID(task)
	if sessionID != "" {
		analytics, parseErr := session.ParseSessionJSONLByID(s.claudeProjectsDir, sessionID)
		if parseErr == nil && analytics != nil {
			data.InputTokens = analytics.InputTokens
			data.OutputTokens = analytics.OutputTokens
			data.CacheReadTokens = analytics.CacheReadTokens
			data.CacheWriteTokens = analytics.CacheWriteTokens
			data.CurrentContextTokens = analytics.CurrentContextTokens
			data.ContextPercent = analytics.ContextPercent(200000)
			data.TotalTurns = analytics.TotalTurns
			data.DurationSeconds = analytics.Duration.Seconds()
			data.EstimatedCost = analytics.EstimatedCost
			for _, tc := range analytics.ToolCalls {
				data.ToolCalls = append(data.ToolCalls, toolCallData{Name: tc.Name, Count: tc.Count})
			}
		}
	}

	writeJSON(w, http.StatusOK, analyticsResponse{Analytics: data})
}
```

Add a helper to extract the active Claude session ID from a hub task:

```go
func getActiveClaudeSessionID(task *hub.Task) string {
	for i := len(task.Sessions) - 1; i >= 0; i-- {
		if task.Sessions[i].ClaudeSessionID != "" {
			return task.Sessions[i].ClaudeSessionID
		}
	}
	return ""
}
```

Wire the route in `handleTaskByID` dispatch (add after the `/preview` case):

```go
case "analytics":
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "use GET")
		return
	}
	s.handleTaskAnalytics(w, r, taskID)
```

**Step 4: Run test to verify it passes**

Run: `go test -race -v ./internal/web -run TestTaskAnalytics`
Expected: PASS

**Step 5: Verify ParseSessionJSONLByID exists or create wrapper**

Check if `session.ParseSessionJSONLByID` exists. If not, add a helper in `internal/session/analytics.go` that locates the JSONL file by session UUID under `~/.claude/projects/`. If the function already exists under a different name, use that. The key is: given a Claude session ID, find and parse the conversation JSONL.

**Step 6: Commit**

```bash
git add internal/web/handlers_hub.go internal/web/handlers_hub_test.go internal/session/analytics.go
git commit -m "feat(web): add GET /api/tasks/{id}/analytics endpoint"
```

---

### Task 2: Backend — Restart API Endpoint

**Files:**
- Modify: `internal/web/handlers_hub.go` (add handler)
- Modify: `internal/web/hub_session_bridge.go` (add RestartTask method)
- Test: `internal/web/handlers_hub_test.go`

**Step 1: Write the failing test**

```go
func TestTaskRestartEndpoint(t *testing.T) {
	srv := newTestServerWithHub(t)

	task := &hub.Task{
		Project:     "test-proj",
		Description: "Restart test",
		Phase:       hub.PhaseExecute,
		Status:      hub.TaskStatusRunning,
	}
	require.NoError(t, srv.hubTasks.Save(task))

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/restart", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	// Without a real tmux session, restart will return OK with a status message
	// (graceful degradation — task exists but no session to restart)
	assert.Contains(t, []int{http.StatusOK, http.StatusConflict}, rr.Code)
}

func TestTaskRestartNotFound(t *testing.T) {
	srv := newTestServerWithHub(t)

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/nonexistent/restart", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race -v ./internal/web -run TestTaskRestart`
Expected: FAIL

**Step 3: Add the restart handler**

In `internal/web/handlers_hub.go`:

```go
type taskRestartResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (s *Server) handleTaskRestart(w http.ResponseWriter, r *http.Request, taskID string) {
	if s.hubTasks == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "hub not initialized")
		return
	}

	task, err := s.hubTasks.Get(taskID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}

	// Try to restart via the hub bridge
	if s.hubBridge != nil {
		if restartErr := s.hubBridge.RestartTask(task); restartErr == nil {
			writeJSON(w, http.StatusOK, taskRestartResponse{
				Status:  "restarted",
				Message: "session restarted",
			})
			return
		}
	}

	// No session to restart
	writeJSON(w, http.StatusConflict, taskRestartResponse{
		Status:  "no_session",
		Message: "no active session to restart",
	})
}
```

Wire the route in `handleTaskByID`:

```go
case "restart":
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "use POST")
		return
	}
	s.handleTaskRestart(w, r, taskID)
```

**Step 4: Add RestartTask to hub_session_bridge.go**

```go
func (b *HubSessionBridge) RestartTask(task *hub.Task) error {
	sessionID := ""
	for i := len(task.Sessions) - 1; i >= 0; i-- {
		if task.Sessions[i].ClaudeSessionID != "" {
			sessionID = task.Sessions[i].ClaudeSessionID
			break
		}
	}
	if sessionID == "" {
		return fmt.Errorf("no claude session ID found")
	}

	storage, err := b.openStorage(b.profile)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	instances, _, err := storage.LoadWithGroups()
	if err != nil {
		return fmt.Errorf("load instances: %w", err)
	}

	for _, inst := range instances {
		if inst.ClaudeSessionID == sessionID {
			if !inst.CanRestart() {
				return fmt.Errorf("session cannot be restarted")
			}
			return inst.Restart()
		}
	}

	return fmt.Errorf("session instance not found for ID %s", sessionID)
}
```

**Step 5: Run tests**

Run: `go test -race -v ./internal/web -run TestTaskRestart`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/web/handlers_hub.go internal/web/handlers_hub_test.go internal/web/hub_session_bridge.go
git commit -m "feat(web): add POST /api/tasks/{id}/restart endpoint"
```

---

### Task 3: Frontend — Search Bar

**Files:**
- Modify: `internal/web/static/dashboard.html` (add search input)
- Modify: `internal/web/static/dashboard.js` (add search state + filter logic)
- Modify: `internal/web/static/dashboard.css` (search bar styles)

**Step 1: Add search input HTML**

In `dashboard.html`, add a search bar div above the filter-bar inside `.panel-left` (before line 72):

```html
<div class="search-bar" id="search-bar">
  <input type="text" class="search-input" id="search-input"
         placeholder="Search agents..." aria-label="Search agents" />
</div>
```

**Step 2: Add search state to JS**

In `dashboard.js`, add to the `state` object (~line 5):

```javascript
searchQuery: "",
```

**Step 3: Add search input event handler**

In the `init()` function area, add:

```javascript
var searchInput = document.getElementById("search-input")
if (searchInput) {
  searchInput.addEventListener("input", function () {
    state.searchQuery = this.value.trim().toLowerCase()
    renderTaskList()
  })
}
```

**Step 4: Integrate search into renderTaskList filter**

In `renderTaskList()` (~line 1464), modify the filter logic to include search:

```javascript
var visible = state.tasks.filter(function (t) {
  if (state.projectFilter && t.project !== state.projectFilter) return false
  if (state.searchQuery) {
    var q = state.searchQuery
    // Magic prefix shortcuts
    if (q === "/waiting") return effectiveAgentStatus(t) === "waiting"
    if (q === "/running") return effectiveAgentStatus(t) === "running"
    if (q === "/idle") return effectiveAgentStatus(t) === "idle"
    if (q === "/error") return effectiveAgentStatus(t) === "error"
    // Fuzzy match on description, project, id
    var haystack = ((t.description || "") + " " + (t.project || "") + " " + (t.id || "")).toLowerCase()
    if (haystack.indexOf(q) === -1) return false
  }
  return true
})
```

**Step 5: Add CSS styles**

In `dashboard.css`, add search bar styles:

```css
.search-bar {
  padding: 6px 8px;
  border-bottom: 1px solid var(--border);
}

.search-input {
  width: 100%;
  padding: 6px 8px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 3px;
  color: var(--text);
  font: inherit;
  font-size: 0.82rem;
  outline: none;
  transition: border-color 120ms;
}

.search-input:focus {
  border-color: var(--accent);
}

.search-input::placeholder {
  color: var(--text-dim);
}
```

**Step 6: Build and verify visually**

Run: `make build && ./build/agent-deck web --headless`
Open: `http://127.0.0.1:8420`
Verify: Search bar appears above filter pills, typing filters the task list.

**Step 7: Commit**

```bash
git add internal/web/static/dashboard.html internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(web): add search bar to dashboard task list"
```

---

### Task 4: Frontend — Status Filter Strip

**Files:**
- Modify: `internal/web/static/dashboard.js` (add status filter state + render)
- Modify: `internal/web/static/dashboard.css` (status pill styles)

**Step 1: Add status filter state**

In `dashboard.js` state object, add:

```javascript
statusFilters: [],  // array of active status strings, e.g. ["running", "waiting"]
```

**Step 2: Render status filter pills in renderFilterBar()**

After the existing project pills loop in `renderFilterBar()` (~line 1405), add a second row:

```javascript
// Status filter row
var statusRow = el("div", "status-filter-row")
var statuses = [
  { key: "running",  icon: "\u25CF", label: "Running",  color: "var(--blue)" },
  { key: "waiting",  icon: "\u25D0", label: "Waiting",  color: "var(--orange)" },
  { key: "idle",     icon: "\u25CB", label: "Idle",     color: "var(--text-dim)" },
  { key: "error",    icon: "\u2715", label: "Error",    color: "var(--red)" },
]

for (var si = 0; si < statuses.length; si++) {
  var s = statuses[si]
  var isActive = state.statusFilters.indexOf(s.key) !== -1
  var sPill = el("button", "filter-pill status-pill" + (isActive ? " filter-pill--active" : ""))
  sPill.dataset.status = s.key
  var sIcon = el("span", null, s.icon)
  sIcon.style.color = s.color
  sPill.appendChild(sIcon)
  sPill.appendChild(document.createTextNode(" " + s.label))
  sPill.addEventListener("click", handleStatusFilterClick)
  statusRow.appendChild(sPill)
}

filterBar.appendChild(statusRow)
```

**Step 3: Add status filter click handler**

```javascript
function handleStatusFilterClick(e) {
  var status = e.currentTarget.dataset.status
  var idx = state.statusFilters.indexOf(status)
  if (idx === -1) {
    state.statusFilters.push(status)
  } else {
    state.statusFilters.splice(idx, 1)
  }
  renderFilterBar()
  renderTaskList()
}
```

**Step 4: Integrate status filter into renderTaskList**

Modify the filter in `renderTaskList()`:

```javascript
if (state.statusFilters.length > 0) {
  var agentSt = effectiveAgentStatus(t)
  // Map "thinking" to "running" for filter purposes
  if (agentSt === "thinking") agentSt = "running"
  if (state.statusFilters.indexOf(agentSt) === -1) return false
}
```

**Step 5: Add CSS**

```css
.status-filter-row {
  display: flex;
  gap: 4px;
  padding-top: 4px;
  flex-wrap: wrap;
}

.status-pill {
  font-size: 0.75rem;
  padding: 2px 6px;
}
```

**Step 6: Build and verify**

Run: `make build && ./build/agent-deck web --headless`
Verify: Status pills appear below project pills, clicking toggles filter, multiple can be active.

**Step 7: Commit**

```bash
git add internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(web): add status filter pills to dashboard"
```

---

### Task 5: Frontend — View Mode Selector

**Files:**
- Modify: `internal/web/static/dashboard.js`
- Modify: `internal/web/static/dashboard.css`

**Step 1: Add view mode state**

```javascript
viewMode: "tier",  // "tier" | "project" | "status"
```

**Step 2: Add view selector to filter bar**

At the end of `renderFilterBar()`, add:

```javascript
// View mode selector
var viewSelect = el("select", "view-mode-select")
var modes = [
  { key: "tier", label: "Group: Tier" },
  { key: "project", label: "Group: Project" },
  { key: "status", label: "Group: Status" },
]
for (var mi = 0; mi < modes.length; mi++) {
  var opt = el("option", null, modes[mi].label)
  opt.value = modes[mi].key
  if (modes[mi].key === state.viewMode) opt.selected = true
  viewSelect.appendChild(opt)
}
viewSelect.addEventListener("change", function () {
  state.viewMode = this.value
  renderTaskList()
})
filterBar.appendChild(viewSelect)
```

**Step 3: Modify renderTaskList to support alternate grouping**

After the existing tier-based rendering code, add branches for project and status grouping. When `state.viewMode === "project"`, group tasks by `task.project` instead of tier. When `state.viewMode === "status"`, group by `effectiveAgentStatus(task)`. Use the same collapsible section pattern (tier-section/tier-header) but with different labels.

**Step 4: Add CSS for view selector**

```css
.view-mode-select {
  margin-left: auto;
  padding: 3px 6px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 3px;
  color: var(--text);
  font: inherit;
  font-size: 0.75rem;
  cursor: pointer;
  flex-shrink: 0;
}
```

**Step 5: Build and verify**

Run: `make build && ./build/agent-deck web --headless`
Verify: Dropdown appears in filter bar, switching modes regroups the task list.

**Step 6: Commit**

```bash
git add internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(web): add view mode selector (tier/project/status grouping)"
```

---

### Task 6: Frontend — Context Menu on Task Cards

**Files:**
- Modify: `internal/web/static/dashboard.js` (add kebab menu + actions)
- Modify: `internal/web/static/dashboard.css` (context menu styles)

**Step 1: Add context menu HTML structure**

Modify `createAgentCard()` in `dashboard.js` to add a kebab button. After the `topRight` construction (~line 1576):

```javascript
// Kebab menu button
var kebab = el("button", "kebab-btn", "\u22EE")
kebab.title = "Actions"
kebab.setAttribute("aria-label", "Task actions")
kebab.addEventListener("click", function (e) {
  e.stopPropagation()
  toggleContextMenu(task, kebab)
})
topRight.appendChild(kebab)
```

**Step 2: Create context menu rendering function**

```javascript
function toggleContextMenu(task, anchor) {
  // Close any existing menu
  closeContextMenu()

  var menu = el("div", "context-menu")
  menu.id = "context-menu"

  var items = [
    { icon: "\u21BB", label: "Restart", action: function () { restartTask(task.id) } },
    { icon: "\u2442", label: "Fork", action: function () { openForkModal(task) } },
    { icon: "\u270E", label: "Rename", action: function () { startInlineRename(task) } },
    { icon: "\u2197", label: "Send to\u2026", action: function () { openSendToModal(task) } },
    { icon: "\u2015", label: "divider" },
    { icon: "\u2715", label: "Delete", action: function () { confirmDeleteTask(task.id) }, danger: true },
  ]

  for (var i = 0; i < items.length; i++) {
    var item = items[i]
    if (item.label === "divider") {
      menu.appendChild(el("div", "context-menu-divider"))
      continue
    }
    var row = el("button", "context-menu-item" + (item.danger ? " context-menu-item--danger" : ""))
    row.appendChild(el("span", "context-menu-icon", item.icon))
    row.appendChild(document.createTextNode(" " + item.label))
    ;(function (action) {
      row.addEventListener("click", function (e) {
        e.stopPropagation()
        closeContextMenu()
        action()
      })
    })(item.action)
    menu.appendChild(row)
  }

  // Position relative to anchor
  var rect = anchor.getBoundingClientRect()
  menu.style.position = "fixed"
  menu.style.top = rect.bottom + "px"
  menu.style.left = (rect.left - 120) + "px"
  document.body.appendChild(menu)

  // Close on outside click
  setTimeout(function () {
    document.addEventListener("click", closeContextMenu, { once: true })
  }, 0)
}

function closeContextMenu() {
  var existing = document.getElementById("context-menu")
  if (existing) existing.remove()
}
```

**Step 3: Add action functions (stubs first, wire later)**

```javascript
function restartTask(taskId) {
  var headers = authHeaders()
  fetch(apiPathWithToken("/api/tasks/" + taskId + "/restart"), {
    method: "POST",
    headers: headers,
  })
    .then(function (r) { return r.json() })
    .then(function () { fetchTasks() })
    .catch(function (err) { console.error("restart:", err) })
}

function confirmDeleteTask(taskId) {
  if (!confirm("Delete this task? This cannot be undone.")) return
  var headers = authHeaders()
  fetch(apiPathWithToken("/api/tasks/" + taskId), {
    method: "DELETE",
    headers: headers,
  })
    .then(function (r) {
      if (!r.ok) throw new Error("delete failed: " + r.status)
      if (state.selectedTaskId === taskId) state.selectedTaskId = null
      fetchTasks()
    })
    .catch(function (err) { console.error("delete:", err) })
}

function startInlineRename(task) {
  var newDesc = prompt("Rename task:", task.description)
  if (newDesc === null || newDesc === task.description) return
  var headers = authHeaders()
  headers["Content-Type"] = "application/json"
  fetch(apiPathWithToken("/api/tasks/" + task.id), {
    method: "PATCH",
    headers: headers,
    body: JSON.stringify({ description: newDesc }),
  })
    .then(function (r) {
      if (!r.ok) throw new Error("rename failed: " + r.status)
      fetchTasks()
    })
    .catch(function (err) { console.error("rename:", err) })
}
```

**Step 4: Add CSS for context menu**

```css
.kebab-btn {
  background: none;
  border: none;
  color: var(--text-dim);
  cursor: pointer;
  padding: 0 4px;
  font-size: 1rem;
  line-height: 1;
  border-radius: 3px;
  transition: background 100ms;
}

.kebab-btn:hover {
  background: var(--bg-card);
  color: var(--text);
}

.context-menu {
  background: var(--bg-panel);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 4px 0;
  min-width: 140px;
  box-shadow: 0 4px 12px rgba(0,0,0,0.3);
  z-index: 1000;
}

.context-menu-item {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 6px 12px;
  background: none;
  border: none;
  color: var(--text);
  font: inherit;
  font-size: 0.82rem;
  cursor: pointer;
  text-align: left;
}

.context-menu-item:hover {
  background: var(--bg-card);
}

.context-menu-item--danger {
  color: var(--red);
}

.context-menu-item--danger:hover {
  background: rgba(255,50,50,0.1);
}

.context-menu-divider {
  height: 1px;
  background: var(--border);
  margin: 4px 0;
}

.context-menu-icon {
  width: 16px;
  text-align: center;
  flex-shrink: 0;
}
```

**Step 5: Build and verify**

Run: `make build && ./build/agent-deck web --headless`
Verify: Kebab button visible on cards, clicking shows menu, actions work.

**Step 6: Commit**

```bash
git add internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(web): add context menu with task actions on agent cards"
```

---

### Task 7: Frontend — Action Toolbar in Detail Panel

**Files:**
- Modify: `internal/web/static/dashboard.html` (add action-bar div)
- Modify: `internal/web/static/dashboard.js` (render action bar)
- Modify: `internal/web/static/dashboard.css` (action bar styles)

**Step 1: Add action bar container in HTML**

In `dashboard.html`, add after the `claude-meta` div and before `detail-tabs` (~line 92):

```html
<div class="action-bar" id="action-bar"></div>
```

**Step 2: Render action bar in JS**

Add a `renderActionBar(task)` function and call it from `renderRightPanel()`:

```javascript
function renderActionBar(task) {
  var bar = document.getElementById("action-bar")
  if (!bar) return
  clearChildren(bar)

  if (!task) return

  var actions = [
    { icon: "\u21BB", label: "Restart", fn: function () { restartTask(task.id) } },
    { icon: "\u2442", label: "Fork", fn: function () { openForkModal(task) } },
    { icon: "\u270E", label: "Rename", fn: function () { startInlineRename(task) } },
    { icon: "\u2197", label: "Send to", fn: function () { openSendToModal(task) } },
  ]

  var leftGroup = el("div", "action-bar-left")
  for (var i = 0; i < actions.length; i++) {
    var btn = el("button", "action-btn", actions[i].icon + " " + actions[i].label)
    btn.title = actions[i].label
    ;(function (fn) {
      btn.addEventListener("click", fn)
    })(actions[i].fn)
    leftGroup.appendChild(btn)
  }
  bar.appendChild(leftGroup)

  // Delete button (right-aligned, danger style)
  var deleteBtn = el("button", "action-btn action-btn--danger", "\u2715 Delete")
  deleteBtn.addEventListener("click", function () { confirmDeleteTask(task.id) })
  bar.appendChild(deleteBtn)
}
```

In `renderRightPanel()`, add `renderActionBar(task)` call after `renderClaudeMeta(task)`.

**Step 3: Add CSS**

```css
.action-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 16px;
  border-bottom: 1px solid var(--border);
  gap: 6px;
  flex-shrink: 0;
}

.action-bar-left {
  display: flex;
  gap: 4px;
}

.action-btn {
  padding: 4px 8px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 3px;
  color: var(--text);
  font: inherit;
  font-size: 0.75rem;
  cursor: pointer;
  transition: background 100ms;
}

.action-btn:hover {
  background: var(--bg-panel);
  border-color: var(--accent);
}

.action-btn--danger {
  color: var(--red);
  border-color: transparent;
  background: transparent;
}

.action-btn--danger:hover {
  background: rgba(255,50,50,0.1);
  border-color: var(--red);
}
```

**Step 4: Build and verify**

Run: `make build && ./build/agent-deck web --headless`
Verify: Action bar visible above tabs when task selected, buttons trigger same operations as context menu.

**Step 5: Commit**

```bash
git add internal/web/static/dashboard.html internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(web): add action toolbar to detail panel"
```

---

### Task 8: Frontend — Analytics Tab

**Files:**
- Modify: `internal/web/static/dashboard.html` (add analytics tab button + container)
- Modify: `internal/web/static/dashboard.js` (fetch + render analytics)
- Modify: `internal/web/static/dashboard.css` (analytics panel styles)

**Step 1: Add analytics tab and container in HTML**

In `dashboard.html`, add the analytics tab button after the messages tab (~line 95):

```html
<button class="detail-tab" data-tab="analytics">Analytics</button>
```

Add the analytics container after the messages container (~line 111):

```html
<div class="analytics-container" id="analytics-container" style="display:none"></div>
```

**Step 2: Extend switchDetailTab for analytics**

In `switchDetailTab()` in dashboard.js, add an analytics case:

```javascript
var analyticsContainer = document.getElementById("analytics-container")

if (tabName === "analytics") {
  if (terminalContainer) terminalContainer.style.display = "none"
  if (messagesContainer) messagesContainer.style.display = "none"
  if (analyticsContainer) analyticsContainer.style.display = ""
  if (toolbar) toolbar.style.display = "none"
  if (state.selectedTaskId) loadAnalytics(state.selectedTaskId)
} else {
  if (analyticsContainer) analyticsContainer.style.display = "none"
}
```

**Step 3: Add analytics fetch and render**

```javascript
var analyticsTimer = null

function loadAnalytics(taskId) {
  if (analyticsTimer) clearInterval(analyticsTimer)

  fetchAnalyticsOnce(taskId)
  // Auto-refresh every 5 seconds while tab is active
  analyticsTimer = setInterval(function () {
    var activeTab = document.querySelector(".detail-tab--active")
    if (activeTab && activeTab.dataset.tab === "analytics") {
      fetchAnalyticsOnce(taskId)
    } else {
      clearInterval(analyticsTimer)
      analyticsTimer = null
    }
  }, 5000)
}

function fetchAnalyticsOnce(taskId) {
  fetch(apiPathWithToken("/api/tasks/" + taskId + "/analytics"), { headers: authHeaders() })
    .then(function (r) {
      if (!r.ok) throw new Error("analytics fetch failed: " + r.status)
      return r.json()
    })
    .then(function (data) {
      renderAnalytics(data.analytics || {})
    })
    .catch(function (err) {
      console.error("analytics:", err)
      renderAnalytics(null)
    })
}

function renderAnalytics(data) {
  var container = document.getElementById("analytics-container")
  if (!container) return
  clearChildren(container)

  if (!data || (data.inputTokens === 0 && data.outputTokens === 0)) {
    container.appendChild(el("div", "analytics-empty", "No analytics data available yet."))
    return
  }

  // Context usage bar
  var ctxSection = el("div", "analytics-section")
  ctxSection.appendChild(el("div", "analytics-label", "Context Usage"))
  var barOuter = el("div", "context-bar-outer")
  var barFill = el("div", "context-bar-fill")
  barFill.style.width = Math.min(data.contextPercent || 0, 100) + "%"
  barOuter.appendChild(barFill)
  ctxSection.appendChild(barOuter)
  ctxSection.appendChild(el("div", "analytics-sublabel",
    "Input: " + formatNumber(data.inputTokens) + " tokens  Output: " + formatNumber(data.outputTokens)))
  container.appendChild(ctxSection)

  // Metrics grid
  var grid = el("div", "analytics-grid")
  grid.appendChild(metricCard("Tool Calls", (data.toolCalls || []).reduce(function (s, t) { return s + t.count }, 0)))
  grid.appendChild(metricCard("Cost", "$" + (data.estimatedCost || 0).toFixed(2)))
  grid.appendChild(metricCard("Duration", formatDuration(null, data.durationSeconds)))
  grid.appendChild(metricCard("Turns", data.totalTurns || 0))
  grid.appendChild(metricCard("Cache Read", formatNumber(data.cacheReadTokens || 0)))
  grid.appendChild(metricCard("Cache Write", formatNumber(data.cacheWriteTokens || 0)))
  container.appendChild(grid)

  // Tool usage breakdown
  var tools = data.toolCalls || []
  if (tools.length > 0) {
    var toolSection = el("div", "analytics-section")
    toolSection.appendChild(el("div", "analytics-label", "Tool Usage"))
    var maxCount = tools.reduce(function (m, t) { return Math.max(m, t.count) }, 1)
    for (var i = 0; i < tools.length; i++) {
      var row = el("div", "tool-bar-row")
      row.appendChild(el("span", "tool-bar-name", tools[i].name))
      var barW = el("div", "tool-bar-wrapper")
      var bar = el("div", "tool-bar-fill")
      bar.style.width = ((tools[i].count / maxCount) * 100) + "%"
      barW.appendChild(bar)
      row.appendChild(barW)
      row.appendChild(el("span", "tool-bar-count", tools[i].count.toString()))
      toolSection.appendChild(row)
    }
    container.appendChild(toolSection)
  }
}

function metricCard(label, value) {
  var card = el("div", "metric-card")
  card.appendChild(el("div", "metric-value", String(value)))
  card.appendChild(el("div", "metric-label", label))
  return card
}

function formatNumber(n) {
  if (n >= 1000) return (n / 1000).toFixed(1) + "k"
  return String(n)
}
```

**Step 4: Add CSS for analytics**

```css
.analytics-container {
  flex: 1;
  overflow-y: auto;
  padding: 16px;
}

.analytics-empty {
  color: var(--text-dim);
  text-align: center;
  padding: 40px 16px;
  font-size: 0.9rem;
}

.analytics-section {
  margin-bottom: 20px;
}

.analytics-label {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--text);
  margin-bottom: 8px;
}

.analytics-sublabel {
  font-size: 0.75rem;
  color: var(--text-dim);
  margin-top: 4px;
}

.context-bar-outer {
  height: 8px;
  background: var(--bg-card);
  border-radius: 4px;
  overflow: hidden;
}

.context-bar-fill {
  height: 100%;
  background: var(--accent);
  border-radius: 4px;
  transition: width 300ms ease;
}

.analytics-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 8px;
  margin-bottom: 20px;
}

.metric-card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 8px 10px;
  text-align: center;
}

.metric-value {
  font-size: 1.1rem;
  font-weight: 600;
  color: var(--text);
}

.metric-label {
  font-size: 0.7rem;
  color: var(--text-dim);
  margin-top: 2px;
}

.tool-bar-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.tool-bar-name {
  width: 60px;
  font-size: 0.75rem;
  color: var(--text-dim);
  text-align: right;
  flex-shrink: 0;
}

.tool-bar-wrapper {
  flex: 1;
  height: 6px;
  background: var(--bg-card);
  border-radius: 3px;
  overflow: hidden;
}

.tool-bar-fill {
  height: 100%;
  background: var(--accent);
  border-radius: 3px;
}

.tool-bar-count {
  width: 24px;
  font-size: 0.75rem;
  color: var(--text-dim);
  flex-shrink: 0;
}
```

**Step 5: Build and verify**

Run: `make build && ./build/agent-deck web --headless`
Verify: Analytics tab appears, clicking it shows analytics panel. If an agent has JSONL data, metrics populate. Otherwise shows "No analytics data available yet."

**Step 6: Commit**

```bash
git add internal/web/static/dashboard.html internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(web): add analytics tab with context usage, metrics, and tool breakdown"
```

---

### Task 9: Frontend — Fork Modal

**Files:**
- Modify: `internal/web/static/dashboard.html` (add fork modal markup)
- Modify: `internal/web/static/dashboard.js` (open/close/submit fork)
- Modify: `internal/web/static/dashboard.css` (reuses existing modal styles)

**Step 1: Add fork modal HTML**

In `dashboard.html`, add after the existing add-project-modal closing div (~line 231):

```html
<!-- Fork Task modal -->
<div id="fork-task-backdrop" class="modal-backdrop" aria-hidden="true"></div>
<div id="fork-task-modal" class="modal" role="dialog" aria-label="Fork task" aria-hidden="true">
  <div class="modal-header">
    <span class="modal-title">Fork Task</span>
    <button id="fork-task-close" class="modal-close" type="button" aria-label="Close">&times;</button>
  </div>
  <div class="modal-body">
    <div class="form-group">
      <label class="modal-label">Source</label>
      <div id="fork-source" class="fork-source-label"></div>
    </div>
    <div class="form-group">
      <label class="modal-label" for="fork-title">Title</label>
      <input id="fork-title" type="text" class="form-input modal-field" />
    </div>
    <div class="form-group">
      <label class="modal-label" for="fork-project">Project</label>
      <select id="fork-project" class="hub-select modal-field"></select>
    </div>
  </div>
  <div class="modal-footer">
    <span id="fork-status" class="modal-status"></span>
    <button id="fork-cancel" class="hub-btn" type="button">Cancel</button>
    <button id="fork-submit" class="hub-btn-primary" type="button">Fork</button>
  </div>
</div>
```

**Step 2: Add JS open/close/submit**

```javascript
var forkSourceTask = null

function openForkModal(task) {
  forkSourceTask = task
  var modal = document.getElementById("fork-task-modal")
  var backdrop = document.getElementById("fork-task-backdrop")
  var source = document.getElementById("fork-source")
  var title = document.getElementById("fork-title")
  var project = document.getElementById("fork-project")

  if (source) source.textContent = task.description || task.id
  if (title) title.value = (task.description || "") + " (fork)"

  // Populate project dropdown
  if (project) {
    clearChildren(project)
    for (var i = 0; i < state.projects.length; i++) {
      var opt = el("option", null, state.projects[i].name)
      opt.value = state.projects[i].name
      if (state.projects[i].name === task.project) opt.selected = true
      project.appendChild(opt)
    }
  }

  if (modal) modal.classList.add("open")
  if (backdrop) backdrop.classList.add("open")
  if (modal) modal.setAttribute("aria-hidden", "false")
  if (title) title.focus()
}

function closeForkModal() {
  var modal = document.getElementById("fork-task-modal")
  var backdrop = document.getElementById("fork-task-backdrop")
  if (modal) modal.classList.remove("open")
  if (backdrop) backdrop.classList.remove("open")
  if (modal) modal.setAttribute("aria-hidden", "true")
  forkSourceTask = null
}

function submitFork() {
  if (!forkSourceTask) return
  var title = document.getElementById("fork-title")
  var desc = title ? title.value.trim() : ""
  if (!desc) return

  var headers = authHeaders()
  headers["Content-Type"] = "application/json"
  fetch(apiPathWithToken("/api/tasks/" + forkSourceTask.id + "/fork"), {
    method: "POST",
    headers: headers,
    body: JSON.stringify({ description: desc }),
  })
    .then(function (r) {
      if (!r.ok) throw new Error("fork failed: " + r.status)
      return r.json()
    })
    .then(function (data) {
      closeForkModal()
      fetchTasks()
      if (data.task && data.task.id) selectTask(data.task.id)
    })
    .catch(function (err) {
      console.error("fork:", err)
      var status = document.getElementById("fork-status")
      if (status) status.textContent = "Fork failed: " + err.message
    })
}
```

Wire event listeners in `init()`:

```javascript
document.getElementById("fork-task-close").addEventListener("click", closeForkModal)
document.getElementById("fork-task-backdrop").addEventListener("click", closeForkModal)
document.getElementById("fork-cancel").addEventListener("click", closeForkModal)
document.getElementById("fork-submit").addEventListener("click", submitFork)
```

**Step 3: Add fork source label CSS**

```css
.fork-source-label {
  color: var(--text-dim);
  font-size: 0.82rem;
  padding: 4px 0;
}
```

**Step 4: Build and verify**

Run: `make build && ./build/agent-deck web --headless`
Verify: Fork action from context menu or action bar opens modal, submit creates forked task.

**Step 5: Commit**

```bash
git add internal/web/static/dashboard.html internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(web): add fork task modal"
```

---

### Task 10: Frontend — Session Picker Modal (Send To)

**Files:**
- Modify: `internal/web/static/dashboard.html` (add send-to modal markup)
- Modify: `internal/web/static/dashboard.js` (open/close/submit send-to)

**Step 1: Add send-to modal HTML**

```html
<!-- Send To modal -->
<div id="send-to-backdrop" class="modal-backdrop" aria-hidden="true"></div>
<div id="send-to-modal" class="modal" role="dialog" aria-label="Send output to session" aria-hidden="true">
  <div class="modal-header">
    <span class="modal-title">Send Output To</span>
    <button id="send-to-close" class="modal-close" type="button" aria-label="Close">&times;</button>
  </div>
  <div class="modal-body">
    <input id="send-to-search" type="text" class="form-input" placeholder="Search sessions..." />
    <div id="send-to-list" class="send-to-list"></div>
  </div>
  <div class="modal-footer">
    <span id="send-to-status" class="modal-status"></span>
    <button id="send-to-cancel" class="hub-btn" type="button">Cancel</button>
    <button id="send-to-submit" class="hub-btn-primary" type="button" disabled>Send</button>
  </div>
</div>
```

**Step 2: Add JS for send-to modal**

```javascript
var sendToSourceTask = null
var sendToTargetId = null

function openSendToModal(task) {
  sendToSourceTask = task
  sendToTargetId = null
  var modal = document.getElementById("send-to-modal")
  var backdrop = document.getElementById("send-to-backdrop")
  var search = document.getElementById("send-to-search")

  if (search) search.value = ""
  renderSendToList("")
  document.getElementById("send-to-submit").disabled = true

  if (modal) modal.classList.add("open")
  if (backdrop) backdrop.classList.add("open")
  if (modal) modal.setAttribute("aria-hidden", "false")
  if (search) search.focus()
}

function renderSendToList(query) {
  var list = document.getElementById("send-to-list")
  if (!list) return
  clearChildren(list)

  var q = query.toLowerCase()
  for (var i = 0; i < state.tasks.length; i++) {
    var t = state.tasks[i]
    if (sendToSourceTask && t.id === sendToSourceTask.id) continue
    if (q && (t.description + " " + t.project + " " + t.id).toLowerCase().indexOf(q) === -1) continue

    var row = el("button", "send-to-item" + (sendToTargetId === t.id ? " send-to-item--selected" : ""))
    var status = effectiveAgentStatus(t)
    var meta = AGENT_STATUS_META[status] || AGENT_STATUS_META.idle
    var dot = el("span", null, meta.icon)
    dot.style.color = meta.color
    row.appendChild(dot)
    row.appendChild(document.createTextNode(" " + (t.project || "") + " / " + t.id + "  " + meta.label))
    ;(function (id) {
      row.addEventListener("click", function () {
        sendToTargetId = id
        renderSendToList(document.getElementById("send-to-search").value)
        document.getElementById("send-to-submit").disabled = false
      })
    })(t.id)
    list.appendChild(row)
  }
}

function closeSendToModal() {
  var modal = document.getElementById("send-to-modal")
  var backdrop = document.getElementById("send-to-backdrop")
  if (modal) modal.classList.remove("open")
  if (backdrop) backdrop.classList.remove("open")
  if (modal) modal.setAttribute("aria-hidden", "true")
  sendToSourceTask = null
  sendToTargetId = null
}

function submitSendTo() {
  if (!sendToSourceTask || !sendToTargetId) return
  // Send the source task's description as input to the target task
  var input = "Output from " + sendToSourceTask.id + ": " + (sendToSourceTask.description || "")
  var headers = authHeaders()
  headers["Content-Type"] = "application/json"
  fetch(apiPathWithToken("/api/tasks/" + sendToTargetId + "/input"), {
    method: "POST",
    headers: headers,
    body: JSON.stringify({ input: input }),
  })
    .then(function (r) {
      if (!r.ok) throw new Error("send failed: " + r.status)
      closeSendToModal()
    })
    .catch(function (err) {
      console.error("send:", err)
      var status = document.getElementById("send-to-status")
      if (status) status.textContent = "Send failed: " + err.message
    })
}
```

Wire listeners and search input handler in `init()`.

**Step 3: Add CSS**

```css
.send-to-list {
  max-height: 200px;
  overflow-y: auto;
  margin-top: 8px;
}

.send-to-item {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 8px 10px;
  background: none;
  border: 1px solid transparent;
  border-radius: 3px;
  color: var(--text);
  font: inherit;
  font-size: 0.82rem;
  cursor: pointer;
  text-align: left;
}

.send-to-item:hover {
  background: var(--bg-card);
}

.send-to-item--selected {
  border-color: var(--accent);
  background: rgba(232, 169, 50, 0.08);
}
```

**Step 4: Build and verify**

Run: `make build && ./build/agent-deck web --headless`
Verify: "Send to" action opens picker, search filters, selecting + send works.

**Step 5: Commit**

```bash
git add internal/web/static/dashboard.html internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(web): add session picker modal for cross-task output sharing"
```

---

### Task 11: Frontend — Enhanced New Task Modal

**Files:**
- Modify: `internal/web/static/dashboard.html` (add new form fields)
- Modify: `internal/web/static/dashboard.js` (wire new fields into create)

**Step 1: Add tool, group, and advanced section to new task modal**

In `dashboard.html` inside the `new-task-modal` body (after the route-suggestion div, ~line 149):

```html
<div class="form-section-header" id="new-task-agent-config-header">Agent Config</div>
<div class="form-group">
  <label class="modal-label" for="new-task-tool">Tool</label>
  <select id="new-task-tool" class="hub-select modal-field">
    <option value="claude">Claude</option>
    <option value="gemini">Gemini</option>
    <option value="opencode">OpenCode</option>
    <option value="codex">Codex</option>
  </select>
</div>

<div class="form-section-header collapsible" id="new-task-advanced-toggle">
  Advanced <span class="toggle-caret">&#x25BE;</span>
</div>
<div id="new-task-advanced" class="form-section-collapsed">
  <div class="form-group">
    <label class="radio-label">
      <input type="checkbox" id="new-task-worktree" /> Create in worktree
    </label>
  </div>
  <div class="form-group" id="new-task-branch-group" style="display:none">
    <label class="modal-label" for="new-task-branch">Branch</label>
    <input id="new-task-branch" type="text" class="form-input modal-field" placeholder="feat/my-feature" />
  </div>
</div>
```

Move the existing phase selector inside the advanced section.

**Step 2: Wire toggle and worktree checkbox in JS**

```javascript
// Advanced section toggle
var advToggle = document.getElementById("new-task-advanced-toggle")
var advSection = document.getElementById("new-task-advanced")
if (advToggle && advSection) {
  advToggle.addEventListener("click", function () {
    advSection.classList.toggle("form-section-collapsed")
    advToggle.classList.toggle("form-section-open")
  })
}

// Worktree checkbox shows/hides branch input
var worktreeCheck = document.getElementById("new-task-worktree")
var branchGroup = document.getElementById("new-task-branch-group")
if (worktreeCheck && branchGroup) {
  worktreeCheck.addEventListener("change", function () {
    branchGroup.style.display = this.checked ? "" : "none"
  })
}
```

**Step 3: Include new fields in task creation payload**

Modify `submitNewTask()` to include tool and branch in the request body. The `createTaskRequest` Go struct already accepts `branch`. For `tool`, it will be used by the hub bridge when starting the session.

**Step 4: Add CSS for collapsible sections**

```css
.form-section-header {
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--text-dim);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-top: 12px;
  margin-bottom: 6px;
  padding-bottom: 4px;
  border-bottom: 1px solid var(--border);
}

.form-section-header.collapsible {
  cursor: pointer;
}

.form-section-collapsed {
  display: none;
}

.form-section-open + .form-section-collapsed {
  display: block;
}

.toggle-caret {
  float: right;
  transition: transform 150ms;
}

.form-section-open .toggle-caret {
  transform: rotate(180deg);
}
```

**Step 5: Build and verify**

Run: `make build && ./build/agent-deck web --headless`
Verify: New task modal has tool selector, collapsible advanced section with worktree + branch.

**Step 6: Commit**

```bash
git add internal/web/static/dashboard.html internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(web): enhance new task modal with tool selector and advanced options"
```

---

### Task 12: Frontend — Notification Badge

**Files:**
- Modify: `internal/web/static/dashboard.js` (compute + render badge)
- Modify: `internal/web/static/dashboard.css` (badge styles)

**Step 1: Add notification badge to renderTopBar()**

Find the existing `renderTopBar()` function and add a notification count:

```javascript
function renderTopBar() {
  var topBarRight = document.getElementById("top-bar-right")
  if (!topBarRight) return
  clearChildren(topBarRight)

  // Notification badge: count of needs-attention agents
  var attentionCount = 0
  for (var i = 0; i < state.tasks.length; i++) {
    var s = effectiveAgentStatus(state.tasks[i])
    if (s === "waiting" || s === "error") attentionCount++
  }
  if (attentionCount > 0) {
    var notif = el("button", "notification-badge")
    notif.textContent = attentionCount + " waiting"
    notif.title = "Click to scroll to needs-attention agents"
    notif.addEventListener("click", scrollToNeedsAttention)
    topBarRight.appendChild(notif)
  }

  // Agent count
  var dot = el("span", "top-bar-agent-dot")
  topBarRight.appendChild(el("span", "top-bar-agent-indicator"))
  topBarRight.lastChild.appendChild(dot)
  topBarRight.lastChild.appendChild(document.createTextNode(" " + state.tasks.length))
}

function scrollToNeedsAttention() {
  var section = document.querySelector('.tier-section[data-tier="needsAttention"]')
  if (section) section.scrollIntoView({ behavior: "smooth", block: "start" })
}
```

**Step 2: Add CSS**

```css
.notification-badge {
  background: rgba(232, 169, 50, 0.15);
  color: var(--orange);
  border: 1px solid rgba(232, 169, 50, 0.3);
  border-radius: 3px;
  padding: 2px 8px;
  font: inherit;
  font-size: 0.75rem;
  font-weight: 600;
  cursor: pointer;
  margin-right: 8px;
  animation: pulse-badge 2s infinite;
}

@keyframes pulse-badge {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.7; }
}

.notification-badge:hover {
  background: rgba(232, 169, 50, 0.25);
}
```

**Step 3: Build and verify**

Run: `make build && ./build/agent-deck web --headless`
Verify: When agents are in waiting/error state, badge appears in top bar and pulses. Clicking scrolls to them.

**Step 4: Commit**

```bash
git add internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(web): add notification badge for needs-attention agents"
```

---

### Task 13: Frontend — Help Panel

**Files:**
- Modify: `internal/web/static/dashboard.html` (add help button + modal)
- Modify: `internal/web/static/dashboard.js` (open/close help)
- Modify: `internal/web/static/dashboard.css` (help panel styles)

**Step 1: Add help button to sidebar**

In `dashboard.html`, inside the `.sidebar-bottom` div (~line 48), add before the status line:

```html
<button class="sidebar-help-btn" id="help-btn" title="Help" aria-label="Help">?</button>
```

**Step 2: Add help modal HTML**

```html
<!-- Help modal -->
<div id="help-backdrop" class="modal-backdrop" aria-hidden="true"></div>
<div id="help-modal" class="modal modal--wide" role="dialog" aria-label="Help" aria-hidden="true">
  <div class="modal-header">
    <span class="modal-title">Agent Deck Help</span>
    <button id="help-close" class="modal-close" type="button" aria-label="Close">&times;</button>
  </div>
  <div class="modal-body help-body">
    <h3>Task Management</h3>
    <dl class="help-list">
      <dt>Create task</dt><dd>Type in chat bar or click task card &rarr; context menu</dd>
      <dt>Fork task</dt><dd>Context menu &rarr; Fork, or Action bar &rarr; Fork</dd>
      <dt>Restart</dt><dd>Context menu or Action bar &rarr; Restart</dd>
      <dt>Rename</dt><dd>Context menu or Action bar &rarr; Rename</dd>
      <dt>Delete</dt><dd>Context menu or Action bar &rarr; Delete</dd>
      <dt>Send output</dt><dd>Context menu or Action bar &rarr; Send to...</dd>
    </dl>
    <h3>Filtering</h3>
    <dl class="help-list">
      <dt>By project</dt><dd>Click project pills in filter bar</dd>
      <dt>By status</dt><dd>Click status pills (Running, Waiting, Idle, Error)</dd>
      <dt>Search</dt><dd>Type in search bar. Use /waiting, /running, /idle, /error</dd>
      <dt>View mode</dt><dd>Use dropdown to group by Tier, Project, or Status</dd>
    </dl>
    <h3>Detail View</h3>
    <dl class="help-list">
      <dt>Terminal</dt><dd>Live session output with font size controls</dd>
      <dt>Messages</dt><dd>Conversation history</dd>
      <dt>Analytics</dt><dd>Token usage, cost, tool breakdown</dd>
    </dl>
  </div>
</div>
```

**Step 3: Wire JS**

```javascript
document.getElementById("help-btn").addEventListener("click", function () {
  document.getElementById("help-modal").classList.add("open")
  document.getElementById("help-backdrop").classList.add("open")
})
document.getElementById("help-close").addEventListener("click", function () {
  document.getElementById("help-modal").classList.remove("open")
  document.getElementById("help-backdrop").classList.remove("open")
})
document.getElementById("help-backdrop").addEventListener("click", function () {
  document.getElementById("help-modal").classList.remove("open")
  this.classList.remove("open")
})
```

**Step 4: Add CSS**

```css
.sidebar-help-btn {
  width: 28px;
  height: 28px;
  border-radius: 50%;
  border: 1px solid var(--border);
  background: transparent;
  color: var(--text-dim);
  font-weight: 600;
  font-size: 0.85rem;
  cursor: pointer;
  transition: background 100ms, color 100ms;
  margin-bottom: 6px;
}

.sidebar-help-btn:hover {
  background: var(--bg-card);
  color: var(--text);
}

.modal--wide {
  max-width: 500px;
}

.help-body {
  max-height: 60vh;
  overflow-y: auto;
}

.help-body h3 {
  font-size: 0.85rem;
  margin: 16px 0 8px;
  color: var(--accent);
}

.help-body h3:first-child {
  margin-top: 0;
}

.help-list {
  margin: 0;
}

.help-list dt {
  font-weight: 600;
  font-size: 0.82rem;
  float: left;
  width: 120px;
  color: var(--text);
}

.help-list dd {
  font-size: 0.82rem;
  color: var(--text-dim);
  margin-left: 130px;
  margin-bottom: 6px;
}
```

**Step 5: Build and verify**

Run: `make build && ./build/agent-deck web --headless`
Verify: `?` button in sidebar bottom opens help modal with all operations listed.

**Step 6: Commit**

```bash
git add internal/web/static/dashboard.html internal/web/static/dashboard.js internal/web/static/dashboard.css
git commit -m "feat(web): add help panel with operations reference"
```

---

### Task 14: Final Integration — Build, Verify, Run All Tests

**Step 1: Run Go tests**

Run: `go test -race -v ./internal/web/...`
Expected: All tests pass including new analytics and restart tests.

**Step 2: Run full test suite**

Run: `make test`
Expected: All tests pass.

**Step 3: Run lint**

Run: `make lint`
Expected: No lint errors.

**Step 4: Full visual verification**

Run: `make build && ./build/agent-deck web --headless`
Open: `http://127.0.0.1:8420`

Verify all features:
- [ ] Search bar filters tasks
- [ ] Status pills filter by agent status
- [ ] View mode dropdown regroups tasks
- [ ] Kebab context menu on each card with all actions
- [ ] Action bar in detail view with all actions
- [ ] Analytics tab shows data (or empty state)
- [ ] Fork modal opens pre-filled, creates forked task
- [ ] Send-to modal lists tasks, sends input
- [ ] New task modal has tool selector + advanced section
- [ ] Notification badge shows waiting count, clicks scroll
- [ ] Help button opens reference modal

**Step 5: Final commit**

```bash
git commit --allow-empty -m "feat(web): web workflow UI enhancements complete

Closes the TUI-to-web feature gap for session lifecycle, status
filtering, analytics, notifications, and help."
```
