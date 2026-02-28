# Message Presentation Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Align the web dashboard's Messages tab with YepAnywhere's message display patterns using server-side HTML rendering.

**Architecture:** The Go server parses Claude Code JSONL conversation files into structured content blocks (text, thinking, tool_use, tool_result), groups them into turns, and renders an HTML fragment via Go `html/template`. The client receives pre-rendered HTML and attaches minimal event delegation for interactive elements (tool expand/collapse, collapsible text).

**Tech Stack:** Go 1.24+, `html/template`, Chroma syntax highlighting (existing), `go-diff` (existing), vanilla JS with event delegation

---

### Task 1: Merge YepAnywhere Branch

**Files:**
- All files from `feature/YepAnywhere-Investigation` branch

**Step 1: Merge the branch**

Run: `git merge feature/YepAnywhere-Investigation --no-edit`

This brings in: DAG reader (`internal/dag/`), highlight package (`internal/highlight/`), eventbus (`internal/eventbus/`), augments (`internal/web/augments.go`), message handler (`internal/web/handlers_messages.go`), dashboard tabs/toolbar HTML/CSS/JS, connection manager, and tool renderers.

**Step 2: Verify the merge built cleanly**

Run: `make build`
Expected: Clean build with no errors

**Step 3: Run tests**

Run: `make test`
Expected: All tests pass

**Step 4: Commit if merge needed manual resolution**

Only if there were conflicts:
```bash
git add -A
git commit -m "chore: merge feature/YepAnywhere-Investigation into feature/Message-Presentation"
```

---

### Task 2: Content Block Parser

**Files:**
- Create: `internal/web/message_renderer.go`
- Test: `internal/web/message_renderer_test.go`

**Step 1: Write the failing test for content block parsing**

Create `internal/web/message_renderer_test.go`:

```go
package web

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseContentBlocks_TextOnly(t *testing.T) {
	msg := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"hello world"}]}`)
	blocks := parseContentBlocks(msg)
	require.Len(t, blocks, 1)
	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "hello world", blocks[0].Text)
}

func TestParseContentBlocks_StringContent(t *testing.T) {
	msg := json.RawMessage(`{"role":"user","content":"simple string"}`)
	blocks := parseContentBlocks(msg)
	require.Len(t, blocks, 1)
	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "simple string", blocks[0].Text)
}

func TestParseContentBlocks_ThinkingBlock(t *testing.T) {
	msg := json.RawMessage(`{"role":"assistant","content":[{"type":"thinking","thinking":"let me reason"},{"type":"text","text":"answer"}]}`)
	blocks := parseContentBlocks(msg)
	require.Len(t, blocks, 2)
	assert.Equal(t, "thinking", blocks[0].Type)
	assert.Equal(t, "let me reason", blocks[0].Text)
	assert.Equal(t, "text", blocks[1].Type)
	assert.Equal(t, "answer", blocks[1].Text)
}

func TestParseContentBlocks_ToolUse(t *testing.T) {
	msg := json.RawMessage(`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}`)
	blocks := parseContentBlocks(msg)
	require.Len(t, blocks, 1)
	assert.Equal(t, "tool_use", blocks[0].Type)
	assert.Equal(t, "Bash", blocks[0].ToolName)
	assert.Equal(t, "t1", blocks[0].ToolUseID)
	assert.JSONEq(t, `{"command":"ls"}`, string(blocks[0].ToolInput))
}

func TestParseContentBlocks_ToolResult(t *testing.T) {
	msg := json.RawMessage(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"file1.go\nfile2.go"}]}`)
	blocks := parseContentBlocks(msg)
	require.Len(t, blocks, 1)
	assert.Equal(t, "tool_result", blocks[0].Type)
	assert.Equal(t, "t1", blocks[0].ToolUseID)
	assert.Equal(t, "file1.go\nfile2.go", blocks[0].Text)
}

func TestParseContentBlocks_EmptyMessage(t *testing.T) {
	msg := json.RawMessage(`{}`)
	blocks := parseContentBlocks(msg)
	assert.Empty(t, blocks)
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race -v ./internal/web -run TestParseContentBlocks`
Expected: FAIL — `parseContentBlocks` undefined

**Step 3: Write minimal implementation**

Create `internal/web/message_renderer.go`:

