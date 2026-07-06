// Package runtime implements the core Agent execution engine — the heart of the multi-agent
// platform. It orchestrates the ReAct (Reasoning + Acting) loop that powers every agent.
//
// # Architecture Overview
//
// The runtime package sits at the center of the system, connecting three key subsystems:
//
//  1. LLM Client (internal/llm) — sends chat requests to the AI model, receives
//     streaming SSE responses with text content and tool_call deltas.
//  2. Tool Registry (internal/tool) — manages available tools; the engine builds
//     tool definitions for the LLM and dispatches tool calls to the registry.
//  3. Event Bus (pkg/event) — the real-time communication channel to the frontend
//     via WebSocket. Every state transition (thinking, tool call, observation,
//     completion, failure) is broadcast as a typed event so the UI can render
//     the agent's internal state in real time.
//
// ## The ReAct Loop
//
// The ReAct (Reasoning + Acting) loop is the decision-making cycle that every agent
// follows. It is a state machine with three phases:
//
//	┌──────────────────────────────────────────────────┐
//	│                   ReAct Loop                      │
//	│                                                   │
//	│  ┌──────────┐    tool_calls?    ┌──────────────┐ │
//	│  │  THINK   │──────────────────>│ EXECUTE_TOOL │ │
//	│  │ (LLM)    │                   │ (Registry)   │ │
//	│  └──────────┘                   └──────────────┘ │
//	│       ^                                │         │
//	│       │       observation             │          │
//	│       └────────────────────────────────┘          │
//	│                                                   │
//	│  No tool_calls? → final answer → task_completed   │
//	└──────────────────────────────────────────────────┘
//
// Phase 1 — THINK: The engine sends the conversation history (system prompt + user
// messages + assistant responses + tool results) to the LLM. The LLM streams back
// text tokens (shown to the user as typewriter effect) and may also emit tool_call
// deltas. If the LLM returns only text with no tool_calls, that text is the final
// answer — the task is complete.
//
// Phase 2 — EXECUTE_TOOL: If the LLM requests one or more tool calls, the engine
// dispatches each to the Tool Registry. The tool's result is formatted as JSON and
// appended to the conversation as a "tool" role message. The engine emits
// tool_call_started, tool_call_output, and observation events so the UI can render
// tool execution progress.
//
// Phase 3 — OBSERVE: The tool result is fed back into the conversation history.
// The loop returns to Phase 1 (THINK), where the LLM sees the observation and
// decides whether to call more tools or produce a final answer. This cycle repeats
// until either the LLM produces a final answer or MaxSteps is exceeded.
//
// ## Event-Driven Transparency (白盒Agent)
//
// The engine is designed for full observability — every internal state change is
// emitted as an event. This is the "white-box" philosophy: the frontend can see
// exactly what the agent is thinking, which tools it's calling, and what results
// it's observing. Event types:
//
//	agent_ready          — agent is initialized and ready to process input
//	step_started         — a new think or tool_call step has begun
//	llm_thinking         — the LLM is processing (before tokens arrive)
//	llm_delta            — a single token of text from the LLM (streamed)
//	llm_message_complete — the LLM has finished generating for this step
//	tool_call_started    — a tool execution has begun
//	tool_call_output     — the tool's raw result
//	tool_call_complete   — the tool execution finished successfully
//	tool_call_failed     — the tool execution failed with an error
//	observation          — the result being fed back to the LLM
//	task_completed       — the agent produced a final answer
//	task_failed          — the agent failed (error, cancellation, max steps)
//	step_complete        — a think or tool_call step has finished
//
// ## Persistence (Optional)
//
// When a Persistence implementation is provided (e.g., SQLite-backed), the engine
// saves task records, step records, and conversation messages after each phase.
// When Persistence is nil, persistence is silently skipped — this is safe for
// testing or ephemeral agent runs.
//
// ## Multi-Agent Support
//
// Each Engine instance runs a single agent's ReAct loop. Multi-agent orchestration
// happens at a higher layer (cmd/server) that creates multiple Engine instances
// and coordinates their execution. The Engine is intentionally single-agent to
// keep the ReAct loop simple and testable.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// EventBus is the real-time event transport layer that connects the Engine to the
// frontend WebSocket clients. Every state change in the ReAct loop is published
// through this interface so the UI can render agent thinking, tool execution, and
// results in real time.
//
// The EventBus is intentionally a minimal interface with only SendEvent — this
// allows the engine to work with any transport (WebSocket, gRPC stream, in-memory
// channel for testing) without coupling to a specific protocol.
//
// In the current architecture, the WebSocket hub (internal/ws) implements this
// interface and broadcasts events to all connected frontend clients.
type EventBus interface {
	// SendEvent publishes an event to all connected clients.
	// Events are typed (see the event package for the full list) and carry
	// task/agent/step metadata for the frontend to route to the correct UI panel.
	SendEvent(event.Event)
}

