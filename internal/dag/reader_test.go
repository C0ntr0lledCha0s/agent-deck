package dag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadSession_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	msgs, err := ReadSession(dir)
	require.NoError(t, err)
	assert.Nil(t, msgs)
}

func TestReadSession_SimpleConversation(t *testing.T) {
	dir := t.TempDir()

	// Two-line JSONL: user message then assistant reply.
	jsonl := `{"uuid":"a","parentUuid":"","type":"human","message":{"role":"user","content":[{"type":"text","text":"hello"}]},"timestamp":"2025-01-01T00:00:00Z"}
{"uuid":"b","parentUuid":"a","type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi there"}]},"timestamp":"2025-01-01T00:00:01Z"}
`
	err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(jsonl), 0644)
	require.NoError(t, err)

	msgs, err := ReadSession(dir)
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "hello", msgs[0].Content)
	assert.Equal(t, "human", msgs[0].Type)
	assert.Equal(t, "a", msgs[0].UUID)

	assert.Equal(t, "assistant", msgs[1].Role)
	assert.Equal(t, "hi there", msgs[1].Content)
	assert.Equal(t, "assistant", msgs[1].Type)
	assert.Equal(t, "b", msgs[1].UUID)
}

func TestReadSession_SkipsAgentFiles(t *testing.T) {
	dir := t.TempDir()

	// Main session file.
	sessionData := `{"uuid":"m1","parentUuid":"","type":"human","message":{"role":"user","content":"main msg"},"timestamp":"2025-01-01T00:00:00Z"}
`
	err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(sessionData), 0644)
	require.NoError(t, err)

	// Subagent file — should be skipped.
	agentData := `{"uuid":"s1","parentUuid":"","type":"human","message":{"role":"user","content":"agent msg"},"timestamp":"2025-01-01T00:00:00Z"}
`
	err = os.WriteFile(filepath.Join(dir, "agent-sub.jsonl"), []byte(agentData), 0644)
	require.NoError(t, err)

	msgs, err := ReadSession(dir)
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	assert.Equal(t, "m1", msgs[0].UUID)
	assert.Equal(t, "main msg", msgs[0].Content)
}

func TestReadSession_ToolUseBlocks(t *testing.T) {
	dir := t.TempDir()

	// Assistant message with text + tool_use, followed by user message with tool_result.
	jsonl := `{"uuid":"a","parentUuid":"","type":"human","message":{"role":"user","content":"check the file"},"timestamp":"2025-01-01T00:00:00Z"}
{"uuid":"b","parentUuid":"a","type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Let me read that file."},{"type":"tool_use","id":"toolu_123","name":"Read","input":{"file_path":"/foo/bar.go"}}]},"timestamp":"2025-01-01T00:00:01Z"}
{"uuid":"c","parentUuid":"b","type":"human","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_123","content":"package main\nfunc main() {}"}]},"timestamp":"2025-01-01T00:00:02Z"}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(jsonl), 0644))

	msgs, err := ReadSession(dir)
	require.NoError(t, err)
	require.Len(t, msgs, 3)

	// First message: plain user text, no tool blocks.
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "check the file", msgs[0].Content)
	assert.Empty(t, msgs[0].ToolUseBlocks)
	assert.Empty(t, msgs[0].ToolResultBlocks)

	// Second message: assistant with text + tool_use.
	assert.Equal(t, "assistant", msgs[1].Role)
	assert.Equal(t, "Let me read that file.", msgs[1].Content)
	require.Len(t, msgs[1].ToolUseBlocks, 1)
	assert.Equal(t, "toolu_123", msgs[1].ToolUseBlocks[0].ID)
	assert.Equal(t, "Read", msgs[1].ToolUseBlocks[0].Name)

	var input map[string]string
	require.NoError(t, json.Unmarshal(msgs[1].ToolUseBlocks[0].Input, &input))
	assert.Equal(t, "/foo/bar.go", input["file_path"])
	assert.Empty(t, msgs[1].ToolResultBlocks)

	// Third message: user with tool_result.
	assert.Equal(t, "user", msgs[2].Role)
	assert.Empty(t, msgs[2].Content)
	assert.Empty(t, msgs[2].ToolUseBlocks)
	require.Len(t, msgs[2].ToolResultBlocks, 1)
	assert.Equal(t, "toolu_123", msgs[2].ToolResultBlocks[0].ToolUseID)
	assert.Contains(t, msgs[2].ToolResultBlocks[0].Content, "package main")
	assert.False(t, msgs[2].ToolResultBlocks[0].IsError)
}

func TestReadSession_ToolResultError(t *testing.T) {
	dir := t.TempDir()

	jsonl := `{"uuid":"a","parentUuid":"","type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_err","name":"Bash","input":{"command":"exit 1"}}]},"timestamp":"2025-01-01T00:00:00Z"}
{"uuid":"b","parentUuid":"a","type":"human","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_err","content":"command failed","is_error":true}]},"timestamp":"2025-01-01T00:00:01Z"}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(jsonl), 0644))

	msgs, err := ReadSession(dir)
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	require.Len(t, msgs[0].ToolUseBlocks, 1)
	assert.Equal(t, "Bash", msgs[0].ToolUseBlocks[0].Name)

	require.Len(t, msgs[1].ToolResultBlocks, 1)
	assert.True(t, msgs[1].ToolResultBlocks[0].IsError)
	assert.Equal(t, "command failed", msgs[1].ToolResultBlocks[0].Content)
}

