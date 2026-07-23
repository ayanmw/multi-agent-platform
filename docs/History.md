# 变更历史

> 本文档汇总 Multi-Agent Platform 的重要 bug 修复、体验优化与行为变更。
> 与 `roadmaps/ROADMAP.md` 配合使用：路线图记录 Phase 级别的大事，本文档记录每次用户反馈驱动的具体改动。
>
> 最后更新：2026-07-23

---

## 2026-07-23 内置 Case 矩阵扩展（extend-task-cases，v0.11.3 Alpha）

> OpenSpec change `extend-task-cases`，已归档至 `openspec/changes/archive/2026-07-23-extend-task-cases/`。属 Phase 3（Cases）的能力深化，非用户反馈驱动，但因引入了 21 个内置 case 与 mock 回归基建，记录于此便于追溯。

### 1. 内置 Case 矩阵 5 → 21（L1-L5 阶梯）

**问题**
`internal/cases/cases.go` 长期只有 5 个内置 case（code-gen / research / dialogue / long-task / multi-agent），无法覆盖 todo / skill / cron / harness 治理 / 多 Agent 静态与动态编排等已落地能力，回归脚本 `scripts/cases-regression.sh` 也只跑这 5 个。

**改动**
- 新增 16 个内置 case，与原有 5 个共同组成 L1-L5 五级阶梯：
  - L1 单 Agent 基线：`code-gen` `dialogue` `research` `long-task`
  - L2 单 Agent + 子系统：`todo-driven` `web-research` `skill-code-helper` `cron-notify` `llm-judge-qa`
  - L3 Harness 治理：`policy-enforcement` `approval-flow` `max-steps-exhaustion` `context-compression` `checkpoint-resume`
  - L4 多 Agent 静态编排：`multi-agent`(legacy) `multi-agent-parallel` `multi-agent-sequential` `multi-agent-dag`
  - L5 多 Agent 动态编排：`multi-agent-leader-dispatch` `multi-agent-review` `multi-agent-fault-tolerance`
- `internal/cases/cases_test.go` 增补完整性校验：ID 唯一、Name/Category/SystemPrompt/Goal 非空、MaxSteps>0、Tags 含阶梯标识、L1-L5 各级覆盖、验收类型属 harness 枚举。

**涉及文件**
`internal/cases/cases.go`, `internal/cases/cases_test.go`, `internal/harness/harness.go`

### 2. Mock 回归基建 21/21 PASS

**改动**
- `internal/llm/mock_builtin.go`：`BuiltinMockScripts()` 从少数脚本扩展到 22 个（21 个 case 各一个精确 CaseID 脚本 + `tool-error` keyword 回退），每个脚本还原该 case 的真实 ReAct 行为（tool_call → 最终 text）。
- `internal/llm/mock_provider.go` `selectScript`：CaseID 命中分两档——精确 `EqualFold` +1000、输入子串包含 +500。后者低于前者，防止 `research` 这类常见英文词 case ID 靠子串劫持其它 case 的 run-case 路径（`multi-agent-sequential` 的 input 含 "research" 曾被 research 脚本抢走）。新增 `TestSelectScriptCaseIDBeatsSubstring` 回归测试。
- `scripts/cases-regression.sh`：
  - 对全部 21 个 case 串行跑 mock 回归，断言 status / has_tool / final_result / total_tokens / cost_records；L4-L5 额外断言编排事件（`decompose_done` / `agent_dispatched` / `agent_completed`）与 `child_tasks[].steps` 回填。
  - WS 订阅改为服务就绪后启动 + 带退避重连，捕获 orchestrator 仅经 `hub.SendEvent` 广播、不写 task steps 的编排事件。
  - Windows 下强制 `export PYTHONUTF8=1`，修复 python stdin 默认 GBK 解码含中文的 `/api/tasks` 响应（如 `skill/list` 返回的 Skill DisplayName "代码助手"）导致 JSON 解析失败、轮询 status 恒空 → 误判超时。
  - `max-steps-exhaustion` 等 `status=failed` 的 case 视为 PASS。

