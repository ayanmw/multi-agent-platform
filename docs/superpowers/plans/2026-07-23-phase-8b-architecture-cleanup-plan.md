# Phase 8-B: 架构收尾实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Phase 8-A 造好但未接线的抽象（AgentRunner / ToolDescriptor / Executor / Loader / v27 tools 表）真正接上，同时消除 HTTP handler 层的上帝函数与 main.go 闭包，确保动态工具持久化、执行上下文 Workdir、recovery 路径收口。

**Architecture:** 通过 `appServer` 聚合全部依赖，所有 handler 改为 `appServer` 方法；`switch req.Action` 改为 `taskActionRegistry` 注册表分发；`Tool` 接口保持 `Execute(input)` 签名，Registry 新增 `ExecuteWithCtx` 桥接；`DynamicTool` 委托 `DynamicExecutor`；`AgentRunner` 新增 `Recover` 入口。

**Tech Stack:** Go 1.25, gorilla/mux (当前 http.DefaultServeMux), modernc.org/sqlite, gorilla/websocket

---

## File Map

| 文件 | 新增/修改 | 职责 |
|------|----------|------|
| `internal/tool/dynamic.go` | 修改 | `DynamicTool` 委托 `DynamicExecutor`，删除私有 executeXXX 方法 |
| `internal/tool/executor.go` | 修改 | 保留 `DynamicExecutor.executeShell/executeHTTP/executeInline`，承接 Workdir |
| `internal/tool/loader.go` | 修改 | 删除 `BuiltInToolLoader`，保留 `ToolLoader/DBToolLoader/RecordLoader` |
| `internal/tool/registry.go` | 修改 | 新增 `ExecuteWithCtx` 方法 |
| `internal/tool/builtin.go` | 修改 | `run_shell/write_file/read_file` 改读 `ExecuteContext.Workdir` |
| `internal/runtime/engine.go` | 修改 | 改调 `tools.ExecuteWithCtx` |
| `cmd/server/runner.go` | 修改 | 新增 `RecoverSpec/AgentRunner.Recover`；补齐 EngineConfig 字段 |
| `cmd/server/server.go` | 修改 | `appServer` 字段按子系统分组；`registerRoutes` 改方法值；`deps()` 替代 `makeRunnerDeps` |
| `cmd/server/main.go` | 修改 | 启动期加载 DB 动态工具；闭包退场；appServer 构造与 cron 初始化顺序调整；瘦身 |
| `cmd/server/api.go` | 修改 | 包级 handler 改为 `appServer` 方法 |
| `cmd/server/tool_api.go` | 修改 | `handleRegisterTool/handleDeleteTool` 改 `InsertToolV2/DeleteToolV2`，handler 方法化 |
| `cmd/server/checkpoint_api.go` | 新增 | `handleRecoverCheckpoint/handleListCheckpoints` 从 main.go 迁出，方法化 |
| `cmd/server/tasks_api.go` | 新增 | `handleTasksRoot` + `taskActionRegistry` + `actionChat/actionMultiAgent/actionStreamDemo` |
| `cmd/server/cron_api.go` | 修改 | cron REST 方法化；`cronTaskStarter` 适配器改捕获 `s` |
| `cmd/server/api_skill.go` | 修改 | `registerSkillRoutes` + handler 方法化 |
| `cmd/server/api_todo.go` | 修改（已有或拆分） | `registerTodoRoutes` 方法化 |
| `cmd/server/mcp_api.go` | 修改 | `registerMCPRoutes` 方法化 |
| `cmd/server/mock_api.go` | 修改 | `registerMockRoutes` 方法化 |
| `cmd/server/model_price_api.go` | 修改 | `registerModelPriceRoutes` 方法化 |
| `internal/cron/action.go` | 修改 | cron ActionRunner 接收 `TaskStarter` 为函数值，无需持有 `appServer` |
| `pkg/db/tool.go` | 不修改 | 已提供 `InsertToolV2/DeleteToolV2/QueryToolsV2/GetToolV2` |
| `roadmaps/ROADMAP.md` | 修改 | 新增 v0.13.0 版本记录 |
| `CLAUDE.md` | 修改 | 扩展 Phase 表加 8-B，更新项目结构 |

---

## Task 1: P0 写端 — `handleRegisterTool`/`handleDeleteTool` 持久化改 V2

**Files:**
- Modify: `cmd/server/tool_api.go:1-261`
- Test: `cmd/server/tool_api_test.go`（若不存在则新增；spec 未要求新增，先用 `pkg/db/tool_test.go` 风格）

