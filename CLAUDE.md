# 多 Agent 平台 — 项目说明

> Go + Vue 3 多 Agent 实时协作平台。从零构建，完全可观测。

---

## 设计哲学

**白盒 Agent**：每一个 LLM token、每一次 tool call、每一个 step 状态转换都生成事件并实时广播到前端。不做"黑盒 Agent"。

参考但不依赖以下框架的设计思想：
- **eino** (ByteDance) — 组件化编排、Stream 接口
- **langchaingo** — Chain / Memory 抽象层次
- **trpc-agent-go** — Agent 间通信协议、并发模型

所有实现从零手写，保证最大控制权和可观测性。

---

## 技术栈

| 层 | 技术 | 说明 |
|----|------|------|
| 语言 | Go 1.25 | 后端全部 |
| 数据库 | modernc.org/sqlite | 纯 Go SQLite，单文件部署 |
| 通信 | gorilla/websocket | Phase 0-4，Phase 6 迁 gRPC |
| 前端 | Vue 3 + Vite + TypeScript | `web/`(v1) + `web/v2/`(控制室，默认根路径)，按 URL 路径 `/ui/v{N}/` 分发 |
| LLM | OpenAI-compatible API | `aicoding.dobest.com/v1`，deepseek-v4-flash |
| Mock | MockProvider | 22 个内置脚本，回归脚本确定性评测 |
| 配置 | .env + 环境变量 | 优先级：系统环境变量 > .env > 默认值 |

---

## 项目结构

```
cmd/server/main.go          # 入口：HTTP Server + WS Hub + API 路由
internal/
  agent/agent.go             # Agent 类型定义
  cases/cases.go             # 21 个内置 Task Case（L1-L5 阶梯）+ 自定义 Case CRUD
  config/config.go           # .env 加载 + 配置管理
  cron/                      # Cron / 定时器子系统（见下文「Cron / 定时器系统」）
  llm/client.go              # OpenAI-compatible SSE streaming 客户端
  llm/mock_provider.go       # 确定性 MockProvider（22 个内置脚本，回归用）
  runtime/engine.go          # ReAct Loop 引擎 + Step 状态机
  skill/                     # Skill 可复用 prompt 包
    skill.go                 # Skill 领域模型（来源、状态、模板、变量）
    registry.go              # 进程内 Skill 注册表
    store.go                 # SQLite 持久化
    loader.go                # built_in / local_db 加载
    renderer.go              # {{ variable }} 模板渲染
    builtin.go               # 内置 Skill 种子
  todo/                      # Session 级 TODO 子系统（model/store/service + Agent Tools）
  tool/registry.go           # Tool 注册表
  tool/builtin.go            # 3 个内置工具 (run_shell, write_file, read_file)
  ws/hub.go                  # WebSocket Hub (connect/broadcast/disconnect)
pkg/
  event/event.go             # 统一事件结构 + 序列化
  db/database.go             # SQLite 初始化 + Schema (26+ 表)
web/                         # 前端 Vite + Vue 3 + TypeScript（v1）
web/v2/                      # Observable Control Room 前端（v2，默认根路径服务）
data/                        # SQLite 数据库文件
storage/                     # 文件存储
.env                         # API Key + 配置 (gitignore)
```

## Skill 系统

Skill 是可复用的 prompt + 任务知识包，不是 Agent、Tool 或 Plugin。它让同一个 Agent 在不切换配置的前提下，根据启用的 Skill 动态切换专长。

### 核心概念

| 概念 | 说明 |
|------|------|
| `SkillSource` | `built_in` / `local_file` / `local_db` / `market` / `mcp` |
| `SkillState` | `discovered → validated → loaded → enabled / disabled / invalid` |
| `SkillTemplate` | 模板，名称 `system_prompt` / `task_prompt` 会被注入到 Engine system prompt |
| `SkillParameter` | 模板变量定义，含类型、必填、默认值、描述 |
| `Renderer` | 使用 `{{ variable }}` 风格占位符渲染模板并自动提取变量 |