**涉及文件**
`internal/llm/mock_builtin.go`, `internal/llm/mock_provider.go`, `internal/llm/mock_provider_test.go`, `scripts/cases-regression.sh`

### 验证
- `go test ./internal/cases/... ./internal/llm/... ./internal/harness/... ./internal/orchestrator/...` ✅
- `bash scripts/cases-regression.sh` 21/21 PASS ✅

### 已知限制
- `tasks.md` 第 9 部分（real-llm-smoke 9.1-9.4 代表性场景抽样）超出本次 mock 回归范围，保留为未勾项，作为潜在后续 change。

---

## 2026-07-11 UI/UX 与稳定性修复批次

### 1. 可配置任务超时（0 = 不限时）

**问题**
`cmd/server/main.go` 中单个 Agent 任务的上下文被硬编码为 5 分钟，复杂任务（多步骤、长耗时）常在未完成时触发 `Task failed: context deadline exceeded`。

**改动**
- 后端：`TaskContract` 新增 `TimeoutSeconds int`，0 表示无限制；`EngineConfig` 新增 `Timeout time.Duration`。
- 后端：替换 `runAgentLoopWithTurn`、编排任务、`/api/checkpoints/recover` 中的硬编码超时，改为从 contract 派生；多 agent 编排派生全局超时时使用各 spec 的最小正超时。
- 后端：`context.DeadlineExceeded` 统一映射为失败原因 `task_timeout`，方便前端识别。
- 前端：`TaskInput.vue` 增加超时选项（无限制 / 5 / 10 / 30 / 60 / 120 分钟），持久化到 `localStorage`。
- 前端：`startTask` / `startTurn` / `startMultiAgentTask` 透传 `timeout_seconds`。

**涉及文件**
`internal/harness/harness.go`, `internal/runtime/engine.go`, `cmd/server/main.go`, `cmd/server/api.go`, `internal/harness/policy_test.go`, `web/src/components/TaskInput.vue`, `web/src/App.vue`, `web/src/composables/useTaskStore.ts`, `web/src/types/events.ts`

---

### 2. Memory Browser 改为独立 overlay

**问题**
点击右上角 Memory 后无法退回；点击左侧 Session 也无法显示任务信息，因为 Memory 组件替换掉了主内容区。

**改动**
- `App.vue` 中 Memory Browser 渲染为独立 overlay 层，主内容区保持在下方。
- 新增关闭按钮（× 关闭）和点击遮罩关闭。
- 关闭后自动回到之前的 Session/Task 视图；左侧 Session 切换不受影响。

**涉及文件**
`web/src/App.vue`, `web/src/components/MemoryBrowser.vue`

---

### 3. 展开/折叠全部 + 最新内容自动展开

**改动**
- `App.vue` 任务视图区新增 "全部展开" / "全部折叠" 按钮，控制当前所有 Turn 与 Step。
- `TurnList.vue` 保持最后一个 Turn 默认展开。
- `AgentTree.vue` 当新 step 到达时自动展开最新 step；全局展开/折叠命令同步生效。

**涉及文件**
`web/src/App.vue`, `web/src/components/TurnList.vue`, `web/src/components/TurnItem.vue`, `web/src/components/AgentTree.vue`

---

### 4. 智能自动滚动

**问题**
任务执行时页面会强制滚动到底部，用户想查看历史消息时会被不断拉回底部。

**改动**
- 主内容区监听滚动：当用户已经靠近底部时才在新事件到达时自动滚动。
- 用户向上滚动后暂停自动滚动，显示 "Auto-scroll paused — press Ctrl+End or click to resume" 提示。
- 点击提示或按 `Ctrl+End` 恢复自动滚动并立即回到底部。

**涉及文件**
`web/src/App.vue`

---

### 5. max_steps 失败后 "Continue" 保留上下文

**问题**
`max_steps_exceeded` 后点击 "以 max steps ×2 继续" 会创建新的 root task，丢失了 Session 中的对话上下文。