- [ ] **Step 1: 用 `db.InsertToolV2` 替换 `db.InsertTool`，构造 `ExecutionConfig`**

在 `cmd/server/tool_api.go` 的 `handleRegisterTool` 中，把原来调用 `db.InsertTool(req.Name, req.Description, req.Parameters, true)` 的位置改写成：

```go
execConfig := map[string]any{"type": req.Type}
switch req.Type {
case "shell":
    execConfig["command"] = req.Command
case "http":
    execConfig["url"] = req.URL
    execConfig["method"] = req.Method
case "inline":
    execConfig["code"] = req.Code
}

td := tool.ToolDescriptor{
    Name:            req.Name,
    Description:     req.Description,
    Parameters:      req.Parameters,
    Source:          tool.ToolSourceLocalDB,
    ExecutionConfig: execConfig,
}
dt := tool.NewDynamicToolFromDescriptor(td)

// 冲突检查改为按 CanonicalName
if _, exists := s.toolRegistry.Get(dt.CanonicalName()); exists {
    http.Error(w, fmt.Sprintf("tool %q already registered", dt.CanonicalName()), http.StatusConflict)
    return
}

if err := db.InsertToolV2(db.ToolRecord{
    Name:            req.Name,
    Description:     req.Description,
    Schema:          req.Parameters,
    Source:          "local_db",
    Enabled:         true,
    ExecutionConfig: execConfig,
}); err != nil {
    http.Error(w, fmt.Sprintf("insert tool: %v", err), http.StatusInternalServerError)
    return
}
s.toolRegistry.Register(dt)
```

- [ ] **Step 2: 冲突检查从 Name 改为 CanonicalName，支持多版本并存**

删除 `toolRegistry.List()` 遍历比较 `t.Name()` 的旧逻辑，改为 `s.toolRegistry.Get(dt.CanonicalName())`。已在 Step 1 完成。

- [ ] **Step 3: `handleDeleteTool` 改 `db.DeleteToolV2(namespace, name, version)`**

在 `handleDeleteTool` 中，把 `db.DeleteTool(name)` 改成：

```go
ns := r.URL.Query().Get("namespace")
version := r.URL.Query().Get("version")
if version == "" {
    version = "1.0.0"
}
if err := db.DeleteToolV2(ns, name, version); err != nil {
    http.Error(w, fmt.Sprintf("delete tool: %v", err), http.StatusInternalServerError)
    return
}
key := name
if ns != "" && version != "" {
    key = fmt.Sprintf("%s/%s@%s", ns, name, version)
} else if ns != "" {
    key = fmt.Sprintf("%s/%s", ns, name)
} else if version != "" {
    key = fmt.Sprintf("%s@%s", name, version)
}
if err := s.toolRegistry.Unregister(key); err != nil {
    http.Error(w, fmt.Sprintf("unregister tool: %v", err), http.StatusInternalServerError)
    return
}
```

- [ ] **Step 4: Commit**

```bash
git add cmd/server/tool_api.go
git commit -m "Phase 8-B P0: dynamic tool persistence writes execution_config_json"
```

---

## Task 2: P0 读端 — 启动期加载 DB 动态工具

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 在 main.go 中找到 `tool.RegisterBuiltins(toolRegistry)` 调用点**

- [ ] **Step 2: 在其后追加 DBToolLoader 加载逻辑**