```go
package web

import "encoding/json"

// contentBlock represents a single content block from a Claude Code message.
type contentBlock struct {
	Type       string          // text, thinking, tool_use, tool_result
	Text       string          // text content (for text, thinking, tool_result)
	ToolName   string          // tool name (for tool_use)
	ToolUseID  string          // tool_use id or tool_use_id reference
	ToolInput  json.RawMessage // raw input JSON (for tool_use)
}

// parseContentBlocks extracts structured content blocks from a Claude Code
// message JSON blob. Handles both string content and array-of-blocks content.
func parseContentBlocks(msg json.RawMessage) []contentBlock {
	if len(msg) == 0 {
		return nil
	}

	var parsed struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(msg, &parsed); err != nil || len(parsed.Content) == 0 {
		return nil
	}

	// Try string content first.
	var s string
	if err := json.Unmarshal(parsed.Content, &s); err == nil && s != "" {
		return []contentBlock{{Type: "text", Text: s}}
	}

	// Try array of content blocks.
	var rawBlocks []json.RawMessage
	if err := json.Unmarshal(parsed.Content, &rawBlocks); err != nil {
		return nil
	}

	var blocks []contentBlock
	for _, raw := range rawBlocks {
		var b struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Thinking  string          `json:"thinking"`
			Name      string          `json:"name"`
			ID        string          `json:"id"`
			ToolUseID string          `json:"tool_use_id"`
			Input     json.RawMessage `json:"input"`
			Content   json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			continue
		}

		switch b.Type {
		case "text":
			blocks = append(blocks, contentBlock{Type: "text", Text: b.Text})
		case "thinking":
			blocks = append(blocks, contentBlock{Type: "thinking", Text: b.Thinking})
		case "tool_use":
			blocks = append(blocks, contentBlock{
				Type:      "tool_use",
				ToolName:  b.Name,
				ToolUseID: b.ID,
				ToolInput: b.Input,
			})
		case "tool_result":
			text := ""
			if len(b.Content) > 0 {
				// Content can be a string or array of blocks.
				var cs string
				if json.Unmarshal(b.Content, &cs) == nil {
					text = cs
				}
			}
			blocks = append(blocks, contentBlock{
				Type:      "tool_result",
				ToolUseID: b.ToolUseID,
				Text:      text,
			})
		}
	}

	return blocks
}
```

**Step 4: Run test to verify it passes**

Run: `go test -race -v ./internal/web -run TestParseContentBlocks`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/web/message_renderer.go internal/web/message_renderer_test.go
git commit -m "feat(web): add content block parser for message rendering"
```

---

### Task 3: Turn Grouper

**Files:**
- Modify: `internal/web/message_renderer.go`
- Modify: `internal/web/message_renderer_test.go`

**Step 1: Write the failing test for turn grouping**

Add to `internal/web/message_renderer_test.go`:

```go
func TestGroupIntoTurns_Basic(t *testing.T) {
	msgs := []dagMessage{
		{Role: "user", Blocks: []contentBlock{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Blocks: []contentBlock{{Type: "thinking", Text: "hmm"}, {Type: "text", Text: "hi"}}},
	}
	turns := groupIntoTurns(msgs)
	require.Len(t, turns, 2)
	assert.Equal(t, "user", turns[0].Role)
	assert.Len(t, turns[0].Blocks, 1)
	assert.Equal(t, "assistant", turns[1].Role)
	assert.Len(t, turns[1].Blocks, 2)
}

func TestGroupIntoTurns_ConsecutiveAssistant(t *testing.T) {
	msgs := []dagMessage{
		{Role: "user", Blocks: []contentBlock{{Type: "text", Text: "do it"}}},
		{Role: "assistant", Blocks: []contentBlock{{Type: "tool_use", ToolName: "Bash"}}},
		{Role: "user", Blocks: []contentBlock{{Type: "tool_result", Text: "output"}}},
		{Role: "assistant", Blocks: []contentBlock{{Type: "text", Text: "done"}}},
	}
	turns := groupIntoTurns(msgs)
	require.Len(t, turns, 2)
	assert.Equal(t, "user", turns[0].Role)
	// All assistant + tool_result messages between user prompts = one assistant turn
	assert.Equal(t, "assistant", turns[1].Role)
	assert.Len(t, turns[1].Blocks, 3) // tool_use + tool_result + text
}

func TestGroupIntoTurns_MultipleUserPrompts(t *testing.T) {
	msgs := []dagMessage{
		{Role: "user", Blocks: []contentBlock{{Type: "text", Text: "first"}}},
		{Role: "assistant", Blocks: []contentBlock{{Type: "text", Text: "reply1"}}},
		{Role: "user", Blocks: []contentBlock{{Type: "text", Text: "second"}}},
		{Role: "assistant", Blocks: []contentBlock{{Type: "text", Text: "reply2"}}},
	}
	turns := groupIntoTurns(msgs)
	require.Len(t, turns, 4)
	assert.Equal(t, "user", turns[0].Role)
	assert.Equal(t, "assistant", turns[1].Role)
	assert.Equal(t, "user", turns[2].Role)
	assert.Equal(t, "assistant", turns[3].Role)
}

func TestGroupIntoTurns_Empty(t *testing.T) {
	turns := groupIntoTurns(nil)
	assert.Empty(t, turns)
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race -v ./internal/web -run TestGroupIntoTurns`
Expected: FAIL — `dagMessage` and `groupIntoTurns` undefined

**Step 3: Write minimal implementation**

Add to `internal/web/message_renderer.go`:

```go
import "time"

// dagMessage is a parsed message with its content blocks extracted.
type dagMessage struct {
	Role   string
	Blocks []contentBlock
	Time   time.Time
}

// renderedTurn represents a grouped conversation turn for template rendering.
type renderedTurn struct {
	Role   string
	Blocks []contentBlock
	Time   time.Time
}

// groupIntoTurns groups messages into conversation turns following
// YepAnywhere's pattern: user messages with text content are standalone turns;
// everything between user text prompts (assistant messages, tool results)
// forms a single assistant turn.
func groupIntoTurns(msgs []dagMessage) []renderedTurn {
	if len(msgs) == 0 {
		return nil
	}

	var turns []renderedTurn
	var currentAssistant *renderedTurn

	flushAssistant := func() {
		if currentAssistant != nil && len(currentAssistant.Blocks) > 0 {
			turns = append(turns, *currentAssistant)
			currentAssistant = nil
		}
	}

	for _, msg := range msgs {
		isUserPrompt := msg.Role == "user" && hasTextContent(msg.Blocks)

		if isUserPrompt {
			flushAssistant()
			turns = append(turns, renderedTurn{
				Role:   "user",
				Blocks: msg.Blocks,
				Time:   msg.Time,
			})
		} else {
			// Accumulate into assistant turn (includes tool_result messages
			// which have role=user but only contain tool_result blocks).
			if currentAssistant == nil {
				currentAssistant = &renderedTurn{
					Role: "assistant",
					Time: msg.Time,
				}
			}
			currentAssistant.Blocks = append(currentAssistant.Blocks, msg.Blocks...)
		}
	}

	flushAssistant()
	return turns
}

// hasTextContent returns true if any block is a text block with non-empty text.
func hasTextContent(blocks []contentBlock) bool {
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			return true
		}
	}
	return false
}
```

**Step 4: Run test to verify it passes**

Run: `go test -race -v ./internal/web -run TestGroupIntoTurns`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/web/message_renderer.go internal/web/message_renderer_test.go
git commit -m "feat(web): add turn grouper for message presentation"
```

