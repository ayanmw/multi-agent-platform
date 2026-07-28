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

	// 验证 injectedSkillBlocks 记录了本次注入的 skill 明细。
	if len(e.injectedSkillBlocks) != 1 {
		t.Fatalf("len(injectedSkillBlocks)=%d, want 1", len(e.injectedSkillBlocks))
	}
	block := e.injectedSkillBlocks[0]
	if block.SkillID != "test-skill" {
		t.Fatalf("block.SkillID=%s, want test-skill", block.SkillID)
	}
	if block.TemplateName != "system_prompt" {
		t.Fatalf("block.TemplateName=%s, want system_prompt", block.TemplateName)
	}
	if block.Content == "" {
		t.Fatalf("block.Content should not be empty")
	}
	if block.CharCount <= 0 {
		t.Fatalf("block.CharCount should be positive, got %d", block.CharCount)
	}
	if block.EstimatedTokens <= 0 {
		t.Fatalf("block.EstimatedTokens should be positive, got %d", block.EstimatedTokens)
	}
}

func TestEngineSkillRenderedEvent(t *testing.T) {
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
	_ = NewEngine(cfg, tools, bus, "task-skill-rendered")

	var found bool
	for _, ev := range bus.events {
		if ev.Type == skill.EventSkillRendered {
			found = true
			if ev.TaskID != "task-skill-rendered" {
				t.Fatalf("task_id=%s, want task-skill-rendered", ev.TaskID)
			}
			if ev.AgentID != "test-agent" {
				t.Fatalf("agent_id=%s, want test-agent", ev.AgentID)
			}
			blocks, ok := ev.Data["skill_blocks"].([]map[string]any)
			if !ok || len(blocks) != 1 {
				t.Fatalf("skill_blocks missing or wrong length: %v", ev.Data["skill_blocks"])
			}
			if blocks[0]["skill_id"] != "test-skill" {
				t.Fatalf("skill_id=%v, want test-skill", blocks[0]["skill_id"])
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected skill_rendered event, got events: %v", bus.events)
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
