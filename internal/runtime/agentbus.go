// Package runtime — 用于 Agent 间通信的 AgentBus interface。
//
// # 设计理由
//
// AgentBus interface 允许 agent 在执行期间互相发送消息。它定义在 runtime 包
// （而非 orchestrator）以避免循环引用：orchestrator 引用 runtime，因此
// runtime 不能引用 orchestrator。
//
// orchestrator 包提供具体实现（AgentBus struct）以及一个实现此 interface
// 的 adapter，用于在两种 message 类型之间转换。
//
// # 通信模式
//
// Agent 可以通过 AgentBus 以多种模式通信：
//   - Request/Response：Agent A 向 Agent B 发送请求，B 作出响应
//   - Observation：Agent A 向 Agent B 发送 observation 作为上下文
//   - Broadcast：Agent A 向所有 agent 发送消息（ToAgentID 为空）
//   - Error：Agent A 向 Agent B 报告错误
//
// # 与 ReAct Loop 的集成
//
// 当一个 Engine 持有 AgentBus 时，它会在 Run() 中启动一个 goroutine 监听
// 到达的消息。消息到达后会被作为 user message 追加到对话中，格式为：
// "[Agent {from}]: {content}"。LLM 会将其视为新的 user input 并据此响应。
//
// # 感知 SubTask 的路由（Phase 7-I）
//
// 从 Phase 7-I 起，消息可选地携带 SubTaskID。这允许同一个 agent ID 并发
// 运行多个子任务（例如 leader agent 编排不同的 worker 组），并仍能接收到
// 发往正确子任务的消息。实现应优先将消息投递给 (agentID, subTaskID) 精确
// 匹配的 handler，然后再回退到仅按 agentID 匹配的 handler 以保持向后兼容。
package runtime

// AgentMessage 是通过 AgentBus 在 agent 之间发送的消息。
// 它携带发送方身份、接收方身份、可选的子任务路由字段、消息内容以及可选的元数据。
type AgentMessage struct {
	// FromAgentID 是发送该消息的 agent。
	FromAgentID string `json:"from_agent_id"`

	// ToAgentID 是目标 agent。若为空，则该消息会广播给所有 agent。
	ToAgentID string `json:"to_agent_id"`

	// SubTaskID 是目标子任务。设置时，AgentBus 应优先匹配精确的
	// (ToAgentID, SubTaskID) handler，再回退到仅 ToAgentID 的 handler。
	// 于 Phase 7-I 引入。
	SubTaskID string `json:"sub_task_id,omitempty"`

	// FromSubTaskID 是发送方所属的子任务。于 Phase 7-J 引入，以便
	// 持久化层能同时记录 agent 间通信的两端。
	FromSubTaskID string `json:"from_sub_task_id,omitempty"`

	// Type 描述消息类型："request"、"response"、"observation"、"error"
	Type string `json:"type"`

	// Content 是消息主体。
	Content string `json:"content"`

	// Metadata 携带任意键值对作为上下文。
	Metadata map[string]string `json:"metadata,omitempty"`
}

// AgentBus 是 agent 间通信通道的 interface。
// 它允许 agent 在执行期间互相发送消息。
// 该 bus 必须是 goroutine 安全的。
//
// # 用法
//
//	// In the Engine:
//	if e.agentBus != nil {
//	    e.agentBus.SendMessage(runtime.AgentMessage{
//	        FromAgentID: e.cfg.AgentID,
//	        ToAgentID:   "agent_reviewer",
//	        Type:        "request",
//	        Content:     "Please review the code I just wrote.",
//	    })
//	}
//
// # 实现
//
// 具体实现位于 internal/orchestrator（AgentBus struct）。
// 一个 adapter（orchestrator.agentBusAdapter）桥接两种 message 类型。
type AgentBus interface {
	// RegisterHandler 为指定 agent 注册一个 message handler。
	// 当发给该 agent 的消息到达时，handler 会被调用。
	// 每个 agent 仅允许一个 handler；再次调用 RegisterHandler 会替换
	// 之前注册的 handler。
	//
	// Phase 7-I：实现 SHOULD 也支持感知 subTaskID 的注册，可以通过提供
	// 独立的 RegisterHandlerBySubTask 方法，或通过接受 subTaskID 参数。
	// 默认签名保持向后兼容：subTaskID 为空表示"所有子任务"。
	RegisterHandler(agentID string, handler func(AgentMessage))

	// RegisterHandlerBySubTask 为特定的 (agentID, subTaskID) 对注册 handler。
	// 当 SubTaskID 为空时，其行为必须与 RegisterHandler 完全一致。
	// 于 Phase 7-I 引入。
	RegisterHandlerBySubTask(agentID, subTaskID string, handler func(AgentMessage))

	// UnregisterHandler 移除指定 agent 的 message handler。
	UnregisterHandler(agentID string)

	// UnregisterHandlerBySubTask 移除特定 (agentID, subTaskID) 对的 handler。
	// 于 Phase 7-I 引入。
	UnregisterHandlerBySubTask(agentID, subTaskID string)

	// SendMessage 从一个 agent 向另一个 agent 发送消息。
	// 若目标 agent 已注册 handler，则该 handler 会被立即调用；
	// 否则该消息会被入队以延迟投递。
	//
	// Phase 7-I：SendMessage MUST 先查找精确的 (ToAgentID, SubTaskID)
	// handler，再回退到仅 ToAgentID 的 handler，以保证子任务专属路由
	// 优先。
	SendMessage(msg AgentMessage)
}
