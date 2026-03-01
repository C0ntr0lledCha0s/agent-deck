# Design: Message Pane Enhancements

**Date:** 2026-03-01
**Branch:** feature/Message-Presentation

## Context

The messages pane now has a YepAnywhere-aligned timeline system. This design covers five additional enhancements: syntax-highlighted code blocks, copy-to-clipboard, tool output highlighting with diffs, a thinking block truncation fix, and collapsed middle blocks for long assistant turns.

## Existing Infrastructure

- `internal/highlight/` — Chroma-based syntax highlighter with CSS class output and dark/light theme variables
- `internal/web/augments.go` — Server-side diff computation (`computeEditAugment`) and syntax highlighting (`computeReadAugment`) for terminal augments
- `internal/web/message_renderer.go` — Go template rendering messages as HTML, uses goldmark for markdown
- Goldmark renders fenced code blocks as `<pre><code class="language-X">...</code></pre>` (no highlighting)

## Feature 1: Syntax-Highlighted Markdown Code Blocks

**Approach:** Custom goldmark renderer extension that intercepts fenced code blocks and runs content through `highlight.Code()`.

**Changes:**
- New file `internal/web/goldmark_highlight.go` — implements `goldmark.Extender` that replaces the default fenced code block renderer
- Extracts language from the code fence info string (e.g. ` ```go `)
- Calls `highlight.Code(source, language)` to produce `<pre class="chroma">...</pre>`
- Falls back to plain `<pre><code>` for unknown/missing languages
- Register extension in `mdRenderer` initialization in `message_renderer.go`
- Inject `highlight.CSSVariables()` into the dashboard HTML (via `styles.css` or a `<style>` block in the template)

## Feature 2: Syntax-Highlighted Tool Output + Diffs

**Approach:** Enhance tool result rendering to produce highlighted HTML instead of raw text.

**Changes to `message_renderer.go`:**
- Add `ToolResultHTML template.HTML` field to `contentBlock` struct
- In `pairToolResults`, after matching tool_use with tool_result:
  - For **Read/Write** tools: extract `file_path` from `ToolInput`, call `highlight.Code(resultText, highlight.DetectLanguage(filePath))`
  - For **Edit** tools: extract `old_string`/`new_string` from `ToolInput`, call `computeEditAugment(old, new, filePath)` to produce diff HTML
  - For **Bash/Grep/Glob** and others: HTML-escape and use as-is
- Store result as `ToolResultHTML` (pre-rendered HTML)
- Update template to use `{{.ToolResultHTML}}` (unescaped) instead of `{{.ToolResultText}}` in `<pre>` blocks
- Add a `tool-output-highlighted` CSS class when content is highlighted, to avoid double-styling

**CSS additions:**
- Include Chroma CSS variables from `highlight.CSSVariables()` in dashboard
- `.tool-output .chroma` inherits `font-family: var(--font-mono)`
- `.tool-output .diff-add` and `.tool-output .diff-del` already styled in dashboard.css

## Feature 3: Copy Button on Code Blocks

**Approach:** CSS hover-reveal button + JS clipboard handler.

**HTML:** Add a wrapper `<div class="code-block-wrapper">` around every `<pre>` in both markdown content and tool output, containing a `<button class="copy-btn">Copy</button>`.

**CSS:**
```
.code-block-wrapper { position: relative; }
.copy-btn {
  position: absolute; top: 6px; right: 6px;
  opacity: 0; transition: opacity 0.15s;
  /* small pill button styling */
}
.code-block-wrapper:hover .copy-btn { opacity: 1; }
```

**JS:** In `initMessageInteractions`, add click handler for `.copy-btn`:
- `navigator.clipboard.writeText(pre.textContent)`
- Swap button text to "Copied!" for 1.5s

**Implementation note:** For markdown code blocks, the wrapper is added by the goldmark extension. For tool output `<pre>`, the wrapper is added in the Go template.

## Feature 4: Fix Thinking Block Truncation

**Problem:** `truncateLines` puts only 12 lines in the DOM. "Show more" toggles `maxHeight` but the full text was never rendered.

**Fix:**
- Render the **full text** in the template, wrapped in `<div class="truncated-content" style="max-height: 180px; overflow: hidden;">`
- Remove the `truncateLines` template function call — render complete text always
- Keep the `fade-overlay` and "Show more" button
- JS "Show more" handler sets `maxHeight: none` (full text is now in the DOM)
- This applies to both user prompt collapsible text and any future collapsible blocks

## Feature 5: Collapsed Middle Blocks

**Approach:** Server-side template wraps middle blocks; client-side JS expands.

**Threshold:** Collapse when an assistant turn has >8 blocks after pairing.

**Template structure:**
```html
<div class="assistant-turn">
  <!-- First text/thinking block(s) — always visible -->
  <div class="text-block md-content timeline-item">I'll investigate...</div>

  <!-- Collapsed middle -->
  <div class="collapsed-middle timeline-item">
    <div class="collapsed-middle-summary" role="button">
      <span>▸ 15 tool calls</span>
    </div>
    <div class="collapsed-middle-content" style="display:none;">
      <!-- All middle blocks rendered normally -->
      <div class="tool-row timeline-item status-complete">...</div>
      ...
    </div>
  </div>

  <!-- Last text block + trailing tool rows — always visible -->
  <div class="text-block md-content timeline-item">All tests pass...</div>
  <div class="tool-row timeline-item status-complete">...</div>
</div>
```

**Go logic (in template or pre-processing):**
- After `pairToolResults`, count blocks in each assistant turn
- If >8 blocks: find first text/thinking block index, last text block index
- Blocks between first+1 and last-1 become "middle" — wrapped in collapsed div
- Count tool_use blocks in middle for the summary label
- Blocks from last text block onward stay visible

**CSS:**
```
.collapsed-middle-summary {
  cursor: pointer; padding: 2px 4px;
  color: var(--muted); font-size: 13px;
}
.collapsed-middle-summary:hover { background: var(--bg-hover); }
```

**JS:** Click handler on `.collapsed-middle-summary` toggles `.collapsed-middle-content` display.

## Files Modified

| File | Changes |
|------|---------|
| `internal/web/goldmark_highlight.go` | **New** — goldmark extension for syntax highlighting |
| `internal/web/message_renderer.go` | Add ToolResultHTML, register highlight extension, collapsing logic |
| `internal/web/message_renderer_test.go` | Tests for highlighting, collapsing, truncation fix |
| `internal/web/static/dashboard.css` | Copy button, collapsed-middle, chroma CSS, code wrapper |
| `internal/web/static/dashboard.js` | Copy handler, collapsed-middle expand, truncation fix |
| `internal/web/static/styles.css` | Chroma CSS variables (from highlight.CSSVariables()) |
| `internal/web/static/dashboard.html` | Possibly inject chroma CSS `<style>` block |

## Testing

- Unit tests for goldmark highlight extension (code fence → chroma HTML)
- Unit tests for tool output highlighting (Read → highlighted, Edit → diff HTML)
- Unit tests for collapsed middle logic (>8 blocks triggers collapse, <8 doesn't)
- Unit test for truncation fix (full text in DOM)
- Visual verification via headless server + Playwright
