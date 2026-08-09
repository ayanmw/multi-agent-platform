// backend_sqlite.go — SQLite 后端实现（N3-04c / E7）
//
// 这是平台的**默认零配置后端**。本文件的全部逻辑都是从 database.go 的 Init()
// 原样搬迁而来，行为逐字节保持不变：同一个驱动名、同一组 PRAGMA、同样的
// SetMaxOpenConns(1)、同样的 createTables + RunMigrations 顺序。
//
// 搬迁的意义不在于改变 SQLite 的行为，而在于让「SQLite 特有的取舍」有一个
// 明确的归属地：单连接串行化、WAL、busy_timeout、不开外键，这些都是 SQLite
// 的实现细节，不该继续泄漏到通用的 Init() 流程里。
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动，注册驱动名 "sqlite"
)

// sqliteDialectInstance 是进程内共享的 SQLite 方言单例（无状态，可安全共享）。
var sqliteDialectInstance Dialect = sqliteDialect{}

// sqliteDialect 实现 Dialect。SQLite 的类型系统是动态的（type affinity），
// 这里给出的是与既有 createTables/migrations 中完全一致的写法。
type sqliteDialect struct{}

func (sqliteDialect) Name() string { return "sqlite" }

// Placeholder：SQLite 用位置无关的 `?`，忽略序号。
func (sqliteDialect) Placeholder(int) string { return "?" }

// QuoteIdentifier：SQLite 用双引号，内部双引号翻倍转义。
func (sqliteDialect) QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (sqliteDialect) ColumnType(t LogicalType) string {
	switch t {
	case TypeText:
		return "TEXT"
	case TypeInteger:
		return "INTEGER"
	case TypeReal:
		return "REAL"
	case TypeBool:
		return "BOOLEAN" // SQLite 无原生布尔，BOOLEAN 是 NUMERIC affinity 的别名
	case TypeJSON:
		return "JSON" // 同样是别名（TEXT affinity），保留语义可读性
	case TypeBlob:
		return "BLOB"
	case TypeTimestamp:
		return "DATETIME"
	default:
		return "TEXT"
	}
}

func (sqliteDialect) NowExpr() string { return "CURRENT_TIMESTAMP" }

// SupportsConcurrentWriters：SQLite 是单文件单写。
// 即便开了 WAL，多写者也只能串行；跨进程/跨节点共享同一份 .db 文件更是
// 明确不受支持。这是横向扩展的硬约束，必须如实声明。
func (sqliteDialect) SupportsConcurrentWriters() bool { return false }

// sqliteBackend 实现 Backend，封装 modernc.org/sqlite 的接入细节。
type sqliteBackend struct{}

func (sqliteBackend) Name() string { return "sqlite" }

func (sqliteBackend) Dialect() Dialect { return sqliteDialectInstance }

// NormalizeDSN 确保数据文件的父目录存在。
//
// 特例：`:memory:` 与 `file::memory:` 形式是内存库，没有父目录（大量单测在用）。
func (sqliteBackend) NormalizeDSN(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("db: sqlite dsn is empty")
	}
	if isSQLiteMemoryDSN(raw) {
		return raw, nil
	}
	dir := filepath.Dir(raw)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create data directory: %w", err)
	}
	return raw, nil
}

// Open 打开 SQLite 连接。驱动名 "sqlite" 由 modernc.org/sqlite 的 init 注册。
func (sqliteBackend) Open(dsn string) (*sql.DB, error) {
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	return sqlDB, nil
}

// Configure 施加 SQLite 特有的并发取舍。此处逐条保留原 Init() 的注释与顺序。
func (sqliteBackend) Configure(sqlDB *sql.DB) error {
	// modernc.org/sqlite 推荐使用单一连接模型，避免并发写导致的 BUSY/LOCKED 错误。
	// 设置 MaxOpenConns(1) 让所有数据库操作串行化，配合 WAL 和 busy_timeout
	// 进一步提升并发容忍度。
	sqlDB.SetMaxOpenConns(1)

	// 配置 SQLite 并发写行为：5 秒 busy_timeout + WAL 日志。
	// 注意：foreign_keys 不在此处全局开启，因为历史代码（包括 tests 和 orchestrator）
	// 在插入 task 前并不总是保证 session 已存在，开启 FK 会导致这些路径失败。
	// 外键一致性由应用层保证；如需强制 FK，应在已知 session 存在的特定事务内开启。
	pragmas := []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
	}
	for _, pragma := range pragmas {
		if _, err := sqlDB.Exec(pragma); err != nil {
			return fmt.Errorf("failed to execute %s: %w", pragma, err)
		}
	}
	return nil
}

// Bootstrap 建表并跑迁移。
//
// createTables 与 RunMigrations 仍作用于包级全局 `DB`（既有 CRUD 的共享句柄），
// 调用方 InitWithBackend 保证在此之前已完成赋值；参数 sqlDB 用于断言两者一致，
// 防止未来有人绕过 Init 直接调用导致操作到错误的连接上。
func (sqliteBackend) Bootstrap(sqlDB *sql.DB) error {
	if sqlDB != DB {
		return fmt.Errorf("db: sqlite bootstrap requires the global handle to be assigned first")
	}
	if err := createTables(); err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}
	// 为已存在的数据库运行自动 schema migration
	if err := RunMigrations(); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}
	return nil
}

// isSQLiteMemoryDSN 判断 DSN 是否指向内存库（无需创建目录）。
func isSQLiteMemoryDSN(dsn string) bool {
	return dsn == ":memory:" || strings.HasPrefix(dsn, "file::memory:") || strings.Contains(dsn, "mode=memory")
}

func init() { RegisterBackend(sqliteBackend{}) }
