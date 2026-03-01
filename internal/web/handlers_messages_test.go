package web

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/dag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── buildAugmentedMessages tests ──────────────────────────────────────

func TestBuildAugmentedMessages_SkipsMetadataEntries(t *testing.T) {
	msgs := buildAugmentedMessages([]dag.SessionMessage{
		{UUID: "a", Role: "", Content: "", Type: "progress"},
		{UUID: "b", Role: "user", Content: "hello"},
	})
	require.Len(t, msgs, 1)
	assert.Equal(t, "b", msgs[0].UUID)
}

func TestBuildAugmentedMessages_SuppressesToolResultOnlyUser(t *testing.T) {
	msgs := buildAugmentedMessages([]dag.SessionMessage{
		{UUID: "a", Role: "assistant", Content: "reading", ToolUseBlocks: []dag.ToolUseBlock{
			{ID: "t1", Name: "Read", Input: json.RawMessage(`{"file_path":"/a.go"}`)},
		}},
		{UUID: "b", Role: "user", Content: "", ToolResultBlocks: []dag.ToolResultBlock{
			{ToolUseID: "t1", Content: "file content"},
		}},
	})
	require.Len(t, msgs, 1, "tool-result-only user message should be suppressed")
	assert.Equal(t, "a", msgs[0].UUID)
}

func TestBuildAugmentedMessages_KeepsUserWithTextAndToolResult(t *testing.T) {
	msgs := buildAugmentedMessages([]dag.SessionMessage{
		{UUID: "a", Role: "assistant", Content: "", ToolUseBlocks: []dag.ToolUseBlock{
			{ID: "t1", Name: "Bash"},
		}},
		{UUID: "b", Role: "user", Content: "here is the result", ToolResultBlocks: []dag.ToolResultBlock{
			{ToolUseID: "t1", Content: "output"},
		}},
	})
	require.Len(t, msgs, 2, "user message with text+tool_result should NOT be suppressed")
	assert.Equal(t, "b", msgs[1].UUID)
	assert.Equal(t, "here is the result", msgs[1].Content)
}

