# Cron / 定时器系统 设计文档

> 状态：Draft（brainstorming 产出，待 writing-plans 转为实现计划）
> 日期：2026-07-21
> 关联模块：`internal/cron/`（新增）、`cmd/server/main.go`（接入）、`web/v2`（前端管理页）

## 1. 目标

为多 Agent 平台新增一套**定时器系统**，作为与 `skill` / `tool` / `memory` 平级的独立子系统：

- **LLM/Agent 可在运行时通过 tool 创建定时回调**，定时触发新一轮 task / session（核心权重路径）。
- **用户通过 Web UI 完全可见、可管理**：列表、新增、编辑、启用/禁用/暂停、手动触发、查看执行历史。
- **除定时发起 task/session 外**，也能定时执行脚本（白名单 tool 调用）、向 session 发通知、调用外部 webhook。
- **完全事件化**：所有状态变更与每次触发都通过现有 Event Bus 广播 `cron_*` 事件，前端实时可见。

## 2. 范围

| 项 | 决策 |
|----|------|
| 拥有者 | Agent tool 创建 + 用户 UI 创建，**统一存储、无权限区分** |
| 执行语义 | 既可在已有 session 新增一轮 task，也可自动新建 session；**session 失效则执行失败** |
| 调度规则 | 先支持 **cron 表达式 / interval / once** 三种；自然语言后续（NLP Phase +1） |
| 调度精度 | **秒级**（robfig/cron 6 域：秒 分 时 日 月 周） |
| 表达式方言 | 后端统一存储为标准 cron 表达式；前端可显示 `display_type`（preset/interval/cron），切换为自由 cron 后不再回退 preset |
| 执行者 | action_payload 内显式配置 `agent_id`（必填）+ `session_id`（可选：空则新建，非空则复用，失效则失败） |
| 触发身份 | 暂不绑 owner，`user_input` 前加 `[cron:<id>:<name>]` 标记；预留 `owner` 字段供 RBAC |
| Action 类型 | `start_task` / `script` / `webhook` / `notify_session`，**首期四种全做**；payload 开放、预留接口后续扩展 |
| 脚本安全 | script action 只允许**白名单 tool 调用**，复用现有 run_shell sandbox / approval policy |
| 并发控制 | 每个 cron 可配 `allow_concurrent`（默认 false = 串行；重叠触发记 `skipped`） |
| 模板变量 | user_input 支持占位符：`{{.Now}}` `{{.PrevTrigger}}` `{{.PrevStatus}}` `{{.PrevResult}}`，渲染失败保留占位符 |
| 错误策略 | 失败后继续 + 记录到 executions + 广播 `cron_execution_failed` |
| 离线错过 | 服务重启后**只记录错过不补跑**，发 `cron_missed` 事件 |
| 同步时机 | server 启动全量加载 enabled cron 到 scheduler + CRUD 后**增量同步**（单条增/删/改） |
| 可观测 | **完全事件化**：`cron_triggered` / `cron_execution_started|completed|failed|skipped` / `cron_created|updated|deleted|enabled|disabled|paused|missed` |
| 前端范围 | **只做 v2**；v1 不动 |
| 前端形态 | **两处**：① 侧边只读 dock 面板（看本 session 相关 cron + 触发流，可跳转）；② ManageFlyout 新增 `cron` tab（全量管理：CRUD + 执行历史） |
| 执行历史 | executions 表**全量保留**，前端按 cron/状态/时间过滤，提供手动清理 API |
| 测试 | 后端单元 + mock 集成 + real_llm smoke + 前端组件 `.test.ts`，全覆盖 |

## 3. 架构

```
┌─────────────────────────────────────────────────────┐
│                  Cron 子系统 (internal/cron/)         │
│                                                      │
│  ┌──────────┐   ┌───────────┐   ┌────────────────┐  │
│  │ Scheduler│──>│  Executor │──>│ Action Runner  │  │
│  │robfig/cron│  │(事件+串行)│   │4 种 action_type│  │
│  └────┬─────┘   └─────┬─────┘   └───────┬────────┘  │
│       │               │                 │            │
│       │          ┌────▼─────┐           │            │
│       └─────────>│  Store   │<──────────┘            │
│                  │ (SQLite) │                        │
│                  └──────────┘                        │
│                                                      │
│  对外三面：REST API / Agent Tools / Event Bus        │
└─────────────────────────────────────────────────────┘
        │              │                  │
        ▼              ▼                  ▼
   前端管理页     LLM 运行时创建      WS 广播给前端
   (v2 dock +    (cron/create         (cron_* 事件)
    ManageTab)    /list/delete/trigger)
```

### 3.1 组件职责

