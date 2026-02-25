# YepAnywhere-Inspired Features Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add 9 features inspired by YepAnywhere to Agent Deck's web dashboard: WebSocket EventBus, syntax highlighting, connection resilience, tool renderers, tiered inbox, enhanced push notifications, file uploads, and DAG conversation model.

**Architecture:** Bottom-up approach — build the WebSocket EventBus foundation first (replaces SSE), then layer features on top. All frontend stays vanilla JS (no framework). Server-side augments (syntax highlighting, diffs) keep the browser client lightweight.

**Tech Stack:** Go 1.24, gorilla/websocket, alecthomas/chroma/v2, sergi/go-diff, vanilla JS, xterm.js, Web Push API.

**Design doc:** `docs/plans/2026-02-25-yepanywhere-features-design.md`
**Research:** `docs/research/yepanywhere-analysis.md`, `docs/research/borrowable-features.md`, `docs/research/code-patterns-reference.md`

**Security note:** All HTML rendering in the client must use safe DOM methods (textContent, createElement) or DOMPurify sanitization. Server-rendered HTML from augments is pre-sanitized. Never use innerHTML with unsanitized user input. The augments.go server code uses its own escapeHTML function for all content.

---

## Phase 1: Foundation

### Task 1: Create EventBus Package

**Files:**
- Create: `internal/eventbus/eventbus.go`
- Create: `internal/eventbus/eventbus_test.go`

**Step 1: Write the failing test**

Create `internal/eventbus/eventbus_test.go` with tests for:
- `TestEventBus_SubscribeAndEmit` — subscribe handler, emit 2 events, verify both received
- `TestEventBus_Unsubscribe` — subscribe, emit, unsubscribe, emit again, verify count is 1
- `TestEventBus_ErrorIsolation` — first subscriber panics, second still receives event
- `TestEventBus_ConcurrentAccess` — 100 goroutines emitting, single subscriber counts all 100

Test uses `stretchr/testify` assertions. EventBus has `New()`, `Subscribe(Handler) func()`, `Emit(Event)`.

Event struct: `Type EventType`, `Channel string`, `Data interface{}`.

EventType constants: `EventSessionStatusChanged`, `EventSessionCreated`, `EventSessionUpdated`, `EventSessionRemoved`, `EventTaskCreated`, `EventTaskUpdated`, `EventTaskRemoved`, `EventPushSent`, `EventPushDismissed`, `EventUploadProgress`, `EventUploadComplete`, `EventHeartbeat`.

**Step 2: Run test to verify it fails**

Run: `go test -race -v ./internal/eventbus/ -run TestEventBus`
Expected: FAIL — package does not exist yet

**Step 3: Write minimal implementation**

Create `internal/eventbus/eventbus.go`:
- `EventBus` struct with `sync.RWMutex`, `subscribers map[int]Handler`, `nextID int`
- `New()` constructor
- `Subscribe(handler Handler) func()` — adds handler, returns cleanup func that deletes it
- `Emit(event Event)` — snapshots handlers under RLock, dispatches to each with panic recovery via `defer func() { recover() }()`
- `SubscriberCount() int`

**Step 4: Run tests to verify they pass**

Run: `go test -race -v ./internal/eventbus/ -run TestEventBus`
Expected: All 4 tests PASS

**Step 5: Commit**

```bash
git add internal/eventbus/
git commit -m "feat(eventbus): add in-memory pub/sub with error isolation"
```

---

### Task 2: Create WebSocket EventBus Hub

**Files:**
- Create: `internal/eventbus/hub.go`
- Create: `internal/eventbus/hub_test.go`

**Step 1: Write the failing test**

Create `internal/eventbus/hub_test.go` with tests for:
- `TestProtocol_ParseSubscribe` — parse `{"type":"subscribe","channel":"sessions"}`
- `TestProtocol_ParseUnsubscribe` — parse unsubscribe message
- `TestProtocol_ParsePing` — parse ping message
- `TestProtocol_MarshalEvent` — marshal a ServerMessage with event data
- `TestHub_ClientTracking` — register client, verify count=1, unregister, verify count=0