```go
if db.DB != nil {
    loader := tool.NewDBToolLoader(func() ([]map[string]any, error) {
        records, err := db.QueryToolsV2()
        if err != nil {
            return nil, err
        }
        maps := make([]map[string]any, 0, len(records))
        for _, tr := range records {
            // 跳过无执行配置的旧记录，避免加载后 type 为空报错
            if tr.ExecutionConfig == nil {
                continue
            }
            if t, _ := tr.ExecutionConfig["type"].(string); t == "" {
                continue
            }
            maps = append(maps, map[string]any{
                "namespace":        tr.Namespace,
                "name":             tr.Name,
                "version":          tr.Version,
                "source":           tr.Source,
                "description":      tr.Description,
                "parameters":       tr.Schema,
                "execution_config": tr.ExecutionConfig,
            })
        }
        return maps, nil
    })
    loaded, err := loader.Load(context.Background())
    if err != nil {
        log.Printf("[tool] failed to load dynamic tools: %v", err)
    } else {
        registered := 0
        for _, t := range loaded {
            if t.Source() == "local_db" {
                toolRegistry.Register(t)
                registered++
            }
        }
        log.Printf("[tool] loaded %d dynamic tool(s) from DB", registered)
    }
}
```

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "Phase 8-B P0: load dynamic tools from DB on startup"
```

---

## Task 3: B3 — `DynamicTool` 委托 `DynamicExecutor` + 删除 `BuiltInToolLoader`

**Files:**
- Modify: `internal/tool/dynamic.go`
- Modify: `internal/tool/loader.go`
- Test: `internal/tool/dynamic_test.go`（扩充分支断言）

- [ ] **Step 1: 修改 `DynamicTool` 结构体，移除执行相关字段，增加 executor 字段**

把 `internal/tool/dynamic.go` 中的：

```go
type DynamicTool struct {
    name, description string
    parameters        map[string]any
    toolType          DynamicToolType
    command           string
    url, method       string
    code              string
}
```

改为：

```go
type DynamicTool struct {
    name, namespace, version, description string
    parameters                            map[string]any
    toolType                              DynamicToolType
    descriptor                            ToolDescriptor
    executor                              *DynamicExecutor
}
```

- [ ] **Step 2: 修改 `NewDynamicTool` 构造器，用 descriptor + executor 初始化**

原：

```go
func NewDynamicTool(name, description string, parameters map[string]any, toolType DynamicToolType) *DynamicTool {
    return &DynamicTool{name: name, description: description, parameters: parameters, toolType: toolType}
}
```

改为：

```go
func NewDynamicTool(name, description string, parameters map[string]any, toolType DynamicToolType) *DynamicTool {
    desc := ToolDescriptor{
        Name:            name,
        Description:     description,
        Parameters:      parameters,
        Source:          ToolSourceLocalDB,
        ExecutionConfig: map[string]any{"type": string(toolType)},
    }
    return &DynamicTool{
        name:        name,
        description: description,
        parameters:  parameters,
        toolType:    toolType,
        descriptor:  desc,
        executor:    NewDynamicExecutor(desc),
    }
}
```

- [ ] **Step 3: `Execute` 委托 executor**

原 `Execute` 中的 switch 删除，改为：

```go
func (t *DynamicTool) Execute(input map[string]any) (any, error) {
    return t.executor.Execute(ExecuteContext{}, input)
}
```

- [ ] **Step 4: `SetCommand/SetHTTP/SetCode` 同步更新 descriptor 与 executor**

原 `SetCommand` 直接写 `t.command`，改为：

```go
func (t *DynamicTool) SetCommand(command string) {
    t.descriptor.ExecutionConfig["command"] = command
    t.executor = NewDynamicExecutor(t.descriptor)
}

func (t *DynamicTool) SetHTTP(url, method string) {
    t.descriptor.ExecutionConfig["url"] = url
    t.descriptor.ExecutionConfig["method"] = method
    t.executor = NewDynamicExecutor(t.descriptor)
}

