# Phase 8-A: 架构演进设计 — Agent 进程化与 Tool 插件化

> 日期：2026-07-22  
> 范围：B（中等）  
> 分支/工作区：`phase-8a-arch-evolution`  
> 状态：设计 spec，待 review

---

## 1. 背景与目标

进入 Phase 7-Cron 后，系统在 Agent 启动链路（`cmd/server/main.go` 的 `runAgentLoop*`）与 Tool 子系统上出现了两类可预见的瓶颈：

1. **Agent 启动函数上帝化**：`runAgentLoopWithTurn` 已超过 20 个参数，复用只能依赖闭包，导致 cron、checkpoint recovery、multi-agent、session chat 四条入口各自重复拼装 EngineConfig。
2. **Tool 系统边界不清晰**：内置工具的 executor 是闭包，无法序列化；Registry 以 `FullName` 为键，同名工具会静默覆盖；`IsBuiltin` 仍硬编码 3 个旧工具名；动态工具持久化表 schema 没有 namespace/version/source/execution_config，阻碍了未来 plugin/WASM/子进程执行。

Phase 8-A 不是把 Agent 立刻拆成独立进程，也不是立即引入 WASM 插件，而是**为这两个方向做第一阶段架构整理**：把 agent 启动入口收口成 `AgentRunner + AgentRunSpec`，把 Tool 拆成 `ToolDescriptor + ToolExecutor + ToolLoader`，并补齐多版本与持久化表，使跨进程/跨语言扩展在未来变得直接、可测试。

本设计对应的两个探索性主题分别归档为：

- `docs/agent-process-isolation-research.md`
- `docs/tool-pluginization-research.md`

（仅方向探索，不写实施计划。）

---

## 2. 范围边界（Scope B）

**本次做（范围 B）：**

1. **AgentRunner 提取**（中等）：在 `cmd/server` 新增 `runner.go`。定义 `AgentRunSpec`、`AgentDeps`，由 `AgentRunner.Run(ctx, spec)` 负责把纯数据 spec 映射成 `runtime.EngineConfig` 并启动 goroutine；`cmd/server/main.go`、`api.go`、`cron_api.go` 的入口统一改为调用 `AgentRunner`。
2. **Tool 接口扩展与 Registry 多版本**（中等）：给 `Tool` 增加 `Version()/Source()`；Registry 键改为 `namespace/name@version`；`IsBuiltin` 改为按 `Source() == "builtin"` 判断；保证同一 namespace/name 不同 version 可共存；别名仍可在 `namespace` 内解析。
3. **v27 `tools` 表迁移与持久化层扩展**（中等）：在 `pkg/db` 新建 `tool.go`，注册 v27 migration，新表含 `namespace / name / version / source / description_json / parameters_json / enabled / execution_config_json / created_at / updated_at`。扩展 `ToolRecord`。
4. **ToolDescriptor / ToolExecutor / ToolLoader 抽象**（中等，奠基意义）：
   - `ToolDescriptor`：纯数据、可 JSON 序列化，含 namespace/name/version/source/description/parameters/aliases/tags/execution_config。
   - `ToolExecutor`：接口 `Execute(input map[string]any, ctx ExecuteContext) (any, error)`；内置工具实现该接口。
   - `BuiltinTool` 仍作为 `Tool` 实现，但内部通过 `executor ToolExecutor` 执行；新动态工具从 DB 加载后也能构造 executor。
   - `ToolLoader` 接口：从 DB / local file / 未来 plugin 加载 Tool。
