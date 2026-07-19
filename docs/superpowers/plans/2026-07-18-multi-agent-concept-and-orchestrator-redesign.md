# 多 Agent 协作概念重构与主 Agent 调度规划

> **给 agentic worker 的工作指引：** 必备子技能：使用 `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans` 来按 task 逐步实现本 plan。步骤使用 checkbox（`- [ ]`）语法进行跟踪。

**目标：** 明确 `Session / Task / SubTask / Agent / AgentBus / Workflow / Turn / Step` 的边界与层级关系，引入**主 Agent（Leader）调度**模型，解决当前多 Agent 共享 `task_id` 导致的事件/步骤/上下文窗口混乱问题，并给出分 Phase 实现路径。

**状态：** 规划文档，已审阅并细化权限模型。Task 7 由此升级为一个多 Phase 重构任务。

---

## 1. 核心概念定义

| 概念 | 定义 | 标识 | 是否持久化 | 层级 |
|------|------|------|------------|------|
| **Session** | 用户与系统的一次浏览器连接，承载多轮对话与 UI 状态 | `session_id` | ✅ `sessions` 表 | 顶层 |
| **Task（主任务 / Root Task）** | 一次用户请求对应的根执行单元，是计费、回放、聚合的根节点 | `task_id` | ✅ `tasks` + `task_meta` 表 | 次于 Session |
| **SubTask** | 一个 Agent 的一次具体执行实例，是 Task 内部的子节点 | `sub_task_id` | ✅ `tasks` + `task_meta` 表 | 在 Task 之下 |
| **Agent** | 静态配置实体，定义 system prompt、model、allowed tools 等 | `agent_id` | 运行时配置 / 可选模板表 | 跨 Task 复用 |
| **Step** | Agent（SubTask）内部 ReAct Loop 的一次迭代 | `step_index`（SubTask 内唯一） | ✅ `steps` 表 | 在 SubTask 内部 |
| **AgentBus** | Agent 之间的消息总线，一次消息传递产生可观测事件 | `from_agent_id` / `to_agent_id` | ✅ `agent_messages` 表 | 跨 SubTask |
| **Workflow** | 主 Agent 对子 Agent 的调度策略与编排描述 | `workflow_id` / 内联 `strategy` | 配置 + 执行日志 | 在 Task 内部 |
| **Turn** | 前端时间轴的"一轮用户输入 → 一次系统响应"单元 | 数组下标 | ❌ 纯前端 | 前端概念 |

### 1.1 核心原则

1. **一个 Session 包含多个 Task。**
2. **一个 Task 包含一棵 SubTask 树。**
3. **SubTask 0 = 主 Agent 的 SubTask，也等同于 Root Task 本身。**
4. **后续 SubTask 的 `parent_task_id` 都指向同一个 Root Task ID（不是链式继承）。**
5. **Agent 是配置模板；SubTask 是运行实例。**
6. **主 Agent（Leader）拥有 Workflow 定义权与子 Agent 派发权；子 Agent 不能递归派生子 Agent。**
7. **AgentBus 只存在于一次 Task 内部，不跨 Task。**
8. **Leader Agent 同时是该 Task 的子 Agent 动作审批者（可替代 user 审批）。**

---

## 2. 当前实现的问题

### 2.1 Task 与 SubTask 的边界模糊

- `orchestrator.go:346` 当前用 `subTaskID := rootTaskID + "_" + spec.AgentID` 生成子任务 ID。
- 但 `EngineConfig.ParentTaskID` / `IsRoot` 已经存在，`SaveTaskMeta` 也支持 `parent_task_id`。
- 前端事件仍然主要按 `task_id` 路由，`AgentTree` 靠 `agent_id` 分组，无法精确表达"主 Agent 的 SubTask"与"子 Agent 的 SubTask"。
- `ContextWindowPanel` 绑定的是 root `task_id`，多 Agent 下只能展示第一个 Agent 的快照。

### 2.2 Workflow 不是 Agent 行为

- 当前 `WorkflowConfig` 由 `MultiAgentWorkflowEditor.vue` 编辑，直接通过 `/api/multi-agent` 提交给 orchestrator。
- orchestrator 根据 `strategy` 直接创建并运行子 Agent。
- **问题**：调度逻辑不可观测，用户看不到"为什么这样派生子 Agent"。
- 目标：把 Workflow 变成**主 Agent 的 ReAct Loop 内的决策产物**，调度过程本身产生 step 与 tool_call 事件。

