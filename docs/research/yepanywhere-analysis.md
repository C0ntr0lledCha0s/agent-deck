# YepAnywhere Analysis — Feature Research for Agent Deck

**Date:** 2026-02-25
**Repository:** https://github.com/kzahel/yepanywhere (MIT License)
**Local clone:** ~/github-code/yepanywhere/

## Overview

YepAnywhere is a self-hosted web UI for Claude Code and Codex agents. It provides mobile-first supervision of AI coding agents with push notifications, file uploads, E2E encrypted remote access, and rich tool rendering — all without cloud accounts or databases.

**Tech stack:** TypeScript, Node.js 20+, Hono (server), React 19 (client), Vite, pnpm monorepo, NaCl + SRP-6a crypto.

**Key architectural difference from Agent Deck:**
- Agent Deck controls agents via tmux pane keystrokes + prompt pattern matching
- YepAnywhere reads Claude's JSONL files directly + uses the official `@anthropic-ai/claude-agent-sdk` to spawn processes

---

## Architecture

### Monorepo Structure (6 packages)

```
packages/
  shared/     — Types, Zod schemas, DAG, binary framing, crypto, relay protocol
  client/     — React 19 SPA (50+ components, 50+ hooks, 9-file connection layer)
  server/     — Hono server (24 modules)
  relay/      — E2E encrypted relay server
  desktop/    — Tauri (Rust) wrapper
  mobile/     — Android (Kotlin)
```

### Server Modules (packages/server/src/)

| Module | Purpose |
|--------|---------|
| `app.ts` | Hono app creation, route mounting, middleware, reader cache |
| `config.ts` | Environment-based config with profile support (`YEP_ANYWHERE_PROFILE`) |
| `sdk/providers/` | Claude, Codex, Codex-OSS, Gemini, Gemini-ACP, OpenCode adapters |
| `sessions/` | JSONL readers per provider + DAG + normalization + fork + pagination |
| `supervisor/` | Process lifecycle: Supervisor → Process → WorkerQueue |
| `watcher/` | EventBus + FileWatcher + BatchProcessor |
| `augments/` | Server-side Shiki syntax highlighting + diff rendering + streaming |
| `push/` | Web Push (VAPID) + PushNotifier + connected-browser suppression |
| `remote-access/` | SRP-6a credentials + session management + relay |
| `routes/` | 25 route files (sessions, inbox, uploads, git, activity) |
| `auth/` | Cookie-based authentication |
| `crypto/` | SRP-6a server implementation |
| `highlighting/` | Shiki code highlighting engine |
| `indexes/` | Session index cache |
| `metadata/` | Session/project metadata persistence |
| `notifications/` | Read/unread tracking |
| `uploads/` | File upload management |
| `services/` | ConnectedBrowsers, BrowserProfile, Relay, Settings, Sharing, NetworkBinding |

### Client Architecture (packages/client/src/)

| Area | Key Files |
|------|-----------|
| Connection layer | `lib/connection/` — ConnectionManager, DirectConnection, WebSocketConnection, SecureConnection, RelayProtocol, SRP client, NaCl wrapper |
| Event system | `lib/activityBus.ts` — WebSocket activity event bus |
| Contexts | InboxContext (5-tier priority), RemoteConnectionContext, StreamingMarkdownContext, AgentContentContext |
| Hooks | useSessionStream, useStreamingContent (50ms throttle), useSession (1078 lines), usePushNotifications, useSpeechRecognition |
| Tool renderers | `components/renderers/tools/` — 16 registered renderers (BashRenderer, EditRenderer at 1094 lines, ReadRenderer, WriteRenderer, TaskRenderer, etc.) |
| Components | InboxContent (tiered), SessionPage (1180 lines), MessageList (ResizeObserver auto-scroll), MessageInput (voice, files, slash commands), ToolApprovalPanel, QuestionAnswerPanel |

---

## Borrowable Features — Detailed Analysis

### 1. Tool-Specific Web Renderers

**What:** Registry-based pluggable renderers for each tool type (Bash, Edit, Read, Write, Grep, Glob, WebSearch, Task, etc.) with 4 display modes: inline, interactive summary, collapsed preview, expandable.

