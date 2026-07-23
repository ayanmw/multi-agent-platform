# Phase 8-B: 架构收尾设计 — 缺陷修复与上帝函数消除

> 日期：2026-07-23
> 范围：B（中等）
> 分支/工作区：`worktree-phase-8b-arch-cleanup`（`.claude/worktrees/phase-8b-arch-cleanup`）
> 前置：Phase 8-A 已合并 main（commit `8d3dcc7`）
> 方法论：superpowers（brainstorming → writing-plans → 执行）
> 状态：设计 spec，待 review

---

## 1. 背景与目标

Phase 8-A 完成了"架构奠基"：`AgentRunSpec/AgentDeps/AgentRunner` 收口启动链路、Tool 接口扩展 `Version/Source/CanonicalName`、`ToolDescriptor/ToolExecutor/ToolLoader` 抽象、v27 tools 表迁移、`cmd/server` 拆分为 main/server/runner/api 四文件。但 8-A 的 spec 第 2 节"本次不做"明确把"handler 透传收敛"与"加载链路接线"划在范围外，留下了三类问题：

1. **真实回归（P0）**：动态工具注册后重启即丢失。`handleRegisterTool` 用旧 `db.InsertTool` 封装（不写 `execution_config_json`），且 `main.go` 启动期不从 DB 加载动态工具进 registry（无 `QueryToolsV2` 调用）。v27 表有 `execution_config_json` 字段，但无生产路径写它、也无路径在启动时读它。
2. **死代码抽象（B3）**：`BuiltInToolLoader`（`Load` 返回 `nil,nil`）、`DynamicExecutor`（`DynamicTool.Execute` 不委托它，自己 switch 三种 type）均无生产调用。抽象建了但两头没接上。
3. **recovery 路径未收口 + 执行上下文未通（P1）**：`handleRecoverCheckpoint` 仍直接构建 Engine（未走 AgentRunner），且 EngineConfig 缺 `SkillRegistry/AgentBus/WorkingMemory/SessionMessageWriter/ActiveTodos/WorkspaceDir` 等字段，恢复后的 agent 是退化的。同时 `BuiltinTool.Execute` 传 `ExecuteContext{}`，`Workdir` 永远空，新执行抽象没有真正承担执行上下文职责。
4. **上帝函数残留（B1+B2）**：`handleSessionChat`(17参)/`handleRunCase`(17参)/`handleRecoverCheckpoint`(12参) 仍是包级高元函数；`handleTasksRoot`/`startChatTask` 是 main() 闭包，捕获 15+ 局部变量，靠包级 `var` 跨文件共享；`appServer` 已聚合 24 个依赖字段却未被 handler 使用。

Phase 8-B 的目标是**让 8-A 已经造好但没接线的抽象接上 + 把 8-A 没走完的最后一公里走完 + 消除 HTTP 层上帝函数**，使 8-A 的投资真正生效。不引入新 capability，不跨 Phase。

### 与 8-A 的关系

8-B 是 8-A spec 主动划在范围外的收尾工作，不是新需求。8-A 造好了"插座"（AgentRunner / Descriptor / Executor / Loader / v27 表），8-B 把"电线"接上（Loader 接线 / Executor 接线 / Workdir 接线 / recovery 接入）并拆除"旧明线"（闭包退场 / handler 方法化）。

---

## 2. 范围边界（Scope B）

**本次做：**

1. **P0 — 动态工具持久化与启动期加载**（修真实回归）
   - `handleRegisterTool` 改用 `db.InsertToolV2`，把 command/url/method/code 写进 `execution_config_json`。
   - `main.go` 启动期在 `tool.RegisterBuiltins` 之后，调 `db.QueryToolsV2()` + `tool.DBToolLoader` 把 `source=local_db` 的记录还原为 `DynamicTool` 注册进 registry。
   - `handleDeleteTool` 改用 `db.DeleteToolV2`（按 namespace/name/version 删除）。
   - 注册前冲突检查改为按 `CanonicalName` 判断（支持多版本）。