Uses a `mockConn` implementing `WSConn` interface (`WriteJSON(v interface{}) error`).

**Step 2: Run test to verify it fails**

Run: `go test -race -v ./internal/eventbus/ -run TestProtocol`
Expected: FAIL — types not defined

**Step 3: Write implementation**

Create `internal/eventbus/hub.go`:
- `ClientMessage` struct: Type, Channel, SessionID, SubscriptionID
- `ServerMessage` struct: Type, Channel, EventType, SubscriptionID, Data
- `ParseClientMessage(raw json.RawMessage) (*ClientMessage, error)`
- `WSConn` interface: `WriteJSON(v interface{}) error`
- `Hub` struct: mu, bus, clients map, nextID, unsub func
- `NewHub(bus *EventBus) *Hub` — creates hub, subscribes to bus for broadcast
- `RegisterClient(conn WSConn) string` — assigns client ID, returns it
- `UnregisterClient(id string)` — removes client and subscriptions
- `HandleMessage(clientID string, raw json.RawMessage) error` — switch on type: subscribe, unsubscribe, ping
- `broadcast(event Event)` — routes events to clients based on channel match
- `eventChannel(et EventType) string` — maps event types to subscription channels (sessions, tasks, push, uploads, system)
- `ClientCount() int`, `ConnectedClientIDs() []string`, `Close()`

**Step 4: Run tests**

Run: `go test -race -v ./internal/eventbus/`
Expected: All tests PASS

**Step 5: Commit**

```bash
git add internal/eventbus/
git commit -m "feat(eventbus): add WebSocket hub with client tracking and event routing"
```

---

### Task 3: Wire EventBus into Web Server

**Files:**
- Modify: `internal/web/server.go:38-58` (Server struct), `107-140` (routes)
- Create: `internal/web/handlers_eventbus.go`

**Step 1: Add EventBus and Hub fields to Server struct**

In `internal/web/server.go`:
- Add `eventBus *eventbus.EventBus` and `eventHub *eventbus.Hub` fields to `Server` struct (line ~38-58)
- Import `github.com/asheshgoplani/agent-deck/internal/eventbus`
- In `NewServer()` (~line 61-153): create EventBus and Hub, assign to server
- Register route: `mux.HandleFunc("GET /ws/events", s.withAuth(s.handleEventBusWS))`
- In `notifyMenuChanged()` (~line 262): also emit `eventbus.Event{Type: eventbus.EventSessionUpdated}` through bus
- In `notifyTaskChanged()` (~line 294): also emit `eventbus.Event{Type: eventbus.EventTaskUpdated}` through bus

**Step 2: Create the WebSocket handler**

Create `internal/web/handlers_eventbus.go`:
- `handleEventBusWS(w, r)` — upgrades to WebSocket, registers client with hub, starts heartbeat goroutine (30s ticker sending `ServerMessage{Type: "heartbeat"}`), enters read loop calling `hub.HandleMessage()` for each message
- On close: unregister client
- Uses existing `wsUpgrader` from handlers_ws.go

**Step 3: Build and test**

Run: `go test -race -v ./internal/web/ && go test -race -v ./internal/eventbus/ && make build`
Expected: All PASS, build succeeds

**Step 4: Commit**

```bash
git add internal/web/server.go internal/web/handlers_eventbus.go
git commit -m "feat(web): wire EventBus into web server with /ws/events endpoint"
```

---

### Task 4: Add Chroma Dependency and Highlight Package

**Files:**
- Modify: `go.mod`
- Create: `internal/highlight/highlight.go`
- Create: `internal/highlight/highlight_test.go`

**Step 1: Add dependency**

Run: `go get github.com/alecthomas/chroma/v2`

**Step 2: Write the failing test**

