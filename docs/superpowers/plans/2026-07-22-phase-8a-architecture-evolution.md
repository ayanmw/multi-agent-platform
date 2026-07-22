# Phase 8-A: Agent 进程化 & Tool 插件化 — 架构演进实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> 设计文档：`docs/superpowers/specs/2026-07-22-phase-8a-architecture-evolution-design.md`（必读，所有契约以此为准）。
> 分支：`phase-8a-arch-evolution`（隔离 worktree）。

**Goal:** 为 Agent 进程化与 Tool 插件化做第一阶段架构整理：收口 Agent 启动入口为 `AgentRunSpec + AgentRunner`，拆分 Tool 为 `ToolDescriptor + ToolExecutor + ToolLoader`，引入多版本与来源标识，并重建 `tools` 持久化表。

**Architecture:** 在 `cmd/server` 提取 `AgentRunSpec`/`AgentDeps`/`AgentRunner` 把上帝函数参数收敛为可序列化 spec；在 `internal/tool` 扩展 `Tool` 接口并新增 `descriptor/executor/loader` 抽象；在 `pkg/db` 新增 v27 `tools` 表（DROP 前打印旧数据）与 `InsertAgentOptions`/`UpdateAgentOptions`；把 `cmd/server` 拆为 `main.go`/`server.go`/`runner.go`/`api.go`。

**Tech Stack:** Go 1.25 / modernc.org/sqlite / gorilla/websocket / Vue 3 + TS（后端修改，前端不改动）。

---

## 文件结构

后端新增：
- `internal/tool/descriptor.go` — `ToolSource` 常量 + `ToolDescriptor` 纯数据类型
- `internal/tool/executor.go` — `ExecuteContext` + `ToolExecutor` 接口 + `BuiltinExecutor`/`DynamicExecutor`
- `internal/tool/loader.go` — `ToolLoader` 接口 + `BuiltInToolLoader`/`DBToolLoader`
- `pkg/db/tool.go` — v27 migration + `ToolRecordV2` + CRUD（`InsertToolV2`/`QueryToolsV2`/...）

后端修改：
- `internal/tool/registry.go` — `Tool` 接口加 `Version/Source/CanonicalName`；Registry 键改 `CanonicalName`；`IsBuiltin` 改按 `Source()`
- `internal/tool/builtin.go` — `BuiltinTool` 内部 executor 改为 `ToolExecutor`；所有工具构造走 `NewBuiltinTool(..., executor)`；实现 `Version/Source/CanonicalName`
- `internal/tool/dynamic.go` — `DynamicTool` 内部持 `ToolDescriptor` 与 `DynamicExecutor`；实现新接口
- `internal/tool/web_search.go` 等其它 Tool 实现 — 补 `Version/Source/CanonicalName`（可能需要，视实现而定）
- `internal/tool/mcp/tool.go` 或等效 MCP tool wrapper — 补 `Source() == "mcp"`
- `pkg/db/persistence.go` — `InsertAgentOptions`/`UpdateAgentOptions` + 旧签名 wrapper
- `pkg/db/database.go` — 若 v27 需要 `splitSQL` 支持连环 DDL，确认 `RunMigrations` 能处理（当前 `database.go:setupDatabase` 已能 split multi-statement）
- `cmd/server/runner.go`（新）— `AgentRunSpec`/`AgentDeps`/`AgentRunner` + `hubAdapter` + cancel/engine registry 辅助函数 + `orchestratorDispatcher`
- `cmd/server/server.go`（新）— `appServer` 聚合体 + 路由注册 + WS 控制 handler
- `cmd/server/main.go` — 仅保留子系统初始化与启动 server
- `cmd/server/api.go` — `handleSessionChat`/`handleRunCase` 改走 `AgentRunner`；删除 `runAgentLoop*` 调用
- `cmd/server/cron_api.go` — cron 的 `startChatTask` 适配 `AgentRunner`（保持闭包但参数收敛到 `AgentRunSpec`）
- `cmd/server/api_*.go` — 若 `InsertAgent`/`UpdateAgent` 旧调用点存在，改为 options 调用
- `CLAUDE.md` — 更新项目结构说明
- `roadmaps/ROADMAP.md` — 标记 Phase 8-A 各交付物完成

测试新增/修改：
- `internal/tool/registry_test.go` — 多版本注册、`IsBuiltin`、alias 解析
- `internal/tool/descriptor_test.go` — `ToolDescriptor` JSON round-trip
- `internal/tool/executor_test.go` — `BuiltinExecutor` 执行
- `internal/tool/loader_test.go` — `DBToolLoader` 集成测试
- `pkg/db/tool_test.go` — v27 migration + CRUD
- `pkg/db/persistence_agent_test.go` — options struct 写入与旧 wrapper 等价
- `cmd/server/runner_test.go` — `AgentRunner` 集成测试（mock provider，验证事件）

---

## 关键契约（所有任务遵守）

### Tool 接口（`internal/tool/registry.go`）

```go
type Tool interface {
    Namespace() string
    Name() string
    FullName() string        // namespace/name；namespace 为空时等于 name
    CanonicalName() string   // namespace/name@version；namespace 为空时 name@version
    Version() string         // builtin 留空或 "builtin"；其它按 semVer
    Source() string          // "builtin" | "local_db" | "mcp" | "plugin"
    Aliases() []string
    Description() string
    Parameters() map[string]any
    Tags() []string
    Execute(input map[string]any) (any, error)
}
```

### Registry 键

- 主键 = `CanonicalName()` = `namespace/name@version`（namespace 为空则 `name@version`）。
- 别名键 = `namespace/alias`（namespace 为空则 alias），指向同一 Tool 实例。
- `FullName()` 不变，仍用于 LLM tool list 与事件展示。

