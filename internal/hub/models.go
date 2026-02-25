package hub

import "time"

// Phase represents the workflow phase of a task.
type Phase string

const (
	PhaseBrainstorm Phase = "brainstorm"
	PhasePlan       Phase = "plan"
	PhaseExecute    Phase = "execute"
	PhaseReview     Phase = "review"
)

// TaskStatus represents the workflow stage of a task (kanban column).
type TaskStatus string

const (
	TaskStatusBacklog  TaskStatus = "backlog"
	TaskStatusPlanning TaskStatus = "planning"
	TaskStatusRunning  TaskStatus = "running"
	TaskStatusReview   TaskStatus = "review"
	TaskStatusDone     TaskStatus = "done"
)

// AgentStatus represents what Claude is doing right now.
type AgentStatus string

const (
	AgentStatusThinking AgentStatus = "thinking"
	AgentStatusWaiting  AgentStatus = "waiting"
	AgentStatusRunning  AgentStatus = "running"
	AgentStatusIdle     AgentStatus = "idle"
	AgentStatusError    AgentStatus = "error"
	AgentStatusComplete AgentStatus = "complete"
)

// DiffInfo tracks file change statistics for a task.
type DiffInfo struct {
	Files int `json:"files"`
	Add   int `json:"add"`
	Del   int `json:"del"`
}

// Session represents one phase-session within a task's lifecycle.
type Session struct {
	ID              string `json:"id"`
	Phase           Phase  `json:"phase"`
	Status          string `json:"status"` // "active" | "complete"
	Duration        string `json:"duration"`
	Artifact        string `json:"artifact,omitempty"`
	Summary         string `json:"summary,omitempty"`
	ClaudeSessionID string `json:"claudeSessionId,omitempty"`
}

// Task wraps a session with orchestration metadata.
type Task struct {
	ID           string     `json:"id"`
	SessionID    string     `json:"sessionId"`
	TmuxSession  string     `json:"tmuxSession,omitempty"`
	Status       TaskStatus `json:"status"`
	Project      string     `json:"project"`
	Description  string     `json:"description"`
	Phase        Phase      `json:"phase"`
	Branch       string     `json:"branch,omitempty"`
	Skills       []string    `json:"skills,omitempty"`
	MCPs         []string    `json:"mcps,omitempty"`
	Diff         *DiffInfo   `json:"diff,omitempty"`
	Container    string      `json:"container,omitempty"`
	AskQuestion  string      `json:"askQuestion,omitempty"`
	AgentStatus  AgentStatus `json:"agentStatus"`
	Sessions     []Session   `json:"sessions,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	ParentTaskID string     `json:"parentTaskId,omitempty"`
}

// Project defines a workspace that tasks can be routed to.
type Project struct {
	Name        string    `json:"name"`
	Repo        string    `json:"repo,omitempty"`
	Path        string    `json:"path"`
	Keywords    []string  `json:"keywords"`
	Container   string    `json:"container,omitempty"`
	DefaultMCPs []string  `json:"defaultMcps,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// RouteResult describes a keyword-match routing result.
type RouteResult struct {
	Project         string   `json:"project"`
	Confidence      float64  `json:"confidence"`
	MatchedKeywords []string `json:"matchedKeywords"`
}