Create `internal/highlight/highlight_test.go` with tests for:
- `TestHighlightCode_Go` — highlight Go code, verify output contains `<span` tags and the function name
- `TestHighlightCode_UnknownLanguage` — unknown lang falls back gracefully, output contains original text
- `TestDetectLanguage` — map filenames to Chroma lexer names (main.go->Go, app.js->JavaScript, style.css->CSS, Dockerfile->Docker, unknown.xyz->"")
- `TestHighlightCode_CacheHit` — highlight same code twice, results are identical
- `TestHighlightCode_UsesClasses` — output uses CSS classes (not inline styles)

**Step 3: Write implementation**

Create `internal/highlight/highlight.go`:
- Package-level `formatter` (`html.New(html.WithClasses(true), html.TabWidth(4))`) and `style`
- LRU cache: `sync.RWMutex`, `map[string]string`, max 256 entries with clear-on-full eviction
- `Code(code, language string) (string, error)` — check cache, get lexer, tokenise, format, cache result
- `DetectLanguage(filename string) string` — use `lexers.Match(filename)`, return lexer name or ""
- `CSS() string` — return formatter CSS
- `CSSVariables() string` — return CSS custom property definitions for light/dark themes
- `cacheKey(code, language) string` — SHA-256 of language + code, truncated to 16 bytes hex

**Step 4: Run tests**

Run: `go test -race -v ./internal/highlight/`
Expected: All PASS

**Step 5: Commit**

```bash
git add internal/highlight/ go.mod go.sum
git commit -m "feat(highlight): add Chroma-based syntax highlighting with CSS classes"
```

---

### Task 5: Client-Side Connection Resilience

**Files:**
- Modify: `internal/web/static/dashboard.js:118-165` (replace connectSSE)
- Modify: `internal/web/static/dashboard.css` (add connection bar styles)
- Modify: `internal/web/static/dashboard.html` (add connection bar element)

**Step 1: Add ConnectionManager class to dashboard.js**

Replace `connectSSE()` (lines 118-165) with `ConnectionManager` class:
- Constructor: url, state (disconnected/connected/reconnecting), ws, lastEventAt, reconnectAttempts, subscriptions Map, listeners
- Config: staleThresholdMs=45000, pongTimeoutMs=2000, baseDelayMs=1000, maxDelayMs=30000, maxAttempts=10
- `connect()` — opens WebSocket, sets up onopen/onmessage/onclose/onerror
- `_handleMessage(msg)` — dispatch pong, heartbeat, event/snapshot to subscription handlers
- `subscribe(channel, handler)` — stores in Map, sends subscribe JSON if connected, returns unsubscribe function
- `_resubscribeAll()` — re-sends all subscribe messages after reconnect
- `_scheduleReconnect()` — exponential backoff with jitter
- `_startStaleCheck()` — 15s interval, checks lastEventAt against staleThreshold
- `_setupVisibility()` — on visibilitychange to "visible", send ping, timeout for pong
- `_checkWithPing()` — sends ping, sets 2s timeout for pong response
- `_setState(newState)` — fires stateChange and reconnect callbacks
- `on(event, handler)` — register listener (stateChange, reconnect)
- `disconnect()` — clean close

**Step 2: Add OutputBuffer class**

Add to dashboard.js near terminal code:
- Constructor: terminal ref, flushIntervalMs (50), buffer string, pending flag, maxBufferSize (64KB)
- `write(data)` — append to buffer, flush immediately if over max, else schedule via requestAnimationFrame
- `flush()` — write buffer to terminal, clear buffer and pending flag

**Step 3: Add connection bar CSS to dashboard.css**

Add `.connection-bar` (fixed top, 3px height, z-index 9999) with `--connected` (green, opacity 0), `--reconnecting` (orange, opacity 1), `--disconnected` (red, opacity 1) modifiers.

**Step 4: Add connection bar element to dashboard.html**

Add `<div id="connection-bar" class="connection-bar connection-bar--connected"></div>` as first child of `<body>`.

**Step 5: Wire into init block**