func TestExtractToolResultContent_PlainString(t *testing.T) {
	raw := json.RawMessage(`"hello world"`)
	assert.Equal(t, "hello world", extractToolResultContent(raw))
}

func TestExtractToolResultContent_ArrayOfBlocks(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"line 1"},{"type":"text","text":"line 2"}]`)
	assert.Equal(t, "line 1\nline 2", extractToolResultContent(raw))
}

func TestExtractToolResultContent_Null(t *testing.T) {
	raw := json.RawMessage(`null`)
	assert.Equal(t, "", extractToolResultContent(raw))
}

func TestExtractToolResultContent_Empty(t *testing.T) {
	assert.Equal(t, "", extractToolResultContent(nil))
	assert.Equal(t, "", extractToolResultContent(json.RawMessage{}))
}

func TestExtractToolResultContent_MalformedJSON(t *testing.T) {
	raw := json.RawMessage(`{not valid json}`)
	assert.Equal(t, "", extractToolResultContent(raw))
}

func TestExtractToolResultContent_EmptyArray(t *testing.T) {
	raw := json.RawMessage(`[]`)
	assert.Equal(t, "", extractToolResultContent(raw))
}

func TestExtractToolResultContent_ArrayWithEmptyTexts(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":""},{"type":"text","text":"only this"}]`)
	assert.Equal(t, "only this", extractToolResultContent(raw))
}

func TestReadSession_MultipleToolUses(t *testing.T) {
	dir := t.TempDir()

	// Assistant message with two tool_use blocks.
	jsonl := `{"uuid":"a","parentUuid":"","type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"I will read two files."},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/a.go"}},{"type":"tool_use","id":"toolu_2","name":"Read","input":{"file_path":"/b.go"}}]},"timestamp":"2025-01-01T00:00:00Z"}
{"uuid":"b","parentUuid":"a","type":"human","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"file a"},{"type":"tool_result","tool_use_id":"toolu_2","content":"file b"}]},"timestamp":"2025-01-01T00:00:01Z"}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(jsonl), 0644))

	msgs, err := ReadSession(dir)
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	assert.Equal(t, "I will read two files.", msgs[0].Content)
	require.Len(t, msgs[0].ToolUseBlocks, 2)
	assert.Equal(t, "toolu_1", msgs[0].ToolUseBlocks[0].ID)
	assert.Equal(t, "toolu_2", msgs[0].ToolUseBlocks[1].ID)

	require.Len(t, msgs[1].ToolResultBlocks, 2)
	assert.Equal(t, "toolu_1", msgs[1].ToolResultBlocks[0].ToolUseID)
	assert.Equal(t, "file a", msgs[1].ToolResultBlocks[0].Content)
	assert.Equal(t, "toolu_2", msgs[1].ToolResultBlocks[1].ToolUseID)
	assert.Equal(t, "file b", msgs[1].ToolResultBlocks[1].Content)
}
