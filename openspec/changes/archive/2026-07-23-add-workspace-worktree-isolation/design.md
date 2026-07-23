## Context

当前 session workspace 是普通目录(`cmd/server/main.go:404` 解析 `wsSess.WorkspaceDir`,回退 `proj.WorkingDirectory`),通过 `AgentRunSpec.WorkspaceDir`(`main.go:546`)注入,工具层从 `input["workdir"]` 解析相对路径(`internal/tool/builtin.go:224` `resolvePath` / `:413` `executeWriteFile`),带 path-traversal 防护。`ExecuteContext.Workdir`(`internal/tool/executor.go:14`)已预留但未被 builtin 使用。

多 session 共享同一 project workspace 时,文件互相覆盖、无版本隔离、无"丢弃/保留"能力。本变更新增 git worktree 作为 workspace 的可选 backend,镜像 Claude Code `EnterWorktree/ExitWorktree` 的隔离 + 护栏 + 回收语义,向后兼容普通目录模式。

约束:
- 不引入新外部依赖,仅用 `git` CLI。
- 现有 21 case 回归基于普通目录,worktree 默认不开启,回归不受影响。
- 路径统一 `.claude/worktrees/`(memory `worktree-path-unification-rule`),禁止散落 `.worktrees/`。
- 并发治理遵循 memory:`subagent-serial-execution`、`lead-subagent-parallel-control`。

## Goals / Non-Goals

**Goals:**
- `internal/workspace.Manager` 提供 worktree 原语(Create/Keep/Remove/Get/List),baseRef 区分 fresh/head。
- `Remove` 未提交变更护栏:有未提交文件或分支未合并时默认拒绝,需显式 `discard_changes` 才删除。
- session↔worktree 状态机:一 session 至多一个 active worktree,sessions 表加 `active_worktree_id`。
- tool CWD 注入打通:active worktree 时,该 run 所有 tool 工作目录指向 worktree.Path,且 LLM 无法伪造 workdir 逃逸。
- REST API + `worktree_*` 事件 + session 结束钩子 + 启动孤儿扫描。

**Non-Goals:**
- 不做前端控制室 UI 接入(后端先行,前端单列后续任务)。
- 不替换普通目录 workspace;worktree 是可选 backend。
- 不引入 Docker/Firecracker 沙箱(run_shell 沙箱仍是 Phase 5 待定问题)。
- 不做 worktree 内的 RBAC / 权限模型(复用现有 path-traversal 防护)。
- 不做多 worktree 并发 per session(一 session 一 active worktree)。

## Decisions

### D1: worktree 作为 workspace backend,而非取代
**选择**:session 开启 worktree 时 `WorkspaceDir` 指向 `worktree.Path`;不开时退回普通目录。
**理由**:向后兼容,渐进落地,现有 case/回归零改动。
**备选**:worktree 取代所有 session workspace —— 隔离更彻底但需重构所有基于"普通目录"假设的代码与回归脚本,风险过大。

### D2: 原语收口到 `internal/workspace.Manager`,仅用 git CLI
**选择**:`Manager` 封装 `git worktree add` / `git worktree remove` / `git status --porcelain`,不依赖 go-git 库。
**理由**:项目"标准库优先"约定;`run_shell` 已依赖 git/bash 环境,无新依赖。
**备选**:go-git 库 —— 增加依赖与维护成本,无额外收益。

### D3: baseRef 语义对齐 Claude Code `worktree.baseRef`
**选择**:`fresh`(默认)从 `origin/<default-branch>` 切出;`head` 从当前本地 HEAD 切出。`origin` 不存在时回退到本地默认分支并记录 warning。
**理由**:对齐已知好用的语义,fresh 给干净起点,head 保留当前工作上下文。

### D4: 未提交护栏在 Manager.Remove 层强制
**选择**:`Remove(id, discardChanges)` 先跑 `git -C <path> status --porcelain` 与"分支是否已合并到默认分支"检查;有未提交或未合并且 `discardChanges=false` → 返回 `RemoveReport{Blocked: true, Uncommitted: [...], Unmerged: bool}`,不删除。
**理由**:对应 `ExitWorktree` 的 `discard_changes` 护栏;满足 memory `git-worktree-merge-safety`(避免误删设计文档/未跟踪文件)。
**备选**:仅在上层 API 检查 —— 易绕过,原语层强制更安全。

### D5: tool CWD 注入走 per-run 可变 holder,由 runner 持有
**选择**:每次 run 持有一个**可变 workdir holder**(初值 = session `WorkspaceDir`),所有 tool 经 `ExecuteContext.Workdir` 读取它。`worktree/create` 在 run 中途改写 holder,后续 tool 立刻生效(对齐 Claude Code 中途 EnterWorktree 切 cwd 的语义);`worktree/exit` 恢复为 session `WorkspaceDir`。LLM 通过 tool input 传入的 workdir SHALL NOT 覆盖 holder。
**理由**:`ExecuteContext.Workdir` 已预留(`executor.go:14`);LLM 自主决定 worktree 时机意味着 CWD 必须可中途切换,不能在 `AgentRunSpec` 构造时冻结。runner 持有 holder 是唯一可信源,确保 LLM 无法伪造 workdir 逃逸到 worktree 之外——这是隔离的真正保证。
**权衡**:需把 builtin 从"读 input["workdir"]"切到"读 ExecuteContext.Workdir(优先)+ input(回退)",改动 builtin.go 但向后兼容;holder 需在 runner 与 worktree tool 间共享(经 `AgentDeps` 注入)。