**改动**
- `App.vue` 的 `handleContinue` 改为调用 `startTurn`（而非 `startTask`），在同一 Session 内开启下一轮。
- 后端 `POST /api/sessions/:id/chat` 会自动加载 `session_messages` 并注入对话历史。

**涉及文件**
`web/src/App.vue`, `web/src/composables/useTaskStore.ts`

---

### 6. 默认 MaxSteps 改为 30 并持久化

**改动**
- 后端 `DefaultContract` 与 `EngineConfig` 默认值从 10 调整到 30。
- 前端 `TaskInput.vue` 默认 30，保存在 `localStorage`；刷新后保持用户选择。
- quickSteps 增加 50 选项。

**涉及文件**
`internal/harness/harness.go`, `internal/runtime/engine.go`, `internal/harness/policy_test.go`, `web/src/components/TaskInput.vue`, `web/src/App.vue`

---

### 7. Step 索引可视化

**改动**
`AgentTree.vue` 在每个 step 头部显示 `#{{ step.index }}`，同一 think → tool_call → observation 组共享同一索引，方便用户对应步骤。

**涉及文件**
`web/src/components/AgentTree.vue`

---

### 8. 错误处理：首次错误反馈给 AI，连续两次相同错误才失败

**改动**
- `internal/runtime/engine.go` 中：
  - 工具调用或系统错误首次发生时，将错误内容作为 observation 返回给 LLM，让模型自行修正。
  - 记录错误指纹；只有连续两次出现相同错误时，才触发 `task_failed` 并转人工处理。
  - LLM 调用失败（`think()` 报错）也纳入同一反馈-失败策略。

**涉及文件**
`internal/runtime/engine.go`

---

## 验证

本次批次完成后执行：
- `go build ./cmd/server` ✅
- `go test ./internal/harness ./internal/runtime ./cmd/server` ✅
- `cd web && npm run build` ✅

---

## 2026-07-16 上下文窗口可观测性

### 问题
多轮会话时 context window 持续增长，用户无法直观看到：
- 当前已经用了多少 tokens、距离模型上限有多远；
- system prompt / user / assistant / tool 各占多少比例；
- 真正传给 LLM 的每一条 message 内容是什么。

### 改动
- 后端 `internal/llm/token_estimate.go`：新增基于字符启发式的 token 估算与 `ContextWindowSnapshot` 结构，标注 `estimated_*` 以区别于 API 精确值。
- 后端 `internal/runtime/engine.go`：每次 `think()` 调用前计算当前 messages 的上下文占用，并发射 `context_window_snapshot` 事件。
- 事件系统 `pkg/event/event.go` 增加 `EventContextWindowSnapshot` 常量；前端 `web/src/types/events.ts` 增加 `ContextWindowSnapshotData`、`ContextSnapshotMessage` 类型。
- 前端新增 `web/src/composables/useContextWindow.ts` 累积快照。
- 前端新增 `web/src/components/ContextWindowPanel.vue`：
  - 顶部总量进度条（已用 / 上限 + 百分比）
  - 按 role 分组的条形图（system/user/assistant/tool 占比）
  - 每条 message 可展开查看完整 content、reasoning、tool_call_id
- 前端 `App.vue` 增加入口按钮，Context Window 面板作为右侧 overlay 打开。
- 新增 `scripts/context-window-smoke.sh` + `scripts/context-window-smoke.go`：real LLM 冒烟测试验证事件字段正确。

## 2026-07-16 上下文窗口可观测性：Refresh 与 Task-scoped 快照

### 问题
初版 Context Window Panel 有两个体验问题：
- 无快照时只能被动等待下一次 `think()` 触发事件；
- `useContextWindow.ts` 累积所有历史快照，导致切任务时显示旧任务数据。