5. **DB options struct 化**（小-中）：把 `pkg/db.InsertAgent / UpdateAgent` 超过 8 参数改为 `InsertAgentOptions / UpdateAgentOptions` struct，保持旧函数薄封装可兼容。
6. **`cmd/server` 拆成 3-4 个文件**（中等）：`api.go` 已存在，新增 `server.go`、`runner.go`。`server.go` 放 `appServer` 聚合体与路由注册；`runner.go` 放 `AgentRunSpec/AgentDeps/AgentRunner/hubAdapter/相关 registry 管理`；`main.go` 只剩 `main()` 与全局基础设施编排。
7. **复用 `runtime.EventBus` 解耦 Hub，不新增 EventSink**（小）：已有 `hubAdapter` 足够，确认并保留其作为 `runtime.EventBus` 的实现。
8. **文档与路线图更新**：两篇探索文档 + 更新 `roadmaps/ROADMAP.md`；设计 spec 与实现计划。

**本次不做：**

- 把 Agent 真正拆成独立 OS 进程或 sidecar。
- 真正加载外部 WASM/.so/Python 插件。
- REST API 变更（`/api/tools` 的行为保留；底层持久化结构升级但 API 字段兼容）。
- Phase 7 的 UI v2、Cron、Skill 子系统功能扩展。

---

## 3. 架构设计

### 3.1 AgentRunner 与 AgentRunSpec

```go
// 在 cmd/server/runner.go

type AgentRunSpec struct {
    TaskID        string
    AgentID       string
    SystemPrompt  string
    UserInput     string
    SessionID     string
    ParentTaskID  string
    TurnIndex     int
    IsRoot        bool
    Contract      harness.TaskContract
    CaseID        string
    WorkingMemory string
    Role          runtime.AgentRole
    CanDispatchSubAgents bool
    CanDefineWorkflow    bool
    ApproverMode         string
    SupervisorSubTaskID  string
    RootTraceCtx         *observability.TraceContext
}

type AgentDeps struct {
    Cfg              *config.Config
    Tools            *tool.Registry
    Persist          runtime.Persistence
    ApprovalHandler  harness.ApprovalHandler
    AgentBus         runtime.AgentBus
    CheckpointMgr    *runtime.CheckpointManager
    CostRepo         cost.CostRepository
    ModelRegistry    *llm.ModelRegistry
    ModelRouter      *llm.Router
    RouterProviders  map[string]llm.Provider
    CaseService      *cases.Service
    TodoSvc          *todo.Service
    MemRecall        *harness.MemoryRecall
    Tracer           runtimeTracer // 见后文
}

type AgentRunner struct {
    Hub  *ws.Hub
    Deps AgentDeps
}

func (r *AgentRunner) Run(ctx context.Context, spec AgentRunSpec) {
    // 1. 解析 workspaceDir、provider、policy chain、onUsage、session message writer
    // 2. root/leader 时 clone toolRegistry 并注入 leader 工具
    // 3. 构造 runtime.EngineConfig
    // 4. storeCancel / storeEngine / defer remove
    // 5. 发送 task_started 事件
    // 6. engine.Run(ctx, userInput)
    // 7. 任务完成/失败后更新 session、发送事件
}
```

**关键决策：**

- `AgentDeps` 是**进程内服务指针聚合**；保持不变性假设（runner 不会修改其中字段）。
- `AgentRunSpec` 是**纯数据、可序列化**；未来即使 Agent 拆进程，也可把它 JSON 化后传给子进程 stdin/IPC。这是本轮最核心的价值。
- `AgentRunner.Run` 仍负责启动 goroutine（与现在一致），但调用方可以选择同步等待（需要时再加 `RunSync`）。

### 3.2 Tool 身份与多版本

**Tool 接口扩展（`internal/tool/registry.go`）：**

```go
type Tool interface {
    Namespace() string
    Name() string
    FullName() string        // namespace/name，namespace 为空时 = name
    CanonicalName() string   // namespace/name@version
    Version() string         // semantic-ish，如 "1.0.0"；builtin 留空时视为 "v0" 或 "builtin"
    Source() string          // "builtin", "local_db", "mcp", "plugin"
    Aliases() []string
    Description() string
    Parameters() map[string]any
    Tags() []string
    Execute(input map[string]any) (any, error)
}
```

