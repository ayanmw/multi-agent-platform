## Why

当前 session 的 workspace 是普通目录,多 session 共享同一 project workspace 时文件互相覆盖、没有版本隔离、没有"丢弃变更/保留待复查"能力。引入 git worktree 作为 workspace 的一种可选 backend,让 LLM 在 run 中自主决定何时进入隔离分支、何时退出,实现 Claude Code `EnterWorktree/ExitWorktree` 同款的隔离 + 未提交护栏 + 孤儿回收,且完全向后兼容——worktree 是主动触发的叠加能力,不触发则零感知、对存量无影响。

## What Changes

- 新增 `internal/workspace` 包:`Manager` 封装 git worktree 原语(Create / Keep / Remove / Get / List),路径统一落 `.claude/worktrees/`,baseRef 区分 `fresh`(从 origin/默认分支)/ `head`(从当前 HEAD)。
- `Remove` 实现未提交变更护栏:有未提交文件或分支未合并时,默认拒绝删除,必须显式 `discard_changes=true` 才继续(对齐 `ExitWorktree` 的 `discard_changes` 语义)。
- `.claude/worktrees/` 写入 `.gitignore`,防止 worktree 内容污染主仓库 status。
- 新增 Agent Tool `worktree/create`、`worktree/exit`、`worktree/status`(**主要入口**,LLM 在 run 中自主调用):`create` 在 run 中途切换该 run 的 CWD 到新 worktree,后续 tool 立刻在隔离分支产码;`exit{keep}` 保留目录待复查、`exit{remove}` 删除(护栏触发时返回未提交文件列表);`status` 查当前是否在 worktree。
- **per-run 可变 CWD holder**:每次 run 持有一个可变 workdir(初值 = session `WorkspaceDir`),`worktree/create` 改写它、所有 tool 经 `ExecuteContext.Workdir` 读取。LLM 通过 tool input 传入的 workdir SHALL NOT 覆盖 holder——防逃逸。
- 打通 `ExecuteContext.Workdir` 注入链路:builtin 从"读 input["workdir"]"切到"读 `ExecuteContext.Workdir`(优先)+ input(回退)"。
- `sessions` 表新增 `active_worktree_id` 列(migration vN),记录会话级 active worktree 状态机:一个 session 任一时刻最多一个 active worktree,已有 active 再 create 返回错误。
- 新增 REST API 作为**前端/用户入口**:`POST /api/sessions/:id/worktree`(创建,前端可随意触发)、`GET /api/sessions/:id/worktree`(查 active,前端随时查看)。**`exit` 不暴露 REST**:退出需判定"是否干净、是否已合并"才能安全清理,风险高,交由 LLM 经 `worktree/exit` Agent Tool 执行;用户/前端不直接 exit。
- 新增 `worktree_*` 事件(`worktree_created` / `worktree_removed` / `worktree_exit_blocked` / `worktree_orphan_removed`),经 `hub.SendEvent` WS 广播,贴合白盒 Agent 可观测性约定。
- **不设 session 结束钩子**:项目无此机制,"session 结束"语义模糊;生命周期收敛为 LLM 主动 exit(唯一退出入口,因需判定干净/合并状态)+ 启动孤儿扫描兜底。不引入 `WORKTREE_DEFAULT_EXIT` 配置。
- **向后兼容 / 存量零影响**:worktree 是主动触发的叠加能力——LLM 不调 `worktree/create` 时,`WorkspaceDir` 仍指向普通目录,现有 case / 回归脚本零改动、存量 session 零感知。`WORKTREE_ENABLED` 仅控制是否允许 create,默认 true(不触发即不生效)。

## Capabilities

### New Capabilities
- `workspace-worktree-isolation`: 基于 git worktree 的 session 级工作区隔离——Manager 原语、未提交变更护栏、session↔worktree 状态机绑定、per-run 可变 CWD holder 与防逃逸注入、Agent Tool 主入口(worktree/create/exit/status)+ REST 次入口、事件广播、孤儿扫描兜底。LLM 自主决定进入/退出 worktree,不触发则零感知。

### Modified Capabilities
<!-- 无现有 spec 的需求级变更;workspace 普通目录行为保持不变,worktree 是叠加的主动 backend。 -->

## Impact

- **新增代码**:`internal/workspace/`(Manager + 测试)、`internal/tool/worktree.go`(Agent Tool:worktree/create、worktree/exit、worktree/status)、`cmd/server/workspace_api.go`(REST handler,仅 create+get)、`pkg/db` 新 migration、`pkg/event` 新事件常量。
- **修改代码**:`cmd/server/runner.go`(`AgentDeps` 注入 Manager + per-run workdir holder,tool 调用传 `ExecuteContext.Workdir`)、`internal/tool/builtin.go` + `executor.go`(打通 `ExecuteContext.Workdir`,优先于 input)、`cmd/server/main.go`(构造 Manager、注册路由 / 孤儿扫描)、`cmd/server/server.go`(路由注册)。
- **DB**:新增 migration(sessions 表加 `active_worktree_id`)。
- **依赖**:无新外部依赖,仅用 `git` CLI(已有 `run_shell` 依赖 git/bash 环境)。
- **回归**:现有 21 case 回归脚本基于普通目录 workspace,worktree 为主动触发的叠加能力,默认不触发,回归 21/21 不受影响。
- **前端**(可选,后续):控制室 UI 显示 active worktree 状态与未提交护栏确认弹窗、手动 remove 入口——本变更后端先行,前端接入单列后续任务。
