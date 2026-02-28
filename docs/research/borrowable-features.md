# Borrowable Features from YepAnywhere

**Priority-ranked features for Agent Deck integration, with implementation notes.**

---

## Tier 1 — High Impact, Moderate Effort

### 1. Tool-Specific Web Renderers

**Impact:** Transforms the web dashboard from raw terminal output to structured, interactive tool views.

**YepAnywhere reference:**
- `packages/client/src/components/renderers/tools/` — 16 renderers
- `packages/server/src/augments/` — server-side diff + highlighting

**What to build:**
- Go-side: Parse tool_use/tool_result JSON from session messages
- Go-side: Compute unified diffs for Edit tool, syntax highlight with Chroma
- JS-side: ToolRendererRegistry with pluggable renderers per tool name
- Renderers: BashRenderer (collapsible output), EditRenderer (diff view), ReadRenderer (highlighted code), WriteRenderer, GrepRenderer, GlobRenderer

**Key files in Agent Deck to modify:**
- `internal/web/static/` — Add renderer components
- `internal/web/` — Add augment API endpoints

**Estimated scope:** ~2000 lines Go + ~3000 lines JS

---

### 2. Server-Side Syntax Highlighting

**Impact:** Rich code display in web dashboard without heavy client-side libraries.

**YepAnywhere reference:**
- `packages/server/src/augments/augment-generator.ts` — Shiki with CSS variables
- `packages/server/src/highlighting/` — Shiki engine setup

**What to build:**
- Go-side: Integrate `github.com/alecthomas/chroma/v2` for syntax highlighting
- CSS variables theme for light/dark mode switching
- API endpoint: POST raw code → HTML with highlight spans
- Streaming: Compute highlights on the server as tool results arrive

**Key files in Agent Deck to modify:**
- New: `internal/highlight/` package
- `internal/web/` — New highlight API endpoint
- `internal/web/static/` — CSS variables for highlight theme

**Estimated scope:** ~500 lines Go + ~200 lines CSS

---

### 3. Push Notifications for Agent Approvals

**Impact:** Users get mobile alerts when agents need attention, reducing idle wait time.

**YepAnywhere reference:**
- `packages/server/src/push/` — PushService, PushNotifier, VAPID
- `packages/client/public/sw.js` — Service Worker
- `packages/client/src/hooks/usePushNotifications.ts`

**What to build:**
- Go-side: VAPID key generation + storage, subscription CRUD in statedb, web-push delivery
- Go-side: Status change listener → push notification with connected-browser suppression
- JS-side: Service Worker for push event handling, notification click → session URL
- JS-side: Push subscription management in settings
- Notification types: `approval-needed`, `session-error`, `dismiss`

**Dependencies:** `github.com/SherClockHolmes/webpush-go`

**Key files in Agent Deck to modify:**
- New: `internal/push/` package
- `internal/web/` — Push subscription routes
- `internal/web/static/` — Service Worker, push hooks
- `internal/statedb/` — Push subscription table

**Estimated scope:** ~800 lines Go + ~500 lines JS

---

### 4. Tiered Inbox / Priority Dashboard

**Impact:** Surfaces the most important sessions immediately instead of flat listing.

**YepAnywhere reference:**
- `packages/server/src/routes/inbox.ts` — 5-tier server logic
- `packages/client/src/contexts/InboxContext.tsx` — Client-side tier management
- `packages/client/src/components/InboxContent.tsx` — Tiered UI

**What to build:**
- Go-side: Tier assignment in sessions API (needsAttention > active > recent > idle)
- JS-side: Tiered session list with stable ordering (`mergeWithStableOrder`)
- Badges: "Approval" (yellow), "Error" (red) for attention items; pulsing dot for active
- Max 20 per tier, sorted by last update

**Key files in Agent Deck to modify:**
- `internal/web/` — Modify session list API to return tier assignments
- `internal/web/static/` — Tiered dashboard view

**Estimated scope:** ~300 lines Go + ~500 lines JS

---

### 5. Connection Resilience

**Impact:** Prevents silent disconnects, especially on mobile/unstable networks.

**YepAnywhere reference:**
- `packages/client/src/lib/connection/ConnectionManager.ts`
- `packages/client/src/lib/activityBus.ts`

**What to build:**
- JS-side: ConnectionManager state machine (connected/reconnecting/disconnected)
- Stale detection: No events in 45s → reconnect
- Visibility change: Ping on tab focus, reconnect if no pong in 2s
- Exponential backoff: 1s base, 30s max, 10 attempts, with jitter
- Synthetic `reconnect` event for listener refetch
- Server-side: Add heartbeat to SSE streams (30s interval)

**Key files in Agent Deck to modify:**
- `internal/web/static/` — ConnectionManager, SSE reconnect logic
- `internal/web/` — Add SSE heartbeat

**Estimated scope:** ~100 lines Go + ~400 lines JS

---

## Tier 2 — Medium Impact, Higher Effort

### 6. DAG Conversation Model

**Impact:** Enables fork-from-any-message, branch visualization, dead branch pruning.

**YepAnywhere reference:**
- `packages/server/src/sessions/dag.ts` — Server-side DAG builder
- `packages/shared/src/dag.ts` — Shared reorder utility