Replace `connectSSE()` call (~line 1091) with ConnectionManager initialization:
- Build WebSocket URL from `location.protocol`/`location.host` + `/ws/events`
- Create ConnectionManager, subscribe to "sessions" and "tasks" channels
- Wire stateChange to update connection bar CSS class
- Wire reconnect to refetch tasks

**Step 6: Build**

Run: `make build`
Expected: Build succeeds

**Step 7: Commit**

```bash
git add internal/web/static/
git commit -m "feat(web): add ConnectionManager with auto-reconnect and OutputBuffer throttle"
```

---

## Phase 2: Core Features

### Task 6: Server-Side Tool Augments

**Files:**
- Modify: `go.mod` (add go-diff)
- Create: `internal/web/augments.go`
- Create: `internal/web/augments_test.go`

**Step 1: Add dependency**

Run: `go get github.com/sergi/go-diff/diffmatchpatch`

**Step 2: Write the failing test**

Create `internal/web/augments_test.go` with tests for:
- `TestComputeEditAugment` — diff two Go code strings, verify additions/deletions count and diffHTML contains changed text
- `TestComputeBashAugment` — 2-line stdout, verify lineCount=2, isError=false
- `TestComputeBashAugment_Error` — stderr with exit code 127, verify isError=true
- `TestComputeReadAugment` — 3-line Go file, verify lineCount=3 and contentHTML contains "package"

**Step 3: Write implementation**

Create `internal/web/augments.go`:
- `editAugment` struct: DiffHTML, Additions, Deletions
- `bashAugment` struct: StdoutHTML, Stderr, LineCount, IsError, Truncated
- `readAugment` struct: ContentHTML, LineCount, Language
- `computeEditAugment(oldText, newText, filename string) (*editAugment, error)` — uses `diffmatchpatch.DiffMain` + `DiffCleanupSemantic`, builds HTML with `diff-add`/`diff-del` spans, counts additions/deletions. All text passed through `escapeHTML()` before embedding in HTML.
- `computeBashAugment(stdout, stderr string, exitCode int) *bashAugment` — counts lines, detects errors
- `computeReadAugment(content, filename string) (*readAugment, error)` — detect language via highlight package, highlight code, count lines
- `escapeHTML(s string) string` — replace &, <, >, " with entities

**Step 4: Run tests**

Run: `go test -race -v ./internal/web/ -run TestCompute`
Expected: All PASS

**Step 5: Commit**

```bash
git add internal/web/augments.go internal/web/augments_test.go go.mod go.sum
git commit -m "feat(web): add server-side tool augments — diffs, bash, read highlighting"
```

---

### Task 7: DAG Package for JSONL Parsing

**Files:**
- Create: `internal/dag/dag.go`
- Create: `internal/dag/dag_test.go`
- Create: `internal/dag/reader.go`
- Create: `internal/dag/reader_test.go`

**Step 1: Write DAG builder tests**

Create `internal/dag/dag_test.go`:
- `TestBuildDAG_LinearChain` — 3 entries a->b->c, active branch has all 3 in order
- `TestBuildDAG_BranchSelectsMostRecent` — root with 2 children, newer timestamp wins
- `TestBuildDAG_Empty` — nil input, empty result
- `TestBuildDAG_SingleNode` — one entry, active branch has 1 node

**Step 2: Run test to verify it fails**

Run: `go test -race -v ./internal/dag/ -run TestBuildDAG`
Expected: FAIL — package does not exist

**Step 3: Write DAG builder**

Create `internal/dag/dag.go`:
- `Entry` struct: UUID, ParentUUID, Timestamp, Type, Message (json.RawMessage), Raw (json.RawMessage), LineIndex, LogicalParentUUID
- `DAGNode` struct: UUID, ParentUUID, LineIndex, Entry pointer
- `DAGResult` struct: ActiveBranch []*DAGNode, TotalNodes, BranchCount
- `BuildDAG(entries []Entry) (*DAGResult, error)`:
  1. Build nodeMap (uuid->node) and childrenMap (parentUUID->child UUIDs)
  2. Find tips (nodes with no children in childrenMap)
  3. Sort tips by timestamp desc (tiebreak lineIndex desc)
  4. Walk from selected tip to root via ParentUUID (with LogicalParentUUID fallback for compact_boundary)
  5. Reverse to root-to-tip order
  6. Return result