**Registry 键：**

- 主键：`CanonicalName()`，即 `namespace/name@version`；namespace 为空时 `name@version`。
- 别名键：仍按 `namespace/alias` 注册，指向主键对应的 Tool 实例。
- `FullName()` 仍返回 `namespace/name`（不含版本），用于事件、审批 UI、工具调用人可读标识。

**IsBuiltin：**

```go
func (r *Registry) IsBuiltin(name string) bool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    t, ok := r.tools[name]
    return ok && t.Source() == "builtin"
}
```

**多版本策略：**

- 注册时若主键已存在，按现有逻辑**静默覆盖最新版**（未来可加 conflict policy，本次不做）。
- 同一 `namespace/name` 下不同 version 都能在 `ListAll()` 返回（按注册顺序）。
- LLM tool list 默认只发最新版（通过 `List()` 去重后每个 `FullName` 只保留最高 version）；需要多版本时可走 `ListAll()`。

### 3.3 ToolDescriptor / ToolExecutor / ToolLoader

**位置**：`internal/tool/descriptor.go`、`internal/tool/executor.go`、`internal/tool/loader.go`。

```go
// descriptor.go

type ToolSource string

const (
    ToolSourceBuiltin ToolSource = "builtin"
    ToolSourceLocalDB ToolSource = "local_db"
    ToolSourceMCP     ToolSource = "mcp"
    ToolSourcePlugin  ToolSource = "plugin"
)

type ToolDescriptor struct {
    Namespace       string         `json:"namespace"`
    Name            string         `json:"name"`
    Version         string         `json:"version"`
    Source          ToolSource     `json:"source"`
    Description     string         `json:"description"`
    Parameters      map[string]any `json:"parameters"`
    Aliases         []string       `json:"aliases"`
    Tags            []string       `json:"tags"`
    ExecutionConfig map[string]any `json:"execution_config"`
}

func (d ToolDescriptor) FullName() string     { ... }
func (d ToolDescriptor) CanonicalName() string { return fmt.Sprintf("%s@%s", d.FullName(), d.Version) }
```

```go
// executor.go

type ExecuteContext struct {
    Workdir string
    // 未来可扩展 env、timeout、approver 等
}

type ToolExecutor interface {
    Execute(ctx ExecuteContext, input map[string]any) (any, error)
}

// BuiltinExecutor 用 Go 函数实现（替代现在的闭包 executor func）
type BuiltinExecutor struct {
    Fn func(ctx ExecuteContext, input map[string]any) (any, error)
}

type DynamicExecutor struct { ... } // 从 Descriptor.ExecutionConfig 解析 shell/http/inline 执行
```

```go
// loader.go

type ToolLoader interface {
    Load(ctx context.Context) ([]Tool, error)
}

type BuiltInToolLoader struct {
    cfg *config.Config // 如 web_search 配置
}

type DBToolLoader struct { ... } // 从 pkg/db.QueryToolsV2 加载
```

**BuiltinTool 改造：**

- 把 `executor func(input map[string]any) (any, error)` 改成 `executor ToolExecutor`。
- `BuiltinTool.Execute(input)` 内部调用 `t.executor.Execute(ExecuteContext{}, input)`。
- `NewBuiltinTool` 增加 `ToolExecutor` 参数；保留旧签名的兼容包装，通过闭包构造 BuiltinExecutor。

**DynamicTool 改造：**

- 内部保存一个 `ToolDescriptor`。
- `DynamicTool` 实现 `Tool` 接口，并作为数据+执行体的统一入口；未来若需纯数据，可调用 `Descriptor()`。

### 3.4 v27 `tools` 表迁移

**新建 `pkg/db/tool.go`：**

```go
package db

func init() {
    migrations = append(migrations, Migration{
        Version:     27,
        Description: "Redefine tools table with namespace, version, source, execution_config",
        SQL: `DROP TABLE IF EXISTS tools;
