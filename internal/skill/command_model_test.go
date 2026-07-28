package skill

import "testing"

func TestSkillCommandEventConstants(t *testing.T) {
	want := []string{
		EventSkillCommandLoaded,
		EventSkillCommandUnloaded,
		EventSkillCommandChanged,
	}
	for _, s := range want {
		if s == "" {
			t.Fatal("command event constant must not be empty")
		}
	}
}

func TestSkillCommandModel(t *testing.T) {
	cmd := SkillCommand{
		ID:           "ops:new",
		Name:         "新增 OpenSpec 变更",
		Description:  "通过命令快速创建 OpenSpec 变更",
		SourcePath:   ".claude/commands/ops/new.md",
		Scope:        SkillCommandScopeProject,
		WorkspaceDir: "/tmp/proj",
		ProjectID:    "proj-1",
		SkillID:      "openspec-new-change",
		Prompt:       "你是一个专业的产品经理...",
		Tags:         []string{"ops", "openspec"},
		Icon:         "plus",
		CommandKey:   "new",
	}
	if cmd.ID != "ops:new" {
		t.Fatalf("unexpected id: %s", cmd.ID)
	}
}
