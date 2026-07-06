// Package orchestrator implements the multi-agent orchestration layer.
//
// # Architecture
//
// The orchestrator sits above the Engine and coordinates multiple agents running
// concurrently. It is responsible for:
//  1. Task decomposition — splitting a user request into sub-tasks for different agents
//  2. Agent lifecycle — starting, monitoring, and stopping agent goroutines
//  3. Event routing — ensuring each agent's events are correctly tagged with agent_id
//  4. Progress aggregation — combining progress from multiple agents into a unified view
//  5. Agent communication — one agent can call another agent via the AgentBus
//
// # Design Philosophy
//
// The orchestrator is NOT a "black box" scheduler. It emits events for every
// lifecycle transition (agent_started, agent_completed, agent_failed) so the
// frontend can render the multi-agent execution in real time. Each agent
// runs as an independent goroutine with its own Engine, sharing the WebSocket
// Hub for event broadcasting.
//
// # Agent Communication
//
// Agents can communicate with each other via the AgentBus — a thin message
// passing layer. Agent A sends a message to Agent B, the orchestrator routes
// it, and Agent B's Engine processes it as a "user" message in its ReAct loop.
// This enables patterns like:
//   - Code Review: Agent A writes code → Agent B reviews it → Agent A fixes issues
//   - Research Dispatcher: Orchestrator fans out sub-questions to research agents
//   - Supervisor: Orchestrator monitors agent outputs and intervenes if needed
//
// # Concurrency Model
//
// Each agent runs in its own goroutine. The orchestrator uses a WaitGroup to
// track completion. Agents share the WebSocket Hub (which is goroutine-safe)
// for event broadcasting. The orchestrator does NOT share state between agents
// — each agent has its own Engine, conversation history, and tool registry.
// Communication is explicit via the AgentBus.
package orchestrator

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/runtime"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// AgentSpec defines a single agent to be launched by the orchestrator.
// Each agent has its own configuration, system prompt, and task.
type AgentSpec struct {
	// AgentID is a unique identifier for this agent (e.g., "code_writer", "reviewer").
	AgentID string

	// Name is the human-readable display name (e.g., "Code Writer", "Code Reviewer").
	Name string

	// SystemPrompt defines the agent's personality, capabilities, and constraints.
	// Each agent type (writer, reviewer, researcher) has a different system prompt.
	SystemPrompt string

	// Input is the task description for this specific agent.
	Input string

	// Model is the LLM model for this agent. If empty, the orchestrator default is used.
	Model string

	// Contract is the TaskContract for this agent. Defines scope, budget, tools, etc.
	// If nil, DefaultContract(Input) is used.
	Contract *harness.TaskContract

	// AllowedTools is the list of tool names this agent is allowed to use.
	// If empty, all registered tools are available.
	AllowedTools []string

	// ParentAgentID is the agent that spawned this agent (for agent-to-agent communication).
	// Empty for root-level agents.
	ParentAgentID string

	// WorkingMemory is optional context from prior tasks, injected into the
	// system prompt before the agent starts. Built by MemoryRecall before
	// orchestration. When set, it is prepended to the system prompt.
	WorkingMemory string
}

// AgentResult holds the result of a single agent's execution.
type AgentResult struct {
	AgentID     string `json:"agent_id"`
	Name        string `json:"name"`
	Status      string `json:"status"` // "completed", "failed", "cancelled"
	Result      string `json:"result"`
	TotalTokens int    `json:"total_tokens"`
	Error       string `json:"error,omitempty"`
	Duration    int64  `json:"duration_ms"`
}

// Orchestrator manages multiple agents running concurrently.
//
// # Lifecycle
//
//  1. Create orchestrator with New()
//  2. Call Run() with a list of AgentSpecs
//  3. Orchestrator launches each agent in its own goroutine
//  4. Each agent emits events through the shared Hub
//  5. Orchestrator waits for all agents to complete
//  6. Returns aggregated results
//
// # Usage
//
//	orch := orchestrator.New(hub, cfg, tools, persist, agentBus, checkpointMgr)
//	results := orch.RunBlocking(ctx, specs)
//	for _, r := range results {
//	    fmt.Printf("%s: %s (%d tokens)\n", r.Name, r.Status, r.TotalTokens)
//	}
type Orchestrator struct {
	hub          *ws.Hub
	cfg          *config.Config
	tools        *tool.Registry
	persist      runtime.Persistence
	agentBus     *AgentBusAdapter         // Phase 5: inter-agent communication
	checkpointMgr *runtime.CheckpointManager // Phase 5: crash recovery
}

