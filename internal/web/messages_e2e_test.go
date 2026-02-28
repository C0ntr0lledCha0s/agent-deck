//go:build !windows

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTestJSONL creates a JSONL conversation file with the given entries in
// the directory structure expected by the messages handler:
//
//	<baseDir>/<encodedProjectPath>/<filename>.jsonl
//
// Returns the project path that should be set on the session.
func writeTestJSONL(t *testing.T, baseDir, projectPath string, entries []map[string]any) {
	t.Helper()
	encoded := encodeProjectPath(projectPath)
	dir := filepath.Join(baseDir, encoded)
	require.NoError(t, os.MkdirAll(dir, 0o755))

	var lines []string
	for _, e := range entries {
		b, err := json.Marshal(e)
		require.NoError(t, err)
		lines = append(lines, string(b))
	}

	path := filepath.Join(dir, "conversation.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
}

// newServerWithMessages creates a test server wired with a fake session that
// has a ProjectPath pointing to synthetic JSONL data.
func newServerWithMessages(t *testing.T, sessionID, projectPath, claudeProjectsDir string) *Server {
	t.Helper()
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Profile:    "test-profile",
	})
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{
			Profile: "test-profile",
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID:          sessionID,
						Title:       "test-session",
						Tool:        "claude",
						Status:      "running",
						ProjectPath: projectPath,
					},
				},
			},
		},
	}
	srv.claudeProjectsDir = claudeProjectsDir
	return srv
}

// TestMessagesE2E_ConversationRendering exercises the messages API with
// synthetic JSONL conversation data: user+assistant messages, tool use
// entries, empty conversations, and error cases.
func TestMessagesE2E_ConversationRendering(t *testing.T) {
	claudeDir := t.TempDir()
	projectPath := "/home/testuser/myproject"

	// Create a realistic conversation: user asks a question, assistant
	// responds with text, then uses a tool (Bash), then summarizes.
	entries := []map[string]any{
		{
			"uuid":       "msg-001",
			"parentUuid": "",
			"timestamp":  "2026-02-28T10:00:00Z",
			"type":       "human",
			"message": map[string]any{
				"role":    "user",
				"content": "Write a hello world function in Python",
			},
		},
		{
			"uuid":       "msg-002",
			"parentUuid": "msg-001",
			"timestamp":  "2026-02-28T10:00:05Z",
			"type":       "assistant",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "text", "text": "Here is a hello world function:"},
				},
			},
		},
		{
			"uuid":       "msg-003",
			"parentUuid": "msg-002",
			"timestamp":  "2026-02-28T10:00:10Z",
			"type":       "assistant",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "text", "text": "I've created the function. Let me test it."},
				},
			},
		},
		{
			"uuid":       "msg-004",
			"parentUuid": "msg-003",
			"timestamp":  "2026-02-28T10:00:15Z",
			"type":       "human",
			"message": map[string]any{
				"role":    "user",
				"content": "Looks good, thanks!",
			},
		},
	}

	writeTestJSONL(t, claudeDir, projectPath, entries)
	srv := newServerWithMessages(t, "sess-msg-001", projectPath, claudeDir)
	handler := srv.Handler()

	// ── Step 1: Fetch messages — returns full conversation ──
	t.Run("returns_conversation_messages", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/messages/sess-msg-001", nil))
		require.Equal(t, http.StatusOK, rr.Code)

		var resp messagesResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))

		assert.Equal(t, "sess-msg-001", resp.SessionID)
		require.Len(t, resp.Messages, 4, "should return all 4 messages in active branch")
		assert.Equal(t, 4, resp.DAGInfo.TotalNodes)

		// Verify ordering: root to tip.
		assert.Equal(t, "msg-001", resp.Messages[0].UUID)
		assert.Equal(t, "msg-004", resp.Messages[3].UUID)

		// Verify user message content.
		assert.Equal(t, "user", resp.Messages[0].Role)
		assert.Equal(t, "Write a hello world function in Python", resp.Messages[0].Content)

		// Verify assistant message content (array content blocks).
		assert.Equal(t, "assistant", resp.Messages[1].Role)
		assert.Equal(t, "Here is a hello world function:", resp.Messages[1].Content)

		// Verify parent chain.
		assert.Equal(t, "", resp.Messages[0].ParentUUID)
		assert.Equal(t, "msg-001", resp.Messages[1].ParentUUID)
		assert.Equal(t, "msg-002", resp.Messages[2].ParentUUID)
		assert.Equal(t, "msg-003", resp.Messages[3].ParentUUID)
	})

	// ── Step 2: Session not found ──
	t.Run("session_not_found", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/messages/nonexistent", nil))
		assert.Equal(t, http.StatusNotFound, rr.Code)
		assert.Contains(t, rr.Body.String(), `"code":"NOT_FOUND"`)
	})

	// ── Step 3: Missing session ID ──
	t.Run("missing_session_id", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/messages/", nil))
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	// ── Step 4: Method not allowed ──
	t.Run("method_not_allowed", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/messages/sess-msg-001", nil))
		assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	})

	// ── Step 5: Session with no JSONL data returns empty messages ──
	t.Run("no_jsonl_returns_empty", func(t *testing.T) {
		// Create a session pointing to a project with no JSONL directory.
		srvEmpty := newServerWithMessages(t, "sess-empty", "/nonexistent/project", claudeDir)
		rrEmpty := httptest.NewRecorder()
		srvEmpty.Handler().ServeHTTP(rrEmpty, httptest.NewRequest(http.MethodGet, "/api/messages/sess-empty", nil))
		require.Equal(t, http.StatusOK, rrEmpty.Code)

		var resp messagesResponse
		require.NoError(t, json.Unmarshal(rrEmpty.Body.Bytes(), &resp))
		assert.Equal(t, "sess-empty", resp.SessionID)
		assert.Empty(t, resp.Messages)
	})
}

