package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeEditAugment(t *testing.T) {
	oldText := `package main

func greet() string {
	return sayHello()
}
`
	newText := `package main

func greet() string {
	return sayGoodbye()
}

func extra() {}
`
	aug, err := computeEditAugment(oldText, newText, "main.go")
	require.NoError(t, err)

	assert.Greater(t, aug.Additions, 0, "should have additions")
	assert.Greater(t, aug.Deletions, 0, "should have deletions")
	assert.Contains(t, aug.DiffHTML, "Goodbye", "DiffHTML should contain added text")
	assert.Contains(t, aug.DiffHTML, "Hello", "DiffHTML should contain deleted text")
	assert.Contains(t, aug.DiffHTML, "diff-add", "DiffHTML should use diff-add class")
	assert.Contains(t, aug.DiffHTML, "diff-del", "DiffHTML should use diff-del class")
}

func TestComputeBashAugment(t *testing.T) {
	stdout := "line one\nline two\n"
	aug := computeBashAugment(stdout, "", 0)

	assert.Equal(t, 2, aug.LineCount)
	assert.False(t, aug.IsError)
	assert.False(t, aug.Truncated)
	assert.Contains(t, aug.StdoutHTML, "line one")
	assert.Contains(t, aug.StdoutHTML, "line two")
}

func TestComputeBashAugment_Error(t *testing.T) {
	stderr := "bash: unknown-cmd: command not found"
	aug := computeBashAugment("", stderr, 127)

	assert.True(t, aug.IsError, "exit code 127 should be an error")
	assert.Equal(t, 0, aug.LineCount, "no stdout lines")
	assert.Contains(t, aug.Stderr, "command not found")
}

func TestComputeReadAugment(t *testing.T) {
	content := `package main

import "fmt"
`
	aug, err := computeReadAugment(content, "main.go")
	require.NoError(t, err)

	assert.Equal(t, 2, aug.LineCount)
	assert.Contains(t, aug.ContentHTML, "package")
	assert.Equal(t, "Go", aug.Language)
}
