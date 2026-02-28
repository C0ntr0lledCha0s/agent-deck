# Web Workflow UI Design

**Date**: 2026-02-28
**Status**: Approved
**Approach**: Enhanced Dashboard (evolve existing `dashboard.html`)

## Problem

The web dashboard is missing core workflow capabilities compared to the TUI. The TUI has 60+ keybindings for session lifecycle, status filtering, analytics, MCP/skills management, search, and group management. The web focuses on Hub task management but lacks session operations, status filtering, and analytics.

## Design Decisions

- **Paradigm**: Task-centric (not session-centric). Tasks and sessions are intrinsically linked.
- **Priority features**: Session lifecycle, status/filtering, analytics. MCP/Skills deferred.
- **Analytics layout**: Tabbed panels (Terminal | Messages | Analytics).
- **Operations access**: Both context menus on task cards AND action bar in detail view.

## Changes

### 1. Task List Enhancements (Left Panel)

#### 1a. Status Filter Strip

Below the existing project filter pills, add toggleable status filter buttons:

```
┌─ Filter Bar ─────────────────────────────┐
│ [All] [agent-deck] [other-proj] [+Proj]  │  existing project pills
│ [● Running] [◐ Waiting] [○ Idle] [✕ Err] │  NEW status toggles
└──────────────────────────────────────────┘
```

- Multiple statuses can be active simultaneously (additive with project filter)
- Colors match `AGENT_STATUS_META` definitions
- Clicking when all active = deselect all (show all)

#### 1b. Search Bar

Add a search input above the filter bar:

```
┌─ Search ─────────────────────────────────┐
│ 🔍 Search agents...                      │
└──────────────────────────────────────────┘
```

- Fuzzy-matches against task description, project name, task ID
- Real-time filtering as you type
- Magic prefixes: `/waiting`, `/running`, `/idle` trigger status filters

#### 1c. Context Menu on Task Cards

Each task card gets a kebab menu button in the top-right:

```
┌─ agent-card ──────────────────┐
│ agent-deck    ◐ Input   ⋮     │
│ Implement auth feature        │
│ t-3 · 4m ago    ██░          │
└───────────────────────────────┘
```

Menu items:
- Restart: Kill + recreate tmux session
- Fork: Open fork modal (pre-filled from parent)
- Rename: Inline edit of task description
- Send to...: Open session picker modal
- Delete: With confirmation dialog

#### 1d. View Mode Selector

Add a view dropdown in the filter bar:

```
View: [Tier ▾]
```

Options:
- **Tier** (default): Needs Attention / Active / Recent / Idle
- **Project**: Group by project name, collapsible sections
- **Status**: Group by agent status

Client-side only, no backend changes needed.

### 2. Detail Panel Enhancements (Right Panel)

#### 2a. Action Toolbar

Row of icon buttons between detail header and tabs:

```
┌─ Detail Header ──────────────────────────────┐
│ ← Back   Implement auth feature   ◐ Waiting  │
│ agent-deck / t-3  → feat/auth  tmux: ad-t-3  │
├─ Action Bar ─────────────────────────────────┤
│ [↻ Restart] [⑂ Fork] [✎ Rename] [↗ Send]    │
│                              [✕ Delete]       │
├──────────────────────────────────────────────┤
│ [Terminal]  [Messages]  [Analytics]           │
```

- Same operations as context menu
- Delete right-aligned, red on hover, visually separated
- Icon + short label, compact row

#### 2b. Analytics Tab

Third tab alongside Terminal and Messages:

```
┌─ Analytics ──────────────────────────────────┐
│                                              │
│  Context Usage                               │
│  ████████████▓▓▓░░░░░░░░░░░░░░░ 42%        │
│  Input: 84,230 tokens  Output: 12,440       │
│                                              │
│  ┌─ Metrics ───────────────────────────────┐ │
│  │ Tool Calls    23      Cost    $0.42     │ │
│  │ Duration      4m 32s  Model   opus-4    │ │
│  │ Cache Read    12,340  Write   3,200     │ │
│  └─────────────────────────────────────────┘ │
│                                              │
│  Tool Usage Breakdown                        │
│  Read ████████ 8                             │
│  Edit ██████ 6                               │
│  Bash █████ 5                                │
│  Grep ████ 4                                 │
│                                              │
└──────────────────────────────────────────────┘
```

Data:
- Context bar: horizontal progress showing input (solid) + output (lighter) as % of model context window
- Metrics grid: tool calls, cost, duration, model, cache stats
- Tool usage: horizontal bar chart of tool call counts
- Gemini tasks: show Gemini-specific metrics (same adapter pattern as TUI)
- Auto-refreshes while tab is active (poll every 5s)

### 3. New Task Modal Enhancements

Extend the existing modal:

