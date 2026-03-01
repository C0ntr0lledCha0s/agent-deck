# Code Patterns Reference — YepAnywhere → Agent Deck

**Specific implementation patterns from YepAnywhere that can be adapted for Go.**

---

## Pattern: EventBus with Error-Isolated Dispatch

**Source:** `packages/server/src/watcher/EventBus.ts`

Simple, effective pub/sub. One bad handler doesn't crash others.

```go
// Go equivalent
type EventType string

const (
    EventSessionStatusChanged EventType = "session-status-changed"
    EventSessionCreated       EventType = "session-created"
    EventProcessStateChanged  EventType = "process-state-changed"
    // ...
)

type Event struct {
    Type    EventType
    Payload interface{}
}

type EventBus struct {
    mu          sync.RWMutex
    subscribers map[int]func(Event)
    nextID      int
}

func (eb *EventBus) Subscribe(handler func(Event)) func() {
    eb.mu.Lock()
    id := eb.nextID
    eb.nextID++
    eb.subscribers[id] = handler
    eb.mu.Unlock()
    return func() {
        eb.mu.Lock()
        delete(eb.subscribers, id)
        eb.mu.Unlock()
    }
}

func (eb *EventBus) Emit(event Event) {
    eb.mu.RLock()
    handlers := make([]func(Event), 0, len(eb.subscribers))
    for _, h := range eb.subscribers {
        handlers = append(handlers, h)
    }
    eb.mu.RUnlock()
    for _, h := range handlers {
        func() {
            defer func() { recover() }() // error isolation
            h(event)
        }()
    }
}
```

---

## Pattern: Two-Bucket Replay Buffer

**Source:** `packages/server/src/supervisor/Process.ts`

Bounded memory buffer giving 15-30s replay for late-joining SSE/WS clients.

```go
type ReplayBuffer[T any] struct {
    mu             sync.RWMutex
    current        []T
    previous       []T
    swapInterval   time.Duration
    ticker         *time.Ticker
}

func NewReplayBuffer[T any](swapInterval time.Duration) *ReplayBuffer[T] {
    rb := &ReplayBuffer[T]{
        swapInterval: swapInterval,
        ticker:       time.NewTicker(swapInterval),
    }
    go rb.swapLoop()
    return rb
}

func (rb *ReplayBuffer[T]) Add(item T) {
    rb.mu.Lock()
    rb.current = append(rb.current, item)
    rb.mu.Unlock()
}

func (rb *ReplayBuffer[T]) Replay() []T {
    rb.mu.RLock()
    defer rb.mu.RUnlock()
    result := make([]T, 0, len(rb.previous)+len(rb.current))
    result = append(result, rb.previous...)
    result = append(result, rb.current...)
    return result
}

func (rb *ReplayBuffer[T]) swapLoop() {
    for range rb.ticker.C {
        rb.mu.Lock()
        rb.previous = rb.current
        rb.current = nil
        rb.mu.Unlock()
    }
}
```

---

## Pattern: Tiered Priority Assignment

**Source:** `packages/server/src/routes/inbox.ts`

```go
type InboxTier string

const (
    TierNeedsAttention InboxTier = "needsAttention"
    TierActive         InboxTier = "active"
    TierRecentActivity InboxTier = "recentActivity"
    TierUnread8h       InboxTier = "unread8h"
    TierUnread24h      InboxTier = "unread24h"
)

func AssignTier(s *Session, now time.Time) InboxTier {
    if s.PendingInputType != "" {
        return TierNeedsAttention
    }
    if s.ProcessState == "running" {
        return TierActive
    }
    if now.Sub(s.UpdatedAt) < 30*time.Minute {
        return TierRecentActivity
    }
    if !s.IsRead && now.Sub(s.UpdatedAt) < 8*time.Hour {
        return TierUnread8h
    }
    if !s.IsRead && now.Sub(s.UpdatedAt) < 24*time.Hour {
        return TierUnread24h
    }
    return "" // excluded
}
```

