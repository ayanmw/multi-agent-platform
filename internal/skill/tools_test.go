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
		"created_at INTEGER NOT NULL," +
		"updated_at INTEGER NOT NULL)"
	if _, err := sqlDB.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return sqlDB
}

func TestCreateAndDeleteSkillTool(t *testing.T) {
	sqlDB := initToolsTestDB(t)
	defer sqlDB.Close()

	store := NewStore(sqlDB)
	registry := NewRegistry()

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
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill listed, got %d", len(skills))
	}
	if skills[0]["id"] != "test/skill-tool" {
		t.Fatalf("unexpected listed skill id %v", skills[0]["id"])
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

	listRes, err = toolRegistry.Execute("skill/list", nil)
	if err != nil {
		t.Fatalf("list after delete execute: %v", err)
	}
	skills = listRes.([]map[string]any)
	if len(skills) != 0 {
		t.Fatalf("expected 0 skills after delete, got %d", len(skills))
	}
}