---

### Task 4: Tool Result Pairing

**Files:**
- Modify: `internal/web/message_renderer.go`
- Modify: `internal/web/message_renderer_test.go`

**Step 1: Write the failing test**

Add to `internal/web/message_renderer_test.go`:

```go
func TestPairToolResults(t *testing.T) {
	blocks := []contentBlock{
		{Type: "tool_use", ToolName: "Bash", ToolUseID: "t1", ToolInput: json.RawMessage(`{"command":"ls"}`)},
		{Type: "tool_result", ToolUseID: "t1", Text: "file1.go"},
		{Type: "tool_use", ToolName: "Read", ToolUseID: "t2", ToolInput: json.RawMessage(`{"file_path":"main.go"}`)},
		{Type: "tool_result", ToolUseID: "t2", Text: "package main"},
	}
	paired := pairToolResults(blocks)
	require.Len(t, paired, 2)
	assert.Equal(t, "tool_use", paired[0].Type)
	assert.Equal(t, "Bash", paired[0].ToolName)
	assert.Equal(t, "file1.go", paired[0].ToolResultText)
	assert.Equal(t, "Read", paired[1].ToolName)
	assert.Equal(t, "package main", paired[1].ToolResultText)
}

func TestPairToolResults_UnpairedToolUse(t *testing.T) {
	blocks := []contentBlock{
		{Type: "text", Text: "hello"},
		{Type: "tool_use", ToolName: "Bash", ToolUseID: "t1", ToolInput: json.RawMessage(`{"command":"ls"}`)},
	}
	paired := pairToolResults(blocks)
	require.Len(t, paired, 2)
	assert.Equal(t, "text", paired[0].Type)
	assert.Equal(t, "tool_use", paired[1].Type)
	assert.Empty(t, paired[1].ToolResultText)
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race -v ./internal/web -run TestPairToolResults`
Expected: FAIL — `pairToolResults` undefined, `ToolResultText` field missing

**Step 3: Write minimal implementation**

Add `ToolResultText` field to `contentBlock` and implement `pairToolResults`:

```go
// Add to contentBlock struct:
// ToolResultText string // paired tool_result text (populated by pairToolResults)

// pairToolResults matches tool_result blocks with their tool_use blocks by
// ToolUseID, merging the result text into the tool_use block and removing
// the standalone tool_result. Non-tool blocks pass through unchanged.
func pairToolResults(blocks []contentBlock) []contentBlock {
	// Index tool_result blocks by their ToolUseID.
	resultMap := make(map[string]string)
	for _, b := range blocks {
		if b.Type == "tool_result" && b.ToolUseID != "" {
			resultMap[b.ToolUseID] = b.Text
		}
	}

	var out []contentBlock
	for _, b := range blocks {
		if b.Type == "tool_result" {
			continue // consumed by pairing
		}
		if b.Type == "tool_use" && b.ToolUseID != "" {
			if text, ok := resultMap[b.ToolUseID]; ok {
				b.ToolResultText = text
			}
		}
		out = append(out, b)
	}
	return out
}
```

