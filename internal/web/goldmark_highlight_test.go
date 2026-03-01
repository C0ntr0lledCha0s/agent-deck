package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
