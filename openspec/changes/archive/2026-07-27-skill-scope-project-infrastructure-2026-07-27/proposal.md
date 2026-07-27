# Proposal: Skill 作用域与全局/项目级基础设施

## 问题

当前 backend skill 注册表是单实例、全局的：
- `Skill` 没有 `Scope` / `ProjectID` / `WorkspaceDir` 字段，无法区分全局 skill 与项目级 skill。
- `GET /api/skills` 只能按 source 过滤，不能按当前 session 的 project/workdir 过滤。
- `cmd/server/runner.go` 直接用 `GetEnabledSkillIDs(globalSkillRegistry)`注入 Engine，所有启用的 skill 对所有 session 生效。
- `EngineConfig.SkillVariables` 当前为 `nil`，skill 模板中的 `{{project_id}}`、`{{workspace_dir}}` 等变量无法使用。

这导致：用户无法为不同项目维护独立的 skill 集合；项目级与全局 skill 会互相污染。

## 目标

1. 在 `Skill` 领域模型中引入 `Scope`（`global` / `project` / `session`）、`ProjectID`、`WorkspaceDir`。
2. DB skills 表增加对应列，并迁移。
3. REST API 支持按 `scope`、`project_id`、`workdir` 查询与创建。
4. 运行期按 session/project/workdir 解析真正需要激活的 skill 列表。
5. 向 Engine 注入 project/session/workdir 相关变量，供 skill 模板渲染。

## 成功标准

- 创建 `scope=project`、`project_id=foo` 的 skill 后，仅当 session 属于 `foo` 项目或 workdir 匹配时才被注入 Engine。
- 全局 skill 仍然对所有 session 生效，向后兼容。
- `go test ./internal/skill ./cmd/server ./internal/runtime` 全部通过。
- `LLM_USE_MOCK=true scripts/cases-regression.sh` 21/21 PASS。

## 关联变更

- 总设计：`.claude/plans/refactored-jumping-cocoa.md`
- Spec 2（文件系统扫描）依赖本 Spec。
- Spec 4（Agent Tool CRUD）依赖本 Spec 的 scope 权限。
- Spec 5（Context Window 统计）依赖本 Spec 的 EngineConfig 注入点。
