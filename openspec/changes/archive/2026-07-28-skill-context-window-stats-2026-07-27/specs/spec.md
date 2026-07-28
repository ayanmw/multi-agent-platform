# Spec: Context Window Skill 统计与事件同步

## 新增/修改文件

### 后端

1. `internal/skill/skill.go`
   - 新增 `InjectedSkillBlock` 类型（或放在 `internal/runtime` 更合适），定义见 design。

2. `internal/llm/token_estimate.go`
   - 新增 `SkillBlock` 类型：
     ```go
     type SkillBlock struct {
         SkillID         string `json:"skill_id"`
         TemplateName    string `json:"template_name"`
         EstimatedTokens int    `json:"estimated_tokens"`
         CharCount       int    `json:"char_count"`
         // Content 可选，不在 snapshot 中携带全文
     }
     ```
   - 新增 `ContextWindowSnapshot` 结构副本或修改现有结构，增加 `SkillBlocks []SkillBlock`。
   - 修改 `BuildContextWindowSnapshot` 签名：
     ```go
     func BuildContextWindowSnapshot(model string, maxContextTokens int, messages []ContextWindowMessage, skillBlocks []SkillBlock) ContextWindowSnapshot
     ```
   - 保持现有 JSON 字段不变（messages, model, max_context_tokens 等）。

3. `internal/runtime/engine.go`
   - 在 `NewEngine` 渲染 active skill 时，构建 `[]InjectedSkillBlock` 并保存到 engine 字段。
   - 在 `NewEngine` 末尾（或首次 think 前）广播 `skill_rendered` 事件，data = `{skill_blocks: [...]}`。
   - 修改 `think()` 中 `BuildContextWindowSnapshot` 调用，传入 `e.injectedSkillBlocks`。
   - 若 `SkillRegistry` 为空或 `ActiveSkills` 为空，`injectedSkillBlocks` 为空切片。

4. `internal/runtime/context_snapshot.go`
   - 若存在，确认 `RecordTaskContextSnapshot` 默认使用 interface{} 可接受带 `SkillBlocks` 的结构；否则改为存储 `llm.ContextWindowSnapshot` 类型。

5. `cmd/server/api.go`
   - `GET /api/tasks/:id/context_window` 返回包含 `skill_blocks` 的 JSON。
   - 若从 session_messages 重建 snapshot，也要构造空的 skill_blocks。

### 前端

6. `web/v2/src/types/events.ts`
   - `ContextWindowSnapshotData` 新增 `skill_blocks?: SkillBlock[]`。
   - 新增 skill 生命周期事件类型：
     - `skill_enabled`
     - `skill_disabled`
     - `skill_loaded`
     - `skill_unloaded`
     - `skill_changed`
     - `skill_rendered`

7. `web/v2/src/types/skill.ts`
   - 新增 `SkillBlock` 类型（与后端对齐）。

8. 新增 `web/v2/src/composables/useSkillEvents.ts`
   - 订阅 `skill_enabled` / `skill_disabled` / `skill_loaded` / `skill_unloaded` / `skill_changed` / `skill_rendered`。
   - 维护 `enabledSkillIds`、`skillBlocksByTask`、加载状态。

9. 修改 `web/v2/src/composables/useContextWindow.ts`
   - 确保 snapshot data 透传到组件；可不做额外处理。

10. 修改 `web/v2/src/components/ContextWindowPanel.vue`
    - capacity ring 与 composition 之间新增 "Skill Injection" 区块。
    - 列出每个 `skill_block`：skill 名、模板名、estimated_tokens、占比。
    - 若 `skill_blocks` 为空，显示 "No skill context injected"。
    - messages timeline 中 system message 若匹配 skill block content 前缀，显示 `InjectedSkillBadge`。

11. 新增 `web/v2/src/components/InjectedSkillBadge.vue`
    - 小 badge："skill: <name>"，hover 显示模板名。

## 测试

- `internal/runtime/engine_skill_test.go` 更新/新增：
  - 启用 skill 后启动 engine，验证 `injectedSkillBlocks` 非空、内容包含渲染后文本。
  - 验证 `skill_rendered` 事件已广播。
- `internal/llm/token_estimate_test.go` 新增：
  - `BuildContextWindowSnapshot` 传入 skillBlocks 后返回字段正确。
- `cmd/server/api_skill_context_test.go` 新增：
  - `GET /api/tasks/:id/context_window` 包含 skill_blocks。
- 前端测试中验证 `ContextWindowPanel` 渲染 skill blocks 区。
