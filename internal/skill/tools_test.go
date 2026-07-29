package skill

import (
	"database/sql"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
	_ "modernc.org/sqlite"
)

func initToolsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	schema := "CREATE TABLE IF NOT EXISTS skills (" +
		"id TEXT PRIMARY KEY," +
		"version TEXT NOT NULL DEFAULT '1.0.0'," +
		"display_name TEXT NOT NULL," +
		"description TEXT DEFAULT ''," +
		"authors_json TEXT DEFAULT '[]'," +
		"tags_json TEXT DEFAULT '[]'," +
		"source TEXT NOT NULL DEFAULT 'local_db'," +
		"source_url TEXT DEFAULT ''," +
		"is_local_editable BOOLEAN DEFAULT 1," +
		"templates_json TEXT DEFAULT '[]'," +
		"parameters_json TEXT DEFAULT '[]'," +
		"required_tools_json TEXT DEFAULT '[]'," +
		"suggested_tools_json TEXT DEFAULT '[]'," +
		"permissions_json TEXT DEFAULT '[]'," +
		"triggers_json TEXT DEFAULT '{}'," +
		"state TEXT NOT NULL DEFAULT 'discovered'," +
		"invalid_reason TEXT DEFAULT ''," +
		"scope TEXT DEFAULT 'global'," +
		"project_id TEXT DEFAULT ''," +
		"workspace_dir TEXT DEFAULT ''," +
		"created_at INTEGER NOT NULL," +
		"updated_at INTEGER NOT NULL)"
	if _, err := sqlDB.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return sqlDB
}

func newSkillToolRegistry(t *testing.T) (*Store, *Registry) {
	sqlDB := initToolsTestDB(t)
	t.Cleanup(func() { sqlDB.Close() })
	store := NewStore(sqlDB)
	registry := NewRegistry()
	for _, s := range DefaultBuiltins() {
		registry.Register(*s)
	}
	return store, registry
}

func TestCreateAndDeleteSkillTool(t *testing.T) {
	store, registry := newSkillToolRegistry(t)

	toolRegistry := tool.NewRegistry()
	toolRegistry.Register(NewSkillCreateLocalTool(store, registry))
	toolRegistry.Register(NewSkillDeleteLocalTool(store, registry))
	toolRegistry.Register(NewSkillListTool(registry))

	createInput := map[string]any{
		"id":           "test/skill-tool",
		"display_name": "Tool Test Skill",
		"description":  "Created by skill tool test",
		"content":      "Focus on {{topic}}.",
		"parameters": []any{
			map[string]any{"name": "topic", "type": "string", "required": true},
		},
	}

	res, err := toolRegistry.Execute("skill/create_local", createInput)
	if err != nil {
		t.Fatalf("create_local execute: %v", err)
	}
	created := res.(map[string]any)
	if created["id"] != "test/skill-tool" {
		t.Fatalf("expected created id test/skill-tool, got %v", created["id"])
	}

	if _, ok := registry.Get("test/skill-tool"); !ok {
		t.Fatalf("skill should be registered after create")
	}
	if _, err := store.Get("test/skill-tool"); err != nil {
		t.Fatalf("skill should be persisted: %v", err)
	}

	listRes, err := toolRegistry.Execute("skill/list", nil)
	if err != nil {
		t.Fatalf("list execute: %v", err)
	}
	skills := listRes.([]map[string]any)
	if len(skills) != 3 { // 2 built-in + 1 created
		t.Fatalf("expected 3 skills listed, got %d", len(skills))
	}

	deleteRes, err := toolRegistry.Execute("skill/delete_local", map[string]any{"id": "test/skill-tool"})
	if err != nil {
		t.Fatalf("delete_local execute: %v", err)
	}
	deleted := deleteRes.(map[string]any)
	if deleted["deleted"] != true {
		t.Fatalf("expected deleted=true")
	}

	if _, ok := registry.Get("test/skill-tool"); ok {
		t.Fatalf("skill should be unregistered after delete")
	}
	if _, err := store.Get("test/skill-tool"); err == nil {
		t.Fatalf("skill should be removed from store after delete")
	}
}