2. **B3 — 死代码诚实化**（混合方案 Z）
   - `DynamicTool.Execute` 改为委托 `DynamicExecutor`（`NewDynamicExecutor(descriptor)`），`DynamicTool` 持有一个 `executor *DynamicExecutor` 字段，自身退回为"Descriptor + Executor 薄壳"。`executeShell/executeHTTP/executeInline` 三个私有方法从 `dynamic.go` 删除（逻辑迁到 `executor.go`，已存在）。
   - `BuiltInToolLoader` 删除（内置工具注册保持 `tool.RegisterBuiltins` 直跑，强行套 Loader 是过度抽象）。
   - `DynamicExecutor` 与 `ExecuteContext` 成为动态工具的统一执行路径，承接 P1b 的 Workdir。

3. **P1 — recovery 接入 AgentRunner + 执行上下文接通**
   - **P1a**：`AgentRunner` 新增 `Recover(ctx, RecoverSpec)` 入口。`RecoverSpec` 携带 `TaskID` 与 `CheckpointManager`（或复用 `AgentDeps.CheckpointMgr`），内部调 `runtime.RecoverFromCheckpoint` 构建 Engine，但 EngineConfig 补齐 `SkillRegistry/ActiveSkills/AgentBus/WorkingMemory/SessionMessageWriter/ActiveTodos/WorkspaceDir/Tracer/RootTraceCtx/CostRepo 回调` 等字段，让恢复路径与正常 chat 走同一套依赖注入。`handleRecoverCheckpoint` 改为 `(s *appServer) handleRecoverCheckpoint(w, r)`，调 `s.newRunner().Recover(ctx, RecoverSpec{...})`。
   - **P1b**：`ExecuteContext.Workdir` 从 Engine 的 `WorkspaceDir` 注入。Engine 在调用 `tools.Execute` 前，若 `cfg.WorkspaceDir != ""`，构造 `ExecuteContext{Workdir: cfg.WorkspaceDir}` 透传给工具。`Tool` 接口的 `Execute(input)` 签名**不变**（保持向后兼容）——Workdir 通过 Registry/Engine 内部桥接注入到 `BuiltinTool.executor` 与 `DynamicExecutor`，不暴露给接口调用方。`run_shell`/`write_file` 改读 `ExecuteContext.Workdir` 作为默认 CWD（LLM 显式传 `workdir` 仍优先）。

4. **B1 — handler 全部方法化 + switch 分发改为 Register 模式**
   - `cmd/server` 所有包级 handler（`handleSessionChat`/`handleRunCase`/`handleRecoverCheckpoint`/`handleListCheckpoints`/`handleTasksRoot`/`handleListTasks`/`handleGetTask`/`handleSessions`/`handleAgents`/`handleAgentByID`/`handleMemoryByID`/`handleMemoryEmbed`/`handleMemoryStats`/`handleListMemories`/`handleCreateMemory`/`handleAudit`/`handleTraces`/`handleReplay`/`handleReplayEvents`/`handleContractLimits`/`handleRegisterTool`/`handleListTools`/`handleDeleteTool`/`handleGetTaskContextWindow`/`handleGetAgentMessages` 等）改为 `(s *appServer) handleXxx(w, r)` 方法，依赖从 `s` 取。
   - `registerRoutes` 注册方式改为方法值：`mux.HandleFunc("/api/xxx", s.handleXxx)`，删除 30+ 行"局部别名 + 透传"样板。
   - **`switch req.Action` 分发改为 Register 模式**：`handleTasksRoot` 内的 `switch req.Action { case "chat" / "multi-agent" / "stream-demo" }` 拆为 action handler 注册表 `map[string]actionHandler`，每个 action 是独立方法 `(s *appServer) actionChat(req) (...)` / `actionMultiAgent` / `actionStreamDemo`，降低 `handleTasksRoot` 体积并允许拆到多个文件。同理适用于 `handleTasks` 子资源路由（`context_window`/`agent-messages`/单任务详情）与 `/api/tools` 的 method 分发、`/api/sessions` 的 method 分发等多分支 switch。
   - `registerSkillRoutes`/`registerTodoRoutes`/`registerMCPRoutes`/`registerMCPMarketRoutes`/`RegisterMockRoutes`/`RegisterModelPriceRoutes`/`RegisterCronAPI` 等"子路由注册函数"改为 `appServer` 方法（`(s *appServer) registerSkillRoutes()`），依赖从 `s` 取，不再透传参数。