**Step 4: Run DAG tests**

Run: `go test -race -v ./internal/dag/ -run TestBuildDAG`
Expected: All PASS

**Step 5: Write JSONL reader and tests**

Create `internal/dag/reader.go`:
- `SessionMessage` struct: UUID, ParentUUID, Type, Role, Content, Message, Timestamp, LineIndex
- `ReadSession(sessionDir string) ([]SessionMessage, error)`:
  1. Glob `*.jsonl`, filter out `agent-*.jsonl`
  2. Sort by mtime, read most recent
  3. Parse each line as Entry (skip malformed, use 10MB line buffer)
  4. BuildDAG to get active branch
  5. Convert to SessionMessages (extract role/content from message JSON)

Create `internal/dag/reader_test.go`:
- `TestReadSession_EmptyDir` — empty dir returns nil, no error
- `TestReadSession_SimpleConversation` — 2-line JSONL in temp dir, verify 2 messages with correct roles
- `TestReadSession_SkipsAgentFiles` — session.jsonl + agent-sub.jsonl, only reads session.jsonl

**Step 6: Run all tests**

Run: `go test -race -v ./internal/dag/`
Expected: All PASS

**Step 7: Commit**

```bash
git add internal/dag/
git commit -m "feat(dag): add JSONL DAG parser and session reader for Claude conversations"
```

---

### Task 8: Messages API Endpoint

**Files:**
- Create: `internal/web/handlers_messages.go`
- Modify: `internal/web/server.go:107-140` (add route)

**Step 1: Write the messages endpoint**

Create `internal/web/handlers_messages.go`:
- `augmentedMessage` struct: UUID, ParentUUID, Type, Role, Timestamp, Content, ToolName, ToolInput, ToolResult, Augment
- `handleSessionMessages(w, r)`:
  1. Extract session ID from path (trimPrefix `/api/session/`, trimSuffix `/messages`)
  2. Load MenuSnapshot, find session by ID to get ProjectPath
  3. Call `findClaudeSessionDir(projectPath)` to locate `~/.claude/projects/<encoded-path>/`
  4. Call `dag.ReadSession(sessionDir)` to get messages
  5. Build augmentedMessage list (TODO: full tool augmentation in future step)
  6. Return JSON `{ sessionId, messages, dagInfo: { totalNodes } }`
- `findClaudeSessionDir(projectPath string) string` — tries encoded dir name (`-`+path with `/`->`-`), then scans claude projects dir for match
- `encodeProjectPath(path) string` — Claude's dash-separated encoding
- `decodeProjectPath(encoded) string` — reverse encoding

**Step 2: Register route in server.go**

Add in route block (~line 107-140): `mux.HandleFunc("GET /api/session/{id}/messages", s.withAuth(s.handleSessionMessages))`

Note: The URL pattern uses `{id}` but the handler extracts the session ID manually from the path since the standard mux with this pattern needs the full path parsed. The handler trims the prefix and suffix to get the ID.

**Step 3: Build and verify**

Run: `make build`
Expected: Build succeeds

**Step 4: Commit**

```bash
git add internal/web/handlers_messages.go internal/web/server.go
git commit -m "feat(web): add /api/session/{id}/messages endpoint with DAG-based JSONL reading"
```

---

### Task 9: Client-Side Tool Renderers

**Files:**
- Modify: `internal/web/static/dashboard.js` (add renderer registry + renderers)
- Modify: `internal/web/static/dashboard.css` (add renderer styles)
- Modify: `internal/web/static/dashboard.html` (add Messages tab)

