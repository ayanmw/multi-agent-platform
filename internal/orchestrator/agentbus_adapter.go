// Package orchestrator — AgentBus adapter for runtime interface.
//
// # Design Rationale
//
// The orchestrator package defines its own AgentMessage type (with a Timestamp
// field) and AgentBus struct. The runtime package defines an AgentBus interface
// with its own AgentMessage type (without Timestamp). This adapter bridges the
// two, allowing the orchestrator's AgentBus to satisfy the runtime.AgentBus
// interface.
//
// This avoids circular imports: orchestrator imports runtime, so runtime
// cannot import orchestrator. The runtime interface is minimal and the adapter
// converts between the two message types.
//
// # Usage
//
//	bus := orchestrator.NewAgentBus()
//	adapter := orchestrator.NewAgentBusAdapter(bus)
//	// adapter implements runtime.AgentBus
//	engine := runtime.NewEngine(runtime.EngineConfig{
//	    AgentBus: adapter,
//	    // ...
//	}, ...)
package orchestrator

import (
	"github.com/anmingwei/multi-agent-platform/internal/runtime"
)

// AgentBusAdapter wraps the orchestrator's AgentBus to implement the
// runtime.AgentBus interface. It converts between the two message types,
// mapping runtime.AgentMessage to orchestrator.AgentMessage and vice versa.
//
// The Timestamp field is set by the orchestrator's SendMessage method,
// so it is not carried in the runtime.AgentMessage type.
type AgentBusAdapter struct {
	bus *AgentBus
}

// NewAgentBusAdapter creates a new adapter that wraps the given orchestrator AgentBus.
// The adapter implements runtime.AgentBus, so it can be passed to EngineConfig.AgentBus.
func NewAgentBusAdapter(bus *AgentBus) *AgentBusAdapter {
	return &AgentBusAdapter{bus: bus}
}

// RegisterHandler registers a message handler for a specific agent using the
// runtime.AgentMessage type. The adapter converts the orchestrator.AgentMessage
// to a runtime.AgentMessage before calling the handler.
func (a *AgentBusAdapter) RegisterHandler(agentID string, handler func(runtime.AgentMessage)) {
	// Wrap the runtime handler to convert from orchestrator.AgentMessage to
	// runtime.AgentMessage. The Timestamp and Metadata are dropped because
	// runtime.AgentMessage doesn't have them.
	a.bus.RegisterHandler(agentID, func(msg AgentMessage) {
		handler(runtime.AgentMessage{
			FromAgentID: msg.FromAgentID,
			ToAgentID:   msg.ToAgentID,
			Type:        msg.Type,
			Content:     msg.Content,
		})
	})
}

// UnregisterHandler removes the message handler for a specific agent.
func (a *AgentBusAdapter) UnregisterHandler(agentID string) {
	a.bus.UnregisterHandler(agentID)
}

// SendMessage sends a message from one agent to another. The adapter converts
// the runtime.AgentMessage to an orchestrator.AgentMessage before sending.
// The Timestamp is set by the orchestrator's SendMessage method.
func (a *AgentBusAdapter) SendMessage(msg runtime.AgentMessage) {
	a.bus.SendMessage(AgentMessage{
		FromAgentID: msg.FromAgentID,
		ToAgentID:   msg.ToAgentID,
		Type:        msg.Type,
		Content:     msg.Content,
	})
}