### 关键文件

```
internal/skill/
  skill.go       # 核心模型（SkillSource / SkillState / Skill / Template / Parameter）
  registry.go    # 内存注册表，支持 List / Get / Set / Exists
  store.go       # SQLite 持久化（skills 表 migration）
  loader.go      # built_in + local_db 加载
  renderer.go    # {{ variable }} 模板渲染与变量提取
  builtin.go     # 内置 Skill 种子（builtin-code-helper / builtin-error-diagnosis）
  tools.go       # skill/create_local, skill/delete_local, skill/list Agent Tools
  events.go      # Skill 相关 event constants（skill_enabled / skill_disabled / skill_created / skill_deleted）
```

### 注入机制

Engine 在 `NewEngine` 构建 system prompt 后，如果有 `SkillRegistry` 和 `ActiveSkills`：

```go
if cfg.SkillRegistry != nil && len(cfg.ActiveSkills) > 0 {
    renderer := skill.NewRenderer()
    for _, id := range cfg.ActiveSkills {
        s, ok := cfg.SkillRegistry.Get(id)
        if !ok { continue }
        for _, tmpl := range s.Templates {
            if tmpl.Name == "system_prompt" || tmpl.Name == "task_prompt" {
                rendered = append(rendered, renderer.Render(tmpl, cfg.SkillVariables))
            }
        }
    }
}
```

启用多个 Skill 时，多个模板按顺序追加到 `## Skill Instructions` 章节，便于叠加。变量缺失时，Renderer 优先使用 `SkillParameter.Default`；无默认值则保留占位符。

### REST API

```
GET    /api/skills?source=built_in|local_db   # 列出（可过滤来源）
GET    /api/skills/search?q=code              # 搜索 id / display_name / description / tags
POST   /api/skills                            # 创建 local_db Skill
GET    /api/skills/:id                        # 详情
PUT    /api/skills/:id                        # 更新 local editable Skill
DELETE /api/skills/:id                        # 删除 local editable Skill
POST   /api/skills/:id/enable                 # 启用（同步 registry 与 store）
POST   /api/skills/:id/disable                # 禁用
```

内置 Skill（`is_local_editable=false`）不可 PUT / DELETE，返回 `403 Forbidden`。单元测试见 `cmd/server/api_skill_test.go`。

### Agent Tools

| Tool | 说明 |
|------|------|
| `skill/create_local` (alias `skill_create_local`) | Agent 在运行中创建本地 Skill |
| `skill/delete_local` | 删除本地 Skill |
| `skill/list` | 列出已加载 Skill |

### 前端触发方式

- 在 `TaskInput` 输入框中键入 `/` 触发 `SkillPicker` 悬浮面板。
- `SkillPicker` 调用 `GET /api/skills/search?q=`，支持 ↑/↓ 选择、Enter 确认、Esc 取消。
- 选中后，父组件在输入框填入 `/skill-id ` 前缀。
- `App.vue` 的 `handleSend` 解析该前缀，先调用 `POST /api/skills/{id}/enable`，然后将剩余文本作为真实 input 发送。
- 输入框不再含 `/` 触发字符时，SkillPicker 自动关闭。

---

## Cron / 定时器系统

Cron 是与 skill / tool / memory 平级的独立子系统，提供三类入口：Agent Tool（LLM 运行时创建）、REST API（Web UI 管理）、Event Bus（每次状态变更与触发都广播 `cron_*` 事件）。调度基于 `github.com/robfig/cron/v3`（秒级 6 域）。

### 核心概念

