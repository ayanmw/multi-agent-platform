package runtime

import (
	"sync"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// contextSnapshotStore 保存运行中（或刚结束）SubTask 的最新 context-window snapshot。
// 它是有意设计的临时缓存：平台不保留长期上下文历史，只保留当前 SubTask 的当前快照。
// 当 Engine 在 think() 中构建 snapshot 后，会先写入这里，HTTP API 才能在不触发 LLM
// 调用的情况下即时返回 live snapshot。
//
// 仓库用包级 sync.Map 实现，因为 snapshot 按 SubTaskID 索引，且必须比单个 Engine
// 引用活得更久：API handler 不持有 Engine 指针，但仍需要读取 live snapshot。
// 在 7-G 之后，每个 agent 执行实例有独立的 SubTaskID；leader 的 SubTaskID 等于 root
// task ID，子 agent 的 SubTaskID 由父任务派生。key 使用 SubTaskID 保证每个实例的上下文窗口
// 彼此隔离、独立可观测。
var contextSnapshotStore sync.Map

// RecordTaskContextSnapshot 保存某个 SubTask 的最新 snapshot。
// 在 think() 中构建 snapshot 后、发送事件到 event bus 前调用。
func RecordTaskContextSnapshot(subTaskID string, snapshot llm.ContextWindowSnapshot) {
	if subTaskID == "" {
		return
	}
	contextSnapshotStore.Store(subTaskID, snapshot)
}

// GetTaskContextSnapshot 返回 SubTask 的最新 live snapshot（如有）。
// 第二个返回值表示是否找到。若未命中，需要持久化快照的调用方可回退到
// db.QueryConversationsByTask 等持久化来源。
func GetTaskContextSnapshot(subTaskID string) (llm.ContextWindowSnapshot, bool) {
	if subTaskID == "" {
		return llm.ContextWindowSnapshot{}, false
	}
	if v, ok := contextSnapshotStore.Load(subTaskID); ok {
		if s, ok := v.(llm.ContextWindowSnapshot); ok {
			return s, true
		}
	}
	return llm.ContextWindowSnapshot{}, false
}

// DeleteTaskContextSnapshot 删除某个 SubTask 的 snapshot。
// 该函数暴露给调用方，用于在任务结束或内存压力下主动清理；Engine 本身的
// Run() 终态逻辑也会调用它来避免已完成 SubTask 长期占用内存。
func DeleteTaskContextSnapshot(subTaskID string) {
	if subTaskID == "" {
		return
	}
	contextSnapshotStore.Delete(subTaskID)
}