**How it works:**
- `ToolRendererRegistry` maps tool names to renderer objects
- Tool name aliases normalize cross-provider names (`shell_command → Bash`, `apply_patch → Edit`)
- `BashRenderer`: Command in code block, stdout/stderr with 20-line collapse, 4-line collapsed preview
- `EditRenderer` (1094 lines): Server-rendered diff HTML via `jsdiff.structuredPatch()`, Shiki-highlighted, "Show full context" toggle, error classification for user rejections
- `ReadRenderer`/`WriteRenderer`: Syntax-highlighted file content, markdown source/preview toggle
- `TaskRenderer` (610 lines): Nested subagent content, auto-scroll, status badges, context usage percentage

**Agent Deck relevance:** Currently shows raw terminal output in web dashboard. Tool renderers would make output dramatically more readable.

**Implementation approach:** Go-side Chroma library for syntax highlighting. Compute diffs server-side. Send structured JSON to web client with pre-rendered HTML fragments.

### 2. Server-Side Syntax Highlighting

**What:** All markdown/code highlighting done server-side using Shiki with CSS variables theme, sent as pre-rendered HTML.

**How it works:**
- `AugmentGenerator` uses Shiki with `createCssVariablesTheme()` — enables light/dark toggle without re-rendering
- `BlockDetector` incrementally parses streaming markdown into completed blocks (code, heading, paragraph, list)
- `StreamCoordinator` feeds text deltas through block detection → augment generation
- Dynamic language loading: unknown languages loaded on demand
- Incomplete code blocks rendered optimistically during streaming

**Agent Deck relevance:** Web dashboard could offload all highlighting to the Go server, keeping the browser client lightweight (especially important for mobile).

**Implementation approach:** Go's `github.com/alecthomas/chroma` library provides equivalent functionality. Emit HTML with CSS variable classes for theme support.

### 3. Push Notifications

**What:** Web Push notifications (VAPID) when an agent needs approval, with connected-browser suppression and dismiss events.

**How it works:**
- `PushService`: Stores subscriptions per browser profile in JSON, uses `web-push` npm library, auto-cleans expired (410/404)
- `PushNotifier`: Subscribes to EventBus for `process-state-changed`, sends on `waiting-input` transition, sends `dismiss` when resolved
- **Smart suppression:** Skips push for browser profiles with active WebSocket (`ConnectedBrowsersService`)
- **Save debouncing:** Coalesces rapid subscription changes to avoid disk I/O storms
- VAPID keys auto-generated on first run, stored with 0600 permissions
- **Service Worker** (`sw.js`): Handles push events, shows OS notifications, click opens session URL, conditional `clients.claim()` to avoid disrupting active connections
- Notification types: `pending-input`, `session-halted`, `dismiss`, `test`

**Agent Deck relevance:** Users could get mobile alerts when an agent is stuck waiting for approval while away from their desk.

**Implementation approach:** Go's `github.com/SherClockHolmes/webpush-go` library. Add VAPID key generation, subscription storage in SQLite, push delivery with browser suppression.

### 4. Tiered Inbox / Priority System

**What:** Sessions categorized into 5 priority tiers for the dashboard.

**How it works:**
- Tiers: `needsAttention` > `active` > `recentActivity` > `unread8h` > `unread24h`
- Each tier capped at 20 items, sorted by `updatedAt` descending
- Sessions assigned to exactly one tier (highest priority wins)
- Archived sessions excluded
- `mergeWithStableOrder()` prevents UI jank: existing items keep position, new items append
- Debounced refetch (500ms) on SSE events
- Badges: "Approval" (yellow), "Question" (blue) for needsAttention; pulsing dot for active

**Agent Deck relevance:** Hub dashboard currently shows flat session/task lists. Priority tiers would surface the most important sessions immediately.

**Implementation approach:** Server-side tier assignment in the sessions API. Client-side stable merge algorithm.

### 5. Connection Resilience (WebSocket/SSE)

**What:** Production-hardened connection management with stale detection, reconnection, and subscription re-establishment.