### 2.3 缺少主/子 Agent 权限模型

- 当前所有 Agent 平级，没有"谁能派生子 Agent"的约束。
- `run_shell` / `write_file` 等工具已有 PolicyGate，但"派生子 Agent"还没有对应的权限门。
- 风险：子 Agent 理论上可能无限递归派生，导致任务爆炸。
- **新增**：子 Agent 的危险工具审批希望由 Leader Agent 自主判定，而不是每次都需要用户介入，否则多 Agent 协作失去意义。

### 2.4 AgentBus 与 Step 的关系未显式表达

- AgentBus 消息产生 `agent_message_sent` / `agent_message_received` 事件。
- 但这些事件与 ReAct Step 是并行流，没有绑定到"哪个 SubTask 因为收到消息而进入下一步"。
- 目标：AgentBus 消息应作为目标 SubTask 的**输入事件**，触发或记录到该 SubTask 的 step 上下文里。

---

## 3. 目标架构设计

### 3.1 层级模型

```text
Session (session_id)
  └── Task (task_id = root_task_id)
        └── SubTask 0: Leader Agent (agent_id = "leader" / 主 Agent)
              │    ├── Step 0: think
              │    ├── Step 1: tool_call dispatch_sub_agent
              │    └── Step N: final_summary
              │
              ├── SubTask 1: Child Agent A (agent_id = "researcher")
              │    ├── Step 0: think
              │    ├── Step 1: tool_call run_shell
              │    └── Step N: observation
              │
              ├── SubTask 2: Child Agent B (agent_id = "writer")
              │    └── ...
              └── SubTask 3: Child Agent C (agent_id = "reviewer")
                   └── ...
```

- **SubTask 0** 既是 Root Task，也是主 Agent 的运行实例。
- 所有 Child SubTask 的 `parent_task_id` 都等于 Root Task ID。
- 主 Agent 的 SubTask 具有 `is_root = true`，子 Agent 的 SubTask 具有 `is_root = false`。

### 3.2 主 Agent（Leader）职责

| 能力 | 子 Agent 是否具备 | 说明 |
|------|------------------|------|
| 理解用户原始请求 | ✅ | 所有 Agent 都具备 |
| 决定 Workflow 策略 | ✅ 仅主 Agent | parallel / sequential / pipeline |
| 定义与派发子 Agent | ✅ 仅主 Agent | 通过 `dispatch_sub_agent` 工具 |
| 接收 AgentBus 结果汇总 | ✅ 仅主 Agent | 其他子 Agent 默认向主 Agent 汇报 |
| 与用户交互 / 产出最终答案 | ✅ 仅主 Agent | 最终 response 来自主 Agent |
| 调用普通工具 | ✅ | run_shell / write_file / read_file 等 |
| 审批子 Agent 的危险动作 | ✅ 仅主 Agent | 替代 user 成为子 Agent 动作审批者 |
| 再次派生子 Agent | ❌ | 子 Agent 的 `allowedTools` 不包含 `dispatch_sub_agent` |
| 定义 Workflow | ❌ | Workflow 只能是主 Agent 的输出 |

### 3.3 Workflow 是主 Agent 的产物

```text
User Input
  → Leader SubTask starts
    → Leader thinks
    → Leader calls tool dispatch_sub_agent(specs, strategy)
      → Orchestrator validates permission
      → Orchestrator creates Child SubTasks
      → Child SubTasks run in parallel/sequential/pipeline
      → Child SubTasks send results back to Leader via AgentBus
    → Leader observes results
    → Leader decides next action or produces final answer
```

**关键变化**：
- `dispatch_sub_agent` 成为一个真实 Tool，出现在主 Agent 的 tool_call 事件里。
- Orchestrator 不再是"车间主任"，而是**主 Agent 的调度工具**。
- 前端可以看到 Leader 为"什么"、"怎么"派生子 Agent。

### 3.4 审批模型：Leader 作为子 Agent 动作审批者

当前系统的审批流程：

```text
子 Agent 调用危险工具
  → Engine 创建 pause/approval 事件
  → 前端弹窗请求 user 审批
  → user 批准后工具才执行
```

多 Agent 模式下，每个子 Agent 的每个 `run_shell` / `write_file` 都等待 user 审批，效率极低。**应允许 Leader Agent 代替 user 审批其直属子 Agent 的动作**。

新的审批流程（按审批来源优先级）：

