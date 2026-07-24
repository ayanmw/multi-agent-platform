## Context

Phase 8-A 已经将 `cmd/server` 的运行入口收敛到 `AgentRunSpec`/`AgentDeps`/`AgentRunner`，定义了 `ToolDescriptor`/`ToolExecutor`/`ToolLoader` 抽象，并把 SQLite tools 表迁移到 v27（含 `execution_config_json`）。但 8-A 的 spec 明确把以下工作划在范围外：
- handler 层大量依赖仍通过 17 参包级函数或 main() 闭包传递；
- 动态工具写表仍走旧 `db.InsertTool`，未写 `execution_config_json`；
- 启动期没有从 v27 表把动态工具加载回 registry；
- `DynamicTool.Execute` 自己实现 shell/http/inline，没有委托已有的 `DynamicExecutor`；
- `BuiltinTool.Execute` 传 `ExecuteContext{}`，`Workdir` 未被注入；
- `handleRecoverCheckpoint` 直接构建 Engine，EngineConfig 缺失 skill/session/ws/cost 等字段，恢复路径是退化的。

本次变更（cleanup-2）就是把这些 8-A 未接线的抽象接上，并消除 HTTP 层的上帝函数与闭包。

## Goals / Non-Goals

**Goals:**
1. 动态工具注册后持久化到 v27 表，服务重启后自动加载回 registry 并可执行。
2. `DynamicTool` 诚实委托 `DynamicExecutor`，删除重复实现与未使用的 `BuiltInToolLoader`。
3. `ExecuteContext.Workdir` 从 Engine 经 Registry 注入到 Builtin/Dynamic 执行体，LLM 伪造 `input["workdir"]` 被覆盖。
4. `AgentRunner` 提供 `Recover` 入口，补齐 recovery 所需的全部 EngineConfig 字段。
5. 所有 `cmd/server` 包级 handler 改为 `appServer` 方法，`handleTasksRoot`/`startChatTask` 等闭包退场。
6. `switch req.Action` 改为注册表分发；子系统路由注册函数改为 `appServer` 方法。
7. 更新 ROADMAP 与 CLAUDE.md，记录 8-B 收尾完成。
8. 保证 `go build ./...`、`go test ./...` 全绿，smoke-test 不退化。

**Non-Goals:**
- 不引入新 capability（不扩展 skill/cron/worktree/memory 功能）。
- 不改 `/api/tools` 字段结构（仅底层持久化从 `InsertTool` 切到 `InsertToolV2`，响应兼容）。
- 不实现外部 WASM/.so/Python 插件。
- 不做 Linter arity 规则等基础设施。
- 不改动 Phase 7 UI-v2 / Cron / Skill 的功能。

## Decisions

**Decision 1: `Tool.Execute(input)` 签名保持不变，新增 `Registry.ExecuteWithCtx` 与 `ExecuteWithCtx` 实现方法**
- Rationale：直接改接口会波及大量既有调用方与测试。通过 Engine 调用 `Registry.ExecuteWithCtx`，Registry 再把 `ExecuteContext` 透传给内部 `BuiltinTool.executor(ctx, input)` 与 `DynamicTool.executor.Execute(ctx, input)`，可在零接口破坏的前提下完成 Workdir 注入。
- Alternative：把 `ExecuteContext` 直接塞进 `Tool` 接口签名。否决：改动面太大，且 8-A 已明确接口是长期投资，本次只做收尾。

**Decision 2: `DynamicTool` 持有一个 `*DynamicExecutor` 字段，删除 `dynamic.go` 中三个私有方法**
- Rationale：避免 shell/http/inline 执行逻辑在两处维护。`executor.go` 已具备完整实现并已处理 `ctx.Workdir`。
- Alternative：保留 `DynamicTool` 自己实现并让它也读 `ctx.Workdir`。否决：重复代码违背 DRY，且 `DynamicExecutor` 就是为了统一执行而引入的抽象。