**Step 4: Run test to verify it passes**

Run: `go test -race -v ./internal/web -run TestPairToolResults`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/web/message_renderer.go internal/web/message_renderer_test.go
git commit -m "feat(web): add tool result pairing for message renderer"
```

---

### Task 5: HTML Template Renderer

**Files:**
- Modify: `internal/web/message_renderer.go`
- Modify: `internal/web/message_renderer_test.go`

**Step 1: Write the failing test**

Add to `internal/web/message_renderer_test.go`:

```go
func TestRenderMessagesHTML_UserBubble(t *testing.T) {
	turns := []renderedTurn{
		{Role: "user", Blocks: []contentBlock{{Type: "text", Text: "hello world"}}},
	}
	html, err := renderMessagesHTML(turns)
	require.NoError(t, err)
	assert.Contains(t, html, `class="user-prompt-container"`)
	assert.Contains(t, html, `class="message message-user-prompt"`)
	assert.Contains(t, html, "hello world")
}

func TestRenderMessagesHTML_AssistantTurn(t *testing.T) {
	turns := []renderedTurn{
		{Role: "assistant", Blocks: []contentBlock{{Type: "text", Text: "hi there"}}},
	}
	html, err := renderMessagesHTML(turns)
	require.NoError(t, err)
	assert.Contains(t, html, `class="assistant-turn"`)
	assert.Contains(t, html, "hi there")
	assert.NotContains(t, html, "message-user-prompt")
}

func TestRenderMessagesHTML_ThinkingBlock(t *testing.T) {
	turns := []renderedTurn{
		{Role: "assistant", Blocks: []contentBlock{
			{Type: "thinking", Text: "let me think about this"},
			{Type: "text", Text: "the answer"},
		}},
	}
	html, err := renderMessagesHTML(turns)
	require.NoError(t, err)
	assert.Contains(t, html, `class="thinking-block collapsible"`)
	assert.Contains(t, html, "let me think about this")
	assert.Contains(t, html, "the answer")
}

