# 变更历史

> 本文档汇总 Multi-Agent Platform 的重要 bug 修复、体验优化与行为变更。
> 与 `roadmaps/ROADMAP.md` 配合使用：路线图记录 Phase 级别的大事，本文档记录每次用户反馈驱动的具体改动。
>
> 最后更新：2026-07-11

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
- 新增关闭按钮（× Close）和点击遮罩关闭。
- 关闭后自动回到之前的 Session/Task 视图；左侧 Session 切换不受影响。

**涉及文件**
`web/src/App.vue`, `web/src/components/MemoryBrowser.vue`

---

### 3. 展开/折叠全部 + 最新内容自动展开

**改动**
- `App.vue` 任务视图区新增 "Expand All" / "Collapse All" 按钮，控制当前所有 Turn 与 Step。
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
`max_steps_exceeded` 后点击 "Continue with max steps ×2" 会创建新的 root task，丢失了 Session 中的对话上下文。

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

### 涉及文件
`internal/llm/token_estimate.go`, `internal/llm/token_estimate_test.go`, `internal/llm/model_profile.go`
`internal/runtime/engine.go`, `pkg/event/event.go`
`web/src/types/events.ts`, `web/src/composables/useContextWindow.ts`, `web/src/components/ContextWindowPanel.vue`, `web/src/App.vue`
`scripts/context-window-smoke.sh`, `scripts/context-window-smoke.go`

### 验证
- `go test ./internal/llm` ✅
- `go build ./cmd/server` ✅
- `cd web && npm run build` ✅
- `bash scripts/context-window-smoke.sh` (real LLM) ✅ PASS 9 / FAIL 0

---

## 相关支撑改动：任务耗时统计

与上述修复批次一并落盘：

- `tasks` 表新增 `duration_ms` 字段（DB 迁移 v14）。
- Engine 在任务完成、失败、取消时调用 `UpdateTaskDuration` 写入耗时。
- 后端新增 `AggregateSessionDuration`，会话详情 API 返回 `duration_ms` 与 `total_tokens` 聚合值。
- 前端 `TaskState` / `AgentState` / `Step` 类型增加 `durationMs`，`MetricsPanel`、`TurnItem`、`AgentTree` 展示 task/turn/agent 级别耗时。

**涉及文件**
`pkg/db/database.go`, `pkg/db/migrate.go`, `pkg/db/persistence.go`, `internal/runtime/persistence.go`, `internal/runtime/engine.go`, `cmd/server/api.go`, `cmd/server/main.go`, `web/src/types/events.ts`, `web/src/composables/useTaskStore.ts`, `web/src/components/MetricsPanel.vue`, `web/src/components/TurnItem.vue`, `web/src/components/AgentTree.vue`
