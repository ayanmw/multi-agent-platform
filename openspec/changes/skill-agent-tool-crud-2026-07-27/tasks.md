# Tasks: Skill Agent Tool 完整 CRUD

- [ ] 1. 扩展 `ExecuteContext`/`ExecuteWithCtx` 透传 Variables
  - 新增 `Variables map[string]any`
  - Engine 把 SkillVariables 写入
  - 确保现有 tool 不 panic

- [ ] 2. 实现 `skill/get` Agent Tool
  - 参数 `id`
  - 返回完整 skill JSON

- [ ] 3. 实现 `skill/update_local` Agent Tool
  - 参数 `id`, `updates` JSON
  - built_in fork shadow 逻辑
  - local_db shadow 直接修改

- [ ] 4. 实现 `skill/enable`、`skill/disable` Agent Tool
  - scope 权限检查
  - 广播事件

- [ ] 5. 实现 `skill/search` Agent Tool
  - 参数 `q/source/scope`
  - 返回 summary 列表

- [ ] 6. 修改 `skill/delete_local`
  - built_in shadow 删除恢复 built_in
  - local_file 禁止删除

- [ ] 7. REST API `handleUpdateSkill` / `handleDeleteSkill` fork shadow 同步
  - 与 Agent Tool 行为一致

- [ ] 8. `cmd/server/main.go` 注册新增 tools

- [ ] 9. 测试
  - `internal/skill/tools_test.go`
  - 更新 `cmd/server/api_skill_test.go`

- [ ] 10. 验证
  - `go test ./internal/skill ./cmd/server`
  - `go build ./...`
  - mock regression 21/21 PASS