func TestRenderMessagesHTML_ToolBlock(t *testing.T) {
	turns := []renderedTurn{
		{Role: "assistant", Blocks: []contentBlock{
			{Type: "tool_use", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls -la"}`), ToolResultText: "file1.go"},
		}},
	}
	html, err := renderMessagesHTML(turns)
	require.NoError(t, err)
	assert.Contains(t, html, `class="tool-block"`)
	assert.Contains(t, html, `class="tool-header"`)
	assert.Contains(t, html, "ls -la")
	assert.Contains(t, html, "file1.go")
}

func TestRenderMessagesHTML_EscapesHTML(t *testing.T) {
	turns := []renderedTurn{
		{Role: "user", Blocks: []contentBlock{{Type: "text", Text: "<script>alert('xss')</script>"}}},
	}
	html, err := renderMessagesHTML(turns)
	require.NoError(t, err)
	assert.NotContains(t, html, "<script>")
	assert.Contains(t, html, "&lt;script&gt;")
}

func TestRenderMessagesHTML_Empty(t *testing.T) {
	html, err := renderMessagesHTML(nil)
	require.NoError(t, err)
	assert.Contains(t, html, "No messages yet")
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race -v ./internal/web -run TestRenderMessagesHTML`
Expected: FAIL — `renderMessagesHTML` undefined

**Step 3: Write minimal implementation**

Add to `internal/web/message_renderer.go`:

```go
import (
	"bytes"
	"html/template"
	"strings"
)

// toolInputSummary extracts a short summary from tool input JSON for display
// in the tool header.
func toolInputSummary(name string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
	switch name {
	case "Bash":
		if cmd, ok := m["command"].(string); ok {
			return cmd
		}
	case "Read", "Write", "Edit":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
	case "Glob":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
	case "Grep":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
	}
	return ""
}

// toolIcon returns the display icon for a tool name.
func toolIcon(name string) string {
	switch name {
	case "Bash":
		return "$"
	case "Read":
		return "\U0001F4C4" // file emoji
	case "Write":
		return "\u270D"     // writing hand
	case "Edit":
		return "\u270E"     // pencil
	case "Glob":
		return "\U0001F50D" // magnifying glass
	case "Grep":
		return "\U0001F50E" // magnifying glass right
	default:
		return "\u2699"     // gear
	}
}

var messagesTemplate = template.Must(template.New("messages").Funcs(template.FuncMap{
	"toolInputSummary": toolInputSummary,
	"toolIcon":         toolIcon,
	"truncateLines": func(s string, maxLines int) string {
		lines := strings.Split(s, "\n")
		if len(lines) <= maxLines {
			return s
		}
		return strings.Join(lines[:maxLines], "\n")
	},
	"lineCount": func(s string) int {
		if s == "" {
			return 0
		}
		return len(strings.Split(s, "\n"))
	},
	"needsTruncation": func(s string) bool {
		lines := strings.Split(s, "\n")
		return len(lines) > 12 || len(s) > 1200
	},
}).Parse(`{{if not .}}<div class="messages-empty">No messages yet.</div>
{{else}}{{range .}}{{if eq .Role "user"}}` +
	`<div class="user-prompt-container">` +
	`<div class="message message-user-prompt">` +
	`<div class="message-content">` +
	`{{range .Blocks}}{{if eq .Type "text"}}` +
	`{{if needsTruncation .Text}}<div class="text-block collapsible-text"><div class="truncated-content">{{truncateLines .Text 12}}<div class="fade-overlay"></div></div><button class="show-more-btn" type="button">Show more</button></div>` +
	`{{else}}<div class="text-block">{{.Text}}</div>{{end}}` +
	`{{end}}{{end}}` +
	`</div></div></div>` +

	`{{else}}` +

	`<div class="assistant-turn">` +
	`{{range .Blocks}}` +
	`{{if eq .Type "thinking"}}` +
	`<details class="thinking-block collapsible">` +
	`<summary class="collapsible__summary"><span class="collapsible__icon">&#x25B8;</span> Thinking</summary>` +
	`<div class="thinking-content">{{.Text}}</div>` +
	`</details>` +
	`{{else if eq .Type "text"}}` +
	`<div class="text-block">{{.Text}}</div>` +
	`{{else if eq .Type "tool_use"}}` +
	`<div class="tool-block">` +
	`<div class="tool-header">` +
	`<span class="tool-icon">{{toolIcon .ToolName}}</span>` +
	`<span class="tool-command">{{toolInputSummary .ToolName .ToolInput}}</span>` +
	`</div>` +
	`<div class="tool-body tool-collapsed">` +
	`{{if .ToolResultText}}<pre>{{.ToolResultText}}</pre>{{end}}` +
	`</div>` +
	`</div>` +
	`{{end}}` +
	`{{end}}` +
	`</div>` +

	`{{end}}{{end}}{{end}}`))

// renderMessagesHTML renders conversation turns as an HTML fragment.
func renderMessagesHTML(turns []renderedTurn) (string, error) {
	var buf bytes.Buffer
	if err := messagesTemplate.Execute(&buf, turns); err != nil {
		return "", err
	}
	return buf.String(), nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test -race -v ./internal/web -run TestRenderMessagesHTML`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/web/message_renderer.go internal/web/message_renderer_test.go
git commit -m "feat(web): add HTML template renderer for messages"
```

---

### Task 6: HTML Endpoint Handler

**Files:**
- Modify: `internal/web/handlers_messages.go`
- Modify: `internal/web/server.go`

**Step 1: Write the failing test**

Add to `internal/web/message_renderer_test.go`:

```go
func TestHandleMessagesHTML_Integration(t *testing.T) {
	claudeDir := t.TempDir()
	projectPath := "/home/testuser/myproject"

	entries := []map[string]any{
		{
			"uuid": "a", "parentUuid": "", "type": "human",
			"message":   map[string]any{"role": "user", "content": "hello"},
			"timestamp": "2025-01-01T00:00:00Z",
		},
		{
			"uuid": "b", "parentUuid": "a", "type": "assistant",
			"message": map[string]any{"role": "assistant", "content": []map[string]any{
				{"type": "text", "text": "hi there"},
			}},
			"timestamp": "2025-01-01T00:00:01Z",
		},
	}
	writeTestJSONL(t, claudeDir, projectPath, entries)

	srv := newServerWithMessages(t, "sess-1", projectPath, claudeDir)
	req := httptest.NewRequest("GET", "/api/messages/sess-1/html", nil)
	rec := httptest.NewRecorder()
	srv.handleSessionMessagesHTML(rec, req)

	require.Equal(t, 200, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	body := rec.Body.String()
	assert.Contains(t, body, "user-prompt-container")
	assert.Contains(t, body, "hello")
	assert.Contains(t, body, "assistant-turn")
	assert.Contains(t, body, "hi there")
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race -v ./internal/web -run TestHandleMessagesHTML`
Expected: FAIL — `handleSessionMessagesHTML` undefined

**Step 3: Write the handler**

Add to `internal/web/handlers_messages.go`:

```go
// handleSessionMessagesHTML serves GET /api/messages/{sessionID}/html.
// Returns a pre-rendered HTML fragment of the conversation.
func (s *Server) handleSessionMessagesHTML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	// Extract session ID: /api/messages/{sessionID}/html
	path := r.URL.Path
	const prefix = "/api/messages/"
	const suffix = "/html"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "route not found")
		return
	}
	sessionID := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "session id is required")
		return
	}

	// Reuse the session lookup logic from the JSON handler.
	sessionDir, err := s.resolveSessionDir(r, sessionID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	if sessionDir == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<div class="messages-empty">No messages yet.</div>`))
		return
	}

	result, err := dag.ReadSessionFull(sessionDir)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read conversation")
		return
	}

	if result == nil || len(result.Messages) == 0 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<div class="messages-empty">No messages yet.</div>`))
		return
	}

	// Parse messages into dagMessages with content blocks.
	var dagMsgs []dagMessage
	for _, m := range result.Messages {
		blocks := parseContentBlocks(m.Message)
		dagMsgs = append(dagMsgs, dagMessage{
			Role:   m.Role,
			Blocks: blocks,
			Time:   m.Timestamp,
		})
	}

	// Group into turns and pair tool results.
	turns := groupIntoTurns(dagMsgs)
	for i := range turns {
		turns[i].Blocks = pairToolResults(turns[i].Blocks)
	}

	html, err := renderMessagesHTML(turns)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to render messages")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}
```

