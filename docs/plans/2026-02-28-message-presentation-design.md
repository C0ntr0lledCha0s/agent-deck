# Message Presentation Design — YepAnywhere Alignment

**Date**: 2026-02-28
**Branch**: `feature/Message-Presentation`
**Base**: Merge `feature/YepAnywhere-Investigation` into this branch first

## Goal

Align the web dashboard's Messages tab with YepAnywhere's message presentation patterns using a template-driven (server-side rendered) approach.

## Background

YepAnywhere is a self-hosted web UI for supervising Claude Code agents. Its message presentation uses two distinct visual tracks:
- **User messages**: compact left-aligned bubbles with background fill, 6px border-radius, `fit-content` width (max 80%)
- **Assistant turns**: no bubble background, 24px left indent, grouped content blocks (text, thinking, tool calls)

The `feature/YepAnywhere-Investigation` branch already has:
- Messages tab with Terminal/Messages tab switching
- `/api/messages/{sessionID}` endpoint reading Claude Code's JSONL conversation files
- DAG reader (`internal/dag/`) for conversation tree traversal
- Server-side augments (`internal/web/augments.go`) for diffs, syntax highlighting, bash formatting
- Client-side `ToolRenderers` and `renderMessages()` — these get replaced

## Approach: Template-Driven Server-Side Rendering

The Go server renders the entire conversation as an HTML fragment. The client receives the pre-rendered HTML and uses safe DOM insertion methods with appropriate sanitization. Minimal event listeners handle interactivity.

### Why Server-Side

- Go's `html/template` handles XSS escaping automatically
- Syntax highlighting (Chroma) and diff computation already run server-side
- Client JS becomes trivial — no message parsing, grouping, or rendering logic
- Matches YepAnywhere's own pattern of serving pre-rendered content

## Content Block Parsing

The current `extractRoleContent` in `dag/reader.go` flattens content blocks into a single text string. A new parser extracts the full block structure from Claude Code's message format:

```go
type contentBlock struct {
    Type       string          // text, thinking, tool_use, tool_result
    Text       string          // for text/thinking blocks
    ToolName   string          // for tool_use
    ToolInput  json.RawMessage // for tool_use
    ToolResult json.RawMessage // paired tool_result content
    Augment    interface{}     // computed diff/highlight/bash augment
}

type renderedTurn struct {
    Role   string         // "user" or "assistant"
    Blocks []contentBlock
    Time   time.Time
}
```

Content block types from Claude Code JSONL:
- `text` — `{type: "text", text: "..."}`
- `thinking` — `{type: "thinking", thinking: "..."}`
- `tool_use` — `{type: "tool_use", id: "...", name: "Bash", input: {...}}`
- `tool_result` — `{type: "tool_result", tool_use_id: "...", content: "..."}`

The parser maps `tool_result` blocks back to their `tool_use` by `tool_use_id`, creating paired entries for the template.

## Turn Grouping

Messages are grouped into turns following YepAnywhere's pattern:
- Each user message is a standalone turn
- All consecutive assistant messages (and their tool results) form a single assistant turn
- This matches `groupItemsIntoTurns()` in YepAnywhere's `MessageList.tsx`

## HTML Template Structure

### User Turn
```html
<div class="user-prompt-container">
  <div class="message message-user-prompt">
    <div class="message-content">
      <div class="text-block">User's message text</div>
    </div>
  </div>
</div>
```

### Assistant Turn
```html
<div class="assistant-turn">
  <!-- Thinking: collapsible via native <details> -->
  <details class="thinking-block collapsible">
    <summary class="collapsible__summary">
      <span class="collapsible__icon">&#x25B8;</span> Thinking
    </summary>
    <div class="thinking-content">reasoning text</div>
  </details>

  <!-- Text -->
  <div class="text-block">Assistant response text</div>

  <!-- Tool card: expandable -->
  <div class="tool-block">
    <div class="tool-header">
      <span class="tool-icon">$</span>
      <span class="tool-command">make build</span>
      <span class="tool-badge">exit 0</span>
    </div>
    <div class="tool-body tool-collapsed">
      <pre>server-rendered output</pre>
    </div>
  </div>
</div>
```

