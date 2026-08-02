# 三子系统深度分析与可执行规划（2026-08-02）

> 范围：Agent CRUD 前端 / 多轮对话历史回读 / AgentBus↔ReAct Loop 闭环
> 目标：纠正 ROADMAP 认知偏差，给出基于代码真实现状的分阶段可执行计划
> 结论先行：**A 是认知偏差（页面已存在），B 是架构层错位（主线，最该投入），C 是半双工协议 + 一个真实路由 bug**

---

## 一、现状详析（与 ROADMAP 描述的关键差异）

### A. Agent CRUD（前端管理页面 + 后端支撑）

**ROADMAP L77 描述**：「后端 API 已完整，缺独立管理页面（目前仅 AgentConfig 在选择 Agent 时使用）」

**代码真实现状**：管理页面**早已存在且完整**，ROADMAP 描述错误。
- 后端：`internal/agent/agent.go` 定义 `Agent`/`AgentConfig`/`AgentRole`；`pkg/db/persistence.go` 有完整 CRUD（`InsertAgent` L603 / `QueryAgents` L640 / `UpdateAgent` L700 / `DeleteAgent` L735 / `SeedDefaultAgent` L746）；`cmd/server/api.go` 有 `handleAgents` L871 / `handleAgentByID` L922；路由在 `server.go:231-242`（`/api/agents`、`/api/agents/{id}`，需 `RoleAdmin`）。
- 前端（v2 默认）：`web/v2/src/components/AgentConfig.vue`（**1344 行**，列表/编辑/删除/Test Connection/权限/ModelDropdown 齐全）已挂载为 Manage 面板一级 tab（`ManageContent.vue:247-248`）；`useAgentStore.ts` 封装全套 CRUD。**v1 也有一份 1005 行版本**。
- 「选择 Agent」入口是 `OptionsFlyout.vue:405` 的 `agent-select`，与 `AgentConfig.vue` 是**两个不同组件**。

**真实缺口（增量打磨，非从零建页）**：
1. `QueryAgents()`（`persistence.go:640`）不支持分页/搜索，前端列表无分页/搜索/排序。
2. `agents` 表无 `role` 列（`AgentRole` 只靠 `config` JSON 或运行时 spec 传递），列表无法按 Leader/Worker 区分筛选。
3. `is_default` 无显式「设为默认」独立入口。
4. v1/v2 两份 `AgentConfig.vue` 并存，双重维护成本（建议 v1 下线时一并收敛，不与本次耦合）。

---

### B. 多轮对话历史回读（session 级多轮记忆接入 Engine）

**ROADMAP L78 描述**：「当前每个 task 独立上下文，session 级多轮记忆尚未接入 Engine」

**代码真实现状**：**已接入，但接在了错误的层（HTTP handler），且有 4 个真实缺陷。**
- 多轮链路已通：`web/v2/src/App.vue:685-732` 区分无 session / 有 rootTaskId（走 `startTurn` → `POST /api/sessions/{id}/chat`）。
- 后端 `handleSessionChat`（`api.go:1838-2032`）：L1925 压缩 → L1934 加载全部历史 `QuerySessionMessages` → L1943 `buildHistoryContext` → L1952 memory recall → L1996 **把历史压扁成 system prompt 文本** `fullSystemPrompt = historyContext + workingMemory + systemPrompt`。
- 历史写入 `engine.go:writeSessionMessage` L1371-1386（覆盖全部 message 类型）。

**4 个实质缺陷**：
| # | 缺陷 | 位置 | 后果 |
|---|------|------|------|
| B-1 | Engine 不感知历史，每次 `Run` 全新对话 | `engine.go:761,818` messages 仅 `[system,user]` 起步 | 丢失 role 结构、`tool_calls` 结构化信息；无法用 provider prompt cache |
| B-2 | 历史**嵌套自我复制**，上下文二次膨胀 | `engine.go:826` 把含上一轮 history 的 system prompt 写入 session_messages；下一轮 `buildHistoryContext` 再读出 | 第 N 轮 system prompt 嵌套 N-1…轮片段，语义严重失真 |
| B-3 | `buildHistoryContext` 无过滤、无条数限制 | `api.go:2054-2067`；`QuerySessionMessages` `persistence.go:936-958` 无 LIMIT | 前 20 轮（`compressor.go` `turnThreshold=20`）完全不压缩，system prompt 单调增长 |
| B-4 | 只有单 Agent chat 有多轮，multi-agent 完全没有 | `tasks_api.go:138-227`(actionMultiAgent) / `:316-431`(startChatTask) / `server.go:720-727` 均无 historyContext | 多 Agent 同 session 第二轮，leader/worker 看不到第一轮任何内容 |

