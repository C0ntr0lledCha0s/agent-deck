# Message Pane Enhancements Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add syntax highlighting, copy-to-clipboard, diff rendering, thinking block fix, and collapsed middle blocks to the messages pane.

**Architecture:** Five independent features layered on the existing message renderer. Features 1-2 (highlighting) build on the existing `internal/highlight` Chroma package. Feature 3 (copy button) is pure CSS/JS. Feature 4 (truncation fix) is a template+JS fix. Feature 5 (collapse) adds server-side block grouping with client-side expand.

**Tech Stack:** Go (goldmark, chroma), vanilla JS, CSS

---

## Task 1: Add Chroma CSS Variables to Dashboard

Inject the syntax highlighting CSS so Chroma-highlighted HTML renders with colors.

**Files:**
- Create: `internal/web/static/highlight.css`
- Modify: `internal/web/static/dashboard.html:18-19`

**Step 1: Generate and save the Chroma CSS**

Create `internal/web/static/highlight.css` by calling `highlight.CSSVariables()`. Since this is static content that rarely changes, generate it once and save as a static file:

```bash
# In a throwaway Go program, or just copy from highlight.go:CSSVariables() output
```

Simpler approach: create `highlight.css` manually with the CSS variable definitions already in `internal/highlight/highlight.go:110-213`. Copy the output of `CSSVariables()` directly into the file.

Write a small test to verify the file stays in sync:

```go
// internal/web/highlight_css_test.go
func TestHighlightCSSFileMatchesGenerated(t *testing.T) {
    generated := highlight.CSSVariables()
    fileContent, err := embeddedStaticFiles.ReadFile("static/highlight.css")
    require.NoError(t, err)
    assert.Equal(t, generated, string(fileContent))
}
```

**Step 2: Link the CSS in dashboard.html**

Add after the existing stylesheet links (line ~19):

```html
<link rel="stylesheet" href="/static/highlight.css" />
```

**Step 3: Run test to verify**

```bash
go test -v ./internal/web/... -run TestHighlightCSS
```

**Step 4: Commit**

```bash
git add internal/web/static/highlight.css internal/web/static/dashboard.html internal/web/highlight_css_test.go
git commit -m "feat(web): add Chroma syntax highlight CSS to dashboard"
```

---

## Task 2: Goldmark Syntax Highlighting Extension

Replace goldmark's default fenced code block renderer with one that uses Chroma.

**Files:**
- Create: `internal/web/goldmark_highlight.go`
- Create: `internal/web/goldmark_highlight_test.go`
- Modify: `internal/web/message_renderer.go:297-300` (mdRenderer initialization)

**Step 1: Write the failing test**

```go
// internal/web/goldmark_highlight_test.go
package web

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestRenderMarkdown_HighlightsFencedCode(t *testing.T) {
    input := "```go\nfunc main() {}\n```"
    html := string(renderMarkdown(input))
    // Should contain chroma CSS classes, not plain <code>
    assert.Contains(t, html, `class="chroma"`)
    assert.Contains(t, html, "func")
    assert.NotContains(t, html, `<code class="language-go">`)
}

func TestRenderMarkdown_PlainCodeBlockNoLanguage(t *testing.T) {
    input := "```\nhello world\n```"
    html := string(renderMarkdown(input))
    // No language hint — should still render as <pre> with chroma fallback
    assert.Contains(t, html, "<pre")
    assert.Contains(t, html, "hello world")
}

func TestRenderMarkdown_InlineCodeUnchanged(t *testing.T) {
    input := "Use `foo()` here"
    html := string(renderMarkdown(input))
    assert.Contains(t, html, "<code>foo()</code>")
    // Inline code should NOT get chroma treatment
    assert.NotContains(t, html, "chroma")
}
```

**Step 2: Run test to verify it fails**

```bash
go test -v ./internal/web/... -run TestRenderMarkdown_Highlights
```
Expected: FAIL — `class="chroma"` not found (goldmark renders plain `<code class="language-go">`)