**How it works:**
- `ConnectionManager` state machine: `connected → reconnecting → disconnected`
- **Stale detection:** Interval checks `now - lastEventAt > 45s`, triggers reconnect
- **Visibility change:** When tab becomes visible, sends ping. If no pong in 2s, declares stale
- **Exponential backoff:** `baseDelay=1s`, `maxDelay=30s`, `maxAttempts=10`, with jitter
- **Subscription re-establishment:** After reconnect, all active subscriptions re-subscribed automatically
- **Injectable interfaces:** `TimerInterface`, `VisibilityInterface` for testability
- `ActivityBus` emits synthetic `reconnect` event so all listeners refetch independently
- `forceReconnect()` method for manual recovery

**Agent Deck relevance:** Current SSE connections have no reconnect logic. Mobile users would lose real-time updates without knowing.

**Implementation approach:** Implement equivalent state machine in JavaScript for the web dashboard. Add server-side keepalive/heartbeat to SSE streams.

### 6. DAG Conversation Model

**What:** Conversations modeled as a directed acyclic graph via `parentUuid` for robust forking and branch management.

**How it works:**
- Build `uuid → node` and `parentUuid → children[]` maps
- Find tips (leaf nodes with no children)
- Select active tip: most recent timestamp, tiebreak by branch length, then lineIndex
- Walk from tip to root via parentUuid chain → active branch
- **Compaction handling:** `compact_boundary` entries with `logicalParentUuid` bridge compaction gaps
- **Orphaned tool detection:** Scans ALL messages for `tool_result` IDs, finds `tool_use` blocks on active branch with no matching result
- **Sibling branch collection:** BFS from branch points to collect complete subtrees for parallel Tasks
- `needsReorder()` O(n) check — only runs full reorder when messages are actually out of order

**Agent Deck relevance:** Agent Deck already has session forking. A DAG model enables fork-from-any-message, branch visualization, and proper dead branch pruning.

**Implementation approach:** Go implementation of DAG builder. Store branch metadata in statedb. UI visualization in web dashboard.

### 7. EventBus Over WebSocket

**What:** Typed pub/sub event system over a single WebSocket connection, replacing HTTP polling.

**How it works:**
- Server `EventBus`: Simple `Set<Handler>` with error-isolated dispatch, 16 typed events
- Events: `file-change`, `session-status-changed`, `session-created`, `session-updated`, `process-state-changed`, `session-seen`, `session-metadata-changed`, `browser-tab-connected/disconnected`, `source-change`, `worker-activity-changed`
- Client `ActivityBus`: Wraps `connection.subscribeActivity()`, distributes via `on(type, callback) → unsubscribe`
- Synthetic `reconnect` and `refresh` events on connection recovery

**Agent Deck relevance:** Replace SSE polling with WebSocket event bus for the web dashboard. More efficient and enables bidirectional communication.

**Implementation approach:** Extend existing WebSocket infrastructure. Define typed Go events. Add subscription management.

### 8. E2E Encrypted Remote Access

**What:** SRP-6a zero-knowledge authentication + NaCl secretbox encryption through an optional relay server.

**How it works:**
- **SRP-6a handshake:** Client proves knowledge of password without transmitting it. Server stores verifier only.
- **Session resumption:** Stored session key + server-issued nonce → transport key derivation. Avoids re-entering password.
- **NaCl encryption:** XSalsa20-Poly1305 with 24-byte nonces. Sequence numbers prevent replay.
- **Binary envelope:** `[version][nonce][ciphertext]` → decrypt → `[format byte][payload]`
- **Relay as dumb pipe:** Matches clients to servers by username, forwards opaque encrypted blobs
- **Remote session management:** 7-day idle / 30-day max lifetime, max 5 sessions per user
- Four supported modes: Secure Relay, Tailscale, Cloudflare Tunnel, Caddy+SSH

**Agent Deck relevance:** Would enable secure mobile access to Agent Deck's web dashboard from anywhere, without exposing the server directly.

**Implementation approach:** Go has `golang.org/x/crypto/nacl/secretbox` (exact same primitive). SRP-6a Go libraries available. Build relay as separate binary or embedded option.

### 9. File Uploads via WebSocket

**What:** Binary chunked file upload protocol over WebSocket with progress tracking and backpressure.

