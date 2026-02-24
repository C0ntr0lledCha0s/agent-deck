# Hub Phase 3: Multi-Project Routing — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add keyword-based message routing that matches natural language task descriptions to projects from `projects.yaml`, exposed via `POST /api/route` and integrated into the dashboard's new task flow.

**Architecture:** New `router.go` in the hub package implements case-insensitive keyword matching with confidence scoring. The web layer exposes it as `POST /api/route`. The frontend calls this endpoint as the user types a task description, auto-suggesting the best-matching project.

**Tech Stack:** Go (hub package for routing logic, web package for HTTP handler), vanilla JS (debounced API calls).

**Prerequisites:** Phase 2 (Task Creation & Input) complete. `POST /api/tasks` and New Task modal working.

---

## Task 1: Route function — keyword matching engine

**Files:**
- Create: `internal/hub/router.go`
- Create: `internal/hub/router_test.go`

**Step 1: Write failing tests**

Create `internal/hub/router_test.go`:

```go
package hub

import (
	"testing"
)

func TestRouteExactKeywordMatch(t *testing.T) {
	projects := []*Project{
		{Name: "api-service", Keywords: []string{"api", "backend", "auth"}},
		{Name: "web-app", Keywords: []string{"frontend", "ui", "react"}},
	}

	result := Route("Fix the auth endpoint in the API", projects)

	if result == nil {
		t.Fatal("expected a route result")
	}
	if result.Project != "api-service" {
		t.Fatalf("expected api-service, got %s", result.Project)
	}
	if result.Confidence <= 0 {
		t.Fatalf("expected positive confidence, got %f", result.Confidence)
	}
	if len(result.MatchedKeywords) == 0 {
		t.Fatal("expected matched keywords")
	}
}

func TestRouteMultipleKeywordsIncreasesConfidence(t *testing.T) {
	projects := []*Project{
		{Name: "api-service", Keywords: []string{"api", "backend", "auth"}},
		{Name: "web-app", Keywords: []string{"frontend", "ui", "react"}},
	}

	single := Route("Fix the API", projects)
	multi := Route("Fix the auth endpoint in the API backend", projects)

	if single == nil || multi == nil {
		t.Fatal("expected route results")
	}
	if multi.Confidence <= single.Confidence {
		t.Fatalf("more keywords should increase confidence: single=%f multi=%f",
			single.Confidence, multi.Confidence)
	}
}

func TestRouteNoMatch(t *testing.T) {
	projects := []*Project{
		{Name: "api-service", Keywords: []string{"api", "backend", "auth"}},
	}

	result := Route("Update the documentation for kubernetes", projects)

	if result != nil {
		t.Fatalf("expected nil for no match, got project=%s confidence=%f",
			result.Project, result.Confidence)
	}
}

func TestRouteCaseInsensitive(t *testing.T) {
	projects := []*Project{
		{Name: "api-service", Keywords: []string{"api", "backend"}},
	}

	result := Route("Fix the API Backend", projects)

	if result == nil {
		t.Fatal("expected case-insensitive match")
	}
	if result.Project != "api-service" {
		t.Fatalf("expected api-service, got %s", result.Project)
	}
}

func TestRouteBestMatchWins(t *testing.T) {
	projects := []*Project{
		{Name: "api-service", Keywords: []string{"api", "backend", "auth"}},
		{Name: "web-app", Keywords: []string{"frontend", "ui", "react", "api"}},
	}

	// "api" matches both, but "auth" and "backend" tip toward api-service.
	result := Route("Fix the auth in the API backend", projects)

	if result == nil {
		t.Fatal("expected a route result")
	}
	if result.Project != "api-service" {
		t.Fatalf("expected api-service (3 matches), got %s", result.Project)
	}
}

func TestRouteEmptyProjects(t *testing.T) {
	result := Route("Fix something", nil)
	if result != nil {
		t.Fatal("expected nil for empty projects")
	}
}

func TestRouteEmptyMessage(t *testing.T) {
	projects := []*Project{
		{Name: "api-service", Keywords: []string{"api"}},
	}
	result := Route("", projects)
	if result != nil {
		t.Fatal("expected nil for empty message")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/hub/... -count=1 -run "TestRoute" -v`
Expected: FAIL — `Route` function not defined.

**Step 3: Implement Route function**

Create `internal/hub/router.go`:

