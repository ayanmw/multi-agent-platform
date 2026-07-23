# workspace-worktree-isolation Specification

## Purpose
TBD - created by archiving change add-workspace-worktree-isolation. Update Purpose after archive.
## Requirements
### Requirement: Worktree Manager 原语
系统 SHALL 提供 `internal/workspace.Manager` 封装 git worktree 原语,支持 Create / Keep / Remove / Get / List,worktree 目录统一落在 `.claude/worktrees/<id>` 下,不散落到其它路径。

#### Scenario: 从 fresh baseRef 创建 worktree
- **WHEN** 调用 `Manager.Create(sessionID, "fresh")` 且 `origin/<默认分支>` 可用
- **THEN** 系统在 `.claude/worktrees/<id>` 创建 worktree,其分支从 `origin/<默认分支>` 切出,返回的 `Worktree` 含 ID / Branch / Path / BaseRef="fresh" / SessionID / CreatedAt

#### Scenario: 从 head baseRef 创建 worktree
- **WHEN** 调用 `Manager.Create(sessionID, "head")`
- **THEN** 系统创建 worktree,其分支从当前本地 HEAD 切出

#### Scenario: origin 不可用时回退
- **WHEN** 调用 `Create(..., "fresh")` 但 `origin/<默认分支>` 不存在
- **THEN** 系统回退到本地默认分支切出 worktree,并返回 warning,不阻断创建

#### Scenario: Get 不存在的 worktree
- **WHEN** 调用 `Manager.Get("<不存在的ID>")`
- **THEN** 返回 nil 且不报错

#### Scenario: List 列出全部 worktree
- **WHEN** 调用 `Manager.List()`
- **THEN** 返回当前 `git worktree list` 中位于 `.claude/worktrees/` 下的全部 worktree

### Requirement: 未提交变更护栏
`Manager.Remove` SHALL 在删除前检测未提交变更与未合并分支;存在未提交文件或分支未合并到默认分支时,若未显式传 `discardChanges=true`,MUST 拒绝删除并返回阻塞报告。

#### Scenario: 有未提交文件且未显式丢弃
- **WHEN** 调用 `Manager.Remove(id, false)` 且该 worktree 有未提交文件
- **THEN** 系统返回 `RemoveReport{Blocked: true, Uncommitted: [<文件列表>]}`,worktree 目录与分支保留不删

#### Scenario: 有未提交文件但显式丢弃
- **WHEN** 调用 `Manager.Remove(id, true)` 且该 worktree 有未提交文件
- **THEN** 系统删除 worktree 目录与分支

#### Scenario: 无未提交变更直接删除
- **WHEN** 调用 `Manager.Remove(id, false)` 且 worktree 干净、分支已合并
- **THEN** 系统删除 worktree 目录与分支

#### Scenario: Keep 保留 worktree
- **WHEN** 调用 `Manager.Keep(id)`
- **THEN** worktree 目录与分支保留在磁盘上,不删除

### Requirement: gitignore 隔离
系统 SHALL 确保 `.claude/worktrees/` 被 `.gitignore` 忽略,防止 worktree 内容污染主仓库 status。

#### Scenario: .gitignore 已忽略
- **WHEN** Manager 初始化且 `.claude/worktrees/` 已在 `.gitignore` 中
- **THEN** 不修改 `.gitignore`

#### Scenario: .gitignore 未忽略
- **WHEN** Manager 初始化且 `.claude/worktrees/` 未被忽略
- **THEN** 系统追加 `.claude/worktrees/` 到 `.gitignore`

### Requirement: Session Worktree 状态机
系统 SHALL 维护 session 与 worktree 的一对一活跃绑定:一个 session 任一时刻最多一个 active worktree,记录在 `sessions.active_worktree_id`。worktree 进入/退出由 LLM 在 run 中通过 Agent Tool 主动触发,或由用户经 REST API 手动触发;系统不设 session 结束钩子自动回收。

