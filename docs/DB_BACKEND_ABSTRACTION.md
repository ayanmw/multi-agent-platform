# DB 后端可插拔抽象（N3-04c / E7 可扩展性）

> 本文档是 `pkg/db` 后端可插拔抽象的接口说明与原型指南。错误提示（如
> `ErrSchemaBootstrapUnsupported`）会指向本文档，作为「下一步该怎么做」的唯一出处。

---

## 1. 动机：为什么需要这一层

在 N3-04c 之前，`pkg/db` 的 `Init()` 把「用哪个驱动、怎么调参、建表迁移怎么跑」
硬编码在一起：`sql.Open("sqlite", path)` + `SetMaxOpenConns(1)` + SQLite 专属 PRAGMA +
`createTables()` + `RunMigrations()`。

这带来两个问题，二者都是 Phase R（轮次 14 评审）中 **E7 = Partial** 的核心证据：

1. **横向扩展没有任何入口**。SQLite 的「单文件单写」约束被固化进了架构 —— 多节点
   共享一份状态（部署多个 server 实例指向同一个 Postgres）在架构上没有落点。
2. **方言差异无处收敛**。占位符 `?` vs `$N`、`BLOB` vs `BYTEA`、`JSON` vs `JSONB` 等
   差异散落在数千行 CRUD 中，没有一个集中的方言开关。

本抽象通过两个正交接口，把「后端差异」从 `Init()` 流程中抽离出来，让水平扩展成为
**架构上可达、机器可读、可测试**的目标，而不是口头承诺。

---

## 2. 架构：两个正交抽象

```
                 pkg/db
   ┌─────────────────────────────────────────────┐
   │  Init / InitWithBackend / InitWithBackendOptions│
   │            （统一初始化流水线）                 │
   └───────────────┬─────────────────────────────┘
                   │  LookupBackend(name)
                   ▼
            ┌──────────────┐        实现 ┌──────────────────┐
            │ Backend 接口  │◀───────────│ sqliteBackend{}   │  (默认, 零配置)
            │ (连接生命周期)│           ├──────────────────┤
            └──────┬───────┘◀───────────│ postgresBackend{} │  (原型)
                   │ Dialect()          └──────────────────┘
                   ▼
            ┌──────────────┐
            │ Dialect 接口  │  Name / Placeholder / QuoteIdentifier
            │ (SQL 方言)    │  ColumnType / NowExpr / SupportsConcurrentWriters
            └──────────────┘
```

### 2.1 `Backend` —— 连接生命周期

负责「怎么连、怎么调参、怎么建表」：

```go
type Backend interface {
    Name() string                      // 注册名："sqlite" / "postgres"
    NormalizeDSN(raw string) (string, error)   // 校验/规范化 DSN（建父目录、校验 scheme）
    Open(dsn string) (*sql.DB, error)          // 建立 *sql.DB（惰性，未 Ping）
    Configure(sqlDB *sql.DB) error             // 连接池 + 会话级参数（Ping 之后、Bootstrap 之前）
    Bootstrap(sqlDB *sql.DB) error             // 建表 + 跑迁移；不支持则返回 ErrSchemaBootstrapUnsupported
    Dialect() Dialect                          // 返回该后端的方言
}
```

### 2.2 `Dialect` —— SQL 方言

负责「跨后端一定会不同、且能被机械转换」的 SQL 差异：

```go
type Dialect interface {
    Name() string
    Placeholder(idx int) string        // SQLite "?"；Postgres "$1" "$2"…
    QuoteIdentifier(name string) string // 引用标识符（注意 Postgres 引用后大小写敏感）
    ColumnType(t LogicalType) string   // 逻辑类型 → 物理类型（见 §2.3）
    NowExpr() string                   // "CURRENT_TIMESTAMP" / "NOW()"
    SupportsConcurrentWriters() bool   // false=单写(SQLite)；true=并发写(Postgres)
}
```

`SupportsConcurrentWriters()` 是 **E7「横向扩展路径清晰」的机器可读判据**。
`InitWithBackendOptions` 在启动期会为非并发写后端打印一条 `slog.Debug` 告警，让运维
在容量规划阶段就看见单写约束，而不是等到线上出现写冲突。

### 2.3 `LogicalType` —— 与具体库无关的列类型

