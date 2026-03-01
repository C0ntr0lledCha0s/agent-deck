package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/dag"
)

// toolCallInfo represents a single tool invocation and its result,
// sent as part of an assistant message's Tools array.
type toolCallInfo struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	IsError bool            `json:"isError,omitempty"`
	Augment json.RawMessage `json:"augment,omitempty"`
}

// augmentedMessage is the wire format for a single conversation message
// returned by the /api/messages/{id} endpoint. It wraps dag.SessionMessage
// with tool call data grouped into the Tools array for assistant messages.
type augmentedMessage struct {
	UUID       string          `json:"uuid"`
	ParentUUID string          `json:"parentUuid"`
	Type       string          `json:"type"`
	Role       string          `json:"role"`
	Timestamp  time.Time       `json:"timestamp"`
	Content    string          `json:"content"`
	Tools      []toolCallInfo  `json:"tools,omitempty"`
	// Legacy single-tool fields (kept for backward compatibility).
	ToolName   string          `json:"toolName,omitempty"`
	ToolInput  json.RawMessage `json:"toolInput,omitempty"`
	ToolResult json.RawMessage `json:"toolResult,omitempty"`
	Augment    json.RawMessage `json:"augment,omitempty"`
}

// messagesResponse is the JSON response for /api/messages/{id}.
type messagesResponse struct {
	SessionID string              `json:"sessionId"`
	Messages  []augmentedMessage  `json:"messages"`
	DAGInfo   messagesDAGInfo     `json:"dagInfo"`
}

// messagesDAGInfo contains DAG metadata about the conversation.
type messagesDAGInfo struct {
	TotalNodes int `json:"totalNodes"`
}

// handleSessionMessages serves GET /api/messages/{sessionID}.
// It locates the Claude Code JSONL conversation directory for the session's
// project path, reads the active branch via the dag package, and returns
// the messages as JSON.
func (s *Server) handleSessionMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	const prefix = "/api/messages/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "route not found")
		return
	}
	sessionID := strings.TrimPrefix(r.URL.Path, prefix)
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "session id is required")
		return
	}

	// Look up the session to get its ProjectPath.
	snapshot, err := s.menuData.LoadMenuSnapshot()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load session data")
		return
	}

	var projectPath string
	var tmuxSession string
	found := false
	for _, item := range snapshot.Items {
		if item.Type != MenuItemTypeSession || item.Session == nil {
			continue
		}
		if item.Session.ID != sessionID {
			continue
		}
		projectPath = item.Session.ProjectPath
		tmuxSession = item.Session.TmuxSession
		found = true
		break
	}

	if !found {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "session not found")
		return
	}

	if projectPath == "" {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "session has no project path")
		return
	}

	// Locate the Claude Code session directory for this project.
	sessionDir := s.findClaudeSessionDir(projectPath)

	// Fallback: if the configured projectPath doesn't match a Claude projects
	// directory, query the tmux pane's actual working directory. This handles
	// hub-launched sessions where the configured path differs from where Claude
	// Code was actually started.
	if sessionDir == "" && tmuxSession != "" {
		if actualPath := tmuxPaneCurrentPath(r.Context(), tmuxSession); actualPath != "" && actualPath != projectPath {
			sessionDir = s.findClaudeSessionDir(actualPath)
		}
	}

	if sessionDir == "" {
		writeJSON(w, http.StatusOK, messagesResponse{
			SessionID: sessionID,
			Messages:  []augmentedMessage{},
			DAGInfo:   messagesDAGInfo{},
		})
		return
	}

	// Read the active conversation branch.
	result, err := dag.ReadSessionFull(sessionDir)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read conversation")
		return
	}

	if result == nil || len(result.Messages) == 0 {
		totalNodes := 0
		if result != nil {
			totalNodes = result.TotalNodes
		}
		writeJSON(w, http.StatusOK, messagesResponse{
			SessionID: sessionID,
			Messages:  []augmentedMessage{},
			DAGInfo:   messagesDAGInfo{TotalNodes: totalNodes},
		})
		return
	}

	// Build augmented messages with tool call data.
	msgs := buildAugmentedMessages(result.Messages)

	writeJSON(w, http.StatusOK, messagesResponse{
		SessionID: sessionID,
		Messages:  msgs,
		DAGInfo:   messagesDAGInfo{TotalNodes: result.TotalNodes},
	})
}

// findClaudeSessionDir locates the Claude Code projects directory for the
// given project path. Claude Code stores conversations in:
//
//	~/.claude/projects/-<encoded-path>/
//
// where the encoding replaces "/" with "-" and prepends "-".
// For example: /home/user/myproject -> -home-user-myproject
//
// Returns the directory path if found, or empty string if not found.
// Note: no fallback scan is performed because the encoding is lossy
// (dashes in path component names are indistinguishable from separators),
// so decoding would produce false matches for paths containing hyphens.
//
// If s.claudeProjectsDir is set, it is used as the base directory instead
// of the default ~/.claude/projects.
func (s *Server) findClaudeSessionDir(projectPath string) string {
	baseDir := s.claudeProjectsDir
	if baseDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		baseDir = filepath.Join(homeDir, ".claude", "projects")
	}

	encoded := encodeProjectPath(projectPath)
	candidate := filepath.Join(baseDir, encoded)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}

	return ""
}