5. **B2 — 闭包退场 + `appServer` 持有 cron Service**
   - `handleTasksRoot` 闭包退场：从 main() 移出，改为 `(s *appServer) handleTasksRoot(w, r)`，删除包级 `var handleTasksRoot func(...)`。
   - `startChatTask` 闭包退场：改为 `(s *appServer) startChatTask(opts startChatTaskOpts) (...)`，删除包级 `var startChatTask func(...)`。
   - `appServer` 新增字段持有 `*cron.Service`。cron 的 `ActionRunner` 在 `main()` 构造时注入 `s.startChatTask` 作为 `TaskStarter`（经 `cronTaskStarter` 适配器，适配器也改为捕获 `s` 或变成 `appServer` 方法）。
   - `appServer` 字段按子系统分组、组间空行隔离、每组加注释说明对应分组（见 §3.4）。
   - 配置型单例（`globalSkillRegistry`/`globalOrchestrator`/`globalCronService`/`tracer`）尽量进 `appServer` 字段；运行期并发协调 Map（`cancelRegistry`/`engineRegistry`/`traceRegistry`）保留包级——它们是运行期状态注册表，不是依赖注入。允许少量包级 var 保留，但尽量进 `appServer`。

6. **文档与路线图更新**：更新 `roadmaps/ROADMAP.md`（新增 v0.13.0 版本记录）与 `CLAUDE.md`（扩展 Phase 表加 8-B、更新项目结构）。

**本次不做（B4/B5 follow-up，8-B 收尾时再考量是否单独立项）：**

- **B4**：`BuildRecordFromProfile`/`orchestrator.New` 等 options struct 化（边际价值低）。
- **B5**：Linter 强制函数 arity 上限（需要引入 golangci-lint 自定义规则，基础设施成本高）。
- 真正加载外部 WASM/.so/Python 插件。
- REST API 行为变更（`/api/tools` 字段兼容；底层持久化结构升级但 API 响应不变）。
- Phase 7 的 UI v2、Cron、Skill 子系统功能扩展。

---

## 3. 架构设计

### 3.1 P0 — 动态工具持久化与启动期加载

**写端（`handleRegisterTool`）：**

```go
// cmd/server/tool_api.go
func (s *appServer) handleRegisterTool(w http.ResponseWriter, r *http.Request) {
    // ...解析 req（Name/Description/Parameters/Type/Command/URL/Method/Code）...
    // 构造 ToolDescriptor.ExecutionConfig（含 type + command/url/method/code）
    execConfig := map[string]any{"type": req.Type}
    switch req.Type {
    case "shell":  execConfig["command"] = req.Command
    case "http":   execConfig["url"] = req.URL; execConfig["method"] = req.Method
    case "inline": execConfig["code"] = req.Code
    }
    // 用 InsertToolV2 持久化（写 execution_config_json）
    if err := db.InsertToolV2(db.ToolRecord{
        Name: req.Name, Description: req.Description, Schema: req.Parameters,
        Source: "local_db", Enabled: true, ExecutionConfig: execConfig,
    }); err != nil { /* 回滚 registry 注册 */ }
    // ...注册到 registry（用 NewDynamicToolFromDescriptor）+ 审计日志...
}
```

**读端（`main.go` 启动期）：**