**Step 1: Add ToolRenderers object**

Add to dashboard.js after OutputBuffer:
- `ToolRenderers` object with `_renderers` map, `register(name, renderer)`, `get(name)`, `render(name, input, result, augment)`
- Default renderer: creates div with pre-formatted JSON via `document.createTextNode`
- `escapeHtml(s)` utility using `document.createElement('div')` + `textContent` + `textContent` (safe DOM method, no innerHTML with user input)

**Step 2: Add tool-specific renderers**

All renderers use safe DOM creation methods (createElement, textContent, appendChild). For server-rendered augment HTML (pre-sanitized on server), use a dedicated container element.

- **BashRenderer**: header with `$` icon + command (textContent) + error badge; collapsible body with stdout/stderr
- **EditRenderer**: header with filename (textContent) + `+N`/`-N` badges; collapsible body with server-rendered diff HTML (from augment.diffHtml, pre-sanitized by server's escapeHTML)
- **ReadRenderer**: header with filename + line count badge; collapsible body with server-rendered highlighted content

For augment HTML display, use a wrapper approach: create a container div, set a data attribute marking it as server-rendered, and only inject pre-sanitized HTML from the Go server's augment pipeline (which already escapes all user content via `escapeHTML()`).

**Step 3: Add Messages tab to dashboard.html**

In right panel detail section (~line 65-77), add:
- Tab bar: `<div id="detail-tabs" class="detail-tabs">` with Terminal and Messages tab buttons
- Messages container: `<div id="messages-container" class="messages-container">`

**Step 4: Add renderer CSS to dashboard.css**

Add styles for: `.tool-block`, `.tool-header`, `.tool-icon`, `.tool-command`, `.tool-filename`, `.tool-badge` (with `--error`, `--add`, `--del` variants), `.tool-body`, `.tool-collapsed`, `.tool-stderr`, `.diff-add`, `.diff-del`, `.messages-container`, `.message-block` (with `--user`, `--assistant` variants), `.detail-tabs`, `.detail-tab` (with `--active` variant).

**Step 5: Add tab switching and message loading**

Add to dashboard.js:
- `loadSessionMessages(sessionId)` — fetch `/api/session/{id}/messages`, call `renderMessages()`
- `renderMessages(messages)` — clear container, create DOM elements for each message (using createElement + textContent for safe rendering)
- Tab click handler: toggle between terminal and messages views

**Step 6: Build**

Run: `make build`
Expected: Build succeeds

**Step 7: Commit**

```bash
git add internal/web/static/
git commit -m "feat(web): add tool renderer registry and Messages tab with Bash/Edit/Read renderers"
```

---

### Task 10: Tiered Inbox

**Files:**
- Modify: `internal/web/session_data_service.go:53-65` (MenuSession struct)
- Modify: `internal/web/menu_snapshot_builder.go:10-81` (BuildMenuSnapshot)
- Modify: `internal/web/static/dashboard.js:242-305` (renderTaskList)
- Modify: `internal/web/static/dashboard.css` (tier styles)

**Step 1: Add Tier and TierBadge fields**

In `session_data_service.go` MenuSession struct (~line 53-65), add:
```go
Tier      string `json:"tier,omitempty"`
TierBadge string `json:"tierBadge,omitempty"`
```

**Step 2: Add tier assignment**

In `menu_snapshot_builder.go`, create `assignSessionTiers(items []MenuItem)`:
- `needsAttention` — status waiting or error (badge: "approval" or "error")
- `active` — status running
- `recent` — updated < 30min, idle
- `idle` — everything else

Call at end of `BuildMenuSnapshot()` before returning.

**Step 3: Update client-side rendering**

In dashboard.js, modify session card rendering to:
- Group sessions by tier
- Render tier sections with headers (label + count badge)
- `needsAttention`: gold accent header, always expanded
- `active`: green pulsing dot indicator
- `idle`: dimmed, collapsed by default
- Stable merge: on EventBus updates, existing cards keep position, new cards append

**Step 4: Add tier CSS**

Add styles for `.tier-section`, `.tier-header` (with `--needsAttention`, `--active`, `--idle` variants), `.tier-badge`, `.tier-collapsed`, `.pulse-dot` with keyframe animation.

**Step 5: Build and verify**

Run: `make build`
Expected: Build succeeds

**Step 6: Commit**

```bash
git add internal/web/session_data_service.go internal/web/menu_snapshot_builder.go internal/web/static/
git commit -m "feat(web): add tiered inbox — needsAttention, active, recent, idle"
```

---

## Phase 3: Mobile/Remote Enhancement

### Task 11: Enhanced Push Notifications

**Files:**
- Modify: `internal/web/push_service.go:464-492` (replace poll timer with EventBus)
- Modify: `internal/web/static/sw.js:69-97` (add dismiss handling)

**Step 1: Refactor pushService to use EventBus**

In `push_service.go`:
- Add `eventBus *eventbus.EventBus` and `eventHub *eventbus.Hub` fields to `pushService` struct
- In `run()` (~line 464-492): replace the 3s poll ticker with an EventBus subscription for `EventSessionStatusChanged`
- The subscription handler calls the existing `syncOnce()` logic immediately on event
- Keep a fallback 30s poll ticker for safety (catches any missed events)
- Add browser suppression: get `eventHub.ConnectedClientIDs()`, skip push for connected clients
- Add dismiss tracking: maintain `notifiedSessions` set. When session transitions FROM waiting/error, send dismiss push.

**Step 2: Update Service Worker**

In `sw.js` push handler (~line 69-97):
- Add `dismiss` type check at top of handler: if `data.type === 'dismiss'`, get notifications by tag and close them, then return
- Add session-viewing suppression: before showing notification, check `self.clients.matchAll()` for any focused client whose URL contains the session ID
- Update `activate` handler: use conditional `clients.claim()` — only claim if no existing windows are open (avoids disrupting WebSocket connections)

**Step 3: Build**

Run: `make build`
Expected: Build succeeds

**Step 4: Commit**

```bash
git add internal/web/push_service.go internal/web/static/sw.js
git commit -m "feat(push): EventBus-driven notifications with browser suppression and dismiss"
```

---

### Task 12: File Upload WebSocket Handler

**Files:**
- Create: `internal/web/handlers_upload.go`
- Create: `internal/web/handlers_upload_test.go`
- Modify: `internal/web/server.go` (add route)
- Modify: `internal/web/static/dashboard.js` (upload UI)
- Modify: `internal/web/static/dashboard.css` (upload styles)

**Step 1: Write upload handler test**

Create `internal/web/handlers_upload_test.go`:
- `TestSanitizeFilename` — test cases: "normal.txt" -> "normal.txt", "../../../etc/passwd" -> "etcpasswd", empty -> "unnamed", "path/to/file.js" -> "pathtofile.js"

**Step 2: Write upload handler**

Create `internal/web/handlers_upload.go`:
- `maxUploadSize` constant: 100MB
- `uploadStartMsg`, `uploadProgressMsg`, `uploadCompleteMsg` structs
- `handleUploadWS(w, r)`:
  1. Extract session ID from path
  2. Upgrade to WebSocket
  3. Read loop: JSON text messages for start/end, binary messages for chunks
  4. On `start`: validate size, sanitize filename, create upload dir at `<profileDir>/uploads/<sessionID>/`, create file with UUID prefix
  5. On binary chunk: write to file, track received bytes, enforce size limit per-chunk, send progress every 64KB
  6. On `end`: close file, send complete message
  7. On disconnect: cleanup partial file
- `sanitizeFilename(name string) string` — strip `..`, `/`, `\`, trim, default "unnamed"

**Step 3: Register route**

Add to server.go routes: `mux.HandleFunc("GET /ws/session/{id}/upload", s.withAuth(s.handleUploadWS))`

Note: Config struct needs `ProfileDir string` field added if not present. Check `internal/web/server.go` Config struct.

**Step 4: Add client-side upload UI**

In dashboard.js:
- Paste handler on chat input: detect clipboard images/files, initiate upload
- Drag-and-drop handler on chat area: highlight drop zone, initiate upload on drop
- File picker: attachment button opens `<input type="file">`
- Upload function: opens WebSocket to `/ws/session/{id}/upload`, sends start, reads file as 64KB chunks via `File.slice()` + `FileReader`, sends binary frames, sends end
- Progress bar: create DOM element below chat input, update width on progress messages

In dashboard.css:
- `.upload-progress` bar styles
- `.upload-dropzone` highlight styles
- `.upload-thumbnail` for image previews

**Step 5: Run tests and build**

Run: `go test -race -v ./internal/web/ -run TestSanitize && make build`
Expected: All PASS, build succeeds

**Step 6: Commit**

```bash
git add internal/web/handlers_upload.go internal/web/handlers_upload_test.go internal/web/server.go internal/web/static/
git commit -m "feat(web): add WebSocket file upload with progress tracking and drag-drop UI"
```

---

### Task 13: Remove Legacy SSE and Cleanup

**Files:**
- Modify: `internal/web/handlers_events.go` (deprecate SSE)
- Modify: `internal/web/server.go` (remove channel-based subscribers)
- Modify: `internal/web/static/dashboard.js` (remove SSE code)

**Step 1: Remove SSE EventSource code from dashboard.js**

Delete the old `connectSSE()` function and any references to `state.menuEvents` (the EventSource).

**Step 2: Remove channel-based subscriber maps from server.go**

Remove from Server struct: `menuSubscribers`, `taskSubscribers`, `menuMu`, `taskMu`.
Remove functions: `subscribeMenuChanges`, `unsubscribeMenuChanges`, `notifyMenuChanged` (non-EventBus version), `subscribeTaskChanges`, `unsubscribeTaskChanges`, `notifyTaskChanged` (non-EventBus version).

Replace `notifyMenuChanged()` and `notifyTaskChanged()` with versions that only emit through EventBus.

**Step 3: Mark SSE endpoint as deprecated**

In `handlers_events.go`, simplify `handleMenuEvents()` to:
- Send a single SSE event: `data: {"deprecated": true, "message": "Use /ws/events WebSocket instead"}`
- Close the connection

**Step 4: Run full test suite**

Run: `make test && make build`
Expected: All tests PASS, build succeeds

**Step 5: Commit**

```bash
git add internal/web/
git commit -m "refactor(web): remove SSE polling, all real-time events now via WebSocket EventBus"
```

---

## Task Summary

| Task | Feature | Phase | Key Files |
|------|---------|-------|-----------|
| 1 | EventBus package | 1 | `internal/eventbus/eventbus.go` |
| 2 | WebSocket Hub | 1 | `internal/eventbus/hub.go` |
| 3 | Wire into server | 1 | `internal/web/server.go`, `handlers_eventbus.go` |
| 4 | Chroma highlighting | 1 | `internal/highlight/highlight.go` |
| 5 | Connection resilience | 1 | `internal/web/static/dashboard.js` |
| 6 | Server-side augments | 2 | `internal/web/augments.go` |
| 7 | DAG JSONL parser | 2 | `internal/dag/dag.go`, `reader.go` |
| 8 | Messages API | 2 | `internal/web/handlers_messages.go` |
| 9 | Tool renderers (JS) | 2 | `internal/web/static/dashboard.js` |
| 10 | Tiered inbox | 2 | `session_data_service.go`, `dashboard.js` |
| 11 | Enhanced push | 3 | `internal/web/push_service.go`, `sw.js` |
| 12 | File uploads | 3 | `internal/web/handlers_upload.go` |
| 13 | SSE removal | 3 | `internal/web/handlers_events.go` |

Each task is independently committable. Phases build on each other. Tasks within a phase are sequential.
