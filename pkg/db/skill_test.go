// skill_test.go —— skills 表 CRUD 的测试。
package db

import (
	"errors"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/skill"
)

// TestSkillCRUD 验证 Skill 记录的完整生命周期：
// 创建、保存、读取、列表、过滤、删除。
func TestSkillCRUD(t *testing.T) {
	freshDB(t)

	s := skill.Skill{
		ID:              "test/skill-crud",
		Version:         "0.1.0",
		DisplayName:     "Test Skill",
		Description:     "A skill used by the CRUD test.",
		Authors:         []string{"tester"},
		Tags:            []string{"test", "crud"},
		Source:          skill.SkillSourceLocalDB,
		SourceURL:       "db://test",
		IsLocalEditable: true,
		Templates: []skill.SkillTemplate{
			{
				Name:       "system",
				Content:    "You are a helpful assistant for {{topic}}.",
				Variables:  []string{"topic"},
				IsRequired: true,
			},
		},
		Parameters: []skill.SkillParameter{
			{
				Name:        "topic",
				Type:        "string",
				Required:    true,
				Default:     "general",
				Description: "The topic to focus on.",
			},
		},
		RequiredTools:  []string{"read_file"},
		SuggestedTools: []string{"write_file"},
		Permissions:    []string{"file:read"},
		Triggers: skill.SkillTriggers{
			Keywords:     []string{"test"},
			Intents:      []string{"demo"},
			FilePatterns: []string{"*.test"},
		},
		State:         skill.SkillStateEnabled,
		InvalidReason: "",
		CreatedAt:     1700000000,
		UpdatedAt:     1700000000,
	}

	// 保存
	if err := SaveSkill(s); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}

	// 读取
	got, err := GetSkill(s.ID)
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if got.ID != s.ID {
		t.Errorf("GetSkill ID = %q, want %q", got.ID, s.ID)
	}
	if got.Version != s.Version {
		t.Errorf("GetSkill Version = %q, want %q", got.Version, s.Version)
	}
	if got.DisplayName != s.DisplayName {
		t.Errorf("GetSkill DisplayName = %q, want %q", got.DisplayName, s.DisplayName)
	}
	if got.Source != s.Source {
		t.Errorf("GetSkill Source = %q, want %q", got.Source, s.Source)
	}
	if got.State != s.State {
		t.Errorf("GetSkill State = %q, want %q", got.State, s.State)
	}
	if len(got.Templates) != 1 || got.Templates[0].Name != "system" {
		t.Errorf("GetSkill Templates = %+v, want 1 template named system", got.Templates)
	}
	if len(got.Parameters) != 1 || got.Parameters[0].Name != "topic" {
		t.Errorf("GetSkill Parameters = %+v, want 1 parameter named topic", got.Parameters)
	}
	if len(got.Triggers.Keywords) != 1 || got.Triggers.Keywords[0] != "test" {
		t.Errorf("GetSkill Triggers.Keywords = %v, want [test]", got.Triggers.Keywords)
	}
	if got.UpdatedAt < s.UpdatedAt {
		t.Errorf("GetSkill UpdatedAt should be refreshed on save, got %d", got.UpdatedAt)
	}

	// 列出全部
	all, err := ListSkills("", "")
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	found := false
	for _, item := range all {
		if item.ID == s.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListSkills did not contain %q", s.ID)
	}

	// 按 source 过滤
	bySource, err := ListSkills(string(skill.SkillSourceLocalDB), "")
	if err != nil {
		t.Fatalf("ListSkills by source: %v", err)
	}
	if len(bySource) == 0 {
		t.Error("ListSkills by source returned no results")
	}

	// 按 state 过滤
	byState, err := ListSkills("", string(skill.SkillStateEnabled))
	if err != nil {
		t.Fatalf("ListSkills by state: %v", err)
	}
	if len(byState) == 0 {
		t.Error("ListSkills by state returned no results")
	}

	// 删除
	if err := DeleteSkill(s.ID); err != nil {
		t.Fatalf("DeleteSkill: %v", err)
	}
	_, err = GetSkill(s.ID)
	if !errors.Is(err, ErrSkillNotFound) {
		t.Fatalf("after DeleteSkill, GetSkill error = %v, want ErrSkillNotFound", err)
	}
}