| 概念 | 说明 |
|------|------|
| `ScheduleType` | `cron`（6 域秒级表达式）/ `interval`（`30s`/`5m` 转成 `@every`）/ `once`（`time.AfterFunc`，到点触发后自动移除） |
| `ActionType` | `start_task`（启动 Agent task，复用 chat 链路）/ `script`（白名单 tool 调用）/ `webhook`（HTTP 回调）/ `notify_session`（向 session 广播 + 写 session_messages） |
| `Status` | `enabled` / `disabled` / `paused` |
| `ExecStatus` | `running` / `completed` / `failed` / `skipped`（并发重叠跳过）/ `missed`（离线错过） |
| `TemplateContext` | 渲染 `action_payload` 的模板变量：`{{.Now}} {{.PrevTrigger}} {{.PrevStatus}} {{.PrevResult}} {{.Count}} {{.CronID}} {{.CronName}}` |
| 串行 skip | `AllowConcurrent=false` 且上一轮仍在跑时，记一条 `skipped` execution 并发 `cron_execution_skipped` 事件 |

### 关键文件

```
internal/cron/
  model.go       # 领域模型（Cron / Execution / ScheduleType / ActionType / Status / Payload 子结构）
  store.go       # DBStore 接口 + Store 薄封装 + EventBus 接口（打破 cron→db 循环依赖）
  template.go    # text/template 渲染（Now/PrevTrigger/PrevStatus/PrevResult/Count/CronID/CronName）
  action.go      # ActionRunner：4 种 action 执行（TaskStarter / SessionMessageWriter 注入）
  executor.go    # Executor：单次触发编排（串行 skip / 模板渲染 / 发事件 / 记 execution）
  scheduler.go   # Scheduler：robfig/cron 包装 + 启动加载 + 增量同步 + once AfterFunc
  service.go     # Service：CRUD + 状态机 + 校验 + 手动触发，CRUD 后通知 Scheduler
  tools.go       # cron/create, cron/list, cron/delete, cron/trigger Agent Tools
  events.go      # cron_* 事件构造 helper（事件常量在 pkg/event）
pkg/db/cron.go   # crons + cron_executions 表 migration v26 + DBStore 的 pkg/db 侧实现
cmd/server/cron_api.go  # startChatTask 复用函数 + /api/crons* REST handler + 适配器
```

### 注入与接入

- `cmd/server/main.go` 在 DB 可用时构造：`cronStore` → `ActionRunner`（注入 `toolRegistry` / `cfg.CronAllowedTools` 白名单 / `startChatTask` 作为 `TaskStarter` / `db` 作为 `SessionMessageWriter`）→ `Executor` → `Scheduler`（`cfg.CronEnabled` 时启动加载 enabled cron）→ `Service`，再注册 Agent Tools + REST API。
- `startChatTask` 是从 `/api/tasks` chat action 抽出的可复用闭包，捕获 main 局部依赖；`cron/start_task` action 经 `cronTaskStarter` 适配后调用它，input 加 `[cron:<id>:<name>]` 溯源前缀。
- `CRON_ENABLED=false` 时 `Scheduler` 为 nil，仍可 CRUD 与手动触发，只是不会自动到点触发。
- 适配器：`cronDBStoreAdapter`（db→cron.DBStore）、`cronSessionMsgWriter`（写 session_messages）、`cronExecutorAdapter`（ExecutorPort2 无 ctx 桥接）。

### REST API

```
GET    /api/crons                       # 列表（?status=&action_type=&source=&q=）
POST   /api/crons                       # 创建
GET    /api/crons/:id                   # 详情
PUT    /api/crons/:id                   # 更新（部分字段）
DELETE /api/crons/:id                   # 删除（手动级联 executions，因 modernc sqlite 默认未开 foreign_keys）
POST   /api/crons/:id/enable|disable|pause|resume   # 状态切换
POST   /api/crons/:id/trigger           # 手动触发（body: {override_input?}）
GET    /api/crons/:id/executions        # 该 cron 的执行历史（?limit=&offset=&status=）
GET    /api/crons/executions            # 全局执行历史（?cron_id=&status=&limit=&offset=）
DELETE /api/crons/executions            # 清理执行历史（?cron_id=&status=）
```

