# Merge Main into YepAnywhere Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Merge `origin/main` into `feature/YepAnywhere-Investigation`, resolving all conflicts in favor of the EventBus architecture while keeping all features from both branches.

**Architecture:** Single `git merge` with manual conflict resolution across 5 files. The EventBus+WebSocket pattern (Yep) replaces the SSE subscriber pattern (main). Main's new features (workspace, templates, headless, hub bridge) are already wired to use `notifyMenuChanged()`/`notifyTaskChanged()` which now emit EventBus events.

**Tech Stack:** Go 1.24+, EventBus pub/sub, WebSocket, Bubble Tea TUI, Docker SDK

---

### Task 1: Start the Merge

**Files:**
- All repository files (git merge operation)

**Step 1: Fetch latest and start merge**

```bash
git fetch origin main
git merge --no-ff origin/main
```

Expected: Merge fails with 5 CONFLICT files:
- `go.mod`
- `go.sum`
- `internal/web/server_test.go`
- `internal/web/static/dashboard.html`
- `internal/web/static/dashboard.js`

Auto-resolved files (verify these merged cleanly):
- `internal/web/server.go` — should have both EventBus fields AND main's workspace/template/bridge fields
- `internal/web/handlers_hub.go` — should have main's extended routes calling `notifyTaskChanged()`
- `internal/web/static/dashboard.css` — both sets of styles

**Step 2: Verify auto-merged server.go is correct**

Read `internal/web/server.go` and confirm:
- Imports include both `eventbus` and `workspace` packages
- Server struct has `eventBus`, `eventHub`, `hubTemplates`, `containerRuntime`, `hubBridge`
- Server struct does NOT have `menuSubscribers`, `taskSubscribers`, or their mutexes
- `notifyMenuChanged()` uses `s.eventBus.Emit(...)` NOT channel broadcast
- Routes include `/ws/events`, `/api/templates`, `/api/workspaces`

If server.go is wrong, fix it to match the above criteria.

**Step 3: Verify auto-merged handlers_hub.go compiles**

Check that `internal/web/handlers_hub.go` references methods that exist on the Server struct (especially `notifyTaskChanged()`, `s.hubTemplates`, `s.containerRuntime`, `s.hubBridge`).

---

### Task 2: Resolve go.mod Conflict

**Files:**
- Modify: `go.mod` (line ~42-48)

**Step 1: Resolve the conflict**

The conflict is between Yep's `regexp2` dependency and main's Docker SDK dependencies. Keep both. The resolved section should read:

```
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/docker/go-connections v0.6.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
```

Remove all `<<<<<<<`, `=======`, `>>>>>>>` markers.

**Step 2: Resolve go.sum**

Run:
```bash
go mod tidy
```

This will regenerate `go.sum` from the resolved `go.mod`, eliminating all 4 go.sum conflicts automatically.

**Step 3: Verify**

Run: `go build ./...`
Expected: Compiles successfully.

---

### Task 3: Resolve server_test.go Conflict

**Files:**
- Modify: `internal/web/server_test.go` (line ~195-223)

**Step 1: Resolve the conflict**

Keep BOTH test functions. The conflict is just two different new tests added at the same insertion point. Replace the conflict region with both functions sequentially:

```go
func TestHeadlessServerHealthz(t *testing.T) {
	// Headless mode passes nil MenuData — verify the server works correctly.
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Profile:    "test",
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, `"ok":true`) {
		t.Fatalf("expected health response to contain ok=true, got: %s", body)
	}
	if !strings.Contains(body, `"profile":"test"`) {
		t.Fatalf("expected health response to contain profile, got: %s", body)
	}
}

func TestNotifyMenuChangedEmitsEvent(t *testing.T) {
```

Remove all `<<<<<<<`, `=======`, `>>>>>>>` markers.

**Step 2: Verify**

Run: `go test -race -v ./internal/web/ -run "TestHeadless|TestNotifyMenu|TestNotifyTask"`
Expected: All 3 tests pass.

---

### Task 4: Resolve dashboard.html Conflict

**Files:**
- Modify: `internal/web/static/dashboard.html` (line ~92-99)

**Step 1: Resolve the conflict**

Keep BOTH elements — Yep's detail tabs AND main's claude-meta div. The claude-meta goes above the tabs (metadata about the session), then tabs (to switch between terminal and messages views). Replace the conflict region with:

```html
              <div class="claude-meta" id="claude-meta"></div>
              <div id="detail-tabs" class="detail-tabs">
                <button class="detail-tab detail-tab--active" data-tab="terminal">Terminal</button>
                <button class="detail-tab" data-tab="messages">Messages</button>
              </div>
```

Remove all `<<<<<<<`, `=======`, `>>>>>>>` markers.

---

### Task 5: Resolve dashboard.js Conflict 1 — getCardBorderColor

**Files:**
- Modify: `internal/web/static/dashboard.js` (line ~152-222)

**Step 1: Resolve the conflict**

This conflict has Yep's side empty (just `<<<<<<< HEAD` then `=======`) and main's side with `getCardBorderColor()` + `connectSSE()`. We want:
- **Keep** `getCardBorderColor()` from main — it improves card visibility for waiting agents
- **Drop** `connectSSE()` from main — Yep's ConnectionManager (WebSocket) replaces this entirely

