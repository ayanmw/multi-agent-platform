// Package runtime — AgentBus interface for inter-agent communication.
//
// # Design Rationale
//
// The AgentBus interface allows agents to send messages to each other during
// execution. It is defined in the runtime package (not orchestrator) to avoid
// circular imports: orchestrator imports runtime, so runtime cannot import
// orchestrator.
//
// The orchestrator package provides the concrete implementation (AgentBus struct)
// and an adapter that implements this interface, converting between the two
// message types.
//
// # Communication Patterns
//
// Agents can communicate via the AgentBus in several patterns:
//   - Request/Response: Agent A sends a request to Agent B, B responds
//   - Observation: Agent A sends an observation to Agent B for context
//   - Broadcast: Agent A sends a message to all agents (ToAgentID empty)
//   - Error: Agent A reports an error to Agent B
//
// # Integration with ReAct Loop
//
// When an Engine has an AgentBus, it starts a goroutine in Run() that listens
// for incoming messages. When a message arrives, it is appended to the
// conversation as a user message: "[Agent {from}]: {content}". The LLM sees
// this as a new user input and can respond accordingly.
//
// # SubTask-aware Routing (Phase 7-I)
//
// Starting with Phase 7-I, messages can optionally carry a SubTaskID. This
// allows the same agent ID to run multiple concurrent sub-tasks (e.g. the
// leader agent orchestrating different groups of workers) and still receive
// messages targeted at the correct sub-task. Implementations should deliver
// messages to an (agentID, subTaskID) exact handler first, then fall back to
// an agentID-only handler for backward compatibility.
package runtime

// AgentMessage is a message sent between agents via the AgentBus.
// It carries the sender's identity, the message content, and optional metadata.
type AgentMessage struct {
	// FromAgentID is the agent that sent the message.
	FromAgentID string `json:"from_agent_id"`

	// ToAgentID is the target agent. If empty, the message is broadcast to all agents.
	ToAgentID string `json:"to_agent_id"`

	// SubTaskID is the target sub-task. When set, the AgentBus should prefer
	// an exact (ToAgentID, SubTaskID) handler before falling back to a
	// ToAgentID-only handler. Added in Phase 7-I.
	SubTaskID string `json:"sub_task_id,omitempty"`

	// Type describes the message type: "request", "response", "observation", "error"
	Type string `json:"type"`

	// Content is the message body.
	Content string `json:"content"`

	// Metadata carries arbitrary key-value pairs for context.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// AgentBus is the inter-agent communication channel interface.
// It allows agents to send messages to each other during execution.
// The bus must be goroutine-safe.
//
// # Usage
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
// # Implementation
//
// The concrete implementation lives in internal/orchestrator (AgentBus struct).
// An adapter (orchestrator.agentBusAdapter) bridges the two message types.
type AgentBus interface {
	// RegisterHandler registers a message handler for a specific agent.
	// When a message arrives addressed to this agent, the handler is called.
	// Only one handler per agent is allowed; calling RegisterHandler again
	// replaces the previous handler.
	//
	// Phase 7-I: implementations SHOULD also support subTaskID-aware
	// registration, either by providing a separate RegisterHandlerBySubTask
	// method or by accepting subTaskID as a parameter. The default signature
	// keeps backward compatibility: subTaskID empty means "all sub-tasks".
	RegisterHandler(agentID string, handler func(AgentMessage))

	// RegisterHandlerBySubTask registers a handler for a specific
	// (agentID, subTaskID) pair. When SubTaskID is empty it must behave
	// identically to RegisterHandler. Added in Phase 7-I.
	RegisterHandlerBySubTask(agentID, subTaskID string, handler func(AgentMessage))

	// UnregisterHandler removes the message handler for a specific agent.
	UnregisterHandler(agentID string)

	// UnregisterHandlerBySubTask removes the handler for a specific
	// (agentID, subTaskID) pair. Added in Phase 7-I.
	UnregisterHandlerBySubTask(agentID, subTaskID string)

	// SendMessage sends a message from one agent to another.
	// If the target agent has a registered handler, the handler is called
	// immediately. Otherwise, the message is queued for later delivery.
	//
	// Phase 7-I: SendMessage MUST first look for an exact
	// (ToAgentID, SubTaskID) handler, then fall back to a ToAgentID-only
	// handler, so sub-task specific routing takes precedence.
	SendMessage(msg AgentMessage)
}