| 优先级 | 审批来源 | 适用场景 |
|--------|----------|----------|
| 1 | 系统自动通过 | 读操作、低风险白名单工具 |
| 2 | Leader Agent 审批 | 子 Agent 调用危险工具，由 Leader 自主判断是否批准 |
| 3 | User 审批 | Leader 本身调用危险工具，或未配置 Leader 审批时 |

**实现要点**：

1. **审批作用域按 SubTask 区分**：Leader 只能审批它自己派生的 Child SubTask，不能审批其他 Task 的 SubTask。
2. **Leader 审批是一个内部 LLM 调用**：将子 Agent 的 tool_call 内容发送给 Leader，Leader 以 tool_call `approve_sub_agent_action` 或 `reject_sub_agent_action` 回复。
3. **审批结果持久化**：将 Leader 的审批决定记录为一条特殊 step 或 agent_message，便于审计与回放。
4. **失败回退**：如果 Leader 审批失败（如 Leader 已结束），回退到 user 审批或拒绝。

审批事件流转：

```text
Child SubTask 调用 run_shell
  → Engine.PauseForApproval 检查子 Agent 的审批代理者
    → 若该 SubTask 的 SupervisorSubTaskID = Leader.SubTaskID
      → 构造审批请求发给 Leader
      → Leader 调用 approve_sub_agent_action / reject_sub_agent_action
      → 结果写回 Child SubTask 的 approval channel
    → 若无 Supervisor 或 Leader 不可用
      → 回退到 user 审批
```

**数据模型扩展**：

- `EngineConfig` 增加 `SupervisorSubTaskID string`：本 SubTask 的审批代理者（子 Agent 指向 Leader，Leader 为空）。
- `ApprovalRequest` 增加 `delegated_to_leader bool` 与 `leader_decision_event_id string` 字段。

### 3.5 AgentBus 通信模型

| 通信方向 | 是否允许 | 说明 |
|----------|----------|------|
| Child → Leader | ✅ 默认 | 子 Agent 完成或中间结果汇报给主 Agent |
| Leader → Child | ✅ 显式授权 | 主 Agent 可以发送指令、补充上下文 |
| Child → Child | ❌ 默认禁止 | 需要主 Agent 显式授权（通过 Workflow 配置 `OutputTo`） |
| 跨 Task | ❌ | AgentBus 按 Task 隔离，Task 结束后消息队列清空 |

- AgentBus 消息持久化到 `agent_messages` 表时已包含 `task_id`、`from_sub_task_id`、`to_sub_task_id`。
- 收到 AgentBus 消息的 SubTask，会把消息内容作为一条 `user` 角色消息追加到 conversation，并触发一个 `step_started`。

### 3.6 SubTask 隔离要求

| 资源 | 隔离级别 |
|------|----------|
| 对话历史 (messages) | SubTask 级独立 |
| Context Window 快照 | SubTask 级独立 |
| Step 序列 | SubTask 级独立 |
| Cancel / Pause / Resume | SubTask 级独立 |
| Token 统计 | SubTask 级独立，Task 级聚合 |
| Checkpoints | SubTask 级独立 |
| AgentBus 消息收件箱 | 按 `agent_id` + `sub_task_id` 注册 |
| 审批代理关系 | SubTask 级：Leader 是 Child SubTask 的 Supervisor |

---

## 4. 数据库 Schema 调整

### 4.1 `task_meta` 表已支持

当前 `pkg/db/database.go` 已有 `task_meta` 表（或等效结构），包含 `parent_task_id` / `is_root`。需要确保：
- 主 Agent SubTask 的 `is_root = true`。
- 子 Agent SubTask 的 `parent_task_id = root_task_id`（不是 chain 式继承）。

### 4.2 `agent_messages` 表增强

```sql
ALTER TABLE agent_messages ADD COLUMN from_sub_task_id TEXT;
ALTER TABLE agent_messages ADD COLUMN to_sub_task_id TEXT;
CREATE INDEX IF NOT EXISTS idx_agent_messages_subtasks
  ON agent_messages(task_id, from_sub_task_id, to_sub_task_id);
```

### 4.3 审批记录增强

```sql
ALTER TABLE approvals ADD COLUMN delegated_to_leader BOOLEAN DEFAULT 0;
ALTER TABLE approvals ADD COLUMN leader_sub_task_id TEXT;
ALTER TABLE approvals ADD COLUMN leader_decision_step_id TEXT;
```