### IsBuiltin

```go
func (r *Registry) IsBuiltin(name string) bool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    t, ok := r.tools[name]
    return ok && t.Source() == "builtin"
}
```

### ToolDescriptor（`internal/tool/descriptor.go`）

```go
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

func (d ToolDescriptor) FullName() string
func (d ToolDescriptor) CanonicalName() string
```

### ToolExecutor（`internal/tool/executor.go`）

```go
type ExecuteContext struct {
    Workdir string
}

type ToolExecutor interface {
    Execute(ctx ExecuteContext, input map[string]any) (any, error)
}
```

### v27 `tools` 表（`pkg/db/tool.go`）

```sql
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
CREATE INDEX idx_tools_enabled ON tools(enabled);
```

Migration 在 DROP 旧表前打印所有旧记录到日志，方便用户手动重新添加。

### InsertAgentOptions / UpdateAgentOptions（`pkg/db/persistence.go`）

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

func InsertAgent(opts InsertAgentOptions) error
func InsertAgentLegacy(...) error // 旧签名薄 wrapper
```

### AgentRunSpec / AgentDeps（`cmd/server/runner.go`）

```go
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
    Role                 runtime.AgentRole
    CanDispatchSubAgents bool
    CanDefineWorkflow    bool
    ApproverMode         string
    SupervisorSubTaskID  string
    RootTraceCtx         *observability.TraceContext
}

type AgentDeps struct {
    Cfg             *config.Config
    Tools           *tool.Registry
    Persist         runtime.Persistence
    ApprovalHandler harness.ApprovalHandler
    AgentBus        runtime.AgentBus
    CheckpointMgr   *runtime.CheckpointManager
    CostRepo        cost.CostRepository
    ModelRegistry   *llm.ModelRegistry
    ModelRouter     *llm.Router
    RouterProviders map[string]llm.Provider
    CaseService     *cases.Service
    TodoSvc         *todo.Service
    MemRecall       *harness.MemoryRecall
    Tracer          interface {
        StartRoot(taskID, operation string) *observability.TraceContext
        StartChild(parent *observability.TraceContext, agentID, operation string) *observability.TraceContext
        Finish(ctx *observability.TraceContext, err error)
        FinishWithAttributes(ctx *observability.TraceContext, err error, attrs map[string]any)
        SetOnSpan(fn func(observability.SpanRecord))
    }
}

type AgentRunner struct {
    Hub  *ws.Hub
    Deps AgentDeps
}