// EngineConfig holds all configuration needed to create and run an Engine.
// It is the single source of truth for an agent's identity, model settings,
// safety limits, and persistence backend.
//
// Design rationale: All configuration is explicit and passed at construction time
// rather than read from global state. This makes engines testable in isolation
// and enables multi-agent setups where each agent has different configs.
type EngineConfig struct {
	// AgentID is the human-readable identifier for this agent (e.g., "code-reviewer").
	// It appears in all events and is used by the frontend to label the agent.
	AgentID string

	// SystemPrompt is the system-level instruction that defines the agent's
	// personality, capabilities, and constraints. It is sent as the first message
	// in every conversation and is never trimmed from context.
	SystemPrompt string

	// Model is the LLM model name (e.g., "deepseek-v4-flash"). It is passed
	// directly to the API and must be a model supported by the configured endpoint.
	Model string

	// Endpoint is the base URL of the OpenAI-compatible API (e.g., "https://aicoding.dobest.com/v1").
	// The Engine appends "/chat/completions" to this URL for chat requests.
	Endpoint string

	// APIKey is the Bearer token for authenticating with the LLM API.
	// It is sent as the Authorization header on every request.
	//
	// Deprecated: Prefer using Provider instead. When Provider is nil, Endpoint and
	// APIKey are used to create a default OpenAIProvider. In Phase 6+, these fields
	// will be removed in favor of the Provider abstraction.
	APIKey string

	// Provider is the LLM Provider implementation. When set, it takes precedence
	// over Endpoint/APIKey/Model. This enables multi-provider support where
	// different agents use different providers (OpenAI, Anthropic, DeepSeek, etc.).
	// When nil, an OpenAIProvider is created from Endpoint, APIKey, and Model.
	// Added in Phase 5.
	Provider llm.Provider

	// Temperature controls the randomness of LLM output (0.0–2.0).
	// Lower values produce more deterministic responses; higher values produce
	// more creative/varied output. Defaults to 0.7 if not set.
	Temperature float32

	// MaxTokens is the maximum number of tokens the LLM may generate per response.
	// This acts as a safety limit to prevent runaway token consumption.
	// Defaults to 4096 if not set.
	MaxTokens int

	// MaxSteps is the maximum number of ReAct loop iterations before the engine
	// forcibly terminates. This prevents infinite loops where the LLM keeps
	// calling tools without ever producing a final answer. Defaults to 10.
	MaxSteps int

	// Persistence is an optional backend for saving task/step/conversation records.
	// When nil, persistence is silently skipped — useful for testing or ephemeral runs.
	// When set (e.g., to a SQLite-backed implementation), every step and message
	// is durably stored for later audit and replay.
	Persistence Persistence

	// PolicyGate is the optional Harness policy enforcement layer. When set,
	// every tool call is checked against the policy chain before execution.
	// When nil, policy enforcement is skipped — all tool calls are allowed.
	// See internal/harness for the full PolicyGate implementation.
	PolicyGate *harness.PolicyGate

	// ProgressManager is the optional Harness progress tracking. When set,
	// progress nodes are written at key milestones (tool calls, step completions,
	// task completion) to an external progress file that survives crashes.
	Progress *harness.ProgressManager

	// TaskContract is the structured task definition that defines scope,
	// permissions, budget, and acceptance criteria for this task.
	// Used by PolicyGate for enforcement and Progress for tracking.
	Contract harness.TaskContract

	// SessionID identifies the session this task belongs to.
	// Empty for tasks not yet associated with a session.
	SessionID string

	// ParentTaskID identifies the parent task for sub-tasks spawned by agents.
	// Empty for root tasks.
	ParentTaskID string

	// IsRoot indicates whether this task is the root task of its session.
	// Root tasks represent the primary user request; child tasks represent
	// sub-agent work delegated from the root.
	IsRoot bool

	// ApprovalHandler is the optional Harness approval handler. When set,
	// the Engine can handle ErrApprovalRequired errors from the PolicyGate
	// by sending approval requests to the frontend and waiting for user decisions.
	// When nil, ErrApprovalRequired errors cause immediate task failure.
	// See internal/harness for the ApprovalHandler interface.
	// Added in Phase 5.
	ApprovalHandler harness.ApprovalHandler
}

// Engine executes the ReAct (Reasoning + Acting) loop for a single agent.
//
// # Lifecycle
//
// An Engine is created via NewEngine, then Run() is called once with user input.
// The engine processes the input through the ReAct loop and returns the final
// answer (or an error). After Run() returns, the Engine should not be reused —
// create a new Engine for each task.
//
// # State
//
// The Engine maintains the full conversation history as a slice of llm.Message.
// This includes:
//   - system: the agent's system prompt (set once at creation)
//   - user: the initial user input + any intermediate user messages
//   - assistant: LLM responses (text content + tool calls)
//   - tool: tool execution results (JSON-serialized)
//
// The stepIdx counter tracks the current ReAct loop iteration. It starts at 0
// and increments after each tool execution. The engine terminates when stepIdx
// reaches MaxSteps or the LLM produces a final answer.
//
// # Event Flow
//
// Every significant state change is emitted as an event through the EventBus:
//
//	agent_ready           → step_started → llm_thinking → llm_delta* →
//	llm_message_complete  → step_complete → [tool_call_started → tool_call_output →
//	tool_call_complete → observation → step_complete]* → task_completed
//
// The * suffix indicates events that may repeat multiple times (streaming tokens,
// multiple tool calls, multiple loop iterations).
type Engine struct {
	cfg             EngineConfig              // immutable configuration set at creation
	llm             llm.Provider               // LLM Provider interface (abstracts API protocol)
	tools           *tool.Registry             // the tool registry shared across agents
	bus             EventBus                   // event transport for real-time frontend updates
	persist         Persistence                // optional persistence backend (nil = no persistence)
	gate            *harness.PolicyGate        // optional policy enforcement (nil = allow all)
	progress        *harness.ProgressManager   // optional progress tracking (nil = skip)
	taskProgress    *harness.TaskProgress      // current progress state (nil if progress is nil)
	taskID          string                     // unique task identifier for correlation
	messages        []llm.Message              // full conversation history (system + user + assistant + tool)
	stepIdx         int                        // current ReAct loop iteration (0-based)
	totalTokens     int                        // cumulative total tokens across all LLM calls
	tokenUsage      llm.Usage                  // cumulative detailed token usage (input/cache/output)
	approvalHandler harness.ApprovalHandler    // optional approval handler for ErrApprovalRequired
}

