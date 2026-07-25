## Why

Phase 8-A 已经完成了多 Agent 平台的架构奠基：`AgentRunSpec`/`AgentDeps`/`AgentRunner` 收口启动链路、Tool 接口扩展 `Version/Source/CanonicalName`、`ToolDescriptor`/`ToolExecutor`/`ToolLoader` 抽象、v27 tools 表迁移、`cmd/server` 拆分为 main/server/runner/api 四文件。但 8-A 的 spec 把“handler 透传收敛”与“加载链路接线”明确划在范围外，留下三类真实问题：
1. **真实回归（P0）**：动态工具注册后重启即丢失——`handleRegisterTool` 仍用旧 `db.InsertTool`（不写 `execution_config_json`），且启动期没有从 v27 表加载动态工具回 registry。
2. **死代码抽象（B3）**：`BuiltInToolLoader` 返回 `nil,nil`、`DynamicTool.Execute` 自己实现 shell/http/inline 而不委托 `DynamicExecutor`。
3. **recovery 与执行上下文未收口（P1）**：`handleRecoverCheckpoint` 直接构造 Engine，EngineConfig 缺失 skill/session/ws/cost 等字段；`BuiltinTool.Execute` 传 `ExecuteContext{}`，`Workdir` 未注入。
4. **上帝函数与闭包残留（B1+B2）**：`handleSessionChat`/`handleRunCase`/`handleRecoverCheckpoint` 仍是高参包级函数；`handleTasksRoot`/`startChatTask` 是 main() 闭包，捕获大量局部变量；`appServer` 已聚合依赖却未被 handler 使用。

本次 Phase 8-B cleanup-2 的目标是把 8-A 未接线的抽象接上、未收尾的部分闭环，并消除 HTTP 层上帝函数与闭包，使 8-A 的投资真正生效。不引入新 capability，不跨 Phase。

## What Changes

- **P0 — 动态工具持久化与启动期加载**：
  - `handleRegisterTool` 改用 `db.InsertToolV2`，把 command/url/method/code 写进 `execution_config_json`。
  - `main.go` 启动期在 `tool.RegisterBuiltins` 之后调用 `db.QueryToolsV2()` + `tool.DBToolLoader` 把 `source=local_db` 记录还原为 `DynamicTool` 注册进 registry；跳过 `execution_config_json` 为空或 type 缺失的旧记录并打印 warning。
  - `handleDeleteTool` 改用 `db.DeleteToolV2`（按 namespace/name/version 删除）。
  - 注册前冲突检查改为按 `CanonicalName` 判断，支持多版本并存。

- **B3 — 死代码诚实化**：
  - `DynamicTool` 改为委托 `DynamicExecutor`，删除 `dynamic.go` 中重复的 `executeShell/executeHTTP/executeInline` 三个私有方法。
  - 删除未使用的 `BuiltInToolLoader`。

- **P1b — `ExecuteContext.Workdir` 注入**：
  - 新增 `Registry.ExecuteWithCtx(name string, ctx ExecuteContext, input map[string]any)`，Engine 改调此入口。
  - `BuiltinTool.executor` 与 `DynamicTool.executor.Execute` 优先使用 `ctx.Workdir` 作为 CWD；LLM 显式传 `input["workdir"]` 仍优先，但 Engine 层会用 `WorkdirHolder.Get()` 覆盖，从而防止 workdir 逃逸。
  - `Tool.Execute(input)` 签名保持不变（向后兼容）。

- **P1a — recovery 收口到 AgentRunner**：
  - 新增 `AgentRunner.Recover(ctx, RecoverSpec{TaskID})`，内部调用 `runtime.RecoverFromCheckpoint` 但补齐 `SkillRegistry`/`ActiveSkills`/`AgentBus`/`WorkingMemory`/`SessionMessageWriter`/`ActiveTodos`/`WorkspaceDir`/`Tracer`/`RootTraceCtx`/cost 回调等 EngineConfig 字段。
  - `handleRecoverCheckpoint` 改为 `(s *appServer) handleRecoverCheckpoint(w, r)`，调 `s.newRunner().Recover(...)`。

- **B1/B2 — handler 方法化 + 闭包退场 + appServer 字段分组**：
  - `cmd/server` 所有包级 handler 改为 `(s *appServer) handleXxx(w, r)` 方法，依赖从 `s` 取。
  - `registerRoutes` 改为方法值注册，删除 30+ 行局部别名透传样板。
  - `handleTasksRoot` 从 main() 闭包中移出，拆分到 `cmd/server/tasks_api.go`；`switch req.Action` 改为 `taskActionRegistry` 注册表分发；`actionChat`/`actionMultiAgent`/`actionStreamDemo` 作为 `appServer` 方法。
  - `startChatTask` 闭包退场，改为 `(s *appServer) startChatTask(opts) ...` 方法；cron `ActionRunner` 注入 `s.startChatTask` 作为 `TaskStarter`。
  - `registerSkillRoutes`/`registerTodoRoutes`/`registerMCPRoutes`/`RegisterMockRoutes`/`RegisterModelPriceRoutes`/`RegisterCronAPI` 改为 `appServer` 方法；测试通过 `registerXxxOn(mux)` 暴露隔离注册能力。
  - `appServer` 字段按子系统分组，补充注释；删除 `makeRunnerDeps`，调用方改用 `s.deps()`/`s.newRunner()`。

- **文档更新**：
  - 更新 `roadmaps/ROADMAP.md`（新增 v0.13.0 版本记录）。
  - 更新 `CLAUDE.md`（扩展 Phase 表加 Phase 8-B、更新项目结构）。

## Capabilities

### New Capabilities
- `phase-8b-dynamic-tool-persistence`：动态工具从 HTTP 注册 → v27 表持久化 → 启动期加载回 registry 的完整链路。
- `phase-8b-execution-context`：`ExecuteContext.Workdir` 从 Engine 经 Registry 注入到 Builtin/Dynamic 执行体，统一控制 tool CWD。
- `phase-8b-recovery-runner`：`AgentRunner.Recover` 补齐 recovery 路径的 EngineConfig 字段，恢复后的 agent 与正常 chat 走同一套依赖注入。

### Modified Capabilities
- `tool-execute-context-extension`：扩展 requirements，明确 `ExecuteContext.Workdir` 对动态工具同样生效（此前仅 builtin 工具）。

## Impact

- `cmd/server`：main.go、server.go、runner.go、api.go、tasks_api.go、tool_api.go、checkpoint_api.go、cron_api.go、mcp_api.go、api_todo.go、api_skill.go、mock_api.go、model_price_api.go。
- `internal/tool`：dynamic.go、executor.go、registry.go、loader.go、builtin.go。
- `internal/runtime`：engine.go（工具调用入口改调 `ExecuteWithCtx`）。
- 测试：新增 `cmd/server/tool_api_test.go`、已部分存在 `internal/tool/dynamic_executor_test.go`，并同步修改 `cmd/server/*_test.go`。
- 文档：`roadmaps/ROADMAP.md`、`CLAUDE.md`。
- API 与数据 schema 兼容：`/api/tools` 响应字段不变；v27 表结构不变。
