package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func setupCommandDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, content := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestParseCommandFile_IDPriorities(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	rel := "test.md"

	// command > id > path
	os.WriteFile(path, []byte("---\ncommand: custom:cmd\nid: from-id\nname: Test\n---\nbody"), 0644)
	cmd, err := parseCommandFile(path, rel, SkillCommandScopeProject, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.ID != "custom:cmd" {
		t.Fatalf("command priority: got %s", cmd.ID)
	}

	os.WriteFile(path, []byte("---\nid: from-id\nname: Test\n---\nbody"), 0644)
	cmd, err = parseCommandFile(path, rel, SkillCommandScopeProject, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.ID != "from-id" {
		t.Fatalf("id priority: got %s", cmd.ID)
	}

	os.WriteFile(path, []byte("body only"), 0644)
	cmd, err = parseCommandFile(path, "ops/new.md", SkillCommandScopeProject, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.ID != "ops:new" {
		t.Fatalf("path priority: got %s", cmd.ID)
	}
}

func TestCommandLoader_LoadGlobal(t *testing.T) {
	files := map[string]string{
		".claude/commands/global/greet.md": "---\nname: Greet\n---\nsay hi",
	}
	dir := setupCommandDir(t, files)
	loader := NewCommandLoader(NewCommandRegistry(), nil)
	if err := loader.LoadGlobal(dir); err != nil {
		t.Fatal(err)
	}
	cmd, ok := loader.Registry().Get("global:greet")
	if !ok {
		t.Fatalf("expected global:greet")
	}
	if cmd.Scope != SkillCommandScopeGlobal {
		t.Fatalf("expected global scope, got %s", cmd.Scope)
	}
}

func TestCommandLoader_ProjectScope(t *testing.T) {
	files := map[string]string{
		".claude/commands/ops/new.md": "---\nname: New\nskill: openspec-new-change\n---\nhelp",
	}
	dir := setupCommandDir(t, files)
	loader := NewCommandLoader(NewCommandRegistry(), nil)
	if err := loader.LoadForWorkdir(dir, "proj-1"); err != nil {
		t.Fatal(err)
	}
	cmd, ok := loader.Registry().Get("ops:new")
	if !ok {
		t.Fatalf("expected ops:new")
	}
	if cmd.Scope != SkillCommandScopeProject {
		t.Fatalf("expected project scope")
	}
	if cmd.ProjectID != "proj-1" {
		t.Fatalf("unexpected project id: %s", cmd.ProjectID)
	}
	if cmd.SkillID != "openspec-new-change" {
		t.Fatalf("unexpected skill id: %s", cmd.SkillID)
	}
}

func TestCommandLoader_RefreshAll(t *testing.T) {
	files := map[string]string{
		".claude/commands/a.md": "---\nname: A\n---\na",
	}
	dir := setupCommandDir(t, files)
	loader := NewCommandLoader(NewCommandRegistry(), nil)
	if _, _, err := loader.RefreshAll(dir, []string{dir}, map[string]string{dir: "p1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := loader.Registry().Get("a"); !ok {
		t.Fatalf("expected a after refresh")
	}
}

// TestCommandLoader_GlobalAndProjectSameIDNoOverride 验证 H4：同名命令（global vs project）
// 不互相覆盖。旧实现单维 byID map 会让后注册的 project 版本覆盖 global 版本。
func TestCommandLoader_GlobalAndProjectSameIDNoOverride(t *testing.T) {
	globalDir := setupCommandDir(t, map[string]string{
		".claude/commands/greet.md": "---\nname: Global Greet\n---\nglobal body",
	})
	workdir := setupCommandDir(t, map[string]string{
		".claude/commands/greet.md": "---\nname: Proj Greet\n---\nproject body",
	})
	loader := NewCommandLoader(NewCommandRegistry(), nil)
	if err := loader.LoadGlobal(globalDir); err != nil {
		t.Fatal(err)
	}
	if err := loader.LoadForWorkdir(workdir, "proj-1"); err != nil {
		t.Fatal(err)
	}
	// Get(裸ID) 优先返回 project 版本（同名时 project 覆盖 global 语义）。
	cmd, ok := loader.Registry().Get("greet")
	if !ok {
		t.Fatalf("expected greet command")
	}
	if cmd.Scope != SkillCommandScopeProject {
		t.Fatalf("expected project scope for bare-ID Get (project overrides global), got %s", cmd.Scope)
	}
	// List("") 应同时返回 global + project 两条（不互相覆盖）。
	all := loader.Registry().List("")
	var globalCount, projectCount int
	for _, c := range all {
		if c.ID != "greet" {
			continue
		}
		switch c.Scope {
		case SkillCommandScopeGlobal:
			globalCount++
		case SkillCommandScopeProject:
			projectCount++
		}
	}
	if globalCount != 1 || projectCount != 1 {
		t.Fatalf("expected 1 global + 1 project greet, got global=%d project=%d", globalCount, projectCount)
	}
	// UnloadForWorkdir 只卸载 project 版本，global 保留（M12）。
	loader.UnloadForWorkdir(workdir)
	if _, ok := loader.Registry().Get("greet"); !ok {
		t.Fatalf("global greet should survive project unload (M12)")
	}
}

// TestParseCommandFile_CRLF 验证 M2：CRLF 文件的 frontmatter 仍能正确解析。
func TestParseCommandFile_CRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crlf.md")
	crlf := "---\r\nname: CRLF Cmd\r\ndescription: crlf test\r\n---\r\nbody line\r\n"
	if err := os.WriteFile(path, []byte(crlf), 0644); err != nil {
		t.Fatal(err)
	}
	cmd, err := parseCommandFile(path, "crlf.md", SkillCommandScopeProject, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Name != "CRLF Cmd" {
		t.Fatalf("expected name 'CRLF Cmd' from CRLF frontmatter, got %q", cmd.Name)
	}
	if cmd.Description != "crlf test" {
		t.Fatalf("expected description 'crlf test', got %q", cmd.Description)
	}
	if cmd.Prompt != "body line" {
		t.Fatalf("expected body 'body line', got %q", cmd.Prompt)
	}
}

// TestParseCommandFile_InvalidIDFallback 验证 M13：frontmatter 写非法 id（如含路径遍历）
// 时回退到 rel 路径生成的合法 id。
func TestParseCommandFile_InvalidIDFallback(t *testing.T) {
	dir := t.TempDir()
	// command 文件位于 .claude/commands/sub/evil.md，rel = "sub/evil.md"，合法 fallback id = "sub:evil"。
	path := filepath.Join(dir, ".claude", "commands", "sub", "evil.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	// frontmatter command 含路径遍历字符，非法。
	if err := os.WriteFile(path, []byte("---\ncommand: ../evil\nname: Evil\n---\nbody"), 0644); err != nil {
		t.Fatal(err)
	}
	loader := NewCommandLoader(NewCommandRegistry(), nil)
	if err := loader.LoadForWorkdir(dir, "proj-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := loader.Registry().Get("sub:evil"); !ok {
		t.Fatalf("expected fallback id 'sub:evil', registry: %+v", loader.Registry().List(""))
	}
}
