package web

import (
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/highlight"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHighlightCSSFileMatchesGenerated(t *testing.T) {
	generated := highlight.CSSVariables()
	fileContent, err := embeddedStaticFiles.ReadFile("static/highlight.css")
	require.NoError(t, err)
	assert.Equal(t, generated, string(fileContent))
}