CREATE TABLE tools (
    namespace TEXT DEFAULT '',
    name TEXT NOT NULL,
    version TEXT NOT NULL DEFAULT '1.0.0',
    source TEXT NOT NULL DEFAULT 'local_db',
    description TEXT DEFAULT '',
    parameters_json TEXT DEFAULT '{}',
    enabled BOOLEAN DEFAULT 1,
    execution_config_json TEXT DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (namespace, name, version)
);
CREATE INDEX idx_tools_source ON tools(source);
CREATE INDEX idx_tools_enabled ON tools(enabled);`,
    })
}
```

**说明：**

- v27 直接 DROP 旧表重建。旧表中只存了动态 tool，且 name 是全局唯一的，数据价值低；重建最简单可靠。
- 时间字段用 INTEGER unix 秒，与 cron/skills 保持一致。
- 扩展 `ToolRecord`：
  ```go
  type ToolRecord struct {
      Namespace       string
      Name            string
      Version         string
      Source          string
      Description     string
      Schema          map[string]any
      ExecutionConfig map[string]any
      Enabled         bool
      CreatedAt       time.Time
      UpdatedAt       time.Time
  }
  ```

### 3.5 DB options struct 化

**`pkg/db/persistence.go`：**

```go
type InsertAgentOptions struct {
    ID           string
    Name         string
    Description  string
    SystemPrompt string
    Model        string
    Endpoint     string
    APIKey       string
    Temperature  float64
    MaxTokens    int
    Tools        []string
    IsDefault    bool
}

func InsertAgent(opts InsertAgentOptions) error { ... }

// 兼容旧签名
func InsertAgentLegacy(id, name, description, systemPrompt, model, endpoint, apiKey string, temperature float64, maxTokens int, tools []string, isDefault bool) error {
    return InsertAgent(InsertAgentOptions{...})
}
```

`UpdateAgent` 同理。

### 3.6 文件拆分

- `cmd/server/server.go`：
  - `type appServer struct { ... }`
  - `func (s *appServer) registerRoutes()`
  - `func (s *appServer) newAgentRunner() *AgentRunner`
  - 控制 handler、静态文件服务、version endpoint 等可迁到本文件。
- `cmd/server/runner.go`：
  - `AgentRunSpec / AgentDeps / AgentRunner`
  - `hubAdapter`、`storeCancel/loadCancel/removeCancel`、`storeEngine/loadEngine/removeEngine`
  - `projectRulesPrompt`、`agentAllowedTools` 等 runner 辅助函数
  - `orchestratorDispatcher`（依赖 orchestrator，可作为 runner 的配套）
- `cmd/server/main.go`：
  - `main()` 函数与子系统初始化（costRepo、auth、db、memory、model registry/router、toolRegistry、MCP、skill/todo/cron、agentBus、checkpointMgr、orchestrator）
  - 创建 `appServer`，调用 `registerRoutes()` 与 `ListenAndServe`。
- `cmd/server/api.go`：
  - 保留 `handleSessionChat`、`handleRunCase`、agent/session/project/cases API handler；
  - 修改后通过 `AgentRunner` 启动任务，不再直接调用 `runAgentLoop*`。

### 3.7 EventBus 复用

已有：

```go
type hubAdapter struct{ hub *ws.Hub }
func (a *hubAdapter) SendEvent(evt event.Event) { a.hub.SendEvent(evt) }
```

该实现满足 `runtime.EventBus` 接口。本次：**不新增 EventSink**，保留 `hubAdapter`，但把它移到 `runner.go`（或保留在原位）。所有需要发事件的子系统（cron/todo/tracer）统一接收 `runtime.EventBus` 注入。

---

## 4. 接口与类型清单

