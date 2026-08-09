package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegisteredBackends 断言内建后端在 init 阶段完成注册，
// 且默认后端名可被解析——这是 InitWithBackend 的前置契约。
func TestRegisteredBackends(t *testing.T) {
	names := BackendNames()
	want := map[string]bool{"sqlite": false, "postgres": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Fatalf("backend %q not registered; got %v", n, names)
		}
	}

	// BackendNames 必须有序（错误信息/日志可复现）。
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("BackendNames not sorted: %v", names)
		}
	}
}

func TestLookupBackend(t *testing.T) {
	// 空名回退默认后端。
	b, err := LookupBackend("")
	if err != nil {
		t.Fatalf("LookupBackend(\"\"): %v", err)
	}
	if b.Name() != DefaultBackendName {
		t.Fatalf("empty name should fall back to %q, got %q", DefaultBackendName, b.Name())
	}

	// 大小写不敏感 + 去空白。
	if b, err = LookupBackend("  SQLite "); err != nil || b.Name() != "sqlite" {
		t.Fatalf("case-insensitive lookup failed: %v / %v", b, err)
	}

	// 未知后端必须报错，且错误信息列出可用后端（可操作性）。
	_, err = LookupBackend("mysql")
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
	if !strings.Contains(err.Error(), "sqlite") {
		t.Fatalf("error should list available backends, got: %v", err)
	}
}

// TestRegisterBackendDuplicatePanics 断言重复注册 panic（编程错误必须显式暴露，
// 静默覆盖会让「到底连的哪个库」不可推理）。
func TestRegisterBackendDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate backend registration")
		}
	}()
	RegisterBackend(sqliteBackend{}) // 已在 init 注册过
}

// TestSQLiteDialect 锁定 SQLite 方言映射，防止后续改动无意间改变既有 DDL 语义。
func TestSQLiteDialect(t *testing.T) {
	d := sqliteDialectInstance
	if d.Name() != "sqlite" {
		t.Fatalf("name: %q", d.Name())
	}
	if got := d.Placeholder(1); got != "?" {
		t.Fatalf("placeholder(1) = %q, want ?", got)
	}
	if got := d.Placeholder(7); got != "?" {
		t.Fatalf("placeholder(7) = %q, want ? (position-independent)", got)
	}
	if got := d.QuoteIdentifier(`we"ird`); got != `"we""ird"` {
		t.Fatalf("QuoteIdentifier = %s", got)
	}
	if d.NowExpr() != "CURRENT_TIMESTAMP" {
		t.Fatalf("NowExpr = %q", d.NowExpr())
	}
	if d.SupportsConcurrentWriters() {
		t.Fatal("sqlite must declare itself single-writer (E7 horizontal scaling constraint)")
	}
	cases := map[LogicalType]string{
		TypeText: "TEXT", TypeInteger: "INTEGER", TypeReal: "REAL",
		TypeBool: "BOOLEAN", TypeJSON: "JSON", TypeBlob: "BLOB", TypeTimestamp: "DATETIME",
	}
	for lt, want := range cases {
		if got := d.ColumnType(lt); got != want {
			t.Errorf("ColumnType(%d) = %q, want %q", lt, got, want)
		}
	}
}

// TestPostgresDialect 锁定 Postgres 方言映射（横向扩展原型的核心可验证部分）。
func TestPostgresDialect(t *testing.T) {
	d := postgresDialectInstance
	if d.Name() != "postgres" {
		t.Fatalf("name: %q", d.Name())
	}
	if got := d.Placeholder(1); got != "$1" {
		t.Fatalf("placeholder(1) = %q", got)
	}
	if got := d.Placeholder(12); got != "$12" {
		t.Fatalf("placeholder(12) = %q", got)
	}
	if !d.SupportsConcurrentWriters() {
		t.Fatal("postgres must declare concurrent-writer support")
	}
	if d.NowExpr() != "NOW()" {
		t.Fatalf("NowExpr = %q", d.NowExpr())
	}
	cases := map[LogicalType]string{
		TypeText: "TEXT", TypeInteger: "BIGINT", TypeReal: "DOUBLE PRECISION",
		TypeBool: "BOOLEAN", TypeJSON: "JSONB", TypeBlob: "BYTEA", TypeTimestamp: "TIMESTAMPTZ",
	}
	for lt, want := range cases {
		if got := d.ColumnType(lt); got != want {
			t.Errorf("ColumnType(%d) = %q, want %q", lt, got, want)
		}
	}
}

