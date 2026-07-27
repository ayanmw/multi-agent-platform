// skill_test.go —— skills 表 CRUD 与 settings 的测试。
package db

import (
	"errors"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/skill"
)

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

	if err := SaveSkill(s); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}

	got, err := GetSkill(s.ID)
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if got.ID != s.ID {
		t.Errorf("GetSkill ID = %q, want %q", got.ID, s.ID)
	}
	if got.Templates[0].Name != "system" {
		t.Errorf("GetSkill Templates = %+v", got.Templates)
	}

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

	bySource, err := ListSkills(string(skill.SkillSourceLocalDB), "")
	if err != nil || len(bySource) == 0 {
		t.Fatalf("ListSkills by source: %v / %d", err, len(bySource))
	}

	if err := DeleteSkill(s.ID); err != nil {
		t.Fatalf("DeleteSkill: %v", err)
	}
	_, err = GetSkill(s.ID)
	if !errors.Is(err, ErrSkillNotFound) {
		t.Fatalf("after DeleteSkill, GetSkill error = %v, want ErrSkillNotFound", err)
	}
}

func TestSettingStore(t *testing.T) {
	freshDB(t)

	val, err := GetSetting("skill_scan_dirs")
	if err != nil {
		t.Fatalf("GetSetting initial: %v", err)
	}
	if val != "" {
		t.Errorf("expected empty initial value, got %q", val)
	}

	if err := SetSetting("skill_scan_dirs", `[".claude/skills"]`) ; err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	val, err = GetSetting("skill_scan_dirs")
	if err != nil {
		t.Fatalf("GetSetting after set: %v", err)
	}
	if val != `[".claude/skills"]` {
		t.Errorf("expected stored JSON, got %q", val)
	}
}