| 包 | 新增/修改 | 名称 | 说明 |
|---|---|---|---|
| `cmd/server` | 新增 | `AgentRunSpec` | 纯数据 spec |
| `cmd/server` | 新增 | `AgentDeps` | 进程内服务指针聚合 |
| `cmd/server` | 新增 | `AgentRunner` | 负责 spec → Engine → goroutine |
| `cmd/server` | 新增 | `server.go` | `appServer` 与路由注册 |
| `internal/tool` | 扩展接口 | `Tool` | 加 `CanonicalName/Version/Source` |
| `internal/tool` | 新增 | `ToolDescriptor` | 可序列化元数据 |
| `internal/tool` | 新增 | `ToolExecutor` | 执行体接口 |
| `internal/tool` | 新增 | `ToolLoader` | 加载抽象 |
| `internal/tool` | 修改 | `BuiltinTool` | executor 改为接口 |
| `internal/tool` | 修改 | `DynamicTool` | 持有 Descriptor + executor |
| `pkg/db` | 新增 | `pkg/db/tool.go` | v27 migration + CRUD |
| `pkg/db` | 扩展 | `ToolRecord` | namespace/version/source/execution_config |
| `pkg/db` | 重构 | `InsertAgentOptions` / `UpdateAgentOptions` | options struct |

---

## 5. 数据流与调用链

### 5.1 chat / cron / recovery 统一入口

```
handleSessionChat / handleRunCase / cron start_task / handleRecoverCheckpoint
               │
               ▼
        AgentRunSpec{...}
               │
               ▼
    AgentRunner.Run(ctx, spec)
               │
               ▼
    runtime.EngineConfig{...}
               │
               ▼
    runtime.NewEngine(..., engineTools, &hubAdapter{hub}, taskID)
               │
               ▼
    engine.Run(ctx, userInput)
```

### 5.2 Tool 加载与注册

```
main()
  │
  ├──► BuiltInToolLoader.Load() → BuiltinTool + BuiltinExecutor
  │        │
  │        ▼
  │    toolRegistry.Register(tool)
  │
  ├──► DBToolLoader.Load() → DynamicTool + DynamicExecutor
  │        │
  │        ▼
  │    toolRegistry.Register(tool)
  │
  └──► MCP manager 注册 server 工具（仍走现有路径）
```

### 5.3 Tool 调用

```
Engine.toolRegistry.Execute(name, input)
               │
               ▼
    Tool.Execute(input) ─► ToolExecutor.Execute(ctx, input)
```

---

## 6. 兼容性与降级策略

1. **Tool 接口新增方法**：所有现有 Tool 实现（BuiltinTool、DynamicTool、MCP tool wrapper）必须实现 `Version()/Source()/CanonicalName()`。
   - BuiltinTool 的 `Version()` 返回 `""`（内部当 builtin 处理）或 `"builtin"`，`Source()` 返回 `"builtin"`。
   - DynamicTool 的 `Source()` 返回 `"local_db"`。
   - MCP tool 的 `Source()` 返回 `"mcp"`。
2. **Registry 键变化**：旧代码中通过 `FullName()` 查找工具的逻辑仍可工作，因为 `FullName()` 不变；若需精确版本，使用 `CanonicalName()`。
3. **动态工具旧持久化数据**：v27 DROP 表重建，旧动态工具数据丢失；但动态工具通常由运行时创建，可接受。
4. **DB InsertAgent 旧签名**：保留旧函数作为薄 wrapper，内部转调 options 版本。
5. **cron / todo / skill**：它们通过 `runtime.EventBus` 消费事件；`hubAdapter` 不变，无需修改。

---

## 7. 测试策略

1. **Registry 多版本测试**：在 `internal/tool/registry_test.go` 中增加：
   - 注册 `core/foo@1.0.0` 与 `core/foo@2.0.0`，验证 `ListAll()` 返回 2 个、`List()` 默认只返回 1 个（最新版）。
   - 验证 `IsBuiltin("run_shell") == true`、`IsBuiltin("skill/create_local") == false`。
