package event

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Memory 生命周期事件，通过 WebSocket 广播。由 Memory CRUD API handler 产生，
// 前端据此保持自身缓存同步。
const (
	EventMemoryCreated         = "memory_created"
	EventMemoryUpdated         = "memory_updated"
	EventMemoryDeleted         = "memory_deleted"
	EventMemoryPromoted        = "memory_promoted"
	EventMemoryRecallDone      = "memory_recall_performed"
	EventHeartbeatBeat         = "heartbeat_beat"
	EventContextWindowSnapshot = "context_window_snapshot"

	// EventTaskEvaluated 在任务完成、引擎对 case contract 执行 AcceptanceEvaluator 后发出。
	// 携带 passed、score、reason 以及完整的评估报告。
	EventTaskEvaluated = "task_evaluated"

	// EventMcpToolsChanged 在 registry 中 MCP proxy 工具集合发生变化
	// （server 加载/卸载/启用/禁用）时广播。前端收到后应刷新其工具列表/可用工具定义。
	EventMcpToolsChanged = "mcp_tools_changed"

	// EventTraceSpan 在一个 trace span 结束时发出，前端据此实时渲染调用树。
	EventTraceSpan = "trace_span"

	// Cron 子系统事件常量。定时器的所有状态变更与触发都通过这些事件广播，
	// 前端据此实时渲染 cron 列表与执行流。事件 TaskID 字段填 cron_id，
	// AgentID 填触发该次执行的 agent_id（start_task 时）或 "cron"。
	EventCronCreated            = "cron_created"
	EventCronUpdated            = "cron_updated"
	EventCronDeleted            = "cron_deleted"
	EventCronEnabled            = "cron_enabled"
	EventCronDisabled           = "cron_disabled"
	EventCronPaused             = "cron_paused"
	EventCronResumed            = "cron_resumed"
	EventCronTriggered          = "cron_triggered"
	EventCronExecutionStarted   = "cron_execution_started"
	EventCronExecutionCompleted = "cron_execution_completed"
	EventCronExecutionFailed    = "cron_execution_failed"
	EventCronExecutionSkipped   = "cron_execution_skipped"
	EventCronMissed             = "cron_missed"
	EventCronNotification       = "cron_notification"

	// Workspace worktree 隔离子系统事件常量。worktree 状态变更通过这些事件
	// 经 hub.SendEvent WS 广播（不写 task steps），前端据此实时渲染 active
	// worktree 状态与未提交护栏确认。事件 TaskID 字段填 session_id。
	EventWorktreeCreated       = "worktree_created"       // worktree 创建成功
	EventWorktreeRemoved       = "worktree_removed"       // worktree 被 remove 删除
	EventWorktreeExitBlocked   = "worktree_exit_blocked"  // exit{remove} 因未提交变更被护栏阻塞
	EventWorktreeOrphanRemoved = "worktree_orphan_removed" // 启动孤儿扫描清理 crash 残留

	// 多模型分层路由事件常量。由 Router / Engine 在执行过程中广播，
	// 前端 Inspector Routing 面板据此展示 intent 分类、模型命中、
	// fallback 触发与限流/预算命中状态。
	EventModelRouted          = "model_routed"           // Router 选择主模型
	EventModelFallbackUsed    = "model_fallback_used"    // 主模型失败，已切换 fallback
	EventModelRateLimited     = "model_rate_limited"     // 某模型触发 RPM 限流被过滤
	EventIntentClassified     = "intent_classified"      // intent 分类完成
	EventCostBudgetExceeded   = "cost_budget_exceeded"   // Agent/任务 USD 预算被耗尽

	// Provider 同步事件常量。Provider 手动/自动发现前后通过事件总线广播，
	// 前端模型管理页面据此刷新 provider 健康状态与模型列表。
	EventProviderSyncStarted  = "provider_sync_started"
	EventProviderSyncCompleted = "provider_sync_completed"
	EventProviderSyncFailed   = "provider_sync_failed"

	// 多模型分层路由精细化事件。补充 model_routed，说明当前运行采用的
	// model_mode（single_model / auto_route）以及 Router 实际数据选择结果/兜底。
	EventModelSelected          = "llm/model_selected"
	EventRouterFallbackDefault  = "llm/router_fallback_default"

	// web_research 工具事件常量。当 web_research 内部调用 LLM 做摘要时,
	// 通过事件总线广播开始与完成状态,保持白盒可观测。
	EventWebResearchSummarizeStarted  = "web_research_summarize_started"
	EventWebResearchSummarizeCompleted = "web_research_summarize_completed"
)

// Event 表示通过 WebSocket 发送的结构化事件
type Event struct {
	EventID    string         `json:"event_id"`
	TaskID     string         `json:"task_id"`
	SubTaskID  string         `json:"sub_task_id"`
	AgentID    string         `json:"agent_id"`
	StepIndex  int            `json:"step_index"`
	Type       string         `json:"type"`
	Timestamp  int64          `json:"timestamp"`
	Data       map[string]any `json:"data"`
}

// NewEvent 创建一个新事件，自动生成 ID 和时间戳
func NewEvent(eventType, taskID, agentID string, stepIndex int, data map[string]any) Event {
	return NewEventWithSubTask(eventType, taskID, "", agentID, stepIndex, data)
}

// NewEventWithSubTask 创建一个携带显式 sub-task 身份的新事件。
// taskID 为根任务；subTaskID 标识具体的 agent 执行实例。
// 对于 leader agent，subTaskID 等于 taskID；子 agent 有各自的 subTaskID。
func NewEventWithSubTask(eventType, taskID, subTaskID, agentID string, stepIndex int, data map[string]any) Event {
	if data == nil {
		data = make(map[string]any)
	}
	return Event{
		EventID:   generateID(),
		TaskID:    taskID,
		SubTaskID: subTaskID,
		AgentID:   agentID,
		StepIndex: stepIndex,
		Type:      eventType,
		Timestamp: time.Now().UnixMilli(),
		Data:      data,
	}
}

func generateID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return time.Now().Format("20060102150405")
	}
	return hex.EncodeToString(bytes)
}