Also refactor the session directory lookup from `handleSessionMessages` into a shared `resolveSessionDir` method to avoid duplication:

```go
// resolveSessionDir finds the Claude Code session directory for the given
// session ID. Returns empty string if no conversation data exists, or error
// if the session is not found at all.
func (s *Server) resolveSessionDir(r *http.Request, sessionID string) (string, error) {
	snapshot, err := s.menuData.LoadMenuSnapshot()
	if err != nil {
		return "", fmt.Errorf("failed to load session data")
	}

	var projectPath, tmuxSession string
	found := false
	for _, item := range snapshot.Items {
		if item.Type != MenuItemTypeSession || item.Session == nil {
			continue
		}
		if item.Session.ID != sessionID {
			continue
		}
		projectPath = item.Session.ProjectPath
		tmuxSession = item.Session.TmuxSession
		found = true
		break
	}

	if !found {
		return "", fmt.Errorf("session not found")
	}
	if projectPath == "" {
		return "", fmt.Errorf("session has no project path")
	}

	sessionDir := s.findClaudeSessionDir(projectPath)
	if sessionDir == "" && tmuxSession != "" {
		if actualPath := tmuxPaneCurrentPath(r.Context(), tmuxSession); actualPath != "" && actualPath != projectPath {
			sessionDir = s.findClaudeSessionDir(actualPath)
		}
	}

	return sessionDir, nil
}
```

Register the route in `server.go` right after the existing messages route:

```go
mux.HandleFunc("/api/messages/", s.handleSessionMessages) // existing
// Add a check at the top of handleSessionMessages to dispatch to HTML handler:
```

Since Go's `http.ServeMux` doesn't support nested routing well, the cleanest approach is to have `handleSessionMessages` detect the `/html` suffix and dispatch:

In `handlers_messages.go`, modify `handleSessionMessages` to check for `/html` suffix:

```go
func (s *Server) handleSessionMessages(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/html") {
		s.handleSessionMessagesHTML(w, r)
		return
	}
	// ... existing JSON handler code ...
}
```

**Step 4: Run test to verify it passes**

Run: `go test -race -v ./internal/web -run TestHandleMessagesHTML`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/web/handlers_messages.go internal/web/message_renderer.go internal/web/message_renderer_test.go
git commit -m "feat(web): add /api/messages/{id}/html endpoint for server-rendered messages"
```

---

### Task 7: YepAnywhere-Aligned CSS

**Files:**
- Modify: `internal/web/static/styles.css` — Add `--bg-user-message` and `--thinking-color` variables
- Modify: `internal/web/static/dashboard.css` — Replace message block styles

**Step 1: Add CSS variables**

Add to `:root` in `internal/web/static/styles.css`:

```css
--bg-user-message: #484848;
--thinking-color: #d97706;
```

**Step 2: Replace message CSS**

In `internal/web/static/dashboard.css`, replace the existing `.message-block`, `.message-block--user`, `.message-block--assistant`, `.message-role`, `.message-content` rules (lines ~1613-1646) with:

```css
/* ── User prompt bubble (YepAnywhere-aligned) ─────────────────── */

.user-prompt-container {
  margin-bottom: 16px;
}

.message-user-prompt {
  background: var(--bg-user-message);
  border-radius: 6px;
  width: fit-content;
  max-width: 80%;
  padding: 0.25rem 0.5rem;
  font-size: 0.88rem;
  line-height: 1.5;
  color: var(--text);
}

.message-content {
  font-size: 0.88rem;
  color: var(--text);
  line-height: 1.5;
}

/* ── Assistant turn (YepAnywhere-aligned) ─────────────────────── */

.assistant-turn {
  position: relative;
  padding-left: 24px;
  margin-bottom: 16px;
}

/* ── Text blocks ──────────────────────────────────────────────── */

.text-block {
  font-size: 0.88rem;
  color: var(--text);
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
  margin-bottom: 4px;
}

