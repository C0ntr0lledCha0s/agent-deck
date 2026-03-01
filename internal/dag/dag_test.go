package dag

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildDAG_LinearChain(t *testing.T) {
	// 3 entries: a -> b -> c forming a linear chain
	now := time.Now().UTC()
	entries := []Entry{
		{
			UUID:       "a",
			ParentUUID: "",
			Timestamp:  now.Add(-2 * time.Second),
			Type:       "human",
			Message:    json.RawMessage(`{"role":"user","content":"hello"}`),
			LineIndex:  0,
		},
		{
			UUID:       "b",
			ParentUUID: "a",
			Timestamp:  now.Add(-1 * time.Second),
			Type:       "assistant",
			Message:    json.RawMessage(`{"role":"assistant","content":"hi"}`),
			LineIndex:  1,
		},
		{
			UUID:       "c",
			ParentUUID: "b",
			Timestamp:  now,
			Type:       "human",
			Message:    json.RawMessage(`{"role":"user","content":"bye"}`),
			LineIndex:  2,
		},
	}

	result, err := BuildDAG(entries)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 3, result.TotalNodes)
	assert.Equal(t, 1, result.BranchCount)
	require.Len(t, result.ActiveBranch, 3)

	// Active branch should be in root-to-tip order: a, b, c
	assert.Equal(t, "a", result.ActiveBranch[0].UUID)
	assert.Equal(t, "b", result.ActiveBranch[1].UUID)
	assert.Equal(t, "c", result.ActiveBranch[2].UUID)
}

func TestBuildDAG_BranchSelectsMostRecent(t *testing.T) {
	// Root with 2 children; newer timestamp wins
	now := time.Now().UTC()
	entries := []Entry{
		{
			UUID:       "root",
			ParentUUID: "",
			Timestamp:  now.Add(-3 * time.Second),
			Type:       "human",
			Message:    json.RawMessage(`{"role":"user","content":"start"}`),
			LineIndex:  0,
		},
		{
			UUID:       "old-branch",
			ParentUUID: "root",
			Timestamp:  now.Add(-2 * time.Second),
			Type:       "assistant",
			Message:    json.RawMessage(`{"role":"assistant","content":"old"}`),
			LineIndex:  1,
		},
		{
			UUID:       "new-branch",
			ParentUUID: "root",
			Timestamp:  now.Add(-1 * time.Second),
			Type:       "assistant",
			Message:    json.RawMessage(`{"role":"assistant","content":"new"}`),
			LineIndex:  2,
		},
	}

	result, err := BuildDAG(entries)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 3, result.TotalNodes)
	assert.Equal(t, 2, result.BranchCount)
	require.Len(t, result.ActiveBranch, 2)

	// Active branch should select the newer tip: root -> new-branch
	assert.Equal(t, "root", result.ActiveBranch[0].UUID)
	assert.Equal(t, "new-branch", result.ActiveBranch[1].UUID)
}

func TestBuildDAG_Empty(t *testing.T) {
	result, err := BuildDAG(nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 0, result.TotalNodes)
	assert.Empty(t, result.ActiveBranch)
}

func TestBuildDAG_SingleNode(t *testing.T) {
	entries := []Entry{
		{
			UUID:       "only",
			ParentUUID: "",
			Timestamp:  time.Now().UTC(),
			Type:       "human",
			Message:    json.RawMessage(`{"role":"user","content":"solo"}`),
			LineIndex:  0,
		},
	}

	result, err := BuildDAG(entries)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 1, result.TotalNodes)
	assert.Equal(t, 1, result.BranchCount)
	require.Len(t, result.ActiveBranch, 1)
	assert.Equal(t, "only", result.ActiveBranch[0].UUID)
}
