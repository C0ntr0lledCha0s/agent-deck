package highlight

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHighlightCode_Go(t *testing.T) {
	code := `package main

import "fmt"

func Hello() string {
	return "hello world"
}

func main() {
	fmt.Println(Hello())
}`

	result, err := Code(code, "Go")
	require.NoError(t, err)

	// Should contain HTML span tags for syntax highlighting
	assert.Contains(t, result, "<span")

	// Should contain the function name
	assert.Contains(t, result, "Hello")

	// Should contain Go keywords
	assert.Contains(t, result, "func")
	assert.Contains(t, result, "package")
}

func TestHighlightCode_UnknownLanguage(t *testing.T) {
	code := `some random text that is not code
in any particular language 12345`

	result, err := Code(code, "totally-unknown-language-xyz")
	require.NoError(t, err)

	// Should still contain the original text even if language is unknown
	assert.Contains(t, result, "some random text")
	assert.Contains(t, result, "12345")
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"main.go", "Go"},
		{"app.js", "JavaScript"},
		{"style.css", "CSS"},
		{"Dockerfile", "Docker"},
		{"unknown.xyz", ""},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			lang := DetectLanguage(tt.filename)
			assert.Equal(t, tt.expected, lang)
		})
	}
}

func TestHighlightCode_CacheHit(t *testing.T) {
	code := `func cached() { return 42 }`

	result1, err := Code(code, "Go")
	require.NoError(t, err)

	result2, err := Code(code, "Go")
	require.NoError(t, err)

	// Results should be identical when served from cache
	assert.Equal(t, result1, result2)
}

func TestHighlightCode_UsesClasses(t *testing.T) {
	code := `package main

func main() {
	x := 42
}`

	result, err := Code(code, "Go")
	require.NoError(t, err)

	// Should use CSS classes, not inline styles
	assert.Contains(t, result, "class=")

	// Should NOT contain inline style attributes on span elements
	// (inline styles would look like style="color:#...")
	assert.False(t, strings.Contains(result, "style=\"color:"),
		"output should use CSS classes, not inline styles")
}

func TestCSS(t *testing.T) {
	css := CSS()
	assert.NotEmpty(t, css)
	// Should contain CSS class definitions
	assert.Contains(t, css, "chroma")
}

func TestCSSVariables(t *testing.T) {
	vars := CSSVariables()
	assert.NotEmpty(t, vars)
	// Should contain CSS custom properties
	assert.Contains(t, vars, "--")
	// Should support dark theme
	assert.Contains(t, vars, "dark")
}

func TestCacheEviction(t *testing.T) {
	// Fill cache beyond maxCacheSize to trigger eviction
	for i := 0; i < maxCacheSize+10; i++ {
		code := strings.Repeat("x", i+1)
		_, err := Code(code, "text")
		require.NoError(t, err)
	}

	// Cache should not exceed maxCacheSize after eviction
	cacheMu.RLock()
	size := len(cache)
	cacheMu.RUnlock()

	assert.LessOrEqual(t, size, maxCacheSize,
		"cache size should not exceed maxCacheSize after eviction")
}
