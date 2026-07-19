package skill

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newMemoryStore(t *testing.T) *Store {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	schema := `CREATE TABLE IF NOT EXISTS skills (
		id TEXT PRIMARY KEY,
		version TEXT NOT NULL DEFAULT '1.0.0',
		display_name TEXT NOT NULL,
		description TEXT DEFAULT '',
		authors_json TEXT DEFAULT '[]',
		tags_json TEXT DEFAULT '[]',
		source TEXT NOT NULL DEFAULT 'local_db',
		source_url TEXT DEFAULT '',
		is_local_editable BOOLEAN DEFAULT 1,
		templates_json TEXT DEFAULT '[]',
		parameters_json TEXT DEFAULT '[]',
		required_tools_json TEXT DEFAULT '[]',
		suggested_tools_json TEXT DEFAULT '[]',
		permissions_json TEXT DEFAULT '[]',
		triggers_json TEXT DEFAULT '{}',
		state TEXT NOT NULL DEFAULT 'discovered',
		invalid_reason TEXT DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`
	if _, err := sqlDB.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return NewStore(sqlDB)
}

func TestLoaderLoadAll(t *testing.T) {
	store := newMemoryStore(t)
	reg := NewRegistry()

	dbSkill := &Skill{
		ID:          "db/skill",
		DisplayName: "DB Skill",
		Source:      SkillSourceLocalDB,
		State:       SkillStateEnabled,
	}
	if err := store.Save(dbSkill); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loader := NewLoader(store, reg)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	if !reg.Exists("builtin-code-helper") {
		t.Fatalf("expected builtin-code-helper to be registered")
	}
	if !reg.Exists("builtin-error-diagnosis") {
		t.Fatalf("expected builtin-error-diagnosis to be registered")
	}
	if !reg.Exists("db/skill") {
		t.Fatalf("expected db/skill to be registered")
	}

	src := SkillSourceBuiltIn
	builtins := reg.List(&src)
	if len(builtins) != 2 {
		t.Fatalf("expected 2 builtins, got %d", len(builtins))
	}

	// 校验内置 Skill 的 tags 是否被正确持久化
	s, _ := reg.Get("builtin-code-helper")
	if len(s.Tags) == 0 || s.Tags[0] != "code" {
		t.Fatalf("expected builtin-code-helper to have code tag, got %v", s.Tags)
	}
}