### Agent Tools

| Tool | 说明 |
|------|------|
| `cron/create` (alias `cron_create`) | 创建并启用一个定时器，返回新 cron |
| `cron/list` | 列出当前定时器（可按 status 过滤） |
| `cron/delete` | 按 ID 删除定时器 |
| `cron/trigger` | 手动触发一次执行（可 override_input 覆盖 start_task input） |

### 事件类型

```
cron_created / cron_updated / cron_deleted
cron_enabled / cron_disabled / cron_paused / cron_resumed
cron_triggered / cron_execution_started / cron_execution_completed / cron_execution_failed
cron_execution_skipped / cron_missed / cron_notification
```

事件 `TaskID` 填 `cron_id`，`AgentID` 填触发 agent_id 或 `"cron"`。单元/集成测试见 `internal/cron/*_test.go` 与 `cmd/server/cron_api_test.go`。

### 前端触发方式（web/v2）

- **管理 tab**：`ManageFlyout` / `ManageTabs` 新增 `cron` 项，`ManageContent` 在 `cron` tab 渲染 `CronManager.vue`（列表 + 状态切换 + 手动触发 + 删除），新建/编辑走 `CronForm.vue`，执行历史走 `CronExecutions.vue`。`focusCronId` prop 支持从侧栏一键直达并展开某条 cron 的历史。
- **右侧侧栏**：`CronDockPanel.vue` 作为桌面/平板右栏可折叠面板，只读展示当前定时器与实时 `cron_*` 触发流（`useCronEvents.ts` 订阅 WS），`@open-manage` 跳转管理 tab。`TopBar.vue` 的 ⏰ 按钮控制其开合。
- **数据层**：`web/v2/src/types/cron.ts` 镜像后端领域模型；`composables/useCrons.ts` 封装 `/api/crons*` REST 调用；`composables/useCronEvents.ts` 把 `cron_*` 事件映射到本地 reactive 列表。`types/events.ts` 追加 14 个 `cron_*` EventType。

---


所有前后端通信统一为 `AgentEvent` JSON，关键事件类型：

```
task_started → step_started → llm_thinking → llm_delta* → llm_message_complete
  → tool_call_started → tool_call_output → tool_call_complete
  → observation → step_complete → ... → task_completed / task_failed
```

每个事件携带 `{task_id, agent_id, step_index, timestamp, data}`，前端可按 Agent 独立追踪并行状态。

---

## ReAct Loop 引擎

```
User Input → System Prompt + Messages → LLM (streaming)
  → 有 tool_calls? → Tool Registry.Execute() → 追加 tool result 到 Messages
  → 无 tool_calls? → Final Answer → task_completed
  → 超过 max_steps? → task_failed (max_steps_exceeded)
```

---

## 内置 Case 矩阵（L1-L5 阶梯）

`internal/cases/cases.go` 通过 `cases.All()` 返回 21 个内置 case，按能力阶梯分为五级，是回归脚本 `scripts/cases-regression.sh` 的 mock 评测对象。每个 case 由 `ID / SystemPrompt / DefaultInput / TaskContract(Goal, Scope, MaxSteps, Permissions, AcceptanceCriteria) / Tags` 构成。

| 级别 | Case ID | 验证能力 |
|------|---------|---------|
| L1 单 Agent 基线 | `code-gen` `dialogue` `research` `long-task` | 基础 ReAct Loop、write_file/run_shell、纯对话、多步 |
| L2 单 Agent + 子系统 | `todo-driven` `web-research` `skill-code-helper` `cron-notify` `llm-judge-qa` | todo/cron/skill Agent Tools、web 搜索、llm_judge 验收 |
| L3 Harness 治理 | `policy-enforcement` `approval-flow` `max-steps-exhaustion` `context-compression` `checkpoint-resume` | PolicyGate 拦截、审批、步数耗尽、上下文压缩、checkpoint 续跑 |
| L4 多 Agent 静态编排 | `multi-agent`(legacy) `multi-agent-parallel` `multi-agent-sequential` `multi-agent-dag` | orchestrator parallel/sequential/pipeline(DAG) 静态拆分 |
| L5 多 Agent 动态编排 | `multi-agent-leader-dispatch` `multi-agent-review` `multi-agent-fault-tolerance` | leader 运行时 dispatch_sub_agent、互评、容错降级 |

