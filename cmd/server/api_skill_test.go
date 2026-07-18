package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/skill"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	_ "modernc.org/sqlite"
)

// setupSkillTestDB 初始化一个临时 SQLite 数据库并跑完整迁移，
// 让 skills 表与生产环境一致。t.Cleanup 负责关闭 db.DB。
func setupSkillTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test_skills.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		db.DB = nil
	})
}

// newSkillTestHarness 构造一组 (registry, store) 并通过 registerSkillRoutes
// 把路由挂到独立的 ServeMux 上，返回 httptest.Server 供端到端测试使用。
//
// 注意：每次调用都使用全新的 registry + store + DB，避免用例间状态串扰。
func newSkillTestHarness(t *testing.T) (*httptest.Server, *skill.Registry, *skill.Store) {
	t.Helper()
	setupSkillTestDB(t)
	registry := skill.NewRegistry()
	store := skill.NewStore(db.DB)
	loader := skill.NewLoader(store, registry)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	mux := http.NewServeMux()
	registerSkillRoutes(mux, nil, store, registry)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, registry, store
}

// TestSkillAPIEndToEnd 覆盖 Skill REST API 的核心端到端流程：
// 1) 列出内置 skill → 2) 搜索 → 3) 创建 local_db skill → 4) 获取详情 →
// 5) 更新 → 6) 禁用 / 启用 → 7) 删除 → 8) 不存在返回 404 / 修改内置返回 403。
func TestSkillAPIEndToEnd(t *testing.T) {
	ts, registry, _ := newSkillTestHarness(t)
	client := ts.Client()

	// 1. GET /api/skills — 应至少包含两个内置 skill。
	resp, err := client.Get(ts.URL + "/api/skills")
	if err != nil {
		t.Fatalf("list skills: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing skills, got %d: %s", resp.StatusCode, body)
	}
	var listed []skill.Skill
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("decode listed: %v", err)
	}
	if len(listed) < 2 {
		t.Fatalf("expected at least 2 builtin skills, got %d", len(listed))
	}
	builtinID := listed[0].ID
	if listed[0].Source != skill.SkillSourceBuiltIn {
		t.Fatalf("first listed skill should be built_in, got %s", listed[0].Source)
	}

	// 1b. GET /api/skills?source=built_in 过滤生效。
	resp, err = client.Get(ts.URL + "/api/skills?source=built_in")
	if err != nil {
		t.Fatalf("list with source filter: %v", err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with source filter, got %d: %s", resp.StatusCode, body)
	}
	var filtered []skill.Skill
	if err := json.Unmarshal([]byte(body), &filtered); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	for _, s := range filtered {
		if s.Source != skill.SkillSourceBuiltIn {
			t.Errorf("source filter leaked: %s has source %s", s.ID, s.Source)
		}
	}

	// 2. GET /api/skills/search?q=code 搜索内置 code-helper。
	resp, err = client.Get(ts.URL + "/api/skills/search?q=" + builtinID)
	if err != nil {
		t.Fatalf("search skills: %v", err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 searching, got %d: %s", resp.StatusCode, body)
	}
	var hits []skill.Skill
	if err := json.Unmarshal([]byte(body), &hits); err != nil {
		t.Fatalf("decode search hits: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected at least 1 search hit for q=%s", builtinID)
	}

	// 3. POST /api/skills — 创建一条 local_db skill。
	createPayload, _ := json.Marshal(map[string]any{
		"id":           "user/test-skill",
		"display_name": "Test Skill",
		"description":  "Created by E2E test",
		"content":      "You are a {{language}} expert.",
		"tags":         []string{"test", "e2e"},
		"parameters": []map[string]any{
			{"name": "language", "type": "string", "required": true, "default": "Go"},
		},
	})
	resp, err = client.Post(ts.URL+"/api/skills", "application/json", bytes.NewReader(createPayload))
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating skill, got %d: %s", resp.StatusCode, body)
	}
	var created skill.Skill
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID != "user/test-skill" {
		t.Fatalf("expected created id user/test-skill, got %s", created.ID)
	}
	if created.Source != skill.SkillSourceLocalDB {
		t.Fatalf("expected source local_db, got %s", created.Source)
	}
	if !created.IsLocalEditable {
		t.Fatalf("expected local editable")
	}
	if created.State != skill.SkillStateEnabled {
		t.Fatalf("expected state enabled, got %s", created.State)
	}
	if len(created.Templates) != 1 || created.Templates[0].Name != "system_prompt" {
		t.Fatalf("expected one system_prompt template, got %+v", created.Templates)
	}

	// 3b. 重复创建应返回 400。
	resp, err = client.Post(ts.URL+"/api/skills", "application/json", bytes.NewReader(createPayload))
	if err != nil {
		t.Fatalf("duplicate create: %v", err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on duplicate, got %d: %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// 3c. 缺字段应返回 400。
	badPayload, _ := json.Marshal(map[string]any{"id": "user/bad"})
	resp, err = client.Post(ts.URL+"/api/skills", "application/json", bytes.NewReader(badPayload))
	if err != nil {
		t.Fatalf("bad create: %v", err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on missing fields, got %d: %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// 4. GET /api/skills/:id — 获取刚创建的 skill。
	resp, err = client.Get(ts.URL + "/api/skills/user/test-skill")
	if err != nil {
		t.Fatalf("get skill: %v", err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 getting skill, got %d: %s", resp.StatusCode, body)
	}
	var fetched skill.Skill
	if err := json.Unmarshal([]byte(body), &fetched); err != nil {
		t.Fatalf("decode fetched: %v", err)
	}
	if fetched.DisplayName != "Test Skill" {
		t.Errorf("expected display_name Test Skill, got %s", fetched.DisplayName)
	}

	// 4b. GET 不存在 → 404。
	resp, err = client.Get(ts.URL + "/api/skills/does/not-exist")
	if err != nil {
		t.Fatalf("get missing skill: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for missing skill, got %d", resp.StatusCode)
	}

	// 5. PUT /api/skills/:id — 更新 display_name / description / content。
	updatePayload, _ := json.Marshal(map[string]any{
		"display_name": "Updated Skill",
		"description":  "Updated description",
		"content":      "You are a {{language}} master.",
	})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/skills/user/test-skill", bytes.NewReader(updatePayload))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("update skill: %v", err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 updating skill, got %d: %s", resp.StatusCode, body)
	}
	var updated skill.Skill
	if err := json.Unmarshal([]byte(body), &updated); err != nil {
		t.Fatalf("decode updated: %v", err)
	}
	if updated.DisplayName != "Updated Skill" {
		t.Errorf("expected updated display_name, got %s", updated.DisplayName)
	}
	if updated.Description != "Updated description" {
		t.Errorf("expected updated description, got %s", updated.Description)
	}
	if len(updated.Templates) != 1 || updated.Templates[0].Content != "You are a {{language}} master." {
		t.Errorf("expected updated template content, got %+v", updated.Templates)
	}

	// 5b. PUT 内置 skill → 403。
	builtinUpdate, _ := json.Marshal(map[string]any{"display_name": "Hacked"})
	req, _ = http.NewRequest(http.MethodPut, ts.URL+"/api/skills/"+builtinID, bytes.NewReader(builtinUpdate))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("update builtin: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 updating builtin, got %d", resp.StatusCode)
	}

	// 5c. PUT 不存在 → 404。
	req, _ = http.NewRequest(http.MethodPut, ts.URL+"/api/skills/missing/x", bytes.NewReader(builtinUpdate))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("update missing: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 updating missing, got %d", resp.StatusCode)
	}

	// 6. POST /api/skills/:id/disable — 禁用。
	resp, err = client.Post(ts.URL+"/api/skills/user/test-skill/disable", "application/json", nil)
	if err != nil {
		t.Fatalf("disable skill: %v", err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 disabling skill, got %d: %s", resp.StatusCode, body)
	}
	var disabled skill.Skill
	if err := json.Unmarshal([]byte(body), &disabled); err != nil {
		t.Fatalf("decode disabled: %v", err)
	}
	if disabled.State != skill.SkillStateDisabled {
		t.Fatalf("expected state disabled, got %s", disabled.State)
	}
	// 验证 registry 状态已同步。
	if s, ok := registry.Get("user/test-skill"); !ok || s.State != skill.SkillStateDisabled {
		t.Fatalf("registry state not synced: %+v", s)
	}

	// 6b. 再次 disable 应保持幂等（返回 200，状态不变）。
	resp, err = client.Post(ts.URL+"/api/skills/user/test-skill/disable", "application/json", nil)
	if err != nil {
		t.Fatalf("idempotent disable: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on idempotent disable, got %d", resp.StatusCode)
	}

	// 6c. POST /api/skills/:id/enable — 启用。
	resp, err = client.Post(ts.URL+"/api/skills/user/test-skill/enable", "application/json", nil)
	if err != nil {
		t.Fatalf("enable skill: %v", err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 enabling skill, got %d: %s", resp.StatusCode, body)
	}
	var enabled skill.Skill
	if err := json.Unmarshal([]byte(body), &enabled); err != nil {
		t.Fatalf("decode enabled: %v", err)
	}
	if enabled.State != skill.SkillStateEnabled {
		t.Fatalf("expected state enabled, got %s", enabled.State)
	}

	// 6d. enable 不存在 → 404。
	resp, err = client.Post(ts.URL+"/api/skills/missing/x/enable", "application/json", nil)
	if err != nil {
		t.Fatalf("enable missing: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 enabling missing, got %d", resp.StatusCode)
	}

	// 7. DELETE /api/skills/:id — 删除 local_db skill。
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/api/skills/user/test-skill", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("delete skill: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 deleting skill, got %d", resp.StatusCode)
	}
	if registry.Exists("user/test-skill") {
		t.Fatalf("skill should be removed from registry after delete")
	}

	// 7b. DELETE 内置 → 403。
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/api/skills/"+builtinID, nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("delete builtin: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 deleting builtin, got %d", resp.StatusCode)
	}

	// 7c. DELETE 不存在 → 404。
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/api/skills/missing/x", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 deleting missing, got %d", resp.StatusCode)
	}

	// 8. 删除后 GET 应返回 404。
	resp, err = client.Get(ts.URL + "/api/skills/user/test-skill")
	if err != nil {
		t.Fatalf("get deleted skill: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

// readBody 读取并关闭 resp.Body，返回字符串体；测试中简化样板代码。
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf.String()
}