// TestMessagesE2E_BranchedConversation tests DAG branch resolution: when a
// conversation has branches (user edited a message), the API returns the most
// recent branch.
func TestMessagesE2E_BranchedConversation(t *testing.T) {
	claudeDir := t.TempDir()
	projectPath := "/home/testuser/branched"

	// Create a conversation with a branch at msg-002:
	//   msg-001 (user) → msg-002 (assistant, older)
	//                   → msg-003 (assistant, newer — different branch from msg-001)
	entries := []map[string]any{
		{
			"uuid":       "msg-001",
			"parentUuid": "",
			"timestamp":  "2026-02-28T10:00:00Z",
			"type":       "human",
			"message": map[string]any{
				"role":    "user",
				"content": "Explain goroutines",
			},
		},
		{
			"uuid":       "msg-002",
			"parentUuid": "msg-001",
			"timestamp":  "2026-02-28T10:00:05Z",
			"type":       "assistant",
			"message": map[string]any{
				"role":    "assistant",
				"content": "Goroutines are lightweight threads (old answer).",
			},
		},
		{
			"uuid":       "msg-003",
			"parentUuid": "msg-001",
			"timestamp":  "2026-02-28T10:00:10Z",
			"type":       "assistant",
			"message": map[string]any{
				"role":    "assistant",
				"content": "Goroutines are lightweight concurrent functions managed by the Go runtime (new answer).",
			},
		},
	}

	writeTestJSONL(t, claudeDir, projectPath, entries)
	srv := newServerWithMessages(t, "sess-branched", projectPath, claudeDir)
	handler := srv.Handler()

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/messages/sess-branched", nil))
	require.Equal(t, http.StatusOK, rr.Code)

	var resp messagesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))

	// DAG should have 3 total nodes (both branches) but active branch has 2.
	assert.Equal(t, 3, resp.DAGInfo.TotalNodes)
	require.Len(t, resp.Messages, 2, "active branch: root + most recent tip")
	assert.Equal(t, "msg-001", resp.Messages[0].UUID)
	assert.Equal(t, "msg-003", resp.Messages[1].UUID, "should select newer branch tip")
	assert.Contains(t, resp.Messages[1].Content, "concurrent functions managed by the Go runtime")
}