// New creates a new Orchestrator.
// agentBus and checkpointMgr may be nil — multi-agent communication and
// checkpointing are disabled when nil.
func New(hub *ws.Hub, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, agentBus *AgentBusAdapter, checkpointMgr *runtime.CheckpointManager) *Orchestrator {
	return &Orchestrator{
		hub:          hub,
		cfg:          cfg,
		tools:        tools,
		persist:      persist,
		agentBus:     agentBus,
		checkpointMgr: checkpointMgr,
	}
}

// RunBlocking launches all agents concurrently and blocks until they all complete.
// Returns a slice of results, one per agent. The order matches the input specs.
func (o *Orchestrator) RunBlocking(ctx context.Context, taskID string, specs []AgentSpec) []AgentResult {
	results := make([]AgentResult, len(specs))
	var wg sync.WaitGroup

	for i, spec := range specs {
		wg.Add(1)
		go func(idx int, s AgentSpec) {
			defer wg.Done()
			results[idx] = o.runAgent(ctx, taskID, s)
		}(i, spec)
	}

	wg.Wait()
	return results
}

// RunWithCallback launches agents concurrently and calls onResult for each agent
// as it completes. This allows the caller to process results as they arrive
// without waiting for all agents to finish.
func (o *Orchestrator) RunWithCallback(ctx context.Context, taskID string, specs []AgentSpec, onResult func(AgentResult)) {
	var wg sync.WaitGroup

	for _, spec := range specs {
		wg.Add(1)
		go func(s AgentSpec) {
			defer wg.Done()
			result := o.runAgent(ctx, taskID, s)
			onResult(result)
		}(spec)
	}

	wg.Wait()
}

// runAgent launches a single agent and returns its result.
// This is the core method that creates and runs an Engine for a single agent spec.
func (o *Orchestrator) runAgent(ctx context.Context, taskID string, spec AgentSpec) AgentResult {
	start := time.Now()

	// Build the agent's contract
	contract := harness.DefaultContract(spec.Input)
	if spec.Contract != nil {
		contract = *spec.Contract
	}
	contract.Goal = spec.Input
	if len(spec.AllowedTools) > 0 {
		contract.AllowedTools = spec.AllowedTools
	}

	// Build PolicyGate with the full rule chain
	tokenBudgetRule := &harness.TokenBudgetRule{}
	policyChain := harness.NewPolicyChain(
		&harness.PathTraversalRule{},
		&harness.FileScopeRule{},
		tokenBudgetRule,
		&harness.ToolWhitelistRule{},
	)
	policyGate := harness.NewPolicyGate(policyChain, contract)

	// Progress tracking
	progressManager := harness.NewProgressManager()

	// Use the agent's model if specified, otherwise use the default
	model := spec.Model
	if model == "" {
		model = o.cfg.LLMModel
	}

	engine := runtime.NewEngine(runtime.EngineConfig{
		AgentID:          spec.AgentID,
		SystemPrompt:     spec.SystemPrompt,
		Model:            model,
		Endpoint:         o.cfg.LLMEndpoint,
		APIKey:           o.cfg.LLMAPIKey,
		Temperature:      0.7,
		MaxTokens:        4096,
		MaxSteps:         contract.MaxSteps,
		Persistence:      o.persist,
		PolicyGate:       policyGate,
		Progress:         progressManager,
		Contract:         contract,
		WorkingMemory:    spec.WorkingMemory, // Phase 6: 工作记忆注入
		AgentBus:         o.agentBus,         // Phase 5: 多Agent通信
		CheckpointManager: o.checkpointMgr,   // Phase 5: 崩溃恢复
	}, o.tools, &hubAdapter{hub: o.hub}, taskID)

	// Emit agent_started event for the orchestrator to track
	o.hub.SendEvent(event.NewEvent("agent_ready", taskID, spec.AgentID, 0, map[string]any{
		"agent_name":    spec.Name,
		"model":         model,
		"max_steps":     contract.MaxSteps,
		"parent_agent":  spec.ParentAgentID,
		"allowed_tools": spec.AllowedTools,
	}))

	// Persist task creation for this agent
	if o.persist != nil {
		o.persist.SaveTask(taskID+"_"+spec.AgentID, spec.Input, []string{spec.AgentID})
	}

	// Run the engine
	result, totalTokens, err := engine.Run(ctx, spec.Input)

	duration := time.Since(start).Milliseconds()

	if err != nil {
		log.Printf("[Orchestrator] Agent %s (%s) failed: %v", spec.AgentID, spec.Name, err)
		o.hub.SendEvent(event.NewEvent("task_failed", taskID, spec.AgentID, 0, map[string]any{
			"reason":       err.Error(),
			"agent_name":   spec.Name,
			"total_tokens": totalTokens,
			"duration_ms":  duration,
		}))
		return AgentResult{
			AgentID:     spec.AgentID,
			Name:        spec.Name,
			Status:      "failed",
			Result:      result,
			TotalTokens: totalTokens,
			Error:       err.Error(),
			Duration:    duration,
		}
	}

	log.Printf("[Orchestrator] Agent %s (%s) completed: %d tokens, %dms",
		spec.AgentID, spec.Name, totalTokens, duration)

	o.hub.SendEvent(event.NewEvent("task_completed", taskID, spec.AgentID, 0, map[string]any{
		"result":       result,
		"agent_name":   spec.Name,
		"total_tokens": totalTokens,
		"duration_ms":  duration,
	}))

	return AgentResult{
		AgentID:     spec.AgentID,
		Name:        spec.Name,
		Status:      "completed",
		Result:      result,
		TotalTokens: totalTokens,
		Duration:    duration,
	}
}