func (t *DynamicTool) SetCode(code string) {
    t.descriptor.ExecutionConfig["code"] = code
    t.executor = NewDynamicExecutor(t.descriptor)
}
```

- [ ] **Step 5: `Command/URL/Method/Code` 从 descriptor 读**

```go
func (t *DynamicTool) Command() string {
    s, _ := t.descriptor.ExecutionConfig["command"].(string)
    return s
}
func (t *DynamicTool) URL() string {
    s, _ := t.descriptor.ExecutionConfig["url"].(string)
    return s
}
func (t *DynamicTool) Method() string {
    s, _ := t.descriptor.ExecutionConfig["method"].(string)
    return s
}
func (t *DynamicTool) Code() string {
    s, _ := t.descriptor.ExecutionConfig["code"].(string)
    return s
}
```

- [ ] **Step 6: 删除 `dynamic.go` 中 `executeShell/executeHTTP/executeInline` 三个私有方法**

从文件末尾删除这三个函数。

- [ ] **Step 7: 删除 `BuiltInToolLoader`**

在 `internal/tool/loader.go` 中删除：

```go
type BuiltInToolLoader struct{}
func NewBuiltInToolLoader() *BuiltInToolLoader { ... }
func (l *BuiltInToolLoader) Load(...) ([]Tool, error) { return nil, nil }
```

保留 `ToolLoader` 接口、`RecordLoader`、`DBToolLoader`。

- [ ] **Step 8: 运行 `internal/tool` 测试，确保无回归**

```bash
cd D:/Claude-Code-MultiAgent/.claude/worktrees/phase-8b-arch-cleanup
go test ./internal/tool/... -v
```

- [ ] **Step 9: Commit**

```bash
git add internal/tool/dynamic.go internal/tool/loader.go
git commit -m "Phase 8-B B3: DynamicTool delegates DynamicExecutor; remove BuiltInToolLoader"
```

---

## Task 4: P1b — `Registry.ExecuteWithCtx` + 内置工具读 Workdir

**Files:**
- Modify: `internal/tool/registry.go`
- Modify: `internal/tool/builtin.go`
- Modify: `internal/runtime/engine.go`
- Test: `internal/tool/registry_test.go` 或扩 `internal/tool/executor_test.go`

- [ ] **Step 1: `Registry` 新增 `ExecuteWithCtx` 方法**

在 `registry.go` 中 `Execute` 下方添加：

```go
// ExecuteWithCtx 与 Execute 类似，但支持传入执行上下文。
// Engine 用此方法把 WorkspaceDir 注入到工具执行体，而 Tool 公开接口签名保持不变。
func (r *Registry) ExecuteWithCtx(name string, ctx ExecuteContext, input map[string]any) (any, error) {
    r.mu.RLock()
    tool, ok := r.tools[name]
    if !ok {
        var ambiguous bool
        tool, ambiguous = r.getByFullNameLocked(name)
        if ambiguous {
            r.mu.RUnlock()
            return nil, fmt.Errorf("tool name %q is ambiguous; use canonical name namespace/name@version", name)
        }
    }
    r.mu.RUnlock()
    if tool == nil {
        return nil, fmt.Errorf("tool not found: %s", name)
    }
    // 优先使用执行体上的 Execute(ctx, input)；如果工具未实现，则回退到无 ctx 的 Execute(input)
    if te, ok := tool.(interface{ ExecuteCtx(ExecuteContext, map[string]any) (any, error) }); ok {
        return te.ExecuteCtx(ctx, input)
    }
    return tool.Execute(input)
}
```

> 注意：更干净的实现是让所有 Tool 实现内部持有 `BuiltinExecutor/DynamicExecutor`，其 `Execute(input)` 直接调 `executor.Execute(ExecuteContext{}, input)`；Engine 调用 `ExecuteWithCtx` 时直接调用内部 executor。为最小化改动，这里先用 interface 判定 + 回退。

替代干净方案（推荐）：把 `BuiltinTool` 的 `Execute(input)` 改为：

```go
func (t *BuiltinTool) Execute(input map[string]any) (any, error) {
    return t.executor.Execute(ExecuteContext{}, input)
}
```

并新增未导出方法：

```go
func (t *BuiltinTool) executeWithCtx(ctx ExecuteContext, input map[string]any) (any, error) {
    return t.executor.Execute(ctx, input)
}
```

然后在 `Registry.ExecuteWithCtx` 中用类型断言 `*BuiltinTool` 和 `*DynamicTool` 调用各自的上下文执行入口。下面按推荐方案执行。

- [ ] **Step 2: `BuiltinTool` 增加上下文执行能力**

在 `internal/tool/builtin.go` 中，假设 `BuiltinTool` 已有 `executor *BuiltinExecutor` 字段：

```go
// executeWithCtx 供 Registry 注入 ExecuteContext 时调用。
func (t *BuiltinTool) executeWithCtx(ctx ExecuteContext, input map[string]any) (any, error) {
    return t.executor.Execute(ctx, input)
}

