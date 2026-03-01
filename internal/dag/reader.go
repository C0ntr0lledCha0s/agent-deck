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

// ToolUseBlock represents a tool_use content block from an assistant message.
type ToolUseBlock struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

// ToolResultBlock represents a tool_result content block from a user message.
type ToolResultBlock struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// SessionMessage represents a parsed conversation message from the active branch.
type SessionMessage struct {
	UUID             string
	ParentUUID       string
	Type             string
	Role             string
	Content          string
	ToolUseBlocks    []ToolUseBlock
	ToolResultBlocks []ToolResultBlock
	Message          json.RawMessage
	Timestamp        time.Time
	LineIndex        int
}

// SessionReadResult contains the active branch messages plus DAG metadata.
type SessionReadResult struct {
	Messages   []SessionMessage
	TotalNodes int
}

// ReadSession reads the most recent JSONL conversation file from sessionDir,
// builds the DAG, resolves the active branch, and returns the messages in order.
// Files named agent-*.jsonl are skipped (subagent conversations).
func ReadSession(sessionDir string) ([]SessionMessage, error) {
	result, err := ReadSessionFull(sessionDir)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.Messages, nil
}

// ReadSessionFull is like ReadSession but also returns DAG metadata such as
// the total number of nodes across all branches.
func ReadSessionFull(sessionDir string) (*SessionReadResult, error) {
	selected, err := selectJSONLFile(sessionDir)
	if err != nil {
		return nil, err
	}
	if selected == "" {
		return nil, nil
	}

	// Parse each line as Entry.
	entries, err := parseJSONL(selected)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(selected), err)
	}

	if len(entries) == 0 {
		return nil, nil
	}

	// Build DAG to get active branch.
	dagResult, err := BuildDAG(entries)
	if err != nil {
		return nil, fmt.Errorf("build DAG: %w", err)
	}

	if len(dagResult.ActiveBranch) == 0 {
		return &SessionReadResult{TotalNodes: dagResult.TotalNodes}, nil
	}

	// Convert to SessionMessages.
	msgs := make([]SessionMessage, 0, len(dagResult.ActiveBranch))
	for _, node := range dagResult.ActiveBranch {
		e := node.Entry
		role, content, toolUses, toolResults := extractRoleContent(e.Message)
		msgs = append(msgs, SessionMessage{
			UUID:             e.UUID,
			ParentUUID:       e.ParentUUID,
			Type:             e.Type,
			Role:             role,
			Content:          content,
			ToolUseBlocks:    toolUses,
			ToolResultBlocks: toolResults,
			Message:          e.Message,
			Timestamp:        e.Timestamp,
			LineIndex:        e.LineIndex,
		})
	}

	return &SessionReadResult{
		Messages:   msgs,
		TotalNodes: dagResult.TotalNodes,
	}, nil
}

// selectJSONLFile finds the most recently modified *.jsonl file in sessionDir,
// skipping agent-*.jsonl subagent files.
func selectJSONLFile(sessionDir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(sessionDir, "*.jsonl"))
	if err != nil {
		return "", fmt.Errorf("glob jsonl files: %w", err)
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
		return "", nil
	}

	// Stat all files once before sorting to avoid repeated os.Stat calls
	// inside the comparator and to ensure a consistent sort order.
	type candidate struct {
		path  string
		mtime time.Time
	}
	var cs []candidate
	for _, path := range candidates {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		cs = append(cs, candidate{path: path, mtime: info.ModTime()})
	}
	if len(cs) == 0 {
		return "", nil
	}

	sort.Slice(cs, func(i, j int) bool {
		return cs[i].mtime.After(cs[j].mtime)
	})

	return cs[0].path, nil
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

// extractRoleContent extracts role, text content, and tool blocks from a
// message JSON blob.
// The message format is: {"role": "...", "content": "..." or [...]}
// Content arrays may contain text, tool_use, and tool_result blocks.
func extractRoleContent(msg json.RawMessage) (role, content string, toolUses []ToolUseBlock, toolResults []ToolResultBlock) {
	if len(msg) == 0 {
		return "", "", nil, nil
	}

	var parsed struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(msg, &parsed); err != nil {
		return "", "", nil, nil
	}
	role = parsed.Role

	if len(parsed.Content) == 0 {
		return role, "", nil, nil
	}

	// Content can be a plain string.
	var s string
	if err := json.Unmarshal(parsed.Content, &s); err == nil {
		return role, s, nil, nil
	}

	// Content can be an array of content blocks (text, tool_use, tool_result).
	var blocks []json.RawMessage
	if err := json.Unmarshal(parsed.Content, &blocks); err != nil {
		return role, "", nil, nil
	}

	var texts []string
	for _, raw := range blocks {
		var base struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &base); err != nil {
			continue
		}

		switch base.Type {
		case "text":
			var tb struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(raw, &tb) == nil && tb.Text != "" {
				texts = append(texts, tb.Text)
			}

		case "tool_use":
			var tu struct {
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			}
			if json.Unmarshal(raw, &tu) == nil && tu.Name != "" {
				toolUses = append(toolUses, ToolUseBlock{
					ID:    tu.ID,
					Name:  tu.Name,
					Input: tu.Input,
				})
			}

		case "tool_result":
			var tr struct {
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
				IsError   bool            `json:"is_error"`
			}
			if json.Unmarshal(raw, &tr) == nil {
				resultContent := extractToolResultContent(tr.Content)
				toolResults = append(toolResults, ToolResultBlock{
					ToolUseID: tr.ToolUseID,
					Content:   resultContent,
					IsError:   tr.IsError,
				})
			}
		}
	}

	return role, strings.Join(texts, "\n"), toolUses, toolResults
}

// extractToolResultContent extracts text from a tool_result content field,
// which can be a plain string, a content block array, or null.
func extractToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try plain string first.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	// Try array of content blocks (e.g. [{"type":"text","text":"..."}]).
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var texts []string
		for _, b := range blocks {
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		}
		return strings.Join(texts, "\n")
	}

	return ""
}
