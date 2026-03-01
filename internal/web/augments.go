// Package web — augments.go provides server-side HTML enrichments for tool
// results displayed in the web dashboard. It computes diffs, formats bash
// output, and syntax-highlights file contents before sending them to clients.
package web

import (
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/highlight"
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

// computeEditAugment computes a line-level diff between oldText and newText,
// returning HTML with line numbers and add/del styling (similar to GitHub diff view).
// It uses character-level diffing first, then groups the results by line to produce
// a line-oriented display with proper line numbers on each side.
func computeEditAugment(oldText, newText, filename string) (*editAugment, error) {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	// Compute a simple LCS-based line diff.
	ops := diffLines(oldLines, newLines)

	var buf strings.Builder
	var additions, deletions int
	oldLineNo := 1
	newLineNo := 1

	buf.WriteString(`<div class="diff-table">`)

	for _, op := range ops {
		switch op.kind {
		case diffEqual:
			buf.WriteString(`<div class="diff-line diff-ctx">`)
			writeLineNo(&buf, oldLineNo, newLineNo)
			buf.WriteString(`<span class="diff-code"> `)
			buf.WriteString(escapeHTML(op.text))
			buf.WriteString("</span></div>")
			oldLineNo++
			newLineNo++

		case diffRemove:
			buf.WriteString(`<div class="diff-line diff-del-line">`)
			writeLineNo(&buf, oldLineNo, 0)
			buf.WriteString(`<span class="diff-code">-`)
			buf.WriteString(escapeHTML(op.text))
			buf.WriteString("</span></div>")
			oldLineNo++
			deletions++

		case diffInsert:
			buf.WriteString(`<div class="diff-line diff-add-line">`)
			writeLineNo(&buf, 0, newLineNo)
			buf.WriteString(`<span class="diff-code">+`)
			buf.WriteString(escapeHTML(op.text))
			buf.WriteString("</span></div>")
			newLineNo++
			additions++
		}
	}

	buf.WriteString(`</div>`)

	return &editAugment{
		DiffHTML:  buf.String(),
		Additions: additions,
		Deletions: deletions,
	}, nil
}

// diffOpKind represents the type of a line diff operation.
type diffOpKind int

const (
	diffEqual  diffOpKind = iota
	diffRemove
	diffInsert
)

// diffOp represents a single line in a diff output.
type diffOp struct {
	kind diffOpKind
	text string
}

// diffLines computes a line-level diff between old and new lines using a
// simple LCS (Longest Common Subsequence) algorithm. Returns a sequence of
// diffOp values representing equal, removed, and inserted lines.
func diffLines(oldLines, newLines []string) []diffOp {
	m := len(oldLines)
	n := len(newLines)

	// Build LCS length table.
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	// Backtrack to build diff ops.
	var ops []diffOp
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			ops = append(ops, diffOp{kind: diffEqual, text: oldLines[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			ops = append(ops, diffOp{kind: diffInsert, text: newLines[j-1]})
			j--
		} else {
			ops = append(ops, diffOp{kind: diffRemove, text: oldLines[i-1]})
			i--
		}
	}

	// Reverse (we built it bottom-up).
	for left, right := 0, len(ops)-1; left < right; left, right = left+1, right-1 {
		ops[left], ops[right] = ops[right], ops[left]
	}
	return ops
}

// splitLines splits text into lines, handling trailing newlines properly.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	// Remove trailing empty string from a trailing newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// writeLineNo writes the line number columns for a diff line.
// A value of 0 means the column should be blank (the line doesn't exist on that side).
func writeLineNo(b *strings.Builder, oldNo, newNo int) {
	b.WriteString(`<span class="diff-ln">`)
	if oldNo > 0 {
		b.WriteString(strings.Repeat(" ", 4-len(itoa(oldNo))))
		b.WriteString(itoa(oldNo))
	} else {
		b.WriteString("    ")
	}
	b.WriteString(`</span><span class="diff-ln">`)
	if newNo > 0 {
		b.WriteString(strings.Repeat(" ", 4-len(itoa(newNo))))
		b.WriteString(itoa(newNo))
	} else {
		b.WriteString("    ")
	}
	b.WriteString(`</span>`)
}

// itoa converts an int to a string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
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