```go
// 在 tool.RegisterBuiltins(toolRegistry) 之后
if db.DB != nil {
    loader := tool.NewDBToolLoader(func() ([]map[string]any, error) {
        records, err := db.QueryToolsV2()
        // 把 []db.ToolRecord 转为 []map[string]any（DBToolLoader.Load 的输入）
        ...
        return maps, nil
    })
    if tools, err := loader.Load(context.Background()); err == nil {
        for _, t := range tools {
            if t.Source() == "local_db" {  // 只加载动态工具，不重复注册 builtin
                toolRegistry.Register(t)
            }
        }
        log.Printf("Loaded %d dynamic tool(s) from DB", len(tools))
    }
}
```

**冲突检查**：从 `for _, t := range toolRegistry.List() { if t.Name() == req.Name }` 改为按 `CanonicalName` 判断（`toolRegistry.Get(dt.CanonicalName())` 命中即冲突），支持多版本并存。

**Delete**：`db.DeleteTool(name)` → `db.DeleteToolV2(namespace, name, version)`（动态工具 namespace 默认空、version 默认 `1.0.0`）。

### 3.2 B3 — DynamicTool 委托 DynamicExecutor + 删除 BuiltInToolLoader

**`DynamicTool` 改造：**

```go
// internal/tool/dynamic.go
type DynamicTool struct {
    name, namespace, version, description string
    parameters map[string]any
    toolType   DynamicToolType
    descriptor ToolDescriptor  // 完整 descriptor，含 ExecutionConfig
    executor   *DynamicExecutor  // 委托执行体
}

func NewDynamicTool(name, description string, parameters map[string]any, toolType DynamicToolType) *DynamicTool {
    desc := ToolDescriptor{Name: name, Description: description, Parameters: parameters, Source: ToolSourceLocalDB,
        ExecutionConfig: map[string]any{"type": string(toolType)}}
    return &DynamicTool{name: name, description: description, parameters: parameters,
        toolType: toolType, descriptor: desc, executor: NewDynamicExecutor(desc)}
}

func (t *DynamicTool) Execute(input map[string]any) (any, error) {
    return t.executor.Execute(ExecuteContext{}, input)  // 委托
}
```

`SetCommand/SetHTTP/SetCode` 同步更新 `descriptor.ExecutionConfig` 与 `executor.desc`（或重建 executor）。`Command()/URL()/Method()/Code()` 从 `descriptor.ExecutionConfig` 读。`executeShell/executeHTTP/executeInline` 三个私有方法从 `dynamic.go` 删除（逻辑已在 `executor.go`）。

**`BuiltInToolLoader` 删除**：移除 `loader.go` 中的 `BuiltInToolLoader` 类型与 `NewBuiltInToolLoader`。`ToolLoader` 接口与 `DBToolLoader`/`RecordLoader` 保留（P0 用上）。

### 3.3 P1 — recovery 接入 + Workdir 注入

**P1a — `AgentRunner.Recover`：**

```go
// cmd/server/runner.go
type RecoverSpec struct {
    TaskID string
}

func (r *AgentRunner) Recover(ctx context.Context, spec RecoverSpec) {
    cm := r.Deps.CheckpointMgr
    cp, err := cm.Load(spec.TaskID)
    if err != nil { /* task_failed 事件 */ return }

    contract := harness.DefaultContract("resume")
    contract.MaxSteps = cp.StepIdx + 10

    // 补齐 8-A 缺失的 EngineConfig 字段：与 runAgentLoopWithTurn 对齐
    provider, _ := llm.CreateProviderFromConfig(r.Deps.Cfg, r.Deps.Cfg.LLMModel, "")
    cfg_ := runtime.EngineConfig{
        AgentID: cp.AgentID, SystemPrompt: "You are recovering...", Model: r.Deps.Cfg.LLMModel,
        Endpoint: r.Deps.Cfg.LLMEndpoint, APIKey: r.Deps.Cfg.LLMAPIKey, Provider: provider,
        MaxTokens: 4096, MaxSteps: contract.MaxSteps, Persistence: r.Deps.Persist,
        Contract: contract, ApprovalHandler: r.Deps.ApprovalHandler,
        AgentBus: r.Deps.AgentBus, CheckpointManager: cm,
        Router: r.Deps.ModelRouter, Registry: r.Deps.ModelRegistry, Providers: r.Deps.RouterProviders,
        // 8-A 缺失、8-B 补齐：
        SkillRegistry: r.Deps.SkillRegistry, ActiveSkills: GetEnabledSkillIDs(r.Deps.SkillRegistry),
        SessionMessageWriter: /* 与 runAgentLoopWithTurn 一致的 db.InsertSessionMessage 闭包 */,
        WorkspaceDir: /* 从 session 反查 */,
        Tracer: r.Deps.Tracer, RootTraceCtx: r.Deps.Tracer.StartRoot(spec.TaskID, "recover"),
        OnLLMUsage: /* 与 runAgentLoopWithTurn 一致的 cost 回调 */,
        ActiveTodos: /* 从 todoSvc 加载 */,
    }
    engine := runtime.RecoverFromCheckpoint(cp, cfg_, r.Deps.Tools, &hubAdapter{hub: r.Hub}, spec.TaskID)
    // ...task_started(recovered) 事件 + goroutine 运行 engine.Run(ctx, "") + 成功后 cm.Delete...
}
```

