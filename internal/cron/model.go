// Package cron 实现多 Agent 平台的定时器子系统。
//
// 定时器系统是一个与 skill / tool / memory 平级的独立模块，提供三类入口：
//   1. Agent Tool —— LLM 在运行时通过 cron/create 等工具创建定时回调；
//   2. REST API —— 用户通过 Web UI 管理（CRUD + 状态 + 执行历史）；
//   3. Event Bus —— 所有状态变更与每次触发都广播 cron_* 事件，前端实时可见。
//
// 调度器基于 robfig/cron/v3（秒级 6 域），支持 cron 表达式、interval、once 三种
// 调度类型。触发时可执行四种 action：start_task（启动 Agent task）、script（白名单
// tool 调用）、webhook（HTTP 回调）、notify_session（向 session 发通知 + 写消息）。
//
// 本文件定义领域模型与常量。Store / Scheduler / Executor / ActionRunner / Service
// / Tools 分别见同包其它文件。
package cron

import "time"

// ScheduleType 表示调度规则的类型。
type ScheduleType string

const (
	// ScheduleCron 使用标准 6 域（秒 分 时 日 月 周）cron 表达式。
	ScheduleCron ScheduleType = "cron"
	// ScheduleInterval 使用固定间隔，cron_expr 形如 "30s" / "5m" / "1h"。
	ScheduleInterval ScheduleType = "interval"
	// ScheduleOnce 在指定时刻触发一次，once_at 为 RFC3339 时间。
	ScheduleOnce ScheduleType = "once"
)

// IsValid 判断调度类型是否合法。
func (s ScheduleType) IsValid() bool {
	switch s {
	case ScheduleCron, ScheduleInterval, ScheduleOnce:
		return true
	}
	return false
}

// ActionType 表示定时器触发时执行的动作类型。
type ActionType string

const (
	// ActionStartTask 启动一个新的 Agent task（复用 chat 启动链路）。
	ActionStartTask ActionType = "start_task"
	// ActionScript 按顺序执行一组白名单 tool 调用。
	ActionScript ActionType = "script"
	// ActionWebhook 向外部 URL 发起 HTTP 请求。
	ActionWebhook ActionType = "webhook"
	// ActionNotifySession 向指定 session 广播通知事件并写入 session 消息。
	ActionNotifySession ActionType = "notify_session"
)

// IsValid 判断动作类型是否合法。
func (a ActionType) IsValid() bool {
	switch a {
	case ActionStartTask, ActionScript, ActionWebhook, ActionNotifySession:
		return true
	}
	return false
}

// Status 表示 cron 的运行状态。
type Status string

const (
	// StatusEnabled 已启用，scheduler 中有对应 entry，到点触发。
	StatusEnabled Status = "enabled"
	// StatusDisabled 已禁用，scheduler 中无 entry，配置保留。
	StatusDisabled Status = "disabled"
	// StatusPaused 已暂停，语义偏"临时"，scheduler 中无 entry，可 resume。
	// 实现上 paused 与 disabled 都移除 entry，区别仅在 UI 语义与状态机可达性。
	StatusPaused Status = "paused"
)

// IsValid 判断状态是否合法。
func (s Status) IsValid() bool {
	switch s {
	case StatusEnabled, StatusDisabled, StatusPaused:
		return true
	}
	return false
}

// ExecStatus 表示单次执行的状态。
type ExecStatus string

const (
	ExecRunning   ExecStatus = "running"
	ExecCompleted ExecStatus = "completed"
	ExecFailed    ExecStatus = "failed"
	ExecSkipped   ExecStatus = "skipped" // 并发重叠时跳过
	ExecMissed    ExecStatus = "missed"  // 服务离线期间错过
)

// Cron 是一个定时器的完整领域对象。
type Cron struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`

	// 调度规则
	ScheduleType ScheduleType `json:"schedule_type"`
	CronExpr     string        `json:"cron_expr"` // 6 域秒级表达式（cron）/ 间隔（interval）
	DisplayType  string        `json:"display_type"` // preset|interval|cron|once，前端展示用
	Timezone     string        `json:"timezone"`
	OnceAt       string        `json:"once_at"` // RFC3339，schedule_type=once 时用

	// 动作
	ActionType      ActionType     `json:"action_type"`
	ActionPayload   map[string]any `json:"action_payload"`
	ActionPayloadRaw string        `json:"-"` // 持久化用原始 JSON，应用层不直接用

	// 状态
	Status          Status `json:"status"`
	AllowConcurrent bool   `json:"allow_concurrent"`

	// 来源 / 归属（预留 RBAC）
	Source string `json:"source"` // user | agent
	Owner  string `json:"owner"`

	// 调度元数据
	LastTriggeredAt  *time.Time `json:"last_triggered_at"`
	NextTriggerAt    *time.Time `json:"next_trigger_at"`
	LastExecutionID  string     `json:"last_execution_id"`
	TriggerCount     int        `json:"trigger_count"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Execution 是单次触发的执行记录。