### 回归脚本与 mock 脚本

- `scripts/cases-regression.sh`：独立端口 + 独立 DB + `LLM_USE_MOCK=true` 串行跑全部 21 个 case，断言 status / has_tool / final_result / total_tokens / cost_records；L4-L5 额外断言编排事件（`decompose_done` / `agent_dispatched` / `agent_completed`，经 WS 订阅捕获）与 `child_tasks[].steps` 回填。目标 21/21 PASS。
- `internal/llm/mock_builtin.go`：`BuiltinMockScripts()` 返回 22 个 mock 脚本（21 case 各一个 + `tool-error` keyword 回退）。每个脚本通过精确 `CaseID` 匹配被 `MockProvider.selectScript` 选中（精确命中 +1000，远高于 router-classifier 的 Priority 1000），其 `Responses` 序列还原该 case 的真实 ReAct 行为（tool_call → 最终 text）。
- `selectScript` 区分两档 CaseID 命中：精确 `EqualFold`（+1000）与输入子串包含（+500）。后者低于前者，防止 `research` 这类常见英文词 case ID 靠子串劫持其它 case 的 run-case 路径（`multi-agent-sequential` 的 input 含 "research" 曾被 research 脚本抢走）。
- 回归脚本 Windows 注意事项：必须 `export PYTHONUTF8=1`，否则 python stdin 默认 GBK 解码含中文的 `/api/tasks` 响应（如 `skill/list` 返回的 Skill DisplayName）会 JSON 解析失败、轮询 status 恒空 → 误判超时。

### 编排事件的可观测性约定

orchestrator 的 `decompose_done` / `agent_dispatched` / `agent_completed` 事件用 `event.NewEvent`（无 SubTaskID）经 `hub.SendEvent` 做 WS 广播，**不写 task steps**。因此 HTTP `/api/tasks` 详情里看不到这些事件，只能靠 WS 订阅（或 `/api/replay/events`）捕获。回归脚本据此把 WS 订阅改为"服务就绪后启动 + 带退避重连"。

---

## Phase 计划

```
Phase 0 ✅ → Phase 1 ✅ → Phase 2 ✅ → Phase 3 ✅ → Phase 4 ✅ → Phase 5 ✅ → Phase 6 ✅ → 扩展 Phase ✅🚧
  (骨架)      (Agent)     (UI)       (Cases)    (并发)      (注册)      (高级)       (skill/TODO/cron✅ + UI-v2/7-H2🚧)
```

| Phase | 状态 | 核心交付 |
|-------|------|---------|
| 0 骨架 | ✅ | WS Hub + Event Schema + SQLite + Vue CDN |
| 1 Agent | ✅ | LLM Client + 3 Tools + ReAct Engine + .env |
| 2 UI | ✅ | Vite + TypeScript + AgentTree + TypeWriter + Markdown |
| 3 Cases | ✅ | 5→21 内置 Cases（L1-L5 阶梯）+ Card UI + 历史回放 + mock 回归 21/21 |
| 4 并发 | ✅ | 多 Agent 并行 + 前端多树渲染 |
| 5 注册 | ✅ | 运行时 Tool 注册 + Docker 沙箱 |
| 6 高级 | ✅ | RAG + Auth + gRPC + 可观测性 |

---

## 扩展 Phase