`handleRecoverCheckpoint` 改为 `(s *appServer) handleRecoverCheckpoint(w, r)`：解析 `task_id` → `s.newRunner().Recover(ctx, RecoverSpec{TaskID: req.TaskID})` → 返回 `{status: "recovering"}`。

**P1b — Workdir 注入（不破 `Tool.Execute(input)` 签名）：**

Engine 在 `executeToolCall` 调 `e.tools.Execute(name, args)` 之前已注入 `args["workdir"] = e.cfg.WorkspaceDir`（既有逻辑，engine.go:1803）。8-B 在此基础上让 `BuiltinTool` 与 `DynamicTool` 的执行体优先读 `ExecuteContext.Workdir`：

- 方案：给 `Registry.Execute` 增加一个内部桥接——Engine 调用时传入 `ExecuteContext{Workdir: e.cfg.WorkspaceDir}`，Registry 把它透传给 `BuiltinTool.executor(ctx, input)` 与 `DynamicTool.executor.Execute(ctx, input)`。
- 具体落地：新增 `Registry.ExecuteWithCtx(name string, ctx ExecuteContext, input map[string]any)` 方法；`Tool.Execute(input)` 保持不变（内部默认 `ExecuteContext{}`）。Engine 改调 `ExecuteWithCtx`。`run_shell` 的 `executeShell` 改为：`workdir` 优先取 `input["workdir"]`，为空时取 `ctx.Workdir`，再为空取既有默认。`write_file`/`read_file` 的路径解析同理用 `ctx.Workdir` 作为 base。
- 这样 `Tool` 接口签名零变更（向后兼容既有调用方与测试），Workdir 通过 Engine→Registry→Executor 路径注入，`DynamicExecutor.executeShell` 已有 `if ctx.Workdir != "" { cmd.Dir = ctx.Workdir }`（executor.go:73），天然承接。

### 3.4 B1/B2 — handler 方法化 + appServer 字段分组

**`appServer` 字段分组（按子系统，组间空行 + 注释）：**

