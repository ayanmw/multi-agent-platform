# 隔离白皮书 — 白盒多 Agent 协作平台（E3 多租户与隔离）

> 对应里程碑：N3-02（E3 隔离边界增强）。本文档明确当前三层隔离边界
> （session / worktree / workdir）的实现机制、scope 键贯穿约定，以及已知限制。
> 配套代码改动：`internal/ws/hub.go`（事件路由会话级订阅）、
> `internal/observability/audit_scope_test.go`、`internal/ws/hub_session_scope_test.go`、
> `internal/harness/recall_isolation_test.go`。

---

## 1. 隔离目标

平台在**单进程、单 SQLite 数据库**部署模型下，必须保证：

1. **会话隔离**：session A 的对话、文件、memory、事件、审计轨迹，绝不以任何
   查询路径泄漏到 session B（或任何未授权观察者）。
2. **执行隔离**：agent 的工具执行被限制在自身 worktree / workdir 内，无法越界
   读写其它 session 的文件系统。
3. **审计可追溯**：每一条写操作都带可检索的 scope 键，便于按资源/会话复盘。

> 注意：当前**没有多租户 / org 抽象**（grep 无 `tenant` 引用）。所有 session
> 共享同一数据库与进程。本白皮书描述的是「单部署内的会话级隔离」，
> 而非「跨组织多租户隔离」。后者属于未来里程碑（N3 之后）。

---

## 2. 三层隔离边界

### 2.1 第一层 — Session（逻辑隔离，数据库行级）

- 所有核心实体都以 `session_id` 为外键/索引维度：
  `sessions`、`session_messages`、`tasks(session_id)`、`todos(session_id)`、
  `memories(scope=session, session_id)`、`cost_records(session_id)`、
  `agent_bus` 消息按 `(agent_id, sub_task_id)` 路由且每条 session 绑定独立 root task。
- **会话创建即绑定**：`resolveSession` 保证 task ↔ session 一一对应；
  `tasks_api.go` / `runner.go` 在 run 启动时解析并贯穿 `session_id`。
- **scope 键贯穿**：memory 的 `scope`（session/project/global）+ `session_id`
  双字段编码，使 recall SQL 以 `session_id` 为 WHERE 参数（`pkg/db/memory_scope.go`
  `QueryMemoriesByScopeAndSession`），跨 session 召回在 SQL 层即被阻断。
- 验证：`internal/harness/recall_isolation_test.go`
  （sess-B 的私有 memory 不会泄漏进 sess-A 的 Working Memory）。

### 2.2 第二层 — Worktree（文件系统隔离，git 级）

- `internal/workspace/Manager` 为**每个 session** 创建独立的 git worktree
  （`worktree/create` 工具），session 间文件系统互不相交。
- worktree 状态经 `store.SetActiveWorktree(sessionID, wtID)` 绑定 session，
  防止同一 session 重复创建；`active_worktree_id` 落在 `sessions` 表。
- 离开 worktree（`worktree/exit`）需经 LLM 工具驱动，REST 仅暴露 create/get，
  避免脏状态风险。

### 2.3 第三层 — Workdir（工具执行隔离，进程级）

- `internal/tool/registry.go` 的 `ExecuteWithCtx` 注入 `ExecuteContext.Workdir`，
  当存在非空 `WorkdirHolder` 时，其值**覆盖** LLM 传入的 `input["workdir"]`。
- 工具 CWD 的唯一事实来源是 `WorkdirHolder`（由 `AgentRunner.Run` 创建，
  worktree 中切换到 worktree 路径）。LLM 无法伪造 workdir 逃逸出会话沙箱。
- 这是「白盒」安全哲学的核心：永不信任 LLM 传入的路径，统一经 holder 解析。

---

## 3. Scope 键贯穿约定（三个路径）

| 路径 | scope 键 | 实现 | 越权拦截点 |
|------|----------|------|-----------|
| **审计 Target** | `<resource>/<id>` | `observability.AuditRecord.Target`（如 `agents/<id>`、`model/<p>/<m>`、`apikey/<id>`、`session/<id>`、`cron/<id>`） | `audit_sqlite.go` 落库 + `api.go` 各写 handler 编码 scope 前缀 |
| **事件路由** | `Data["session_id"]` + 路由键 `task_id/sub_task_id/agent_id` | `internal/ws/hub.go` 广播；N3-02 起支持 client 订阅 `?session_id=` | `Client.clientAcceptsEvent`（详见 §4） |
| **Memory recall** | `scope` + `session_id` | `harness.BuildWorkingMemory` → `db.QueryMemoriesByScopeAndSession(projectID, sessionID, "session")` | SQL WHERE 以 `sessionID` 参数化 |

