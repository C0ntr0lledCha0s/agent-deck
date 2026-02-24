# Hub UI Alignment Design

**Date:** 2026-02-24
**Status:** Approved
**Purpose:** Close the gaps between the current web UI implementation and the design documents (ROADMAP.md, hub-v3.jsx, architecture-analysis-consolidated.md).
**Approach:** Parallel tracks with ordered merge.

---

## Context

A reconciliation of the current dashboard implementation against the design documents identified ~15 gaps across data model, views, visual design, and features. This design defines three parallel implementation tracks to close those gaps.

### Scope

**In scope:**
- Data model refactor (separate TaskStatus from AgentStatus, add Session model)
- Dark theme matching hub-v3.jsx color system
- Agents view redesign (two-panel layout with embedded xterm.js)
- Sidebar navigation with view stubs
- Context-aware chat input with mode auto-detection
- AskUserQuestion surfacing
- Session chain visualization

**Out of scope (deferred):**
- Kanban view, Conductor view, Workspaces view, Brainstorm view (stubs only)
- Slash command palette
- Diff panel with approve/reject
- Conductor API endpoints
- Workspace CRUD endpoints

---

## Track Structure

```
Track A: refactor/task-data-model       (Go backend — models, store, handlers, tests)
Track B: feature/agents-view-redesign   (HTML + CSS + JS — dark theme, layout, xterm.js)
Track C: feature/smart-chat-input       (JS — chat component, mode switching)

Merge order:
  Track A (model)  ─┐
                    ├─→  Track B (UI redesign) ─→  Track C (chat)
                    │
  Tracks A and B can be worked in parallel.
  Track B initially works against the old API shape, then rebases onto Track A.
  Track C depends on Track B's layout.
```

---

## Track A: Data Model Refactor

### Files Changed

- `internal/hub/models.go` — new types, restructured Task
- `internal/hub/store.go` — migration logic for old-format JSON
- `internal/web/handlers_hub.go` — updated request/response handling
- `internal/web/handlers_hub_test.go` — updated test expectations
- `internal/hub/store_test.go` — migration tests

### Model Changes

**Separate TaskStatus (workflow stage) from AgentStatus (what Claude is doing):**

```go
// TaskStatus — workflow stage (kanban column)
type TaskStatus string
const (
    TaskStatusBacklog  TaskStatus = "backlog"
    TaskStatusPlanning TaskStatus = "planning"
    TaskStatusRunning  TaskStatus = "running"
    TaskStatusReview   TaskStatus = "review"
    TaskStatusDone     TaskStatus = "done"
)

// AgentStatus — what Claude is doing right now
type AgentStatus string
const (
    AgentStatusThinking AgentStatus = "thinking"
    AgentStatusWaiting  AgentStatus = "waiting"
    AgentStatusRunning  AgentStatus = "running"
    AgentStatusIdle     AgentStatus = "idle"
    AgentStatusError    AgentStatus = "error"
    AgentStatusComplete AgentStatus = "complete"
)
```

**New Task struct:**

```go
type Task struct {
    ID           string      `json:"id"`
    SessionID    string      `json:"sessionId"`
    TmuxSession  string      `json:"tmuxSession,omitempty"`
    Status       TaskStatus  `json:"status"`
    AgentStatus  AgentStatus `json:"agentStatus"`
    Project      string      `json:"project"`
    Description  string      `json:"description"`
    Phase        Phase       `json:"phase"`
    Branch       string      `json:"branch,omitempty"`
    Skills       []string    `json:"skills,omitempty"`
    MCPs         []string    `json:"mcps,omitempty"`
    Diff         *DiffInfo   `json:"diff,omitempty"`
    Container    string      `json:"container,omitempty"`
    AskQuestion  string      `json:"askQuestion,omitempty"`
    Sessions     []Session   `json:"sessions,omitempty"`
    CreatedAt    time.Time   `json:"createdAt"`
    UpdatedAt    time.Time   `json:"updatedAt"`
    ParentTaskID string      `json:"parentTaskId,omitempty"`
}
```

**New Session model:**