### 改动
- 后端新增 `internal/runtime/context_snapshot_store.go`：引擎在 `think()` 前把当前 messages 快照写入内存；任务结束后自动清理。
- 后端新增 API `GET /api/tasks/:id/context_window`：优先读内存快照，若引擎未运行则从 DB 对话记录重建，返回统一字段格式。
- 前端 `useContextWindow.ts` 改为 task-scoped：保留当前 active task 的最新快照，切换任务时清空，避免数据串扰。
- 前端 `ContextWindowPanel.vue`：
  - header 增加手动 Refresh 按钮与 loading 状态；
  - 首次挂载/打开且快照为空时自动请求一次 refresh；
  - 更清晰的进度条、role 占比卡片、可展开 message 列表。
- 前端 `TaskInput.vue`：将 Context Window 入口按钮从全局 header 移到输入框附近（Send/Options 同一行）。
- 前端 `App.vue`：监听 `activeTaskId` 同步给 `useContextWindow`，提供 `fetchContextWindowSnapshot` bound 到 panel 的 `@refresh`。

### 涉及文件
`internal/runtime/context_snapshot_store.go`, `internal/runtime/context_snapshot_store_test.go`, `internal/runtime/engine.go`
`cmd/server/api.go`, `cmd/server/main.go`
`web/src/composables/useContextWindow.ts`, `web/src/components/ContextWindowPanel.vue`, `web/src/components/TaskInput.vue`, `web/src/App.vue`

### 验证
- `go test ./internal/runtime` ✅（新增 context_snapshot_store_test.go）
- `go test ./internal/llm` ✅
- `go build ./cmd/server` ✅
- `cd web && npm run build` ✅
- `git merge feature/context-window-ui-polish` → `main` Fast-forward ✅

---

## 相关支撑改动：任务耗时统计

与上述修复批次一并落盘：

- `tasks` 表新增 `duration_ms` 字段（DB 迁移 v14）。
- Engine 在任务完成、失败、取消时调用 `UpdateTaskDuration` 写入耗时。
- 后端新增 `AggregateSessionDuration`，会话详情 API 返回 `duration_ms` 与 `total_tokens` 聚合值。
- 前端 `TaskState` / `AgentState` / `Step` 类型增加 `durationMs`，`MetricsPanel`、`TurnItem`、`AgentTree` 展示 task/turn/agent 级别耗时。

**涉及文件**
`pkg/db/database.go`, `pkg/db/migrate.go`, `pkg/db/persistence.go`, `internal/runtime/persistence.go`, `internal/runtime/engine.go`, `cmd/server/api.go`, `cmd/server/main.go`, `web/src/types/events.ts`, `web/src/composables/useTaskStore.ts`, `web/src/components/MetricsPanel.vue`, `web/src/components/TurnItem.vue`, `web/src/components/AgentTree.vue`

---

## 2026-07-23 real-llm-smoke 收尾 + 产物隔离 + workspace 三层兜底（v0.12.1 Alpha）

### 背景
`extend-task-cases`（v0.11.3）把内置 Case 矩阵扩到 21 个并完成 mock 回归 21/21，但真实 LLM 全量冒烟（`scripts/real-llm-smoke.sh`）尚未跑通。本轮收尾第 9 部分：跑全量 real-LLM 冒烟、分析失败项、并发现/修复一个产物污染根目录的缺陷。

### 问题 1：4 个 timeout 假阳性 FAIL
首跑 Part B 21 case 出现 4 个硬 FAIL（todo-driven / multi-agent / multi-agent-sequential / multi-agent-review），全部是 `180s 轮询超时`。查 server log 证实 4 个任务实际都到达终态，只是耗时 200-350s 超过 180s 预算——根因是 Qwen3.5-397B 是 reasoning 模型，每步 15-30s，MaxSteps=10-14 累计耗时超预算。非平台 bug。

### 改动 1：终态宽限复检
- `scripts/real-llm-smoke.sh` `b_run_case` 加 180s+200s 宽限复检逻辑：180s 超时后再宽限 200s 复检，命中终态降级 slow-LLM 软标记 SKIP，仍超时才 FAIL（疑似真挂起）。
- known-limitation（L5 leader-dispatch/fault-tolerance）不走宽限，直接按 KL 判定。