**Step 3: Implement the goldmark extension**

```go
// internal/web/goldmark_highlight.go
package web

import (
    "github.com/asheshgoplani/agent-deck/internal/highlight"
    "github.com/yuin/goldmark"
    "github.com/yuin/goldmark/ast"
    "github.com/yuin/goldmark/renderer"
    "github.com/yuin/goldmark/renderer/html"
    "github.com/yuin/goldmark/util"
)

// chromaHighlighter is a goldmark extension that replaces fenced code block
// rendering with Chroma syntax-highlighted HTML.
type chromaHighlighter struct{}

func (c *chromaHighlighter) Extend(m goldmark.Markdown) {
    m.Renderer().AddOptions(renderer.WithNodeRenderers(
        util.Prioritized(&chromaCodeBlockRenderer{}, 100),
    ))
}

type chromaCodeBlockRenderer struct{}

func (r *chromaCodeBlockRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
    reg.Register(ast.KindFencedCodeBlock, r.renderFencedCodeBlock)
}

func (r *chromaCodeBlockRenderer) renderFencedCodeBlock(
    w util.BufWriter, source []byte, node ast.Node, entering bool,
) (ast.WalkStatus, error) {
    if !entering {
        return ast.WalkContinue, nil
    }

    n := node.(*ast.FencedCodeBlock)

    // Extract language from info string.
    lang := ""
    if n.Info != nil {
        lang = string(n.Info.Segment.Value(source))
    }

    // Extract code content from all lines.
    var code []byte
    for i := 0; i < n.Lines().Len(); i++ {
        line := n.Lines().At(i)
        code = append(code, line.Value(source)...)
    }

    // Highlight with Chroma.
    highlighted, err := highlight.Code(string(code), lang)
    if err != nil {
        // Fallback: plain escaped <pre><code>
        _, _ = w.WriteString("<pre><code>")
        _, _ = w.Write(html.DefaultWriter.RawWriter().Bytes()) // use escaping
        _, _ = w.WriteString(template.HTMLEscapeString(string(code)))
        _, _ = w.WriteString("</code></pre>\n")
        return ast.WalkContinue, nil
    }

    _, _ = w.WriteString(highlighted)
    _, _ = w.WriteString("\n")
    return ast.WalkContinue, nil
}
```

Note: The Chroma `highlight.Code()` output already includes `<pre class="chroma">` wrapper. Review the exact output format and adjust if needed — the function may output `<pre ...><code>...</code></pre>` depending on Chroma's html formatter settings.

**Step 4: Wire into mdRenderer**

In `message_renderer.go`, update the `mdRenderer` initialization (~line 297):

```go
var mdRenderer = goldmark.New(
    goldmark.WithExtensions(extension.GFM, &chromaHighlighter{}),
    goldmark.WithRendererOptions(html.WithUnsafe()),
)
```

**Step 5: Run tests**

```bash
go test -v ./internal/web/... -run TestRenderMarkdown
```
Expected: PASS

**Step 6: Commit**

```bash
git add internal/web/goldmark_highlight.go internal/web/goldmark_highlight_test.go internal/web/message_renderer.go
git commit -m "feat(web): add Chroma syntax highlighting for markdown code blocks"
```

---

## Task 3: Syntax-Highlighted Tool Output

Enhance tool result rendering to produce highlighted HTML for Read/Write and diff HTML for Edit.

**Files:**
- Modify: `internal/web/message_renderer.go` (contentBlock struct, pairToolResults, template)
- Modify: `internal/web/message_renderer_test.go`

**Step 1: Write the failing tests**

