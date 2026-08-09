// backend.go — 数据库后端可插拔抽象（N3-04c / E7 可扩展性）
//
// # 为什么需要这一层
//
// 在 N3-04c 之前，`pkg/db` 把「用哪个驱动、怎么调参、建表迁移怎么跑」硬编码在
// `Init()` 里：`sql.Open("sqlite", path)` + `SetMaxOpenConns(1)` + SQLite 专属
// PRAGMA + `createTables()` + `RunMigrations()`。这意味着：
//
//   - SQLite 的「单文件单写」限制被固化进了架构，横向扩展（多节点共享一份状态）
//     没有任何入口；这是 Phase R 轮次 14 评审中 E7 = Partial 的核心证据之一。
//   - 方言差异（占位符 `?` vs `$N`、`BLOB` vs `BYTEA`、`JSON` vs `JSONB`）散落在
//     7000 行 CRUD 里，无处可以集中收敛。
//
// 本文件引入两个正交的抽象：
//
//   - Backend —— 「连接生命周期」抽象：打开连接、调参、引导 schema。
//   - Dialect —— 「SQL 方言」抽象：占位符、标识符引用、逻辑类型到物理类型的映射。
//
// # 设计约束（刻意为之，勿轻易放宽）
//
//  1. **零行为变化**：SQLite 依旧是默认实现，`Init(path)` 的语义、PRAGMA、连接池
//     参数与调用方代码全部保持不变。既有 CRUD 一行未改（它们继续用全局 `DB`）。
//  2. **不引入新依赖**：Postgres 后端只提供方言与连接装配的原型，驱动由宿主程序
//     自行 `import _ "github.com/jackc/pgx/v5/stdlib"` 注册；`pkg/db` 不 import 它。
//  3. **`pkg/db` 是叶子包**：不得 import `internal/observability`（会形成
//     observability → pkg/db → observability 循环），日志一律用标准库 `log/slog`。
//  4. **诚实的能力边界**：非 SQLite 后端的 schema 引导返回
//     `ErrSchemaBootstrapUnsupported` 而不是假装成功——迁移脚本目前是 SQLite 方言
//     （`PRAGMA table_info`、`ALTER TABLE ... ADD COLUMN` 无 IF NOT EXISTS），
//     必须由外部迁移工具承担，详见 docs/DB_BACKEND_ABSTRACTION.md。
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// DefaultBackendName 是未显式配置时使用的后端名。
// 保持 SQLite = 零配置默认，任何部署不做任何改动即可继续工作。
const DefaultBackendName = "sqlite"

// ErrSchemaBootstrapUnsupported 表示该后端不自带 schema 引导能力。
//
// 语义：连接可以建立、CRUD 可以执行，但「建表 + 迁移」必须由外部迁移工具
// （golang-migrate / atlas / 手工 DDL）完成。这不是缺陷而是刻意的能力声明——
// 现有 738 行迁移脚本是 SQLite 方言，跨方言复用会静默产生错误 schema。
var ErrSchemaBootstrapUnsupported = errors.New("db: schema bootstrap not supported by this backend")

// LogicalType 是与具体数据库无关的列类型。
//
// CRUD 代码只描述「我要存什么」，由 Dialect 决定物理类型。这样新增后端时，
// 类型映射集中在一个地方，而不是散落在几十条 CREATE TABLE 里。
type LogicalType int

const (
	TypeText      LogicalType = iota // 变长字符串
	TypeInteger                      // 64 位整数
	TypeReal                         // 浮点数
	TypeBool                         // 布尔（SQLite 无原生类型，用 BOOLEAN 别名）
	TypeJSON                         // JSON 文档（Postgres 用 JSONB 以获得索引能力）
	TypeBlob                         // 二进制（memories.embedding 向量）
	TypeTimestamp                    // 时间戳
)

// Dialect 抽象 SQL 方言差异。
//
// 它只覆盖「跨后端一定会不同、且能被机械转换」的部分；复杂查询（窗口函数、
// 全文检索）不在此列——那类差异应由后端各自提供专门的 Repository 实现。
type Dialect interface {
	// Name 返回方言名（与 Backend.Name 一致，便于日志与断言）。
	Name() string

	// Placeholder 返回第 idx 个绑定参数的占位符，idx 从 1 开始。
	// SQLite/MySQL 返回 "?"；Postgres 返回 "$1"、"$2"……
	Placeholder(idx int) string

	// QuoteIdentifier 按方言规则引用表名/列名，避免与保留字冲突。
	QuoteIdentifier(name string) string

	// ColumnType 把逻辑类型映射为该方言的物理列类型。
	ColumnType(t LogicalType) string

	// NowExpr 返回「当前时间」的 SQL 表达式（用于 DEFAULT 与 UPDATE）。
	NowExpr() string

	// SupportsConcurrentWriters 声明该后端是否允许多个进程/节点并发写同一份数据。
	// SQLite = false（单文件单写，横向扩展的硬约束）；Postgres = true。
	// 这是 E7「横向扩展路径清晰」的机器可读判据，供启动期告警与容量规划使用。
	SupportsConcurrentWriters() bool
}