// NewEngine creates a new Engine with the given configuration, tool registry,
// event bus, and task ID.
//
// Defaults applied:
//   - MaxSteps defaults to 10 (prevents infinite loops)
//   - Temperature defaults to 0.7 (balanced creativity vs. determinism)
//   - MaxTokens defaults to 4096 (reasonable safety limit for most models)
//
// The engine initializes the conversation with the system prompt as the first
// message. The user's input will be appended when Run() is called.
//
// The LLM provider is created per-engine (not shared) so that each agent can use
// a different endpoint, API key, or model — this is essential for multi-agent
// setups where different agents may talk to different LLM providers.
//
// If cfg.Provider is set, it is used directly (enabling custom providers).
// Otherwise, an OpenAIProvider is created from cfg.Endpoint, cfg.APIKey, cfg.Model.
func NewEngine(cfg EngineConfig, tools *tool.Registry, bus EventBus, taskID string) *Engine {
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 10
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.7
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}

	// Resolve the LLM Provider: use the explicit Provider if set, otherwise
	// create a default OpenAIProvider from the legacy Endpoint/APIKey/Model fields.
	provider := cfg.Provider
	if provider == nil {
		provider = llm.NewOpenAIProvider("openai", cfg.Endpoint, cfg.APIKey, cfg.Model)
	}

	return &Engine{
		cfg:             cfg,
		llm:             provider,
		tools:           tools,
		bus:             bus,
		persist:         cfg.Persistence,
		gate:            cfg.PolicyGate,   // nil = no policy enforcement
		progress:        cfg.Progress,     // nil = no progress tracking
		taskID:          taskID,
		messages: []llm.Message{
			{Role: "system", Content: cfg.SystemPrompt},
		},
		stepIdx:         0,
		totalTokens:     0,
		tokenUsage:      llm.Usage{},
		approvalHandler: cfg.ApprovalHandler, // nil = approval not supported
	}
}

