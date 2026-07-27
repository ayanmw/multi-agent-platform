# 已知缺陷与限制（Known Issues & Limitations）

> 本文档记录当前 demo 版本中已经 aware、但暂不修复的功能缺口。它们大多属于企业级增强能力，在现阶段优先级低于核心 Agent 循环、可观测性与编排闭环。纳入文档的目的是避免后人重复踩坑，并为后续 Phase / 企业版规划提供依据。

---

## 1. Checkpoint / 崩溃恢复：后端已保存，前端不可见、重启不自动恢复

### 1.1 当前现状

- **后端有保存能力**：`internal/runtime/checkpoint.go` 已实现 `CheckpointManager`，`internal/runtime/engine.go` 在每次 ReAct loop 迭代结束（tool 执行后）自动调用 `Save`，把完整对话历史、`stepIdx`、`totalTokens`、`taskProgress` 写入 `data/checkpoints/<task_id>.checkpoint.json`。
- **后端有恢复 API**：`cmd/server/checkpoint_api.go` 暴露：
  - `GET /api/checkpoints` — 列出所有可恢复 task ID。
  - `POST /api/checkpoints/recover` — 从 checkpoint 重建 Engine 并在后台 goroutine 继续跑。
- **前端无入口**：`web/`、`web/v2/` 中未检索到任何 `checkpoint` / `recover` 相关代码，控制室 UI 没有"查看 checkpoint"、"恢复任务"、"从断点续跑"的按钮或面板。
- **无自动恢复**：`cmd/server/main.go` 启动时只做了 `reclaimOrphanWorktrees`，没有扫描 `data/checkpoints/` 并自动拉起未完成任务的逻辑。
- **无事件可观测性**：checkpoint 保存、恢复、删除均未广播 `AgentEvent`，前端无法实时感知 checkpoint 生命周期。

### 1.2 这意味着什么

- 进程崩溃或服务器重启后，**理论上**任务可以从文件恢复，但：
  - 用户不知道哪些任务有 checkpoint。
  - 用户无法通过 UI 触发恢复。
  - 系统不会主动帮用户续跑。
- 因此当前 checkpoint 基本处于"暗能力"状态：文件在磁盘里，但产品层面不可用。

### 1.3 Checkpoint 本身的概念与现有数据

Checkpoint 是 ReAct Engine 运行到某一步时的**快照**，包含：

| 字段 | 含义 |
|------|------|
| `task_id` | 任务标识 |
| `agent_id` | 执行 agent 标识 |
| `step_idx` | 当前 ReAct loop 步数（0-based） |
| `total_tokens` | 累计 token 使用量 |
| `messages` | 完整 system / user / assistant / tool 对话历史 |
| `progress` | 可选的 `harness.TaskProgress` |
| `created_at` | 保存时间 |

恢复流程 `RecoverFromCheckpoint` 会把上述状态灌回新 Engine，并让 `MaxSteps` 从断点继续累加，保证任务有足够预算跑完。

### 1.4 为什么demo期不修复

- checkpoint 是企业级高可用能力，解决"服务器挂掉后任务不丢"的问题。
- demo 场景下：进程可控、任务可重跑、用户对中断容忍度高。
- 若现在强推自动恢复，会引入额外风险：重启时静默发起大量 LLM 调用、状态不一致（如 worktree / todo / 子任务未序列化进 checkpoint）、前端 trace 断层。

### 1.5 未来可能的实现方向

1. **最小可用版**  
   前端 `ManageTabs` 或侧栏新增 "Checkpoints" 面板：
   - 调 `GET /api/checkpoints` 列出 checkpoint（task_id、最后保存时间、步数）。
   - 每条提供"恢复"按钮，调 `POST /api/checkpoints/recover`。
   - 后端在 save/recover/delete 时广播 `checkpoint_saved` / `checkpoint_recovered` / `checkpoint_deleted` 事件。

2. **受控自动恢复版**  
   启动时扫描 `data/checkpoints/`，但默认不自动拉起，仅：
   - 在 UI 弹窗提示"检测到 N 个未完成 checkpoint"。
   - 用户一键确认后再批量恢复；或提供 `.env` 开关 `CHECKPOINT_AUTO_RECOVER=true`（默认 false）。

3. **完整企业级版**  
   - 把 worktree、todo、子任务状态、审批队列等运行时上下文序列化进 checkpoint。
   - 恢复时重建完整上下文，并在前端显示"从 checkpoint 恢复"标记。
   - 支持 checkpoint 过期清理、版本化、迁移。

### 1.6 相关文件

- `internal/runtime/checkpoint.go` — checkpoint 读写与恢复。
- `internal/runtime/engine.go:2664` — 每步自动保存。
- `cmd/server/checkpoint_api.go` — REST 恢复入口。
- `cmd/server/runner.go:157` — `AgentRunner.Recover` 统一恢复链路。
- `cmd/server/main.go:1080` — `CheckpointManager` 初始化。

---

## 2. 如何维护本文档

- 发现新的已知缺陷时，按相同格式追加条目。
- 修复某条缺陷后，将该条目移到 "已修复" 章节或删除，并在 `docs/API_CHANGELOG.md` / `roadmaps/ROADMAP.md` 中同步更新。
- 每个条目应包含：现状、影响、为什么暂不修复、未来方向、相关文件。
