# Tasks: Skill Command 系统

- [ ] 1. 新增 `SkillCommand` 类型与 command 事件常量
  - `internal/skill/skill.go`
  - `internal/skill/events.go`

- [ ] 2. 新增 `internal/skill/command_loader.go`
  - 扫描 `.claude/commands/**/*.md`
  - 冒号分层 ID 生成
  - frontmatter 解析

- [ ] 3. 新增 `internal/skill/command_registry.go`
  - 独立内存注册表
  - Register/Unregister/Get/List/ListForWorkdir

- [ ] 4. `internal/skill/loader.go` 集成 command 扫描
  - LoadAll 加载全局 command
  - LoadForWorkdir 加载项目 command
  - RefreshAll 重扫

- [ ] 5. 新增 `cmd/server/api_skill_command.go`
  - `GET /api/skill-commands`
  - `GET /api/skill-commands/:id`
  - `POST /api/skill-commands/:id/invoke`

- [ ] 6. 启动与 session hook
  - `main.go` 启动加载
  - `api.go` session workdir 解析后加载

- [ ] 7. 前端类型 `web/v2/src/types/skill.ts`
  - Skill / SkillCommand 完整类型

- [ ] 8. 新增 `useSkillCommands.ts`
  - 加载/搜索/refresh command 列表

- [ ] 9. 新增 `SkillPicker.vue`
  - / 触发的浮层选择器
  - 分组展示命令与 skill

- [ ] 10. 修改 `CommandBar.vue`
  - 输入 `/` 显示 SkillPicker
  - 选中后预填充并触发 invoke

- [ ] 11. 修改 `App.vue`
  - invoke command 成功后发送剩余文本

- [ ] 12. 测试
  - command loader 单元测试
  - REST API 测试
  - 前端 SkillPicker 单元测试

- [ ] 13. 验证
  - `go test ./internal/skill ./cmd/server`
  - `cd web/v2 && npm run test:unit`
  - `go build ./...`
  - `LLM_USE_MOCK=true scripts/cases-regression.sh`