### 4.4 新增 `sub_task_meta` 表（可选，若 `task_meta` 不够表达）

若 `task_meta` 以 `task_id` 为 key 已经能覆盖 SubTask，则无需新表。否则：

```sql
CREATE TABLE IF NOT EXISTS sub_task_meta (
    sub_task_id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    parent_task_id TEXT NOT NULL,
    supervisor_sub_task_id TEXT,
    is_root BOOLEAN NOT NULL DEFAULT 0,
    strategy_hint TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sub_task_meta_task_id ON sub_task_meta(task_id);
```

---

## 5. 实施 Phase 规划

### Phase A: SubTask 身份显式化（短期）

**目标**：让 SubTask 有显式 ID，前端事件、Context Window、Cancel/Pause 都能精确到 SubTask。

**状态：已完成。** Commit: `Phase 7-G: merge subtask identity and per-SubTask context window into main`

---

### Phase B: 主 Agent 调度架构（中期）

**目标**：把 Workflow 从 orchestrator 直接执行，改为"主 Agent 作为 Leader 通过 tool_call 触发"。

**文件：**
- `internal/tool/builtin.go`（新增 `dispatch_sub_agent` tool）
- `internal/runtime/engine.go`
- `internal/orchestrator/orchestrator.go`
- `cmd/server/main.go`
- `web/src/components/MultiAgentWorkflowEditor.vue`
- `web/src/composables/useTaskStore.ts`

**拆分步骤：**

- [ ] **Step B.1: 设计 `dispatch_sub_agent` Tool**

  ```go
  type DispatchSubAgentInput struct {
      Reason   string      `json:"reason"`
      Strategy string      `json:"strategy"`
      Agents   []AgentSpec `json:"agents"`
  }
  ```

  Tool description：
  > "Dispatch one or more sub-agents to solve parts of the current task. Only the leader agent may call this tool. The orchestrator will create child sub-tasks, run them according to the strategy, and feed their results back to you via AgentBus."

- [ ] **Step B.2: 权限校验**

  在 Tool Registry 执行 `dispatch_sub_agent` 前，检查 EngineConfig 的 role / permission 字段：

  ```go
  if !engine.cfg.CanDispatchSubAgents {
      return nil, fmt.Errorf("sub-agent dispatch is only allowed for the leader agent")
  }
  ```

  主 Agent 的 `EngineConfig.CanDispatchSubAgents = true`，子 Agent 为 `false`。

- [ ] **Step B.3: Orchestrator 作为 Tool 回调**

  `dispatch_sub_agent` 不是一个普通函数工具，而是需要一个回调：

  ```go
  type SubAgentDispatcher interface {
      Dispatch(ctx context.Context, leaderSubTaskID string, strategy string, agents []AgentSpec) ([]AgentResult, error)
  }
  ```

  在 `cmd/server/main.go` 或新文件里实现该接口，注入到 Tool Registry。

- [ ] **Step B.4: 主 Agent 默认 Workflow**

  当用户开启 Multi-Agent 模式但没打开 Workflow Editor 时，默认让 Leader Agent 自己决定是否派生（decides whether to dispatch）。

  若需要向后兼容"用户直接指定子 Agent"模式，保留 `/api/multi-agent` 原有入口，但它在内部也会先启动一个 Leader SubTask，由 Leader 调用 `dispatch_sub_agent`。

- [ ] **Step B.5: 前端 Workflow Editor 生成 Leader Prompt**

  Workflow Editor 保存的配置不再直接 POST 给 `/api/multi-agent`，而是作为**用户给主 Agent 的上下文提示**（或作为 Leader 的 system prompt 补充），让 Leader 在运行时决定如何 dispatch。

  保留一个"强制按配置执行"的开关：若开启，Leader 直接按配置调用 `dispatch_sub_agent`。

- [ ] **Step B.6: 验证与提交**

  - `go test ./...`
  - 跑一个由 Leader 派生 researcher/writer/reviewer 的任务，验证 Leader 的 tool_call 中能看到 `dispatch_sub_agent`。
  - 前端可预览 Leader 的调度决策。

  Commit: `Phase 7-H: leader-agent driven sub-task dispatch`

---

### Phase C: 权限约束模型（中期）

**目标**：建立主/子 Agent 的权限边界，防止子 Agent 递归派生；并引入 Leader 作为子 Agent 动作审批者。

