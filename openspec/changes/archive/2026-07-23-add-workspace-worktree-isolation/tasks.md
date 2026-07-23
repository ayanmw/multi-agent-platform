## 1. 原语层 — `internal/workspace.Manager`

- [x] 1.1 创建 `internal/workspace/manager.go`:`Manager` 结构(rootDir、repoGitDir)、`Worktree` 结构(ID/Branch/Path/BaseRef/SessionID/CreatedAt)、`NewManager(rootDir string)`
- [x] 1.2 实现 `Create(sessionID, baseRef string) (*Worktree, error)`:生成短 ID、`git worktree add <path> -b <branch> [<baseRef>]`、fresh 解析 `origin/<默认分支>` 失败回退本地默认分支 + warning
- [x] 1.3 实现 `Get(id)` / `List()`:基于 `git worktree list --porcelain` 解析,仅返回 `.claude/worktrees/` 下条目
- [x] 1.4 实现 `Keep(id)`:仅清理 Manager 内部记账,不删目录
- [x] 1.5 实现 `Remove(id, discardChanges) (*RemoveReport, error)`:`git -C <path> status --porcelain` 检测未提交、检测分支是否合并到默认分支;有变更且未显式丢弃 → 返回 `RemoveReport{Blocked:true, Uncommitted, Unmerged}`,否则 `git worktree remove` + 删分支
- [x] 1.6 实现 `ensureGitignored()`:`git check-ignore` 检测 `.claude/worktrees/`,未忽略则追加到 `.gitignore`
- [x] 1.7 `internal/workspace/manager_test.go`:用临时 git 仓库覆盖 Create fresh/head/origin 回退、Get/List、Remove 护栏三档、Keep、gitignore 幂等;Windows 优先跑

## 2. DB migration

- [x] 2.1 `pkg/db` 新增 migration vN:`ALTER TABLE sessions ADD COLUMN active_worktree_id TEXT DEFAULT NULL`
- [x] 2.2 提供 `GetSessionActiveWorktree / SetSessionActiveWorktree / ClearSessionActiveWorktree` 读写 helper
- [x] 2.3 migration 回归测试:旧库升级后 `active_worktree_id` 为 NULL,旧 session 行为不变

## 3. Tool CWD 注入打通

- [x] 3.1 `internal/tool/builtin.go`:`executeWriteFile` / `executeReadFile` / `executeShell` / `listDirExecutor` 改为优先使用 `ExecuteContext.Workdir`,仅在为空时回退 `input["workdir"]` 与 `os.Getwd()`
- [x] 3.2 让 `ExecuteContext.Workdir` 能透传到 executor:确认 `BuiltinTool.Execute` 调用链,增加带 ctx 的执行入口或由 runner 在调用 executor 前注入 Workdir
- [x] 3.3 单测:Workdir 经 `ExecuteContext` 注入时,LLM 传入的 `workdir` 字段被忽略;无 Workdir 时回退 input/Getwd 不变
- [x] 3.4 path-traversal 防护保留作第二道,补充测试:即使 workdir 指向 worktree,`..` 路径仍被拒

## 4. Runner 接线 — per-run 可变 CWD holder

- [x] 4.1 定义 per-run workdir holder(可变 string,初值 = session `WorkspaceDir`),确定注入形态:holder 实例 run-scoped,经 `AgentDeps` 注入其构造器
- [x] 4.2 `AgentRunner.Run`:构造 holder,每次 tool 调用构造 `ExecuteContext{Workdir: holder.Get()}` 传入;`worktree/create` / `worktree/exit` tool 持有 holder 引用以中途改写
- [x] 4.3 `AgentDeps` 注入 `workspace.Manager` 与 holder 构造器,供 worktree tool 使用
- [x] 4.4 验证 leader dispatch 子 agent 透传:子 agent run 共享父 session 的 worktree(holder 各自独立但指向同一 worktree.Path);不提供 per-subagent worktree
- [x] 4.5 单测:run 中途 create 切换 holder 后,后续 tool 写文件落 worktree.Path;exit 后恢复 session WorkspaceDir;LLM 伪造 workdir 被忽略

## 5. Agent Tools — LLM 主入口