CRUD 只描述「我要存什么」，由 `Dialect.ColumnType` 决定物理类型，新增后端时类型映射
集中在一处：

| LogicalType      | SQLite        | Postgres             |
|------------------|---------------|---------------------|
| `TypeText`       | `TEXT`        | `TEXT`              |
| `TypeInteger`    | `INTEGER`     | `BIGINT`            |
| `TypeReal`       | `REAL`        | `DOUBLE PRECISION`  |
| `TypeBool`       | `BOOLEAN`*    | `BOOLEAN`           |
| `TypeJSON`       | `JSON`*       | `JSONB`             |
| `TypeBlob`       | `BLOB`        | `BYTEA`             |
| `TypeTimestamp`  | `DATETIME`    | `TIMESTAMPTZ`       |

\* SQLite 无原生布尔/JSON 类型，二者均为 affinity 别名，保留语义可读性。

---

## 3. 统一初始化流水线

`InitWithBackendOptions` 是唯一的初始化入口，所有后端复用同一管线：

```
LookupBackend(name)
  → NormalizeDSN(dsn)
  → Open(dsn)
  → Ping()                         // Open 惰性，Ping 才真建连；失败即关句柄防泄漏
  → Configure(sqlDB)              // 连接池 + 会话参数
  → DB = sqlDB; setActiveBackend(backend)   // 先赋值全局句柄（既有 CRUD 依赖它）
  → Bootstrap(sqlDB)              // 除非 SkipBootstrap
  → 非并发写后端 → slog.Debug 告警
```

公开 API（向后兼容）：

```go
func Init(dataPath string) error                              // 默认后端=SQLite，全仓 25+ 调用点无需改动
func InitWithBackend(backendName, dsn string) error          // 指定后端
func InitWithBackendOptions(backendName, dsn string, opts InitOptions) error

type InitOptions struct {
    SkipBootstrap bool   // 跳过建表+迁移，适用于 schema 由外部迁移工具管理的部署
}
```

---

## 4. 后端注册表

```go
func RegisterBackend(b Backend)        // 通常在包 init() 调用；nil/空名/重名 直接 panic（编程错误不应静默）
func LookupBackend(name string) (Backend, error)  // 大小写不敏感；空名回退默认后端；未知名返回可用列表
func BackendNames() []string           // 已注册名（排序，保证日志/错误信息可复现）

func ActiveBackend() Backend           // 当前生效后端（未初始化=nil）
func ActiveDialect() Dialect           // 当前方言；未初始化回退 SQLite 方言（便于测试）

func Rebind(d Dialect, query string) string  // 见 §6
```

`sqliteBackend` 与 `postgresBackend` 的 `init()` 各自调用 `RegisterBackend`，因此只要
编译进二进制即自动可用（`DB_BACKEND=postgres` 无需额外注册代码）。

---

## 5. SQLite —— 默认零配置实现（行为不变）

`backend_sqlite.go` 把原 `Init()` 的全部逻辑**逐字节搬迁**，行为完全不变：

- 驱动名 `"sqlite"`（`modernc.org/sqlite`，纯 Go，单文件）。
- `SetMaxOpenConns(1)`：单连接串行化，避免并发写 BUSY/LOCKED。
- `PRAGMA busy_timeout = 5000` + `PRAGMA journal_mode = WAL`。
- 不全局开启 `foreign_keys`（历史路径在插入 task 前不一定保证 session 存在）。
- `Bootstrap` 跑 `createTables()` + `RunMigrations()`，并断言操作的是全局 `DB` 句柄。

即：**任何部署不做任何改动即可继续工作**，`DB_BACKEND` 缺省即 SQLite。

---

## 6. `Rebind` —— 既有 CRUD 的跨方言适配器

既有 CRUD 全部以 `?` 书写（SQLite 原生）。`Rebind(d, query)` 把 `?` 改成目标方言占位符，
让这些语句**无需改写**即可在 Postgres 上执行。

解析规则（刻意保守，宁可不转换也不能转错）：

- 跳过单引号字符串字面量（含 `''` 转义）与双引号标识符；
- 跳过 `--` 行注释与 `/* */` 块注释；
- 仅替换裸露的 `?`；
- 同构方言（`Placeholder(1) == "?"`）走零成本快速路径，原样返回。