```go
// appServer 聚合 cmd/server 全部依赖。字段按子系统分组，组间空行隔离，
// 每组注释说明对应子系统。配置型单例也在此持有；运行期并发协调 Map
// （cancelRegistry/engineRegistry/traceRegistry）保留包级，不进聚合体。
type appServer struct {
    // —— 基础设施 ——
    cfg    *config.Config
    hub    *ws.Hub
    persist runtime.Persistence

    // —— LLM 路由 ——
    modelRegistry    *llm.ModelRegistry
    modelRouter      *llm.Router
    routerProviders  map[string]llm.Provider
    routerClassifier llm.Provider

    // —— Tool 子系统 ——
    toolRegistry *tool.Registry

    // —— Skill 子系统 ——
    skillRegistry *skill.Registry
    skillStore    *skill.Store

    // —— Cron 子系统 ——
    cronService *cron.Service

    // —— Memory / Checkpoint ——
    memDB         *harness.SqliteMemoryDB
    memRecall     *harness.MemoryRecall
    checkpointMgr *runtime.CheckpointManager
    vectorStore   memory.VectorStore
    embedProvider llm.EmbeddingProvider

    // —— Cost ——
    costRepo cost.CostRepository

    // —— Orchestrator / Tracer ——
    orch  *orchestrator.Orchestrator
    tracer *observability.Tracer

    // —— Todo / Case / Mock ——
    todoSvc     *todo.Service
    caseService *cases.Service
    mockStore   llm.MockScriptStore

    // —— Auth ——
    authAPI   *auth.AuthAPI
    authStore auth.APIKeyStore

    // —— MCP ——
    mcpManager *mcp.Manager

    // —— 运行入口 ——
    runner *AgentRunner
}
```

`globalSkillRegistry`/`globalOrchestrator`/`globalCronService` 进 `appServer` 字段（`skillRegistry`/`orch`/`cronService`）；`tracer` 进 `appServer.tracer`（保留包级 `tracer` 变量供 `init()` 回调注册，`appServer.tracer` 引用同一实例）。`makeRunnerDeps` 改为 `(s *appServer) deps()` 的内联实现（已有 `deps()` 方法，删 `makeRunnerDeps` 包级函数，调用方改用 `s.deps()` 或 `s.newRunner()`）。

**`registerRoutes` 瘦身：**

```go
func (s *appServer) registerRoutes() {
    mux := http.DefaultServeMux  // 或引入 s.mux 字段
    mux.HandleFunc("/ws", ws.ServeWS(s.hub))
    mux.HandleFunc("/api/tasks", s.handleTasksRoot)
    mux.HandleFunc("/api/tasks/", s.handleTasksSub)  // 子资源路由独立方法
    mux.HandleFunc("/api/sessions", s.handleSessions)
    mux.HandleFunc("/api/sessions/", s.handleSessionSub)  // 含 /chat 子资源
    mux.HandleFunc("/api/tools", s.handleTools)  // method 分发内部走 Register 模式
    mux.HandleFunc("/api/checkpoints", s.handleCheckpoints)
    mux.HandleFunc("/api/checkpoints/recover", s.handleRecoverCheckpoint)
    mux.HandleFunc("/api/run-case", s.handleRunCase)
    mux.HandleFunc("/api/multi-agent", s.handleMultiAgent)
    // ...可观测性 / memory / agents / replay...
    s.registerSkillRoutes()
    s.registerTodoRoutes()
    s.registerCronRoutes()
    s.registerMCPRoutes()
    s.registerMockRoutes()
    s.registerModelPriceRoutes()
}
```

**`switch req.Action` 改 Register 模式（`handleTasksRoot`）：**

```go
// cmd/server/tasks_api.go（新文件，从 main.go 拆出 handleTasksRoot 闭包）
type taskActionHandler func(s *appServer, w http.ResponseWriter, r *http.Request, req taskRequest)

var taskActionRegistry = map[string]taskActionHandler{
    "chat":        (*appServer).actionChat,
    "multi-agent": (*appServer).actionMultiAgent,
    "stream-demo": (*appServer).actionStreamDemo,
}

func (s *appServer) handleTasksRoot(w http.ResponseWriter, r *http.Request) {
    // ...解析 req + case 继承 + contract 校验（共用前置）...
    handler, ok := taskActionRegistry[req.Action]
    if !ok { http.Error(w, "unknown action", http.StatusBadRequest); return }
    handler(s, w, r, req)
}

func (s *appServer) actionChat(w http.ResponseWriter, r *http.Request, req taskRequest) {
    // chat action 逻辑，调 s.startChatTask(...)
}
func (s *appServer) actionMultiAgent(...) { /* leader 启动 */ }
func (s *appServer) actionStreamDemo(...) { /* streamTask */ }
```

