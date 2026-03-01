// Package highlight provides Chroma-based syntax highlighting with CSS class
// output. It wraps github.com/alecthomas/chroma/v2 to produce HTML fragments
// suitable for embedding in web pages, using CSS classes (not inline styles)
// so that light/dark theming works via CSS variables.
package highlight

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// maxCacheSize is the maximum number of entries in the highlight cache.
// When exceeded the entire cache is cleared (clear-on-full eviction).
const maxCacheSize = 256

var (
	// formatter outputs CSS class names, not inline styles.
	formatter = html.New(html.WithClasses(true), html.TabWidth(4))

	// formatterLN is like formatter but includes line numbers.
	formatterLN = html.New(html.WithClasses(true), html.TabWidth(4), html.WithLineNumbers(true))

	// style is used only for CSS class generation; actual colours come from
	// CSS variables injected by CSSVariables().
	style = styles.Get("monokai")

	// Cache stores highlighted HTML keyed by content hash.
	cacheMu sync.RWMutex
	cache   = make(map[string]string, maxCacheSize)
)

// Code highlights the given source code using the specified language name.
// It returns an HTML fragment containing <span> elements with CSS class
// attributes. If the language is unknown, it falls back to a plain-text lexer
// so the original text is always preserved in the output.
func Code(code, language string) (string, error) {
	key := cacheKey(code, language)

	// Fast path: check cache under read lock. Note: two goroutines may both
	// miss and compute the same entry concurrently — this is benign (redundant
	// work only) and avoids holding a write lock during expensive tokenisation.
	cacheMu.RLock()
	if cached, ok := cache[key]; ok {
		cacheMu.RUnlock()
		return cached, nil
	}
	cacheMu.RUnlock()

	// Resolve lexer.
	lexer := lexers.Get(language)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	// Tokenise.
	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return "", fmt.Errorf("highlight: tokenise: %w", err)
	}

	// Format to HTML.
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return "", fmt.Errorf("highlight: format: %w", err)
	}

	result := buf.String()

	// Store in cache with clear-on-full eviction.
	cacheMu.Lock()
	if len(cache) >= maxCacheSize {
		cache = make(map[string]string, maxCacheSize)
	}
	cache[key] = result
	cacheMu.Unlock()

	return result, nil
}

// CodeWithLineNumbers is like Code but includes line numbers in the output.
func CodeWithLineNumbers(code, language string) (string, error) {
	key := cacheKey(code, language+":ln")

	cacheMu.RLock()
	if cached, ok := cache[key]; ok {
		cacheMu.RUnlock()
		return cached, nil
	}
	cacheMu.RUnlock()

	lexer := lexers.Get(language)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return "", fmt.Errorf("highlight: tokenise: %w", err)
	}

	var buf bytes.Buffer
	if err := formatterLN.Format(&buf, style, iterator); err != nil {
		return "", fmt.Errorf("highlight: format: %w", err)
	}

	result := buf.String()

	cacheMu.Lock()
	if len(cache) >= maxCacheSize {
		cache = make(map[string]string, maxCacheSize)
	}
	cache[key] = result
	cacheMu.Unlock()

	return result, nil
}

// DetectLanguage maps a filename to its Chroma lexer name. If no lexer
// matches the filename, it returns an empty string.
func DetectLanguage(filename string) string {
	lexer := lexers.Match(filename)
	if lexer == nil {
		return ""
	}
	return lexer.Config().Name
}

// CSS returns the CSS class definitions needed by the highlighted HTML output.
// These classes are generated from the configured Chroma style and should be
// included once on any page that renders highlighted code.
func CSS() string {
	var buf bytes.Buffer
	// WriteCSS to bytes.Buffer cannot fail; the only error source is the
	// underlying writer, and bytes.Buffer.Write always returns nil error.
	_ = formatter.WriteCSS(&buf, style)
	return buf.String()
}

