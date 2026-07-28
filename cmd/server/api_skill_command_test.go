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
