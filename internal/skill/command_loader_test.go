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
	if err := loader.RefreshAll(dir, []string{dir}, map[string]string{dir: "p1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := loader.Registry().Get("a"); !ok {
		t.Fatalf("expected a after refresh")
	}
}