// Run executes the ReAct loop for the given user input and returns the final
// answer, total tokens consumed, and any error.
//
// # The ReAct Loop (step-by-step)
//
// The loop runs until one of three termination conditions is met:
//  1. The LLM returns a response with no tool_calls → final answer (success)
//  2. stepIdx reaches MaxSteps → forced termination (failure)
//  3. The context is cancelled → graceful shutdown (failure)
//  4. A panic is recovered → emergency shutdown (failure)
//
// Between each iteration, the context is checked for cancellation. This allows
// the caller to cancel a long-running agent (e.g., user clicks "stop" in the UI).
//
// # Panic Recovery
//
// The engine includes a defer recover() at the top of Run() to catch panics
// from any layer (LLM client, tool execution, event bus, persistence). When a
// panic is caught, the engine emits a task_failed event with the panic details
// so the frontend can display the error, then re-panics to preserve the stack
// trace for debugging. This ensures that a single buggy tool or nil pointer
// doesn't silently kill the agent — the frontend always knows what happened.
//
// # Return Values
//
//   - content: the final answer text from the LLM (empty on failure)
//   - totalTokens: total tokens consumed across all LLM calls (0 on failure)
//   - error: nil on success, descriptive error on failure
func (e *Engine) Run(ctx context.Context, userInput string) (content string, totalTokens int, err error) {
	// Panic recovery: catch any panic from the LLM client, tool execution, event
	// bus, or persistence layer. Emit a task_failed event so the frontend knows
	// the agent crashed, then re-panic to preserve the stack trace.
	defer func() {
		if r := recover(); r != nil {
			// Emit task_failed with the panic details so the UI can display the error.
			// The event is sent on a best-effort basis — if the event bus itself
			// panicked, this send may also fail, but we try anyway.
			e.bus.SendEvent(event.NewEvent("task_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"reason": "panic",
				"error":  fmt.Sprintf("%v", r),
			}))
			// Persist the failure status so the task history shows it as failed.
			e.updateTask("failed", "", e.totalTokens)
			// Re-panic to preserve the original stack trace for server-side logging.
			// The panic will be caught by the HTTP server's recovery middleware
			// or the caller's deferred recovery.
			panic(r)
		}
	}()

	// Append the user's input to the conversation history. This is the starting
	// point for the ReAct loop — the LLM will see the system prompt followed by
	// this user message.
	e.messages = append(e.messages, llm.Message{Role: "user", Content: userInput})

	// Persist the user message for audit trail and conversation replay.
	e.saveConversation("user", userInput)

	// Init Harness progress tracking if configured
	if e.progress != nil {
		tp, err := e.progress.Init(e.taskID, e.cfg.Contract)
		if err != nil {
			log.Printf("[Engine] Progress init failed: %v (continuing)", err)
		} else {
			e.taskProgress = tp
		}
	}

	// Notify the frontend that the agent is initialized and ready to process.
	// The UI uses this event to show the agent's name, model, and limits.
	e.bus.SendEvent(event.NewEvent("agent_ready", e.taskID, e.cfg.AgentID, 0, map[string]any{
		"agent_name": e.cfg.AgentID,
		"model":      e.cfg.Model,
		"max_steps":  e.cfg.MaxSteps,
		"session_id": e.cfg.SessionID,
		"is_root":    e.cfg.IsRoot,
	}))

	// =========================================================================
	// REACT LOOP: THINK → TOOL_CALL → OBSERVE → (repeat)
	// =========================================================================
	// Each iteration of this loop is one "step" in the agent's reasoning chain.
	// The loop terminates when the LLM produces a final answer (no tool calls),
	// when MaxSteps is reached, or when the context is cancelled.
	//
	// The stepIdx counter is NOT incremented during the think phase — it is
	// incremented only after a tool is executed. This means stepIdx reflects
	// the number of tool execution rounds, not the number of LLM calls.
	// The final answer (think phase with no tool calls) uses the current stepIdx
	// without incrementing it.
	for e.stepIdx < e.cfg.MaxSteps {
		// Check context cancellation before each iteration. This allows the
		// HTTP handler to cancel the agent when the client disconnects or the
		// user clicks "stop". Without this check, the engine would continue
		// processing even after the frontend has given up.
		select {
		case <-ctx.Done():
			// Context was cancelled — emit failure and return immediately.
			// The frontend can distinguish "cancelled" from "llm_error" and
			// "max_steps_exceeded" by the reason field in the event data.
			e.bus.SendEvent(event.NewEvent("task_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"reason": "cancelled",
			}))
			e.updateTask("failed", "", 0)
			return "", e.totalTokens, ctx.Err()
		default:
			// Context is still valid — continue to the think phase.
		}

		// =====================================================================
		// PHASE 1: THINK — Send the conversation to the LLM and get the response.
		// =====================================================================
		// The LLM receives the full conversation history (system + user +
		// assistant + tool messages) and returns either:
		//   a) Text content with no tool calls → this is the final answer
		//   b) Text content with tool calls → the LLM wants to use a tool
		//
		// During this phase, text tokens are streamed to the frontend via the
		// llm_delta event, creating a typewriter effect in the UI.
		content, usage, toolCalls, err := e.think(ctx)
		if err != nil {
			e.bus.SendEvent(event.NewEvent("task_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"reason": "llm_error",
				"error":  err.Error(),
			}))
			e.updateTask("failed", "", e.totalTokens)
			return "", e.totalTokens, fmt.Errorf("think step %d: %w", e.stepIdx, err)
		}

		// Accumulate token usage for budget tracking (TokenBudgetRule — Phase 4)
		e.totalTokens += usage.TotalTokens
		e.tokenUsage.PromptTokens += usage.PromptTokens
		e.tokenUsage.PromptCacheHitTokens += usage.PromptCacheHitTokens
		e.tokenUsage.PromptCacheMissTokens += usage.PromptCacheMissTokens
		e.tokenUsage.CompletionTokens += usage.CompletionTokens
		e.tokenUsage.TotalTokens += usage.TotalTokens

		// Update the PolicyGate with the latest token usage so TokenBudgetRule
		// can enforce the budget before the next tool execution.
		if e.gate != nil {
			e.gate.SetTokenUsage(e.totalTokens)
		}

		// Emit an agent_status event so the frontend can display real-time
		// token consumption detail (input / cache / output) and progress.
		e.bus.SendEvent(event.NewEvent("agent_status", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"prompt_tokens":            e.tokenUsage.PromptTokens,
			"prompt_cache_hit_tokens":  e.tokenUsage.PromptCacheHitTokens,
			"prompt_cache_miss_tokens": e.tokenUsage.PromptCacheMissTokens,
			"completion_tokens":        e.tokenUsage.CompletionTokens,
			"total_tokens":             e.tokenUsage.TotalTokens,
			"max_steps":                e.cfg.MaxSteps,
			"current_step":             e.stepIdx,
		}))

		log.Printf("[Engine] Step %d: content=%d chars, toolCalls=%d, usage=%+v",
			e.stepIdx, len(content), len(toolCalls), usage)

		// =====================================================================
		// CHECK: Did the LLM produce a final answer or request tool calls?
		// =====================================================================
		// If there are no tool calls, the LLM's text content is the final answer.
		// This is the normal termination path for a successful agent run.
		if len(toolCalls) == 0 {
			// Persist the final step for audit trail.
			e.saveStep(StepRecord{
				TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
				Type: "think", Status: "completed", Content: content, TokenUsed: usage.TotalTokens,
			})
			e.saveConversation("assistant", content)

			// Emit the final observation — the complete answer text along with
			// token usage statistics. The frontend uses this to display the
			// final answer and token cost summary.
			e.bus.SendEvent(event.NewEvent("observation", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"content":           content,
				"total_tokens":      usage.TotalTokens,
				"prompt_tokens":     usage.PromptTokens,
				"completion_tokens": usage.CompletionTokens,
			}))

			// Emit task_completed — this tells the frontend that the agent
			// has finished successfully. The UI transitions from the "running"
			// state to the "completed" state and shows the final result.
			e.bus.SendEvent(event.NewEvent("task_completed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"result":       content,
				"total_tokens": usage.TotalTokens,
				"total_steps":  e.stepIdx,
			}))

			// Persist the completed status. The task record now has the final
			// result text and total token count for cost tracking.
			e.updateTask("completed", content, usage.TotalTokens)
			return content, usage.TotalTokens, nil
		}

		// =====================================================================
		// PHASE 2: EXECUTE_TOOL — Run each tool call requested by the LLM.
		// =====================================================================
		// The LLM may request multiple tool calls in a single response. Each
		// tool call is executed sequentially — the result of tool N is available
		// to the LLM when it processes tool N+1's result on the next think phase.
		//
		// After all tool calls are executed, the loop returns to PHASE 1 (THINK)
		// where the LLM sees the tool results and decides what to do next.
		for _, tc := range toolCalls {
			// Persist the think step BEFORE executing the tool. This ensures
			// that even if the tool execution crashes, the audit trail shows
			// what the LLM was thinking when it decided to call the tool.
			e.saveStep(StepRecord{
				TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
				Type: "think", Status: "completed", Content: content, TokenUsed: usage.TotalTokens,
			})
			e.saveConversation("assistant", content)

			// Execute the tool. The engine dispatches the tool call to the
			// Tool Registry, which looks up the tool by name and invokes its
			// Execute method. The result is a JSON-serializable value.
			//
			// stepIdx is incremented INSIDE executeTool (not here) because
			// executeTool manages the step lifecycle events (started/completed).
			result, toolErr := e.executeTool(tc)
			if toolErr != nil {
				// Tool execution failed — this could be a tool-not-found error,
				// a parameter validation error, or a runtime error inside the tool.
				// Emit failure and return. The frontend will show which tool
				// failed and the error message.
				e.bus.SendEvent(event.NewEvent("task_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
					"reason":    "tool_error",
					"tool_name": tc.Function.Name,
					"error":     toolErr.Error(),
				}))
				e.updateTask("failed", "", e.totalTokens)
				return "", e.totalTokens, fmt.Errorf("tool %s: %w", tc.Function.Name, toolErr)
			}

			// Persist the tool result for audit trail.
			e.saveConversation("tool", result)

			// =================================================================
			// PHASE 3: OBSERVE — Feed the tool result back into the conversation.
			// =================================================================
			// The assistant message (with the tool_call) and the tool result
			// message are appended to the conversation history. On the next
			// loop iteration, the LLM will see these messages and can use the
			// tool result to inform its next response.
			//
			// This is what makes the ReAct loop work: the LLM sees the
			// consequences of its actions and adapts accordingly.
			e.messages = append(e.messages, llm.Message{
				Role:      "assistant",
				Content:   content,
				ToolCalls: []llm.ToolCall{tc},
			})
			e.messages = append(e.messages, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
		}
		// Loop back to PHASE 1 (THINK) — the LLM will now see the tool results
		// and decide whether to call more tools or produce a final answer.
	}

	// =========================================================================
	// MaxSteps exceeded — the agent did not produce a final answer within the
	// allowed number of iterations. This is a safety mechanism to prevent
	// infinite loops (e.g., the LLM keeps calling the same tool with the same
	// arguments without making progress).
	e.bus.SendEvent(event.NewEvent("task_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"reason":       "max_steps_exceeded",
		"max_steps":    e.cfg.MaxSteps,
		"current_step": e.stepIdx,
		"total_tokens": e.totalTokens,
	}))
	e.updateTask("failed", "", e.totalTokens)
	return "", e.totalTokens, fmt.Errorf("max steps (%d) exceeded", e.cfg.MaxSteps)
}