---

## Pattern: BatchProcessor with Key Deduplication

**Source:** `packages/server/src/watcher/BatchProcessor.ts`

Coalesces rapid events (e.g., file changes) into single processing tasks.

```go
type BatchProcessor[T any] struct {
    mu          sync.Mutex
    pending     map[string]func() T
    batchWindow time.Duration
    concurrency int
    timer       *time.Timer
    results     chan T
}

func (bp *BatchProcessor[T]) Enqueue(key string, task func() T) {
    bp.mu.Lock()
    bp.pending[key] = task // replaces previous task for same key
    if bp.timer == nil {
        bp.timer = time.AfterFunc(bp.batchWindow, bp.flush)
    }
    bp.mu.Unlock()
}

func (bp *BatchProcessor[T]) flush() {
    bp.mu.Lock()
    tasks := bp.pending
    bp.pending = make(map[string]func() T)
    bp.timer = nil
    bp.mu.Unlock()

    sem := make(chan struct{}, bp.concurrency)
    for _, task := range tasks {
        sem <- struct{}{}
        go func(t func() T) {
            defer func() { <-sem }()
            bp.results <- t()
        }(task)
    }
}
```

---

## Pattern: File Watcher with Debounce and Mtime Dedup

**Source:** `packages/server/src/watcher/FileWatcher.ts`

```go
type FileWatcher struct {
    debounceMs    time.Duration
    debounceTimers map[string]*time.Timer
    lastMtimes     map[string]int64
    eventBus       *EventBus
}

func (fw *FileWatcher) handleEvent(path string) {
    // Cancel existing timer for this path
    if timer, ok := fw.debounceTimers[path]; ok {
        timer.Stop()
    }
    // Set new debounce timer
    fw.debounceTimers[path] = time.AfterFunc(fw.debounceMs, func() {
        delete(fw.debounceTimers, path)
        // Mtime dedup
        info, err := os.Stat(path)
        if err != nil { return }
        mtime := info.ModTime().UnixMilli()
        if fw.lastMtimes[path] == mtime { return }
        fw.lastMtimes[path] = mtime
        fw.eventBus.Emit(Event{Type: EventFileChanged, Payload: path})
    })
}
```

---

## Pattern: WebSocket Upload Protocol

**Source:** `packages/server/src/routes/upload.ts`

```
Client → Server: JSON { type: "start", fileName, fileSize, mimeType }
Client → Server: Binary chunk (64KB)
Client → Server: Binary chunk (64KB)
...
Client → Server: JSON { type: "complete" }
Server → Client: JSON { type: "progress", received, total } (every 64KB)
Server → Client: JSON { type: "complete", uploadId, filePath, fileSize }
Server → Client: JSON { type: "error", message }
```

Key: Message queue serialization prevents binary chunks arriving before async `startUpload()` completes.

---

## Pattern: ConnectionManager State Machine (JS)

**Source:** `packages/client/src/lib/connection/ConnectionManager.ts`

```javascript
class ConnectionManager {
  state = 'connected'; // connected | reconnecting | disconnected
  lastEventAt = Date.now();
  reconnectAttempts = 0;

  CONFIG = {
    staleThresholdMs: 45000,
    pongTimeoutMs: 2000,
    baseDelayMs: 1000,
    maxDelayMs: 30000,
    maxAttempts: 10,
  };

  recordEvent() { this.lastEventAt = Date.now(); }

  checkStale() {
    if (Date.now() - this.lastEventAt > this.CONFIG.staleThresholdMs) {
      this.reconnect();
    }
  }

  onVisibilityChange() {
    if (document.visibilityState === 'visible') {
      this.sendPing();
      setTimeout(() => {
        if (!this.receivedPong) this.reconnect();
      }, this.CONFIG.pongTimeoutMs);
    }
  }

  reconnect() {
    this.state = 'reconnecting';
    const delay = Math.min(
      this.CONFIG.baseDelayMs * Math.pow(2, this.reconnectAttempts),
      this.CONFIG.maxDelayMs
    ) * (0.5 + Math.random() * 0.5); // jitter
    setTimeout(() => this.attemptConnect(), delay);
  }
}
```