// TestTerminalAndMessagesE2E verifies that a single session can serve both
// live terminal output (via WebSocket PTY) and historical messages (via the
// messages API). Requires tmux.
func TestTerminalAndMessagesE2E(t *testing.T) {
	requireTmuxForWebIntegration(t)

	claudeDir := t.TempDir()
	projectPath := "/home/testuser/dual-view"
	sessionName := fmt.Sprintf("agentdeck_msg_it_%d", time.Now().UnixNano())

	// Create tmux session for live terminal.
	if output, err := exec.Command("tmux", "new-session", "-d", "-s", sessionName).CombinedOutput(); err != nil {
		t.Skipf("tmux new-session unavailable: %v (%s)", err, strings.TrimSpace(string(output)))
	}
	defer func() { _ = exec.Command("tmux", "kill-session", "-t", sessionName).Run() }()

	// Create synthetic JSONL for the messages view.
	entries := []map[string]any{
		{
			"uuid":       "dual-001",
			"parentUuid": "",
			"timestamp":  "2026-02-28T12:00:00Z",
			"type":       "human",
			"message": map[string]any{
				"role":    "user",
				"content": "Run the test suite",
			},
		},
		{
			"uuid":       "dual-002",
			"parentUuid": "dual-001",
			"timestamp":  "2026-02-28T12:00:05Z",
			"type":       "assistant",
			"message": map[string]any{
				"role":    "assistant",
				"content": "Running tests now...",
			},
		},
	}
	writeTestJSONL(t, claudeDir, projectPath, entries)

	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Profile:    "test-profile",
	})
	srv.claudeProjectsDir = claudeDir
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{
			Profile: "test-profile",
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID:          "sess-dual",
						Title:       "dual-view-session",
						Tool:        "claude",
						Status:      "running",
						TmuxSession: sessionName,
						ProjectPath: projectPath,
					},
				},
			},
		},
	}

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	// ── Part 1: Terminal view — WebSocket PTY streaming ──
	t.Run("terminal_streams_pty_output", func(t *testing.T) {
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL(testServer.URL, "/ws/session/sess-dual"), nil)
		if err != nil {
			if resp != nil {
				t.Fatalf("dial failed with status %d: %v", resp.StatusCode, err)
			}
			t.Fatalf("dial failed: %v", err)
		}
		defer func() {
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				time.Now().Add(200*time.Millisecond),
			)
			_ = conn.Close()
		}()

		waitForStatusOrSkipOnAttachFailure(t, conn, "terminal_attached")

		// Send a unique marker through the terminal.
		marker := fmt.Sprintf("ADMSG_E2E_%d", time.Now().UnixNano())
		require.NoError(t, conn.WriteJSON(wsClientMessage{
			Type: "input",
			Data: fmt.Sprintf("printf '%s\\n'\r", marker),
		}))

		received, err := readBinaryUntilContains(conn, marker, 8*time.Second)
		require.NoError(t, err, "marker should appear in terminal stream; excerpt=%q", trimForError(received, 350))
	})

	// ── Part 2: Messages view — historical conversation ──
	t.Run("messages_returns_conversation", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/messages/sess-dual", nil)
		testServer.Config.Handler.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		var resp messagesResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))

		assert.Equal(t, "sess-dual", resp.SessionID)
		require.Len(t, resp.Messages, 2)
		assert.Equal(t, "user", resp.Messages[0].Role)
		assert.Equal(t, "Run the test suite", resp.Messages[0].Content)
		assert.Equal(t, "assistant", resp.Messages[1].Role)
		assert.Equal(t, "Running tests now...", resp.Messages[1].Content)
	})
}

// TestHandleMessagesHTML_Integration tests the /api/messages/{id}/html endpoint
// end-to-end with synthetic JSONL data.
func TestHandleMessagesHTML_Integration(t *testing.T) {
	claudeDir := t.TempDir()
	projectPath := "/home/testuser/myproject"

	entries := []map[string]any{
		{
			"uuid": "a", "parentUuid": "", "type": "human",
			"message":   map[string]any{"role": "user", "content": "hello"},
			"timestamp": "2025-01-01T00:00:00Z",
		},
		{
			"uuid": "b", "parentUuid": "a", "type": "assistant",
			"message": map[string]any{"role": "assistant", "content": []map[string]any{
				{"type": "text", "text": "hi there"},
			}},
			"timestamp": "2025-01-01T00:00:01Z",
		},
	}
	writeTestJSONL(t, claudeDir, projectPath, entries)

	srv := newServerWithMessages(t, "sess-1", projectPath, claudeDir)
	req := httptest.NewRequest("GET", "/api/messages/sess-1/html", nil)
	rec := httptest.NewRecorder()
	srv.handleSessionMessages(rec, req)

	require.Equal(t, 200, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	body := rec.Body.String()
	assert.Contains(t, body, "user-prompt-container")
	assert.Contains(t, body, "hello")
	assert.Contains(t, body, "assistant-turn")
	assert.Contains(t, body, "hi there")
}