```go
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

**New DiffInfo model:**

```go
type DiffInfo struct {
    Files int `json:"files"`
    Add   int `json:"add"`
    Del   int `json:"del"`
}
```

### Backward Compatibility

Existing task JSON files use `"status": "thinking"` (agent-level values). The store loader detects old-format statuses and migrates on read:

| Old status value | New TaskStatus | New AgentStatus |
|-----------------|---------------|-----------------|
| `thinking` | `running` | `thinking` |
| `waiting` | `planning` | `waiting` |
| `running` | `running` | `running` |
| `idle` | `backlog` | `idle` |
| `error` | `running` | `error` |
| `complete` | `done` | `complete` |

Migration happens transparently on `Load()`. The migrated task is written back to disk on next `Save()`.

API responses include both `status` and `agentStatus`. New fields default to zero values — existing JSON files load without error.

---

## Track B: Agents View Redesign

### Files Changed

- `internal/web/static/dashboard.html` — restructured layout
- `internal/web/static/dashboard.css` — dark theme, two-panel layout, all component styles
- `internal/web/static/dashboard.js` — rewritten with component functions
- `internal/web/static/styles.css` — shared CSS variable updates for dark theme

### Layout

Two-panel layout with sidebar stubs:

```
┌──┬────────────┬─────────────────────────────────────┐
│  │ TASK LIST   │ TASK DETAIL                          │
│S │ ┌─────────┐ │ ┌─────────────────────────────────┐  │
│I │ │ Filter  │ │ │ Task Header                     │  │
│D │ └─────────┘ │ │ project · t-007 → branch        │  │
│E │ Active · 3  │ ├─────────────────────────────────┤  │
│B │ ▣ web-app   │ │ Session Chain [B][P][E][R]       │  │
│A │ ┌─────────┐ │ ├─────────────────────────────────┤  │
│R │ │ t-007 ● │ │ │ Preview Header                  │  │
│  │ │ t-006 ✓ │ │ │ web-app  ● thinking             │  │
│  │ └─────────┘ │ ├─────────────────────────────────┤  │
│  │ ▣ api-svc   │ │                                 │  │
│  │ ┌─────────┐ │ │ TERMINAL (xterm.js)             │  │
│  │ │ t-005 ◐ │ │ │                                 │  │
│  │ └─────────┘ │ │                                 │  │
│  │             │ │                                 │  │
│  │ Completed·2 │ └─────────────────────────────────┘  │
├──┴────────────┴─────────────────────────────────────┤
│ [↩ t-007/execute ▾] Type message...          [Send] │
└─────────────────────────────────────────────────────┘
```

**Sidebar (56px):** 5 view icons (Agents ⟐, Kanban ▦, Conductor ◎, Workspaces ▣, Brainstorm ◇). Only Agents is functional — others show placeholder. Bottom shows NUC connection status and active agent count.

**Left panel (280px, desktop):** Filter bar with project pills and group-by-project toggle. Tasks split into "Active" and "Completed" sections with counts. AgentCard components with left border colored by task status, agent status badge, mini session chain bars.

**Right panel (flex):** Task header (description, status badge, Attach/SSH/IDE buttons), session chain (phase pip navigator with duration and artifact labels), preview header (project name, agent status, workspace path, active skills), then xterm.js terminal filling remaining space.

**Empty state (no task selected):** Centered icon and "Select an agent to preview" text per hub-v3.jsx.

### Dark Theme

CSS custom properties replacing the current light theme:

```css
:root {
    --bg: #0b0d11;
    --bg-card: #10131a;
    --bg-panel: #0e1018;
    --border: #1a1e2a;
    --border-light: #222838;
    --text: #dce4f0;
    --text-mid: #8b95aa;
    --text-dim: #4a5368;
    --amber: #e8a932;
    --green: #2dd4a0;
    --red: #f06060;
    --purple: #8b8cf8;
    --blue: #4ca8e8;
    --orange: #f59e0b;
    --term-bg: #080a0e;
    --term-text: #c8d0dc;
    --font-mono: 'JetBrains Mono', 'Fira Code', monospace;
    --font-sans: 'IBM Plex Sans', -apple-system, sans-serif;
}
```

Phase colors: brainstorm `#c084fc`, plan `#8b8cf8`, execute `#e8a932`, review `#4ca8e8`, done `#2dd4a0`.

### xterm.js Embedding

Move the existing xterm.js terminal from the standalone `/terminal` page into the right panel. The WebSocket connection (`/ws/session/{id}`) already exists.

