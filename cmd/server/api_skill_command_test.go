package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/skill"
)

func setupSkillCommandTestServer(t *testing.T, files map[string]string) (*httptest.Server, *skill.Loader, string) {
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

	skillRegistry := skill.NewRegistry()
	skillLoader := skill.NewLoader(nil, skillRegistry)
	fileLoader := skill.NewFileLoader(skillRegistry, nil, nil, nil)
	cmdLoader := skill.NewCommandLoader(skill.NewCommandRegistry(), nil)
	skillLoader.SetFileLoader(fileLoader, dir)
	skillLoader.SetCommandLoader(cmdLoader)
	if err := skillLoader.LoadAll(); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerSkillCommandRoutes(mux, nil, skillLoader, nil, skillRegistry)
	return httptest.NewServer(mux), skillLoader, dir
}

func TestAPIListSkillCommands(t *testing.T) {
	files := map[string]string{
		".claude/commands/ops/new.md": "---\nname: New\nskill: openspec-new-change\n---\nhelp",
	}
	ts, _, _ := setupSkillCommandTestServer(t, files)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/skill-commands")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	var body skillCommandListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(body.Commands))
	}
	if body.Commands[0].ID != "ops:new" {
		t.Fatalf("unexpected id: %s", body.Commands[0].ID)
	}
}

func TestAPIGetSkillCommand(t *testing.T) {
	files := map[string]string{
		".claude/commands/ops/new.md": "---\nname: New\n---\nhelp text",
	}
	ts, _, _ := setupSkillCommandTestServer(t, files)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/skill-commands/ops:new")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	var body skillCommandDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Prompt, "help text") {
		t.Fatalf("expected prompt, got %s", body.Prompt)
	}
}

func TestAPIInvokeSkillCommand_EnableSkillAndTemporary(t *testing.T) {
	files := map[string]string{
		".claude/commands/ops/new.md": "---\nname: New\nskill: openspec-new-change\n---\nhelp",
	}
	ts, _, _ := setupSkillCommandTestServer(t, files)
	defer ts.Close()

	// register target skill as disabled
	// Can't do without database store in this test path; test via temporary skill behavior instead.
	body := map[string]string{"workdir": "unused"}
	b, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+"/api/skill-commands/ops:new/invoke", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	var result invokeSkillCommandResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.TemporarySkillID != "cmd:ops:new" {
		t.Fatalf("unexpected temporary skill id: %s", result.TemporarySkillID)
	}
}

// TestTemporarySkillResolveActiveSkillsWithExtra 验证 C1 修复：
// session scope 的临时 skill（如 cmd:xxx）通过 ResolveActiveSkillsWithExtra 的
// extraIDs 只对当前 run 注入：当前 run 包含、无 extraIDs 的其它 run 不包含。
func TestTemporarySkillResolveActiveSkillsWithExtra(t *testing.T) {
	reg := skill.NewRegistry()
	tmp := skill.Skill{
		ID:          "cmd:my-cmd",
		State:       skill.SkillStateEnabled,
		Scope:       skill.SkillScopeSession,
		WorkspaceDir: "/home/user/proj",
		ProjectID:   "proj-a",
		Templates:   []skill.SkillTemplate{{Name: "system_prompt", Content: "prompt", Variables: nil}},
	}
	reg.Register(tmp)
	global := skill.Skill{ID: "global-sk", State: skill.SkillStateEnabled, Scope: skill.SkillScopeGlobal}
	reg.Register(global)

	// 无 extraIDs 时 session scope 的临时 skill 不应出现。
	ids := skill.ResolveActiveSkillsWithExtra(reg, "proj-a", "/home/user/proj", nil)
	if contains(ids, "cmd:my-cmd") {
		t.Fatalf("extraIDs 为空时 session scope 临时 skill 不应进入 ActiveSkills，got %v", ids)
	}
	if !contains(ids, "global-sk") {
		t.Fatalf("global skill 应在 ActiveSkills 中，got %v", ids)
	}

	// 传入 extraIDs 时临时 skill 被强制纳入。
	ids = skill.ResolveActiveSkillsWithExtra(reg, "proj-a", "/home/user/proj", []string{"cmd:my-cmd"})
	if !contains(ids, "cmd:my-cmd") {
		t.Fatalf("extraIDs 包含 cmd:my-cmd 时应注入当前 run，got %v", ids)
	}
	if !contains(ids, "global-sk") {
		t.Fatalf("global skill 应在 ActiveSkills 中，got %v", ids)
	}

	// 另一 session 的 workspaceDir 不同时，仍需主动传 extraIDs 才注入（验证不自动污染）。
	ids = skill.ResolveActiveSkillsWithExtra(reg, "other-proj", "/home/other", nil)
	if contains(ids, "cmd:my-cmd") {
		t.Fatalf("其它 session 且无 extraIDs 时不应注入临时 skill，got %v", ids)
	}
}

// TestTemporarySkillCommandFlow 验证 registerTemporarySkill + invoke 返回的 temporary_skill_id
// 能被 runner 通过 ResolveActiveSkillsWithExtra 注入当前 run，且仅限当前 run。
func TestTemporarySkillCommandFlow(t *testing.T) {
	reg := skill.NewRegistry()
	cmd := skill.SkillCommand{
		ID:          "cmd-test",
		Name:        "Test Cmd",
		Prompt:      "You are a test command.",
		Scope:       skill.SkillCommandScopeProject,
		ProjectID:   "proj-a",
		WorkspaceDir: "/home/user/proj-a",
	}
	tmpID := registerTemporarySkill(nil, reg, cmd)
	if tmpID != "cmd:cmd-test" {
		t.Fatalf("expected temporary_skill_id=cmd:cmd-test, got %s", tmpID)
	}

	// 模拟 session chat WITHOUT extraIDs → 不应注入 session scope 的临时 skill。
	ids := skill.ResolveActiveSkillsWithExtra(reg, "proj-a", "/home/user/proj-a", nil)
	if contains(ids, tmpID) {
		t.Fatalf("无 extraIDs 的 run 不应注入临时 skill，got %v", ids)
	}

	// 模拟 session chat WITH extraIDs → 应注入。
	ids = skill.ResolveActiveSkillsWithExtra(reg, "proj-a", "/home/user/proj-a", []string{tmpID})
	if !contains(ids, tmpID) {
		t.Fatalf("有 extraIDs 的 run 应注入临时 skill，got %v", ids)
	}

	// 模拟其它 session 的 run → 不应自动注入（验证不污染其它 run）。
	ids = skill.ResolveActiveSkillsWithExtra(reg, "other-proj", "/home/other", nil)
	if contains(ids, tmpID) {
		t.Fatalf("其它 session 的 run 不应注入 session scope 临时 skill，got %v", ids)
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