// saveConversation persists a conversation message to the storage backend.
//
// This is a no-op when persistence is nil (e.g., in tests or ephemeral runs).
// Errors are logged but not returned — persistence failures are non-fatal to
// the agent's execution. The agent continues processing even if the database
// is unavailable, because the primary goal is to complete the user's task.
//
// Design rationale: persistence is a cross-cutting concern that should not
// interrupt the agent's core loop. If the database is down, we log the error
// and move on — the task still completes, just without an audit trail.
func (e *Engine) saveConversation(role, content string) {
	if e.persist == nil {
		return
	}
	if err := e.persist.SaveConversation(ConversationRecord{
		TaskID: e.taskID, Role: role, Content: content,
	}); err != nil {
		log.Printf("[Engine] Failed to save conversation: %v", err)
	}
}

// saveStep persists a step record to the storage backend.
//
// Each step represents one phase of the ReAct loop (think or tool_call).
// Steps are persisted with their status (completed/failed), content, and
// token usage for cost tracking and audit.
//
// Like saveConversation, this is a no-op when persistence is nil and errors
// are logged but not returned — persistence failures do not interrupt the agent.
func (e *Engine) saveStep(s StepRecord) {
	if e.persist == nil {
		return
	}
	if err := e.persist.SaveStep(s); err != nil {
		log.Printf("[Engine] Failed to save step: %v", err)
	}
}

// updateTask persists the final task status to the storage backend.
//
// Called when the task reaches a terminal state (completed or failed).
// The status, final result text, and total token count are written to the
// task record so the task history UI can display task outcomes and costs.
//
// Like saveConversation and saveStep, this is a no-op when persistence is nil.
func (e *Engine) updateTask(status, finalResult string, totalTokens int) {
	if e.persist == nil {
		return
	}
	if err := e.persist.UpdateTask(e.taskID, status, finalResult, totalTokens); err != nil {
		log.Printf("[Engine] Failed to update task: %v", err)
	}
}

