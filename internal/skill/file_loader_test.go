package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileLoaderLoadGlobalDiscoversSkill(t *testing.T) {
	base := t.TempDir()
	skillDir := filepath.Join(base, ".claude", "skills", "hello")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: Hello Skill
description: A test filesystem skill
tags: [test]
---
You are a helpful assistant, and you always say "loaded from filesystem".
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	fl := NewFileLoader(reg, nil, nil, nil)
	if err := fl.LoadGlobal(base); err != nil {
		t.Fatalf("LoadGlobal failed: %v", err)
	}

	s, ok := reg.Get("hello")
	if !ok {
		t.Fatalf("expected skill 'hello' to be registered")
	}
	if s.Source != SkillSourceLocalFile {
		t.Errorf("source = %q, want local_file", s.Source)
	}
	if s.DisplayName != "Hello Skill" {
		t.Errorf("display_name = %q, want Hello Skill", s.DisplayName)
	}
	if s.Scope != SkillScopeGlobal {
		t.Errorf("scope = %q, want global", s.Scope)
	}
	if len(s.Templates) != 1 || s.Templates[0].Name != "system_prompt" {
		t.Errorf("unexpected templates: %+v", s.Templates)
	}
}

func TestFileLoaderLoadForWorkdir(t *testing.T) {
	workdir := t.TempDir()
	skillDir := filepath.Join(workdir, ".claude", "skills", "project-helper")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := `---
display_name: Project Helper
---
Help with this project.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	fl := NewFileLoader(reg, nil, nil, nil)
	if err := fl.LoadForWorkdir(workdir, "proj-1"); err != nil {
		t.Fatalf("LoadForWorkdir failed: %v", err)
	}

	s, ok := reg.Get("project-helper")
	if !ok {
		t.Fatalf("expected skill 'project-helper'")
	}
	if s.Scope != SkillScopeProject {
		t.Errorf("scope = %q, want project", s.Scope)
	}
	if s.WorkspaceDir != workdir {
		t.Errorf("workspace_dir = %q, want %q", s.WorkspaceDir, workdir)
	}
	if s.ProjectID != "proj-1" {
		t.Errorf("project_id = %q, want proj-1", s.ProjectID)
	}
}

func TestFileLoaderIgnoresDisabledScanDir(t *testing.T) {
	base := t.TempDir()
	// 在两个目录下各放一个 skill
	for _, dir := range []string{".claude/skills/foo", ".agents/skills/bar"} {
		full := filepath.Join(base, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "SKILL.md"), []byte("---\n---\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	settings := &memorySettingStore{data: map[string]string{
		"skill_scan_dirs": `[".claude/skills"]`,
	}}
	reg := NewRegistry()
	fl := NewFileLoader(reg, nil, settings, nil)
	if err := fl.LoadGlobal(base); err != nil {
		t.Fatalf("LoadGlobal failed: %v", err)
	}

	if _, ok := reg.Get("foo"); !ok {
		t.Errorf("expected 'foo' skill from .claude/skills")
	}
	if _, ok := reg.Get("bar"); ok {
		t.Errorf("did not expect 'bar' skill; .agents/skills should be disabled")
	}
}

func TestFileLoaderRefreshUnloadsDeletedSkill(t *testing.T) {
	base := t.TempDir()
	skillDir := filepath.Join(base, ".claude", "skills", "delete-me")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\n---\nbody"), 0644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	fl := NewFileLoader(reg, nil, nil, nil)
	if err := fl.LoadGlobal(base); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("delete-me"); !ok {
		t.Fatalf("expected 'delete-me' after initial load")
	}

	// 删除 skill 文件
	if err := os.RemoveAll(skillDir); err != nil {
		t.Fatal(err)
	}

	if err := fl.RefreshAll(base, nil, nil); err != nil {
		t.Fatalf("RefreshAll failed: %v", err)
	}
	if _, ok := reg.Get("delete-me"); ok {
		t.Errorf("expected 'delete-me' to be unloaded after refresh")
	}
}

func TestFileLoaderFrontmatterOverrides(t *testing.T) {
	base := t.TempDir()
	skillDir := filepath.Join(base, ".claude", "skills", "override-test")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := `---
id: my/custom-id
display_name: Custom Name
description: Custom description
tags: [go, backend]
scope: global
template_name: task_prompt
---
Custom body.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	fl := NewFileLoader(reg, nil, nil, nil)
	if err := fl.LoadForWorkdir(base, ""); err != nil {
		t.Fatalf("LoadForWorkdir failed: %v", err)
	}

	s, ok := reg.Get("my/custom-id")
	if !ok {
		t.Fatalf("expected skill 'my/custom-id'")
	}
	if s.DisplayName != "Custom Name" || s.Description != "Custom description" {
		t.Errorf("unexpected metadata: %+v", s)
	}
	if len(s.Tags) != 2 || s.Tags[0] != "go" {
		t.Errorf("unexpected tags: %v", s.Tags)
	}
	// 显式 scope=global 覆盖项目级默认值。
	if s.Scope != SkillScopeGlobal {
		t.Errorf("scope = %q, want global", s.Scope)
	}
	if len(s.Templates) != 1 || s.Templates[0].Name != "task_prompt" {
		t.Errorf("unexpected template name: %+v", s.Templates)
	}
}

func TestFileLoaderInvalidSkillRegisteredAsInvalid(t *testing.T) {
	base := t.TempDir()
	skillDir := filepath.Join(base, ".claude", "skills", "broken")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\n{not yaml\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	fl := NewFileLoader(reg, nil, nil, nil)
	if err := fl.LoadGlobal(base); err != nil {
		t.Fatalf("LoadGlobal failed: %v", err)
	}

	s, ok := reg.Get("broken")
	if !ok {
		t.Fatalf("expected 'broken' skill to appear as invalid")
	}
	if s.State != SkillStateInvalid {
		t.Errorf("state = %q, want invalid", s.State)
	}
	if s.InvalidReason == "" {
		t.Errorf("expected InvalidReason to be set")
	}
}

type memorySettingStore struct {
	data map[string]string
}

func (m *memorySettingStore) GetSetting(key string) (string, error) {
	return m.data[key], nil
}

func (m *memorySettingStore) SetSetting(key, value string) error {
	m.data[key] = value
	return nil
}