同理把 `/api/tasks/` 子资源 switch（`context_window`/`agent-messages`/单任务详情）拆为子资源 handler 注册表；`/api/tools` 的 method switch 拆为 method handler 注册表；`/api/sessions` method switch 同理。**目标：单个 handler 函数体积显著下降，分支逻辑拆到独立方法/文件，文件组织更轻松。**

### 3.5 文件组织

8-B 后 `cmd/server` 文件布局：

```
cmd/server/
  main.go              # main() + 子系统初始化 + 构造 appServer + 启动（瘦身，闭包移出后 ~1300 行）
  server.go            # appServer struct + deps() + newRunner() + registerRoutes()
  runner.go            # AgentRunSpec/AgentDeps/AgentRunner + Recover + 辅助
  api.go               # 通用 handler 方法（tasks/sessions/agents/memory/audit/traces/replay）
  tasks_api.go         # handleTasksRoot + action handler 注册表 + actionChat/actionMultiAgent/actionStreamDemo
  tools_api.go         # handleRegisterTool/handleListTools/handleDeleteTool（从 tool_api.go 改名）
  checkpoint_api.go    # handleRecoverCheckpoint/handleListCheckpoints（从 main.go 拆出）
  cron_api.go          # cron REST + cronTaskStarter 适配器
  api_skill.go         # registerSkillRoutes + skill handler 方法
  api_todo.go          # registerTodoRoutes
  mcp_api.go / mcp_market_api.go / mock_api.go / model_price_api.go  # 子路由注册方法化
```

---

## 4. 兼容性与风险

| 维度 | 处理 |
|------|------|
| `Tool.Execute(input)` 签名 | **不变**。Workdir 通过 `Registry.ExecuteWithCtx` 内部注入，既有调用方与测试零改动。 |
| `/api/tools` API 响应 | 字段不变。底层从 `InsertTool` 换 `InsertToolV2` 但响应仍返回 name/description/parameters/type/command/url/method/code。 |
| v27 表既有数据 | 启动期 `QueryToolsV2` 加载所有 `source=local_db` 记录。8-A 之前用旧 `InsertTool` 写的记录 `execution_config_json='{}'`，加载后 `DynamicTool` 的 type 为空 → `DynamicExecutor.Execute` 返回 "unknown dynamic tool type" 错误。**风险**：旧动态工具加载后不可用。**缓解**：加载时跳过 `execution_config_json` 为空或 type 缺失的记录并打日志警告（让用户重新注册）。 |
| `RecoverFromCheckpoint` 行为 | 不变。8-B 只补 EngineConfig 字段，不改 `runtime.RecoverFromCheckpoint` 签名。 |
| `makeRunnerDeps` 删除 | 调用方（main.go/api.go 既有 `NewAgentRunner(hub, makeRunnerDeps(...))`）改用 `s.newRunner()`。 |
| 闭包退场顺序 | `startChatTask` 改方法后，cron `ActionRunner` 构造依赖 `s.startChatTask`——`main()` 必须先构造 `appServer` 再构造 cron 子系统。调整初始化顺序：构造 `appServer` → 构造 cron（注入 `s.startChatTask`）→ `s.registerRoutes()`。 |
| 包级 var 保留 | `cancelRegistry`/`engineRegistry`/`traceRegistry`/`tracer`/`hubInstance` 保留包级；`handleTasksRoot`/`startChatTask`/`globalCronService` 删除（进 `appServer`）；`globalSkillRegistry`/`globalOrchestrator` 进 `appServer` 但保留包级变量供 `init()`/runner.go 既有引用过渡（或同步改引用）。 |

---

## 5. 测试策略

