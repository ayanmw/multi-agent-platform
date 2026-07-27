package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/skill"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// newFileSkillTestHarness 构造一个独立的 skill 测试服务器，允许显式指定全局扫描目录 globalDir。
// 避免与 api_skill_test.go 的 newSkillTestHarness 在该变更中不断耦合。
func newFileSkillTestHarness(t *testing.T, globalDir string) (*httptest.Server, *skill.Registry) {
	t.Helper()
	setupSkillTestDB(t)
	registry := skill.NewRegistry()
	store := skill.NewStore(db.DB)
	loader := skill.NewLoader(store, registry)
	fl := skill.NewFileLoader(registry, store, &dbSkillSettingStore{}, nil)
	loader.SetFileLoader(fl, globalDir)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	mux := http.NewServeMux()
	registerSkillRoutes(mux, nil, store, registry, loader, &dbSkillSettingStore{})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, registry
}

func TestSkillScanAPI(t *testing.T) {
	base := t.TempDir()
	skillDir := filepath.Join(base, ".claude", "skills", "scanner-api-test")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: Scanner API Skill\n---\nLoaded from filesystem via scan API.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ts, reg := newFileSkillTestHarness(t, base)
	client := ts.Client()

	// GET /api/skills/scan-config 默认返回全部目录。
	resp, err := client.Get(ts.URL + "/api/skills/scan-config")
	if err != nil {
		t.Fatalf("get scan-config: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 scan-config, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, ".claude/skills") {
		t.Errorf("scan-config should include .claude/skills: %s", body)
	}

	// POST /api/skills/scan-config 关闭 .agents/skills。
	payload, _ := json.Marshal(map[string]any{"enabled_dirs": []string{".claude/skills", ".agent/skills"}})
	resp, err = client.Post(ts.URL+"/api/skills/scan-config", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("set scan-config: %v", err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 set scan-config, got %d: %s", resp.StatusCode, body)
	}
	if strings.Contains(body, ".agents/skills") {
		t.Errorf("scan-config should not include .agents/skills: %s", body)
	}

	// 直接断言 registry 中已发现 global skill。
	if _, ok := reg.Get("scanner-api-test"); !ok {
		t.Errorf("expected 'scanner-api-test' in registry after LoadAll")
	}
}

func TestSkillScanEndpoint(t *testing.T) {
	workdir := t.TempDir()
	skillDir := filepath.Join(workdir, ".claude", "skills", "scan-endpoint-test")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\n---\nbody"), 0644); err != nil {
		t.Fatal(err)
	}

	ts, reg := newFileSkillTestHarness(t, t.TempDir())
	client := ts.Client()

	// 在 harness 初始化 DB 之后创建 session，确保 InsertSession 与 API handler 使用同一个 db.DB。
	sessionID := "test_scan_session"
	if err := db.InsertSession(db.SessionRecord{
		ID:           sessionID,
		Name:         "scan-test",
		Status:       "empty",
		WorkspaceDir: workdir,
		ProjectID:    "default",
	}); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// 调用 scan 接口应能发现并注册 workdir 中的 skill（通过 session 的 workspace_dir）。
	resp, err := client.Post(ts.URL+"/api/skills/scan", "application/json", nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 scan, got %d: %s", resp.StatusCode, body)
	}

	// 验证 registry 中存在该文件系统 skill。
	_, ok := reg.Get("scan-endpoint-test")
	if !ok {
		t.Errorf("expected 'scan-endpoint-test' in registry after scan")
	}
}
