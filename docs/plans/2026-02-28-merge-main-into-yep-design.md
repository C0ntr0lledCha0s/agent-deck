# Merge Main into YepAnywhere — EventBus Resolution Design

**Date:** 2026-02-28
**Branch:** `feature/YepAnywhere-Investigation` ← `origin/main`
**Approach:** Single `git merge` with manual conflict resolution

## Context

Both branches diverged from `40f5bff` (Merge branch 'feature/hub-interface'). Main added 43 commits (workspace, templates, headless mode, hub bridge, dashboard visuals). YepAnywhere added 26 commits (EventBus, DAG parser, syntax highlighting, tiered inbox, push notifications, file upload, SSE removal).

Key architectural decision: **YepAnywhere's EventBus+WebSocket replaces main's SSE subscriber pattern.** All main features must be ported to use EventBus.

## Conflict Files (5)

### 1. `internal/web/server.go`

**Server struct:** Keep Yep's `eventBus`/`eventHub`. Add main's `hubTemplates`, `containerRuntime`, `hubBridge`. Drop main's `menuSubscribers`/`taskSubscribers` channel maps and mutexes.

**Imports:** Keep Yep's `eventbus` import. Add main's `workspace` and `sync` imports.

**NewServer():** Keep Yep's EventBus initialization. Add main's Docker runtime init, template store init, hub bridge init. Register main's new routes (`/api/templates`, `/api/workspaces`).

**notifyMenuChanged/notifyTaskChanged:** Keep Yep's `eventBus.Emit()` versions. Drop main's channel-broadcast versions and the subscribe/unsubscribe helper methods.

**Shutdown:** Keep Yep's `eventHub.Close()`.

### 2. `internal/web/static/dashboard.js`

Three conflict regions:

**Conflict 1 (~line 152):** Main added `getCardBorderColor()`. Yep has nothing. → Keep main's function.

**Conflict 2 (~line 1499):** Yep rewrote task rendering with tier-based inbox. Main uses active/completed split. → Keep Yep's tier system for agents view. Add main's kanban/conductor/workspaces as separate views.

**Conflict 3 (~line 3024):** Yep added `loadSessionMessages()`. Main added different code. → Keep both; they're additive.

**Non-conflict integration:**
- Replace main's `connectSSE()` with Yep's WebSocket ConnectionManager
- Add main's `renderView()` dispatcher, pointing agents → Yep's tier renderer
- Add main's kanban, conductor, workspaces, brainstorm views
- Add main's `renderTopBar()`, `renderClaudeMeta()`, `renderSlashPalette()`
- Add main's add-project modal logic

### 3. `internal/web/static/dashboard.html`

One conflict in detail-view area. Yep added Terminal/Messages tab buttons. Main added `claude-meta` div. → Keep both (tabs above terminal, claude-meta in header area).

Also bring in main's top-bar, sidebar labels, and add-project modal HTML.

### 4. `go.mod` / `go.sum`

Both added different dependencies. Yep: `regexp2` (Chroma). Main: Docker SDK packages. → Keep both.

### 5. `internal/web/server_test.go`

Both added new test functions. → Keep both.

## Non-Conflicting Files (auto-merge)

From main (clean additions):
- `internal/hub/workspace/` — ContainerRuntime + DockerRuntime
- `internal/hub/template_store.go` — Template CRUD
- `internal/web/hub_session_bridge.go` — Hub-to-tmux session orchestration
- `cmd/agent-deck/web_cmd.go` — `--headless` flag
- `internal/web/handlers_hub.go` — Extended hub API (1649 lines, auto-merged)
- Various test files and docs

From Yep (already present):
- `internal/eventbus/` — Pub/sub + WebSocket hub
- `internal/dag/` — JSONL DAG parser
- `internal/highlight/` — Chroma syntax highlighting
- `internal/web/augments.go`, `handlers_eventbus.go`, `handlers_messages.go`, `handlers_upload.go`, `push_service.go`

## Post-Merge Verification

After resolving conflicts:
1. `handlers_hub.go` calls to `notifyTaskChanged()` work with EventBus (same method signature)
2. Main's new routes compile with Yep's Server struct
3. Dashboard JS loads without errors
4. `go build` succeeds
5. `go test ./internal/web/...` passes