```go
func TestPairToolResults_HighlightsReadOutput(t *testing.T) {
    blocks := []contentBlock{
        {Type: "tool_use", ToolName: "Read", ToolUseID: "t1",
            ToolInput: json.RawMessage(`{"file_path":"main.go"}`)},
        {Type: "tool_result", ToolUseID: "t1", Text: "package main\n\nfunc main() {}"},
    }
    paired := pairToolResults(blocks)
    require.Len(t, paired, 1)
    // Should have highlighted HTML
    assert.NotEmpty(t, paired[0].ToolResultHTML)
    assert.Contains(t, string(paired[0].ToolResultHTML), "chroma")
}

func TestPairToolResults_DiffForEditOutput(t *testing.T) {
    blocks := []contentBlock{
        {Type: "tool_use", ToolName: "Edit", ToolUseID: "t1",
            ToolInput: json.RawMessage(`{"file_path":"main.go","old_string":"foo","new_string":"bar"}`)},
        {Type: "tool_result", ToolUseID: "t1", Text: "OK"},
    }
    paired := pairToolResults(blocks)
    require.Len(t, paired, 1)
    assert.NotEmpty(t, string(paired[0].ToolResultHTML))
    assert.Contains(t, string(paired[0].ToolResultHTML), "diff-del")
    assert.Contains(t, string(paired[0].ToolResultHTML), "diff-add")
}

func TestPairToolResults_BashOutputPlain(t *testing.T) {
    blocks := []contentBlock{
        {Type: "tool_use", ToolName: "Bash", ToolUseID: "t1",
            ToolInput: json.RawMessage(`{"command":"echo hi"}`)},
        {Type: "tool_result", ToolUseID: "t1", Text: "hi"},
    }
    paired := pairToolResults(blocks)
    require.Len(t, paired, 1)
    // Bash output: ToolResultHTML should be HTML-escaped plain text
    assert.Equal(t, template.HTML("hi"), paired[0].ToolResultHTML)
}
```

**Step 2: Run tests to verify they fail**

```bash
go test -v ./internal/web/... -run TestPairToolResults_Highlight
```

**Step 3: Add ToolResultHTML field and highlighting logic**

Add to `contentBlock` struct:
```go
ToolResultHTML template.HTML // pre-rendered HTML for tool output (highlighted or escaped)
```

Update `pairToolResults` to compute highlighted HTML:

```go
func pairToolResults(blocks []contentBlock) []contentBlock {
    resultMap := make(map[string]string)
    for _, b := range blocks {
        if b.Type == "tool_result" && b.ToolUseID != "" {
            resultMap[b.ToolUseID] = b.Text
        }
    }

    var out []contentBlock
    for _, b := range blocks {
        if b.Type == "tool_result" {
            continue
        }
        if b.Type == "tool_use" && b.ToolUseID != "" {
            if text, ok := resultMap[b.ToolUseID]; ok {
                b.ToolResultText = text
                b.ToolResultHTML = highlightToolOutput(b.ToolName, b.ToolInput, text)
            }
        }
        out = append(out, b)
    }
    return out
}
```

Add new function:

```go
// highlightToolOutput produces syntax-highlighted or diff-rendered HTML
// for tool results based on the tool name and input.
func highlightToolOutput(toolName string, input json.RawMessage, text string) template.HTML {
    if text == "" {
        return ""
    }

    var m map[string]interface{}
    if len(input) > 0 {
        _ = json.Unmarshal(input, &m)
    }

    switch toolName {
    case "Read", "Write":
        if fp, ok := m["file_path"].(string); ok {
            lang := highlight.DetectLanguage(fp)
            if lang != "" {
                if highlighted, err := highlight.Code(text, lang); err == nil {
                    return template.HTML(highlighted)
                }
            }
        }
    case "Edit":
        if oldStr, ok := m["old_string"].(string); ok {
            if newStr, ok := m["new_string"].(string); ok {
                if aug, err := computeEditAugment(oldStr, newStr, ""); err == nil {
                    return template.HTML(`<pre class="tool-output tool-output-diff">` + aug.DiffHTML + `</pre>`)
                }
            }
        }
    }

    return template.HTML(template.HTMLEscapeString(text))
}
```

**Step 4: Update the template**

Replace the tool output `<pre>` in the template:

