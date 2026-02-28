package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/dag"
)

// augmentedMessage is the wire format for a single conversation message
// returned by the /api/messages/{id} endpoint. It wraps dag.SessionMessage
// with additional fields for tool augmentation (populated in a future step).
type augmentedMessage struct {
	UUID       string          `json:"uuid"`
	ParentUUID string          `json:"parentUuid"`
	Type       string          `json:"type"`
	Role       string          `json:"role"`
	Timestamp  time.Time       `json:"timestamp"`
	Content    string          `json:"content"`
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
	found := false
	for _, item := range snapshot.Items {
		if item.Type != MenuItemTypeSession || item.Session == nil {
			continue
		}
		if item.Session.ID != sessionID {
			continue
		}
		projectPath = item.Session.ProjectPath
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

	// Build augmented messages (tool augmentation is a future step).
	msgs := make([]augmentedMessage, 0, len(result.Messages))
	for _, m := range result.Messages {
		msgs = append(msgs, augmentedMessage{
			UUID:       m.UUID,
			ParentUUID: m.ParentUUID,
			Type:       m.Type,
			Role:       m.Role,
			Timestamp:  m.Timestamp,
			Content:    m.Content,
		})
	}

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
// remaining "/" replaced with "-", and is prepended with "-".
//
// This encoding is lossy: paths containing literal hyphens in directory names
// produce the same encoding as paths with "/" separators in those positions.
// For example, both /home/user/my-project and /home/user/my/project encode to
// -home-user-my-project. This matches Claude Code's own encoding, so the
// fast-path os.Stat lookup will find the correct directory.
//
// Example: /home/user/myproject -> -home-user-myproject
func encodeProjectPath(path string) string {
	path = filepath.Clean(path)
	trimmed := strings.TrimPrefix(path, "/")
	return "-" + strings.ReplaceAll(trimmed, "/", "-")
}
