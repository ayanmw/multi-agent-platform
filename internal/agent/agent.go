package agent

import "time"

// Status represents agent execution status
type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusPaused    Status = "paused"
	StatusCancelled Status = "cancelled"
)

// AgentRole 表示 agent 在分布式任务中的角色。
// 在 leader-agent 驱动的任务派发中，只有 leader 允许调用 dispatch_sub_agent。
type AgentRole string

const (
	AgentRoleLeader AgentRole = "leader"
	AgentRoleWorker AgentRole = "worker"
)

// Agent represents an agent configuration
type Agent struct {
	ID           string
	Name         string
	SystemPrompt string
	Model        string
	Endpoint     string
	APIKey       string
	Temperature  float32
	MaxTokens    int
	Tools        []string // allowed tool names
	Role         AgentRole
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// StepType represents the type of a step in the agent loop
type StepType string

const (
	StepTypeThink      StepType = "think"
	StepTypeToolCall   StepType = "tool_call"
	StepTypeObservation StepType = "observation"
)

// Step represents a single step in the agent execution
type Step struct {
	Index      int
	Type       StepType
	Status     Status
	Thinking   string
	ToolName   string
	ToolInput  map[string]any
	ToolOutput string
	DurationMs int
	CreatedAt  time.Time
}