---

### C. AgentBus ↔ ReAct Loop 闭环

**ROADMAP L79 描述**：「AgentBus listener 已存在，但 LLM 主动收发 agent message 的协议未完全闭环」

**代码真实现状**：**「收」已闭环可观测；「发」只有系统内部路径，LLM 无法主动发；且有一个真实路由 bug。**
- 接口分层（无循环依赖）：`runtime/agentbus.go`（`AgentMessage` L36 / `AgentBus` interface L82）↔ `orchestrator/orchestrator.go`（`AgentBus` struct L1278 / `SendMessage` L1414）↔ `agentbus_adapter.go`（双向转换）。
- **收（完整）**：`engine.go:849-940` listener goroutine，L906 把消息注入为 `[Agent X]: ...` user message → `appendMessage` + `writeSessionMessage`。生命周期/并发安全完善（L762-780 / L530 / L726）。
- **发（系统触发，非 LLM）**：`sendAgentMessage` L2586（私有）/ `SendAgentMessage` L2594（public，自投递）/ `sendAgentMessageWithSubTask` L2620。**真实调用点**仅 2 处：`approval_delegation.go:219`、`runner.go:445`（都是系统代码，与 LLM 决策无关）。
- 编排侧主动发：`orchestrator.go:351-361`(sequential 链式) / `:391-403`(OutputTo) 已是真实生产路径。

**缺口**：
| # | 缺口 | 位置 | 后果 |
|---|------|------|------|
| C-1 | **LLM 无工具可主动发 agent message** | `builtin.go` 全工具清单无 `send_agent_message`/`reply_to_agent`/`wait_for_agent_message` | 半双工：worker 只能把回应写进 final answer 等 `RunBlocking` 返回，非消息驱动协作 |
| C-2 | **真实 bug：`ToAgentID: ""` 永不投递** | `engine.go:2628` | `sendAgentMessageWithSubTask` 构造的消息 key miss（`subTaskHandlerKey` L1367 是 `agentID+"\x1f"+subTaskID`），在 `queue` 滞留至 `maxQueue=100` 淘汰，污染共享队列。当前靠 `runner.go:445` 重复发同样内容「兜底」，死路径仍占位 |
| C-3 | `SendAgentMessage` 名不副实（自注入非发送） | `engine.go:2604` `ToAgentID: e.cfg.AgentID` | 误导后续开发者 |
| C-4 | 消息到达与 ReAct 迭代无同步点 | `engine.go:852,858-861,929` | 消息在 LLM 请求发出后到达本轮看不到；Run 结束后到达直接丢弃；`agentMsgCh` 容量 10 满则静默丢；无「等待」阻塞语义 |
| C-5 | 结果回传不走 AgentBus | `builtin.go` dispatch 截断成 tool observation（`runner.go:471 RunBlocking`） | 同一份 worker 产出两条语义不同的回流通道，前端时间线不一致 |

---

## 二、依赖关系与实施顺序

```
A (Agent CRUD)  ─── 完全解耦，独立 ──►  可随时/并行做

B (多轮历史)  ─┐
               ├── 均改动 internal/runtime/engine.go 的 Run() 与 e.messages 临界区
C (AgentBus)  ─┘    ── B 改 L818 前的 messages 初始化
                    ── C 改 L849-940 listener + L2586-2650 发送
                    ── 共用 messagesMu (engine.go:530, L726)

C-2 bug 永久滞留队列，必须在 B/C 大改前修（否则 bug 被带进新架构）
B 定义 e.messages 构造契约 → C-P1 消息格式必须建立其上
C-P2（阻塞等待）重写 Run 并发骨架，必须与 B 合并成一个 Phase
```

**建议顺序**：A → C-P0（bug 修复）→ B（主线）→ C-P1（LLM 主动发送）→ C-P2（可选）

---

## 三、分阶段可执行计划

