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

// TestEngineSkillRenderedNotBroadcastWhenNoBlocks 验证 NewEngine 在 skill 子系统
// 处于多种负向配置时不广播 skill_rendered 事件：
//  (1) SkillRegistry=nil
//  (2) ActiveSkills 为空切片
//  (3) ActiveSkills 指向 registry 中不存在的 ID
//  (4) Skill 存在但无 system_prompt/task_prompt 模板
// 这些边界条件保证 "registry 非空但无有效 skill 可注入" 时不会发送含空
// skill_blocks 的事件污染前端事件流。
func TestEngineSkillRenderedNotBroadcastWhenNoBlocks(t *testing.T) {
	// 场景1：nil registry + nil ActiveSkills——不应广播。
	bus1 := &recordingBus{}
	cfg1 := EngineConfig{
		AgentID:      "test-agent",
		SystemPrompt: "You are a helpful assistant.",
		Model:        "fake-model",
	}
	_ = NewEngine(cfg1, tool.NewRegistry(), bus1, "task-skill-nil")
	for _, ev := range bus1.events {
		if ev.Type == skill.EventSkillRendered {
			t.Fatalf("nil registry 不应广播 skill_rendered, got: %v", ev)
		}
	}

	// 场景2：非空 registry 但 ActiveSkills 为空——不应广播。
	bus2 := &recordingBus{}
	reg := skill.NewRegistry()
	cfg2 := EngineConfig{
		AgentID:        "test-agent",
		SystemPrompt:   "You are a helpful assistant.",
		Model:          "fake-model",
		SkillRegistry:  reg,
		ActiveSkills:   []string{},
		SkillVariables: map[string]any{},
	}
	_ = NewEngine(cfg2, tool.NewRegistry(), bus2, "task-skill-empty")
	for _, ev := range bus2.events {
		if ev.Type == skill.EventSkillRendered {
			t.Fatalf("空 ActiveSkills 不应广播 skill_rendered, got: %v", ev)
		}
	}

	// 场景3：ActiveSkills 指向不存在 skill id——不应广播空 skill_blocks。
	bus3 := &recordingBus{}
	cfg3 := EngineConfig{
		AgentID:        "test-agent",
		SystemPrompt:   "You are a helpful assistant.",
		Model:          "fake-model",
		SkillRegistry:  reg,
		ActiveSkills:   []string{"non-existent"},
		SkillVariables: map[string]any{},
	}
	_ = NewEngine(cfg3, tool.NewRegistry(), bus3, "task-skill-missing")
	for _, ev := range bus3.events {
		if ev.Type == skill.EventSkillRendered {
			t.Fatalf("不存在的 skill 不应广播 skill_rendered, got: %v", ev)
		}
	}

	// 场景4：skill 存在但无 system_prompt/task_prompt 模板——不应广播空 skill_blocks。
	reg.Register(skill.Skill{
		ID:          "no-template",
		DisplayName: "No Template",
		Source:      skill.SkillSourceBuiltIn,
		State:       skill.SkillStateEnabled,
		Templates:   []skill.SkillTemplate{}, // 空 templates
	})
	bus4 := &recordingBus{}
	cfg4 := EngineConfig{
		AgentID:        "test-agent",
		SystemPrompt:   "You are a helpful assistant.",
		Model:          "fake-model",
		SkillRegistry:  reg,
		ActiveSkills:   []string{"no-template"},
		SkillVariables: map[string]any{},
	}
	_ = NewEngine(cfg4, tool.NewRegistry(), bus4, "task-skill-no-tmpl")
	for _, ev := range bus4.events {
		if ev.Type == skill.EventSkillRendered {
			t.Fatalf("无匹配模板的 skill 不应广播 skill_rendered, got: %v", ev)
		}
	}
}

// TestEngineSkillRenderedNotBroadcastWhenInactive 覆盖 NewEngine 在 skill
// 子系统处于三种负向配置时不广播 skill_rendered 事件且 injectedSkillBlocks 为空：
//  (1) SkillRegistry=nil；(2) ActiveSkills 为空切片；(3) ActiveSkills 指向不存在 ID。
// 相比 TestEngineSkillRenderedNotBroadcastWhenNoBlocks，本测试额外断言
// Engine.injectedSkillBlocks 字段在这些负向场景下保持空切片，防止 future change
// 静默写入空 block 但事件被条件跳过时遗漏检测。
func TestEngineSkillRenderedNotBroadcastWhenInactive(t *testing.T) {
	// 场景1：nil registry + nil ActiveSkills——不应广播，injectedSkillBlocks 为空。
	bus1 := &recordingBus{}
	cfg1 := EngineConfig{
		AgentID:      "test-agent",
		SystemPrompt: "You are a helpful assistant.",
		Model:        "fake-model",
	}
	e1 := NewEngine(cfg1, tool.NewRegistry(), bus1, "task-skill-nil")
	for _, ev := range bus1.events {
		if ev.Type == skill.EventSkillRendered {
			t.Fatalf("nil registry 不应广播 skill_rendered, got: %v", ev)
		}
	}
	if len(e1.injectedSkillBlocks) != 0 {
		t.Fatalf("nil registry 时 injectedSkillBlocks 应为空, got %d blocks", len(e1.injectedSkillBlocks))
	}

	// 场景2：非空 registry 但 ActiveSkills 为空——不应广播，injectedSkillBlocks 为空。
	bus2 := &recordingBus{}
	reg := skill.NewRegistry()
	cfg2 := EngineConfig{
		AgentID:        "test-agent",
		SystemPrompt:   "You are a helpful assistant.",
		Model:          "fake-model",
		SkillRegistry:  reg,
		ActiveSkills:   []string{},
		SkillVariables: map[string]any{},
	}
	e2 := NewEngine(cfg2, tool.NewRegistry(), bus2, "task-skill-empty")
	for _, ev := range bus2.events {
		if ev.Type == skill.EventSkillRendered {
			t.Fatalf("空 ActiveSkills 不应广播 skill_rendered, got: %v", ev)
		}
	}
	if len(e2.injectedSkillBlocks) != 0 {
		t.Fatalf("空 ActiveSkills 时 injectedSkillBlocks 应为空, got %d blocks", len(e2.injectedSkillBlocks))
	}

	// 场景3：ActiveSkills 指向不存在 skill id——不应广播，injectedSkillBlocks 为空。
	bus3 := &recordingBus{}
	cfg3 := EngineConfig{
		AgentID:        "test-agent",
		SystemPrompt:   "You are a helpful assistant.",
		Model:          "fake-model",
		SkillRegistry:  reg,
		ActiveSkills:   []string{"non-existent"},
		SkillVariables: map[string]any{},
	}
	e3 := NewEngine(cfg3, tool.NewRegistry(), bus3, "task-skill-missing")
	for _, ev := range bus3.events {
		if ev.Type == skill.EventSkillRendered {
			t.Fatalf("不存在的 skill 不应广播 skill_rendered, got: %v", ev)
		}
	}
	if len(e3.injectedSkillBlocks) != 0 {
		t.Fatalf("不存在的 skill id 时 injectedSkillBlocks 应为空, got %d blocks", len(e3.injectedSkillBlocks))
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