### 3.1 审计 Target scope 约定

每条审计记录的 `Target` 必须采用 `<resource>/<id>` 形式 —— 资源段在前、实例 ID 在后，
以单个 `/` 分隔。该约定使审计可被「按资源维度检索与隔离」，杜绝无 scope 的笼统
Target 造成的跨资源审计混淆。验证：`internal/observability/audit_scope_test.go`。

> 已知限制：审计记录当前**不带 session 列**（资源维度而非会话维度）。
> 资源型 mutation（agents/tools/models/apikey…）的 scope 已充分；会话级操作
> （session 创建/删除、memory 读写）的审计以 `session/<id>` 前缀编码 Target。
> 若未来需要「按 session 拉取全部审计」，应新增审计 `session_id` 列（schema 迁移）。

---

## 4. 事件路由的会话级隔离（N3-02 新增）

### 4.1 问题

改造前 `Hub` 把**每一条事件广播给所有已连接 client**（无服务端 session 过滤）。
前端 `web/v2` 以单个全局 WebSocket 连接（`/ws`，不带 `session_id`）接收全部事件，
再在客户端按 `evt.data.session_id` 过滤。这意味着：**任何已连接的 WS 客户端
都能在服务端收到其它 session 的实时事件**，属于 E3 跨 session 暴露面。

### 4.2 修复（向后兼容）

- `Client` 新增 `sessionIDs []string` 订阅集；**空 = 接收全部（legacy 行为）**。
- `ServeWS` 读 `?session_id=`（逗号分隔），非空则该连接仅收命中事件。
- `Client.clientAcceptsEvent`：未订阅 → 收全部；已订阅 → 仅当事件携带非空
  `session_id` 且命中订阅集时接收；无 `session_id` 的系统事件只投给 legacy client。
- 默认 Web UI **不受影响**（仍连 `/ws` 不带参数 → 收全部 → 客户端过滤）。
- 验证：`internal/ws/hub_session_scope_test.go`。

### 4.3 已知限制（务必知晓）

- 前端全局连接模型未变：单连接看多个 session，仍依赖客户端过滤。服务端
  session 订阅是**opt-in 能力**，当前仅 `scripts/ws-smoke.go` 等 API/工具消费者使用。
- 要从 Web UI 端彻底闭环，需要前端改为「按当前 session 重连 `/ws?session_id=`」
  或动态订阅 —— 属前端结构性改动，留待后续里程碑（已在 N3 评审记录标注）。
- 在 `REQUIRE_AUTH=true` 的生产姿态下，未认证连接本就被拒，暴露面显著降低；
  结合 N3-01 的特权路由最小暴露，纵深防御成立。

---

## 5. 静态审查结论（无跨 session 查询路径）

逐路径核查关键读路径是否以 `session_id` 参数化或受 holder 约束：

- **Memory recall**：✅ `QueryMemoriesByScopeAndSession(projectID, sessionID, "session")`
  以 `sessionID` 为 WHERE 参数；project/global 层级本身即共享语义（设计预期）。
- **Todo / Cost / Tasks**：✅ 均以 `session_id` 为查询参数或外键（`api_todo.go`、
  `cost/repository.go`、`tasks_api.go`）。
- **工具 workdir**：✅ 经 `WorkdirHolder` 覆盖，LLM 无法伪造路径逃逸。
- **审计 Target**：⚠️ 资源维度 scope 充分；会话级操作以 `session/<id>` 前缀编码，
  缺独立 session 列（见 §3.1 限制）。
- **WebSocket 事件广播**：⚠️→✅ N3-02 起支持服务端 session 订阅（opt-in），
  默认 Web UI 仍客户端过滤（见 §4 限制）。

**结论**：在「单部署会话级隔离」目标下，未发现未加 scope 的跨 session 查询路径；
审计与事件路由的两处 ⚠️ 为已知设计权衡，已在本文档显式记录，不视为实现缺陷。

---

## 6. 未来增强方向（非本任务）

1. 审计新增 `session_id` 列 + 按 session 检索端点。
2. 前端 WebSocket 改为按 session 订阅，彻底闭环事件路由隔离。
3. 多租户 / org 抽象（独立 schema 或独立 DB 实例），支撑跨组织隔离。
4. Worktree 配额与生命周期自动回收（孤儿扫描已在 `main.go` 实现）。