// hubAdapter adapts ws.Hub to the runtime.EventBus interface.
// This is shared across all agents — the Hub is goroutine-safe.
type hubAdapter struct {
	hub *ws.Hub
}

func (a *hubAdapter) SendEvent(evt event.Event) {
	a.hub.SendEvent(evt)
}

// ============================================================================
// AgentBus — inter-agent communication
// ============================================================================

// AgentMessage is a message sent from one agent to another.
// It carries the sender's identity, the message content, and optional metadata.
type AgentMessage struct {
	// FromAgentID is the agent that sent the message.
	FromAgentID string `json:"from_agent_id"`

	// ToAgentID is the target agent. If empty, the message is broadcast to all agents.
	ToAgentID string `json:"to_agent_id"`

	// Type describes the message type: "request", "response", "observation", "error"
	Type string `json:"type"`

	// Content is the message body.
	Content string `json:"content"`

	// Metadata carries arbitrary key-value pairs for context.
	Metadata map[string]string `json:"metadata,omitempty"`

	// Timestamp is when the message was sent.
	Timestamp time.Time `json:"timestamp"`
}

// AgentBus is the inter-agent communication channel.
// It allows agents to send messages to each other during execution.
// The bus is goroutine-safe and can be shared across all agents.
type AgentBus struct {
	mu       sync.RWMutex
	handlers map[string]func(AgentMessage) // agentID → message handler
	queue    []AgentMessage                 // pending messages for agents not yet registered
	maxQueue int                            // max pending messages
}

// NewAgentBus creates a new AgentBus with a default queue size of 100.
func NewAgentBus() *AgentBus {
	return &AgentBus{
		handlers: make(map[string]func(AgentMessage)),
		maxQueue: 100,
	}
}

// RegisterHandler registers a message handler for a specific agent.
// When a message arrives addressed to this agent, the handler is called.
func (b *AgentBus) RegisterHandler(agentID string, handler func(AgentMessage)) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.handlers[agentID] = handler

	// Deliver any pending messages for this agent
	for i, msg := range b.queue {
		if msg.ToAgentID == agentID {
			handler(msg)
			b.queue = append(b.queue[:i], b.queue[i+1:]...)
			break
		}
	}
}

// UnregisterHandler removes a message handler for a specific agent.
func (b *AgentBus) UnregisterHandler(agentID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.handlers, agentID)
}

// SendMessage sends a message from one agent to another.
// If the target agent has a registered handler, the handler is called immediately.
// Otherwise, the message is queued for later delivery.
func (b *AgentBus) SendMessage(msg AgentMessage) {
	msg.Timestamp = time.Now()

	b.mu.RLock()
	handler, ok := b.handlers[msg.ToAgentID]
	b.mu.RUnlock()

	if ok {
		// Deliver immediately
		handler(msg)
		return
	}

	// Queue for later delivery
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.queue) >= b.maxQueue {
		// Drop oldest message to prevent unbounded growth
		b.queue = b.queue[1:]
	}
	b.queue = append(b.queue, msg)
}

// ============================================================================
// TaskDecomposer — splits a user request into sub-tasks
// ============================================================================