| 组件 | 职责 | 依赖 |
|------|------|------|
| `Scheduler` | 用 robfig/cron/v3 维护在内存中的 cron entry；启动加载 + 增量同步；到点回调 Executor | `Store`、`Executor` |
| `Executor` | 单次触发编排：发 `cron_triggered` 事件 → 串行/skip 判定 → 渲染模板 → 调 Action Runner → 记 execution → 发终态事件 | `Store`、`ActionRunner`、`runtime.EventBus` |
| `ActionRunner` | 按 `action_type` 执行：`start_task`（调 task 启动入口）、`script`（白名单 tool）、`webhook`（HTTP）、`notify_session`（WS + 写 session message） | `tool.Registry`、task 启动函数、`ws.Hub` |
| `Store` | SQLite 持久化 crons / executions 两表 + 查询/过滤/清理 | `db.DB` |
| `Service` | 对外门面：CRUD + 启用/禁用/暂停/手动触发 + CRUD 后通知 Scheduler 增量同步 | `Store`、`Scheduler` |
| `Tools` | 实现 `tool.Tool` 接口的 4 个 Agent Tools：`cron/create`、`cron/list`、`cron/delete`、`cron/trigger` | `Service` |
| `Handler`（在 cmd/server） | REST API：`/api/crons*` | `Service` |

### 3.2 与现有 task 启动链路的接入

`start_task` action **不复制** `runAgentLoop` 的 20+ 参数链路，而是**复用** `cmd/server` 现有的 task 启动入口。具体做法：

- 把 `handleTasksRoot` 中 `case "chat"` 的核心启动逻辑抽成一个可复用函数 `startChatTask(opts StartTaskOpts) (sessionID, taskID string, err error)`，原 handler 调用它，cron ActionRunner 也调用它。
- `StartTaskOpts` 字段：`AgentID`、`Input`、`SessionID`（空则新建）、`SystemPrompt`（可选）、`MaxSteps`、`TimeoutSeconds`、`Scope`、`AllowedTools`、`TokenBudget`、`CostBudgetUSD`、`CaseID`。
- cron 触发时 `Input` 前自动加 `[cron:<id>:<name>] ` 前缀，便于在 task 列表 / trace 中溯源。

这样 cron 不侵入 ReAct Loop，只作为另一个"触发源"接入。

## 4. 数据模型

### 4.1 `crons` 表

```sql
CREATE TABLE IF NOT EXISTS crons (
    id TEXT PRIMARY KEY,                  -- "cron_" + uuid
    name TEXT NOT NULL,                   -- 人类可读名称
    description TEXT DEFAULT '',
    -- 调度规则
    schedule_type TEXT NOT NULL,          -- 'cron' | 'interval' | 'once'
    cron_expr TEXT NOT NULL DEFAULT '',   -- 6 域秒级 cron 表达式（统一存储格式）
    display_type TEXT DEFAULT 'cron',     -- 前端展示用：'preset' | 'interval' | 'cron' | 'once'
    timezone TEXT DEFAULT 'UTC',
    once_at TEXT DEFAULT '',              -- ISO8601 时刻（schedule_type='once' 时用）
    -- 动作
    action_type TEXT NOT NULL,            -- 'start_task' | 'script' | 'webhook' | 'notify_session'
    action_payload JSON NOT NULL DEFAULT '{}',
    -- 状态
    status TEXT NOT NULL DEFAULT 'enabled', -- 'enabled' | 'disabled' | 'paused'
    allow_concurrent BOOLEAN DEFAULT 0,
    -- 来源 / 归属（预留）
    source TEXT DEFAULT 'user',           -- 'user' | 'agent'
    owner TEXT DEFAULT '',                -- 预留 RBAC
    -- 调度状态
    last_triggered_at DATETIME,
    next_trigger_at DATETIME,
    last_execution_id TEXT DEFAULT '',
    trigger_count INTEGER DEFAULT 0,
    -- 时间戳
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_crons_status ON crons(status);
CREATE INDEX IF NOT EXISTS idx_crons_next_trigger ON crons(next_trigger_at);
```

### 4.2 `cron_executions` 表

```sql
CREATE TABLE IF NOT EXISTS cron_executions (
    id TEXT PRIMARY KEY,                  -- "cronexec_" + uuid
    cron_id TEXT NOT NULL,
    triggered_at DATETIME NOT NULL,
    status TEXT NOT NULL,                 -- 'running'|'completed'|'failed'|'skipped'|'missed'
    reason TEXT DEFAULT '',               -- skip/missed/failed 原因
    rendered_input TEXT DEFAULT '',       -- 模板渲染后的实际 input
    result_summary TEXT DEFAULT '',       -- final_result 摘要（截断）
    task_id TEXT DEFAULT '',              -- start_task 产生的 task_id
    session_id TEXT DEFAULT '',
    duration_ms INTEGER DEFAULT 0,
    error TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (cron_id) REFERENCES crons(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_cron_executions_cron_id ON cron_executions(cron_id);
CREATE INDEX IF NOT EXISTS idx_cron_executions_triggered_at ON cron_executions(triggered_at DESC);
CREATE INDEX IF NOT EXISTS idx_cron_executions_status ON cron_executions(status);
```