```
Phase skill ✅ → Phase TODO ✅ → Phase 7-cron ✅ → Phase UI-v2 🚧 → Phase 7-H2 🚧 → Phase 7 🔜
  (Skill 系统)    (TODO 子系统)   (定时器)          (控制室 UI)      (编排闭环)      (生产化与深度集成)
```

| Phase | 状态 | 核心交付 |
|-------|------|---------|
| skill | ✅ | 可复用 prompt 包 + Renderer + Registry + REST API + Agent Tools + 前端 `/` 触发 SkillPicker + E2E 测试 |
| TODO | ✅ | session 级 TODO + 6 个 Agent Tools + `/api/todos` + 前端拖拽/嵌套子任务 |
| 7-cron | ✅ | Cron 子系统（4 种 action + robfig/cron 秒级调度 + 事件化 + 前端管理 UI） |
| UI-v2 | 🚧 | `web/v2/` Observable Control Room（Dock 三栏 + 移动 3-tab，根路径默认 v2，`/ui/v1/` 保留旧版） |
| 7-H2 | 🚧 | multi-agent 编排遗留闭环（leader-driven dispatch_sub_agent + Tracer 事件流 + child steps 回填 + DAG） |
| 3+ extend-task-cases | ✅ | 内置 Case 矩阵 5→21（L1-L5 阶梯）+ mock 回归 21/21（OpenSpec change 已归档） |
| 7 生产化 | ⬜ | tokenizer、context 压缩、RBAC、MCP 增强、K8s 部署等（Roadmap 统一规划）|

## 编码约定

- **Go**: 标准库优先，interface 抽象，goroutine 安全
- **事件驱动**: 所有状态变更通过 EventBus 接口广播，不直接操作前端状态
- **Tool 接口**: `Name() / Description() / Parameters() / Execute(input) -> output`
- **错误处理**: 每个 Step 失败都生成 `task_failed` 事件，含具体 reason
- **Token 统计**: 严格使用 API 返回的 `usage` 字段，不做本地估算

---

## Git 铁律

- 每个 Phase 完成后必须提交 Git
- 提交信息格式：`Phase X: 简要描述`
- 同步更新 `roadmaps/ROADMAP.md`

---

## 注释铁律 — 白盒 Agent 设计哲学

**代码即文档**。本项目的核心设计哲学是"白盒 Agent"——每一行代码都应该是可读、可理解、可追溯的。这意味着：

- **每个导出的类型/函数/接口必须有注释**，说明其职责、使用场景、与其它模块的关系
- **关键流程必须有行内注释**（ReAct Loop、SSE 解析、Event 路由、Tool 执行等）
- **TODO 注释标注未实现功能**，格式：`// TODO: Phase X — 描述`
- **接口设计需注释"为什么"**，而非"是什么"（代码已经说了"是什么"）
- **前端组件同样要求**：每个组件的 props/emits/生命周期 需注释其数据流和事件流

Why: 多 Agent 系统的复杂度在于不可见的状态流转和决策链路。注释是"白盒"的第一层——让阅读者不需要运行代码就能理解 Agent 的内部逻辑。从文档 → 注释 → 前端可视化，三层递进体现设计哲学。

---

## 内存规则

- 会话期间短期记忆
- 踩坑记录写入 memory
- 重大设计决策写入 memory
- 全局记忆写入 memory
- Token 管理：不携带完整历史，精简上下文

---

## API 配置

```
Endpoint:  https://aicoding.dobest.com/v1
Model:     deepseek-v4-flash
API Key:   写在 .env (gitignore)
```

.env 文件格式：
```
LLM_ENDPOINT=https://aicoding.dobest.com/v1
LLM_API_KEY=sk-xxx
LLM_MODEL=deepseek-v4-flash
```

---

## 待定问题

- `run_shell` 沙箱方案 (Docker / Firecracker) → Phase 5
- 前端状态管理 (Pinia vs reactive) → Phase 2
- Agent 间通信协议 → Phase 4
- Markdown 渲染库 (marked / markdown-it) → Phase 2