#### Scenario: 已有 active worktree 再创建
- **WHEN** session 已有 `active_worktree_id` 且再次请求创建 worktree(经 Agent Tool 或 REST)
- **THEN** 返回错误(Tool 返回 observation 错误 / REST 返回 409 Conflict),不创建新 worktree

#### Scenario: Keep 退出
- **WHEN** 调用 `worktree/exit` 或 REST exit 传 `{action:keep}` 且 session 有 active worktree
- **THEN** worktree 目录保留,`sessions.active_worktree_id` 清空为 NULL

#### Scenario: Remove 退出干净 worktree
- **WHEN** 调用 exit 传 `{action:remove}` 且 worktree 干净
- **THEN** worktree 目录与分支删除,`active_worktree_id` 清空

#### Scenario: Remove 退出有未提交变更
- **WHEN** 调用 exit 传 `{action:remove, discard_changes:false}` 且 worktree 有未提交变更
- **THEN** 返回错误(Tool observation 含未提交文件列表 / REST 409 + 文件列表),worktree 与 `active_worktree_id` 均保留

#### Scenario: 无 session 结束钩子
- **WHEN** session 停止接收新消息但未显式 exit worktree
- **THEN** 系统 SHALL NOT 自动 remove 或 keep 该 worktree;`active_worktree_id` 保持,等待 LLM 下次主动 exit 或用户手动清理或启动孤儿扫描

### Requirement: Per-run 可变 CWD 与中途切换
系统 SHALL 为每次 run 持有一个可变工作目录 holder(初值为 session `WorkspaceDir`),`worktree/create` 在 run 中途改写 holder 为新 worktree.Path,后续 tool 调用立即使用新 CWD;`worktree/exit` 恢复 holder 为 session `WorkspaceDir`。LLM 通过 tool input 传入的 workdir SHALL NOT 覆盖 holder。

#### Scenario: run 中途 create 切换 CWD
- **WHEN** agent 在 run 中先调 `write_file` 写 `a.txt`(落 session WorkspaceDir),再调 `worktree/create`,再调 `write_file` 写 `b.txt`
- **THEN** `a.txt` 落 session WorkspaceDir,`b.txt` 落新 worktree.Path;两次写入目录不同

#### Scenario: active worktree 时 tool 使用 worktree 路径
- **WHEN** session 有 active worktree 且 agent 调用 write_file 写相对路径 `output.txt`
- **THEN** 文件写入 `<worktree.Path>/output.txt`,而非普通目录 workspace

#### Scenario: LLM 伪造 workdir 被忽略
- **WHEN** session 有 active worktree 且 agent 调用 write_file 传入 `workdir=/etc`
- **THEN** 系统忽略 LLM 传入的 workdir,仍以 holder(worktree.Path)解析相对路径

#### Scenario: exit 恢复 session WorkspaceDir
- **WHEN** agent 调 `worktree/exit{keep}` 后再调 `write_file` 写 `c.txt`
- **THEN** `c.txt` 落 session WorkspaceDir(普通目录),不再落 worktree

#### Scenario: 无 active worktree 时回退普通目录
- **WHEN** session 无 active worktree 且 agent 调用 write_file
- **THEN** 沿用现有 `WorkspaceDir`(普通目录)行为,不改变

### Requirement: Worktree Agent Tools(LLM 主入口)
系统 SHALL 提供 `worktree/create`、`worktree/exit`、`worktree/status` 三个 Agent Tool,供 LLM 在 run 中自主进入/退出/查询隔离工作区。`WORKTREE_ENABLED=false` 时 `worktree/create` SHALL 返回错误 observation。

#### Scenario: create 工具
- **WHEN** LLM 调用 `worktree/create` 传 `{base_ref: "fresh"}` 且 session 无 active worktree
- **THEN** 工具创建 worktree,切换该 run 的 CWD holder,返回 observation 含 worktree ID/Branch/Path,广播 `worktree_created` 事件