/* ── Collapsible text ─────────────────────────────────────────── */

.collapsible-text .truncated-content {
  position: relative;
  overflow: hidden;
}

.collapsible-text .fade-overlay {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  height: 40px;
  background: linear-gradient(transparent, var(--bg-user-message));
  pointer-events: none;
}

.assistant-turn .collapsible-text .fade-overlay {
  background: linear-gradient(transparent, var(--bg));
}

.show-more-btn {
  display: inline-block;
  border: none;
  background: transparent;
  color: var(--accent);
  font-size: 0.78rem;
  cursor: pointer;
  padding: 4px 0;
  margin-top: 2px;
}

.show-more-btn:hover {
  text-decoration: underline;
}

/* ── Thinking blocks ──────────────────────────────────────────── */

.thinking-block.collapsible {
  margin: 4px 0;
}

.collapsible__summary {
  display: flex;
  align-items: center;
  gap: 4px;
  color: var(--muted);
  font-size: 0.82rem;
  cursor: pointer;
  user-select: none;
  list-style: none;
}

.collapsible__summary::-webkit-details-marker {
  display: none;
}

.collapsible__icon {
  display: inline-block;
  transition: transform 150ms ease;
  font-size: 0.7rem;
}

details[open] > .collapsible__summary .collapsible__icon {
  transform: rotate(90deg);
}

.thinking-content {
  color: var(--thinking-color);
  font-size: 0.82rem;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
  padding: 4px 0 4px 16px;
  border-left: 2px solid var(--thinking-color);
  margin: 4px 0;
  opacity: 0.85;
}

/* ── Messages empty state ─────────────────────────────────────── */