---

## Pattern: Stable Merge for Real-Time Lists

**Source:** `packages/client/src/contexts/InboxContext.tsx`

Prevents UI jank when SSE/WS events update a list.

```javascript
function mergeWithStableOrder(existing, incoming) {
  const existingMap = new Map(existing.map(item => [item.id, item]));
  const result = [];
  const seen = new Set();

  // Keep existing order, update data
  for (const item of existing) {
    const updated = incoming.find(i => i.id === item.id);
    if (updated) {
      result.push(updated);
      seen.add(item.id);
    }
    // Items removed from incoming are dropped
  }

  // Append new items at end
  for (const item of incoming) {
    if (!seen.has(item.id)) {
      result.push(item);
    }
  }

  return result;
}
```

---

## Pattern: Push Notification with Browser Suppression

**Source:** `packages/server/src/push/PushNotifier.ts`

```go
func (pn *PushNotifier) OnProcessStateChanged(sessionID string, newState string) {
    if newState == "waiting-input" {
        // Get connected browser profile IDs
        connectedIDs := pn.connectedBrowsers.GetConnectedIDs()
        // Send push to all EXCEPT connected browsers
        pn.pushService.SendToAll(payload, ExcludeIDs(connectedIDs))
        pn.notifiedSessions.Add(sessionID)
    } else if pn.notifiedSessions.Has(sessionID) {
        // Send dismiss to close notification on other devices
        pn.pushService.SendToAll(dismissPayload, nil)
        pn.notifiedSessions.Remove(sessionID)
    }
}
```

---

## Pattern: CSS Variables Theme for Syntax Highlighting

**Source:** `packages/server/src/augments/augment-generator.ts`

Enables light/dark mode toggle without re-rendering highlighted code.

```css
/* Light theme */
:root {
  --shiki-foreground: #24292e;
  --shiki-background: #ffffff;
  --shiki-token-comment: #6a737d;
  --shiki-token-keyword: #d73a49;
  --shiki-token-string: #032f62;
  --shiki-token-function: #6f42c1;
}

/* Dark theme */
[data-theme="dark"] {
  --shiki-foreground: #e1e4e8;
  --shiki-background: #24292e;
  --shiki-token-comment: #6a737d;
  --shiki-token-keyword: #f97583;
  --shiki-token-string: #9ecbff;
  --shiki-token-function: #b392f0;
}
```

Go's Chroma can output HTML with class names. Map Chroma classes to CSS variables.

---

## Pattern: Service Worker Push Handler

**Source:** `packages/client/public/sw.js`

```javascript
self.addEventListener('push', (event) => {
  const data = event.data?.json();
  if (!data) return;

  if (data.type === 'dismiss') {
    // Close existing notification for this session
    event.waitUntil(
      self.registration.getNotifications({ tag: data.sessionId })
        .then(notifications => notifications.forEach(n => n.close()))
    );
    return;
  }

  // Check if user is already viewing this session
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true })
      .then(clients => {
        const isViewing = clients.some(c =>
          c.focused && c.url?.includes(`/s/${data.sessionId}`)
        );
        if (isViewing) return; // suppress

        return self.registration.showNotification(data.title, {
          body: data.body,
          tag: data.sessionId, // replaces existing for same session
          data: { url: `/s/${data.sessionId}` },
          requireInteraction: true,
        });
      })
  );
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = event.notification.data?.url;
  event.waitUntil(
    self.clients.matchAll({ type: 'window' }).then(clients => {
      // Focus existing window if available
      const existing = clients.find(c => c.url?.includes(url));
      if (existing) return existing.focus();
      return self.clients.openWindow(url);
    })
  );
});
```