**Decision 3: `handleRegisterTool` 改用 `db.InsertToolV2`，启动期用 `tool.DBToolLoader` 加载 `source=local_db` 记录**
- Rationale：这是 v27 表引入时的预期生产路径。`InsertToolV2` 写 `execution_config_json`，`DBToolLoader.Load` 把记录反序列化为 `DynamicTool`。
- Alternative：在 `InsertTool` 里偷偷加字段。否决：`InsertToolV2` 已经存在，应直接使用新的原子接口。

**Decision 4: `appServer` 持有 `*cron.Service`，子系统路由注册函数改为 `appServer` 方法**
- Rationale：消除 main() 闭包（如 `startChatTask` 捕获 15+ 局部变量）和包级 var。`appServer` 已经聚合全部依赖，是 handler 依赖的自然来源。
- Alternative：继续用包级函数 + 透传参数。否决：这正是 8-A 要消除的上帝函数问题。

**Decision 5: 运行期并发协调 Map（`cancelRegistry`/`engineRegistry`/`traceRegistry`）保留包级**
- Rationale：这些不是依赖注入，而是运行期全局状态注册表，且若干 goroutine 清理逻辑依赖它们。进 `appServer` 会增加生命周期复杂度。
- Alternative：全部塞进 `appServer`。否决：无明确收益，反而让跨 goroutine 访问需要额外传引用。

**Decision 6: 启动期加载时跳过 `execution_config_json` 为空或 `type` 缺失的记录**
- Rationale：8-A 之前用旧 `InsertTool` 写的记录在 v27 表中 `execution_config_json='{}'`，加载后会因 type 为空而执行失败。跳过并打 warning 可以让用户重新注册。
- Alternative：自动推断 type。否决：缺乏足够信息，推断可能错误且静默改变行为；显式失败+警告更安全。

## Risks / Trade-offs

- [Risk] `InsertToolV2`/`DeleteToolV2` 与旧 `InsertTool`/`DeleteTool` 并存，未来可能有人误用旧接口。 → Mitigation：在代码注释中标注旧接口为 legacy，并在本次变更的所有调用点切换到新接口；后续单独立项删除旧 legacy 接口。
- [Risk] `Registry.ExecuteWithCtx` 路径绕过了 `Tool.Execute(input)`，但外部调用方若直接调用 `Tool.Execute` 会拿不到 `Workdir`。 → Mitigation：仅 Engine 和测试使用 `ExecuteWithCtx`；`Tool.Execute` 内部默认 `ExecuteContext{}` 的行为保持不变，并通过注释说明“Engine 调用时请使用 Registry.ExecuteWithCtx”。这是向后兼容的已知约束。
- [Risk] `AgentRunner.Recover` 补齐 EngineConfig 时可能遗漏某个字段，导致恢复路径仍退化。 → Mitigation：与 `runAgentLoopWithTurn` 的 EngineConfig 构造做 diff 检查，确保字段对齐。
- [Risk] handler 方法化后，既有测试直接调包级函数会编译失败。 → Mitigation：本次变更同步修改所有相关测试文件。
- [Risk] 修改 `registerRoutes` 时路由注册顺序或路径冲突导致 404。 → Mitigation：按原顺序注册，仅把透传改为方法值；新增子资源分发方法仅做内部转发。

## Migration Plan

- 无需上线迁移脚本；本次变更对 API 响应与数据结构兼容。
- 部署后首次启动，`DBToolLoader` 会尝试加载 v27 表中所有 `source=local_db` 记录；旧记录（`execution_config_json='{}'`）会被跳过并打印 warning，管理员可重新注册这些工具。
- 回滚：本次变更是代码层重构，回滚到上一版本即可；持久化数据仍存储在 v27 表中，无 schema 变更。

## Open Questions

- 是否需要在本次同时删除 `db.InsertTool`/`DeleteTool` 旧函数？建议本次保留以控制范围，后续单独立项清理 legacy DB 接口。
- `stream-demo` 中 `web_fetch`/`web_search` 工具的 TODO 是否在本次处理？建议本次不处理，因它属于 capability 扩展而非 8-A 收尾。
