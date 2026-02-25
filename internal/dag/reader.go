package dag

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxLineSize is the maximum line buffer size for reading JSONL files (10 MB).
const maxLineSize = 10 * 1024 * 1024

// SessionMessage represents a parsed conversation message from the active branch.
type SessionMessage struct {
	UUID       string
	ParentUUID string
	Type       string
	Role       string
	Content    string
	Message    json.RawMessage
	Timestamp  time.Time
	LineIndex  int
}

// ReadSession reads the most recent JSONL conversation file from sessionDir,
// builds the DAG, resolves the active branch, and returns the messages in order.
// Files named agent-*.jsonl are skipped (subagent conversations).
func ReadSession(sessionDir string) ([]SessionMessage, error) {
	// Glob *.jsonl, filter out agent-*.jsonl.
	matches, err := filepath.Glob(filepath.Join(sessionDir, "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("glob jsonl files: %w", err)
	}

	var candidates []string
	for _, m := range matches {
		base := filepath.Base(m)
		if strings.HasPrefix(base, "agent-") {
			continue
		}
		candidates = append(candidates, m)
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Sort by mtime descending, pick most recent.
	sort.Slice(candidates, func(i, j int) bool {
		si, _ := os.Stat(candidates[i])
		sj, _ := os.Stat(candidates[j])
		if si == nil || sj == nil {
			return false
		}
		return si.ModTime().After(sj.ModTime())
	})

	selected := candidates[0]

	// Parse each line as Entry.
	entries, err := parseJSONL(selected)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(selected), err)
	}

	if len(entries) == 0 {
		return nil, nil
	}

	// Build DAG to get active branch.
	result, err := BuildDAG(entries)
	if err != nil {
		return nil, fmt.Errorf("build DAG: %w", err)
	}

	if len(result.ActiveBranch) == 0 {
		return nil, nil
	}

	// Convert to SessionMessages.
	msgs := make([]SessionMessage, 0, len(result.ActiveBranch))
	for _, node := range result.ActiveBranch {
		e := node.Entry
		role, content := extractRoleContent(e.Message)
		msgs = append(msgs, SessionMessage{
			UUID:       e.UUID,
			ParentUUID: e.ParentUUID,
			Type:       e.Type,
			Role:       role,
			Content:    content,
			Message:    e.Message,
			Timestamp:  e.Timestamp,
			LineIndex:  e.LineIndex,
		})
	}

	return msgs, nil
}

// parseJSONL reads a JSONL file and returns parsed entries, skipping malformed lines.
func parseJSONL(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, maxLineSize), maxLineSize)

	lineIndex := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			lineIndex++
			continue
		}

		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			// Skip malformed lines.
			lineIndex++
			continue
		}

		e.Raw = make(json.RawMessage, len(line))
		copy(e.Raw, line)
		e.LineIndex = lineIndex
		entries = append(entries, e)
		lineIndex++
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	return entries, nil
}

// extractRoleContent extracts role and text content from a message JSON blob.
// The message format is: {"role": "...", "content": "..." or [...]}
func extractRoleContent(msg json.RawMessage) (role, content string) {
	if len(msg) == 0 {
		return "", ""
	}

	var parsed struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(msg, &parsed); err != nil {
		return "", ""
	}
	role = parsed.Role

	if len(parsed.Content) == 0 {
		return role, ""
	}

	// Content can be a plain string.
	var s string
	if err := json.Unmarshal(parsed.Content, &s); err == nil {
		return role, s
	}

	// Content can be an array of content blocks.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(parsed.Content, &blocks); err == nil {
		var texts []string
		for _, b := range blocks {
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		}
		return role, strings.Join(texts, "\n")
	}

	return role, ""
}
