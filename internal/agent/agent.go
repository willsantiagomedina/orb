package agent

import "time"

// Status describes one agent's current execution state.
type Status string

const (
	// StatusIdle means the agent is not currently processing work.
	StatusIdle Status = "idle"
	// StatusThinking means the agent is actively generating/streaming reasoning.
	StatusThinking Status = "thinking"
	// StatusTool means the agent is currently executing or handling a tool call.
	StatusTool Status = "tool"
)

// Snapshot is an immutable agent view used by the TUI.
type Snapshot struct {
	ID          string
	Name        string
	BackendID   string
	SessionName string
	TaskID      string
	Worktree    string
	Status      Status
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateInput contains required data when creating an agent.
type CreateInput struct {
	Name        string
	BackendID   string
	SessionName string
	TaskID      string
	Worktree    string
}
