## 1. 环境就绪

- [x] 1.1 重新创建干净的 `.claude/worktrees/phase-8b-cleanup-2` worktree
- [x] 1.2 补齐 `web/dist/index.html` 与 `web/v2/dist/index.html` placeholder 使 `go build` 通过
- [x] 1.3 创建 OpenSpec change `phase-8b-cleanup-2`
- [x] 1.4 完成 proposal / design / specs 产物

## 2. P0 — 动态工具持久化与启动期加载

- [x] 2.1 `handleRegisterTool` 改用 `db.InsertToolV2`，构造含 `type` + command/url/method/code 的 `execution_config`
- [x] 2.2 注册前冲突检查改为按 `CanonicalName` 判断
- [x] 2.3 `handleDeleteTool` 改用 `db.DeleteToolV2`（namespace/name/version）
- [x] 2.4 `main.go` 启动期在 `RegisterBuiltins` 后调用 `db.QueryToolsV2` + `tool.DBToolLoader`，加载 `source=local_db` 记录并注册到 registry；跳过 `execution_config_json` 为空或 type 缺失的记录并打印 warning
- [x] 2.5 新增 `cmd/server/tool_api_test.go` 集成测试：注册动态 shell 工具 → 重启 registry 加载 → 断言工具存在且可执行

## 3. B3 — 死代码诚实化

- [x] 3.1 `DynamicTool` 改为委托 `DynamicExecutor`，新增 `executor *DynamicExecutor` 字段
- [x] 3.2 从 `dynamic.go` 删除 `executeShell/executeHTTP/executeInline` 三个私有方法
- [x] 3.3 `SetCommand/SetHTTP/SetCode` 同步更新 `descriptor.ExecutionConfig` 与 executor
- [x] 3.4 删除未使用的 `BuiltInToolLoader`（loader.go）
- [x] 3.5 扩展 `internal/tool/dynamic_executor_test.go`，覆盖 shell/http/inline/Workdir 场景

## 4. P1b — `ExecuteContext.Workdir` 注入

- [x] 4.1 `Registry` 新增 `ExecuteWithCtx(name string, ctx ExecuteContext, input map[string]any) (any, error)`
- [x] 4.2 `BuiltinTool` 的 `ExecuteWithCtx` 实现优先使用 `ctx.Workdir` 作为 CWD/相对路径 base
- [x] 4.3 `DynamicTool` 的 `ExecuteWithCtx` 委托 `DynamicExecutor.Execute(ctx, input)`
- [x] 4.4 `runtime.Engine.executeToolCall` 改调 `ExecuteWithCtx`；在调用前用 `WorkdirHolder.Get()` 覆盖 `input["workdir"]`
- [x] 4.5 确保 `Tool.Execute(input)` 接口签名不变（向后兼容）

## 5. P1a — Recovery 收口到 AgentRunner

- [x] 5.1 `cmd/server/runner.go` 新增 `RecoverSpec` 与 `AgentRunner.Recover`
- [x] 5.2 `Recover` 内部构造完整 EngineConfig（对齐 `runAgentLoopWithTurn` 的字段）
- [x] 5.3 `handleRecoverCheckpoint` 改为 `appServer` 方法并调用 `s.newRunner().Recover`
- [x] 5.4 区分 checkpoint 缺失返回 404

## 6. B1/B2 — Handler 方法化 + 闭包退场

- [x] 6.1 将 `cmd/server/api.go` / `cmd/server/main.go` 中的包级 handler 改为 `appServer` 方法
- [x] 6.2 从 `main.go` 提取 `handleTasksRoot` 到 `cmd/server/tasks_api.go`，并将 `switch req.Action` 改为 `taskActionRegistry` 注册表
- [x] 6.3 新增 `actionChat/actionMultiAgent/actionStreamDemo` 为 `appServer` 方法
- [x] 6.4 `startChatTask` 从 main() 闭包改为 `(s *appServer) startChatTask(opts)` 方法
- [x] 6.5 `registerRoutes` 改为方法值注册，删除局部别名透传样板
- [x] 6.6 提取 `/api/tasks/` 子资源分发（`handleTasksSub`）、`/api/sessions/` 子资源分发（`handleSessionsSub`）、`/api/tools` 方法分发（`handleTools`）、`/api/memories` 根/子资源分发（`handleMemoriesRoot`/`handleMemoriesSub`）
- [x] 6.7 子系统路由注册函数改为 `appServer` 方法：`registerCronRoutes`/`registerMCPRoutes`/`registerTodoRoutes`/`registerSkillRoutes`/`registerMockRoutes`/`registerModelPriceRoutes`；提供 `registerXxxOn(mux)` 测试隔离入口
- [x] 6.8 `appServer` 字段按子系统分组 + 注释；新增 `cronService` 等字段
- [x] 6.9 删除 `makeRunnerDeps`，调用方改用 `s.deps()` / `s.newRunner()`

## 7. 测试与文档

- [ ] 7.1 同步修改 `cmd/server/*_test.go` 中的测试调用方式
- [ ] 7.2 运行 `go build ./...` 与 `go test ./...` 全绿
- [ ] 7.3 运行 `scripts/cases-regression.sh` 或等价的 smoke 回归，确认不低于基线
- [ ] 7.4 更新 `roadmaps/ROADMAP.md`（新增 v0.13.0 版本记录）
- [ ] 7.5 更新 `CLAUDE.md`（扩展 Phase 表加 Phase 8-B、更新项目结构）

## 8. OpenSpec 收尾

- [ ] 8.1 同步更新 `tasks.md` 勾选状态
- [ ] 8.2 运行 `openspec verify-change` 通过
- [ ] 8.3 提交 Git（commit message：`Phase 8-B cleanup-2: 动态工具持久化、Engine Workdir 注入、handler 方法化与闭包退场`）
- [ ] 8.4 运行 `openspec archive-change`
