# Tasks: Skill Scope / Project / Workdir 基础设施

- [x] 1. 扩展 `internal/skill/skill.go` 数据模型
  - 新增 `Scope`、`ProjectID`、`WorkspaceDir` 字段
  - 新增 Scope 常量 `global` / `project` / `session`
  - 不破坏 JSON 与 DB 反序列化

- [x] 2. `pkg/db/skill.go` migration
  - 新 migration（建议 v29）给 `skills` 表增加 `scope`、`project_id`、`workspace_dir`
  - 更新 `SaveSkill` / `GetSkill` / `ListSkills`

- [x] 3. `internal/skill/store.go` 同步 CRUD
  - 读取/写入三列
  - 保持 JSON 复杂字段不变

- [x] 4. `internal/skill/registry.go` 新增 `ResolveActiveSkills`
  - 输入：registry、projectID、workspaceDir
  - 返回 enabled skill ID 列表
  - 去重：project > global
  - 行为单元测试覆盖

- [x] 5. `cmd/server/api_skill.go` REST 扩展
  - `GET /api/skills` 增加可选 `scope`、`project_id`、`workdir` 过滤参数
  - `POST /api/skills` body 增加 `scope`、`project_id`、`workspace_dir`
  - `PUT /api/skills/:id` 允许修改 scope 相关字段（local_db 且 editable）
  - 保留原测试通过

- [x] 6. `cmd/server/runner.go` 注入 `ResolveActiveSkills` 与 `SkillVariables`
  - `runAgentLoopWithTurn` 与 `Recover` 两个入口
  - `SkillVariables` 包含 `project_id`、`project_name`、`session_id`、`workspace_dir`

- [x] 7. 确保 `internal/runtime/engine.go` 正确使用 `SkillVariables`
  - 若 nil 不 panic
  - 渲染时传 `cfg.SkillVariables`

- [x] 8. 新增/更新测试
  - `internal/skill/registry_test.go`：`ResolveActiveSkills` 各种过滤与覆盖场景
  - `cmd/server/api_skill_project_test.go`：E2E 创建 project scope skill 并验证运行期注入

- [x] 9. 运行验证
  - `go test ./internal/skill ./cmd/server ./internal/runtime`
  - `go build ./...`
  - `LLM_USE_MOCK=true scripts/cases-regression.sh`