func (t *BuiltinTool) Execute(input map[string]any) (any, error) {
    return t.executeWithCtx(ExecuteContext{}, input)
}
```

> 如果 `BuiltinTool` 还没有 `executor` 字段，需要在该文件中把三个内置工具的工厂函数改成返回 `&BuiltinTool{name: ..., executor: &BuiltinExecutor{Fn: func(ctx ExecuteContext, input map[string]any) (any, error) { ... }}}`。

- [ ] **Step 3: `DynamicTool` 增加未导出 `executeWithCtx` 入口**

在 `internal/tool/dynamic.go` 中新增：

```go
func (t *DynamicTool) executeWithCtx(ctx ExecuteContext, input map[string]any) (any, error) {
    return t.executor.Execute(ctx, input)
}
```

- [ ] **Step 4: 修改 `Registry.ExecuteWithCtx` 用类型断言分派**

```go
func (r *Registry) ExecuteWithCtx(name string, ctx ExecuteContext, input map[string]any) (any, error) {
    r.mu.RLock()
    tt, ok := r.tools[name]
    if !ok {
        var ambiguous bool
        tt, ambiguous = r.getByFullNameLocked(name)
        if ambiguous {
            r.mu.RUnlock()
            return nil, fmt.Errorf("tool name %q is ambiguous; use canonical name namespace/name@version", name)
        }
    }
    r.mu.RUnlock()
    if tt == nil {
        return nil, fmt.Errorf("tool not found: %s", name)
    }
    switch t := tt.(type) {
    case *BuiltinTool:
        return t.executeWithCtx(ctx, input)
    case *DynamicTool:
        return t.executeWithCtx(ctx, input)
    default:
        return t.Execute(input)
    }
}
```

- [ ] **Step 5: `run_shell/write_file/read_file` 改读 `ctx.Workdir`**

在 `internal/tool/builtin.go` 中，把 `runShellFn` 里解析 `workdir` 的逻辑改为：

```go
workdir, _ := input["workdir"].(string)
if workdir == "" {
    workdir = ctx.Workdir
}
```

`write_file/read_file` 的 base dir 解析同样处理。

- [ ] **Step 6: Engine 改调 `ExecuteWithCtx`**

在 `internal/runtime/engine.go` 的 `executeToolCall` 中，把：

```go
res, err := e.tools.Execute(tc.Function.Name, args)
```

改为：

```go
ctx := tool.ExecuteContext{}
if e.cfg.WorkspaceDir != "" {
    ctx.Workdir = e.cfg.WorkspaceDir
}
res, err := e.tools.ExecuteWithCtx(tc.Function.Name, ctx, args)
```

保留既有的 `args["workdir"] = e.cfg.WorkspaceDir` 作为兼容 fallback（或直接用它即可）。

- [ ] **Step 7: 运行测试**

```bash
go test ./internal/tool/... ./internal/runtime/... -v
```

- [ ] **Step 8: Commit**

```bash
git add internal/tool/registry.go internal/tool/builtin.go internal/runtime/engine.go
git commit -m "Phase 8-B P1b: ExecuteWithCtx bridge injects Workdir to tools"
```

---

## Task 5: P1a — `AgentRunner.Recover` + `handleRecoverCheckpoint` 方法化

**Files:**
- Modify: `cmd/server/runner.go`
- Create: `cmd/server/checkpoint_api.go`
- Modify: `cmd/server/server.go`
- Modify: `cmd/server/api.go`（若其已包含 handleRecoverCheckpoint，则迁移到新文件）

- [ ] **Step 1: 在 `runner.go` 定义 `RecoverSpec`**

```go
// RecoverSpec 描述一次 checkpoint 恢复请求。
type RecoverSpec struct {
    TaskID string
}
```

- [ ] **Step 2: 实现 `(r *AgentRunner) Recover(ctx, spec)`**

```go
func (r *AgentRunner) Recover(ctx context.Context, spec RecoverSpec) (string, error) {
    cm := r.Deps.CheckpointMgr
    if cm == nil {
        return "", fmt.Errorf("checkpoint manager not available")
    }
    cp, err := cm.Load(spec.TaskID)
    if err != nil {
        return "", fmt.Errorf("load checkpoint: %w", err)
    }

    contract := harness.DefaultContract("resume")
    contract.MaxSteps = cp.StepIdx + 10

    model := r.Deps.Cfg.LLMModel
    provider, _ := llm.CreateProviderFromConfig(r.Deps.Cfg, model, "")

    cfg_ := runtime.EngineConfig{
        AgentID:           cp.AgentID,
        SystemPrompt:      "You are recovering from a checkpoint. Continue the task.",
        Model:             model,
        Endpoint:          r.Deps.Cfg.LLMEndpoint,
        APIKey:            r.Deps.Cfg.LLMAPIKey,
        Provider:          provider,
        MaxTokens:         4096,
        MaxSteps:          contract.MaxSteps,
        Persistence:       r.Deps.Persist,
        Contract:          contract,
        ApprovalHandler:   r.Deps.ApprovalHandler,
        AgentBus:          r.Deps.AgentBus,
        CheckpointManager: cm,
        Router:            r.Deps.ModelRouter,
        Registry:          r.Deps.ModelRegistry,
        Providers:         r.Deps.RouterProviders,
        SkillRegistry:     r.Deps.SkillRegistry,
        ActiveSkills:      GetEnabledSkillIDs(r.Deps.SkillRegistry),
        SessionMessageWriter: r.Deps.SessionMessageWriter,
        WorkspaceDir:      cp.WorkspaceDir,
        Tracer:            r.Deps.Tracer,
        RootTraceCtx:      r.Deps.Tracer.StartRoot(spec.TaskID, "recover"),
        OnLLMUsage:        r.Deps.CostRepo.OnLLMUsage,
        ActiveTodos:       r.Deps.ActiveTodos,
    }

    engine := runtime.RecoverFromCheckpoint(cp, cfg_, r.Deps.Tools, &hubAdapter{hub: r.Hub}, spec.TaskID)
    // 可选：删除 checkpoint，行为与之前保持一致
    if err := cm.Delete(spec.TaskID); err != nil {
        log.Printf("[recover] failed to delete checkpoint %s: %v", spec.TaskID, err)
    }

    go func() {
        engine.Run(context.Background(), "")
    }()
    return cp.AgentID, nil
}
```

> 注意：需要确认 `CheckpointManager.Load` 返回的 `Checkpoint` 是否有 `WorkspaceDir` 字段；若无则改为从请求或 session 反查。`GetEnabledSkillIDs` 需要在 `runner.go` 中已有或新增 helper。`CostRepo.OnLLMUsage` 的签名需与 `AgentDeps.CostRepo` 实际类型一致。

- [ ] **Step 3: 把 `handleRecoverCheckpoint` 从 main.go 迁到 `checkpoint_api.go`，并改为 `appServer` 方法**

```go
// cmd/server/checkpoint_api.go
package main

