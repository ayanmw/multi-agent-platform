// backend_postgres.go — Postgres 后端原型（N3-04c / E7 横向扩展路径）
//
// # 这是什么，不是什么
//
// **是**：一个可运行、可测试的方言实现 + 连接装配原型，把「换成 Postgres 到底
// 要改哪些地方」从口头承诺变成代码里可验证的清单。
//
// **不是**：一个开箱即用的生产后端。它刻意保留两个显式缺口，并在运行时以
// 明确的错误告知使用者，而不是静默地半可用：
//
//	缺口 1（驱动）：`pkg/db` **不 import 任何 Postgres 驱动**。这是刻意的——
//	  为一个尚未启用的后端给所有部署增加编译期依赖（以及它的传递依赖树）不划算。
//	  宿主程序想启用时，只需在自己的 main 包里加一行 blank import：
//	      import _ "github.com/jackc/pgx/v5/stdlib"   // 注册驱动名 "pgx"
//	  未注册时 Open 会返回一条**可操作**的错误，而不是 database/sql 那句语焉不详的
//	  "unknown driver"。
//
//	缺口 2（schema 引导）：现有 738 行迁移是 SQLite 方言（`PRAGMA table_info`、
//	  不带 IF NOT EXISTS 的 ALTER TABLE、`AUTOINCREMENT` 语义差异），跨方言复用
//	  会静默产出错误 schema。因此 Bootstrap 返回 ErrSchemaBootstrapUnsupported，
//	  要求用外部迁移工具。移植路线见 docs/DB_BACKEND_ABSTRACTION.md。
//
// 方言部分（占位符 / 类型映射 / 引用规则 / 并发写能力声明）是**完整且已被单测
// 覆盖**的，配合 Rebind() 可让既有以 `?` 书写的 CRUD 语句在 Postgres 上执行。
package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// postgresDriverName 是宿主程序需要注册的驱动名。
// 选择 "pgx"（jackc/pgx/v5/stdlib 的注册名）而非 "postgres"（lib/pq），
// 因为 pgx 是当前社区事实标准且维护活跃。
const postgresDriverName = "pgx"

// postgresDialectInstance 是进程内共享的 Postgres 方言单例（无状态）。
var postgresDialectInstance Dialect = postgresDialect{}

// postgresDialect 实现 Dialect。
type postgresDialect struct{}

func (postgresDialect) Name() string { return "postgres" }

// Placeholder：Postgres 用序号占位符 $1、$2……（与 SQLite 的 `?` 的关键差异，
// 也是 Rebind 存在的唯一理由）。
func (postgresDialect) Placeholder(idx int) string { return pgPlaceholder(idx) }

// QuoteIdentifier：Postgres 用双引号，且**引用后大小写敏感**——这一点与 SQLite
// 不同，是移植时最容易踩的坑（未引用的标识符会被折叠成小写）。
func (postgresDialect) QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (postgresDialect) ColumnType(t LogicalType) string {
	switch t {
	case TypeText:
		return "TEXT"
	case TypeInteger:
		return "BIGINT"
	case TypeReal:
		return "DOUBLE PRECISION"
	case TypeBool:
		return "BOOLEAN"
	case TypeJSON:
		// JSONB 而非 JSON：支持 GIN 索引与包含查询，是 Postgres 侧的实质收益。
		return "JSONB"
	case TypeBlob:
		return "BYTEA"
	case TypeTimestamp:
		return "TIMESTAMPTZ"
	default:
		return "TEXT"
	}
}

func (postgresDialect) NowExpr() string { return "NOW()" }

// SupportsConcurrentWriters：Postgres 支持多进程/多节点并发写，
// 这正是把它作为横向扩展目标后端的原因（MVCC + 行级锁）。
func (postgresDialect) SupportsConcurrentWriters() bool { return true }

// postgresBackend 实现 Backend，作为横向扩展的参考原型。
type postgresBackend struct{}

func (postgresBackend) Name() string { return "postgres" }

func (postgresBackend) Dialect() Dialect { return postgresDialectInstance }

// NormalizeDSN 校验连接串形态。
//
// 接受两种写法：URL（postgres://user:pass@host:5432/db?sslmode=disable）与
// keyword/value（host=... user=... dbname=...）。这里只做形态校验，不做连通性
// 检查——后者是 Open/Ping 的职责。
func (postgresBackend) NormalizeDSN(raw string) (string, error) {
	dsn := strings.TrimSpace(raw)
	if dsn == "" {
		return "", fmt.Errorf("db: postgres dsn is empty (expected postgres://... or key=value form)")
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return dsn, nil
	}
	if strings.Contains(dsn, "=") {
		return dsn, nil // keyword/value 形式
	}
	return "", fmt.Errorf("db: invalid postgres dsn %q: expected postgres://... URL or key=value pairs", raw)
}

// Open 通过标准库 database/sql 打开连接，驱动由宿主程序注册。
//
// 若驱动未注册，database/sql 会返回 `unknown driver "pgx" (forgotten import?)`。
// 我们把它包装成带修复指引的错误——「白盒」不仅指可观测，也指出错时不让人猜。
func (postgresBackend) Open(dsn string) (*sql.DB, error) {
	sqlDB, err := sql.Open(postgresDriverName, dsn)
	if err != nil {
		if strings.Contains(err.Error(), "unknown driver") {
			return nil, fmt.Errorf(
				"db: postgres backend requires driver %q to be registered; add a blank import to your main package "+
					`(import _ "github.com/jackc/pgx/v5/stdlib") and rebuild: %w`,
				postgresDriverName, err)
		}
		return nil, fmt.Errorf("failed to open postgres database: %w", err)
	}
	return sqlDB, nil
}

// Configure 设置连接池。
//
// 与 SQLite 的取舍完全相反：Postgres 支持并发写，因此放开连接池上限；
// 这些值刻意保守（避免打爆默认 max_connections=100 的服务端），
// 生产环境应按实例规格与副本数调整，参见 docs/DB_BACKEND_ABSTRACTION.md。
func (postgresBackend) Configure(sqlDB *sql.DB) error {
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	return nil
}

// Bootstrap 明确声明「不支持」，而不是尝试用 SQLite 方言的迁移去跑。
//
// 返回的错误包装了 ErrSchemaBootstrapUnsupported，调用方可用 errors.Is 判定并
// 选择跳过（外部迁移已就绪时）或硬失败。
func (postgresBackend) Bootstrap(*sql.DB) error {
	return fmt.Errorf(
		"%w: the built-in migrations in pkg/db/migrate.go are SQLite-flavored "+
			"(PRAGMA table_info / bare ALTER TABLE ADD COLUMN). Run schema migrations with an external tool "+
			"(golang-migrate, atlas) before starting the server; see docs/DB_BACKEND_ABSTRACTION.md",
		ErrSchemaBootstrapUnsupported)
}

func init() { RegisterBackend(postgresBackend{}) }