Behaviors:
- Selecting a task disconnects current WebSocket, connects to new task's session
- No task selected → no terminal, show empty state
- Terminal resizes with panel via existing FitAddon
- Standalone `/terminal` page remains available for full-screen use

### Component Functions (Vanilla JS)

Rewrite `dashboard.js` as component-oriented functions:

| hub-v3.jsx Component | dashboard.js Function |
|----------------------|----------------------|
| `Sidebar` | `renderSidebar(view)` |
| `FilterBar` | `renderFilterBar(filter, projects)` |
| `AgentCard` | `createAgentCard(task, isActive)` |
| `AgentStatusBadge` | `createAgentStatusBadge(status)` |
| `SessionChain` | `renderSessionChain(task, activeSessionId)` |
| `TmuxPreview` header | `renderPreviewHeader(task, session)` |

State managed in single `state` object (same pattern as current).

### Responsive Design

- Breakpoint at 768px
- Mobile: sidebar becomes horizontal bottom nav, panels stack vertically (list OR detail, not both), "← Back" button on detail view
- Filter bar horizontally scrollable on mobile

---

## Track C: Context-Aware Chat Input

### Files Changed

- `dashboard.js` — new `SmartChatInput` component section
- `dashboard.css` — chat input styles, override menu styles

### Chat Modes

| Mode | Icon | Color | Trigger | Action |
|------|------|-------|---------|--------|
| Reply ↩ | `--amber` | Task selected + active session | `POST /api/tasks/{id}/input` |
| New + | `--blue` | No task selected / task is done | `POST /api/tasks` |
| Conductor ◎ | `--purple` | Conductor view (future) | `POST /conductor/message` |

### Auto-Detection Logic

```
if (view === "agents" && activeTask has active session)
    → Reply mode: "↩ {taskId} / {phase}"
else if (view === "agents" && activeTask but no active session)
    → New in same project: "+ {project}"
else if (view === "agents" && projectFilter !== "all")
    → New in filtered project: "+ {project}"
else if (view === "agents")
    → New with auto-route: "+ auto-route"
else if (view === "conductor")
    → Conductor mode: "◎ conductor-ops"
```

### Mode Override Menu

Clicking the mode button opens a dropdown:
- "New in {project}" — new task, same project
- "New (auto-route)" — conductor picks project
- "New in {other-project}" — for each other workspace
- "◎ Message conductor" — switch to conductor mode
- "← Back to: {auto}" — when manually overridden, return to auto-detection

Override clears when navigation changes.

### AskUserQuestion Surfacing

When selected task has `agentStatus: "waiting"` and `askQuestion` is set:
- Orange banner above input: `◐ Agent is asking: {question}`
- Placeholder changes to: `Answer: {question}`
- Task card shows pulsing orange `◐ INPUT` badge

### Layout

Pinned to bottom of main content area, full width, always visible:

```
┌──────────────────────────────────────────────────────┐
│ [↩ t-007/execute ▾]  Type message...          [Send] │
│  via tmux send-keys → ad-web-app-fix-auth            │
└──────────────────────────────────────────────────────┘
```

### Deferred (Not in This Track)

- Slash command palette (`/new`, `/fork`, `/compact`, etc.)
- Project skills commands
- Brainstorm mode

---

## What's NOT Changing

- `/terminal` page (index.html) — remains as standalone full-screen terminal
- WebSocket terminal bridge (`terminal_bridge.go`) — reused for embedded xterm.js
- Push notification system — untouched
- Service worker / PWA — untouched
- Agent Deck menu integration (`/api/menu`, `/api/session/`) — untouched
- All existing API endpoints — backward compatible
- Authentication — untouched

---

## Decisions Made

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Frontend technology | Keep vanilla JS | No build step, embeds in Go binary, works |
| Theme | Dark (matching hub-v3.jsx) | Terminal-native look, design alignment |
| View scope | Agents view only (others as stubs) | Focus on getting primary view right |
| Data model | Fix model first (parallel track) | Foundation for all future views |
| Terminal preview | Embedded xterm.js (interactive) | Reuses existing WebSocket bridge |
| Standalone terminal | Keep `/terminal` page | Full-screen option |
| Chat input | Full context-aware, no slash commands | Core mobile UX, defer command palette |
| Implementation | Parallel tracks with ordered merge | Visual progress + backend progress simultaneously |
