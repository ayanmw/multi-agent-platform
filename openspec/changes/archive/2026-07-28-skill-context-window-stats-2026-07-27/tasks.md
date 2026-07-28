# Tasks: Context Window Skill 统计与事件同步

- [x] 1. 定义 `InjectedSkillBlock` / `SkillBlock` 类型
  - `internal/skill/skill.go` 新增 `InjectedSkillBlock`
  - `internal/llm/token_estimate.go` 新增 `SkillBlock`

- [x] 2. 扩展 `internal/llm/token_estimate.go`
  - `ContextWindowSnapshot` 增加 `SkillBlocks`
  - `BuildContextWindowSnapshot` 增加 `skillBlocks []SkillBlock` 参数

- [x] 3. `internal/runtime/engine.go`
  - NewEngine 记录 `injectedSkillBlocks`
  - 渲染完 skills 后广播 `skill_rendered` 事件
  - `think()` 把 skill blocks 传入 `BuildContextWindowSnapshot`

- [x] 4. `cmd/server/api.go`
  - context_window API 返回 `skill_blocks`（重建路径空切片）

- [x] 5. 前端 `types/events.ts`
  - `ContextWindowSnapshotData` 增加 `skill_blocks`
  - 新增 skill 生命周期事件类型（skill_enabled / disabled / loaded / unloaded / changed / rendered）

- [x] 6. 前端 `types/skill.ts`
  - 新增 `SkillBlock` 类型

- [x] 7. 新增 `useSkillEvents.ts` + 测试
  - 订阅 skill 事件，维护 reactive 状态、启用集合、blocks 缓存与计数器

- [x] 8. 修改 `ContextWindowPanel.vue`
  - 新增 "Skill Injection" 区
  - `InjectedSkillBadge` 显示在 system message 行

- [x] 9. 新增 `InjectedSkillBadge.vue`

- [x] 10. 测试
  - `internal/runtime/engine_skill_test.go`：skill block 注入 + `skill_rendered` 事件
  - `internal/llm/token_estimate_test.go`：SkillBlocks 透传且不重复计数
  - `cmd/server/skill_e2e_test.go`：context_window API 返回 skill_blocks 与 total 校验
  - `useSkillEvents.test.ts` + `ContextWindowPanel.test.ts`

- [x] 11. 验证
  - `go test ./internal/runtime ./internal/llm ./cmd/server ./internal/skill` ✅
  - `cd web/v2 && npm test` → 170 tests pass ✅
  - `go build ./...` ✅
  - mock regression 21/21 PASS ✅
  - `go test ./...` 全绿，除 `TestRealGeminiSearch` 因 Google API 免费额度耗尽失败（与本变更无关）