**文件：**
- `internal/agent/agent.go`
- `internal/runtime/engine.go`
- `internal/runtime/approval.go`（新增或改造）
- `internal/tool/builtin.go`
- `internal/orchestrator/orchestrator.go`
- `internal/harness/policy.go`（可选，新增 rule）
- `pkg/db/database.go`

**拆分步骤：**

- [ ] **Step C.1: Agent 角色定义**

  ```go
  type AgentRole string

  const (
      AgentRoleLeader AgentRole = "leader"
      AgentRoleWorker AgentRole = "worker"
  )
  ```

  `Agent` 结构体增加 `Role AgentRole`。

- [ ] **Step C.2: EngineConfig 增加权限与审批代理字段**

  ```go
  type EngineConfig struct {
      // ...
      Role                 AgentRole
      CanDispatchSubAgents bool
      CanDefineWorkflow    bool
      SupervisorSubTaskID  string // 审批代理者 SubTaskID；Leader 为空，子 Agent 指向 Leader
      ApproverMode         string // "user" | "leader" | "auto"
  }
  ```

  初始化时根据 Role 自动设置：
  - `leader`：`CanDispatchSubAgents = true`, `CanDefineWorkflow = true`, `ApproverMode = "user"`
  - `worker`：全部为 `false`，`ApproverMode = "leader"`

- [ ] **Step C.3: Tool 层权限拦截**

  `dispatch_sub_agent` tool 执行时校验 `CanDispatchSubAgents`。
  未来若子 Agent 可能获得"代理"能力，需显式授权并在 `allowedTools` 中列明。

- [ ] **Step C.4: Leader 审批子 Agent 动作**

  1. 在 `Engine.PauseForApproval` 中，若当前 Engine 是 Worker 且 `ApproverMode == "leader"`，则将审批请求转发给 `SupervisorSubTaskID` 对应的 Leader Engine。
  2. Leader Engine 收到审批请求后，作为一次特殊的 tool_call 调用 `approve_sub_agent_action` / `reject_sub_agent_action`。
  3. Leader 的审批结果写回 Worker Engine 的 approval channel，继续执行。
  4. 审批事件 `approval_delegated` / `approval_decided_by_leader` 发射到前端。

- [ ] **Step C.5: 数据库持久化调整**

  在 `approvals` 表中增加 `delegated_to_leader`、`leader_sub_task_id`、`leader_decision_step_id` 字段。

- [ ] **Step C.6: 验证与提交**

  - 单测：子 Agent 调用 `dispatch_sub_agent` 必须失败。
  - 单测：主 Agent 调用 `dispatch_sub_agent` 成功。
  - 单测：子 Agent 的 Worker 调用危险工具时，Leader 能收到审批请求并返回批准/拒绝。

  Commit: `Phase 7-I: leader-only sub-agent dispatch permission model and leader delegation for approvals`

---

### Phase D: AgentBus 与 Step 的绑定（长期）

**目标**：让 AgentBus 消息作为 SubTask 的输入事件，可被 Step 序列追踪。

**文件：**
- `internal/runtime/engine.go`
- `internal/orchestrator/agentbus.go`
- `pkg/db/database.go`
- `web/src/components/AgentBusTimeline.vue`

**拆分步骤：**

- [ ] **Step D.1: AgentBus 消息按 SubTask 路由**

  `AgentBus.RegisterHandler` 增加 `subTaskID` 参数，消息应投递到 `(agentID, subTaskID)` 对。

- [ ] **Step D.2: 收到 AgentBus 消息触发 Step**

  在 Engine 的 AgentBus listener 中，收到消息后：
  1. 追加为 `user` 消息。
  2. 发射 `step_started`（type: `agent_message_input`）事件。
  3. 继续 ReAct Loop。

- [ ] **Step D.3: 持久化 `from_sub_task_id` / `to_sub_task_id`**

  `db.InsertAgentMessage` 增加 subTask 字段。

- [ ] **Step D.4: 验证与提交**

  Commit: `Phase 7-J: bind agent-bus messages to sub-task step timeline`

---

## 6. 概念区分速查表