```
┌─ New Task ──────────────────────── ✕ ─┐
│                                        │
│  Project     [agent-deck      ▾]       │
│  Description [____________________]    │
│              [____________________]    │
│  Route suggestion: agent-deck (95%)    │
│                                        │
│  ── Agent Config ──────────────────    │
│  Tool  [Claude ▾]                      │
│  Group [myproject ▾]                   │
│                                        │
│  ── Advanced ──────────────────────    │
│  □ Create in worktree                  │
│    Branch: [feat/auth_______]          │
│  Phase  [Execute ▾]                    │
│                                        │
│            [Cancel]  [Create]          │
└────────────────────────────────────────┘
```

New fields:
- Tool selector: Claude / Gemini / OpenCode / Codex
- Group selector: existing groups + "New Group..."
- Worktree toggle: checkbox reveals branch name input
- Advanced section: collapsed by default

### 4. Fork Modal

```
┌─ Fork Task ─────────────────────── ✕ ─┐
│                                        │
│  Source: "Implement auth feature"      │
│                                        │
│  Title  [Implement auth feature (fork)]│
│  Project [agent-deck ▾]               │
│  □ Create in new worktree              │
│                                        │
│            [Cancel]  [Fork]            │
└────────────────────────────────────────┘
```

### 5. Session Picker Modal (Send To)

```
┌─ Send Output To ────────────── ✕ ─┐
│  🔍 Search sessions...             │
│                                    │
│  ● agent-deck / t-1  Running      │
│  ◐ other-proj / t-4  Waiting      │
│  ○ tools / t-7        Idle        │
│                                    │
│             [Cancel]  [Send]       │
└────────────────────────────────────┘
```

### 6. Notifications

Top bar notification badge:

```
┌─ Top Bar ────────────────────────────────────┐
│ agents                    🔔 2 waiting  ● 5  │
└──────────────────────────────────────────────┘
```

- Shows count of "Needs Attention" agents (waiting + error)
- Click scrolls to first Needs Attention card
- Pulses on new waiting state transitions
- Reuses existing push notification infrastructure

### 7. Help Panel

`?` button in sidebar bottom opens a reference overlay listing available operations and where to find them in the UI.

## Backend API Changes

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/api/tasks/{id}/analytics` | GET | Analytics data (context, tokens, cost, tools) |
| `/api/tasks/{id}/restart` | POST | Kill + recreate tmux session |
| `/api/tasks/{id}/rename` | PATCH | Update task description/title |
| `/api/tasks/{id}/fork` | POST | Exists — enhance with tool/group params |

The analytics endpoint wraps `session.GetAnalytics()` which the TUI already uses. Restart maps to existing session restart logic in the `session` package.

## Complete Layout

```
┌────────────────────────────────────────────────────────────────┐
│ ╔═══╗                                                          │
│ ║ A ║  agents                              🔔 2 waiting  ● 5  │
│ ║ K ║ ┌──────────────────┬─────────────────────────────────┐   │
│ ║ C ║ │ 🔍 Search...     │ Implement auth feature ◐ Wait  │   │
│ ║ W ║ │                  │ agent-deck / t-3 → feat/auth    │   │
│ ║ B ║ │ [All] [ad] [+P]  │                                 │   │
│ ║   ║ │ [●Run][◐Wait][○] │ [↻Restart][⑂Fork][✎][↗][✕Del] │   │
│ ║   ║ │ View: [Tier ▾]   │                                 │   │
│ ║   ║ │                  │ [Terminal] [Messages] [Analytics]│   │
│ ║   ║ │ ▸ Needs Attn (2) │ ┌─────────────────────────────┐ │   │
│ ║   ║ │   ◐ auth feat    │ │ $ claude                    │ │   │
│ ║   ║ │   ✕ api tests    │ │ > Working on auth...        │ │   │
│ ║   ║ │ ▸ Active (1)     │ │                             │ │   │
│ ║   ║ │   ● refactor db  │ │                             │ │   │
│ ║   ║ │ ▸ Idle (2)       │ └────────────── A- 14 A+ ⤓ ⛶ ┘ │   │
│ ║   ║ │                  │                                 │   │
│ ║   ║ └──────────────────┴─────────────────────────────────┘   │
│ ║   ║ ┌────────────────────────────────────────────────────┐   │
│ ║ ? ║ │ + auto-route │ Describe a new task...      [Send] │   │
│ ║ ● ║ └────────────────────────────────────────────────────┘   │
│ ╚═══╝                                                          │
└────────────────────────────────────────────────────────────────┘
```

## Out of Scope (Future)

- MCP Manager UI (attach/detach MCP servers per task)
- Skills Manager UI
- Settings panel (17 TUI settings)
- Worktree finish (merge + cleanup) UI
- Global Claude session search/import
- Session ordering (drag to reorder)