// encodeProjectPath converts a filesystem path to Claude Code's dash-separated
// directory encoding. The absolute path has its leading "/" removed, all
// remaining "/" and "." replaced with "-", and is prepended with "-".
//
// This encoding is lossy: paths containing literal hyphens in directory names
// produce the same encoding as paths with "/" separators in those positions.
// For example, both /home/user/my-project and /home/user/my/project encode to
// -home-user-my-project. This matches Claude Code's own encoding, so the
// fast-path os.Stat lookup will find the correct directory.
//
// Example: /home/user/myproject       -> -home-user-myproject
// Example: /home/user/.worktrees/feat -> -home-user--worktrees-feat
func encodeProjectPath(path string) string {
	path = filepath.Clean(path)
	trimmed := strings.TrimPrefix(path, "/")
	encoded := strings.ReplaceAll(trimmed, "/", "-")
	encoded = strings.ReplaceAll(encoded, ".", "-")
	return "-" + encoded
}

// buildAugmentedMessages converts dag.SessionMessages into the wire format,
// matching tool_use blocks in assistant messages with their tool_result blocks
// in subsequent user messages. User messages that contain only tool_result
// blocks (no text) are suppressed from output to avoid empty rows.
func buildAugmentedMessages(dagMsgs []dag.SessionMessage) []augmentedMessage {
	// Index tool results by tool_use_id from all user messages.
	resultMap := make(map[string]dag.ToolResultBlock)
	for _, m := range dagMsgs {
		for _, tr := range m.ToolResultBlocks {
			resultMap[tr.ToolUseID] = tr
		}
	}

	msgs := make([]augmentedMessage, 0, len(dagMsgs))
	for _, m := range dagMsgs {
		// Skip user messages that are purely tool_result containers (no text).
		if m.Role == "user" && m.Content == "" && len(m.ToolResultBlocks) > 0 {
			continue
		}

		am := augmentedMessage{
			UUID:       m.UUID,
			ParentUUID: m.ParentUUID,
			Type:       m.Type,
			Role:       m.Role,
			Timestamp:  m.Timestamp,
			Content:    m.Content,
		}

		// For assistant messages with tool_use blocks, build the Tools array.
		if len(m.ToolUseBlocks) > 0 {
			tools := make([]toolCallInfo, 0, len(m.ToolUseBlocks))
			for _, tu := range m.ToolUseBlocks {
				tc := toolCallInfo{
					ID:    tu.ID,
					Name:  tu.Name,
					Input: tu.Input,
				}

				// Match with result.
				if tr, ok := resultMap[tu.ID]; ok {
					if tr.Content != "" {
						resultJSON, _ := json.Marshal(tr.Content)
						tc.Result = resultJSON
					}
					tc.IsError = tr.IsError
				}

				// Compute augments for known tool types.
				tc.Augment = computeToolAugment(tc.Name, tc.Input, tc.Result, tc.IsError)

				tools = append(tools, tc)
			}
			am.Tools = tools
		}

		msgs = append(msgs, am)
	}

	return msgs
}

// computeToolAugment computes server-side augmentation for known tool types,
// returning the JSON-encoded augment or nil if not applicable.
func computeToolAugment(name string, input, result json.RawMessage, isError bool) json.RawMessage {
	switch name {
	case "Bash":
		return computeBashToolAugment(input, result, isError)
	case "Read":
		return computeReadToolAugment(input, result)
	case "Edit":
		return computeEditToolAugment(input, result)
	default:
		return nil
	}
}

// computeBashToolAugment computes augmentation for Bash tool calls.
// isError reflects whether the tool_result was marked as an error in the JSONL.
func computeBashToolAugment(input, result json.RawMessage, isError bool) json.RawMessage {
	var stdout string
	if result != nil {
		_ = json.Unmarshal(result, &stdout)
	}
	if stdout == "" && !isError {
		return nil
	}
	exitCode := 0
	if isError {
		exitCode = 1
	}
	aug := computeBashAugment(stdout, "", exitCode)
	b, err := json.Marshal(aug)
	if err != nil {
		slog.Debug("failed to marshal bash augment", "error", err)
		return nil
	}
	return b
}

// computeReadToolAugment computes augmentation for Read tool calls.
func computeReadToolAugment(input, result json.RawMessage) json.RawMessage {
	var content string
	if result != nil {
		_ = json.Unmarshal(result, &content)
	}
	if content == "" {
		return nil
	}

	var inp struct {
		FilePath string `json:"file_path"`
	}
	if input != nil {
		_ = json.Unmarshal(input, &inp)
	}

	aug, err := computeReadAugment(content, inp.FilePath)
	if err != nil {
		return nil
	}
	b, err := json.Marshal(aug)
	if err != nil {
		slog.Debug("failed to marshal read augment", "error", err)
		return nil
	}
	return b
}

// computeEditToolAugment computes augmentation for Edit tool calls.
func computeEditToolAugment(input, result json.RawMessage) json.RawMessage {
	var inp struct {
		FilePath  string `json:"file_path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if input != nil {
		_ = json.Unmarshal(input, &inp)
	}
	if inp.OldString == "" && inp.NewString == "" {
		return nil
	}

	aug, err := computeEditAugment(inp.OldString, inp.NewString, inp.FilePath)
	if err != nil {
		return nil
	}
	b, err := json.Marshal(aug)
	if err != nil {
		slog.Debug("failed to marshal edit augment", "error", err)
		return nil
	}
	return b
}

// tmuxPaneCurrentPath returns the working directory of a tmux session's active
// pane, or empty string if the session doesn't exist or the query fails.
// The context is used to bound the exec call to the HTTP request lifetime.
func tmuxPaneCurrentPath(ctx context.Context, tmuxSession string) string {
	if tmuxSession == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-t", tmuxSession, "-p", "#{pane_current_path}")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
