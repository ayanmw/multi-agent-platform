package skill

import (
	"fmt"
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

	if _, _, err := fl.RefreshAll(base, nil, nil); err != nil {
		t.Fatalf("RefreshAll failed: %v", err)
	}
	if _, ok := reg.Get("delete-me"); ok {
		t.Errorf("expected 'delete-me' to be unloaded after refresh")
	}
}

// TestFileLoaderRefreshAllReturnsUnloadedCount 验证 M4：RefreshAll 返回的
// unloaded / loaded 据实反映本次刷新的卸载与加载计数，而非恒为 0。
func TestFileLoaderRefreshAllReturnsUnloadedCount(t *testing.T) {
	// 先在一个 base 装一个全局 skill。
	seedBase := t.TempDir()
	skillDir := filepath.Join(seedBase, ".claude", "skills", "will-unload")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\n---\nbody"), 0644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	fl := NewFileLoader(reg, nil, nil, nil)
	if err := fl.LoadGlobal(seedBase); err != nil {
		t.Fatal(err)
	}

	// 切到一个空的 base 做 RefreshAll：原 skill 被卸载，新 base 无 skill。
	emptyBase := t.TempDir()
	loaded, unloaded, err := fl.RefreshAll(emptyBase, nil, nil)
	if err != nil {
		t.Fatalf("RefreshAll failed: %v", err)
	}
	if unloaded < 1 {
		t.Fatalf("expected unloaded >= 1 (previously loaded skill unloaded), got %d", unloaded)
	}
	if loaded != 0 {
		t.Fatalf("expected loaded == 0 for empty base, got %d", loaded)
	}
	if _, ok := reg.Get("will-unload"); ok {
		t.Errorf("expected 'will-unload' to be unloaded after refresh")
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

// TestParseSkillFile_CRLF 验证 M2：Windows CRLF 文件的 frontmatter 仍能正确解析，
// 不会把含 `---` 分隔符的原文回退为 system_prompt 模板。
func TestParseSkillFile_CRLF(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "crlf-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	crlf := "---\r\nname: CRLF Skill\r\ndescription: crlf test\r\n---\r\nYou are a CRLF skill.\r\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(crlf), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := parseSkillFile(filepath.Join(skillDir, "SKILL.md"), SkillScopeGlobal, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.DisplayName != "CRLF Skill" {
		t.Fatalf("display_name = %q, want 'CRLF Skill'", s.DisplayName)
	}
	if s.Description != "crlf test" {
		t.Fatalf("description = %q, want 'crlf test'", s.Description)
	}
	if len(s.Templates) != 1 || s.Templates[0].Content != "You are a CRLF skill." {
		t.Fatalf("template body should not contain `---` separators, got %+v", s.Templates)
	}
}

// TestParseSkillFile_EmptyFrontmatter 验证 M3：空 frontmatter（`---\n---\nbody`）
// 不再把分隔符当 body 写进模板。
func TestParseSkillFile_EmptyFrontmatter(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "empty-fm")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\n---\njust body"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := parseSkillFile(filepath.Join(skillDir, "SKILL.md"), SkillScopeGlobal, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(s.Templates))
	}
	if s.Templates[0].Content != "just body" {
		t.Fatalf("body = %q, want 'just body' (no `---`)", s.Templates[0].Content)
	}
}

// TestFileLoaderLoadGlobalDiscoversAgentAndOpencodeDirs 验证 M4：
// .agent/skills 与 .opencode/skills 目录中的 Skill 能被 FileLoader 扫描并注册，
// 模板内容完整保留。
func TestFileLoaderLoadGlobalDiscoversAgentAndOpencodeDirs(t *testing.T) {
	base := t.TempDir()
	for _, tc := range []struct {
		dir   string
		id    string
		body  string
		tmpl  string
	}{
		{".agent/skills/agent-helper", "agent-helper", "body for agent skill.", "system_prompt"},
		{".opencode/skills/opencode-helper", "opencode-helper", "body for opencode skill.", "task_prompt"},
	} {
		full := filepath.Join(base, filepath.FromSlash(tc.dir))
		if err := os.MkdirAll(full, 0755); err != nil {
			t.Fatal(err)
		}
		content := fmt.Sprintf("---\nname: %s\ndescription: desc-%s\ntemplate_name: %s\n---\n%s",
			tc.id, tc.id, tc.tmpl, tc.body)
		if err := os.WriteFile(filepath.Join(full, "SKILL.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	reg := NewRegistry()
	fl := NewFileLoader(reg, nil, nil, nil)
	if err := fl.LoadGlobal(base); err != nil {
		t.Fatalf("LoadGlobal failed: %v", err)
	}

	for _, tc := range []struct {
		id   string
		name string
		tmpl string
		body string
	}{
		{"agent-helper", "agent-helper", "system_prompt", "body for agent skill."},
		{"opencode-helper", "opencode-helper", "task_prompt", "body for opencode skill."},
	} {
		s, ok := reg.Get(tc.id)
		if !ok {
			t.Fatalf("expected skill %q to be registered", tc.id)
		}
		if s.Source != SkillSourceLocalFile {
			t.Errorf("%q: source = %q, want local_file", tc.id, s.Source)
		}
		if s.Scope != SkillScopeGlobal {
			t.Errorf("%q: scope = %q, want global", tc.id, s.Scope)
		}
		if len(s.Templates) != 1 {
			t.Fatalf("%q: expected 1 template, got %d: %+v", tc.id, len(s.Templates), s.Templates)
		}
		if s.Templates[0].Name != tc.tmpl {
			t.Errorf("%q: template name = %q, want %q", tc.id, s.Templates[0].Name, tc.tmpl)
		}
		if s.Templates[0].Content != tc.body {
			t.Errorf("%q: template body = %q, want %q", tc.id, s.Templates[0].Content, tc.body)
		}
	}
}

// TestFileLoaderRefreshGlobalPreservesProject 验证 M1：RefreshGlobal 只重扫全局层，
// 保留 project scope local_file skill 不被清空。
func TestFileLoaderRefreshGlobalPreservesProject(t *testing.T) {
	base := t.TempDir()
	// 全局层 skill
	globalSkill := filepath.Join(base, ".claude", "skills", "g-skill")
	os.MkdirAll(globalSkill, 0755)
	os.WriteFile(filepath.Join(globalSkill, "SKILL.md"), []byte("---\nname: G\n---\ngb"), 0644)
	// project 层 skill（不同 workdir）
	workdir := t.TempDir()
	projSkill := filepath.Join(workdir, ".claude", "skills", "p-skill")
	os.MkdirAll(projSkill, 0755)
	os.WriteFile(filepath.Join(projSkill, "SKILL.md"), []byte("---\nname: P\n---\npb"), 0644)

	reg := NewRegistry()
	fl := NewFileLoader(reg, nil, nil, nil)
	if err := fl.LoadGlobal(base); err != nil {
		t.Fatal(err)
	}
	if err := fl.LoadForWorkdir(workdir, "proj-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("p-skill"); !ok {
		t.Fatal("expected p-skill loaded")
	}

	// RefreshGlobal 只重扫全局层，project skill 必须保留。
	fl.RefreshGlobal(base)
	if _, ok := reg.Get("p-skill"); !ok {
		t.Fatalf("M1: project skill 'p-skill' should survive RefreshGlobal, got: %+v", reg.List(nil))
	}
	if _, ok := reg.Get("g-skill"); !ok {
		t.Fatalf("global skill 'g-skill' should still exist after RefreshGlobal")
	}
}