import (
    "encoding/json"
    "net/http"
)

type recoverRequest struct {
    TaskID string `json:"task_id"`
}

func (s *appServer) handleRecoverCheckpoint(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    var req recoverRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid json", http.StatusBadRequest)
        return
    }
    if req.TaskID == "" {
        http.Error(w, "task_id required", http.StatusBadRequest)
        return
    }
    agentID, err := s.newRunner().Recover(r.Context(), RecoverSpec{TaskID: req.TaskID})
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]any{
        "status":   "recovering",
        "task_id":  req.TaskID,
        "agent_id": agentID,
    })
}
```

- [ ] **Step 4: 在 `server.go` 的 `registerRoutes` 中把 recovery handler 改为方法值**

```go
mux.HandleFunc("/api/checkpoints/recover", s.handleRecoverCheckpoint)
```

- [ ] **Step 5: Commit**

```bash
git add cmd/server/runner.go cmd/server/checkpoint_api.go cmd/server/server.go
git commit -m "Phase 8-B P1a: AgentRunner.Recover + checkpoint handler methodized"
```

---

## Task 6: B2 — 闭包退场 + `appServer` 持有 cron Service + 字段分组

**Files:**
- Modify: `cmd/server/main.go`
- Modify: `cmd/server/server.go`
- Modify: `cmd/server/runner.go`
- Modify: `cmd/server/cron_api.go`
- Modify: `cmd/server/api.go`
- Create: `cmd/server/tasks_api.go`

- [ ] **Step 1: 改造 `appServer` 字段分组**

在 `server.go` 中把 `appServer` 改为按子系统分组，并用空行隔离（见 spec §3.4）。核心变化：新增 `cronService *cron.Service`；`skillRegistry/orch/tracer` 等字段聚合到 struct。

- [ ] **Step 2: 删除 `handleTasksRoot` / `startChatTask` 包级 var，改为 `appServer` 方法并迁到 `tasks_api.go`**

`tasks_api.go`：

```go
package main

import (
    "encoding/json"
    "net/http"
)

type taskRequest struct {
    Action string `json:"action"`
    // ... 已有字段
}

type taskActionHandler func(s *appServer, w http.ResponseWriter, r *http.Request, req taskRequest)

var taskActionRegistry = map[string]taskActionHandler{
    "chat":        (*appServer).actionChat,
    "multi-agent": (*appServer).actionMultiAgent,
    "stream-demo": (*appServer).actionStreamDemo,
}

func (s *appServer) handleTasksRoot(w http.ResponseWriter, r *http.Request) { ... }
func (s *appServer) actionChat(w http.ResponseWriter, r *http.Request, req taskRequest) { ... }
func (s *appServer) actionMultiAgent(w http.ResponseWriter, r *http.Request, req taskRequest) { ... }
func (s *appServer) actionStreamDemo(w http.ResponseWriter, r *http.Request, req taskRequest) { ... }
```

同时删除 main.go 中的：

```go
var handleTasksRoot func(...)
```

- [ ] **Step 3: `startChatTask` 改为 `appServer` 方法**

把原来 main.go 中的闭包 `startChatTask` 改为：

```go
func (s *appServer) startChatTask(opts startChatTaskOpts) (string, error) { ... }
```

删除 cron_api.go 中的包级 `var startChatTask func(...)`。

- [ ] **Step 4: `cronTaskStarter` 适配器捕获 `*appServer`**

在 `cron_api.go` 中改为：

```go
type appCronStarter struct {
    s *appServer
}