Old:
```
{{if .ToolResultText}}<pre class="tool-output">{{.ToolResultText}}</pre>{{end}}
```

New:
```
{{if .ToolResultHTML}}<div class="tool-output-wrap">{{.ToolResultHTML}}</div>
{{else if .ToolResultText}}<pre class="tool-output">{{.ToolResultText}}</pre>{{end}}
```

When `ToolResultHTML` is set, it's already pre-rendered HTML (may include its own `<pre>` for Chroma output or diff output). When only `ToolResultText` is set (fallback), use the existing escaped `<pre>`.

**Step 5: Run tests**

```bash
go test -v ./internal/web/... -run TestPairToolResults
```

**Step 6: Commit**

```bash
git add internal/web/message_renderer.go internal/web/message_renderer_test.go
git commit -m "feat(web): add syntax highlighting and diff rendering for tool output"
```

---

## Task 4: Copy Button on Code Blocks

Add a hover-reveal copy button on `<pre>` elements.

**Files:**
- Modify: `internal/web/static/dashboard.css` (add copy button styles)
- Modify: `internal/web/static/dashboard.js` (add copy handler in initMessageInteractions)

**Step 1: Add CSS for the copy button**

Add to `dashboard.css` after the `.tool-output` section:

```css
/* ── Copy button on code blocks ──────────────────────────────── */

.code-block-wrapper {
  position: relative;
}

.copy-btn {
  position: absolute;
  top: 6px;
  right: 6px;
  padding: 2px 8px;
  border: 1px solid var(--border-color, #3c3c3c);
  border-radius: 4px;
  background: var(--bg-panel);
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: 11px;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s;
  z-index: 1;
}

.code-block-wrapper:hover .copy-btn {
  opacity: 1;
}

.copy-btn:hover {
  color: var(--text);
  border-color: var(--text-dim);
}

.copy-btn.copied {
  color: var(--green);
  border-color: var(--green);
}
```

**Step 2: Add JS copy handler**

In `initMessageInteractions` in `dashboard.js`, add after the existing click handlers but BEFORE the closing `})`:

```js
// Copy button on code blocks — dynamically wrap <pre> elements
var pres = container.querySelectorAll("pre")
for (var i = 0; i < pres.length; i++) {
  var pre = pres[i]
  if (pre.parentElement && pre.parentElement.classList.contains("code-block-wrapper")) continue
  var wrapper = document.createElement("div")
  wrapper.className = "code-block-wrapper"
  pre.parentNode.insertBefore(wrapper, pre)
  wrapper.appendChild(pre)
  var btn = document.createElement("button")
  btn.className = "copy-btn"
  btn.type = "button"
  btn.textContent = "Copy"
  wrapper.appendChild(btn)
}

// Copy button click handler (event delegation)
container.addEventListener("click", function (e) {
  var copyBtn = e.target.closest(".copy-btn")
  if (!copyBtn) return
  var wrapper = copyBtn.closest(".code-block-wrapper")
  if (!wrapper) return
  var pre = wrapper.querySelector("pre")
  if (!pre) return
  navigator.clipboard.writeText(pre.textContent).then(function () {
    copyBtn.textContent = "Copied!"
    copyBtn.classList.add("copied")
    setTimeout(function () {
      copyBtn.textContent = "Copy"
      copyBtn.classList.remove("copied")
    }, 1500)
  })
})
```

Wait — there's a subtlety. The `initMessageInteractions` already sets up a click listener with event delegation. We should add the copy button logic inside the existing click handler, not create a second listener. Add it as another branch in the existing `container.addEventListener("click", function(e) {` handler.

**Step 3: Build and verify visually**

```bash
make build && ./build/agent-deck web --headless --listen 127.0.0.1:8421
```

Navigate to a session with messages, hover over a code block, verify the "Copy" button appears.

**Step 4: Commit**

```bash
git add internal/web/static/dashboard.css internal/web/static/dashboard.js
git commit -m "feat(web): add copy-to-clipboard button on code blocks"
```