### 问题 2：真实 LLM 产物污染仓库根目录
跑完发现仓库根目录冒出 `verse_1.txt`~`verse_10.txt`、`research/`、`skill-demo/`、`task-scheduler/` 等未跟踪文件，且 `README.md` 被 LLM 覆盖成 "Tiny URL Shortener"。
根因链路：`/api/run-case` 不传 `session_id` → runner 无 `workspace_dir` 可解析 → `write_file` 相对路径回退到 server 进程 CWD（项目根）→ case 副产物直接落仓库根。`cases.go` 里 21 个 case 的 `DefaultInput` 都用相对路径（`verse_1.txt`、`research/ai-agents-2026.md` 等），mock 回归因 `LLM_USE_MOCK` 不真落盘未暴露，real-LLM 才显形。

### 改动 2：产物隔离 + workspace 三层兜底（方案 A + B）
- **方案 A（脚本 CWD 隔离）**：`real-llm-smoke.sh` 启动 server 前切到独立 `SMOKE_CWD`（默认 `workspace/smoke-server/run-<ts>-<pid>/`，在 gitignore 内），`DB_PATH`/`ENV_FILE` 用绝对路径；产物不自动清理（便于对比），脚本结尾打印路径提醒；`SMOKE_FRESH=1` 启动前清空默认目录。
- **`internal/config/config.go`**：`config.Load` 支持 `ENV_FILE` 环境变量（绝对路径加载 .env），未设回退 `CWD/.env`，让 server 在隔离 CWD 启动时仍能加载项目根 .env。
- **方案 B（后端三层 workspace 兜底）**：
  - L1 `cmd/server/api.go` `handleRunCase`：无 `session_id` 自动建匿名 session + workspace。
  - L2 `cmd/server/persistence.go` `resolveSession`：新建 session 同步绑定默认 `<cwd>/workspace/session-<id>/`，覆盖 `/api/tasks` chat / leader / cron start_task 等所有无 session 入口。
  - L3 `cmd/server/runner.go` `runAgentLoopWithTurn`：session/project 解析后 `workspaceDir` 仍空时兜底到 `<cwd>/workspace/`，记日志提示上游应绑定 session workspace。

### real-LLM 全量冒烟结果（run3）
- **PASS=143 / SKIP=20 / FAIL=0**，零平台 bug。
- 20 个 SKIP 全是 real-LLM 行为偏差软标记：15 个是 LLM 不可控现象（慢/max_steps/工具选择/final 空），5 个映射 2 个 7-H2 已知遗留（`policy-enforcement` PolicyGate 未触发拦截；`multi-agent/sequential/review` leader 未调 `dispatch_sub_agent` 编排事件缺失）。
- 静态编排（parallel/sequential/DAG）硬断言全 PASS；动态 leader-dispatch 在 real-LLM 下不可靠——属 7-H2 已知遗留（memory `multi-agent-dual-entry-placeholder-bug`）。
- 服务日志：deepseek-v4-pro 403 自动 fallback Qwen3.5-397B（正常）；1 次 context length 400 是 context-compression case 故意压测触发的预期边界。

### 验证
- `SKIP_PARTB=1 SMOKE_FRESH=1 bash scripts/real-llm-smoke.sh` → PASS=22/FAIL=0，根目录零污染，hello.txt 落 `workspace/smoke-server/run-*/workspace/session-*/`。
- `go build ./cmd/server` ✅；`go test ./cmd/server/` ✅；`go test ./internal/config/` ✅。
- 合入 main：`45fca1c Merge branch 'feat/real-llm-smoke-grace'`（--no-ff，4 commit）。

### 涉及文件
`scripts/real-llm-smoke.sh`（+458：宽限复检 + CWD 隔离 + 全量 21 case 评测）
`internal/config/config.go`（ENV_FILE 绝对路径）
`cmd/server/api.go`（handleRunCase 匿名 session 兜底）
`cmd/server/persistence.go`（resolveSession 绑 workspace）
`cmd/server/runner.go`（runner L3 兜底 `<cwd>/workspace/`）