// TaskDecomposer splits a complex user request into sub-tasks for multiple agents.
// This is a simple rule-based approach — future versions may use an LLM to
// decompose tasks dynamically.
type TaskDecomposer struct{}

// DecomposeResult holds the decomposed task specification.
type DecomposeResult struct {
	// Agents is the list of agent specs to run.
	Agents []AgentSpec

	// Strategy describes how the agents should be coordinated:
	//   "parallel" — all agents run independently
	//   "sequential" — agents run in order, each seeing the previous agent's output
	//   "pipeline" — agents pass data through a chain (A → B → C)
	Strategy string
}

// Decompose splits a user request into agent specs based on the case type.
// For now, the decomposition is based on the preset case definition.
// In Phase 5+, this will use an LLM-based decomposition.
func (td *TaskDecomposer) Decompose(input string, caseType string) *DecomposeResult {
	switch caseType {
	case "multi_agent":
		// Multi-agent case: split into researcher + writer + reviewer
		return &DecomposeResult{
			Strategy: "pipeline",
			Agents: []AgentSpec{
				{
					AgentID: "agent_researcher",
					Name:    "Researcher",
					SystemPrompt: "You are a research agent. Your job is to gather information, " +
						"analyze facts, and provide a structured research summary. " +
						"Use web_search and read_file tools to gather data. " +
						"Output your findings as a clear, structured report.",
					Input: "Research the following topic: " + input + ". Provide a structured summary of findings.",
				},
				{
					AgentID: "agent_writer",
					Name:    "Writer",
					SystemPrompt: "You are a technical writer. Your job is to take research findings " +
						"and produce a well-structured, clear, and engaging document. " +
						"Use write_file to save your output.",
					Input:        "Based on the provided research, write a comprehensive report.",
					ParentAgentID: "agent_researcher",
				},
			},
		}

	case "code_gen":
		// Code generation: single agent with code-gen tools
		return &DecomposeResult{
			Strategy: "parallel",
			Agents: []AgentSpec{
				{
					AgentID: "agent_coder",
					Name:    "Code Generator",
					SystemPrompt: "You are a code generation agent. Write clean, well-documented code. " +
						"Always include tests and explanations.",
					Input:        input,
					AllowedTools: []string{"write_file", "read_file", "run_shell"},
				},
			},
		}

	default:
		// Default: single agent
		return &DecomposeResult{
			Strategy: "parallel",
			Agents: []AgentSpec{
				{
					AgentID: "agent_default",
					Name:    "Default Agent",
					SystemPrompt: "You are a helpful AI assistant with access to tools. " +
						"When you need to run commands, read files, or write files, use the available tools.",
					Input: input,
				},
			},
		}
	}
}

// ============================================================================
// Multi-Agent Preset Cases
// ============================================================================

// MultiAgentSpecs returns predefined multi-agent task specifications.
// These are used by the /api/cases endpoint and the frontend case cards.
func MultiAgentSpecs() []AgentSpec {
	return []AgentSpec{
		{
			AgentID: "agent_researcher",
			Name:    "Research Agent",
			SystemPrompt: "You are a research agent. Gather information, analyze facts, and " +
				"provide structured summaries. Be thorough and cite sources.",
			AllowedTools: []string{"read_file", "write_file", "run_shell"},
		},
		{
			AgentID: "agent_coder",
			Name:    "Code Agent",
			SystemPrompt: "You are a code generation agent. Write clean, well-tested code. " +
				"Always include error handling and documentation.",
			AllowedTools: []string{"write_file", "read_file", "run_shell"},
		},
		{
			AgentID: "agent_reviewer",
			Name:    "Review Agent",
			SystemPrompt: "You are a code review agent. Review code for correctness, " +
				"security, performance, and style. Provide constructive feedback.",
			AllowedTools: []string{"read_file", "write_file", "run_shell"},
		},
	}
}

// AgentColors maps agent roles to display colors for the frontend.
// Colors are used to distinguish agents in the multi-tree view.
var AgentColors = map[string]string{
	"agent_default":    "#4a9eff", // blue
	"agent_researcher": "#51cf66", // green
	"agent_coder":      "#f0a030", // orange
	"agent_writer":     "#9b59b6", // purple
	"agent_reviewer":   "#e74c3c", // red
}

// AgentColor returns the display color for an agent, or a default gray.
func AgentColor(agentID string) string {
	if c, ok := AgentColors[agentID]; ok {
		return c
	}
	return "#888888"
}