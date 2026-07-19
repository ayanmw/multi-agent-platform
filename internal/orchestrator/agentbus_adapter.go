// Package orchestrator — AgentBus adapter，用于适配 runtime 接口。
//
// # Design Rationale(设计理由)
//
// orchestrator 包定义了自己的 AgentMessage 类型（带 Timestamp 字段）和
// AgentBus 结构体。runtime 包定义了一个 AgentBus interface 以及它自己的
// AgentMessage 类型（不带 Timestamp）。本 adapter 桥接两者，让
// orchestrator 的 AgentBus 能够满足 runtime.AgentBus interface。
//
// 这样可以避免循环引用：orchestrator 引用了 runtime，因此 runtime 不能
// 反过来引用 orchestrator。runtime 侧的 interface 非常精简，由 adapter
// 在两种 message 类型之间做转换。
//
// # Usage(用法)
//
//	bus := orchestrator.NewAgentBus()
//	adapter := orchestrator.NewAgentBusAdapter(bus)
//	// adapter 实现了 runtime.AgentBus
//	engine := runtime.NewEngine(runtime.EngineConfig{
//	    AgentBus: adapter,
//	    // ...
//	}, ...)
package orchestrator

import (
	"github.com/anmingwei/multi-agent-platform/internal/runtime"
)

// AgentBusAdapter 包装 orchestrator 的 AgentBus，用于实现 runtime.AgentBus
// interface。它在两种 message 类型之间做转换：把 runtime.AgentMessage 映射到
// orchestrator.AgentMessage，反之亦然。
//
// Timestamp 字段由 orchestrator 的 SendMessage 方法负责设置，因此
// runtime.AgentMessage 类型不携带该字段。Metadata（尤其是
// Metadata["task_id"]，由 Engine.sendAgentMessage 写入）会被透传，这样通过
// AgentBus.SetPersistFn 安装的持久化 hook 就能把每条消息路由到正确的
// agent_messages 行。
type AgentBusAdapter struct {
	bus *AgentBus
}

// NewAgentBusAdapter 创建一个新的 adapter，包装传入的 orchestrator AgentBus。
// 该 adapter 实现了 runtime.AgentBus，因此可以传给 EngineConfig.AgentBus。
func NewAgentBusAdapter(bus *AgentBus) *AgentBusAdapter {
	return &AgentBusAdapter{bus: bus}
}

// RegisterHandler 为指定 agent 注册一个使用 runtime.AgentMessage 类型的
// message handler。adapter 会先把 orchestrator.AgentMessage 转换为
// runtime.AgentMessage，然后再调用 handler。
func (a *AgentBusAdapter) RegisterHandler(agentID string, handler func(runtime.AgentMessage)) {
	// 包装 runtime handler，把 orchestrator.AgentMessage 转换为
	// runtime.AgentMessage。Timestamp 被丢弃，因为 runtime.AgentMessage
	// 没有该字段；Metadata 会被透传。
	a.bus.RegisterHandler(agentID, func(msg AgentMessage) {
		handler(runtime.AgentMessage{
			FromAgentID:   msg.FromAgentID,
			FromSubTaskID: msg.FromSubTaskID,
			ToAgentID:     msg.ToAgentID,
			SubTaskID:     msg.SubTaskID,
			Type:          msg.Type,
			Content:       msg.Content,
			Metadata:      msg.Metadata,
		})
	})
}

// RegisterHandlerBySubTask 在 adapter 层面实现 runtime.AgentBus 的按 SubTask 注册。
// 当 subTaskID 为空时委托给 RegisterHandler，否则注册 (agentID, subTaskID) 精确 handler。
func (a *AgentBusAdapter) RegisterHandlerBySubTask(agentID, subTaskID string, handler func(runtime.AgentMessage)) {
	if subTaskID == "" {
		a.RegisterHandler(agentID, handler)
		return
	}
	a.bus.RegisterHandlerBySubTask(agentID, subTaskID, func(msg AgentMessage) {
		handler(runtime.AgentMessage{
			FromAgentID:   msg.FromAgentID,
			FromSubTaskID: msg.FromSubTaskID,
			ToAgentID:     msg.ToAgentID,
			SubTaskID:     msg.SubTaskID,
			Type:          msg.Type,
			Content:       msg.Content,
			Metadata:      msg.Metadata,
		})
	})
}

// UnregisterHandler 移除指定 agent 的 message handler。
func (a *AgentBusAdapter) UnregisterHandler(agentID string) {
	a.bus.UnregisterHandler(agentID)
}

// UnregisterHandlerBySubTask 移除 (agentID, subTaskID) handler。
// subTaskID 为空时委托给 UnregisterHandler。
func (a *AgentBusAdapter) UnregisterHandlerBySubTask(agentID, subTaskID string) {
	a.bus.UnregisterHandlerBySubTask(agentID, subTaskID)
}

// SendMessage 从一个 agent 向另一个 agent 发送消息。adapter 会先把
// runtime.AgentMessage 转换为 orchestrator.AgentMessage 再发送。
// Timestamp 由 orchestrator 的 SendMessage 方法负责设置，Metadata 会被
// 透传，这样下游的持久化逻辑可以按 task_id 路由。
func (a *AgentBusAdapter) SendMessage(msg runtime.AgentMessage) {
	a.bus.SendMessage(AgentMessage{
		FromAgentID:   msg.FromAgentID,
		FromSubTaskID: msg.FromSubTaskID,
		ToAgentID:     msg.ToAgentID,
		SubTaskID:     msg.SubTaskID,
		Type:          msg.Type,
		Content:       msg.Content,
		Metadata:      msg.Metadata,
	})
}
