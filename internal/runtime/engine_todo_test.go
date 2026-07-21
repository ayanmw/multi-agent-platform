package runtime

import (
	"testing"

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