通过 `pkg/db/migrate.go` 追加一个新 migration version（建表）。

### 4.3 `action_payload` 结构

```jsonc
// start_task
{
  "agent_id": "agent_default",      // 必填
  "session_id": "",                 // 空=新建 session；非空=复用；失效=失败
  "input": "检查服务状态并汇报",     // 支持模板占位符
  "system_prompt": "",              // 可选
  "max_steps": 0,                   // 0=用默认
  "timeout_seconds": 0,
  "scope": "", "allowed_tools": [], "token_budget": 0, "cost_budget_usd": 0,
  "case_id": ""
}

// script —— 白名单 tool 调用，按顺序执行多个 tool_call
{
  "tool_calls": [
    {"tool": "run_shell", "input": {"command": "df -h"}, "approval": false}
  ]
  // tool 名必须在白名单（配置 cron.allowed_tools，默认 run_shell/read_file/write_file/fetch_url）
}

// webhook —— 可配模板 payload
{
  "method": "POST",                 // GET/POST/PUT
  "url": "https://example.com/hook",
  "headers": {"X-Cron-Id": "{{.CronID}}"},
  "body": "{\"triggered_at\":\"{{.Now}}\",\"count\":{{.Count}}}",
  "timeout_seconds": 10
}

// notify_session —— WS 事件 + 写入 session message
{
  "session_id": "sess_xxx",         // 必填
  "message": "[cron] 每日汇报已生成", // 支持模板
  "event_type": "cron_notification" // 可选，默认 cron_notification
}
```

### 4.4 模板变量

所有 action_payload 的字符串字段（以及 start_task 的 input）都过一遍 `text/template`，注入：

| 变量 | 含义 |
|------|------|
| `.Now` | 触发时刻（RFC3339） |
| `.PrevTrigger` | 上次触发时刻（首次为空） |
| `.PrevStatus` | 上次执行状态（首次为空） |
| `.PrevResult` | 上次 final_result 摘要（首次为空） |
| `.Count` | 第几次触发（从 1 开始） |
| `.CronID` / `.CronName` | 当前 cron 元信息 |

渲染失败保留原始占位符，不阻断触发。

## 5. 状态机

```
                create
                  │
                  ▼
              ┌────────┐  disable   ┌──────────┐
              │enabled │──────────>│ disabled │
              └────────┘ <────────── └──────────┘
                  │  enable
                  │ pause
                  ▼
              ┌────────┐  resume    ┌────────┐
              │ paused │ <───────── │(enabled)│
              └────────┘            └────────┘
```

- `enabled`：scheduler 中有对应 entry，到点触发。
- `paused`：scheduler 中**移除** entry，但保留配置，可 resume（区别于 disabled 的语义弱化——实现上 paused 与 disabled 都移除 entry，paused 语义偏"临时"，UI 分开按钮）。
- `disabled`：同 paused，移除 entry。
- 触发时执行态：`running → completed | failed`；重叠时 `skipped`；离线错过 `missed`。

## 6. Agent Tools

实现 `tool.Tool` 接口，注册到 `tool.Registry`，LLM 运行时可调用：

| Tool | 入参 | 行为 |
|------|------|------|
| `cron/create` | name, schedule_type, cron_expr/once_at, action_type, action_payload, allow_concurrent | 创建并启用 |
| `cron/list` | (可选) status 过滤 | 返回当前 cron 列表 |
| `cron/delete` | id | 删除 |
| `cron/trigger` | id, (可选) override_input | 手动触发一次（不影响调度） |

不提供 `cron/update`（首期 YAGNI；用户要改可在 UI 删后重建，或后续补）。

## 7. REST API

```
GET    /api/crons                        # 列表（?status=&action_type=&q=）
POST   /api/crons                        # 创建
GET    /api/crons/:id                    # 详情
PUT    /api/crons/:id                    # 更新
DELETE /api/crons/:id                    # 删除
POST   /api/crons/:id/enable             # 启用
POST   /api/crons/:id/disable            # 禁用
POST   /api/crons/:id/pause              # 暂停
POST   /api/crons/:id/resume             # 恢复
POST   /api/crons/:id/trigger            # 手动触发
GET    /api/crons/:id/executions         # 该 cron 的执行历史（分页 ?limit=&offset=&status=）
GET    /api/crons/executions             # 全局执行历史（分页+过滤）
DELETE /api/crons/executions             # 手动清理（?cron_id=&before=&status=）
```

