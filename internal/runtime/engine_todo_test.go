package runtime

import (
	"encoding/json"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
)

func TestEngineActiveTodosInjection(t *testing.T) {
	bus := &recordingBus{}

	cfg := EngineConfig{
		AgentID:      "test-agent",
		SystemPrompt: "You are a helpful assistant.",
		Model:        "fake-model",
		ActiveTodos:  "## Active TODO List for This Session\n1. [ ] 编写测试 (priority: 1)\n",
	}

	tools := tool.NewRegistry()
	e := NewEngine(cfg, tools, bus, "task-todo-inject")

	if len(e.messages) == 0 {
		t.Fatalf("engine should have at least one message")
	}
	first := e.messages[0]
	if first.Role != "system" {
		t.Fatalf("first message should be system, got %s", first.Role)
	}
	want := "## Active TODO List for This Session"
	if !contains(first.Content, want) {
		t.Fatalf("system prompt should contain %q, got:\n%s", want, first.Content)
	}
	want2 := "todo/* tools"
	if !contains(first.Content, want2) {
		t.Fatalf("system prompt should contain todo tool hint %q, got:\n%s", want2, first.Content)
	}
}

func TestEngineNoActiveTodosWhenEmpty(t *testing.T) {
	bus := &recordingBus{}

	cfg := EngineConfig{
		AgentID:      "test-agent",
		SystemPrompt: "You are a helpful assistant.",
		Model:        "fake-model",
		ActiveTodos:  "",
	}

	tools := tool.NewRegistry()
	e := NewEngine(cfg, tools, bus, "task-no-todo")

	first := e.messages[0]
	if contains(first.Content, "Active TODO List") {
		t.Fatalf("system prompt should not contain TODO list when ActiveTodos is empty, got:\n%s", first.Content)
	}
}

// TestEngineInjectsSessionAndTaskIDIntoToolArgs 验证 Engine 在执行 tool 时，
// 当 LLM 未显式提供 session_id / task_id 时，自动注入 Engine 持有的真实值。
// session_id / task_id 是平台内部路由标识，从不出现在 system prompt 中，
// 因此 LLM 无法（也不应）自行填对——若不自动注入，todo/cron 等工具会因
// 占位符 session（如 "test-session"）而把数据写到错误归属。
func TestEngineInjectsSessionAndTaskIDIntoToolArgs(t *testing.T) {
	// 注册一个 echo tool，把收到的入参原样回传，用于断言注入结果。
	var captured map[string]any
	tools := tool.NewRegistry()
	tools.Register(echoCaptureTool{captured: &captured})

	bus := &recordingBus{}
	cfg := EngineConfig{
		AgentID:   "test-agent",
		Model:     "fake-model",
		SessionID: "real-session-xyz",
	}
	e := NewEngine(cfg, tools, bus, "real-task-123")

	// 模拟 LLM 传入了占位 session_id（如 "test-session"）但没传 task_id。
	tc := newToolCall("echo_capture", map[string]any{"session_id": "test-session"})
	if _, err := e.executeTool(tc); err != nil {
		t.Fatalf("executeTool failed: %v", err)
	}

	if captured["session_id"] != "real-session-xyz" {
		t.Fatalf("占位 session_id 应被覆盖为真实 session_id，got %v", captured["session_id"])
	}
	if captured["task_id"] != "real-task-123" {
		t.Fatalf("缺失的 task_id 应被注入为真实 task_id，got %v", captured["task_id"])
	}

	// 第二次：即便 LLM 显式提供 session_id，也应被 Engine 真实值覆盖
	// （session_id 是路由标识，LLM 无权威性）。
	captured = nil
	tc2 := newToolCall("echo_capture", map[string]any{"session_id": "explicit-session"})
	if _, err := e.executeTool(tc2); err != nil {
		t.Fatalf("executeTool failed: %v", err)
	}
	if captured["session_id"] != "real-session-xyz" {
		t.Fatalf("LLM 提供的 session_id 仍应被真实值覆盖，got %v", captured["session_id"])
	}
}

// echoCaptureTool 是一个测试用 Tool：把执行时收到的入参存到 captured，
// 供测试断言 Engine 的 session_id / task_id 注入行为。
type echoCaptureTool struct {
	captured *map[string]any
}

func (t echoCaptureTool) Namespace() string                    { return "" }
func (t echoCaptureTool) Name() string                         { return "echo_capture" }
func (t echoCaptureTool) FullName() string                     { return "echo_capture" }
func (t echoCaptureTool) Version() string                      { return "" }
func (t echoCaptureTool) Source() string                       { return "builtin" }
func (t echoCaptureTool) CanonicalName() string                { return "echo_capture" }
func (t echoCaptureTool) Aliases() []string                    { return nil }
func (t echoCaptureTool) Description() string                  { return "test echo tool" }
func (t echoCaptureTool) Parameters() map[string]any           { return map[string]any{"type": "object"} }
func (t echoCaptureTool) Tags() []string                       { return nil }
func (t echoCaptureTool) Execute(input map[string]any) (any, error) {
	*t.captured = input
	return input, nil
}

// newToolCall 构造一个带 JSON arguments 的 llm.ToolCall，方便测试。
func newToolCall(name string, args map[string]any) llm.ToolCall {
	b, _ := json.Marshal(args)
	return llm.ToolCall{
		ID:   "call-test",
		Type: "function",
		Function: llm.FunctionCall{
			Name:      name,
			Arguments: string(b),
		},
	}
}