type Execution struct {
	ID            string     `json:"id"`
	CronID        string     `json:"cron_id"`
	TriggeredAt   time.Time  `json:"triggered_at"`
	Status        ExecStatus `json:"status"`
	Reason        string     `json:"reason"`
	RenderedInput string     `json:"rendered_input"`
	ResultSummary string     `json:"result_summary"`
	TaskID        string     `json:"task_id"`
	SessionID     string     `json:"session_id"`
	DurationMS    int        `json:"duration_ms"`
	Error         string     `json:"error"`
	CreatedAt     time.Time  `json:"created_at"`
}

// ListFilter 是 ListCrons 的过滤参数。零值字段不过滤。
type ListFilter struct {
	Status     string
	ActionType string
	Source     string
	Query      string // 模糊匹配 name/description/id
	Limit      int
}

// ExecListFilter 是 ListExecutions 的过滤参数。
type ExecListFilter struct {
	CronID string
	Status string
	Before time.Time
	After  time.Time
	Limit  int
	Offset int
}

// CleanFilter 是 CleanExecutions 的过滤参数。全为零值时不删任何记录。
type CleanFilter struct {
	CronID string
	Status string
	Before time.Time
}

// CreateInput 是 Service.Create 的输入。
type CreateInput struct {
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	ScheduleType    ScheduleType   `json:"schedule_type"`
	CronExpr        string         `json:"cron_expr"`
	OnceAt          string         `json:"once_at"`
	Timezone        string         `json:"timezone"`
	DisplayType     string         `json:"display_type"`
	ActionType      ActionType     `json:"action_type"`
	ActionPayload   map[string]any `json:"action_payload"`
	AllowConcurrent bool           `json:"allow_concurrent"`
	Source          string         `json:"source"` // 默认 user
}

// UpdateInput 是 Service.Update 的输入。所有字段为指针，nil 表示不修改。
type UpdateInput struct {
	Name            *string
	Description     *string
	ScheduleType    *ScheduleType
	CronExpr        *string
	OnceAt          *string
	Timezone        *string
	DisplayType     *string
	ActionType      *ActionType
	ActionPayload   *map[string]any
	AllowConcurrent *bool
}

// StartTaskPayload 是 action_type=start_task 的 payload 结构。
type StartTaskPayload struct {
	AgentID        string   `json:"agent_id"`
	SessionID      string   `json:"session_id"` // 空=新建 session；非空=复用；失效=失败
	Input          string   `json:"input"`      // 支持模板占位符
	SystemPrompt   string   `json:"system_prompt"`
	MaxSteps       int      `json:"max_steps"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	Scope          string   `json:"scope"`
	AllowedTools   []string `json:"allowed_tools"`
	TokenBudget    int      `json:"token_budget"`
	CostBudgetUSD  float64  `json:"cost_budget_usd"`
	CaseID         string   `json:"case_id"`
}

// ScriptToolCall 是 script action 中的一个 tool 调用单元。
type ScriptToolCall struct {
	Tool      string         `json:"tool"`
	Input     map[string]any `json:"input"`
	Approval  bool           `json:"approval"` // 首期忽略，仅记录
}

// ScriptPayload 是 action_type=script 的 payload 结构。
type ScriptPayload struct {
	ToolCalls []ScriptToolCall `json:"tool_calls"`
}

// WebhookPayload 是 action_type=webhook 的 payload 结构。
// 字符串字段均支持模板占位符。
type WebhookPayload struct {
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers"`
	Body           string            `json:"body"`
	TimeoutSeconds int               `json:"timeout_seconds"`
}

// NotifySessionPayload 是 action_type=notify_session 的 payload 结构。
type NotifySessionPayload struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"` // 支持模板占位符
	EventType string `json:"event_type"` // 默认 cron_notification
}