## 8. 事件

新增 `pkg/event` 常量：

```
cron_created / cron_updated / cron_deleted
cron_enabled / cron_disabled / cron_paused / cron_resumed
cron_triggered               # 到点触发（含 rendered_input 预览）
cron_execution_started
cron_execution_completed     # data: result_summary, task_id, duration_ms
cron_execution_failed        # data: error
cron_execution_skipped       # data: reason（并发重叠）
cron_missed                  # data: missed_count（启动时检测到离线期间错过的）
cron_notification            # notify_session action 发给前端的事件
```

所有事件 `TaskID` 字段填 cron_id（让前端按 cron 维度路由），`AgentID` 填触发的 agent_id（start_task 时）或 `"cron"`。

## 9. 前端（仅 v2）

### 9.1 ManageFlyout 新增 `cron` tab

- `ManageTabs.vue` tabs 数组追加 `{ id: 'cron', label: 'Cron' }`。
- `ManageContent.vue` 在 activeTab==='cron' 时渲染新组件 `CronManager.vue`。
- `CronManager.vue`：
  - 顶部：列表（name / schedule / action_type / status / next_trigger / last_triggered / 操作按钮）。
  - 新增/编辑：`CronForm.vue`（schedule_type 切换 preset↔interval↔cron↔once；action_type 切换 4 种 payload 表单）。
  - 行操作：启用/禁用/暂停/恢复/手动触发/查看历史/删除。
  - 执行历史：`CronExecutions.vue`（按 cron/状态/时间过滤 + 手动清理按钮）。

### 9.2 侧边只读 dock 面板 `CronDockPanel.vue`

- 独立 dock 组件，挂在主聊天区侧边（与现有 DockPanel 风格一致）。
- 只显示**与当前 active session 相关**的 cron（session_id 匹配，或无 session 绑定的全局 cron）。
- 实时显示触发流（订阅 `cron_*` 事件，最近 N 条）。
- 只读：每条有"跳转到 ManageFlyout cron tab 并定位该 cron"按钮。
- dock 可折叠。

### 9.3 composable

- `useCrons.ts`：CRUD + 状态操作，封装 fetch 调用。
- `useCronEvents.ts`：订阅 `cron_*` WS 事件，维护执行流 reactive 列表。

## 10. 错误处理与边界

- cron 表达式非法：创建/更新时 400，存不进 DB。
- action_payload 校验：必填字段缺失（如 start_task 的 agent_id）→ 400。
- session 失效：start_task 执行时 QuerySessionByID 失败 → execution status=failed，reason="session not found"，广播 `cron_execution_failed`，不影响下次调度。
- webhook 超时/非 2xx：execution failed，记录 response status。
- script tool 不在白名单：execution failed，reason="tool not allowed"。
- 模板渲染失败：保留占位符，继续执行（warn 日志）。
- scheduler 启动加载：逐条 parse cron_expr，失败的跳过并记 `cron_missed`（reason="invalid expr on load"），不阻塞其它。

## 11. 配置

`.env` 新增（均有默认值）：

```
CRON_ENABLED=true                 # 总开关，false 则 scheduler 不启动
CRON_ALLOWED_TOOLS=run_shell,read_file,write_file,fetch_url  # script action 白名单
CRON_WEBHOOK_TIMEOUT_SECONDS=10
CRON_MAX_EXECUTION_RESULT_CHARS=2000  # result_summary 截断长度
```

## 12. 测试

- 后端单元：
  - `cron/store_test.go`：CRUD、过滤、executions 分页/清理。
  - `cron/scheduler_test.go`：加载、增量同步、串行 skip、错过检测。
  - `cron/executor_test.go`：模板渲染、4 种 action、session 失效、并发 skip。
  - `cron/tools_test.go`：4 个 Agent Tool 输入校验与返回。
  - `cron/api_test.go`：REST 端点。
- 集成 smoke：
  - mock：cron → start_task → mock provider → task_completed → execution completed。
  - real_llm：cron → start_task → 真实 LLM 跑一轮 → 验证事件流与 execution 记录。
- 前端：`CronManager.test.ts`、`CronForm.test.ts`、`CronDockPanel.test.ts`、`useCrons.test.ts`。

## 13. 不做（YAGNI）

- 自然语言调度（NLP）→ Phase +1。
- cron/update Agent tool → 用户改用 UI。
- 跨节点分布式调度 → 单进程 scheduler 足够。
- 补跑错过的触发 → 只记录不补跑。
- RBAC owner 强制 → 预留字段。
- v1 前端同步 → 只做 v2。