## CSS (YepAnywhere-aligned)

| Element | Key Properties |
|---------|---------------|
| `.message-user-prompt` | `background: var(--bg-user-message)`, `border-radius: 6px`, `width: fit-content`, `max-width: 80%`, `padding: 0.25rem 0.5rem`, left-aligned |
| `.assistant-turn` | `position: relative`, `padding-left: 24px`, no background |
| `.thinking-block` | Collapsible `<details>`, `color: var(--text-muted)`, icon rotates on open |
| `.thinking-content` | `color: var(--thinking-color)` (`#d97706` amber) |
| `.tool-block` | `border: 1px solid var(--border)`, `border-radius: 8px`, margin `0.5rem 0` |
| `.tool-body.tool-collapsed` | `display: none` |
| Collapsible text | 12-line / 1200-char truncation with CSS fade overlay + "Show more" button |
| `--bg-user-message` | `#484848` (dark theme) |

## Client-Side Interactivity

Minimal JS after receiving server-rendered HTML:

1. **Tool expand/collapse**: Event delegation — click `.tool-header` toggles `.tool-collapsed` on `.tool-body`
2. **Thinking**: Native `<details>` element, zero JS. Icon rotation via CSS `details[open]`
3. **Collapsible text**: "Show more" click removes `.truncated` class, swaps button text
4. **Auto-scroll**: Scroll to bottom after content update; subsequent updates only auto-scroll if user was at bottom (50px threshold)

## API Endpoint

```
GET /api/messages/{sessionID}/html
```

Returns: `text/html` fragment. The server pre-renders using Go `html/template` which auto-escapes all user content. The client uses safe DOM insertion to place the content in `.messages-container`.

The existing JSON endpoint `/api/messages/{sessionID}` remains for potential API consumers but is no longer used by the dashboard.

## Real-Time Updates

Start with polling: re-fetch HTML every 3-5 seconds while agent is active. The HTML is pre-rendered so it's cheap. WebSocket push via the existing `ConnectionManager` can be added later as an optimization.

## Security

- **XSS**: Go `html/template` auto-escapes all interpolated values. Only pre-sanitized augment HTML (from `escapeHTML()`) is cast to `template.HTML`.
- **Path traversal**: Existing `sessionID` validation (no `/` characters) and `encodeProjectPath()` remain.
- **DoS**: Limit rendered messages to most recent 200. Truncate tool output server-side.
- **Auth**: Existing `s.authorizeRequest(r)` gates the endpoint.
- **Client-side HTML insertion**: Server-rendered HTML is trusted (same-origin, authenticated endpoint, server-side escaped). Use appropriate safe insertion patterns.

## File Changes

### From yep branch merge (no modifications)
- `internal/dag/` — DAG reader and builder
- `internal/highlight/` — Chroma syntax highlighting
- `internal/eventbus/` — WebSocket connection manager
- `internal/web/augments.go` — Server-side diff/highlight/bash computation
- Dashboard HTML (tabs, toolbar), CSS, JS (connection manager, tab switching)

### New files
- `internal/web/message_renderer.go` — Content block parser, turn grouper, HTML template
- `internal/web/message_renderer_test.go` — Tests

### Modified files
- `internal/web/handlers_messages.go` — Add `/html` variant
- `internal/web/server.go` — Register route
- `internal/web/static/dashboard.css` — Replace message styles with YepAnywhere-aligned CSS
- `internal/web/static/dashboard.js` — Replace `renderMessages()` with server-rendered content injection + `initMessageInteractions()`; remove `ToolRenderers`

### Removed client-side code
- `ToolRenderers` object and all tool render functions
- `renderMessages()` function