func TestBuildAugmentedMessages_MatchesToolResults(t *testing.T) {
	msgs := buildAugmentedMessages([]dag.SessionMessage{
		{UUID: "a", Role: "assistant", Content: "let me check", ToolUseBlocks: []dag.ToolUseBlock{
			{ID: "t1", Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`)},
		}},
		{UUID: "b", Role: "user", Content: "", ToolResultBlocks: []dag.ToolResultBlock{
			{ToolUseID: "t1", Content: "file1\nfile2"},
		}},
	})
	require.Len(t, msgs, 1) // user suppressed
	require.Len(t, msgs[0].Tools, 1)
	assert.Equal(t, "t1", msgs[0].Tools[0].ID)
	assert.Equal(t, "Bash", msgs[0].Tools[0].Name)
	assert.False(t, msgs[0].Tools[0].IsError)

	var result string
	require.NoError(t, json.Unmarshal(msgs[0].Tools[0].Result, &result))
	assert.Equal(t, "file1\nfile2", result)
}

func TestBuildAugmentedMessages_OrphanedToolUse(t *testing.T) {
	// Tool use with no matching result should still appear with nil Result.
	msgs := buildAugmentedMessages([]dag.SessionMessage{
		{UUID: "a", Role: "assistant", Content: "reading", ToolUseBlocks: []dag.ToolUseBlock{
			{ID: "orphan", Name: "Read", Input: json.RawMessage(`{"file_path":"/x.go"}`)},
		}},
	})
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Tools, 1)
	assert.Equal(t, "orphan", msgs[0].Tools[0].ID)
	assert.Nil(t, msgs[0].Tools[0].Result)
	assert.False(t, msgs[0].Tools[0].IsError)
}

func TestBuildAugmentedMessages_ErrorToolResult(t *testing.T) {
	msgs := buildAugmentedMessages([]dag.SessionMessage{
		{UUID: "a", Role: "assistant", ToolUseBlocks: []dag.ToolUseBlock{
			{ID: "t1", Name: "Bash", Input: json.RawMessage(`{"command":"fail"}`)},
		}},
		{UUID: "b", Role: "user", ToolResultBlocks: []dag.ToolResultBlock{
			{ToolUseID: "t1", Content: "command not found", IsError: true},
		}},
	})
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Tools, 1)
	assert.True(t, msgs[0].Tools[0].IsError)
}

func TestBuildAugmentedMessages_PreservesTimestamp(t *testing.T) {
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	msgs := buildAugmentedMessages([]dag.SessionMessage{
		{UUID: "a", Role: "user", Content: "hi", Timestamp: ts},
	})
	require.Len(t, msgs, 1)
	assert.Equal(t, ts, msgs[0].Timestamp)
}

// ── computeToolAugment tests ──────────────────────────────────────────

func TestComputeToolAugment_Bash(t *testing.T) {
	input := json.RawMessage(`{"command":"echo hello"}`)
	result := json.RawMessage(`"hello\n"`)

	aug := computeToolAugment("Bash", input, result, false)
	require.NotNil(t, aug, "Bash with output should produce augment")

	var ba bashAugment
	require.NoError(t, json.Unmarshal(aug, &ba))
	assert.Equal(t, 1, ba.LineCount)
	assert.False(t, ba.IsError)
	assert.Contains(t, ba.StdoutHTML, "hello")
}

func TestComputeToolAugment_BashError(t *testing.T) {
	input := json.RawMessage(`{"command":"fail"}`)
	result := json.RawMessage(`"error output"`)

	aug := computeToolAugment("Bash", input, result, true)
	require.NotNil(t, aug)

	var ba bashAugment
	require.NoError(t, json.Unmarshal(aug, &ba))
	assert.True(t, ba.IsError)
}

func TestComputeToolAugment_BashEmptyNoError(t *testing.T) {
	input := json.RawMessage(`{"command":"true"}`)
	result := json.RawMessage(`""`)

	aug := computeToolAugment("Bash", input, result, false)
	assert.Nil(t, aug, "Bash with empty stdout and no error should return nil")
}

func TestComputeToolAugment_Edit(t *testing.T) {
	input := json.RawMessage(`{"file_path":"main.go","old_string":"hello","new_string":"goodbye"}`)

	aug := computeToolAugment("Edit", input, nil, false)
	require.NotNil(t, aug)

	var ea editAugment
	require.NoError(t, json.Unmarshal(aug, &ea))
	assert.Greater(t, ea.Additions, 0)
	assert.Greater(t, ea.Deletions, 0)
	assert.Contains(t, ea.DiffHTML, "goodbye")
	assert.Contains(t, ea.DiffHTML, "hello")
}

func TestComputeToolAugment_EditEmptyStrings(t *testing.T) {
	input := json.RawMessage(`{"file_path":"main.go","old_string":"","new_string":""}`)

	aug := computeToolAugment("Edit", input, nil, false)
	assert.Nil(t, aug, "Edit with empty old_string and new_string should return nil")
}

func TestComputeToolAugment_Read(t *testing.T) {
	input := json.RawMessage(`{"file_path":"main.go"}`)
	result := json.RawMessage(`"package main\n\nfunc main() {}\n"`)

	aug := computeToolAugment("Read", input, result, false)
	require.NotNil(t, aug)

	var ra readAugment
	require.NoError(t, json.Unmarshal(aug, &ra))
	assert.Equal(t, "Go", ra.Language)
	assert.Equal(t, 2, ra.LineCount)
	assert.Contains(t, ra.ContentHTML, "package")
}

func TestComputeToolAugment_ReadEmptyContent(t *testing.T) {
	input := json.RawMessage(`{"file_path":"empty.txt"}`)
	result := json.RawMessage(`""`)

	aug := computeToolAugment("Read", input, result, false)
	assert.Nil(t, aug, "Read with empty content should return nil")
}

func TestComputeToolAugment_UnknownTool(t *testing.T) {
	aug := computeToolAugment("Glob", json.RawMessage(`{}`), json.RawMessage(`"result"`), false)
	assert.Nil(t, aug, "Unknown tool should return nil")
}

func TestComputeToolAugment_NilInputResult(t *testing.T) {
	aug := computeToolAugment("Bash", nil, nil, false)
	assert.Nil(t, aug, "Bash with nil input and result should return nil")

	aug = computeToolAugment("Read", nil, nil, false)
	assert.Nil(t, aug, "Read with nil input and result should return nil")

	aug = computeToolAugment("Edit", nil, nil, false)
	assert.Nil(t, aug, "Edit with nil input should return nil")
}