2. **ToolDescriptor 序列化测试**：`TestToolDescriptor_JSONRoundTrip`。
3. **BuiltinExecutor 测试**：`TestBuiltinExecutor_Execute`。
4. **DBToolLoader 测试（integration）**：用内存 SQLite 写入 ToolRecord，加载后验证 `CanonicalName/Parameters/ExecutionConfig`。
5. **AgentRunner 测试（integration）**：用 mock provider + 内存 registry 跑一轮 `AgentRunner.Run`，验证发出 `task_started` 与 `task_completed` 事件。
6. **db options struct 测试**：确保 `InsertAgent` 与 `InsertAgentLegacy` 写入行一致。
7. **编译检查**：`go build ./...` 通过。
8. **冒烟测试**：`go test ./internal/tool/... ./pkg/db/... ./cmd/server/...`。

---

## 8. 任务拆分（预规划，供 writing-plans 展开）

1. 写 `docs/agent-process-isolation-research.md`。  
2. 写 `docs/tool-pluginization-research.md`。  
3. 扩展 `Tool` 接口并在所有实现上补 `Version/Source/CanonicalName`。  
4. 改 Registry 键为 `CanonicalName`，修正 `IsBuiltin`。  
5. 新增 `ToolDescriptor` / `ToolExecutor` / `ToolLoader`。  
6. 改造 `BuiltinTool` 与 `DynamicTool`。  
7. 新增 `pkg/db/tool.go` v27 migration 与 CRUD。  
8. DB 层 `InsertAgentOptions` / `UpdateAgentOptions`。  
9. `cmd/server/runner.go`：AgentRunSpec / AgentDeps / AgentRunner。  
10. `cmd/server/server.go`：appServer + 路由/控制 handler 迁移。  
11. 修改 `cmd/server/main.go`：使用 appServer + AgentRunner。  
12. 修改 `cmd/server/api.go` 与 `cron_api.go` 调用 AgentRunner。  
13. 单元测试与冒烟测试。  
14. 更新 `roadmaps/ROADMAP.md`，提交 Git。

---

## 9. 风险与回退方案

| 风险 | 可能性 | 影响 | 回退方案 |
|---|---|---|---|
| Registry 键改动导致 LLM tool list 顺序/名称变化 | 中 | LLM 可能按旧名调用工具 | 保留 `FullName()` 不变；LLM 工具列表仍按 `FullName` 输出；Registry 查找同时支持 alias |
| Builtin tool Source 字段遗漏导致 IsBuiltin 误判 | 中 | 动态 API 错误拒绝/允许删除 | 新增单测强制校验所有 builtin tool 的 `Source()=="builtin"` |
| v27 DROP 表导致旧动态工具丢失 | 低 | 历史动态工具不复存在 | 可接受；后续若需要可额外写 backfill 脚本，本次不做 |
| main.go 拆分后全局变量引用丢失 | 中 | 编译失败 | 每拆一步都 `go build ./cmd/server`，CI 不通过不继续 |
| AgentRunner 丢失现有事件/行为 | 中 | 任务周期事件变少 | 按现有 `runAgentLoopWithTurn` 代码 1:1 移植，新增集成测试验证事件 |

---

## 10. Self-Review 清单

- [x] 无 "TBD" / "TODO" 占位（除明确标注的未来 phase）。
- [x] 架构内部一致：Hub 通过 `hubAdapter` 实现 `runtime.EventBus`，不新增 EventSink。
- [x] 范围明确：不做真进程、不做真 WASM。
- [x] `IsBuiltin` 不再硬编码工具名单。
- [x] `AgentRunSpec` 纯数据，可序列化。
- [x] DB 时间字段统一为 INTEGER unix 秒，与现有 skill/cron 模式一致。
- [x] 兼容性条款覆盖 Tool 接口扩展、Registry 键变化、DB signature 变化。

---

*本 spec 供用户 review；review 通过后将据此调用 `writing-plans` skill 生成分任务实现计划。*