Replace the entire conflict region (from `<<<<<<< HEAD` to `>>>>>>> origin/main`) with:

```javascript
  function getCardBorderColor(task) {
    if (task.agentStatus === "waiting") return "var(--orange)"
    return TASK_STATUS_COLORS[task.status] || "var(--text-dim)"
  }

```

Remove the entire `connectSSE()` function and all SSE-related code from the merged section. The `setConnectionState()` function that follows the conflict marker should remain as-is (both branches have it).

---

### Task 6: Resolve dashboard.js Conflict 2 — Tier Rendering vs Active/Completed

**Files:**
- Modify: `internal/web/static/dashboard.js` (line ~1499-1533)

**Step 1: Resolve the conflict**

Keep Yep's tier-based rendering. Drop main's active/completed split. Replace the conflict region with Yep's version only:

```javascript
    // Group by tier
    var tierBuckets = {}
    for (var td = 0; td < TIER_DEFS.length; td++) {
      tierBuckets[TIER_DEFS[td].key] = []
    }
    for (var k = 0; k < visible.length; k++) {
      var tierKey = visible[k].tier || "idle"
      if (!tierBuckets[tierKey]) tierBuckets[tierKey] = []
      tierBuckets[tierKey].push(visible[k])
    }

    // Render each non-empty tier section
    for (var t = 0; t < TIER_DEFS.length; t++) {
      var def = TIER_DEFS[t]
      var bucket = tierBuckets[def.key]
      if (bucket.length === 0) continue
```

Remove all `<<<<<<<`, `=======`, `>>>>>>>` markers. The code that follows (tier section rendering with headers, collapse toggles, cards) should remain as-is — it's all from Yep.

---

### Task 7: Resolve dashboard.js Conflict 3 — Messages Tab vs Add Project Modal

**Files:**
- Modify: `internal/web/static/dashboard.js` (line ~3024-3210+)

**Step 1: Resolve the conflict**

Keep BOTH. Yep's messages tab code comes first, then main's add-project modal code. Replace the conflict markers:

- Keep everything from `<<<<<<< HEAD` side (Yep's `loadSessionMessages()`, `renderMessages()`, `switchDetailTab()`)
- Keep everything from `>>>>>>> origin/main` side (main's add-project modal functions)
- Place Yep's code first, then main's code

The result should flow:
```
  // ── Messages tab ────────────────────────────────────────────────
  function loadSessionMessages(sessionId) { ... }
  function renderMessages(messages) { ... }
  function switchDetailTab(tabName) { ... }

  // ── Add Project modal ────────────────────────────────────────────
  var addProjectModal = document.getElementById("add-project-modal")
  ...
```

Remove all `<<<<<<<`, `=======`, `>>>>>>>` markers.

---

### Task 8: Build and Verify

**Step 1: Verify no remaining conflict markers**

```bash
grep -r "<<<<<<< HEAD" . --include="*.go" --include="*.js" --include="*.html" --include="*.css"
```

Expected: No output (no remaining conflict markers).

**Step 2: Build**

```bash
make build
```

Expected: Successful build to `./build/agent-deck`.

**Step 3: Run tests**

```bash
go test -race -v ./internal/web/... 2>&1 | tail -30
```

Expected: All tests pass. Key tests to watch:
- `TestNotifyMenuChangedEmitsEvent` — confirms EventBus wiring
- `TestNotifyTaskChangedEmitsEvent` — confirms task EventBus wiring
- `TestHeadlessServerHealthz` — confirms headless mode works
- All existing Yep tests (upload, augments, EventBus, push)

**Step 4: Run full test suite**

```bash
make test
```

Expected: All tests pass (some may skip if no tmux server).

---

### Task 9: Commit the Merge

**Step 1: Stage all resolved files**

```bash
git add go.mod go.sum internal/web/server_test.go internal/web/static/dashboard.html internal/web/static/dashboard.js
```

**Step 2: Complete the merge commit**

```bash
git commit -m "$(cat <<'EOF'
merge: integrate main into YepAnywhere with EventBus architecture

Merge origin/main (workspace, templates, headless, hub bridge, visual
improvements) into YepAnywhere branch. All real-time events use the
EventBus+WebSocket pattern — SSE subscriber maps from main are dropped.

Conflict resolutions:
- server.go: auto-merged — EventBus + main's new fields/routes
- dashboard.js: keep Yep's tiered inbox + ConnectionManager, add main's
  getCardBorderColor + add-project modal, drop connectSSE()
- dashboard.html: keep both claude-meta and detail tabs
- server_test.go: keep both new test functions
- go.mod/go.sum: keep both dependency sets (Chroma + Docker SDK)

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: Post-Merge Smoke Test

**Step 1: Start headless server**

```bash
./build/agent-deck web --headless &
sleep 2
```

**Step 2: Verify endpoints respond**

```bash
curl -s http://127.0.0.1:8420/healthz | python3 -m json.tool
curl -s http://127.0.0.1:8420/ | head -5
curl -s http://127.0.0.1:8420/api/templates | head -3
curl -s http://127.0.0.1:8420/api/workspaces | head -3
```

Expected: healthz returns `{"ok": true}`, index returns HTML, templates/workspaces return JSON.

**Step 3: Stop server and clean up**

```bash
kill %1
```