// TestSkillCreateGetUpdateDeleteFlow 覆盖 CRUD 完整流程：创建 → 列表 → 获取 → 更新 → 禁用/启用 → 删除。
func TestSkillCreateGetUpdateDeleteFlow(t *testing.T) {
	store, registry := newSkillToolRegistry(t)

	create := NewSkillCreateLocalTool(store, registry)
	_, err := create.Execute(map[string]any{
		"id":           "local/test-flow",
		"display_name": "Flow Skill",
		"description":  "desc",
		"content":      "Hello {{name}}.",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// list 应包含刚创建的 skill（摘要）。
	listTool := NewSkillListTool(registry)
	listRes, err := listTool.Execute(map[string]any{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listRes.([]map[string]any)) < 1 {
		t.Fatalf("expected at least 1 skill in list, got %d", len(listRes.([]map[string]any)))
	}

	// get 应返回完整 skill。
	getTool := NewSkillGetTool(registry)
	getRes, err := getTool.Execute(map[string]any{"id": "local/test-flow"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got := getRes.(map[string]any)
	if got["display_name"] != "Flow Skill" {
		t.Fatalf("expected display_name Flow Skill, got %v", got["display_name"])
	}

	// update 修改 display_name 和 content。
	updateTool := NewSkillUpdateLocalTool(store, registry)
	updateRes, err := updateTool.Execute(map[string]any{
		"id": "local/test-flow",
		"updates": map[string]any{
			"display_name": "Updated Flow",
			"content":      "Hi {{name}}!",
		},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	updateMap := updateRes.(map[string]any)
	if updateMap["forked_from"] != false {
		t.Fatalf("local_db update should not fork")
	}
	summary := updateMap["skill"].(map[string]any)
	if summary["display_name"] != "Updated Flow" {
		t.Fatalf("expected updated display_name, got %v", summary["display_name"])
	}

	// get 再查确认模板已改。
	getRes, err = getTool.Execute(map[string]any{"id": "local/test-flow"})
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	templates := getRes.(map[string]any)["templates"].([]any)
	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	tmpl := templates[0].(map[string]any)
	if tmpl["content"] != "Hi {{name}}!" {
		t.Fatalf("expected updated content, got %v", tmpl["content"])
	}

	// disable → 再次 list 不应出现禁用项（list 不过滤状态）。
	disable := NewSkillDisableTool(store, registry)
	_, err = disable.Execute(map[string]any{"id": "local/test-flow"})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	s, _ := registry.Get("local/test-flow")
	if s.State != SkillStateDisabled {
		t.Fatalf("expected disabled state, got %s", s.State)
	}

	// 持久化验证：新 registry 加载后仍为 disabled。
	newReg := NewRegistry()
	for _, bs := range DefaultBuiltins() {
		newReg.Register(*bs)
	}
	all, _ := store.ListAll()
	for _, sk := range all {
		if sk.Scope == "" {
			sk.Scope = SkillScopeGlobal
		}
		newReg.Register(sk)
	}
	if restored, ok := newReg.Get("local/test-flow"); !ok || restored.State != SkillStateDisabled {
		t.Fatalf("expected restored disabled state, got %+v", restored)
	}

	enable := NewSkillEnableTool(store, registry)
	_, err = enable.Execute(map[string]any{"id": "local/test-flow"})
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	s, _ = registry.Get("local/test-flow")
	if s.State != SkillStateEnabled {
		t.Fatalf("expected enabled state, got %s", s.State)
	}

	// search 命中。
	search := NewSkillSearchTool(registry)
	searchRes, err := search.Execute(map[string]any{"q": "Updated Flow"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(searchRes.([]map[string]any)) != 1 {
		t.Fatalf("expected 1 search hit, got %d", len(searchRes.([]map[string]any)))
	}

	// delete 删除。
	deleteTool := NewSkillDeleteLocalTool(store, registry)
	_, err = deleteTool.Execute(map[string]any{"id": "local/test-flow"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if registry.Exists("local/test-flow") {
		t.Fatalf("skill should be deleted")
	}
}

// TestSkillUpdateForksBuiltIn 验证更新 built_in skill 时自动 fork 为 local_db shadow，
// 且删除 shadow 后 built_in 重新出现。
func TestSkillUpdateForksBuiltIn(t *testing.T) {
	store, registry := newSkillToolRegistry(t)

	if !registry.Exists("builtin-code-helper") {
		t.Fatal("expected builtin-code-helper to exist")
	}
	original, _ := registry.Get("builtin-code-helper")
	if original.Source != SkillSourceBuiltIn {
		t.Fatalf("expected built_in source, got %s", original.Source)
	}

	updateTool := NewSkillUpdateLocalTool(store, registry)
	res, err := updateTool.Execute(map[string]any{
		"id": "builtin-code-helper",
		"updates": map[string]any{
			"display_name": "Forked Code Helper",
			"content":      "You are a {{language}} wizard.",
		},
	})
	if err != nil {
		t.Fatalf("update builtin: %v", err)
	}
	resMap := res.(map[string]any)
	if resMap["forked_from"] != true {
		t.Fatalf("expected forked_from true, got %v", resMap["forked_from"])
	}

	s, _ := registry.Get("builtin-code-helper")
	if s.Source != SkillSourceLocalDB {
		t.Fatalf("expected shadow source local_db, got %s", s.Source)
	}
	if s.DisplayName != "Forked Code Helper" {
		t.Fatalf("expected forked display name, got %s", s.DisplayName)
	}

	// store 中应能读到 shadow。
	stored, err := store.Get("builtin-code-helper")
	if err != nil {
		t.Fatalf("store get shadow: %v", err)
	}
	if stored.Source != SkillSourceLocalDB {
		t.Fatalf("expected stored shadow source local_db, got %s", stored.Source)
	}

	// 删除 shadow 后 built_in 恢复。
	deleteTool := NewSkillDeleteLocalTool(store, registry)
	_, err = deleteTool.Execute(map[string]any{"id": "builtin-code-helper"})
	if err != nil {
		t.Fatalf("delete shadow: %v", err)
	}
	restored, _ := registry.Get("builtin-code-helper")
	if restored.Source != SkillSourceBuiltIn {
		t.Fatalf("expected restored built_in source, got %s", restored.Source)
	}
	if restored.DisplayName != original.DisplayName {
		t.Fatalf("expected restored original display name, got %s", restored.DisplayName)
	}
}

// TestSkillUpdateShadowOfBuiltInReturnsForkedFrom 验证对已有的 local_db shadow 做二次编辑时，
// forked_from 仍返回 true，表示底层是 built_in。
func TestSkillUpdateShadowOfBuiltInReturnsForkedFrom(t *testing.T) {
	store, registry := newSkillToolRegistry(t)

	updateTool := NewSkillUpdateLocalTool(store, registry)

	// 第一步：把 built_in 更新为 shadow（首次 fork）。
	res, err := updateTool.Execute(map[string]any{
		"id": "builtin-code-helper",
		"updates": map[string]any{
			"display_name": "Shadowed Helper",
			"content":      "You are a coding assistant focused on {{language}}.",
		},
	})
	if err != nil {
		t.Fatalf("update builtin: %v", err)
	}
	if res.(map[string]any)["forked_from"] != true {
		t.Fatalf("first update should forked_from=true, got %v", res.(map[string]any)["forked_from"])
	}

	// 第二步：对已有的 shadow 做二次编辑，应仍返回 forked_from=true。
	res2, err := updateTool.Execute(map[string]any{
		"id": "builtin-code-helper",
		"updates": map[string]any{
			"display_name": "Shadowed Helper v2",
		},
	})
	if err != nil {
		t.Fatalf("update shadow: %v", err)
	}
	if res2.(map[string]any)["forked_from"] != true {
		t.Fatalf("expected forked_from=true on shadow re-edit, got %v", res2.(map[string]any)["forked_from"])
	}

	s, _ := registry.Get("builtin-code-helper")
	if s.Source != SkillSourceLocalDB {
		t.Fatalf("expected shadow source local_db, got %s", s.Source)
	}
	if s.DisplayName != "Shadowed Helper v2" {
		t.Fatalf("expected updated display name, got %s", s.DisplayName)
	}
}

// TestSkillDeleteBuiltInForbidden 验证直接删除 built_in 与 local_file 被禁止。
func TestSkillDeleteBuiltInForbidden(t *testing.T) {
	store, registry := newSkillToolRegistry(t)

	deleteTool := NewSkillDeleteLocalTool(store, registry)
	_, err := deleteTool.Execute(map[string]any{"id": "builtin-code-helper"})
	if err == nil {
		t.Fatal("expected error deleting built-in")
	}
}

// TestSkillUpdateLocalFileForbidden 验证直接更新 local_file 被禁止。
func TestSkillUpdateLocalFileForbidden(t *testing.T) {
	store, registry := newSkillToolRegistry(t)

	// 手动注入一个 local_file skill。
	registry.Register(Skill{
		ID:              "local-file/test",
		DisplayName:     "Local File Skill",
		Source:          SkillSourceLocalFile,
		IsLocalEditable: false,
		State:           SkillStateEnabled,
	})

	updateTool := NewSkillUpdateLocalTool(store, registry)
	_, err := updateTool.Execute(map[string]any{
		"id":      "local-file/test",
		"updates": map[string]any{"display_name": "X"},
	})
	if err == nil {
		t.Fatal("expected error updating local_file skill")
	}

	deleteTool := NewSkillDeleteLocalTool(store, registry)
	_, err = deleteTool.Execute(map[string]any{"id": "local-file/test"})
	if err == nil {
		t.Fatal("expected error deleting local_file skill")
	}
}

// TestSkillSearchFilters 验证 search 的 source/scope 过滤生效。
func TestSkillSearchFilters(t *testing.T) {
	store, registry := newSkillToolRegistry(t)

	create := NewSkillCreateLocalTool(store, registry)
	_, err := create.Execute(map[string]any{
		"id":           "searchable/local",
		"display_name": "Searchable",
		"content":      "x",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	search := NewSkillSearchTool(registry)
	res, err := search.Execute(map[string]any{"source": "built_in"})
	if err != nil {
		t.Fatalf("search source: %v", err)
	}
	for _, item := range res.([]map[string]any) {
		if item["source"] != "built_in" {
			t.Fatalf("source filter leaked: %v", item)
		}
	}

	res, err = search.Execute(map[string]any{"scope": "global"})
	if err != nil {
		t.Fatalf("search scope: %v", err)
	}
	if len(res.([]map[string]any)) < 2 {
		t.Fatalf("expected at least 2 global scope skills, got %d", len(res.([]map[string]any)))
	}
}

// TestSkillUpdateLocalCrossProjectRejected 验证 project scope 越权防护生效：
// Engine 在 executeToolCall 中从 SkillVariables 注入 _project_id，
// skillUpdateLocalTool 读取它对 scope=project 的 skill 做 callerProjectID 校验。
func TestSkillUpdateLocalCrossProjectRejected(t *testing.T) {
	store, registry := newSkillToolRegistry(t)

	create := NewSkillCreateLocalTool(store, registry)
	createRes, err := create.Execute(map[string]any{
		"id":           "proj-scoped/skill",
		"display_name": "Proj Scoped",
		"description":  "project scope skill",
		"content":      "topic={{topic}}",
		"scope":        "project",
		"project_id":   "proj-X",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if createRes.(map[string]any)["forked_from"] != false {
		t.Fatal("create local skill should not fork")
	}

	updateTool := NewSkillUpdateLocalTool(store, registry)

	// 跨 project 调用应被拒。
	_, err = updateTool.Execute(map[string]any{
		"id":         "proj-scoped/skill",
		"_project_id": "proj-Y",
		"updates":    map[string]any{"display_name": "Hacked"},
	})
	if err == nil {
		t.Fatal("expected cross-project update rejected")
	}

	// 同 project 应成功。
	res, err := updateTool.Execute(map[string]any{
		"id":         "proj-scoped/skill",
		"_project_id": "proj-X",
		"updates":    map[string]any{"display_name": "Updated Name"},
	})
	if err != nil {
		t.Fatalf("same-project update should succeed: %v", err)
	}
	if res.(map[string]any)["updated"] != true {
		t.Fatal("expected updated=true")
	}

	// 未传 _project_id 回退放行。
	_, err = updateTool.Execute(map[string]any{
		"id":      "proj-scoped/skill",
		"updates": map[string]any{"display_name": "Plain Update"},
	})
	if err != nil {
		t.Fatalf("update without _project_id should be allowed: %v", err)
	}
}

// TestSkillListAndSearchExcludeSensitiveFields 验证 L14：
// skill/list 与 skill/search 的返回项只含摘要字段，不泄露 templates / parameters / authors 等完整内容。
func TestSkillListAndSearchExcludeSensitiveFields(t *testing.T) {
	store, registry := newSkillToolRegistry(t)

	// 构造一个带大量敏感内容的 skill（templates / parameters / authors）。
	create := NewSkillCreateLocalTool(store, registry)
	_, err := create.Execute(map[string]any{
		"id":           "heavy/skill",
		"display_name": "Heavy",
		"description":  "desc",
		"content":      "system prompt with {{secret}}.",
		"parameters": []any{
			map[string]any{"name": "secret", "type": "string", "required": true},
		},
		"authors": []any{"alice", "bob"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// list
	listRes, err := NewSkillListTool(registry).Execute(map[string]any{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, item := range listRes.([]map[string]any) {
		if item["id"] != "heavy/skill" {
			continue
		}
		for _, key := range []string{"templates", "parameters", "authors", "created_at", "updated_at", "invalid_reason"} {
			if _, ok := item[key]; ok {
				t.Errorf("list leaked field %q for skill %q", key, item["id"])
			}
		}
		// 摘要应保留的字段。
		for _, key := range []string{"id", "display_name", "description", "source", "scope", "project_id", "tags", "state"} {
			if _, ok := item[key]; !ok {
				t.Errorf("list missing expected field %q", key)
			}
		}
	}

	// search
	searchRes, err := NewSkillSearchTool(registry).Execute(map[string]any{"q": "Heavy"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, item := range searchRes.([]map[string]any) {
		if item["id"] != "heavy/skill" {
			continue
		}
		for _, key := range []string{"templates", "parameters", "authors", "created_at", "updated_at", "invalid_reason"} {
			if _, ok := item[key]; ok {
				t.Errorf("search leaked field %q for skill %q", key, item["id"])
			}
		}
	}
}

// TestSkillToggleCrossProjectRejected 验证 enable/disable 同样受 project scope 保护（L11）。
func TestSkillToggleCrossProjectRejected(t *testing.T) {
	store, registry := newSkillToolRegistry(t)

	registry.Register(Skill{
		ID:          "proj-scoped/toggle",
		DisplayName: "Toggle Test",
		Source:      SkillSourceLocalDB,
		State:       SkillStateEnabled,
		Scope:       SkillScopeProject,
		ProjectID:   "proj-X",
	})

	// 同 project disable 成功。
	_, err := toggleSkill(map[string]any{
		"id":         "proj-scoped/toggle",
		"_project_id": "proj-X",
	}, "disable", store, registry, nil)
	if err != nil {
		t.Fatalf("same-project disable succeeded: %v", err)
	}
	s, _ := registry.Get("proj-scoped/toggle")
	if s.State != SkillStateDisabled {
		t.Fatalf("expected disabled, got %s", s.State)
	}

	// 不同 project toggle 应被拒绝。
	_, err = toggleSkill(map[string]any{
		"id":         "proj-scoped/toggle",
		"_project_id": "proj-Y",
	}, "enable", store, registry, nil)
	if err == nil {
		t.Fatal("expected cross-project toggle rejected")
	}
}

// fakeSkillEventBus 测试用广播器，记录所有收到的调用。
type fakeSkillEventBus struct {
	calls []skillEventCall
}

type skillEventCall struct {
	eventType string
	skillID   string
	data      map[string]any
}

func (f *fakeSkillEventBus) BroadcastSkillEvent(eventType, skillID string, data map[string]any) {
	f.calls = append(f.calls, skillEventCall{eventType: eventType, skillID: skillID, data: data})
}

// TestSkillToolBroadcastsEvents 验证 skill Agent Tools 在成功变更后会通过 bus 广播事件。
func TestSkillToolBroadcastsEvents(t *testing.T) {
	store, registry := newSkillToolRegistry(t)

	bus := &fakeSkillEventBus{}

	// create_local 应广播 EventSkillCreated。
	create := NewSkillCreateLocalTool(store, registry).(*SkillCreateLocalTool)
	create.WithBus(bus)
	res, err := create.Execute(map[string]any{
		"id":           "broadcast-created",
		"display_name": "Broadcast Create",
		"content":      "Hello {{name}}.",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.(map[string]any)["id"] != "broadcast-created" {
		t.Fatal("create failed")
	}
	if len(bus.calls) != 1 || bus.calls[0].eventType != EventSkillCreated || bus.calls[0].skillID != "broadcast-created" {
		t.Fatalf("expected EventSkillCreated for broadcast-created, got %+v", bus.calls)
	}

	// enable built_in 应广播 EventSkillEnabled。
	bus.calls = nil
	enable := NewSkillEnableTool(store, registry).(*SkillEnableTool)
	enable.WithBus(bus)
	_, err = enable.Execute(map[string]any{"id": "builtin-code-helper"})
	if err != nil {
		t.Fatalf("enable builtin: %v", err)
	}
	if len(bus.calls) != 1 || bus.calls[0].eventType != EventSkillEnabled || bus.calls[0].skillID != "builtin-code-helper" {
		t.Fatalf("expected EventSkillEnabled for builtin-code-helper, got %+v", bus.calls)
	}
	if bus.calls[0].data["state"] != string(SkillStateEnabled) {
		t.Fatalf("expected enabled state in data, got %v", bus.calls[0].data)
	}
}