- **P0 回归测试**（新增 `cmd/server/tool_api_test.go` 或扩既有）：注册一个 shell 动态工具 → 重启（重新构造 registry + 从 DB 加载）→ 断言工具在 registry 中且 `Execute` 能跑 command。验证 `execution_config_json` 持久化。
- **B3 单测**：`DynamicTool.Execute` 委托 `DynamicExecutor` 后，shell/http/inline 三种类型行为与改造前一致（扩 `internal/tool/dynamic_test.go`）。
- **P1b 单测**：`Registry.ExecuteWithCtx` 传入 `ExecuteContext{Workdir: tmpDir}` → `run_shell` 的 `pwd` 输出 tmpDir；`write_file` 写相对路径落到 tmpDir。
- **既有测试全绿**：`go test ./...` 必须通过（含 `cmd/server` 集成测试、`internal/tool`、`pkg/db`）。handler 方法化后 `api_skill_test.go`/`cron_api_test.go` 等若直接调包级函数需同步改调 `appServer` 方法。
- **smoke-test**：`scripts/smoke-test.sh` 61 PASS/1 FAIL（既有 memory 400 vs 405，与 8-B 无关）保持不退化。
- **verification-before-completion**：完成前实跑 `go build ./...` + `go test ./...` + smoke-test，观察结果，不凭"应该没问题"收尾。

---

## 6. 任务拆分（供 writing-plans 细化）

1. **P0 写端**：`handleRegisterTool` 改 `InsertToolV2` + `handleDeleteTool` 改 `DeleteToolV2` + 冲突检查改 `CanonicalName`。
2. **P0 读端**：`main.go` 启动期 `DBToolLoader` 加载动态工具 + 跳过无 execution_config 的旧记录。
3. **B3**：`DynamicTool` 委托 `DynamicExecutor` + 删除 `executeShell/executeHTTP/executeInline` 私有方法 + 删除 `BuiltInToolLoader`。
4. **P1b**：`Registry.ExecuteWithCtx` + Engine 改调 + `run_shell/write_file/read_file` 读 `ctx.Workdir`。
5. **P1a**：`AgentRunner.Recover` + 补齐 EngineConfig 字段 + `handleRecoverCheckpoint` 方法化。
6. **B2 闭包退场**：`handleTasksRoot`/`startChatTask` 改 `appServer` 方法 + 删包级 var + `appServer` 持有 cron Service + 初始化顺序调整。
7. **B1 handler 方法化**：全部包级 handler 改 `appServer` 方法 + `registerRoutes` 改方法值注册 + `switch req.Action` 改 Register 模式 + 子路由注册函数方法化。
8. **appServer 字段分组**：按子系统重组 + 注释 + `makeRunnerDeps` 删除改 `s.deps()`。
9. **文档**：ROADMAP v0.13.0 + CLAUDE.md 扩展 Phase 表 + 项目结构更新。
10. **验证**：`go build` + `go test ./...` + smoke-test + code-review。

---

## 7. 验收标准

- [ ] 注册动态 shell 工具 → 重启服务 → 工具仍在 registry 且可执行（P0 回归修复）。
- [ ] `BuiltInToolLoader` 删除；`DynamicTool.Execute` 委托 `DynamicExecutor`；`executor.go` 的 `executeShell/executeHTTP/executeInline` 是唯一执行实现（B3）。
- [ ] `handleRecoverCheckpoint` 走 `AgentRunner.Recover`，恢复后 agent 有 skill 注入、session 消息写入、workspace（P1a）。
- [ ] `run_shell`/`write_file` 的 CWD 来自 `ExecuteContext.Workdir`（P1b）。
- [ ] `cmd/server` 无包级高元 handler（≥6 参的 `handleSessionChat`/`handleRunCase`/`handleRecoverCheckpoint` 等全部方法化）；`handleTasksRoot`/`startChatTask` 闭包与对应包级 var 删除（B1+B2）。
- [ ] `registerRoutes` 无 30+ 行透传样板；`switch req.Action` 改为注册表分发（B1）。
- [ ] `appServer` 字段按子系统分组 + 空行 + 注释（B2）。
- [ ] `go build ./...` + `go test ./...` 全绿；smoke-test 不退化。
- [ ] ROADMAP + CLAUDE.md 更新。
