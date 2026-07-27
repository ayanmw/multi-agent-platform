# Tasks: Context Window Skill 统计与事件同步

- [ ] 1. 定义 `InjectedSkillBlock` / `SkillBlock` 类型
  - `internal/skill/skill.go` 或 `internal/llm/token_estimate.go`

- [ ] 2. 扩展 `internal/llm/token_estimate.go`
  - `ContextWindowSnapshot` 增加 `SkillBlocks`
  - `BuildContextWindowSnapshot` 接受 skillBlocks 参数

- [ ] 3. `internal/runtime/engine.go`
  - NewEngine 中记录 `injectedSkillBlocks`
  - 广播 `skill_rendered` 事件
  - think() 中传入 skillBlocks

- [ ] 4. `cmd/server/api.go`
  - context_window API 返回 `skill_blocks`

- [ ] 5. 前端 `types/events.ts`
  - 扩展 `ContextWindowSnapshotData`
  - 新增 skill 生命周期事件类型

- [ ] 6. 前端 `types/skill.ts`
  - 新增 `SkillBlock` 类型

- [ ] 7. 新增 `useSkillEvents.ts`
  - 订阅 skill 事件，维护 reactive 状态

- [ ] 8. 修改 `ContextWindowPanel.vue`
  - Skill Injection 区
  - InjectedSkillBadge

- [ ] 9. 测试
  - engine skill block 注入测试
  - token estimate 测试
  - context_window API 测试
  - 前端组件测试

- [ ] 10. 验证
  - `go test ./internal/runtime ./internal/llm ./cmd/server ./internal/skill`
  - `cd web/v2 && npm run test:unit`
  - `go build ./...`
  - mock regression 21/21 PASS
