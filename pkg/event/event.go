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