// think sends the current conversation history to the LLM and returns the
// accumulated text content, token usage, any tool calls, and an error.
//
// # How it works
//
//  1. Emits step_started and llm_thinking events so the UI shows the agent is
//     actively processing (before any tokens arrive — this prevents the UI
//     from looking stuck during network latency).
//  2. Builds the tool definitions from the Tool Registry — these tell the LLM
//     what tools are available, their descriptions, and their parameter schemas.
//     The LLM uses this to decide whether and how to call tools.
//  3. Constructs a ChatRequest with the full conversation history, tool
//     definitions, model, temperature, and max tokens.
//  4. Calls llm.Provider.ChatStream with a streaming callback. Each text delta
//     from the LLM is forwarded to the frontend as an llm_delta event, creating
//     the typewriter effect in the UI.
//  5. After the stream completes, emits llm_message_complete and step_complete
//     events so the UI knows this think phase is done.
//
// # Why streaming?
//
// Streaming is essential for user experience — without it, the user would stare
// at a blank screen for seconds while the LLM generates the full response.
// With streaming, each token appears as it's generated, giving instant feedback
// and making the agent feel responsive. The streaming callback is also the
// mechanism that enables the "white-box" philosophy: every token the LLM
// generates is visible to the user in real time.
//
// # Tool call handling
//
// The LLM may return tool calls alongside or instead of text content. The
// ChatStream method accumulates tool call deltas across SSE chunks and returns
// the fully assembled tool calls. The engine then decides whether to execute
// tools (continue the loop) or return the text as the final answer.
func (e *Engine) think(ctx context.Context) (string, llm.Usage, []llm.ToolCall, error) {
	// Emit step_started: the UI transitions this step to "running" state.
	e.bus.SendEvent(event.NewEvent("step_started", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "think",
	}))

	// Emit llm_thinking: the UI shows a "Thinking..." indicator. This is sent
	// BEFORE the HTTP request so the user sees immediate feedback, even if the
	// LLM API takes several seconds to respond.
	e.bus.SendEvent(event.NewEvent("llm_thinking", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"content": "Thinking...",
	}))

	// Build the tool definitions from the registry. Each tool's name, description,
	// and JSON Schema parameters are sent to the LLM so it can decide which tools
	// to call and with what arguments. If the registry is empty, the LLM will
	// operate in pure text mode (no tool calls possible).
	toolDefs := make([]llm.ToolDef, 0)
	for _, t := range e.tools.List() {
		toolDefs = append(toolDefs, llm.ToolDef{
			Type: "function",
			Function: llm.FunctionDefinition{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}

	// Construct the chat request. The full conversation history is sent on every
	// call — this is the "stateless" design where the LLM has no memory between
	// calls. The conversation history serves as the agent's memory.
	req := llm.ChatRequest{
		Model:       e.cfg.Model,
		Messages:    e.messages,
		Tools:       toolDefs,
		Temperature: e.cfg.Temperature,
		MaxTokens:   e.cfg.MaxTokens,
	}

	// Call the LLM with streaming. The onChunk callback is invoked for each SSE
	// chunk. Each text delta is forwarded to the frontend as an llm_delta event
	// so the UI can render tokens in real time (typewriter effect).
	content, usage, toolCalls, err := e.llm.ChatStream(req, func(chunk llm.StreamChunk) error {
		// Stream each delta to the frontend
		if chunk.Delta.Content != "" {
			e.bus.SendEvent(event.NewEvent("llm_delta", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"content": chunk.Delta.Content,
			}))
		}
		return nil
	})
	if err != nil {
		return "", usage, nil, err
	}

	// Emit llm_message_complete: the LLM has finished generating. The UI can
	// stop the "thinking" animation and show the complete message.
	e.bus.SendEvent(event.NewEvent("llm_message_complete", e.taskID, e.cfg.AgentID, e.stepIdx, nil))

	// Emit step_complete: this think phase is done. The UI transitions this
	// step to "completed" state.
	e.bus.SendEvent(event.NewEvent("step_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "think",
	}))

	return content, usage, toolCalls, nil
}