---

## Task 5: Fix Thinking Block Truncation

Fix the "Show more" button so it actually reveals full content.

**Files:**
- Modify: `internal/web/message_renderer.go:335-337` (template)
- Modify: `internal/web/message_renderer_test.go`

**Step 1: Write failing test**

```go
func TestRenderMessagesHTML_TruncatedUserContainsFullText(t *testing.T) {
    // Create text longer than 12 lines
    longText := ""
    for i := 0; i < 20; i++ {
        longText += fmt.Sprintf("Line %d of the message\n", i+1)
    }
    turns := []renderedTurn{
        {Role: "user", Blocks: []contentBlock{{Type: "text", Text: longText}}},
    }
    html, err := renderMessagesHTML(turns)
    require.NoError(t, err)
    // The full text must be in the DOM (not truncated)
    assert.Contains(t, html, "Line 20")
    assert.Contains(t, html, "Line 1")
    assert.Contains(t, html, "collapsible-text")
    assert.Contains(t, html, "show-more-btn")
}
```

**Step 2: Run test to verify it fails**

```bash
go test -v ./internal/web/... -run TestRenderMessagesHTML_Truncated
```
Expected: FAIL — "Line 20" not found (truncated at line 12)

**Step 3: Fix the template**

In the template, change the truncation approach. Replace the `needsTruncation` branch:

Old:
```
{{if needsTruncation $clean}}<div class="text-block collapsible-text"><div class="truncated-content">{{truncateLines $clean 12}}<div class="fade-overlay"></div></div><button class="show-more-btn" type="button">Show more</button></div>
```

New:
```
{{if needsTruncation $clean}}<div class="text-block collapsible-text"><div class="truncated-content">{{$clean}}<div class="fade-overlay"></div></div><button class="show-more-btn" type="button">Show more</button></div>
```

The only change: `{{truncateLines $clean 12}}` → `{{$clean}}`. The full text is now in the DOM. The CSS `max-height` + `overflow: hidden` on `.truncated-content` handles the visual truncation.

**Step 4: Add CSS max-height constraint**

In `dashboard.css`, update `.collapsible-text .truncated-content`:

```css
.collapsible-text .truncated-content {
  position: relative;
  overflow: hidden;
  max-height: 200px;  /* ADD: constrain height via CSS instead of text truncation */
}
```

**Step 5: Run tests**

```bash
go test -v ./internal/web/... -run TestRenderMessagesHTML
```

**Step 6: Commit**

```bash
git add internal/web/message_renderer.go internal/web/message_renderer_test.go internal/web/static/dashboard.css
git commit -m "fix(web): render full text in collapsible blocks so Show More works"
```

---

## Task 6: Collapsed Middle Blocks

Add server-side grouping that collapses middle blocks in long assistant turns.

**Files:**
- Modify: `internal/web/message_renderer.go` (add collapsing logic + template changes)
- Modify: `internal/web/message_renderer_test.go`
- Modify: `internal/web/static/dashboard.css` (collapsed-middle styles)
- Modify: `internal/web/static/dashboard.js` (expand handler)

**Step 1: Write failing tests**