#### Scenario: status 工具
- **WHEN** LLM 调用 `worktree/status`
- **THEN** 返回 observation 含 `active: true/false`、若有 active 则含 worktree ID/Branch/Path

#### Scenario: exit 工具
- **WHEN** LLM 调用 `worktree/exit` 传 `{action: "keep"|"remove", discard_changes?: bool}`
- **THEN** 按 action 执行(keep 保留 / remove 删并受护栏),返回 observation 说明结果,广播对应事件

#### Scenario: 功能未开启
- **WHEN** `WORKTREE_ENABLED=false` 且 LLM 调用 `worktree/create`
- **THEN** 工具返回错误 observation,不创建 worktree;现有 workspace 行为不受影响

### Requirement: Worktree REST API(前端/用户入口,仅 create + 查看)
系统 SHALL 提供工作区创建与查询的 REST API 供前端/用户使用;**SHALL NOT 暴露 exit 接口**——退出需判定干净/合并状态,风险高,仅 LLM 经 Agent Tool 执行。

#### Scenario: 创建 worktree
- **WHEN** `POST /api/sessions/:id/worktree` body `{base_ref: "fresh"}`
- **THEN** 返回新建 worktree 信息(ID / Branch / Path),并广播 `worktree_created` 事件

#### Scenario: 查询 active worktree
- **WHEN** `GET /api/sessions/:id/worktree`
- **THEN** 返回当前 active worktree 信息,无则返回 `active: false`;前端据此随时查看 session 的 worktree 状态

#### Scenario: 不提供 exit 接口
- **WHEN** 调用 `POST /api/sessions/:id/worktree/exit`
- **THEN** 返回 404 Not Found(路由不注册),退出只能经 `worktree/exit` Agent Tool

#### Scenario: 未开启 worktree 功能
- **WHEN** `cfg.WORKTREE_ENABLED=false` 且调用 worktree REST(create/get)
- **THEN** 返回 503 Service Unavailable

### Requirement: Worktree 事件广播
worktree 状态变更 SHALL 通过 `hub.SendEvent` 广播 WS 事件,且 SHALL NOT 写入 task steps。

#### Scenario: 创建广播
- **WHEN** worktree 创建成功
- **THEN** 广播 `worktree_created` 事件,含 session_id / worktree_id / branch / path

#### Scenario: 删除广播
- **WHEN** worktree 被 remove 删除
- **THEN** 广播 `worktree_removed` 事件

#### Scenario: 护栏阻塞广播
- **WHEN** `exit{remove}` 因未提交变更被阻塞
- **THEN** 广播 `worktree_exit_blocked` 事件,含未提交文件列表

### Requirement: 生命周期回收与孤儿扫描
系统 SHALL NOT 设 session 结束钩子自动回收 worktree;生命周期 SHALL 收敛为 LLM 主动 `worktree/exit` + 用户手动 REST remove + 启动孤儿扫描兜底。

#### Scenario: 启动孤儿扫描
- **WHEN** 服务启动且 `git worktree list` 存在 DB 全表 `active_worktree_id` 都不认得的 worktree(位于 `.claude/worktrees/`)
- **THEN** 系统清理该孤儿 worktree,广播 `worktree_orphan_removed` 事件

#### Scenario: LLM 忘记 exit 不自动回收
- **WHEN** run 结束但 LLM 未调 `worktree/exit`,session 仍有 `active_worktree_id`
- **THEN** 系统 SHALL NOT 删除该 worktree;下次进入同 session 时 LLM 可经 `worktree/status` 发现仍 active 并自行决定 exit,用户也可手动 remove

#### Scenario: 用户手动 remove
- **WHEN** 用户尝试经 REST 退出 worktree
- **THEN** 无 exit 路由可用(404);remove 只能由 LLM 经 `worktree/exit` Agent Tool 执行,确保退出前判定干净/合并状态

