package web

import (
	"html/template"

	"github.com/asheshgoplani/agent-deck/internal/highlight"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
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

	lang := string(n.Language(source))

	// Extract code content from all lines.
	var code []byte
	for i := 0; i < n.Lines().Len(); i++ {
		line := n.Lines().At(i)
		code = append(code, line.Value(source)...)
	}

	highlighted, err := highlight.Code(string(code), lang)
	if err != nil {
		// Fallback: plain escaped <pre><code>
		_, _ = w.WriteString("<pre><code>")
		_, _ = w.WriteString(template.HTMLEscapeString(string(code)))
		_, _ = w.WriteString("</code></pre>\n")
		return ast.WalkContinue, nil
	}

	_, _ = w.WriteString(highlighted)
	_, _ = w.WriteString("\n")
	return ast.WalkContinue, nil
}