// Backend 抽象一个数据库后端的连接生命周期。
//
// 实现者需保证 Open → Configure → Bootstrap 三步可按序调用且幂等
// （Bootstrap 对已初始化的库应为 no-op 或仅补齐缺失部分）。
type Backend interface {
	// Name 返回后端注册名（"sqlite" / "postgres"）。
	Name() string

	// NormalizeDSN 校验并规范化数据源标识。
	// SQLite：文件路径，需确保父目录存在；Postgres：URL，需校验 scheme。
	// 返回规范化后的 DSN 供 Open 使用。
	NormalizeDSN(raw string) (string, error)

	// Open 建立 *sql.DB（尚未 Ping）。驱动名由实现决定。
	Open(dsn string) (*sql.DB, error)

	// Configure 设置连接池与会话级参数（SQLite: MaxOpenConns(1) + WAL + busy_timeout）。
	// 在 Ping 成功之后、Bootstrap 之前调用。
	Configure(sqlDB *sql.DB) error

	// Bootstrap 建表并运行 schema 迁移。
	// 不支持的后端必须返回包装了 ErrSchemaBootstrapUnsupported 的错误，
	// 由调用方决定是硬失败还是（在外部迁移已就绪时）跳过。
	Bootstrap(sqlDB *sql.DB) error

	// Dialect 返回该后端的 SQL 方言。
	Dialect() Dialect
}

// ---------- 后端注册表 ----------

var (
	backendsMu sync.RWMutex
	backends   = map[string]Backend{}
)

// RegisterBackend 注册一个数据库后端实现。
//
// 通常在包的 init() 中调用。重复注册同名后端会 panic——这属于编程错误，
// 静默覆盖会让「到底连的哪个库」变得不可推理，违背白盒哲学。
func RegisterBackend(b Backend) {
	if b == nil {
		panic("db: RegisterBackend(nil)")
	}
	name := strings.ToLower(strings.TrimSpace(b.Name()))
	if name == "" {
		panic("db: RegisterBackend with empty name")
	}
	backendsMu.Lock()
	defer backendsMu.Unlock()
	if _, dup := backends[name]; dup {
		panic(fmt.Sprintf("db: backend %q registered twice", name))
	}
	backends[name] = b
}

// LookupBackend 按名查找后端；名字大小写不敏感，空名回退到默认后端。
func LookupBackend(name string) (Backend, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		key = DefaultBackendName
	}
	backendsMu.RLock()
	b, ok := backends[key]
	backendsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("db: unknown backend %q (available: %s)", name, strings.Join(BackendNames(), ", "))
	}
	return b, nil
}

// BackendNames 返回已注册后端名的有序列表（排序保证日志/错误信息可复现）。
func BackendNames() []string {
	backendsMu.RLock()
	names := make([]string, 0, len(backends))
	for n := range backends {
		names = append(names, n)
	}
	backendsMu.RUnlock()
	sort.Strings(names)
	return names
}

// ---------- 占位符重写 ----------

// Rebind 把以 `?` 为占位符书写的 SQL 转换成目标方言的占位符形式。
//
// 现有 CRUD 全部用 `?` 书写（SQLite 原生），Rebind 让这些语句无需改写即可
// 在 Postgres（`$N`）上执行——这是「既有 CRUD 不动或用适配器」验收标准的
// 适配器实现。
//
// 解析规则（刻意保守，宁可不转换也不能转错）：
//   - 跳过单引号字符串字面量（含 `''` 转义）与双引号标识符；
//   - 跳过 `--` 行注释与 `/* */` 块注释；
//   - 只替换裸露的 `?`。
func Rebind(d Dialect, query string) string {
	if d == nil || d.Placeholder(1) == "?" {
		return query // 同构方言，零成本快速路径
	}
	var b strings.Builder
	b.Grow(len(query) + 8)

	idx := 0
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch c {
		case '\'', '"':
			// 字符串字面量 / 引用标识符：原样拷贝到配对的引号为止。
			quote := c
			b.WriteByte(c)
			i++
			for i < len(query) {
				b.WriteByte(query[i])
				if query[i] == quote {
					// 连续两个引号是转义，继续留在字面量内部。
					if i+1 < len(query) && query[i+1] == quote {
						i++
						b.WriteByte(query[i])
					} else {
						break
					}
				}
				i++
			}
		case '-':
			if i+1 < len(query) && query[i+1] == '-' {
				for i < len(query) && query[i] != '\n' {
					b.WriteByte(query[i])
					i++
				}
				i-- // 让外层 for 的 i++ 落在换行符上
				continue
			}
			b.WriteByte(c)
		case '/':
			if i+1 < len(query) && query[i+1] == '*' {
				b.WriteString("/*")
				i += 2
				for i < len(query) && !(query[i] == '*' && i+1 < len(query) && query[i+1] == '/') {
					b.WriteByte(query[i])
					i++
				}
				if i < len(query) {
					b.WriteString("*/")
					i++
				}
				continue
			}
			b.WriteByte(c)
		case '?':
			idx++
			b.WriteString(d.Placeholder(idx))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// ---------- 活跃后端状态 ----------

var (
	activeMu      sync.RWMutex
	activeBackend Backend
)

// setActiveBackend 由 InitWithBackend 在初始化成功后记录当前后端。
func setActiveBackend(b Backend) {
	activeMu.Lock()
	activeBackend = b
	activeMu.Unlock()
}

// ActiveBackend 返回当前生效的后端；未初始化时返回 nil。
func ActiveBackend() Backend {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return activeBackend
}

// ActiveDialect 返回当前生效的方言；未初始化时回退到 SQLite 方言，
// 使工具函数（如 Rebind）在测试等未初始化场景下仍有确定行为。
func ActiveDialect() Dialect {
	if b := ActiveBackend(); b != nil {
		return b.Dialect()
	}
	return sqliteDialectInstance
}

// pgPlaceholder 生成 Postgres 风格占位符，抽出来便于复用与测试。
func pgPlaceholder(idx int) string {
	return "$" + strconv.Itoa(idx)
}
