// Package dag parses Claude Code JSONL conversation files and extracts the
// active conversation branch. Claude Code stores conversations as append-only
// JSONL where each entry has a UUID and parent UUID, forming a tree (due to
// retries and edits). The DAG builder finds the tip of the most recent branch
// and walks back to the root to produce the active conversation.
package dag

import (
	"encoding/json"
	"sort"
	"time"
)

// Entry represents a single line from a Claude Code JSONL conversation file.
type Entry struct {
	UUID              string          `json:"uuid"`
	ParentUUID        string          `json:"parentUuid"`
	Timestamp         time.Time       `json:"timestamp"`
	Type              string          `json:"type"`
	Message           json.RawMessage `json:"message"`
	Raw               json.RawMessage `json:"-"`
	LineIndex         int             `json:"-"`
	LogicalParentUUID string          `json:"logicalParentUuid"`
}

// DAGNode represents a node in the conversation DAG.
type DAGNode struct {
	UUID       string
	ParentUUID string
	LineIndex  int
	Entry      *Entry
}

// DAGResult contains the resolved active branch and DAG metadata.
type DAGResult struct {
	ActiveBranch []*DAGNode
	TotalNodes   int
	BranchCount  int
}

// BuildDAG builds a DAG from the given entries and resolves the active
// conversation branch by finding the most recent tip and walking back to root.
func BuildDAG(entries []Entry) (*DAGResult, error) {
	if len(entries) == 0 {
		return &DAGResult{}, nil
	}

	// Build nodeMap (uuid -> node) and childrenMap (parentUUID -> child UUIDs).
	nodeMap := make(map[string]*DAGNode, len(entries))
	childrenMap := make(map[string][]string)

	for i := range entries {
		e := &entries[i]
		node := &DAGNode{
			UUID:       e.UUID,
			ParentUUID: e.ParentUUID,
			LineIndex:  e.LineIndex,
			Entry:      e,
		}
		nodeMap[e.UUID] = node

		parent := e.ParentUUID
		if parent != "" {
			childrenMap[parent] = append(childrenMap[parent], e.UUID)
		}
	}

	// Find tips: nodes with no children in childrenMap.
	var tips []*DAGNode
	for _, node := range nodeMap {
		if _, hasChildren := childrenMap[node.UUID]; !hasChildren {
			tips = append(tips, node)
		}
	}

	branchCount := len(tips)

	if branchCount == 0 {
		return &DAGResult{
			TotalNodes: len(nodeMap),
		}, nil
	}

	// Sort tips by timestamp desc, tiebreak by lineIndex desc.
	sort.Slice(tips, func(i, j int) bool {
		ti := tips[i].Entry.Timestamp
		tj := tips[j].Entry.Timestamp
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return tips[i].LineIndex > tips[j].LineIndex
	})

	// Walk from selected tip to root via ParentUUID, with LogicalParentUUID
	// fallback for compact_boundary entries.
	selectedTip := tips[0]
	var branch []*DAGNode
	visited := make(map[string]bool)
	current := selectedTip

	for current != nil {
		if visited[current.UUID] {
			break // prevent cycles
		}
		visited[current.UUID] = true
		branch = append(branch, current)

		parentID := current.ParentUUID
		// For compact_boundary entries, fall back to LogicalParentUUID.
		if parentID == "" && current.Entry.LogicalParentUUID != "" && current.Entry.Type == "compact_boundary" {
			parentID = current.Entry.LogicalParentUUID
		}

		if parentID == "" {
			break
		}
		current = nodeMap[parentID]
	}

	// Reverse to root-to-tip order.
	for i, j := 0, len(branch)-1; i < j; i, j = i+1, j-1 {
		branch[i], branch[j] = branch[j], branch[i]
	}

	return &DAGResult{
		ActiveBranch: branch,
		TotalNodes:   len(nodeMap),
		BranchCount:  branchCount,
	}, nil
}