| 问题 | 答案 |
|------|------|
| Agent 和 SubTask 什么关系？ | Agent 是配置模板；SubTask 是 Agent 的一次运行实例。 |
| SubTask 0 是什么？ | 主 Agent / Leader Agent 的实例，也是 Root Task 本身。 |
| 子 SubTask 的 parent_task_id 指向谁？ | 统一指向 Root Task ID，不指向前一个 SubTask。 |
| Workflow 由谁管理？ | 主 Agent。子 Agent 没有定义或派发 Workflow 的权限。 |
| AgentBus 消息能不能跨 Task？ | 不能。AgentBus 按 Task 隔离。 |
| 子 Agent 能派生子 Agent 吗？ | 默认不能。只有主 Agent 可以。 |
| 子 Agent 的危险动作由谁审批？ | 默认由 Leader Agent 审批；Leader 自身动作由 user 审批。 |
| Turn 和 Task 什么关系？ | Turn 是前端一轮交互；一次 Turn 可能包含一个完整的 Task（含多个 SubTask）。 |
| Step 属于谁？ | Step 属于某个具体的 SubTask，在 SubTask 内部编号。 |
| Context Window 应该按什么粒度展示？ | 按 SubTask（即 Agent 实例）展示，未来支持按 Step 回溯。 |

---

## 7. 端到端验证场景

### 场景 1：主 Agent 自动规划 multi-agent 任务

1. 用户输入："用 Go 写一个快速排序，并写单元测试，再 review 一下。"
2. 系统创建 Root Task 和 Leader SubTask。
3. Leader 思考后调用 `dispatch_sub_agent`：
   - strategy: `sequential`
   - agents: `[coder, reviewer]`
4. Orchestrator 创建 SubTask 1 (coder) 和 SubTask 2 (reviewer)。
5. coder 完成后通过 AgentBus 把结果发给 Leader。
6. Leader 观测结果，再决定是否需要 reviewer（或自动把结果转发给 reviewer）。
7. reviewer 完成后把结果发回 Leader。
8. Leader 汇总并给出最终回答。

### 场景 2：独立 Context Window

1. 多 Agent 任务运行中。
2. 用户打开 ContextWindowPanel。
3. Panel 顶部下拉框列出：`leader`、`coder`、`reviewer`。
4. 选择 `coder` 后，展示 coder SubTask 当前的 system prompt + messages + token 占用。

### 场景 3：子 Agent 不能越权

1. 某个子 Agent 在 ReAct Loop 中尝试调用 `dispatch_sub_agent`。
2. Tool Registry 返回 `ErrBlockedByPolicy`。
3. Engine 将错误作为 observation 反馈给 LLM，不终止任务（除非连续重复错误）。

### 场景 4：Leader 审批子 Agent 动作

1. 子 Agent coder 调用 `write_file`。
2. Engine 检查到该 SubTask 的 Supervisor 是 Leader，创建 `approval_delegated` 事件。
3. Leader 收到审批请求，调用 `approve_sub_agent_action`。
4. coder 的 tool 继续执行，并生成 `approval_decided_by_leader` 事件。
5. 前端时间线显示审批由 Leader 代理完成。

---

## 8. 风险与回滚

| 风险 | 缓解 |
|------|------|
| SubTask ID 改造影响已有任务回放 | 保留 rootTaskID 字段不变，新增 subTaskID 字段；旧数据按 rootTaskID 仍可读。 |
| Leader 调度增加一次 LLM 调用，延迟更高 | Leader 可对简单任务直接回答，不走 dispatch；支持配置强制 Workflow 减少 LLM 决策。 |
| 子 Agent 权限误判导致正常任务失败 | `CanDispatchSubAgents` 默认对 leader=true，worker=false；有显式日志和事件。 |
| AgentBus 按 SubTask 路由破坏现有 handler | 保留按 `agent_id` 注册的 fallback，新逻辑优先按 `(agentID, subTaskID)` 匹配。 |
| Leader 审批引入循环或死锁 | 审批转发使用超时机制；Leader 不可用自动回退 user 审批。 |
| Leader 审批结果不可解释 | 所有 Leader 审批决定作为 step / agent_message 持久化，支持回放。 |

---

## 9. 与前期任务的衔接

- `Phase 7-A` ~ `Phase 7-F`（已提交）为本规划奠定了基础：per-agent cancel/pause/resume、AgentBus 持久化、LLM 分解、Workflow Editor、AgentBus 可视化。
- `Phase 7-G`（已合并到 main）完成了 SubTask 身份显式化与 Context Window 隔离。
- `Phase 7-H` ~ `Phase 7-J` 为本规划要实现的后续任务。
- Task 7 的 scope 从"补一个 context window 选择器"升级为"SubTask 身份显式化 + 主 Agent 调度 + 权限模型 + AgentBus 绑定"。