// CSSVariables returns CSS custom property definitions for syntax highlight
// classes, supporting both light and dark themes via [data-theme="dark"].
// Include this CSS on pages that render highlighted code to enable theming.
func CSSVariables() string {
	var b strings.Builder

	b.WriteString("/* Syntax highlighting CSS variables — light theme (default) */\n")
	b.WriteString(":root {\n")
	b.WriteString("  --hl-bg: #fafafa;\n")
	b.WriteString("  --hl-fg: #383a42;\n")
	b.WriteString("  --hl-keyword: #a626a4;\n")
	b.WriteString("  --hl-string: #50a14f;\n")
	b.WriteString("  --hl-number: #986801;\n")
	b.WriteString("  --hl-comment: #a0a1a7;\n")
	b.WriteString("  --hl-function: #4078f2;\n")
	b.WriteString("  --hl-type: #c18401;\n")
	b.WriteString("  --hl-operator: #383a42;\n")
	b.WriteString("  --hl-punctuation: #383a42;\n")
	b.WriteString("  --hl-builtin: #e45649;\n")
	b.WriteString("  --hl-variable: #e45649;\n")
	b.WriteString("  --hl-added: #50a14f;\n")
	b.WriteString("  --hl-deleted: #e45649;\n")
	b.WriteString("  --hl-changed: #c18401;\n")
	b.WriteString("  --hl-line-highlight: rgba(0, 0, 0, 0.05);\n")
	b.WriteString("  --hl-gutter: #9d9d9f;\n")
	b.WriteString("}\n\n")

	b.WriteString("/* Syntax highlighting CSS variables — dark theme */\n")
	b.WriteString("[data-theme=\"dark\"] {\n")
	b.WriteString("  --hl-bg: #282c34;\n")
	b.WriteString("  --hl-fg: #abb2bf;\n")
	b.WriteString("  --hl-keyword: #c678dd;\n")
	b.WriteString("  --hl-string: #98c379;\n")
	b.WriteString("  --hl-number: #d19a66;\n")
	b.WriteString("  --hl-comment: #5c6370;\n")
	b.WriteString("  --hl-function: #61afef;\n")
	b.WriteString("  --hl-type: #e5c07b;\n")
	b.WriteString("  --hl-operator: #abb2bf;\n")
	b.WriteString("  --hl-punctuation: #abb2bf;\n")
	b.WriteString("  --hl-builtin: #e06c75;\n")
	b.WriteString("  --hl-variable: #e06c75;\n")
	b.WriteString("  --hl-added: #98c379;\n")
	b.WriteString("  --hl-deleted: #e06c75;\n")
	b.WriteString("  --hl-changed: #e5c07b;\n")
	b.WriteString("  --hl-line-highlight: rgba(255, 255, 255, 0.05);\n")
	b.WriteString("  --hl-gutter: #636d83;\n")
	b.WriteString("}\n\n")

	b.WriteString("/* Map Chroma classes to CSS variables */\n")
	b.WriteString(".chroma { background-color: var(--hl-bg); color: var(--hl-fg); }\n")
	b.WriteString(".chroma .k,\n")  // Keyword
	b.WriteString(".chroma .kc,\n") // KeywordConstant
	b.WriteString(".chroma .kd,\n") // KeywordDeclaration
	b.WriteString(".chroma .kn,\n") // KeywordNamespace
	b.WriteString(".chroma .kp,\n") // KeywordPseudo
	b.WriteString(".chroma .kr,\n") // KeywordReserved
	b.WriteString(".chroma .kt { color: var(--hl-keyword); }\n")
	b.WriteString(".chroma .s,\n")  // String
	b.WriteString(".chroma .sa,\n") // StringAffix
	b.WriteString(".chroma .sb,\n") // StringBacktick
	b.WriteString(".chroma .sc,\n") // StringChar
	b.WriteString(".chroma .dl,\n") // StringDelimiter
	b.WriteString(".chroma .sd,\n") // StringDoc
	b.WriteString(".chroma .s2,\n") // StringDouble
	b.WriteString(".chroma .se,\n") // StringEscape
	b.WriteString(".chroma .sh,\n") // StringHeredoc
	b.WriteString(".chroma .si,\n") // StringInterpol
	b.WriteString(".chroma .sx,\n") // StringOther
	b.WriteString(".chroma .sr,\n") // StringRegex
	b.WriteString(".chroma .s1,\n") // StringSingle
	b.WriteString(".chroma .ss { color: var(--hl-string); }\n")
	b.WriteString(".chroma .m,\n")  // Number
	b.WriteString(".chroma .mb,\n") // NumberBin
	b.WriteString(".chroma .mf,\n") // NumberFloat
	b.WriteString(".chroma .mh,\n") // NumberHex
	b.WriteString(".chroma .mi,\n") // NumberInteger
	b.WriteString(".chroma .il,\n") // NumberIntegerLong
	b.WriteString(".chroma .mo { color: var(--hl-number); }\n")
	b.WriteString(".chroma .c,\n")  // Comment
	b.WriteString(".chroma .ch,\n") // CommentHashbang
	b.WriteString(".chroma .cm,\n") // CommentMultiline
	b.WriteString(".chroma .c1,\n") // CommentSingle
	b.WriteString(".chroma .cs,\n") // CommentSpecial
	b.WriteString(".chroma .cp,\n") // CommentPreproc
	b.WriteString(".chroma .cpf { color: var(--hl-comment); font-style: italic; }\n")
	b.WriteString(".chroma .nf,\n") // NameFunction
	b.WriteString(".chroma .fm { color: var(--hl-function); }\n")
	b.WriteString(".chroma .nc,\n") // NameClass
	b.WriteString(".chroma .no,\n") // NameConstant
	b.WriteString(".chroma .nd,\n") // NameDecorator
	b.WriteString(".chroma .ni,\n") // NameEntity
	b.WriteString(".chroma .ne,\n") // NameException
	b.WriteString(".chroma .nt { color: var(--hl-type); }\n")
	b.WriteString(".chroma .o,\n")  // Operator
	b.WriteString(".chroma .ow { color: var(--hl-operator); }\n")
	b.WriteString(".chroma .p { color: var(--hl-punctuation); }\n")
	b.WriteString(".chroma .nb,\n") // NameBuiltin
	b.WriteString(".chroma .bp { color: var(--hl-builtin); }\n")
	b.WriteString(".chroma .nv,\n") // NameVariable
	b.WriteString(".chroma .vc,\n") // NameVariableClass
	b.WriteString(".chroma .vg,\n") // NameVariableGlobal
	b.WriteString(".chroma .vi { color: var(--hl-variable); }\n")
	b.WriteString(".chroma .gi { color: var(--hl-added); }\n")
	b.WriteString(".chroma .gd { color: var(--hl-deleted); }\n")
	b.WriteString(".chroma .hl { background-color: var(--hl-line-highlight); }\n")
	b.WriteString(".chroma .ln { color: var(--hl-gutter); }\n")

	return b.String()
}

// cacheKey returns a hex-encoded SHA-256 hash (truncated to 16 bytes / 32 hex
// chars) of the language and code, used as the cache map key.
func cacheKey(code, language string) string {
	h := sha256.New()
	h.Write([]byte(language))
	h.Write([]byte{0}) // separator
	h.Write([]byte(code))
	sum := h.Sum(nil)
	return fmt.Sprintf("%x", sum[:16])
}