### Phase 0：认知对齐（0.5 天，前置）
| 任务 | 文件:行 | 动作 |
|------|---------|------|
| P0-1 修正 Agent CRUD 描述 | `roadmaps/ROADMAP.md:77` | 改为「管理页面已存在（v1/v2 双份），缺口为分页/搜索/role 筛选增量」 |
| P0-2 修正多轮历史描述 | `roadmaps/ROADMAP.md:78` | 改为「已接入 handler 层但架构错位 + 4 缺陷（B-1~B-4）」 |
| P0-3 修正 AgentBus 描述 | `roadmaps/ROADMAP.md:79` | 改为「收已闭环；发为半双工 + C-2 路由 bug」 |
**验收**：ROADMAP 三项描述与代码一致；`git commit` 一条 `docs: 校正 ROADMAP 三子系统现状描述`。

### Phase A：Agent CRUD 增量打磨（1–2 天，独立）
| 任务 | 文件 | 动作 | 验收 |
|------|------|------|------|
| A-1 后端分页/搜索 | `pkg/db/persistence.go:640` `QueryAgents` 增 `limit/offset/keyword`；`api.go:871` 透传 query | `go test ./pkg/db/...` 覆盖新参数 |
| A-2 前端列表搜索分页 | `web/v2/src/composables/useAgentStore.ts` + `AgentConfig.vue` | 搜索框 + 分页可用；`npm run build` |
| A-3（可选）role 列 | 新迁移 v35 `ALTER TABLE agents ADD COLUMN role TEXT DEFAULT 'worker'`；同步 `InsertAgentOptions`/`agentRequest`；前端筛选 | `go test ./pkg/db/...`；migrate v35 通过 |
**验收**：`go build ./...` + `go test ./pkg/db/...` + 前端 build 通过；手动验证列表搜索。

### Phase C-P0：AgentBus 路由 bug 修复（1 天，必须在 B/C 大改前）
| 任务 | 文件:行 | 动作 |
|------|---------|------|
| C0-1 修 ToAgentID 空 | `engine.go:2628` | `sendAgentMessageWithSubTask` 增加 `toAgentID` 形参或从 `SupervisorSubTaskID` 反解；确保 `matchesTarget` L1389 能命中 |
| C0-2 消除重复发送 | `approval_delegation.go:219` vs `runner.go:445` | 保留一处（建议 runtime 侧自投递），删另一处，避免双发污染队列 |
| C0-3 改名消歧 | `engine.go:2594` `SendAgentMessage` → `InjectAgentMessage` | 语义对齐 L2604 自投递行为 |
| C0-4 补单测 | `internal/orchestrator/*_test.go` | 覆盖 `matchesTarget` 的 `ToAgentID==""` 场景 |
**验收**：`go test ./internal/orchestrator/... ./internal/runtime/...`；`bash scripts/cases-regression.sh` 21/21；确认 `agent_messages` 队列无滞留。

### Phase B：多轮历史下沉到 Engine（主线，3–5 天，改 Run）
| 任务 | 文件 | 动作 |
|------|------|------|
| B-1 止血 | `engine.go:826` `writeSessionMessage` 跳过 `role=system`；`persistence.go:936` `QuerySessionMessages` 增 `limit` 变体；`api.go:2054` `buildHistoryContext` 过滤 system + 只取最近 N 轮 | 消除 B-2 嵌套自我复制 + B-3 无限制 |
| B-2 下沉到 Engine | `engine.go:280` 附近 `EngineConfig` 加 `HistoryMessages []llm.Message`；`Run` L818 前注入；新增 `cmd/server` 侧 `buildHistoryMessages(msgs) []llm.Message` 还原 role/content/tool_calls/tool_call_id（替代 `buildHistoryContext` 文本压扁） | system prompt 恢复稳定，可命中 prompt cache |
| B-3 贯通 AgentRunSpec | `runner.go:56` `AgentRunSpec` 加 `HistoryMessages`；`AgentRunner.Run` 透传；`tasks_api.go:316`(startChatTask) + `:138`(actionMultiAgent) 复用同一套注入，**补齐 B-4**（multi-agent 第二轮可见第一轮） | leader 注入、worker 不注入（保持子任务上下文干净） |
| B-4 对齐压缩器 | `compressor.go:123` 的 `turn_index==-1` 合成 summary 前置为摘要消息；`turnThreshold=20` 调低或改为可配置 | `buildHistoryMessages` 处理 summary 消息 |
**验收**：`bash scripts/cases-regression.sh` 21/21；**断言** system prompt 长度不随轮次二次增长（回归测试加一条「5 轮对话后 system prompt 不含嵌套历史」）；multi-agent 同 session 第二轮能看到第一轮内容。⚠️ 与 C-P1/P2 共用 `Run()` 临界区，改动后必须重跑并发正确性验证。