// executeTool runs a single tool call requested by the LLM and returns the
// JSON-serialized result.
//
// # How it works
//
//  1. Increments stepIdx — each tool execution is a new step in the ReAct loop.
//  2. Parses the tool call arguments from JSON string to map[string]any.
//     If parsing fails, falls back to an empty map (the tool may still work
//     with default parameters).
//  3. Emits step_started and tool_call_started events so the UI can show the
//     tool name, arguments, and a loading indicator.
//  4. Dispatches the tool call to the Tool Registry, measuring execution time.
//  5. On success: emits tool_call_output, tool_call_complete, and observation
//     events. The observation event is particularly important — it tells the
//     UI what data is being fed back to the LLM.
//  6. On failure: emits tool_call_failed and step_complete events, then returns
//     the error.
//
// # Why measure duration?
//
// Tool execution time is critical for debugging and cost optimization. A tool
// that takes 30 seconds to run is a bottleneck — the duration_ms metric helps
// identify slow tools. The frontend can display execution time in the tool call
// card, giving users visibility into where time is spent.
//
// # Why increment stepIdx here?
//
// The stepIdx is incremented inside executeTool (not in the Run loop) because
// executeTool manages the full step lifecycle (started → executing → completed).
// Each tool execution is a distinct step with its own events, and the stepIdx
// must be correct when those events are emitted.
//
// # Phase 5: Approval Handling
//
// When the PolicyGate returns ErrApprovalRequired (from ApprovalRule), the engine
// catches this error and routes it to handleApprovalRequired. This method emits
// a system_info event to the frontend and waits for the user to approve or deny
// the high-risk operation. If approved, the tool is executed directly (bypassing
// the PolicyGate). If denied, the task fails with an approval_denied error.
func (e *Engine) executeTool(tc llm.ToolCall) (string, error) {
	// Increment stepIdx — each tool execution is a new step. This is done here
	// (not in the caller) so that the step_started and tool_call_started events
	// carry the correct step index.
	e.stepIdx++

	// Parse the tool call arguments from JSON. The LLM returns arguments as a
	// JSON string (not an object) because it streams them incrementally.
	// If parsing fails (e.g., the LLM produced malformed JSON), we fall back
	// to an empty map — the tool may still execute with default values.
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		args = make(map[string]any) // fallback to empty args
	}

	// Emit step and tool call lifecycle events. The UI uses these to show:
	//   - step_started: this step is now "running" in the step list
	//   - tool_call_started: the tool name and arguments in a card
	e.bus.SendEvent(event.NewEvent("step_started", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "tool_call",
	}))
	e.bus.SendEvent(event.NewEvent("tool_call_started", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool": tc.Function.Name,
		"args": args,
	}))

	// Execute the tool and measure its duration. The duration is tracked
	// for performance monitoring and debugging — slow tools are bottlenecks
	// that hurt user experience.
	start := time.Now()
	// Route through PolicyGate if configured; otherwise execute directly.
	// The PolicyGate checks the tool call against the policy chain (FileScopeRule,
	// PathTraversalRule, etc.) before allowing the tool to execute.
	var result any
	var execErr error
	if e.gate != nil {
		result, execErr = e.gate.Execute(tc.Function.Name, args, func(input map[string]any) (any, error) {
			return e.tools.Execute(tc.Function.Name, input)
		})
	} else {
		result, execErr = e.tools.Execute(tc.Function.Name, args)
	}
	duration := time.Since(start).Milliseconds()

	if execErr != nil {
		// === Phase 5: 审批请求处理 ===
		// 检查 PolicyGate 是否返回了 ErrApprovalRequired。
		// 如果 ApprovalRule 检测到高风险操作，会返回此错误。
		// Engine 需要发射 system_info 事件到前端，等待用户批准/拒绝。
		var approvalErr *harness.ErrApprovalRequired
		if errors.As(execErr, &approvalErr) {
			return e.handleApprovalRequired(tc, approvalErr, args, duration)
		}

		// Tool execution failed — emit failure events and return the error.
		// The UI will show the tool name, error message, and duration.
		// The step is still marked as "complete" (not "running") because the
		// tool call phase is over — it just ended in failure.
		e.bus.SendEvent(event.NewEvent("tool_call_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"tool":        tc.Function.Name,
			"error":       execErr.Error(),
			"duration_ms": duration,
		}))
		e.bus.SendEvent(event.NewEvent("step_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", execErr
	}

	// Serialize the tool result to JSON for the LLM conversation. The LLM
	// expects tool results as JSON strings so it can parse the structured data.
	// If serialization fails (unlikely for a well-behaved tool), we still have
	// the raw result object — but the LLM will receive an empty string.
	resultJSON, _ := json.Marshal(result)
	resultStr := string(resultJSON)

	// Emit tool execution events. The UI uses these to show:
	//   - tool_call_output: the raw tool result (collapsible JSON in the UI)
	//   - tool_call_complete: the tool finished successfully with duration
	//   - observation: the data being fed back to the LLM (the "observe" phase)
	//   - step_complete: this step is now "completed" in the step list
	e.bus.SendEvent(event.NewEvent("tool_call_output", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":   tc.Function.Name,
		"result": result,
	}))
	e.bus.SendEvent(event.NewEvent("tool_call_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":        tc.Function.Name,
		"duration_ms": duration,
	}))

	// Emit observation — this is the key event that connects the tool execution
	// back to the ReAct loop. The frontend shows this as the "observation" phase
	// in the Agent Tree visualization, making it clear what data the LLM will
	// see on the next think iteration.
	e.bus.SendEvent(event.NewEvent("observation", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"content": resultStr,
	}))

	e.bus.SendEvent(event.NewEvent("step_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "tool_call",
	}))

	return resultStr, nil
}