// TestRebind 验证「既有以 ? 书写的 CRUD 可直接跑在 Postgres 上」这一适配器承诺，
// 并覆盖必须跳过的上下文（字符串字面量 / 引用标识符 / 注释）。
func TestRebind(t *testing.T) {
	pg := postgresDialectInstance

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "simple insert",
			query: "INSERT INTO agents (id, name) VALUES (?, ?)",
			want:  "INSERT INTO agents (id, name) VALUES ($1, $2)",
		},
		{
			name:  "where clause numbering continues",
			query: "UPDATE tasks SET status = ?, total_tokens = ? WHERE id = ? AND session_id = ?",
			want:  "UPDATE tasks SET status = $1, total_tokens = $2 WHERE id = $3 AND session_id = $4",
		},
		{
			name:  "question mark inside string literal is preserved",
			query: "SELECT * FROM t WHERE note = 'what? really' AND id = ?",
			want:  "SELECT * FROM t WHERE note = 'what? really' AND id = $1",
		},
		{
			name:  "escaped quote inside literal",
			query: "SELECT * FROM t WHERE s = 'it''s ok? yes' AND id = ?",
			want:  "SELECT * FROM t WHERE s = 'it''s ok? yes' AND id = $1",
		},
		{
			name:  "quoted identifier is preserved",
			query: `SELECT "we?ird" FROM t WHERE id = ?`,
			want:  `SELECT "we?ird" FROM t WHERE id = $1`,
		},
		{
			name:  "line comment is preserved",
			query: "SELECT 1 -- is it ? yes\nWHERE id = ?",
			want:  "SELECT 1 -- is it ? yes\nWHERE id = $1",
		},
		{
			name:  "block comment is preserved",
			query: "SELECT 1 /* huh? */ WHERE id = ?",
			want:  "SELECT 1 /* huh? */ WHERE id = $1",
		},
		{
			name:  "no placeholders",
			query: "SELECT COUNT(*) FROM agents",
			want:  "SELECT COUNT(*) FROM agents",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Rebind(pg, tc.query); got != tc.want {
				t.Errorf("Rebind:\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}

	// 同构方言（SQLite）走快速路径，必须原样返回。
	q := "SELECT * FROM t WHERE id = ? AND x = ?"
	if got := Rebind(sqliteDialectInstance, q); got != q {
		t.Fatalf("sqlite Rebind should be identity, got %s", got)
	}
	if got := Rebind(nil, q); got != q {
		t.Fatalf("nil dialect Rebind should be identity, got %s", got)
	}
}

// TestPostgresNormalizeDSN 覆盖 URL 与 keyword/value 两种形态及非法输入。
func TestPostgresNormalizeDSN(t *testing.T) {
	b := postgresBackend{}
	ok := []string{
		"postgres://u:p@localhost:5432/app?sslmode=disable",
		"postgresql://u@host/db",
		"host=localhost user=app dbname=app sslmode=disable",
	}
	for _, dsn := range ok {
		if _, err := b.NormalizeDSN(dsn); err != nil {
			t.Errorf("NormalizeDSN(%q) unexpected error: %v", dsn, err)
		}
	}
	bad := []string{"", "   ", "data/app.db"}
	for _, dsn := range bad {
		if _, err := b.NormalizeDSN(dsn); err == nil {
			t.Errorf("NormalizeDSN(%q) should fail", dsn)
		}
	}
}

// TestPostgresBootstrapUnsupported 断言 Postgres 后端诚实声明能力边界：
// 不假装能跑 SQLite 方言的迁移，且错误可被 errors.Is 判定。
func TestPostgresBootstrapUnsupported(t *testing.T) {
	err := postgresBackend{}.Bootstrap(nil)
	if err == nil {
		t.Fatal("expected bootstrap to be unsupported")
	}
	if !errors.Is(err, ErrSchemaBootstrapUnsupported) {
		t.Fatalf("error should wrap ErrSchemaBootstrapUnsupported, got %v", err)
	}
	if !strings.Contains(err.Error(), "DB_BACKEND_ABSTRACTION") {
		t.Fatalf("error should point to the migration guide, got %v", err)
	}
}

// TestPostgresOpenWithoutDriverGivesActionableError 断言驱动缺失时给出可操作指引，
// 而不是 database/sql 那句语焉不详的 unknown driver。
// 若未来宿主真的注册了 pgx 驱动，本用例自动降级为「不报错即通过」。
func TestPostgresOpenWithoutDriverGivesActionableError(t *testing.T) {
	registered := false
	for _, d := range sql.Drivers() {
		if d == postgresDriverName {
			registered = true
			break
		}
	}
	handle, err := postgresBackend{}.Open("postgres://u@localhost/db")
	if registered {
		if err != nil {
			t.Fatalf("driver registered but Open failed: %v", err)
		}
		_ = handle.Close()
		return
	}
	if err == nil {
		_ = handle.Close()
		t.Fatal("expected error when pgx driver is not registered")
	}
	if !strings.Contains(err.Error(), "pgx/v5/stdlib") {
		t.Fatalf("error should tell the operator how to register the driver, got: %v", err)
	}
}

// TestInitWithBackendSQLiteBehaviorUnchanged 是 N3-04c 最重要的回归护栏：
// 经过抽象层之后，SQLite 的初始化结果（连接数限制、WAL、表是否建好、迁移是否跑过）
// 必须与重构前完全一致。
func TestInitWithBackendSQLiteBehaviorUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backend_probe.db")
	if err := InitWithBackend("sqlite", path); err != nil {
		t.Fatalf("InitWithBackend: %v", err)
	}
	t.Cleanup(func() { _ = Close() })

	if DB == nil {
		t.Fatal("global DB handle not assigned")
	}
	if got := DB.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConns = %d, want 1 (modernc sqlite single-connection model)", got)
	}

	var journal string
	if err := DB.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if !strings.EqualFold(journal, "wal") {
		t.Fatalf("journal_mode = %q, want wal", journal)
	}

	// Bootstrap 生效：核心表存在 + 迁移记录已写入。
	for _, table := range []string{"agents", "tasks", "sessions", "memories", "schema_migrations"} {
		var name string
		err := DB.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %q missing after bootstrap: %v", table, err)
		}
	}
	var applied int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if applied == 0 {
		t.Fatal("no migrations recorded; bootstrap did not run RunMigrations")
	}

	// 活跃后端可被查询（供启动告警与运维自检使用）。
	if b := ActiveBackend(); b == nil || b.Name() != "sqlite" {
		t.Fatalf("ActiveBackend = %v", b)
	}
	if ActiveDialect().SupportsConcurrentWriters() {
		t.Fatal("active dialect should report single-writer for sqlite")
	}
}