- [x] 5.1 `internal/tool/worktree.go`:`worktree/create`(base_ref 默认 fresh,创建 + 改写 holder + 设 `active_worktree_id` + 广播 `worktree_created`)
- [x] 5.2 `worktree/status`:返回 active true/false + ID/Branch/Path
- [x] 5.3 `worktree/exit`({action:keep|remove, discard_changes?}):keep 清 `active_worktree_id` + 恢复 holder;remove 调 Manager.Remove,护栏触发返回未提交文件列表 observation + 广播 `worktree_exit_blocked`,成功广播 `worktree_removed`
- [x] 5.4 重入语义:已有 `active_worktree_id` 再 create → 返回错误 observation;`WORKTREE_ENABLED=false` → create 返回错误 observation
- [x] 5.5 `cmd/server/main.go` 把三个 worktree tool 注册到 base registry(或仅给需要的 agent),注入 Manager + holder 构造器 + sessionID
- [x] 5.6 `internal/tool/worktree_test.go`:create / 重入错误 / status / exit keep / exit remove 干净 / exit remove 护栏 / WORKTREE_ENABLED=false

## 6. 事件与 REST API(前端/用户入口,仅 create + 查看)

- [x] 6.1 `pkg/event` 新增事件常量:`worktree_created` / `worktree_removed` / `worktree_exit_blocked` / `worktree_orphan_removed`
- [x] 6.2 `cmd/server/workspace_api.go`:`POST /api/sessions/:id/worktree`(create,base_ref 默认 fresh)、`GET /api/sessions/:id/worktree`(查 active)。**不注册 exit 路由**——退出仅 LLM 经 Agent Tool
- [x] 6.3 `WORKTREE_ENABLED=false` → 503;create 经 `hub.SendEvent` 广播 `worktree_created`,不写 task steps
- [x] 6.4 `cmd/server/server.go` `registerRoutes` 注册两条路由(create/get)
- [x] 6.5 `cmd/server/workspace_api_test.go`:create / 重入 409 / get active / 无 active 返回 active:false / 503 / exit 路由不存在返回 404

## 7. 孤儿扫描与配置

- [x] 7.1 `internal/config` 新增 `WORKTREE_ENABLED`(默认 true);不引入 `WORKTREE_DEFAULT_EXIT`(无结束钩子)
- [x] 7.2 启动孤儿扫描:`Manager.List()` 对比 DB 全表 `active_worktree_id`,清理 DB 不认得的 worktree,广播 `worktree_orphan_removed`
- [x] 7.3 孤儿扫描测试:造一个 DB 不认得的 worktree,启动后清理 + 事件
- [x] 7.4 不设 session 结束钩子:确认 run 结束路径不会自动 remove/keep worktree,补充测试验证 `active_worktree_id` 在 run 结束后保持

## 8. 集成回归

- [x] 8.1 端到端(LLM 驱动):agent run 中调 `worktree/create` → 后续 write_file 落 worktree → 调 `worktree/exit{keep}` → 再 write_file 落 session WorkspaceDir
- [x] 8.2 端到端:开 worktree → 写文件不提交 → `exit{remove,discard=false}` → 护栏错误 → `exit{remove,discard=true}` → 删除
- [x] 8.3 跑 `scripts/cases-regression.sh`(LLM_USE_MOCK=true)确认 21/21 PASS(worktree 是主动能力,默认不触发)
- [x] 8.4 Windows 环境跑 worktree 原语 + tool + API 测试(`PYTHONUTF8=1`)

## 9. 文档与收尾

- [x] 9.1 更新 `CLAUDE.md`:项目结构补 `internal/workspace/`、`internal/tool/worktree.go`、sessions 表 `active_worktree_id`、worktree Agent Tools + REST API、`worktree_*` 事件、`WORKTREE_ENABLED` 配置;扩展 Phase 表新增一行
- [x] 9.2 更新 `roadmaps/ROADMAP.md`:版本历史与状态表
- [x] 9.3 提交 Git(信息格式 `Phase ...: workspace worktree 隔离`)
- [x] 9.4 `openspec-verify-change` → `openspec-archive-change`(归档到 `openspec/changes/archive/`)