```go
package hub

import (
	"strings"
)

// Route matches a natural language message against project keywords.
// Returns the best-matching project with confidence score, or nil if no keywords match.
// Confidence = matched keywords / total keywords for the winning project.
func Route(message string, projects []*Project) *RouteResult {
	if message == "" || len(projects) == 0 {
		return nil
	}

	words := strings.Fields(strings.ToLower(message))

	var bestProject string
	var bestCount int
	var bestTotal int
	var bestKeywords []string

	for _, p := range projects {
		if len(p.Keywords) == 0 {
			continue
		}

		var matched []string
		for _, kw := range p.Keywords {
			kwLower := strings.ToLower(kw)
			for _, w := range words {
				if w == kwLower || strings.Contains(w, kwLower) {
					matched = append(matched, kw)
					break
				}
			}
		}

		if len(matched) > bestCount {
			bestProject = p.Name
			bestCount = len(matched)
			bestTotal = len(p.Keywords)
			bestKeywords = matched
		}
	}

	if bestCount == 0 {
		return nil
	}

	return &RouteResult{
		Project:         bestProject,
		Confidence:      float64(bestCount) / float64(bestTotal),
		MatchedKeywords: bestKeywords,
	}
}
```

**Step 4: Run tests**

Run: `go test ./internal/hub/... -count=1 -run "TestRoute" -v`
Expected: ALL PASS.

**Step 5: Run all hub tests to verify no regression**

Run: `go test ./internal/hub/... -count=1 -v`
Expected: ALL PASS.

**Step 6: Commit**

```bash
git add internal/hub/router.go internal/hub/router_test.go
git commit -m "feat(hub): add keyword-based message routing engine"
```

---

## Task 2: POST /api/route endpoint

**Files:**
- Modify: `internal/web/handlers_hub.go`
- Modify: `internal/web/handlers_hub_test.go`
- Modify: `internal/web/server.go`

**Step 1: Write failing tests**

Add to `internal/web/handlers_hub_test.go`:

```go
func TestRouteEndpoint(t *testing.T) {
	srv := newTestServerWithHub(t)

	// Write projects.yaml
	hubDir := filepath.Dir(srv.hubProjects.FilePath())
	yaml := `projects:
  - name: api-service
    path: /home/user/code/api
    keywords:
      - api
      - backend
      - auth
  - name: web-app
    path: /home/user/code/web
    keywords:
      - frontend
      - ui
      - react
`
	if err := os.WriteFile(filepath.Join(hubDir, "projects.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write projects.yaml: %v", err)
	}

	body := `{"message":"Fix the auth endpoint in the API"}`
	req := httptest.NewRequest(http.MethodPost, "/api/route", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp hub.RouteResult
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Project != "api-service" {
		t.Fatalf("expected api-service, got %s", resp.Project)
	}
	if resp.Confidence <= 0 {
		t.Fatalf("expected positive confidence, got %f", resp.Confidence)
	}
}

func TestRouteEndpointNoMatch(t *testing.T) {
	srv := newTestServerWithHub(t)

	// Write projects.yaml with specific keywords.
	hubDir := filepath.Dir(srv.hubProjects.FilePath())
	yaml := `projects:
  - name: api-service
    path: /home/user/code/api
    keywords:
      - api
`
	if err := os.WriteFile(filepath.Join(hubDir, "projects.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write projects.yaml: %v", err)
	}

	body := `{"message":"Update kubernetes deployment config"}`
	req := httptest.NewRequest(http.MethodPost, "/api/route", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp routeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Project != "" {
		t.Fatalf("expected empty project for no match, got %s", resp.Project)
	}
}

func TestRouteEndpointEmptyMessage(t *testing.T) {
	srv := newTestServerWithHub(t)

	body := `{"message":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/route", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d: %s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
}

