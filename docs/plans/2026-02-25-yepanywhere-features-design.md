# Design: YepAnywhere-Inspired Feature Roadmap for Agent Deck

**Date:** 2026-02-25
**Status:** Approved
**Approach:** Bottom-Up Foundation (infrastructure first, features on top)
**Frontend:** Vanilla JS (no framework, Web Components for tool renderers)
**Remote Access:** Deferred (architecture documented but not implemented)
**Real-time:** Replace SSE with WebSocket EventBus

---

## Scope

9 features across 4 phases, inspired by [YepAnywhere](https://github.com/kzahel/yepanywhere). Remote access (E2E encrypted relay) is architecturally documented in `docs/research/` but deferred from implementation.

**Research docs:** `docs/research/yepanywhere-analysis.md`, `docs/research/borrowable-features.md`, `docs/research/code-patterns-reference.md`

---

## Phase 1: Foundation

### 1.1 WebSocket EventBus

**Goal:** Replace SSE polling with a single multiplexed WebSocket connection.

**New package:** `internal/eventbus/`

**Components:**
- **`EventBus`** — In-memory pub/sub with error-isolated dispatch. `Subscribe(handler) → unsubscribe`.
- **`Hub`** — WebSocket connection manager. Tracks clients, routes events by subscription filter.
- **`Protocol`** — JSON message format.

**Event types (~16):**
- `session-status-changed` — session changed status (running/waiting/idle/error)
- `session-created` — new session appeared
- `session-updated` — session metadata changed (title, group)
- `session-removed` — session deleted
- `task-created` — hub task created
- `task-updated` — hub task changed
- `task-removed` — hub task deleted
- `push-event` — push notification sent/dismissed
- `upload-progress` — file upload progress
- `upload-complete` — file upload finished
- `heartbeat` — 30s keepalive

**Wire protocol:**
```
Client → Server:
  { "type": "subscribe", "channel": "sessions" }
  { "type": "subscribe", "channel": "session", "sessionId": "abc" }
  { "type": "subscribe", "channel": "tasks" }
  { "type": "unsubscribe", "subscriptionId": "sub-1" }
  { "type": "ping" }

Server → Client:
  { "type": "event", "channel": "sessions", "eventType": "status-changed", "data": {...} }
  { "type": "snapshot", "channel": "sessions", "data": {...} }
  { "type": "pong" }
  { "type": "heartbeat" }
```

**Endpoint:** `GET /ws/events` (separate from existing `/ws/session/{id}` PTY endpoint)

**Integration:** `web.Server` gets `eventBus *eventbus.EventBus` field. `StatusFileWatcher`, session mutations, and task mutations emit through it. Existing `menuSubscribers`/`taskSubscribers` channel maps replaced.

**Migration:** SSE endpoint (`/events/menu`) deprecated but kept during transition. Removed once all consumers use EventBus.

---

### 1.2 Server-Side Syntax Highlighting

**Goal:** Pre-render code with highlighting on the Go server.

**New package:** `internal/highlight/`

**Dependency:** `github.com/alecthomas/chroma/v2`

**Components:**
- **`Highlighter`** — Singleton, lazily initialized.
  - `HighlightCode(code, language string) string` → HTML with CSS class spans
  - `HighlightDiff(oldCode, newCode, filename string) string` → unified diff with highlighted lines
  - `DetectLanguage(filename string) string` → file extension → language mapping
- **LRU cache** — 256 entries, keyed by content hash + language
- **CSS variables theme** — Chroma `html.WithClasses(true)`. Classes mapped to CSS custom properties.

**CSS additions (`dashboard.css`):**
```css
:root { --hl-keyword: #d73a49; --hl-string: #032f62; --hl-comment: #6a737d; ... }
[data-theme="dark"] { --hl-keyword: #f97583; --hl-string: #9ecbff; ... }
```

**No new HTTP endpoint initially.** Called from tool augments (Section 2.1).

---

### 1.3 Connection Resilience + Streaming Throttle

**Goal:** Prevent silent disconnects. Reduce DOM thrashing during rapid PTY output.

**Client-side ConnectionManager (`dashboard.js`):**
- States: `connected → reconnecting → disconnected`
- Stale detection: no event in 45s → ping. No pong in 2s → reconnect.
- Visibility change: ping on tab focus, reconnect if stale.
- Exponential backoff: 1s base, 30s max, 10 attempts, jitter (0.5-1.0).
- Auto re-subscribe all active channels on reconnect.
- `onReconnect` callback for UI component refetch.

**Server-side:** EventBus WebSocket + PTY WebSocket send `heartbeat` every 30s.

**OutputBuffer (`dashboard.js`):**
- Accumulates binary PTY chunks.
- Flushes to xterm.js at most every 50ms via `requestAnimationFrame`.
- Immediate flush if buffer exceeds 64KB (backpressure).

**Connection status indicator:** 3px colored bar at top of dashboard (green/orange/red).

---

## Phase 2: Core Features

### 2.1 Tool-Specific Web Renderers

**Goal:** Structured, interactive views for each tool type.

**Server-side augments (`internal/web/augments.go`):**

Called when `/api/session/{id}/messages` is requested. Reads Claude JSONL via `dag.SessionReader`, augments tool results:

| Tool | Augment Data |
|------|-------------|
| Edit | `diffHtml` (Chroma-highlighted unified diff), `additions`, `deletions` |
| Bash | `stdoutHtml` (highlighted if code-like), `lineCount`, `truncated` |
| Read/Write | `contentHtml` (highlighted file content) |
| Grep | Match highlights |
| Glob | Formatted file list |
| WebSearch | Structured results |
| Task | Nested messages (initially collapsed) |

**New API endpoint:** `GET /api/session/{id}/messages` — Returns augmented message list.

**JSONL reading:** Uses `dag.SessionReader` which locates files at `~/.claude/projects/<encoded-path>/*.jsonl` via the session's `projectPath` from statedb.

**Client-side renderer registry (`dashboard.js`):**

```javascript
const ToolRenderers = {
  renderers: {},
  register(toolName, renderer) { this.renderers[toolName] = renderer; },
  render(toolName, input, result, augment) {
    return (this.renderers[toolName] || this.renderers._default).render(input, result, augment);
  }
};
```

**Renderers:**

| Renderer | Collapsed | Expanded |
|----------|-----------|----------|
| BashRenderer | `$ command` + exit badge | Full stdout/stderr, collapse at 20 lines |
| EditRenderer | `filename (+3 -1)` | Unified diff with highlighting |
| ReadRenderer | `filename (N lines)` | Highlighted file content |
| WriteRenderer | `filename (new)` | Highlighted content |
| GrepRenderer | `pattern → N matches` | File list with match lines |
| GlobRenderer | `pattern → N files` | File list |
| WebSearchRenderer | `"query"` | Results with titles/URLs |
| TaskRenderer | `agent: prompt...` | Nested messages (collapsed) |
| DefaultRenderer | Tool name | Raw JSON |

**Session detail view:** New "Messages" tab alongside the terminal PTY view.

---

### 2.2 Tiered Inbox / Priority Dashboard

**Goal:** Group sessions by urgency.

**Server-side (`menu_snapshot_builder.go`):**

Add `Tier` and `TierBadge` fields to `MenuSession`:

```go
type MenuSession struct {
    // ... existing fields ...
    Tier      string `json:"tier,omitempty"`
    TierBadge string `json:"tierBadge,omitempty"`
}
```

**Tier assignment:**
1. `needsAttention` — status `waiting` or `error`
2. `active` — status `running`
3. `recent` — updated < 30min ago, status `idle`
4. `idle` — everything else

**Badges:** `approval` (waiting for tool approval), `error` (error state), `question` (agent question).

**Client-side (`dashboard.js`):**
- Tiered sections replace flat card list
- `needsAttention`: gold accent, always expanded
- `active`: green pulsing dot
- `recent`: normal styling
- `idle`: dimmed, collapsed by default
- Stable merge: existing items keep position, new items append, removed fade out
- Sidebar badge: count of `needsAttention` sessions on the Agents icon

---

### 2.3 EventBus-Driven Refactoring

Once the EventBus is in place, migrate the dashboard JS to use it:
- Session list updates via `sessions` channel events (not SSE `menu` events)
- Task list updates via `tasks` channel events (not SSE `tasks` events)
- Remove SSE `EventSource` code
- Remove the 2-second poll timer

---

## Phase 3: Mobile/Remote Enhancement

### 3.1 Enhanced Push Notifications

**Goal:** Improve existing push with EventBus integration, browser suppression, dismiss.

**Enhancements to `internal/web/push_service.go`:**
1. Replace 3s poll timer → EventBus subscription for `session-status-changed`
2. Track connected browser profiles (active EventBus WebSocket = connected)
3. Skip push for connected browsers
4. Send `dismiss` push when session leaves `waiting` state
5. Structured notification types: `approval-needed`, `question`, `session-error`, `session-idle`, `dismiss`

**Service Worker improvements (`sw.js`):**
- Handle `dismiss` type → close notification by tag
- Check if user is viewing the session → suppress
- Conditional `clients.claim()` to avoid disrupting WebSocket connections
- Click: focus existing tab if open, else open new

---

### 3.2 File Uploads via WebSocket

**Goal:** Share files with agents from the web dashboard.

**New endpoint:** `/ws/session/{id}/upload`

**Protocol:**
```
Client → Server: JSON { "type": "start", "fileName", "fileSize", "mimeType" }
Client → Server: Binary chunks (64KB each)
Client → Server: JSON { "type": "end" }

Server → Client: JSON { "type": "progress", "received", "total" }
Server → Client: JSON { "type": "complete", "filePath", "fileName" }
Server → Client: JSON { "type": "error", "message" }
```

**Storage:** `~/.agent-deck/uploads/<session-id>/<uuid>_<sanitized-name>`

**Security:** 100MB limit (configurable), filename sanitization (strip traversal, UUID prefix), MIME validation, message queue serialization.

**Client triggers:** Paste (`Ctrl+V`), file picker button, drag-and-drop on chat input.

**UI:** Progress bar, image thumbnails, file name + size, cancel button.

---

### 3.3 DAG Conversation Model

**Goal:** Parse Claude JSONL as a DAG for proper branch handling.

**New package:** `internal/dag/`

**Components:**

```go
type DAGNode struct {
    UUID       string
    ParentUUID string
    LineIndex  int
    Entry      map[string]interface{}
}

type DAGResult struct {
    ActiveBranch []DAGNode
    TotalNodes   int
    BranchCount  int
    Orphaned     []string  // orphaned tool_use IDs
}

func BuildDAG(entries []map[string]interface{}) (*DAGResult, error)
```

**Algorithm:**
1. Build `uuid → node` and `parentUuid → children[]` maps
2. Find tips (nodes with no children)
3. Select active tip: most recent timestamp, tiebreak by branch length, then lineIndex
4. Walk from tip to root via parentUuid → active branch
5. Handle `compact_boundary` via `logicalParentUuid`
6. Detect orphaned tool_use blocks

**SessionReader:**
- Reads `~/.claude/projects/<encoded-path>/*.jsonl`
- Passes through DAGBuilder
- Returns structured messages for the tool renderer API

**Integration:** Called from `/api/session/{id}/messages` endpoint.

**Future enhancement (deferred):** `POST /api/session/{id}/fork?fromMessage={uuid}` for fork-from-any-message.

---

## Phase 4: Deferred

### 4.1 E2E Encrypted Remote Access

Architecture documented in `docs/research/yepanywhere-analysis.md` (Section: E2E Encrypted Remote Access). Implementation deferred.

Key components when ready:
- SRP-6a via `golang.org/x/crypto` + Go SRP library
- NaCl secretbox via `golang.org/x/crypto/nacl/secretbox`
- Binary framing protocol: `[version][nonce][ciphertext]`
- Relay server: dumb pipe matching clients to servers
- Session management: 7-day idle / 30-day max, 5 per user

---

## New Dependencies

| Dependency | Purpose | Phase |
|------------|---------|-------|
| `github.com/alecthomas/chroma/v2` | Syntax highlighting | 1.2 |
| `github.com/sergi/go-diff` | Unified diff computation | 2.1 |

Existing dependencies leveraged:
- `github.com/gorilla/websocket` — EventBus WebSocket
- `github.com/SherClockHolmes/webpush-go` — Push notifications (already in go.mod)
- `github.com/fsnotify/fsnotify` — File watching (already in go.mod)

---

## New Packages

| Package | Purpose | Phase |
|---------|---------|-------|
| `internal/eventbus/` | Typed pub/sub + WebSocket hub | 1.1 |
| `internal/highlight/` | Chroma-based syntax highlighting | 1.2 |
| `internal/dag/` | JSONL DAG parsing + session reading | 3.3 |

---

## Modified Files

| File | Changes | Phase |
|------|---------|-------|
| `internal/web/server.go` | Add EventBus, new WS/API routes | 1.1, 2.1, 3.2 |
| `internal/web/handlers_events.go` | Deprecate SSE, add WS EventBus handler | 1.1, 2.3 |
| `internal/web/handlers_ws.go` | Add heartbeat | 1.3 |
| `internal/web/menu_snapshot_builder.go` | Add tier/badge fields | 2.2 |
| `internal/web/push_service.go` | EventBus integration, browser suppression, dismiss | 3.1 |
| `internal/web/static/dashboard.js` | EventBusClient, ConnectionManager, OutputBuffer, ToolRenderers, tiered inbox | All |
| `internal/web/static/dashboard.css` | Highlight theme vars, tier styles, connection bar, renderer styles | All |
| `internal/web/static/dashboard.html` | Messages tab, upload UI elements | 2.1, 3.2 |
| `internal/web/static/sw.js` | Dismiss handling, safe lifecycle | 3.1 |
| New: `internal/web/augments.go` | Server-side tool augmentation | 2.1 |
| New: `internal/web/handlers_messages.go` | `/api/session/{id}/messages` endpoint | 2.1 |
| New: `internal/web/handlers_upload.go` | Upload WebSocket handler | 3.2 |

---

## Testing Strategy

- **EventBus:** Unit tests for pub/sub, integration test for WebSocket protocol
- **Highlight:** Unit tests with known code snippets, language detection tests
- **DAG:** Unit tests with fixture JSONL files (branching, compaction, orphaned tools)
- **Tool renderers:** Server-side augment tests with fixture tool_use/tool_result JSON
- **Push:** Unit tests for browser suppression logic, dismiss tracking
- **Connection:** Manual testing + Playwright E2E for reconnection scenarios
- **Uploads:** Unit test for protocol handling, size limits, filename sanitization

All test packages with TestMain force `AGENTDECK_PROFILE=_test` per project convention.

---

## Implementation Order

```
Phase 1 (Foundation):
  1.1 EventBus        → 1.2 Syntax Highlighting → 1.3 Connection Resilience

Phase 2 (Core Features):
  2.1 Tool Renderers  → 2.2 Tiered Inbox        → 2.3 SSE Removal

Phase 3 (Mobile/Remote):
  3.1 Push Enhance    → 3.2 File Uploads         → 3.3 DAG Model

Phase 4 (Deferred):
  4.1 Remote Access (documented, not implemented)
```

Each phase builds on the previous. Within phases, items are ordered by dependency.