### Phase C-P1：LLM 主动发送工具（3–5 天，建立在 B 契约上）
| 任务 | 文件 | 动作 |
|------|------|------|
| C1-1 新增工具 | `internal/tool/builtin.go` 仿 `NewDispatchSubAgentTool` L730 新增 `NewSendAgentMessageTool`，定义 `AgentMessageSender` interface 避免 tool 包依赖 runtime；schema：`to_agent_id`(必填)/`msg_type`(`observation`/`question`/`instruction`)/`content` | 走已有 `executeTool` L1995 路径，不改 Run |
| C1-2 权限门控 | `engine.go:342-357` 沿用 `Role`/`CanDispatchSubAgents` 模式，新增 `CanSendAgentMessage`；按通信矩阵约束 Child→Leader 默认允许、Leader→Child 允许、Child→Child 需 `OutputTo` 授权 | 注入点在 `main.go:739` 附近 per-agent registry |
| C1-3 worker 注入 | `orchestrator.go:899` `runAgent` 为 worker 注入工具，`selfSubTaskID = rootTaskID + "_" + spec.AgentID`，目标 leader subTaskID = `rootTaskID`（与 L396-398 特判一致） | worker 可主动发消息给 leader |
**验收**：新增 L-level mock case（worker 主动发消息→leader 收到）；`go test ./internal/tool/... ./internal/orchestrator/...`；`cases-regression` 21/21 + 新 case。

### Phase C-P2：阻塞等待语义（可选，高风险，与 B 合并 Phase）
| 任务 | 文件 | 动作 |
|------|------|------|
| C2-1 wait 工具 | 新增 `wait_for_agent_message` 工具，`executeTool` L1995 阻塞在 `agentMsgCh` 带超时 | worker 发问后挂起等答复 |
| C2-2 队列扩容 | `engine.go:852` `agentMsgCh` 容量提高；L858-861 满则丢弃改为有界阻塞 + 溢出告警 | 不再静默丢消息 |
| C2-3 复用挂起机制 | 与 `Pause`/`Resume`（L952-955）复用同一套挂起状态机 | 避免两套并发状态机 |
**验收**：worker 发问→挂起→leader 答复→恢复 全流程 mock case 通过。**务必与 Phase B 合并为同一 Phase 改 `Run()`，不二次改动。**

---

## 四、风险与跨阶段约束

1. **C-2 bug 必须先修**：`engine.go:2628` 消息永久滞留 `maxQueue=100` 共享队列，在 B/C 大改前不修会被带进新架构，届时更难定位。
2. **B 与 C 必须合并**：两者都改 `engine.go` 的 `Run()` 与 `e.messages`（共用 `messagesMu` L530/L726）。B 改 L818 前初始化，C 改 L849 listener + L2586-2650 发送。**分两次改必导致第二次重做并发验证**。建议 B + C-P1 + C-P2 合并为一个「Engine 通信架构重构」Phase。
3. **消息格式统一**：B 落地「Engine 原生 message 数组」后，C 的 listener 注入（当前 L906 的 `[Agent X]: ...` 字符串 hack）应顺势改为带 `name` 字段的结构化 message，避免两套风格并存于同一数组。
4. **C-P2 决策点**：先跑一段 P1 实际数据，确认「半双工 + 同步 dispatch」是否真构成瓶颈，再决定是否投入 C-P2（唯一需重写 Run 并发骨架的改动）。
5. **测试基线**：所有改动以 `bash scripts/cases-regression.sh` 21/21 为硬指标；Phase B 必须新增「历史不二次膨胀」断言；Phase C 必须新增 worker 主动通信 mock case。mock 测试用 `LLM_USE_MOCK=true` + `PYTHONUTF8=1`（Windows 回归必设，否则中文 JSON 解码失败误判超时）。

---

## 五、立即可做的第一步

1. **Phase 0**（纠偏 ROADMAP，0.5 天，零风险）—— 立刻可执行，先对齐团队认知，避免按错误前提排期。
2. **Phase C-P0**（修 `engine.go:2628` 路由 bug，1 天，低风险）—— 在任何 Engine 大改之前完成。
3. 两者可同日启动；Phase A 随时并行；Phase B 作为主线在 C-P0 之后展开。