```go
// 既有代码（无需改动）：
rows, _ := DB.Query(`SELECT id FROM tasks WHERE status = ? AND created_at > ?`, s, t)
// Postgres 后端使用时包裹一层：
rows, _ := DB.Query(db.Rebind(backend.Dialect(),
    `SELECT id FROM tasks WHERE status = ? AND created_at > ?`), s, t)
```

> 注意：`Rebind` 只解决占位符，不解决类型/函数语义差异。复杂查询
> （窗口函数、全文检索、专有能力）应由各后端提供专门的 Repository 实现，不在 `Dialect`
> 职责内。

---

## 7. Postgres 原型（诚实的能力边界）

`backend_postgres.go` 是一个**可运行、可测试**的原型，把「换 Postgres 要改哪些地方」
变成代码中可验证的清单。它刻意保留两个**显式缺口**，运行时以明确错误告知使用者，
而不是静默半可用：

### 缺口 1：驱动不内置

`pkg/db` **不 import 任何 Postgres 驱动**（不为未启用的后端增加编译期依赖与传递依赖树）。
宿主程序想启用时，在自己的 `main` 包加一行 blank import：

```go
import _ "github.com/jackc/pgx/v5/stdlib"   // 注册驱动名 "pgx"
```

未注册时 `Open` 会返回一条**可操作**的错误（指明要加哪行 import），而不是
`database/sql` 那句语焉不详的 `unknown driver`。

### 缺口 2：schema 引导不支持

现有 ~738 行迁移脚本是 SQLite 方言（`PRAGMA table_info`、不带 `IF NOT EXISTS` 的
`ALTER TABLE ADD COLUMN`、AUTOINCREMENT 语义差异），跨方言复用会**静默产出错误 schema**。
因此 `postgresBackend.Bootstrap` **直接返回 `ErrSchemaBootstrapUnsupported`**（已用
`errors.Is` 判定可识别），而不是假装成功。调用方 `InitWithBackendOptions` 对此**不静默
跳过**——启动即失败并附带修复指引，远比「连上了但没有表、首个请求时才炸」清晰。

启动 Postgres 后端的唯一方式（外部迁移已就绪时）：

```go
db.InitWithBackendOptions("postgres", "postgres://user:pass@host:5432/db?sslmode=disable",
    db.InitOptions{SkipBootstrap: true})
```

### 原型已完整覆盖的部分

- 方言：占位符 `$N`、类型映射（`JSONB`/`BYTEA`/`TIMESTAMPTZ`/`BIGINT`）、引用规则、
  并发写能力声明 —— **均有单测覆盖**（`TestPostgresDialect` 等）。
- 连接装配：`NormalizeDSN`（校验 URL / key=value 形态）、`Open`（可操作错误）、
  `Configure`（`MaxOpenConns(25)` / `MaxIdleConns(5)`，与 SQLite 的串行化取舍相反）。
- 配合 `Rebind()`，既有以 `?` 书写的 CRUD 语句可在 Postgres 上执行。

---

## 8. 配置开关

`internal/config` 新增两个配置项（缺省保持 SQLite 零配置）：

| 配置项              | 环境变量             | 默认      | 说明                                              |
|---------------------|----------------------|-----------|---------------------------------------------------|
| `DBBackend`         | `DB_BACKEND`         | `sqlite`  | 选择后端（`sqlite` / `postgres`）；大小写不敏感   |
| `DBSkipBootstrap`   | `DB_SKIP_BOOTSTRAP`  | `false`   | 跳过内建建表+迁移（外部迁移工具管理 schema 时设 `true`）|

`cmd/server/main.go` 的接线：

```go
if err := db.InitWithBackendOptions(cfg.DBBackend, cfg.DBPath,
    db.InitOptions{SkipBootstrap: cfg.DBSkipBootstrap}); err != nil {
    log.Fatal(err)
}
// 启动日志含 backend 字段；非并发写后端额外打印 WARN（E7 横向扩展可见性）
```

---

## 9. 如何新增一个后端（如 MySQL / 已有 Postgres 生产化）