```go
func TestRenderMessagesHTML_CollapsesLongTurns(t *testing.T) {
    // Create a turn with >8 blocks: text + 10 tools + text
    blocks := []contentBlock{
        {Type: "text", Text: "I'll start working on this."},
    }
    for i := 0; i < 10; i++ {
        blocks = append(blocks, contentBlock{
            Type: "tool_use", ToolName: "Edit", ToolUseID: fmt.Sprintf("t%d", i),
            ToolInput: json.RawMessage(`{"file_path":"f.go","old_string":"a","new_string":"b"}`),
        })
    }
    blocks = append(blocks, contentBlock{Type: "text", Text: "All done, tests pass."})

    turns := []renderedTurn{{Role: "assistant", Blocks: blocks}}
    html, err := renderMessagesHTML(turns)
    require.NoError(t, err)
    assert.Contains(t, html, "collapsed-middle")
    assert.Contains(t, html, "collapsed-middle-summary")
    assert.Contains(t, html, "10 tool calls")
    // First and last text blocks should be outside the collapsed section
    assert.Contains(t, html, "I&#39;ll start working on this.")
    assert.Contains(t, html, "All done, tests pass.")
}

func TestRenderMessagesHTML_NoCollapseShortTurns(t *testing.T) {
    blocks := []contentBlock{
        {Type: "text", Text: "hello"},
        {Type: "tool_use", ToolName: "Bash", ToolUseID: "t1"},
        {Type: "text", Text: "done"},
    }
    turns := []renderedTurn{{Role: "assistant", Blocks: blocks}}
    html, err := renderMessagesHTML(turns)
    require.NoError(t, err)
    assert.NotContains(t, html, "collapsed-middle")
}
```

**Step 2: Run test to verify it fails**

```bash
go test -v ./internal/web/... -run TestRenderMessagesHTML_Collapse
```

**Step 3: Implement collapsing logic**

This is best done as a pre-processing step on the blocks before template rendering. Add a new struct and function:

```go
// collapsedBlock wraps a contentBlock with rendering metadata.
type collapsedBlock struct {
    contentBlock
    CollapseStart bool   // true if this is the "... N tool calls" summary
    CollapseEnd   bool   // true if this closes the collapsed section
    CollapseCount int    // number of tool calls in collapsed section
    Hidden        bool   // true if this block is inside the collapsed section
}
```

Alternative (simpler): Instead of modifying the template iteration, pre-process the blocks to insert a synthetic "collapse_summary" block and mark hidden blocks. The template already switches on `.Type`, so add a new type:

```go
const collapseThreshold = 8

// collapseMiddleBlocks inserts collapse markers into long assistant turns.
// Returns the modified block list. Short turns are returned unchanged.
func collapseMiddleBlocks(blocks []contentBlock) []contentBlock {
    if len(blocks) <= collapseThreshold {
        return blocks
    }

    // Find first text/thinking index and last text index.
    firstTextIdx := -1
    lastTextIdx := -1
    for i, b := range blocks {
        if b.Type == "text" || b.Type == "thinking" {
            if firstTextIdx == -1 {
                firstTextIdx = i
            }
            lastTextIdx = i
        }
    }

    // If we can't find boundaries, don't collapse.
    if firstTextIdx == -1 || lastTextIdx == -1 || lastTextIdx <= firstTextIdx+1 {
        return blocks
    }

    // Count tool calls in the middle section.
    middleStart := firstTextIdx + 1
    middleEnd := lastTextIdx // exclusive
    toolCount := 0
    for i := middleStart; i < middleEnd; i++ {
        if blocks[i].Type == "tool_use" {
            toolCount++
        }
    }

    if toolCount == 0 {
        return blocks
    }

    // Build result: head + collapse_start + middle (hidden) + collapse_end + tail
    var out []contentBlock
    out = append(out, blocks[:middleStart]...)
    out = append(out, contentBlock{
        Type: "collapse_start",
        Text: fmt.Sprintf("%d tool calls", toolCount),
    })
    for _, b := range blocks[middleStart:middleEnd] {
        b.Type = "collapse_hidden_" + b.Type // prefix to mark as hidden
        out = append(out, b)
    }
    out = append(out, contentBlock{Type: "collapse_end"})
    out = append(out, blocks[middleEnd:]...)
    return out
}
```

Wait — prefixing the Type is fragile. Better approach: use a wrapper in the template. Add the collapse markers as separate blocks with their own types, and render hidden blocks normally but inside a hidden `<div>`.

Actually, the cleanest approach: render the collapsed section in the Go template by splitting the blocks into three slices (head, middle, tail) and rendering them separately. This avoids modifying block types.

Revise: Instead of modifying blocks, change the template to accept a `renderedTurn` that has pre-computed slices:

