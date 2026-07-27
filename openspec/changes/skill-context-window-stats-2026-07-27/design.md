# Design: Context Window Skill 统计与事件同步

## 关键决策

1. **Skill 注入记录**
   在 `internal/runtime/engine.go` 的 `NewEngine` 中，渲染每个 active skill 模板时构建 `InjectedSkillBlock`：
   ```go
   type InjectedSkillBlock struct {
       SkillID      string
       TemplateName string
       Content      string
       CharCount    int
       EstimatedTokens int
   }
   ```
   保存在 engine 字段 `injectedSkillBlocks []InjectedSkillBlock`。

2. **Context Snapshot 扩展**
   在 `internal/llm/token_estimate.go` 的 `BuildContextWindowSnapshot` 或 `ContextWindowSnapshot` 中新增：
   ```go
   SkillBlocks []SkillBlock
   ```
   其中 `SkillBlock` 字段与 `InjectedSkillBlock` 对应，但 content 可省略（前端需要时从 detail API 拿）。

3. **token 估算**
   复用 `EstimateTokenCount` 对 skill content 估算 token。
   skill 总 token 计入 `estimated_total_tokens` 的方式：
   - 保持现有 messages 估算不变；skill_blocks 给出明细，但不重复计入 total（因为 skill content 已经作为 system message 的一部分计入了 messages）。
   - 若 system prompt 中 skill 部分独立成块统计，需确保 messages[0] 中估算已包含 skill content。
   - **推荐**：skill_blocks 只做"明细展示"，它的 `EstimatedTokens` 与 messages[0] 的估算共享同一份文本，不额外加 total。

4. **事件同步**
   - `skill_enabled` / `skill_disabled`：由 registry UpdateState + REST/Tool enable 触发，已存在，本 Spec 确保 EventBus 广播。
   - `skill_loaded` / `skill_unloaded` / `skill_changed`：由 filesystem/command loader 触发（Spec 2/3）。
   - 新增 `skill_rendered`：在 Engine `NewEngine` 渲染完 active skills 后广播，data 包含 `skill_blocks`。
   - 新增 `skill_context_changed`：每次启用/禁用 skill 导致下次 run 的 active 列表变化时发送（可选，MVP 不做）。

5. **Engine 生成 snapshot**
   每次 `think()` 调用 `BuildContextWindowSnapshot(selectedModel, maxTokens, e.messages)` 时，同时传入 `e.injectedSkillBlocks`。函数返回的 `ContextWindowSnapshot` 中 `SkillBlocks` 明细附在 messages 之外。

6. **前端数据流**
   - `useContextWindow` 继续监听 `context_window_snapshot`。
   - `ContextWindowPanel` 在 capacity ring 下方新增独立 "Skill Injection" section，列出 `data.skill_blocks`。
   - 若某条 system message 来自 skill 注入，timeline 中显示 `InjectedSkillBadge`（通过比较 content 与 skill_blocks 内容或加 metadata）。
   - 新增 `useSkillEvents.ts`：订阅 `skill_enabled` / `skill_disabled` / `skill_loaded` / `skill_rendered`，维护 `enabledSkillIds` 与 `lastRenderedBlocks` reactive 状态。