**What to build:**
- Go-side: DAG builder for Claude JSONL parsing (uuid → node, parentUuid → children)
- Active branch selection: most recent tip, walk to root
- Compaction handling: follow `logicalParentUuid` across compact_boundary entries
- Orphaned tool detection across branches
- Store branch metadata in statedb
- JS-side: Branch visualization in session view

**Key files in Agent Deck to modify:**
- New: `internal/dag/` package
- `internal/session/` — Integrate DAG parsing
- `internal/statedb/` — Branch metadata table
- `internal/web/static/` — Branch UI

**Estimated scope:** ~600 lines Go + ~800 lines JS

---

### 7. EventBus Over WebSocket

**Impact:** Real-time reactive dashboard without polling. Foundation for other features.

**YepAnywhere reference:**
- `packages/server/src/watcher/EventBus.ts` — Server-side pub/sub
- `packages/shared/src/relay.ts` — Subscription protocol
- `packages/client/src/lib/activityBus.ts` — Client-side distribution

**What to build:**
- Go-side: Typed EventBus with `Subscribe(handler) → unsubscribe`
- Define event types: session-status-changed, session-created, process-state-changed, etc.
- WebSocket subscription management: subscribe/unsubscribe messages
- JS-side: ActivityBus with `on(type, callback) → unsubscribe` pattern
- Migrate existing SSE consumers to use event bus

**Key files in Agent Deck to modify:**
- New: `internal/eventbus/` package
- `internal/web/` — WebSocket subscription handler
- `internal/web/static/` — ActivityBus, migrate from SSE

**Estimated scope:** ~500 lines Go + ~600 lines JS

---

### 8. E2E Encrypted Remote Access

**Impact:** Secure mobile access to Agent Deck from anywhere.

**YepAnywhere reference:**
- `packages/server/src/remote-access/` — SRP credentials, session management
- `packages/server/src/crypto/` — SRP-6a server
- `packages/shared/src/crypto/` — SRP types
- `packages/shared/src/binary-framing.ts` — Wire protocol
- `packages/client/src/lib/connection/SecureConnection.ts` — E2E client

**What to build:**
- Go-side: SRP-6a implementation (or use existing Go SRP library)
- Go-side: NaCl secretbox encryption (`golang.org/x/crypto/nacl/secretbox`)
- Binary framing protocol: [version][nonce][ciphertext] envelope
- Session management: 7-day idle / 30-day max, max 5 per user
- Relay server (separate binary): dumb pipe matching clients to servers
- JS-side: SecureConnection with SRP handshake + session resume
- Config UI: Set password, enable/disable, manage sessions

**Dependencies:** `golang.org/x/crypto/nacl/secretbox`, Go SRP-6a library

**Key files in Agent Deck to modify:**
- New: `internal/remote/` package
- New: `internal/crypto/` package
- New: `cmd/agent-deck/relay_cmd.go` subcommand
- `internal/web/` — Remote access routes
- `internal/web/static/` — SecureConnection, login UI

**Estimated scope:** ~2000 lines Go + ~1500 lines JS

---

### 9. File Uploads via WebSocket

**Impact:** Share screenshots, files, code snippets with agents from the web dashboard.

**YepAnywhere reference:**
- `packages/server/src/routes/upload.ts` — WebSocket upload handler
- `packages/server/src/uploads/manager.ts` — Stream-to-disk
- `packages/client/src/api/upload.ts` — Client upload protocol

**What to build:**
- Go-side: WebSocket upload handler (start → binary chunks → end protocol)
- File management: sanitize filename, UUID prefix, size limits (100MB default)
- Message queue serialization to prevent race conditions
- Backpressure handling on write
- Storage at `~/.agent-deck/uploads/<session-id>/`
- JS-side: Upload via paste, file picker, or drag-and-drop
- Progress tracking UI

**Key files in Agent Deck to modify:**
- New: `internal/uploads/` package
- `internal/web/` — Upload WebSocket handler
- `internal/web/static/` — Upload UI components

**Estimated scope:** ~500 lines Go + ~400 lines JS

---

### 10. Streaming Content Throttle

**Impact:** Prevents DOM thrashing during rapid PTY output streaming.

**YepAnywhere reference:**
- `packages/client/src/hooks/useStreamingContent.ts` — 50ms throttle

**What to build:**
- JS-side: requestAnimationFrame or 50ms setTimeout batch for WebSocket terminal updates
- Leading+trailing edge throttle for API refetches on rapid events
- Buffer accumulation between flushes

**Key files in Agent Deck to modify:**
- `internal/web/static/` — Terminal WebSocket consumer

**Estimated scope:** ~100 lines JS

---

## Implementation Order Recommendation

**Phase 1 — Foundation (enables other features):**
1. Connection Resilience (#5) — prerequisite for reliable real-time features
2. Streaming Content Throttle (#10) — quick win, improves PTY performance
3. Server-Side Syntax Highlighting (#2) — prerequisite for tool renderers

**Phase 2 — Core Features:**
4. Tool-Specific Web Renderers (#1) — highest visual impact
5. Tiered Inbox (#4) — improves session discovery
6. EventBus Over WebSocket (#7) — enables real-time reactive dashboard

**Phase 3 — Mobile/Remote:**
7. Push Notifications (#3) — requires EventBus foundation
8. File Uploads (#9) — enhances mobile experience
9. DAG Conversation Model (#6) — enhances session management

**Phase 4 — Advanced:**
10. E2E Encrypted Remote Access (#8) — largest effort, highest long-term value