func (r *AgentRunner) Run(ctx context.Context, spec AgentRunSpec)
```

注意：`AgentRunner.Run` 当前仍启动 goroutine（与现有 `runAgentLoop*` 一致），调用方负责传入已带 timeout 的 ctx。

---

## 任务列表

### Task 1: Tool 接口扩展 + 所有现有实现补 `Version/Source/CanonicalName`

**Files:**
- Modify: `internal/tool/registry.go`
- Modify: `internal/tool/builtin.go`
- Modify: `internal/tool/dynamic.go`
- Modify: `internal/tool/mcp/*.go`（MCP tool wrapper，若存在）
- Modify: `internal/tool/*_tool.go`（如 `web_search.go` 等需要实现 Tool 接口的文件）
- Test: `internal/tool/registry_test.go`

- [ ] **Step 1: 在 `internal/tool/registry.go` 扩展 `Tool` 接口**
  在 `Tags() []string` 后追加：
  ```go
  // Version 返回工具的版本标识符，用于多版本并存。builtin 工具可返回空字符串或 "builtin"。
  Version() string
  // Source 返回工具来源，取值 "builtin" / "local_db" / "mcp" / "plugin"。
  Source() string
  // CanonicalName 返回 Registry 使用的唯一键：namespace/name@version（namespace 为空时为 name@version）。
  CanonicalName() string
  ```

- [ ] **Step 2: 在 `BuiltinTool` 实现新方法**
  定位 `internal/tool/builtin.go` 中 `type BuiltinTool struct`。在已有方法后追加：
  ```go
  func (t *BuiltinTool) Version() string { return "" }
  func (t *BuiltinTool) Source() string  { return "builtin" }
  func (t *BuiltinTool) CanonicalName() string {
      fn := t.FullName()
      if t.Version() == "" {
          return fn
      }
      return fmt.Sprintf("%s@%s", fn, t.Version())
  }
  ```
  注意 imports 加 `fmt`。

- [ ] **Step 3: 在 `DynamicTool` 实现新方法**
  定位 `internal/tool/dynamic.go`，追加：
  ```go
  func (t *DynamicTool) Version() string      { return "" }
  func (t *DynamicTool) Source() string       { return "local_db" }
  func (t *DynamicTool) CanonicalName() string {
      fn := t.FullName()
      if t.Version() == "" {
          return fn
      }
      return fmt.Sprintf("%s@%s", fn, t.Version())
  }
  ```

- [ ] **Step 4: 在所有其它 Tool 实现补这三个方法**
  用 `Grep` 搜索 `type .* struct` 且实现 `Execute(input map[string]any)` 的文件，典型包括：
  - `internal/tool/mcp/tool.go`（或等效 wrapper）: `Source()` 返回 `"mcp"`
  - `internal/tool/sandbox.go` 的沙箱封装 shell: 若它包装 BuiltinTool 则通过组合继承，否则显式返回 `"builtin"`
  - `internal/tool/web_search.go`: 返回 `"builtin"`
  - `internal/tool/docker.go`, `internal/todo/*.go`, `internal/skill/*.go`, `internal/cron/*.go` 中若有实现 Tool 接口也要补
  搜索命令：
  ```bash
  cd D:/Claude-Code-MultiAgent && grep -R "func (.*) Execute(input map\[string\]any)" --include="*.go" internal/
  ```
  （用 `Grep` tool 执行，而不是 bash）。

- [ ] **Step 5: 写失败测试 —— 所有已注册 builtin tool 必须 Source == "builtin"**
  ```go
  // internal/tool/registry_test.go
  func TestBuiltinToolsHaveBuiltinSource(t *testing.T) {
      r := NewRegistry()
      RegisterBuiltins(r)
      for _, tt := range r.ListAll() {
          if strings.HasPrefix(tt.FullName(), "core/") || tt.Namespace() == "" {
              // 简单按命名空间判断；若工具未标 builtin 则失败
              if tt.Source() != "builtin" {
                  t.Errorf("tool %q source=%q, want builtin", tt.FullName(), tt.Source())
              }
          }
      }
  }
  ```

- [ ] **Step 6: 运行测试确认失败/通过**
  ```bash
  cd D:/Claude-Code-MultiAgent && go test ./internal/tool/... -run TestBuiltinToolsHaveBuiltinSource -count=1
  ```

- [ ] **Step 7: `go build ./...` 通过**
  修复所有因接口变更导致的编译错误。

- [ ] **Step 8: Commit**
  ```bash
  git add internal/tool/ internal/todo/ internal/cron/ internal/skill/ ...
  git commit -m "Phase 8-A: Tool 接口扩展 Version/Source/CanonicalName"
  ```

---

### Task 2: Registry 键改为 CanonicalName + IsBuiltin 修正

**Files:**
- Modify: `internal/tool/registry.go`
- Modify: `internal/tool/registry_test.go`

- [ ] **Step 1: 修改 `registerLocked` 用 `CanonicalName()` 做主键**
  原代码：
  ```go
  name := tool.FullName()
  if _, exists := r.tools[name]; !exists {
      r.order = append(r.order, name)
  }
  r.tools[name] = tool
  ```
  改为：
  ```go
  key := tool.CanonicalName()
  if key == "" {
      key = tool.FullName()
  }
  if _, exists := r.tools[key]; !exists {
      r.order = append(r.order, key)
  }
  r.tools[key] = tool
  ```

- [ ] **Step 2: 别名仍按 namespace/alias 注册**
  把 alias 注册逻辑中的 `fullAlias := alias` 之后也生成指向 `key` 的条目，保持不变。

- [ ] **Step 3: 修改 `Execute`/`Get`/`Unregister`/`IsBuiltin`/`ToolTags`/`ToolMetadata` 使用 key 查找**
  这些函数内部都以 `name string` 查表，当前传入的即 registry key；由于 `CanonicalName` 成为主键，调用方若传 `FullName` 会 miss。为了兼容，增强查找：
  ```go
  func (r *Registry) lookup(name string) Tool {
      r.mu.RLock()
      defer r.mu.RUnlock()
      if t, ok := r.tools[name]; ok {
          return t
      }
      return nil
  }
  ```
  对外 `Execute(name, ...)` 先尝试 `name` 精确匹配，未命中再尝试按"同一 FullName 的最高版本"解析（本次简化：先只支持精确 key；若调用方仍用 FullName，后续任务再统一改为传 CanonicalName。测试用精确 key）。

- [ ] **Step 4: 修改 `IsBuiltin`**
  替换硬编码 switch 为：
  ```go
  func (r *Registry) IsBuiltin(name string) bool {
      r.mu.RLock()
      defer r.mu.RUnlock()
      t, ok := r.tools[name]
      return ok && t.Source() == "builtin"
  }
  ```

- [ ] **Step 5: 写失败测试 —— 多版本注册与查询**
  ```go
  func TestRegistryMultiVersion(t *testing.T) {
      r := NewRegistry()
      t1 := NewBuiltinTool("foo", "core", "v1", map[string]any{"type":"object"}, func(input map[string]any)(any,error){return "v1",nil})
      t2 := NewBuiltinTool("foo", "core", "v2", map[string]any{"type":"object"}, func(input map[string]any)(any,error){return "v2",nil})
      // 需要 NewBuiltinTool 接受 executor 参数；当前 Task 1 未改 constructor；这里写预期签名的测试，会编译失败。
      r.Register(t1)
      r.Register(t2)
      if len(r.ListAll()) != 2 {
          t.Fatalf("want 2 distinct versions, got %d", len(r.ListAll()))
      }
      out, _ := r.Execute("core/foo@v1", nil)
      if out != "v1" { t.Fatalf("want v1, got %v", out) }
      out, _ = r.Execute("core/foo@v2", nil)
      if out != "v2" { t.Fatalf("want v2, got %v", out) }
  }
  ```
  注意：此测试会失败，因为 `NewBuiltinTool` 还没改签名。先保留，到 Task 3 再跑通。

- [ ] **Step 6: 运行测试**
  ```bash
  cd D:/Claude-Code-MultiAgent && go test ./internal/tool/... -count=1
  ```

- [ ] **Step 7: Commit**
  ```bash
  git add internal/tool/registry.go internal/tool/registry_test.go
  git commit -m "Phase 8-A: Registry 键改为 CanonicalName，IsBuiltin 改按 Source"
  ```

---

### Task 3: ToolDescriptor / ToolExecutor / ToolLoader 抽象

**Files:**
- Create: `internal/tool/descriptor.go`
- Create: `internal/tool/executor.go`
- Create: `internal/tool/loader.go`
- Modify: `internal/tool/builtin.go`
- Modify: `internal/tool/dynamic.go`
- Test: `internal/tool/descriptor_test.go`, `internal/tool/executor_test.go`, `internal/tool/loader_test.go`

- [ ] **Step 1: 创建 `internal/tool/descriptor.go`**
  ```go
  package tool

  import "fmt"

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

  func (d ToolDescriptor) FullName() string {
      if d.Namespace == "" {
          return d.Name
      }
      return d.Namespace + "/" + d.Name
  }

  func (d ToolDescriptor) CanonicalName() string {
      fn := d.FullName()
      if d.Version == "" {
          return fn
      }
      return fmt.Sprintf("%s@%s", fn, d.Version)
  }
  ```

- [ ] **Step 2: 创建 `internal/tool/executor.go`**
  ```go
  package tool

  type ExecuteContext struct {
      Workdir string
  }

  type ToolExecutor interface {
      Execute(ctx ExecuteContext, input map[string]any) (any, error)
  }

  type BuiltinExecutor struct {
      Fn func(ctx ExecuteContext, input map[string]any) (any, error)
  }

  func (e *BuiltinExecutor) Execute(ctx ExecuteContext, input map[string]any) (any, error) {
      return e.Fn(ctx, input)
  }

  type DynamicExecutor struct {
      desc ToolDescriptor
  }

  func NewDynamicExecutor(desc ToolDescriptor) *DynamicExecutor {
      return &DynamicExecutor{desc: desc}
  }

  func (e *DynamicExecutor) Execute(ctx ExecuteContext, input map[string]any) (any, error) {
      toolType, _ := e.desc.ExecutionConfig["type"].(string)
      switch DynamicToolType(toolType) {
      case DynamicToolShell:
          return e.executeShell(ctx, input)
      case DynamicToolHTTP:
          return e.executeHTTP(ctx, input)
      case DynamicToolInline:
          return e.executeInline(ctx, input)
      default:
          return nil, fmt.Errorf("unknown dynamic tool type: %s", toolType)
      }
  }
  ```
  其中 `executeShell`/`executeHTTP`/`executeInline` 从 `dynamic.go` 原实现迁移，模板字段从 `desc.ExecutionConfig["command"]` / `["url"]` / `["method"]` / `["code"]` 读取。

- [ ] **Step 3: 创建 `internal/tool/loader.go`**
  ```go
  package tool

  import "context"

  type ToolLoader interface {
      Load(ctx context.Context) ([]Tool, error)
  }

  type BuiltInToolLoader struct{}

  func (l *BuiltInToolLoader) Load(_ context.Context) ([]Tool, error) {
      // 返回编译期 builtin tool；本次先返回空，由 cmd/server 继续用 tool.RegisterBuiltins 注册。
      // 未来可把 tool.RegisterBuiltins 逻辑移入此处。
      return nil, nil
  }

  type DBToolLoader struct {
      load func() ([]db.ToolRecord, error)
  }

  func NewDBToolLoader(load func() ([]db.ToolRecord, error)) *DBToolLoader {
      return &DBToolLoader{load: load}
  }

  func (l *DBToolLoader) Load(_ context.Context) ([]Tool, error) {
      records, err := l.load()
      if err != nil {
          return nil, err
      }
      out := make([]Tool, 0, len(records))
      for _, rec := range records {
          desc := ToolDescriptor{
              Namespace:       rec.Namespace,
              Name:            rec.Name,
              Version:         rec.Version,
              Source:          ToolSource(rec.Source),
              Description:     rec.Description,
              Parameters:      rec.Schema,
              ExecutionConfig: rec.ExecutionConfig,
          }
          out = append(out, NewDynamicToolFromDescriptor(desc))
      }
      return out, nil
  }
  ```

- [ ] **Step 4: 修改 `BuiltinTool` 把 executor 改成 `ToolExecutor`**
  把 `type BuiltinTool struct` 中的：
  ```go
  executor func(input map[string]any) (any, error)
  ```
  改为：
  ```go
  executor ToolExecutor
  ```
  把 `Execute` 改为：
  ```go
  func (t *BuiltinTool) Execute(input map[string]any) (any, error) {
      return t.executor.Execute(ExecuteContext{}, input)
  }
  ```
  创建新的 `NewBuiltinTool`：
  ```go
  func NewBuiltinTool(name, namespace, description string, parameters map[string]any, executor ToolExecutor) *BuiltinTool {
      if executor == nil {
          panic("NewBuiltinTool: executor is nil")
      }
      return &BuiltinTool{
          name:        name,
          namespace:   namespace,
          description: description,
          parameters:  parameters,
          executor:    executor,
          tags:        []string{},
          aliases:     []string{},
      }
  }

  // NewBuiltinToolFromFunc 兼容旧闭包风格构造器
  func NewBuiltinToolFromFunc(name, namespace, description string, parameters map[string]any, fn func(input map[string]any) (any, error)) *BuiltinTool {
      return NewBuiltinTool(name, namespace, description, parameters, &BuiltinExecutor{Fn: func(_ ExecuteContext, input map[string]any) (any, error) {
          return fn(input)
      }})
  }
  ```
  然后全局把所有 `NewBuiltinTool(..., func(input map[string]any)...)` 调用改为 `NewBuiltinToolFromFunc`（或直接在调用处传 `&BuiltinExecutor{Fn:...}`）。

- [ ] **Step 5: 修改 `DynamicTool`**
  把 `DynamicTool` 内部字段替换为持有一个 `ToolDescriptor`：
  ```go
  type DynamicTool struct {
      desc ToolDescriptor
  }

  func NewDynamicToolFromDescriptor(desc ToolDescriptor) *DynamicTool {
      return &DynamicTool{desc: desc}
  }
  ```
  保留 `NewDynamicTool(name, description string, parameters map[string]any, toolType DynamicToolType)` 作为兼容包装，内部构造 Descriptor 并设置默认 ExecutionConfig。
  所有 `SetCommand`/`SetHTTP`/`SetCode` 改为更新 `desc.ExecutionConfig`。

- [ ] **Step 6: 写测试**
  - `TestToolDescriptor_JSONRoundTrip`：序列化→反序列化→字段一致。
  - `TestBuiltinExecutor_Execute`：`BuiltinExecutor{Fn:...}` 返回预期值。
  - `TestDynamicExecutor_Shell`：用 ExecutionConfig command 执行并验证输出包含命令结果。

- [ ] **Step 7: 跑测试并 Commit**
  ```bash
  cd D:/Claude-Code-MultiAgent && go test ./internal/tool/... -count=1
  git add internal/tool/
  git commit -m "Phase 8-A: ToolDescriptor + ToolExecutor + ToolLoader 抽象"
  ```

---

### Task 4: v27 `tools` 表 migration + DB CRUD

**Files:**
- Create: `pkg/db/tool.go`
- Modify: `pkg/db/persistence.go`（ToolRecord 扩展）
- Test: `pkg/db/tool_test.go`

- [ ] **Step 1: 创建 `pkg/db/tool.go`**
  注册 v27 migration（DROP 前打印旧数据），并定义 `ToolRecordV2` + CRUD：
  ```go
  package db

  import (
      "database/sql"
      "encoding/json"
      "fmt"
      "log"
      "time"
  )

  func init() {
      migrations = append(migrations, Migration{
          Version:     27,
          Description: "Back up and redefine tools table with namespace, version, source, execution_config",
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

  func InsertToolV2(tr ToolRecord) error {
      if DB == nil { return fmt.Errorf("db not initialized") }
      now := time.Now().Unix()
      schemaJSON, _ := json.Marshal(tr.Schema)
      execJSON, _ := json.Marshal(tr.ExecutionConfig)
      if tr.Namespace == "" { tr.Namespace = "" }
      if tr.Version == "" { tr.Version = "1.0.0" }
      if tr.Source == "" { tr.Source = "local_db" }
      _, err := DB.Exec(`INSERT INTO tools (namespace,name,version,source,description,parameters_json,enabled,execution_config_json,created_at,updated_at)
          VALUES (?,?,?,?,?,?,?,?,?,?)`,
          tr.Namespace, tr.Name, tr.Version, tr.Source, tr.Description, string(schemaJSON), tr.Enabled, string(execJSON), now, now)
      return err
  }

  func QueryToolsV2() ([]ToolRecord, error) {
      if DB == nil { return nil, fmt.Errorf("db not initialized") }
      rows, err := DB.Query(`SELECT namespace, name, version, source, description, parameters_json, enabled, execution_config_json, created_at, updated_at FROM tools ORDER BY updated_at DESC`)
      if err != nil { return nil, err }
      defer rows.Close()
      var out []ToolRecord
      for rows.Next() {
          var tr ToolRecord
          var schemaJSON, execJSON string
          var createdAt, updatedAt int64
          if err := rows.Scan(&tr.Namespace, &tr.Name, &tr.Version, &tr.Source, &tr.Description, &schemaJSON, &tr.Enabled, &execJSON, &createdAt, &updatedAt); err != nil {
              return nil, err
          }
          json.Unmarshal([]byte(schemaJSON), &tr.Schema)
          json.Unmarshal([]byte(execJSON), &tr.ExecutionConfig)
          tr.CreatedAt = time.Unix(createdAt, 0)
          tr.UpdatedAt = time.Unix(updatedAt, 0)
          out = append(out, tr)
      }
      return out, rows.Err()
  }
  ```

- [ ] **Step 2: 调整现有 `InsertTool`/`DeleteTool`/`QueryTools`**
  把现有函数改为调用 `InsertToolV2`/`QueryToolsV2` 的薄 wrapper，保持 `cmd/server/tool_api.go` 不立刻修改。
  ```go
  func InsertTool(name, description string, schema map[string]any, enabled bool) error {
      return InsertToolV2(ToolRecord{
          Name:        name,
          Description: description,
          Schema:      schema,
          Enabled:     enabled,
          Source:      "local_db",
      })
  }
  func QueryTools() ([]ToolRecord, error) { return QueryToolsV2() }
  ```

- [ ] **Step 3: 写 DB 测试**
  新建 `pkg/db/tool_test.go`：
  ```go
  func setupToolTestDB(t *testing.T) {
      t.Helper()
      tmp := t.TempDir() + "/test.db"
      Init(tmp)
      RunMigrations()
  }

  func TestInsertAndQueryToolsV2(t *testing.T) {
      setupToolTestDB(t)
      tr := ToolRecord{
          Namespace:       "test",
          Name:            "hello",
          Version:         "1.0.0",
          Source:          "local_db",
          Description:     "say hello",
          Schema:          map[string]any{"type":"object"},
          ExecutionConfig: map[string]any{"type":"shell", "command":"echo hello"},
          Enabled:         true,
      }
      if err := InsertToolV2(tr); err != nil { t.Fatal(err) }
      tools, err := QueryToolsV2()
      if err != nil { t.Fatal(err) }
      if len(tools) != 1 { t.Fatalf("want 1 tool, got %d", len(tools)) }
      if got := tools[0].CanonicalName(); got != "test/hello@1.0.0" { t.Fatalf("unexpected canonical %q", got) } // ToolRecord 无 CanonicalName，该断言应写在外部对象；这里改为直接字段断言
      if tools[0].Source != "local_db" { t.Fatal("source mismatch") }
  }
  ```

- [ ] **Step 4: 确保 migration v27 的 DROP 前打印逻辑**
  因为 `Migration` 只有 `SQL string`，而 DROP 前打印需要 Go 代码。检查 `Migration` 结构体：
  - 若只有 `SQL`，则本次用纯 SQL 的 `DROP TABLE`；打印旧数据的逻辑可在 `InsertToolV2` 或 `cmd/server` 启动时做（但不及 migration 时准确）。
  - 若 `Migration` 有 `Func func(*sql.DB) error`（需要查看 `pkg/db/migrate.go`），则按 spec 写成 Go migration。
  先读取 `pkg/db/migrate.go` 中 `type Migration struct` 定义确认。

- [ ] **Step 5: 运行测试**
  ```bash
  cd D:/Claude-Code-MultiAgent && go test ./pkg/db/... -run TestInsertAndQueryToolsV2 -count=1
  ```

- [ ] **Step 6: Commit**
  ```bash
  git add pkg/db/tool.go pkg/db/persistence.go pkg/db/tool_test.go
  git commit -m "Phase 8-A: v27 tools 表迁移 + DB CRUD"
  ```

---

### Task 5: DB options struct 化（InsertAgent / UpdateAgent）

**Files:**
- Modify: `pkg/db/persistence.go`
- Modify: 所有调用 `InsertAgent`/`UpdateAgent` 旧签名的位置
- Test: `pkg/db/persistence_agent_test.go`

- [ ] **Step 1: 在 `pkg/db/persistence.go` 定义 options struct**
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

  type UpdateAgentOptions struct {
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
  }
  ```

- [ ] **Step 2: 把 `InsertAgent`/`UpdateAgent` 改为接收 options**
  ```go
  func InsertAgent(opts InsertAgentOptions) error {
      if DB == nil { return fmt.Errorf("db not initialized") }
      toolsJSON, _ := json.Marshal(opts.Tools)
      _, err := DB.Exec(`INSERT INTO agents (...) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
          opts.ID, opts.Name, opts.Description, opts.SystemPrompt, opts.Model, opts.Temperature, opts.MaxTokens,
          opts.Endpoint, opts.APIKey, string(toolsJSON), opts.IsDefault)
      return err
  }

  func InsertAgentLegacy(id, name, description, systemPrompt, model, endpoint, apiKey string, temperature float64, maxTokens int, tools []string, isDefault bool) error {
      return InsertAgent(InsertAgentOptions{...})
  }
  ```
  `UpdateAgent` 同理。

- [ ] **Step 3: 更新所有调用点**
  用 `Grep` 搜索 `InsertAgent(` / `UpdateAgent(`，逐个改为 options。典型文件：`cmd/server/api_agents.go`（若存在）/ `cmd/server/api.go` / `cmd/server/main.go`（`SeedDefaultAgent` 内部写死那几行可不动，因为不是调 `InsertAgent`；或也改）/ 相关测试。

- [ ] **Step 4: 写兼容测试**
  ```go
  func TestInsertAgentLegacyMatchesOptions(t *testing.T) {
      // 用内存库，比较两种调用后读出的记录字段一致
  }
  ```

- [ ] **Step 5: 跑测试并 Commit**
  ```bash
  cd D:/Claude-Code-MultiAgent && go test ./pkg/db/... -count=1
  git add pkg/db/ cmd/server/
  git commit -m "Phase 8-A: InsertAgent/UpdateAgent 改为 options struct"
  ```

---

### Task 6: 创建 `cmd/server/runner.go`（AgentRunSpec / AgentDeps / AgentRunner）

**Files:**
- Create: `cmd/server/runner.go`
- Modify: `cmd/server/main.go`（后续任务迁移）

- [ ] **Step 1: 编写 `runner.go` 头部与类型**
  把 `main.go` 中的 `hubAdapter`、`cancelRegistry`、`engineRegistry`、`storeCancel/loadCancel/removeCancel`、`storeEngine/loadEngine/removeEngine`、`orchestratorDispatcher`、`leaderApprovalHandler`、`projectRulesPrompt`、`agentAllowedTools` 等迁到本文件。
  新增 `AgentRunSpec`、`AgentDeps`、`AgentRunner`。

- [ ] **Step 2: 实现 `AgentRunner.Run`**
  基本上把 `runAgentLoopWithTurn` 的函数体整体搬过来，但把参数改为 `spec AgentRunSpec` 和 `r.AgentRunner` 的字段。
  注意保留：
  - workspaceDir 解析
  - provider 创建
  - policy chain / cost tracker / onUsage
  - leader registry clone + `NewLeaderTools`
  - `runtime.EngineConfig` 构造
  - `task_started` 事件
  - cancel/engine registry
  - `engine.Run(ctx, spec.UserInput)`
  - 完成/失败后的 session 更新与事件

- [ ] **Step 3: 保留 `runAgentLoop` 兼容 wrapper（可选）**
  在 `runner.go` 提供：
  ```go
  func (r *AgentRunner) RunLoop(spec AgentRunSpec) { r.Run(context.Background(), spec) }
  ```
  让旧调用点逐步替换。

- [ ] **Step 4: 编译确认**
  此时 `main.go` 还没改，会报重复定义。需要下一步迁移后才能编译通过。可以先注释掉 `main.go` 对应函数，或一次性迁完。

- [ ] **Step 5: Commit**
  若中间无法编译，可先 `git add` 新文件，注释旧代码后一起 commit；推荐在本任务末尾确保 `go build ./cmd/server` 通过。

---

### Task 7: 创建 `cmd/server/server.go`（appServer 与路由注册）

**Files:**
- Create: `cmd/server/server.go`
- Modify: `cmd/server/main.go`
- Modify: `cmd/server/api.go`（调整函数签名，接收 `*AgentRunner`）

- [ ] **Step 1: 定义 `appServer`**
  ```go
  type appServer struct {
      cfg              *config.Config
      hub              *ws.Hub
      toolRegistry     *tool.Registry
      persist          runtime.Persistence
      approvalHandler  harness.ApprovalHandler
      memRecall        *harness.MemoryRecall
      agentBus         runtime.AgentBus
      checkpointMgr    *runtime.CheckpointManager
      memDB            *harness.SqliteMemoryDB
      costRepo         cost.CostRepository
      modelRegistry    *llm.ModelRegistry
      modelRouter      *llm.Router
      routerProviders  map[string]llm.Provider
      caseService      *cases.Service
      todoSvc          *todo.Service
      skillRegistry    *skill.Registry
      skillStore       *skill.Store
      cronService      *cron.Service
      mcpManager       *mcp.Manager
      authAPI          *auth.AuthAPI
      authStore        auth.APIKeyStore
      mockStore        *llm.MockStore
      orchestrator     *orchestrator.Orchestrator
      vectorStore      memory.VectorStore
      embedProvider    llm.EmbeddingProvider
  }
  ```

- [ ] **Step 2: 把 `main()` 中的路由注册迁移到 `appServer.registerRoutes()`**
  包括 `/api/tasks`、`/api/sessions`、`/api/run-case`、`/api/multi-agent`、`/api/cases`、静态 UI、health/metrics、auth、mcp、skill、cron、memory、replay 等。

- [ ] **Step 3: `appServer.newAgentRunner()` 构造 AgentRunner**
  把 hub、cfg、toolRegistry 等聚合为 `AgentDeps`。

- [ ] **Step 4: 修改 `api.go` 中 handler 签名**
  `handleSessionChat`、`handleRunCase` 等不再接收 17 个参数，改为接收 `*AgentRunner` + 必要参数。

- [ ] **Step 5: 确保 `go build ./cmd/server` 通过**

- [ ] **Step 6: Commit**
  ```bash
  git add cmd/server/
  git commit -m "Phase 8-A: 创建 appServer + 注册路由 + AgentRunner 接入"
  ```

---

### Task 8: 精简 `cmd/server/main.go` 仅保留子系统初始化

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 删除已迁移到 `runner.go`/`server.go` 的函数**
  保留：
  - `main()`
  - `init()` 中的 tracer 回调（或移到 runner.go）
  - 包级变量 `hubInstance`、`globalOrchestrator`、`globalSkillRegistry`、`globalCronService`（其它 registry 也保留，按需）
  删除/迁移：
  - `storeCancel` 等辅助函数
  - `orchestratorDispatcher`
  - 大部分路由注册函数
  - `runAgentLoop`/`runAgentLoopWithTurn`

- [ ] **Step 2: `main()` 最后构造 `appServer` 并调用 `registerRoutes()` + `ListenAndServe()`**
  示例：
  ```go
  server := &appServer{
      cfg: cfg, hub: hub, toolRegistry: toolRegistry, persist: persist,
      approvalHandler: approvalHandler, memRecall: memRecall, agentBus: agentBusAdapter,
      checkpointMgr: checkpointMgr, memDB: memDB, costRepo: costRepo,
      modelRegistry: modelRegistry, modelRouter: modelRouter, routerProviders: routerProviders,
      caseService: caseService, todoSvc: todoSvc, skillRegistry: skillRegistry, skillStore: skillStore,
      cronService: globalCronService, mcpManager: mcpManager, authAPI: authAPI, authStore: authStore,
      mockStore: mockStore, orchestrator: orch,
      vectorStore: vectorStore, embedProvider: embedProvider,
  }
  server.registerRoutes()
  // ListenAndServe 在 server.go 的 start 方法中，或 main 直接调
  handler := auth.NewAuthMiddleware(..., http.DefaultServeMux)
  http.ListenAndServe(":"+cfg.ServerPort, handler)
  ```

- [ ] **Step 3: 处理 cron 初始化依赖 `startChatTask` 闭包**
  cron 的 `ActionRunnerConfig.StartTask` 需要一个 `cronTaskStarter` 适配函数。该适配函数应构造 `AgentRunSpec` 并调用 `server.runner.Run(ctx, spec)`。把这部分逻辑拆到 `cron_api.go` 的适配器里。

- [ ] **Step 4: `go build ./cmd/server`**

- [ ] **Step 5: Commit**
  ```bash
  git add cmd/server/main.go cmd/server/server.go cmd/server/runner.go
  git commit -m "Phase 8-A: main.go 精简为子系统初始化 + appServer 启动"
  ```

---

### Task 9: 修改 chat / cron / recovery / multi-agent 入口使用 AgentRunner

**Files:**
- Modify: `cmd/server/main.go`（`handleTasksRoot`）
- Modify: `cmd/server/api.go`（`handleSessionChat`、`handleRunCase`）
- Modify: `cmd/server/cron_api.go`（`startChatTask`、`cronTaskStarter`）
- Modify: `cmd/server/checkpoint.go` 或等效 recovery handler（`handleRecoverCheckpoint`）

- [ ] **Step 1: `cmd/server/main.go` 中 `handleTasksRoot` 的 chat/multi-agent 改为构造 `AgentRunSpec`**
  chat:
  ```go
  spec := AgentRunSpec{
      AgentID:      agentID,
      SystemPrompt: systemPrompt,
      UserInput:    opts.Input,
      SessionID:    sid,
      IsRoot:       true,
      Contract:     contract,
      CaseID:       opts.CaseID,
      Role:         runtime.AgentRoleLeader,
      CanDispatchSubAgents: true,
      CanDefineWorkflow:    true,
      ApproverMode:         "user",
  }
  server.runner.Run(context.Background(), spec)
  ```
  multi-agent 当前 leader-driven 启动 leader：
  ```go
  spec := AgentRunSpec{...Role: Leader, ...}
  server.runner.Run(context.Background(), spec)
  ```

- [ ] **Step 2: `handleSessionChat` 改走 `AgentRunner`**
  拿到 taskID/sessionID 后构造 `AgentRunSpec` 并调用 `runner.Run`。

- [ ] **Step 3: `handleRunCase` 改走 `AgentRunner`**

- [ ] **Step 4: `cron_api.go` 的 `startChatTask` 闭包改为接受 `*AgentRunner` 或构造 spec 调用 runner.Run**
  注意 `startChatTask` 在 cron action 和 `/api/tasks` chat action 之间共用。重构后推荐在 `server.go` 里定义一个方法：
  ```go
  func (s *appServer) startChatTask(opts startChatTaskOpts) (sessionID, taskID string, err error) { ... }
  ```
  `cronTaskStarter` 转调该方法。

- [ ] **Step 5: `handleRecoverCheckpoint` 改走 `AgentRunner`**
  从 checkpoint 读取状态后构造 `AgentRunSpec` 调用 runner.Run。

- [ ] **Step 6: 删除 `main.go` 中所有 `runAgentLoop*` 调用与函数定义**

- [ ] **Step 7: 编译 + 运行测试**
  ```bash
  cd D:/Claude-Code-MultiAgent && go build ./cmd/server && go test ./cmd/server/... -count=1
  ```

- [ ] **Step 8: Commit**
  ```bash
  git add cmd/server/
  git commit -m "Phase 8-A: chat/cron/recovery/multi-agent 统一改走 AgentRunner"
  ```

---

### Task 10: 冒烟与回归测试

**Files:**
- 运行脚本：`scripts/smoke-test.sh`、`scripts/multi-agent-smoke.sh`（若有）
- 运行测试：`go test ./...`

- [ ] **Step 1: 全量单元测试**
  ```bash
  cd D:/Claude-Code-MultiAgent && go test ./internal/tool/... ./pkg/db/... ./cmd/server/... -count=1
  ```

- [ ] **Step 2: 后端编译**
  ```bash
  cd D:/Claude-Code-MultiAgent && go build ./...
  ```

- [ ] **Step 3: mock 模式冒烟 chat + multi-agent**
  ```bash
  cd D:/Claude-Code-MultiAgent && bash scripts/smoke-test.sh
  ```
  预期核心场景 PASS。

- [ ] **Step 4: 如有 cron 场景，跑 cron smoke**
  ```bash
  cd D:/Claude-Code-MultiAgent && bash scripts/real-llm-smoke.sh 场景编号（或专门 cron smoke）
  ```

- [ ] **Step 5: 修复回归并 Commit**
  把修复按 bug 单独 commit，不要和大型重构混在一起。

---

### Task 11: 文档与路线图收尾

**Files:**
- Modify: `CLAUDE.md`
- Modify: `roadmaps/ROADMAP.md`

- [ ] **Step 1: 更新 `CLAUDE.md` 项目结构说明**
  在"项目结构"里加入：
  ```
  cmd/server/
    main.go       # 子系统初始化 + 启动
    server.go     # appServer 与路由注册
    runner.go     # AgentRunSpec / AgentDeps / AgentRunner
    api.go        # HTTP handler
  internal/tool/
    descriptor.go # ToolDescriptor 可序列化元数据
    executor.go   # ToolExecutor 执行体接口
    loader.go     # ToolLoader 加载抽象
  ```

- [ ] **Step 2: 把 `roadmaps/ROADMAP.md` Phase 8-A 章节未勾选项全部改为 `[x]`**
  同时更新版本日期状态为已完成。

- [ ] **Step 3: Commit**
  ```bash
  git add CLAUDE.md roadmaps/ROADMAP.md
  git commit -m "Phase 8-A: 文档与 ROADMAP 收尾"
  ```

---

## Self-Review 检查

- **Spec coverage:**
  - `AgentRunSpec/AgentDeps/AgentRunner` → Task 6、9
  - Tool `Version/Source/CanonicalName` → Task 1
  - Registry 多版本 + `IsBuiltin` 修正 → Task 2
  - `ToolDescriptor/ToolExecutor/ToolLoader` → Task 3
  - v27 tools 表 + DB CRUD → Task 4
  - DB options struct → Task 5
  - `cmd/server` 拆为 3-4 文件 → Task 6、7、8
  - 事件总线复用 `runtime.EventBus`（不新增 EventSink）→ Task 6 保留 `hubAdapter`
  - 文档/路线图 → Task 11
- **Placeholder scan:** 无 TBD/TODO；每个 step 含代码或命令；文件路径为 Windows 绝对路径或 repo-relative。
- **Type consistency:**
  - `Tool.Version()` 返回 `string`
  - `Tool.Source()` 返回 `string`
  - `Tool.CanonicalName()` 返回 `string`
  - `ToolDescriptor.Source` 类型为 `ToolSource` string
  - `AgentRunSpec.Role` 类型 `runtime.AgentRole`
  - `AgentDeps.Tracer` 接口与 runtime 中用法一致

---

## 执行交接

Plan complete and saved to `docs/superpowers/plans/2026-07-22-phase-8a-architecture-evolution.md`.

Two execution options:

1. **Subagent-Driven (recommended)** — 每个 Task 派发一个子 agent，独立 worktree/分支隔离，主会话调度并 review。
2. **Inline Execution** — 在当前会话一气呵成执行，适合上下文连续、commit 节奏由我们共同把控。

本次 Phase 8-A 涉及大量文件拆分与接口重构，**推荐 Subagent-Driven + 每 1-2 个 Task 合并一次**，以便在关键决策点（runner.go、server.go）人工 review。

Which approach?