### D6: 状态机与重入语义
**选择**:sessions 表 `active_worktree_id`;已有 active 再 Create → 409;切换必须先 exit。`exit{keep}` 保留目录与分支,清空 `active_worktree_id`;`exit{remove}` 调 Manager.Remove,护栏触发时返回 409 + 文件列表。
**理由**:对应 Claude Code 默认拒绝重入的语义;防止一个 session 漏掉清理旧 worktree。

### D7: 事件走 hub.SendEvent,不写 task steps
**选择**:`worktree_created` / `worktree_removed` / `worktree_exit_blocked` 经 `hub.SendEvent` WS 广播,不写 task steps。
**理由**:对齐 orchestrator 编排事件的可观测性约定(CLAUDE.md「编排事件的可观测性约定」)——这些是 session 级事件而非 task step。

### D8: 生命周期回收(仅孤儿扫描)+ 不设结束钩子 + exit 仅 LLM 可调
**选择**:**不设 session 结束钩子**(项目现状无此机制,且"session 结束"语义模糊)。生命周期收敛为三点:(1) `worktree/exit` 是唯一退出入口,**仅 LLM 经 Agent Tool 调用**——退出需判定"是否干净、是否已合并"才能安全清理,风险高,交由 LLM 执行;(2) 用户/前端**只能 create 与查看**(REST),不暴露 exit,避免用户误删未提交/未合并的工作;(3) 启动孤儿扫描对比 `git worktree list` 与 DB 全表 `active_worktree_id`,清理 DB 不认得的残留 crash worktree(记 `worktree_orphan_removed` 事件)。不引入 `WORKTREE_DEFAULT_EXIT` 配置(无结束钩子即无需默认退行动作)。
**理由**:契合本项目"LLM 自主 + 服务启动清理"的现有范式;exit 的安全判定(干净/合并)是 LLM 擅长的语义决策,用户凭直觉 exit 风险过高。前端只需看到"当前 session 在哪个 worktree"即可,不需操作 exit。
**备选**:REST 也暴露 exit —— 用户可能在 worktree 有未提交工作时误点 remove,护栏虽能拦 discard=false,但交互复杂、易误操作;不暴露更安全。

## Risks / Trade-offs

- **[Windows git/bash 环境]** worktree 路径含中文/空格可能触发 `git worktree` 边界 case → Create/Remove 用 `git -C` + 绝对路径,路径用 `filepath.Clean`;回归脚本已要求 `PYTHONUTF8=1`,worktree 测试在 Windows 优先跑。
- **[LLM 伪造 workdir 逃逸]** 若 tool 仍信任 `input["workdir"]` 覆盖 runner 注入值,隔离失效 → D5:runner 注入为可信源,builtin 优先用 `ExecuteContext.Workdir`,LLM 传的 workdir 仅在无 active worktree 时回退生效;path-traversal 防护保留作第二道。
- **[并发派发多 agent 各需 worktree]** 一 session 一 active worktree,leader 派发的子 agent 共享父 session 的 worktree(同隔离);不提供 per-subagent worktree → 避免并发文件冲突(memory `lead-subagent-parallel-control`)。若未来需独立,显式扩展。
- **[孤儿 worktree 占磁盘]** LLM 忘记 exit 或进程 crash 残留 → 启动孤儿扫描兜底;用户可在前端/REST 手动 remove;`exit{remove}` 删分支+目录。
- **[LLM 忘记 exit 导致 session 一直挂在 worktree]** 这是主动行为可接受的代价:下次进入同 session 时前端可经 `GET active` 看到仍在 worktree(提示用户),LLM 也可经 `worktree/status` 发现并自行 exit。不强制回收、不暴露用户 exit,避免误删在途工作。
- **[baseRef=origin 不可用]** 离线/无 remote → 回退本地默认分支 + warning,不阻断 Create。
- **[migration 兼容]** `active_worktree_id` 加列默认 NULL,旧 session 行为不变(无 active worktree = 普通目录模式)。

## Migration Plan

1. 新增 migration vN:`ALTER TABLE sessions ADD COLUMN active_worktree_id TEXT DEFAULT NULL`。
2. `.gitignore` 追加 `.claude/worktrees/`(若未忽略)。
3. 部署:Manager 在 DB 可用时构造,`cfg.WORKTREE_ENABLED`(默认 true)控制是否允许 `worktree/create`;false 时 tool/API 返回错误,但不影响普通目录模式。
4. 回滚:drop 列 + 删 `.claude/worktrees/` 目录即可;worktree 是叠加的主动能力,移除后系统退回普通目录模式,存量 session 零感知。

## Open Questions

- per-run workdir holder 的共享形态:是放进 `AgentDeps`(run 间共享,但 holder 本身是 run-scoped 实例)还是放进 `AgentRunSpec`(每次 run 新建)?倾向 holder 实例 run-scoped、经 `AgentDeps` 注入其构造器,实施时确认与现有 `deps()` 模式一致。
- `worktree/create` 是否需要审批(approval-flow)?初版不做,作为高风险动作可在后续接入 `approve_sub_agent_action` 同款机制;本变更先让它像 `write_file` 一样直接执行。
- 子 agent(leader dispatch)共享父 session worktree 的透传链路需在 runner 接线时实测验证。