1. 新建 `backend_<name>.go`，实现 `Dialect` 与 `Backend` 两个接口。
2. 在 `init()` 中调用 `RegisterBackend(<name>Backend{})`。
3. 若复用既有 `?` 书写的 CRUD，确保在执行前用 `db.Rebind(backend.Dialect(), query)`
   重写占位符（或在 Repository 层集中处理）。
4. 类型映射只在 `ColumnType` 一处；复杂查询写专门 Repository。
5. 若自带内建迁移，在 `Bootstrap` 中实现；否则返回 `ErrSchemaBootstrapUnsupported`
   并依赖外部迁移工具。
6. 在 `backend_test.go` 中补 `Test<Name>Dialect` / `Test<Name>NormalizeDSN` /
   `Test<Name>Bootstrap*` 覆盖，参照既有 SQLite / Postgres 测试。

---

## 10. 把 Postgres 真正用上生产还需要什么

本原型是「架构可达」的证明，不是生产后端。要落地需要一个独立的迁移工作（建议在
N3 之后的专项里程碑中完成）：

1. **外部迁移工具**：引入 golang-migrate 或 atlas，把现有 SQLite 迁移改写为方言无关
   的迁移集（关键改造：去掉 `PRAGMA table_info` 自检、给 `ALTER TABLE ADD COLUMN` 加
   `IF NOT EXISTS`、统一自增语义）。
2. **驱动依赖**：宿主 `main` 包 blank import `pgx/v5/stdlib`（或纳入 `go.mod`）。
3. **连接池调优**：`MaxOpenConns` / `MaxIdleConns` 按实例规格与副本数调整
   （当前原型用保守的 25/5，避免打爆默认 `max_connections=100`）。
4. **标识符大小写**：Postgres 引用标识符后大小写敏感，未引用标识符折叠为小写 —— 现有
   SQL 中的大小写敏感的表/列名需在迁移与查询中统一（最容易踩的坑）。
5. **外键一致性**：SQLite 默认不开 FK，Postgres 端若开启需补齐历史数据保证。
6. **测试接入**：在 CI 中启动一个 ephemeral Postgres（testcontainer / 现有 sqlite 测试
   之外），对 `Rebind` 后的 CRUD 跑真实集成测试。

---

## 11. 设计约束（刻意为之，勿轻易放宽）

1. **零行为变化**：SQLite 仍是默认，调用方代码与既有 CRUD 一行未改（继续用全局 `DB`）。
2. **不引入新依赖**：Postgres 只提供原型，驱动由宿主程序注册；`pkg/db` 不 import 它。
3. **`pkg/db` 是叶子包**：不得 import `internal/observability`（会形成
   observability → pkg/db → observability 循环），日志一律用标准库 `log/slog`。
4. **诚实的能力边界**：非 SQLite 后端 schema 引导返回 `ErrSchemaBootstrapUnsupported`
   而非假装成功 —— 迁移跨方言复用会静默产生错误 schema。

---

## 12. 测试覆盖

`pkg/db/backend_test.go`（~360 行，全部通过）：

- `TestRegisteredBackends` / `TestLookupBackend` / `TestRegisterBackendDuplicatePanics`
- `TestSQLiteDialect` / `TestPostgresDialect`
- `TestRebind`（8 子例：insert / where / 字符串字面量 / 转义引号 / 引用标识符 /
  行注释 / 块注释 / 无占位符 + 同构快速路径）
- `TestPostgresNormalizeDSN` / `TestPostgresBootstrapUnsupported`
  （`errors.Is(err, ErrSchemaBootstrapUnsupported)` + 文档指引）
- `TestPostgresOpenWithoutDriverGivesActionableError`（驱动缺失时自动降级为可操作错误）
- `TestInitWithBackendSQLiteBehaviorUnchanged`（回归守卫：`MaxOpenConns==1`、WAL、
  `agents` 等表 + `schema_migrations` 存在、`ActiveBackend=="sqlite"`）
- `TestInitWithBackendUnknownName` / `TestInitOptionsSkipBootstrap` / `TestSQLiteMemoryDSN`

验证命令（本地全绿）：

```bash
go build ./... && go vet ./... && go test -count=1 ./...
bash scripts/cases-regression.sh   # 21/21
bash scripts/smoke-test.sh         # 63 PASS / 0 FAIL / 1 SKIP
```