func (cs *appCronStarter) Start(ctx context.Context, input string, sessionID string) error {
    return cs.s.startChatTask(startChatTaskOpts{
        SessionID: sessionID,
        Input:     "[cron:" + ctx.Value("cron_id").(string) + ":" + ctx.Value("cron_name").(string) + "] " + input,
    })
}
```

> `cron_id`/`cron_name` 的 actual key name 以 `internal/cron/action.go` 的 `CronContext` 为准；请查看 `cron.StartTask` 注入的 context 字段定义并替换。

- [ ] **Step 5: main.go 调整初始化顺序**

1. 先构造 `appServer`（不绑定 cronService）。
2. 创建 cron 子系统时把 `s.startChatTask` 注入 `ActionRunner`。
3. 把生成的 cron.Service 赋值给 `s.cronService`。
4. 再 `s.registerRoutes()`。

- [ ] **Step 6: `registerRoutes` 改为方法值注册，删除透传样板**

只能用 `http.DefaultServeMux` 时，不要引入 `s.mux` 字段也能改。核心是把：

```go
mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
    handleTasksRoot(w, r)
})
```

改为：

```go
mux.HandleFunc("/api/tasks", s.handleTasksRoot)
```

- [ ] **Step 7: 编译并修复引用**

```bash
go build ./cmd/server
```

逐条修正编译错误（`makeRunnerDeps` 删除后所有调用点改 `s.deps()` 或 `s.newRunner()`）。

- [ ] **Step 8: Commit**

```bash
git add cmd/server/main.go cmd/server/server.go cmd/server/runner.go cmd/server/cron_api.go cmd/server/api.go cmd/server/tasks_api.go
git commit -m "Phase 8-B B2: closures moved to appServer methods; appServer holds cron service"
```

---

## Task 7: B1 — 剩余 handler 全部方法化 + 路由注册函数方法化

**Files:**
- Modify: `cmd/server/api.go`
- Modify: `cmd/server/tool_api.go`
- Modify: `cmd/server/api_skill.go`
- Modify: 其他 `cmd/server/*_api.go`
- Modify: `cmd/server/server.go`

- [ ] **Step 1: 把所有包级 handler 签名 `func handleXxx(w, r)` 改为 `func (s *appServer) handleXxx(w, r)`**

具体包括 `api.go/api_skill.go/tool_api.go/cron_api.go` 中的：

- handleSessionChat → (s *appServer) handleSessionChat
- handleRunCase → (s *appServer) handleRunCase
- handleListCheckpoints → (s *appServer) handleListCheckpoints
- handleAgents / handleAgentByID
- handleMemoryByID / handleMemoryEmbed / handleMemoryStats / handleListMemories / handleCreateMemory
- handleAudit / handleTraces / handleReplay / handleReplayEvents
- handleContractLimits
- handleSessions
- handleListTasks / handleGetTask / handleGetTaskContextWindow / handleGetAgentMessages
- handleListTools / handleDeleteTool
- skill handler 群
- cron handler 群

- [ ] **Step 2: 把这些 handler 内部的依赖引用从包级变量改为 `s.xxx`**

例如 `toolRegistry` → `s.toolRegistry`，`hubInstance` → `s.hub`，`cfg` → `s.cfg`。

- [ ] **Step 3: 子路由注册函数改为 `appServer` 方法**

```go
func (s *appServer) registerSkillRoutes() { ... }
func (s *appServer) registerTodoRoutes() { ... }
func (s *appServer) registerCronRoutes() { ... }
func (s *appServer) registerMCPRoutes() { ... }
// ...
```

- [ ] **Step 4: `registerRoutes` 中 method 分发也可改成注册表模式（可选但推荐）**

例如 `/api/tools` 的 `switch r.Method` 可改为：

```go
type toolsMethodHandler func(s *appServer, w http.ResponseWriter, r *http.Request)

var toolsMethodRegistry = map[string]toolsMethodHandler{
    http.MethodGet:    (*appServer).handleListTools,
    http.MethodPost:   (*appServer).handleRegisterTool,
    http.MethodDelete: (*appServer).handleDeleteTool,
}

func (s *appServer) handleTools(w http.ResponseWriter, r *http.Request) {
    h, ok := toolsMethodRegistry[r.Method]
    if !ok { http.Error(...); return }
    h(s, w, r)
}
```

- [ ] **Step 5: 编译修复**

```bash
go build ./cmd/server
```

- [ ] **Step 6: Commit**

```bash
git add cmd/server/*.go
git commit -m "Phase 8-B B1: all handlers methodized; routes use method values"
```

---

## Task 8: 测试与验证

**Files:**
- 运行：全仓库 `go test ./...`
- 运行：`scripts/smoke-test.sh`

- [ ] **Step 1: 运行单元测试**

```bash
cd D:/Claude-Code-MultiAgent/.claude/worktrees/phase-8b-arch-cleanup
go test ./... 2>&1 | tee /tmp/8b-test.log
```

- [ ] **Step 2: 分析失败用例**

按失败文件逐个修复。常见：
- `api_skill_test.go` 直接调用包级函数 → 改为 `s.handleXxx`
- `cron_api_test.go` 同样处理
- handler 方法化后测试文件需构造 `appServer`（用 test helper `newTestServer(t)` 若已存在）。

- [ ] **Step 3: 运行 smoke-test**

```bash
bash scripts/smoke-test.sh 2>&1 | tee /tmp/8b-smoke.log
```

目标：61 PASS/1 FAIL（memory 400 vs 405，既有基线）保持不退化。

- [ ] **Step 4: Commit（每修复一类失败单独 commit）**

```bash
git add cmd/server/*_test.go
git commit -m "Phase 8-B: update tests for appServer methodized handlers"
```

---

## Task 9: 文档与路线图更新

**Files:**
- Modify: `roadmaps/ROADMAP.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: 在 `ROADMAP.md` 版本表中新增 v0.13.0**

| Version | Date | Highlights |
|---------|------|------------|
| v0.13.0 | 2026-07-23 | Phase 8-B: 动态工具 DB 持久化+启动加载；DynamicTool 委托 DynamicExecutor；AgentRunner.Recover 收口；handler 全方法化+switch→Register；闭包退场 |

- [ ] **Step 2: 在 `CLAUDE.md` 扩展 Phase 表加 8-B，更新项目结构**

```markdown
| 8-B 架构收尾 | ✅ | AgentRunner.Recover + DBToolLoader 启动加载 + ExecuteWithCtx Workdir + handler 全方法化 + 闭包退场 |
```

- [ ] **Step 3: Commit**

```bash
git add roadmaps/ROADMAP.md CLAUDE.md
git commit -m "Phase 8-B: update ROADMAP and CLAUDE.md"
```

---

## Task 10: 最终全量验证与代码审查

- [ ] **Step 1: 最终 `go build ./...`**

```bash
go build ./...
```

- [ ] **Step 2: 最终 `go test ./...`**

```bash
go test ./...
```

- [ ] **Step 3: 自我审查 checklist**

- [ ] `BuiltInToolLoader` 已删除
- [ ] `DynamicTool.Execute` 委托 `DynamicExecutor`
- [ ] `cmd/server/tool_api.go` 使用 `InsertToolV2`
- [ ] main.go 启动期加载 DB 工具
- [ ] `AgentRunner.Recover` 补齐 EngineConfig
- [ ] `handleRecoverCheckpoint` 是 `appServer` 方法
- [ ] `Registry.ExecuteWithCtx` 存在且 Engine 调用
- [ ] `run_shell/write_file/read_file` 读 `ctx.Workdir`
- [ ] 所有 handler 为 `appServer` 方法
- [ ] `registerRoutes` 无大段透传样板
- [ ] `handleTasksRoot` 闭包已删除
- [ ] `startChatTask` 闭包已删除
- [ ] `globalCronService` 包级 var 已删除并进入 `appServer`
- [ ] `appServer` 字段按子系统分组且空行隔离
- [ ] 文档已更新

- [ ] **Step 4: 提交最终 commit（如有改动）**

---

## Self-Review Results

- **Spec coverage**: 所有 spec 第 2 节条目都有对应 Task（P0=1+2, B3=3, P1=4+5, B1=7, B2=6, 文档=9, 验证=8+10）。
- **Placeholder scan**: 无 TBD/TODO，每步含代码；依赖未确定字段（如 `Checkpoint.WorkspaceDir`、`CostRepo` 回调名）在 Step 5/Step 2 中已要求运行时核对。
- **Type consistency**: `DynamicTool`/`BuiltinTool` 的上下文执行入口、`RecoverSpec`、`taskActionRegistry` 签名已统一。

