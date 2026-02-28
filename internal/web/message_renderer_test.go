package web

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseContentBlocks_TextOnly(t *testing.T) {
	msg := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"hello world"}]}`)
	blocks := parseContentBlocks(msg)
	require.Len(t, blocks, 1)
	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "hello world", blocks[0].Text)
}

func TestParseContentBlocks_StringContent(t *testing.T) {
	msg := json.RawMessage(`{"role":"user","content":"simple string"}`)
	blocks := parseContentBlocks(msg)
	require.Len(t, blocks, 1)
	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "simple string", blocks[0].Text)
}

func TestParseContentBlocks_ThinkingBlock(t *testing.T) {
	msg := json.RawMessage(`{"role":"assistant","content":[{"type":"thinking","thinking":"let me reason"},{"type":"text","text":"answer"}]}`)
	blocks := parseContentBlocks(msg)
	require.Len(t, blocks, 2)
	assert.Equal(t, "thinking", blocks[0].Type)
	assert.Equal(t, "let me reason", blocks[0].Text)
	assert.Equal(t, "text", blocks[1].Type)
	assert.Equal(t, "answer", blocks[1].Text)
}

func TestParseContentBlocks_ToolUse(t *testing.T) {
	msg := json.RawMessage(`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}`)
	blocks := parseContentBlocks(msg)
	require.Len(t, blocks, 1)
	assert.Equal(t, "tool_use", blocks[0].Type)
	assert.Equal(t, "Bash", blocks[0].ToolName)
	assert.Equal(t, "t1", blocks[0].ToolUseID)
	assert.JSONEq(t, `{"command":"ls"}`, string(blocks[0].ToolInput))
}

func TestParseContentBlocks_ToolResult(t *testing.T) {
	msg := json.RawMessage(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"file1.go\nfile2.go"}]}`)
	blocks := parseContentBlocks(msg)
	require.Len(t, blocks, 1)
	assert.Equal(t, "tool_result", blocks[0].Type)
	assert.Equal(t, "t1", blocks[0].ToolUseID)
	assert.Equal(t, "file1.go\nfile2.go", blocks[0].Text)
}

func TestParseContentBlocks_EmptyMessage(t *testing.T) {
	msg := json.RawMessage(`{}`)
	blocks := parseContentBlocks(msg)
	assert.Empty(t, blocks)
}

func TestGroupIntoTurns_Basic(t *testing.T) {
	msgs := []dagMessage{
		{Role: "user", Blocks: []contentBlock{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Blocks: []contentBlock{{Type: "thinking", Text: "hmm"}, {Type: "text", Text: "hi"}}},
	}
	turns := groupIntoTurns(msgs)
	require.Len(t, turns, 2)
	assert.Equal(t, "user", turns[0].Role)
	assert.Len(t, turns[0].Blocks, 1)
	assert.Equal(t, "assistant", turns[1].Role)
	assert.Len(t, turns[1].Blocks, 2)
}

func TestGroupIntoTurns_ConsecutiveAssistant(t *testing.T) {
	msgs := []dagMessage{
		{Role: "user", Blocks: []contentBlock{{Type: "text", Text: "do it"}}},
		{Role: "assistant", Blocks: []contentBlock{{Type: "tool_use", ToolName: "Bash"}}},
		{Role: "user", Blocks: []contentBlock{{Type: "tool_result", Text: "output"}}},
		{Role: "assistant", Blocks: []contentBlock{{Type: "text", Text: "done"}}},
	}
	turns := groupIntoTurns(msgs)
	require.Len(t, turns, 2)
	assert.Equal(t, "user", turns[0].Role)
	// All assistant + tool_result messages between user prompts = one assistant turn
	assert.Equal(t, "assistant", turns[1].Role)
	assert.Len(t, turns[1].Blocks, 3) // tool_use + tool_result + text
}

func TestGroupIntoTurns_MultipleUserPrompts(t *testing.T) {
	msgs := []dagMessage{
		{Role: "user", Blocks: []contentBlock{{Type: "text", Text: "first"}}},
		{Role: "assistant", Blocks: []contentBlock{{Type: "text", Text: "reply1"}}},
		{Role: "user", Blocks: []contentBlock{{Type: "text", Text: "second"}}},
		{Role: "assistant", Blocks: []contentBlock{{Type: "text", Text: "reply2"}}},
	}
	turns := groupIntoTurns(msgs)
	require.Len(t, turns, 4)
	assert.Equal(t, "user", turns[0].Role)
	assert.Equal(t, "assistant", turns[1].Role)
	assert.Equal(t, "user", turns[2].Role)
	assert.Equal(t, "assistant", turns[3].Role)
}

func TestGroupIntoTurns_Empty(t *testing.T) {
	turns := groupIntoTurns(nil)
	assert.Empty(t, turns)
}
