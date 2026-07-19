package skill

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func initTestDB(t *testing.T) *sql.DB {
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
	return sqlDB
}

func TestStoreSaveAndLoad(t *testing.T) {
	sqlDB := initTestDB(t)
	defer sqlDB.Close()

	store := NewStore(sqlDB)
	s := &Skill{
		ID:          "test/store",
		DisplayName: "Store Test",
		Description: "persisted skill",
		Source:      SkillSourceLocalDB,
		State:       SkillStateEnabled,
		Templates: []SkillTemplate{
			{Name: "system", Content: "Hello {{name}}", Variables: []string{"name"}, IsRequired: true},
		},
	}

	if err := store.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Get("test/store")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.ID != "test/store" {
		t.Errorf("ID = %q, want test/store", loaded.ID)
	}
	if loaded.DisplayName != "Store Test" {
		t.Errorf("DisplayName = %q, want Store Test", loaded.DisplayName)
	}
	if len(loaded.Templates) != 1 {
		t.Fatalf("Templates len = %d, want 1", len(loaded.Templates))
	}
	if loaded.Templates[0].Name != "system" {
		t.Errorf("Template name = %q, want system", loaded.Templates[0].Name)
	}

	all, err := store.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ListAll len = %d, want 1", len(all))
	}

	if err := store.Delete("test/store"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = store.Get("test/store")
	if err == nil {
		t.Fatalf("after Delete Get should error")
	}
}