// handleApprovalRequired 处理 PolicyGate 返回的 ErrApprovalRequired 错误。
// 如果配置了 ApprovalHandler，发射 system_info 事件到前端，等待用户批准/拒绝决定。
// 如果用户批准，绕过 PolicyGate 直接执行工具调用。
// 如果用户拒绝或超时，返回错误导致任务失败。
// 如果没有配置 ApprovalHandler，直接返回错误。
//
// # 审批流程
//
//  1. 检查是否配置了 ApprovalHandler（未配置则直接拒绝）
//  2. 发射 system_info(type="approval_required") 事件到前端
//  3. 调用 ApprovalHandler.RequestApproval 发送审批请求
//  4. 调用 ApprovalHandler.WaitForDecision 等待用户决定（默认 30 秒超时）
//  5. 批准：绕过 PolicyGate 直接执行工具，发射正常事件流
//  6. 拒绝/超时：发射失败事件，返回错误
func (e *Engine) handleApprovalRequired(tc llm.ToolCall, approvalErr *harness.ErrApprovalRequired, args map[string]any, duration int64) (string, error) {
	// 如果未配置审批处理器，直接返回错误
	if e.approvalHandler == nil {
		errMsg := fmt.Sprintf("[APPROVAL REQUIRED] %s: %s (未配置审批处理器，操作被拒绝)",
			approvalErr.Tool, approvalErr.Reason)
		e.bus.SendEvent(event.NewEvent("system_info", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type":        "approval_blocked",
			"approval_id": approvalErr.ApprovalID,
			"tool":        approvalErr.Tool,
			"reason":      approvalErr.Reason,
			"message":     "审批处理器未配置，操作被自动拒绝",
		}))
		e.bus.SendEvent(event.NewEvent("step_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", fmt.Errorf("%s", errMsg)
	}

	// 发射 system_info 事件，通知前端显示审批对话框
	e.bus.SendEvent(event.NewEvent("system_info", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type":        "approval_required",
		"approval_id": approvalErr.ApprovalID,
		"tool":        approvalErr.Tool,
		"reason":      approvalErr.Reason,
		"input":       approvalErr.Input,
		"duration_ms": duration,
	}))

	// 发射 tool_call_output 事件，让前端显示工具调用信息
	e.bus.SendEvent(event.NewEvent("tool_call_output", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":        tc.Function.Name,
		"result":      map[string]any{"status": "pending_approval", "approval_id": approvalErr.ApprovalID},
		"duration_ms": duration,
	}))

	// 向前端发起审批请求
	if err := e.approvalHandler.RequestApproval(approvalErr.ApprovalID, approvalErr.Tool, approvalErr.Reason, approvalErr.Input); err != nil {
		log.Printf("[Engine] 审批请求发送失败: %v", err)
		e.bus.SendEvent(event.NewEvent("tool_call_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"tool":        tc.Function.Name,
			"error":       fmt.Sprintf("审批请求发送失败: %v", err),
			"duration_ms": duration,
		}))
		e.bus.SendEvent(event.NewEvent("step_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", fmt.Errorf("approval request failed: %w", err)
	}

	// 等待前端审批决定（默认超时 30 秒）
	approved, waitErr := e.approvalHandler.WaitForDecision(approvalErr.ApprovalID, 30*time.Second)
	if waitErr != nil {
		// 超时或等待错误 — 视为拒绝
		log.Printf("[Engine] 审批等待失败: %v", waitErr)
		e.bus.SendEvent(event.NewEvent("system_info", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type":        "approval_timeout",
			"approval_id": approvalErr.ApprovalID,
			"tool":        approvalErr.Tool,
			"reason":      "审批超时，操作被自动拒绝",
		}))
		e.bus.SendEvent(event.NewEvent("tool_call_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"tool":        tc.Function.Name,
			"error":       fmt.Sprintf("审批超时: %v", waitErr),
			"duration_ms": duration,
		}))
		e.bus.SendEvent(event.NewEvent("step_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", fmt.Errorf("approval timeout: %w", waitErr)
	}

	if !approved {
		// 用户拒绝了审批请求
		log.Printf("[Engine] 审批被拒绝: %s (%s)", approvalErr.Tool, approvalErr.ApprovalID)
		e.bus.SendEvent(event.NewEvent("system_info", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type":        "approval_denied",
			"approval_id": approvalErr.ApprovalID,
			"tool":        approvalErr.Tool,
			"reason":      "用户拒绝了此操作",
		}))
		e.bus.SendEvent(event.NewEvent("tool_call_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"tool":        tc.Function.Name,
			"error":       "用户拒绝了高风险操作",
			"duration_ms": duration,
		}))
		e.bus.SendEvent(event.NewEvent("step_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", fmt.Errorf("user denied approval for %s: %s", approvalErr.Tool, approvalErr.Reason)
	}

	// 用户批准 — 绕过 PolicyGate 直接执行工具调用
	log.Printf("[Engine] 审批通过: %s (%s), 执行工具调用", approvalErr.Tool, approvalErr.ApprovalID)
	e.bus.SendEvent(event.NewEvent("system_info", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type":        "approval_granted",
		"approval_id": approvalErr.ApprovalID,
		"tool":        approvalErr.Tool,
		"message":     "审批通过，正在执行工具调用",
	}))

	// 直接执行工具（不经过 PolicyGate，因为用户已批准）
	execStart := time.Now()
	result, execErr := e.tools.Execute(tc.Function.Name, args)
	execDuration := time.Since(execStart).Milliseconds()

	if execErr != nil {
		e.bus.SendEvent(event.NewEvent("tool_call_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"tool":        tc.Function.Name,
			"error":       execErr.Error(),
			"duration_ms": execDuration,
		}))
		e.bus.SendEvent(event.NewEvent("step_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", execErr
	}

	// 工具执行成功 — 发射正常的事件流
	resultJSON, _ := json.Marshal(result)
	resultStr := string(resultJSON)

	e.bus.SendEvent(event.NewEvent("tool_call_output", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":   tc.Function.Name,
		"result": result,
	}))
	e.bus.SendEvent(event.NewEvent("tool_call_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":        tc.Function.Name,
		"duration_ms": execDuration,
	}))
	e.bus.SendEvent(event.NewEvent("observation", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"content": resultStr,
	}))
	e.bus.SendEvent(event.NewEvent("step_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "tool_call",
	}))

	return resultStr, nil
}