.messages-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--text-dim);
  font-size: 0.88rem;
}
```

**Step 3: Rebuild and verify visually**

Run: `make build`
Expected: Clean build

Run: `./build/agent-deck web --headless` and open the Messages tab in the browser to verify styling.

**Step 4: Commit**

```bash
git add internal/web/static/styles.css internal/web/static/dashboard.css
git commit -m "feat(web): add YepAnywhere-aligned CSS for message presentation"
```

---

### Task 8: Client-Side Integration

**Files:**
- Modify: `internal/web/static/dashboard.js`

**Step 1: Replace `loadSessionMessages` to fetch HTML**

Find the existing `loadSessionMessages` function and replace it:

```js
function loadSessionMessages(sessionId) {
  if (!sessionId) return
  state.lastLoadedMessagesSession = sessionId

  var url = apiPathWithToken("/api/messages/" + encodeURIComponent(sessionId) + "/html")
  fetch(url, { headers: authHeaders() })
    .then(function (r) {
      if (!r.ok) throw new Error("messages fetch failed: " + r.status)
      return r.text()
    })
    .then(function (html) {
      var container = document.getElementById("messages-container")
      if (!container) return
      var wasAtBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 50
      container.textContent = ""
      var wrapper = document.createElement("div")
      wrapper.textContent = ""
      // Server-rendered HTML is from our own authenticated endpoint,
      // pre-sanitized by Go html/template. Parse and adopt nodes safely.
      var parser = new DOMParser()
      var doc = parser.parseFromString(html, "text/html")
      while (doc.body.firstChild) {
        container.appendChild(doc.body.firstChild)
      }
      initMessageInteractions(container)
      if (wasAtBottom || !state.messagesScrolledByUser) {
        container.scrollTop = container.scrollHeight
      }
    })
    .catch(function (err) {
      console.error("loadSessionMessages:", err)
      var container = document.getElementById("messages-container")
      if (container) {
        clearChildren(container)
        container.appendChild(el("div", "messages-empty", "Failed to load messages."))
      }
    })
}
```

**Step 2: Add `initMessageInteractions` for event delegation**

```js
function initMessageInteractions(container) {
  // Tool header click → toggle body visibility (event delegation)
  container.addEventListener("click", function (e) {
    var header = e.target.closest(".tool-header")
    if (header) {
      var body = header.nextElementSibling
      if (body && body.classList.contains("tool-body")) {
        body.classList.toggle("tool-collapsed")
      }
      return
    }

    // Show more/less button
    var btn = e.target.closest(".show-more-btn")
    if (btn) {
      var textBlock = btn.closest(".collapsible-text")
      if (textBlock) {
        var truncated = textBlock.querySelector(".truncated-content")
        if (truncated) {
          var isExpanded = !truncated.style.maxHeight || truncated.style.maxHeight === "none"
          if (isExpanded) {
            truncated.style.maxHeight = "none"
            truncated.style.overflow = "visible"
            var overlay = truncated.querySelector(".fade-overlay")
            if (overlay) overlay.style.display = "none"
            btn.textContent = "Show less"
          } else {
            truncated.style.maxHeight = ""
            truncated.style.overflow = ""
            var overlay2 = truncated.querySelector(".fade-overlay")
            if (overlay2) overlay2.style.display = ""
            btn.textContent = "Show more"
          }
        }
      }
    }
  })
}
```

**Step 3: Remove `renderMessages` and `ToolRenderers`**

Delete the following from `dashboard.js`:
- The `renderMessages(messages)` function
- The entire `ToolRenderers` object and all `.register()` calls (Bash, Edit, Read renderers)
- The `escapeHtml(s)` helper function (only used by ToolRenderers)

**Step 4: Add polling for active sessions**

After `sendChatMessage` successfully sends input, add a delayed reload:

```js
// Already exists in sendTaskInput success handler — update to use new endpoint:
state.lastLoadedMessagesSession = null
setTimeout(function () { loadSessionMessages(msgSessionId) }, 1500)
setTimeout(function () { loadSessionMessages(msgSessionId) }, 5000)
```

**Step 5: Rebuild and test**

Run: `make build`
Expected: Clean build

**Step 6: Commit**

```bash
git add internal/web/static/dashboard.js
git commit -m "feat(web): replace client-side message rendering with server-rendered HTML"
```

---

### Task 9: End-to-End Verification

**Files:**
- Modify: `internal/web/messages_e2e_test.go` (update to test HTML endpoint)

**Step 1: Add HTML endpoint E2E tests**

Add a test case for the HTML endpoint to the existing E2E test file:

```go
func TestMessagesE2E_HTMLRendering(t *testing.T) {
	claudeDir := t.TempDir()
	projectPath := "/home/testuser/myproject"

	entries := []map[string]any{
		{
			"uuid": "a", "parentUuid": "", "type": "human",
			"message":   map[string]any{"role": "user", "content": "write tests"},
			"timestamp": "2025-01-01T00:00:00Z",
		},
		{
			"uuid": "b", "parentUuid": "a", "type": "assistant",
			"message": map[string]any{"role": "assistant", "content": []map[string]any{
				{"type": "thinking", "thinking": "I need to write tests"},
				{"type": "text", "text": "Here are the tests"},
				{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "go test ./..."}},
			}},
			"timestamp": "2025-01-01T00:00:01Z",
		},
		{
			"uuid": "c", "parentUuid": "b", "type": "human",
			"message": map[string]any{"role": "user", "content": []map[string]any{
				{"type": "tool_result", "tool_use_id": "t1", "content": "PASS"},
			}},
			"timestamp": "2025-01-01T00:00:02Z",
		},
	}
	writeTestJSONL(t, claudeDir, projectPath, entries)

	srv := newServerWithMessages(t, "sess-html", projectPath, claudeDir)
	req := httptest.NewRequest("GET", "/api/messages/sess-html/html", nil)
	rec := httptest.NewRecorder()
	srv.handleSessionMessages(rec, req)

	require.Equal(t, 200, rec.Code)
	body := rec.Body.String()

	// User prompt renders as bubble
	assert.Contains(t, body, "user-prompt-container")
	assert.Contains(t, body, "write tests")

	// Assistant turn with thinking, text, and tool
	assert.Contains(t, body, "assistant-turn")
	assert.Contains(t, body, "thinking-block")
	assert.Contains(t, body, "I need to write tests")
	assert.Contains(t, body, "Here are the tests")
	assert.Contains(t, body, "tool-block")
	assert.Contains(t, body, "go test ./...")

	// Tool result paired with tool use
	assert.Contains(t, body, "PASS")
}
```

**Step 2: Run all tests**

Run: `make test`
Expected: All tests pass

**Step 3: Build and manual verification**

Run: `make build`
Run: `./build/agent-deck web --headless`
Open browser to `http://127.0.0.1:8420`, select a session, click Messages tab.

Verify:
- User messages appear as compact left-aligned bubbles with gray background
- Assistant turns are indented 24px with no background
- Thinking blocks are collapsible with amber accent
- Tool cards have headers with icons and are expandable
- Long text is truncated with fade overlay and "Show more"
- Scrolling works and auto-scrolls to bottom

**Step 4: Final commit**

```bash
git add internal/web/messages_e2e_test.go
git commit -m "test(web): add E2E tests for HTML message rendering"
```

---

### Task 10: Cleanup and Final Review

**Step 1: Run full CI check**

Run: `make ci` (or `make lint && make test && make build`)
Expected: All pass

**Step 2: Review the diff**

Run: `git diff main...HEAD --stat`

Verify the changes are scoped to:
- `internal/web/message_renderer.go` (new)
- `internal/web/message_renderer_test.go` (new)
- `internal/web/handlers_messages.go` (modified)
- `internal/web/static/dashboard.css` (modified)
- `internal/web/static/dashboard.js` (modified)
- `internal/web/static/styles.css` (modified)
- Plus all merged files from yep branch
- Plus design/plan docs

**Step 3: Commit any final fixes**

```bash
git add -A
git commit -m "chore: final cleanup for message presentation feature"
```
