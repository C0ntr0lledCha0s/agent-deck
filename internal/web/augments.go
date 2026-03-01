// Package web — augments.go provides server-side HTML enrichments for tool
// results displayed in the web dashboard. It computes diffs, formats bash
// output, and syntax-highlights file contents before sending them to clients.
package web

import (
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/highlight"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// editAugment holds the result of diffing two versions of a file.
type editAugment struct {
	DiffHTML  string `json:"diffHtml"`  // HTML fragment with diff-add / diff-del spans
	Additions int    `json:"additions"` // number of added characters
	Deletions int    `json:"deletions"` // number of deleted characters
}

// bashAugment holds enriched metadata for a bash command result.
type bashAugment struct {
	StdoutHTML string `json:"stdoutHtml"` // HTML-escaped stdout
	StderrHTML string `json:"stderrHtml"` // HTML-escaped stderr
	LineCount  int    `json:"lineCount"`  // number of non-empty lines in stdout
	IsError    bool   `json:"isError"`    // true when the command failed
	Truncated  bool   `json:"truncated"`  // true when output was truncated
}

// readAugment holds syntax-highlighted file content.
type readAugment struct {
	ContentHTML string `json:"contentHtml"` // syntax-highlighted HTML
	LineCount   int    `json:"lineCount"`   // number of non-empty lines
	Language    string `json:"language"`     // detected language name (e.g. "Go", "Python")
}

// computeEditAugment computes a character-level diff between oldText and
// newText, returning HTML with <span class="diff-add"> and
// <span class="diff-del"> wrappers around changed regions.
func computeEditAugment(oldText, newText, filename string) (*editAugment, error) {
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(oldText, newText, true)
	diffs = dmp.DiffCleanupSemantic(diffs)

	var b strings.Builder
	var additions, deletions int

	for _, d := range diffs {
		escaped := escapeHTML(d.Text)
		switch d.Type {
		case diffmatchpatch.DiffInsert:
			b.WriteString(`<span class="diff-add">`)
			b.WriteString(escaped)
			b.WriteString(`</span>`)
			additions += len(d.Text)
		case diffmatchpatch.DiffDelete:
			b.WriteString(`<span class="diff-del">`)
			b.WriteString(escaped)
			b.WriteString(`</span>`)
			deletions += len(d.Text)
		case diffmatchpatch.DiffEqual:
			b.WriteString(escaped)
		}
	}

	return &editAugment{
		DiffHTML:  b.String(),
		Additions: additions,
		Deletions: deletions,
	}, nil
}

// computeBashAugment creates a bashAugment from command output. It counts
// non-empty lines in stdout and marks the result as an error when the exit
// code is non-zero or stderr is non-empty.
func computeBashAugment(stdout, stderr string, exitCode int) *bashAugment {
	lineCount := countNonEmptyLines(stdout)

	return &bashAugment{
		StdoutHTML: escapeHTML(stdout),
		StderrHTML: escapeHTML(stderr),
		LineCount:  lineCount,
		IsError:    exitCode != 0 || stderr != "",
		Truncated:  false,
	}
}

// computeReadAugment syntax-highlights the file content and returns metadata.
func computeReadAugment(content, filename string) (*readAugment, error) {
	lang := highlight.DetectLanguage(filename)

	highlighted, err := highlight.Code(content, lang)
	if err != nil {
		return nil, err
	}

	lineCount := countNonEmptyLines(content)

	return &readAugment{
		ContentHTML: highlighted,
		LineCount:   lineCount,
		Language:    lang,
	}, nil
}

// escapeHTML replaces &, <, >, and " with their HTML entity equivalents.
func escapeHTML(s string) string {
	// Order matters: & must be replaced first to avoid double-escaping.
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// countNonEmptyLines returns the number of non-empty lines in s.
func countNonEmptyLines(s string) int {
	if s == "" {
		return 0
	}
	count := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
