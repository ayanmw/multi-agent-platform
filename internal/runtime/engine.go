package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// EventBus is the interface for sending events to WebSocket clients
type EventBus interface {
	SendEvent(event.Event)
}

// EngineConfig configures the ReAct loop engine
type EngineConfig struct {
	AgentID      string
	SystemPrompt string
	Model        string
	Endpoint     string
	APIKey       string
	Temperature  float32
	MaxTokens    int
	MaxSteps     int
	Persistence  Persistence // optional: set to nil for no persistence
}

// Engine executes the ReAct loop for a single agent
type Engine struct {
	cfg      EngineConfig
	llm      *llm.Client
	tools    *tool.Registry
	bus      EventBus
	persist  Persistence
	taskID   string
	messages []llm.Message
	stepIdx  int
}

// NewEngine creates a new ReAct loop engine
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

	return &Engine{
		cfg:     cfg,
		llm:     llm.NewClient(cfg.Endpoint, cfg.APIKey, cfg.Model),
		tools:   tools,
		bus:     bus,
		persist: cfg.Persistence,
		taskID:  taskID,
		messages: []llm.Message{
			{Role: "system", Content: cfg.SystemPrompt},
		},
		stepIdx: 0,
	}
}

// Run executes the ReAct loop until completion or max steps
func (e *Engine) Run(ctx context.Context, userInput string) (string, int, error) {
	// Add user message to conversation
	e.messages = append(e.messages, llm.Message{Role: "user", Content: userInput})

	// Persist user message
	e.saveConversation("user", userInput)

	// Notify agent is ready
	e.bus.SendEvent(event.NewEvent("agent_ready", e.taskID, e.cfg.AgentID, 0, map[string]any{
		"agent_name": e.cfg.AgentID,
		"model":      e.cfg.Model,
	}))

	// ReAct loop
	for e.stepIdx < e.cfg.MaxSteps {
		select {
		case <-ctx.Done():
			e.bus.SendEvent(event.NewEvent("task_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"reason": "cancelled",
			}))
			e.updateTask("failed", "", 0)
			return "", e.stepIdx, ctx.Err()
		default:
		}

		// Step: Think
		content, usage, toolCalls, err := e.think(ctx)
		if err != nil {
			e.bus.SendEvent(event.NewEvent("task_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"reason": "llm_error",
				"error":  err.Error(),
			}))
			e.updateTask("failed", "", 0)
			return "", e.stepIdx, fmt.Errorf("think step %d: %w", e.stepIdx, err)
		}

		log.Printf("[Engine] Step %d: content=%d chars, toolCalls=%d, usage=%+v",
			e.stepIdx, len(content), len(toolCalls), usage)

		// If no tool calls, this is the final answer
		if len(toolCalls) == 0 {
			// Persist final step
			e.saveStep(StepRecord{
				TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
				Type: "think", Status: "completed", Content: content, TokenUsed: usage.TotalTokens,
			})
			e.saveConversation("assistant", content)

			// Emit final observation
			e.bus.SendEvent(event.NewEvent("observation", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"content":           content,
				"total_tokens":      usage.TotalTokens,
				"prompt_tokens":     usage.PromptTokens,
				"completion_tokens": usage.CompletionTokens,
			}))
			e.bus.SendEvent(event.NewEvent("task_completed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"result":       content,
				"total_tokens": usage.TotalTokens,
				"total_steps":  e.stepIdx,
			}))
			e.updateTask("completed", content, usage.TotalTokens)
			return content, usage.TotalTokens, nil
		}

		// Step: Execute tool calls
		for _, tc := range toolCalls {
			// Persist think step before tool execution
			e.saveStep(StepRecord{
				TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
				Type: "think", Status: "completed", Content: content, TokenUsed: usage.TotalTokens,
			})
			e.saveConversation("assistant", content)

			result, err := e.executeTool(tc)
			if err != nil {
				e.bus.SendEvent(event.NewEvent("task_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
					"reason":    "tool_error",
					"tool_name": tc.Function.Name,
					"error":     err.Error(),
				}))
				e.updateTask("failed", "", 0)
				return "", e.stepIdx, fmt.Errorf("tool %s: %w", tc.Function.Name, err)
			}

			// Persist tool result
			e.saveConversation("tool", result)

			// Add assistant + tool result to conversation
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
	}

	// Max steps exceeded
	e.bus.SendEvent(event.NewEvent("task_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"reason":    "max_steps_exceeded",
		"max_steps": e.cfg.MaxSteps,
	}))
	e.updateTask("failed", "", 0)
	return "", e.stepIdx, fmt.Errorf("max steps (%d) exceeded", e.cfg.MaxSteps)
}

