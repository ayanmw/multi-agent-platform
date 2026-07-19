package mcp

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestDB 创建一个带 mcp_servers 表的临时 SQLite 数据库。
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	schema := `
		CREATE TABLE IF NOT EXISTS mcp_servers (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT 'db',
			name TEXT NOT NULL,
			transport TEXT NOT NULL DEFAULT 'stdio',
			command TEXT,
			args JSON DEFAULT '[]',
			endpoint TEXT,
			environment JSON DEFAULT '{}',
			enabled BOOLEAN DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create mcp_servers table: %v", err)
	}
	return db
}

// TestSqliteRepositoryRoundTrip 验证 save、reload 与字段保持。
func TestSqliteRepositoryRoundTrip(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	repo := NewSqliteRepository(db)
	ctx := context.Background()

	ms := ManagedServer{
		ID:      "time",
		Source:  SourceDB,
		Enabled: true,
		Config: ServerConfig{
			Name:        "time",
			Transport:   "stdio",
			Command:     "node",
			Args:        []string{"time-server.js"},
			Environment: map[string]string{"TZ": "UTC"},
			Enabled:     true,
		},
	}

	if err := repo.Save(ctx, ms); err != nil {
		t.Fatalf("Save: %v", err)
	}

	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 server, got %d", len(all))
	}
	got := all[0]
	if got.ID != ms.ID {
		t.Fatalf("id mismatch: got %q", got.ID)
	}
	if !got.Enabled {
		t.Fatalf("expected enabled")
	}
	if got.Source != SourceDB {
		t.Fatalf("source mismatch: got %q", got.Source)
	}
	if got.Config.Command != "node" {
		t.Fatalf("command mismatch: got %q", got.Config.Command)
	}
	if len(got.Config.Args) != 1 || got.Config.Args[0] != "time-server.js" {
		t.Fatalf("args mismatch: got %v", got.Config.Args)
	}
	if got.Config.Environment["TZ"] != "UTC" {
		t.Fatalf("environment mismatch: got %v", got.Config.Environment)
	}
}

// TestSqliteRepositoryListEnabledOnly 验证 ListEnabled 会过滤掉 disabled 行。
func TestSqliteRepositoryListEnabledOnly(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	repo := NewSqliteRepository(db)
	ctx := context.Background()

	if err := repo.Save(ctx, ManagedServer{ID: "enabled", Source: SourceDB, Enabled: true, Config: ServerConfig{Name: "enabled"}}); err != nil {
		t.Fatalf("Save enabled: %v", err)
	}
	if err := repo.Save(ctx, ManagedServer{ID: "disabled", Source: SourceDB, Enabled: false, Config: ServerConfig{Name: "disabled"}}); err != nil {
		t.Fatalf("Save disabled: %v", err)
	}

	enabled, err := repo.ListEnabled(ctx)
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(enabled) != 1 {
		t.Fatalf("expected 1 enabled server, got %d", len(enabled))
	}
	if enabled[0].ID != "enabled" {
		t.Fatalf("expected enabled id, got %s", enabled[0].ID)
	}
}

// TestSqliteRepositoryUpdate 验证保存相同 ID 会更新字段。
func TestSqliteRepositoryUpdate(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	repo := NewSqliteRepository(db)
	ctx := context.Background()

	if err := repo.Save(ctx, ManagedServer{ID: "same", Source: SourceDB, Enabled: true, Config: ServerConfig{Name: "same"}}); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	if err := repo.Save(ctx, ManagedServer{ID: "same", Source: SourceMarket, Enabled: false, Config: ServerConfig{Name: "same", Command: "python"}}); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 server after update, got %d", len(all))
	}
	if all[0].Enabled {
		t.Fatalf("expected server to be disabled after update")
	}
	if all[0].Config.Command != "python" {
		t.Fatalf("expected updated command, got %q", all[0].Config.Command)
	}
	if all[0].Source != SourceMarket {
		t.Fatalf("expected source market after update, got %q", all[0].Source)
	}
}

// TestSqliteRepositoryDelete 验证 Delete 会删除行。
func TestSqliteRepositoryDelete(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	repo := NewSqliteRepository(db)
	ctx := context.Background()

	if err := repo.Save(ctx, ManagedServer{ID: "gone", Source: SourceDB, Enabled: true, Config: ServerConfig{Name: "gone"}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := repo.Delete(ctx, "gone"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("expected 0 servers after delete, got %d", len(all))
	}
}

// TestSqliteRepositoryUninitialized 验证 db 为 nil 时能优雅返回错误。
func TestSqliteRepositoryUninitialized(t *testing.T) {
	repo := NewSqliteRepository(nil)
	ctx := context.Background()

	if err := repo.Save(ctx, ManagedServer{ID: "x", Config: ServerConfig{Name: "x"}}); err == nil {
		t.Fatalf("expected error on nil db")
	}
	if _, err := repo.ListEnabled(ctx); err == nil {
		t.Fatalf("expected error on nil db")
	}
}

func TestMain(m *testing.M) {
	// 部分 repository 测试使用 modernc.org/sqlite，该库在某些 pragma 上会
	// 向 stderr 输出日志。我们并不关心这些消息。
	os.Exit(m.Run())
}