Add fields to `renderedTurn`:
```go
type renderedTurn struct {
    Role           string
    Blocks         []contentBlock // all blocks (used for short turns)
    HeadBlocks     []contentBlock // first text block(s) — visible
    MiddleBlocks   []contentBlock // collapsed middle
    TailBlocks     []contentBlock // last text + trailing tools — visible
    MiddleToolCount int           // number of tool calls in middle (for summary)
    Collapsed      bool           // true if this turn uses collapse
    Time           time.Time
}
```

Pre-compute in `renderMessagesHTML` or a helper before template execution.

The template then becomes:
```
{{if .Collapsed}}
  {{range .HeadBlocks}}...{{end}}
  <div class="collapsed-middle timeline-item">
    <div class="collapsed-middle-summary">▸ {{.MiddleToolCount}} tool calls</div>
    <div class="collapsed-middle-content" style="display:none;">
      {{range .MiddleBlocks}}...{{end}}
    </div>
  </div>
  {{range .TailBlocks}}...{{end}}
{{else}}
  {{range .Blocks}}...{{end}}
{{end}}
```

This duplicates the block-rendering logic (the `{{if eq .Type "thinking"}}...` switch). To keep it DRY, extract the block rendering into a sub-template `{{template "block" .}}`.

**Step 4: Update the template with sub-template**

Define a sub-template for rendering a single block:

```go
{{define "block"}}{{if eq .Type "thinking"}}...{{else if eq .Type "text"}}...{{else if eq .Type "tool_use"}}...{{end}}{{end}}
```

Then the main template uses `{{template "block" .}}` in both the collapsed and non-collapsed paths.

**Step 5: Add CSS**

```css
.collapsed-middle-summary {
  cursor: pointer;
  padding: 2px 4px;
  color: var(--muted);
  font-size: 13px;
  border-radius: 2px;
}

.collapsed-middle-summary:hover {
  background: var(--bg-hover);
  color: var(--text);
}
```

**Step 6: Add JS expand handler**

In `initMessageInteractions`, add to the click handler:

```js
var collapseSummary = e.target.closest(".collapsed-middle-summary")
if (collapseSummary) {
  var content = collapseSummary.nextElementSibling
  if (content && content.classList.contains("collapsed-middle-content")) {
    var isShown = content.style.display !== "none"
    content.style.display = isShown ? "none" : ""
    collapseSummary.textContent = (isShown ? "▸ " : "▾ ") + collapseSummary.textContent.slice(2)
  }
  return
}
```

**Step 7: Run tests**

```bash
go test -v ./internal/web/... -run TestRenderMessagesHTML
```

**Step 8: Commit**

```bash
git add internal/web/message_renderer.go internal/web/message_renderer_test.go internal/web/static/dashboard.css internal/web/static/dashboard.js
git commit -m "feat(web): collapse middle blocks in long assistant turns"
```

---

## Task 7: Visual Verification

Build and visually verify all five features together.

**Step 1: Build**

```bash
make build
```

**Step 2: Start headless server**

```bash
./build/agent-deck web --headless --listen 127.0.0.1:8421
```

**Step 3: Verify with Playwright**

Navigate to dashboard, select a session with messages. Check:
- [ ] Fenced code blocks in assistant text have syntax colors
- [ ] Read tool output is syntax-highlighted
- [ ] Edit tool output shows red/green diff coloring
- [ ] Hovering over a `<pre>` shows "Copy" button
- [ ] Clicking "Copy" copies text and shows "Copied!"
- [ ] Long user prompts show "Show more" and expanding reveals full text
- [ ] Long assistant turns (>8 blocks) collapse middle into "N tool calls"
- [ ] Clicking collapsed summary expands to show all blocks

**Step 4: Run full test suite**

```bash
go test -v ./internal/web/...
```

**Step 5: Commit any fixes**

```bash
git add -u && git commit -m "fix(web): address visual verification findings"
```
