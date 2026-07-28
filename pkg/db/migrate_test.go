// migrate_test.go — schema migration 机制的单元测试。
//
// 重点覆盖：新库基线化后 schema_migrations 应一次性记录全部版本，而不是
// 逐条执行 v1..vN 的 DDL 再一条条插入记录；从而避免启动日志刷屏。
package db

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// TestNewDatabaseBaselineMigrations 验证全新数据库在 Init 后，schema_migrations
// 表中直接包含从 1 到 maxVersion 的所有 migration 记录；同时验证 Init 过程中没有
// 逐条打印 [Migration] vX 日志。
func TestNewDatabaseBaselineMigrations(t *testing.T) {
	var buf bytes.Buffer
	// 临时把标准 log 输出重定向到 buf，以捕获 RunMigrations 的刷屏日志。
	oldOutput := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(oldOutput)

	freshDB(t)

	uniqueMigrations := deduplicateMigrations(migrations)
	if len(uniqueMigrations) == 0 {
		t.Fatal("no migrations registered")
	}
	maxVersion := uniqueMigrations[len(uniqueMigrations)-1].Version

	for _, m := range uniqueMigrations {
		var exists bool
		err := DB.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=?)`, m.Version,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("query schema_migrations for v%d: %v", m.Version, err)
		}
		if !exists {
			t.Errorf("new database missing schema_migrations record for v%d (max=%d)", m.Version, maxVersion)
		}
	}

	var count int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != len(uniqueMigrations) {
		t.Errorf("schema_migrations count = %d, want %d", count, len(uniqueMigrations))
	}

	// 新库走基线化，最后只有一条汇总日志；允许这一条存在。
	migrationLogCount := strings.Count(buf.String(), "[Migration]")
	if migrationLogCount > 1 {
		t.Errorf("new database printed %d [Migration] log lines; want at most 1 baseline summary", migrationLogCount)
	}
}
