package agent

import "time"

// Status 表示 agent 执行状态
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

// Agent 表示一个 agent 配置
type Agent struct {
	ID           string
	Name         string
	SystemPrompt string
	Model        string
	Endpoint     string
	APIKey       string
	Temperature  float32
	MaxTokens    int
	Tools        []string // 允许使用的 tool 名称
	Role         AgentRole

	// Config 是 Agent 级运行时默认配置（JSON 列持久化）。
	// 当前用于默认权限位，按 OR 语义合并到 TaskContract.Permissions。
	Config AgentConfig

	// PreferredModel 是 Agent 显式指定的 model 名称。
	// 若设置且存在于 ModelRegistry，则路由直接命中该模型，跳过自动选择。
	PreferredModel string

	// PreferredTier 是 Agent 偏好的 model 层级（如 "standard"）。
	// 当未指定 PreferredModel 时，Router 在该 tier 内选择模型。
	PreferredTier string

	// AllowAutoRoute 表示当 PreferredModel/PreferredTier 未命中或不可用时，
	// 是否允许 Router 自动重选其他模型。
	AllowAutoRoute bool

	// MaxCostUSD 是单次 task 的成本预算上限（USD）。
	// 0 表示未设置预算限制。
	MaxCostUSD float64

	CreatedAt time.Time
	UpdatedAt time.Time
}

// AgentConfig 是 Agent 级的运行时默认配置。
// 持久化在 agents.config JSON 列中，可在启动任务时合并到 TaskContract。
type AgentConfig struct {
	// Permissions 是默认权限位，按 OR 语义合并到 TaskContract.Permissions。
	Permissions TaskPermissions `json:"permissions,omitempty"`
}

// TaskPermissions 镜像 harness.TaskPermissions，用于从 agents.config 中
// 反序列化默认权限位。
type TaskPermissions struct {
	AllowNetwork        bool `json:"allow_network"`
	AllowFileDelete     bool `json:"allow_file_delete"`
	AllowFileWrite      bool `json:"allow_file_write"`
	AllowShell          bool `json:"allow_shell"`
	AllowShellDangerous bool `json:"allow_shell_dangerous"`
}

// StepType 表示 agent 循环中一个 step 的类型
type StepType string

const (
	StepTypeThink       StepType = "think"
	StepTypeToolCall    StepType = "tool_call"
	StepTypeObservation StepType = "observation"
)

// Step 表示 agent 执行过程中的单个 step
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