// saveConversation persists a conversation message (no-op if persistence is nil)
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

// saveStep persists a step record (no-op if persistence is nil)
func (e *Engine) saveStep(s StepRecord) {
	if e.persist == nil {
		return
	}
	if err := e.persist.SaveStep(s); err != nil {
		log.Printf("[Engine] Failed to save step: %v", err)
	}
}

// updateTask persists task status update (no-op if persistence is nil)
func (e *Engine) updateTask(status, finalResult string, totalTokens int) {
	if e.persist == nil {
		return
	}
	if err := e.persist.UpdateTask(e.taskID, status, finalResult, totalTokens); err != nil {
		log.Printf("[Engine] Failed to update task: %v", err)
	}
}

// think sends the current conversation to the LLM and streams the response
func (e *Engine) think(ctx context.Context) (string, llm.Usage, []llm.ToolCall, error) {
	// Emit step started
	e.bus.SendEvent(event.NewEvent("step_started", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "think",
	}))

	// Emit thinking event
	e.bus.SendEvent(event.NewEvent("llm_thinking", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"content": "Thinking...",
	}))

	// Build tool definitions for LLM
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

	req := llm.ChatRequest{
		Model:       e.cfg.Model,
		Messages:    e.messages,
		Tools:       toolDefs,
		Temperature: e.cfg.Temperature,
		MaxTokens:   e.cfg.MaxTokens,
	}

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

	// Emit message complete
	e.bus.SendEvent(event.NewEvent("llm_message_complete", e.taskID, e.cfg.AgentID, e.stepIdx, nil))

	// Emit step complete
	e.bus.SendEvent(event.NewEvent("step_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "think",
	}))

	return content, usage, toolCalls, nil
}

// executeTool runs a tool call and emits events
func (e *Engine) executeTool(tc llm.ToolCall) (string, error) {
	e.stepIdx++

	// Parse arguments
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		args = make(map[string]any) // fallback to empty args
	}

	// Emit tool call started
	e.bus.SendEvent(event.NewEvent("step_started", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "tool_call",
	}))
	e.bus.SendEvent(event.NewEvent("tool_call_started", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool": tc.Function.Name,
		"args": args,
	}))

	start := time.Now()
	result, err := e.tools.Execute(tc.Function.Name, args)
	duration := time.Since(start).Milliseconds()

	if err != nil {
		e.bus.SendEvent(event.NewEvent("tool_call_failed", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"tool":        tc.Function.Name,
			"error":       err.Error(),
			"duration_ms": duration,
		}))
		e.bus.SendEvent(event.NewEvent("step_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", err
	}

	// Format result as string for the LLM conversation
	resultJSON, _ := json.Marshal(result)
	resultStr := string(resultJSON)

	// Emit tool call result
	e.bus.SendEvent(event.NewEvent("tool_call_output", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":   tc.Function.Name,
		"result": result,
	}))
	e.bus.SendEvent(event.NewEvent("tool_call_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":        tc.Function.Name,
		"duration_ms": duration,
	}))

	// Emit observation
	e.bus.SendEvent(event.NewEvent("observation", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"content": resultStr,
	}))

	e.bus.SendEvent(event.NewEvent("step_complete", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "tool_call",
	}))

	return resultStr, nil
}