**How it works:**
- Client sends JSON `start { name, size, mimeType }`, then binary chunks (64KB), then JSON `end`
- Server responds with `progress` (every 64KB) and final `complete` with file metadata
- Size enforcement at both start and per-chunk (prevents bypass)
- Message queue serialization prevents race conditions
- Backpressure via write stream drain events
- Files stored at `~/.yep-anywhere/uploads/<projectId>/<sessionId>/<uuid>_<sanitized-name>`
- Binary framing for relay: `encodeUploadChunkFrame(id, offset, chunk)`

**Agent Deck relevance:** Users could share screenshots, files, or code snippets with agents from the web dashboard.

**Implementation approach:** Add WebSocket upload handler to existing web server. Store uploads in `~/.agent-deck/uploads/`. Pass file paths to agent sessions.

### 10. Streaming Content Throttle

**What:** 50ms throttle on WebSocket → React state updates to prevent render storms.

**How it works:**
- `useStreamingContent` hook accumulates content block deltas
- Batches at 50ms intervals (`STREAMING_THROTTLE_MS`)
- Handles `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_stop`
- Routes subagent streams via `parentToolUseId`
- Leading+trailing edge throttle for API refetches on rapid events

**Agent Deck relevance:** PTY WebSocket streaming to the web dashboard could benefit from similar batching.

**Implementation approach:** Add client-side requestAnimationFrame or setTimeout batching to the existing terminal WebSocket consumer.

---

## Additional Patterns Worth Noting

### LRU Reader Cache
Map-based cache (500 entries) for session readers. Prevents re-creating parsers for frequently accessed sessions. Agent Deck could cache tmux pane readers similarly.

### Two-Bucket SSE Replay Buffer
15s bucket swap gives 15-30s of replay history for late-joining clients with bounded memory. No bookkeeping needed — just swap and clear.

### Provider-Agnostic Normalization
Clean separation between raw provider formats and unified `Message[]`. Tool name aliasing (`shell_command → Bash`). Shell command classification (`cat file → Read`, `rg pattern → Grep`).

### Claude SDK JSONL Schemas
Complete Zod schemas for all Claude JSONL entry types. The `init` system entry contains available tools, MCP servers, model info, CLI version. Compaction entries (`compact_boundary`, `microcompact_boundary`) are critical for context tracking.

### Approval UX Patterns
- 150ms click protection on approval buttons
- Keyboard shortcuts: `1/2/3` for options, `Enter/Escape` for confirm/deny
- "Tell Claude what to do instead" deny-with-feedback
- Draft persistence to localStorage

### Mobile Detection
`hasCoarsePointer()` media query instead of user-agent sniffing. Mobile: Enter = newline, send button required. Desktop: Enter = send.

### Context Usage Tracking
Computes post-compaction overhead: `overhead = preTokens - lastPreCompactionAssistantTokens`. Shows context window fill percentage in UI.

---

## Comparative Analysis

| Aspect | YepAnywhere | Agent Deck |
|--------|-------------|------------|
| **Language** | TypeScript (Node.js) | Go |
| **Frontend** | React 19 SPA (Vite) | Bubble Tea TUI + embedded static web |
| **Session detection** | Reads JSONL files directly | tmux pane polling + prompt patterns |
| **Agent control** | Official `@anthropic-ai/claude-agent-sdk` | Sends keystrokes to tmux panes |
| **Real-time** | Single WebSocket with relay protocol | WebSocket PTY streaming + SSE |
| **Remote access** | E2E encrypted relay (SRP-6a + NaCl) | None (local only) |
| **Storage** | No database (JSON files + JSONL) | SQLite via statedb |
| **Process model** | In-process SDK (server-owned) | tmux pane management |
| **Mobile** | Mobile-first design, PWA | Desktop TUI first |
| **Push notifications** | Web Push with VAPID | None |
| **File uploads** | WebSocket binary streaming | None |
| **Multi-provider** | Claude, Codex, Codex-OSS, Gemini, Gemini-ACP, OpenCode | Claude, Gemini, OpenCode |
| **Syntax highlighting** | Server-side (Shiki) | None in web dashboard |
| **Tool rendering** | 16 specialized renderers | Raw terminal output |
| **Connection resilience** | Full state machine + reconnect | Basic SSE |
| **Session forking** | DAG-based, fork from any message | tmux pane cloning |
| **Inbox** | 5-tier priority system | Flat list |
| **Auth** | SRP-6a (zero-knowledge) + cookies | None |
