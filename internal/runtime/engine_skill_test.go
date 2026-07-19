package runtime

import (
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/skill"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
)

func TestEngineSkillPromptInjection(t *testing.T) {
	bus := &recordingBus{}

	reg := skill.NewRegistry()
	reg.Register(skill.Skill{
		ID:          "test-skill",
		DisplayName: "Test Skill",
		Source:      skill.SkillSourceBuiltIn,
		State:       skill.SkillStateEnabled,
		Templates: []skill.SkillTemplate{
			{
				Name:    "system_prompt",
				Content: "Focus on {{ topic }}.",
			},
		},
	})

	cfg := EngineConfig{
		AgentID:        "test-agent",
		SystemPrompt:   "You are a helpful assistant.",
		Model:          "fake-model",
		SkillRegistry:  reg,
		ActiveSkills:   []string{"test-skill"},
		SkillVariables: map[string]any{"topic": "performance"},
	}

	tools := tool.NewRegistry()
	e := NewEngine(cfg, tools, bus, "task-skill-inject")

	if len(e.messages) == 0 {
		t.Fatalf("engine should have at least one message")
	}
	first := e.messages[0]
	if first.Role != "system" {
		t.Fatalf("first message should be system, got %s", first.Role)
	}
	if first.Content == "" {
		t.Fatalf("first system message content should not be empty")
	}
	want := "Focus on performance."
	if !contains(first.Content, want) {
		t.Fatalf("system prompt should contain %q, got:\n%s", want, first.Content)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
