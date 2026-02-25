package dag

import (
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