func TestRouteEndpointMethodNotAllowed(t *testing.T) {
	srv := newTestServerWithHub(t)

	req := httptest.NewRequest(http.MethodGet, "/api/route", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
}
```

**Step 2: Run new tests to verify they fail**

Run: `go test ./internal/web/... -count=1 -run "TestRouteEndpoint" -v`
Expected: FAIL — 404 (route not registered).

**Step 3: Add request/response types and handler**

Add to `internal/web/handlers_hub.go`:

```go
type routeRequest struct {
	Message string `json:"message"`
}

type routeResponse struct {
	Project         string   `json:"project"`
	Confidence      float64  `json:"confidence"`
	MatchedKeywords []string `json:"matchedKeywords"`
}

// handleRoute serves POST /api/route.
func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	var req routeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body")
		return
	}

	if req.Message == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "message is required")
		return
	}

	if s.hubProjects == nil {
		writeJSON(w, http.StatusOK, routeResponse{})
		return
	}

	projects, err := s.hubProjects.List()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load projects")
		return
	}

	result := hub.Route(req.Message, projects)
	if result == nil {
		writeJSON(w, http.StatusOK, routeResponse{})
		return
	}

	writeJSON(w, http.StatusOK, routeResponse{
		Project:         result.Project,
		Confidence:      result.Confidence,
		MatchedKeywords: result.MatchedKeywords,
	})
}
```

**Step 4: Register route in server.go**

In `internal/web/server.go`, add after the `/api/projects` line:

```go
mux.HandleFunc("/api/route", s.handleRoute)
```

**Step 5: Run all tests**

Run: `go test ./internal/web/... ./internal/hub/... -count=1 -v`
Expected: ALL PASS.

**Step 6: Commit**

```bash
git add internal/web/handlers_hub.go internal/web/handlers_hub_test.go internal/web/server.go
git commit -m "feat(hub): add POST /api/route endpoint for keyword-based project routing"
```

---

## Task 3: Frontend — auto-suggest project in New Task modal

**Files:**
- Modify: `internal/web/static/dashboard.js`
- Modify: `internal/web/static/dashboard.css`

**Step 1: Add auto-suggest indicator to CSS**

Append to `dashboard.css`:

```css
/* ── Route suggestion ──────────────────────────────────────────── */

.route-suggestion {
  font-size: 0.82rem;
  color: var(--accent);
  padding: 4px 0;
  min-height: 1.2em;
}

.route-suggestion-muted {
  color: var(--muted);
}
```

**Step 2: Add route suggestion element to HTML**

In `dashboard.html`, inside the modal body, add after the description textarea:

```html
<div class="route-suggestion" id="route-suggestion"></div>
```

**Step 3: Add auto-suggest JS**

Add DOM reference at top of `dashboard.js`:

```javascript
var routeSuggestion = document.getElementById("route-suggestion")
```

Add debounced route function (before event listeners section):

```javascript
// ── Auto-suggest project via routing ─────────────────────────────
var routeTimer = null

function suggestProject(message) {
  if (routeTimer) clearTimeout(routeTimer)
  if (!message || message.length < 5) {
    if (routeSuggestion) routeSuggestion.textContent = ""
    return
  }

  routeTimer = setTimeout(function () {
    var headers = authHeaders()
    headers["Content-Type"] = "application/json"

    fetch(apiPathWithToken("/api/route"), {
      method: "POST",
      headers: headers,
      body: JSON.stringify({ message: message }),
    })
      .then(function (r) {
        if (!r.ok) return null
        return r.json()
      })
      .then(function (data) {
        if (!data || !data.project) {
          if (routeSuggestion) {
            routeSuggestion.textContent = "No matching project"
            routeSuggestion.className = "route-suggestion route-suggestion-muted"
          }
          return
        }
        if (routeSuggestion) {
          routeSuggestion.textContent =
            "Suggested: " + data.project +
            " (" + Math.round(data.confidence * 100) + "% match)"
          routeSuggestion.className = "route-suggestion"
        }
        // Auto-select the suggested project in the dropdown.
        if (newTaskProject) {
          for (var i = 0; i < newTaskProject.options.length; i++) {
            if (newTaskProject.options[i].value === data.project) {
              newTaskProject.selectedIndex = i
              break
            }
          }
        }
      })
      .catch(function () {
        if (routeSuggestion) routeSuggestion.textContent = ""
      })
  }, 300)
}
```

Add event listener for description input:

```javascript
if (newTaskDesc) {
  newTaskDesc.addEventListener("input", function () {
    suggestProject(newTaskDesc.value.trim())
  })
}
```

**Step 4: Run Go tests**

Run: `go test ./internal/web/... -count=1 -v`
Expected: ALL PASS.

**Step 5: Commit**

```bash
git add internal/web/static/dashboard.js internal/web/static/dashboard.css internal/web/static/dashboard.html
git commit -m "feat(hub): add auto-suggest project routing in New Task modal"
```

---

## Summary of endpoints after Phase 3

| Endpoint | Method | Status |
|----------|--------|--------|
| `POST /api/route` | POST | **Phase 3** (new) |

Plus all Phase 1 and Phase 2 endpoints unchanged.
