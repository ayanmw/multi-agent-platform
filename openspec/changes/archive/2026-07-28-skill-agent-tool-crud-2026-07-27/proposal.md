# Proposal: Skill Agent Tool 完整 CRUD

## 问题

当前 LLM 只能通过 3 个 Skill Agent Tool 操作 skill：`skill/create_local`、`skill/delete_local`、`skill/list`。缺少 get / update / enable / disable / search，LLM 无法查看详情、修改 skill、搜索；同时 `internal/skill/builtin.go` 中 built_in skill 的 `IsLocalEditable=false`，前端和 LLM 都无法编辑。虽然 Spec 1 决策 built_in 不可直接修改，但需要实现"fork 为 local_db shadow"的编辑路径，并且 LLM 必须清楚这一规则。

## 目标

1. 新增 `skill/get`：返回某个 skill 的完整模板与参数。
2. 新增 `skill/update_local`：更新 `local_db` skill；若目标是 built_in，自动 fork 为 local_db shadow 后修改。
3. 新增 `skill/enable`、`skill/disable`：启用/禁用 skill（受 scope/project/workdir 限制）。
4. 新增 `skill/search`：按关键词搜索 skill summary（只返回 id/name/description/source/tags/state/scope）。
5. 修改 `handleUpdateSkill` REST API：同样启用 built_in fork shadow。
6. 修改 `handleDeleteSkill` REST API：对 built_in shadow 删除后恢复 built_in；built_in 本体禁止删除。
7. 所有 LLM Tool 输出保持"summary 在前、详情需显式 get"的原则。

## 成功标准

- LLM 可以通过 Agent Tool 完成 create/get/update/delete/enable/disable/list/search。
- 更新 built_in 时，自动产生 `local_db` shadow，ID 相同，优先于 built_in 生效。
- 删除 shadow 后，built_in 重新生效。
- `go test ./internal/skill ./cmd/server` 通过；mock 回归 21/21 PASS。

## 关联变更

- 依赖 Spec 1 的 scope / project / workdir 字段与权限判定。
- Spec 6 前端 UI 依赖本 Spec 提供的 update/delete/enable/disable API。