// TestInitWithBackendUnknownName 断言未知后端在启动期硬失败（fail-fast）。
func TestInitWithBackendUnknownName(t *testing.T) {
	if err := InitWithBackend("cockroach", "whatever"); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

// TestInitOptionsSkipBootstrap 断言 SkipBootstrap 真的跳过建表——
// 这是「schema 由外部迁移工具管理」部署形态的行为契约。
func TestInitOptionsSkipBootstrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no_bootstrap.db")
	if err := InitWithBackendOptions("sqlite", path, InitOptions{SkipBootstrap: true}); err != nil {
		t.Fatalf("InitWithBackendOptions: %v", err)
	}
	t.Cleanup(func() { _ = Close() })

	var name string
	err := DB.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='agents'`).Scan(&name)
	if err == nil {
		t.Fatal("agents table should NOT exist when bootstrap is skipped")
	}
}

// TestSQLiteMemoryDSN 断言内存 DSN 不会被当成文件路径去创建目录。
func TestSQLiteMemoryDSN(t *testing.T) {
	for _, dsn := range []string{":memory:", "file::memory:?cache=shared", "file:x?mode=memory"} {
		if !isSQLiteMemoryDSN(dsn) {
			t.Errorf("isSQLiteMemoryDSN(%q) = false", dsn)
		}
		got, err := sqliteBackend{}.NormalizeDSN(dsn)
		if err != nil || got != dsn {
			t.Errorf("NormalizeDSN(%q) = %q, %v", dsn, got, err)
		}
	}
	if isSQLiteMemoryDSN("data/app.db") {
		t.Error("file path misdetected as memory DSN")
	}
	_, err := sqliteBackend{}.NormalizeDSN("")
	if err == nil {
		t.Error("empty sqlite dsn should fail")
	}
}
