# Execution Discipline — superpowers

## TDD 顺序

1. 先给 `ExecuteContext` 加 `Variables` 字段与 Engine 透传测试，确保不破坏现有 tool（红）。
2. 实现透传（绿）。
3. 写 `SkillUpdateLocalTool` 的 fork shadow 测试（红）。
4. 实现 fork shadow（绿）。
5. 依次实现 get/enable/disable/search/delete 并补测试。
6. 同步 REST API fork shadow，先写测试再实现。
7. 注册 tools 并跑完整 `internal/skill` 测试。

## 调试 checklist

LLM tool 提示无权限：
1. 检查 `ExecuteContext.Variables["project_id"]` 是否已填充。
2. 在 tool 入口打印 Variables。
3. 确认 `Registry.ExecuteWithCtx` 把 engine cfg.SkillVariables 正确写入 ctx.Variables。

built_in fork 失败：
1. 打印 built_in skill 原始数据与 updates。
2. 确认 store.Save 对 local_db shadow 使用相同 ID 且 source=local_db。
3. 确认 registry.Register 覆盖同名 built_in（registry 内部 map 直接覆盖即可）。

删除 shadow 后 built_in 未恢复：
1. 确认 delete 时仅删除 store 中 local_db 记录。
2. 确认 registry.Unregister 后，Loader 或后续查询不会再次加载 shadow（store 已无）。
3. 确认 built_in 仍在 registry（DefaultBuiltins 常驻），否则需要在 unregister 后重新注册 built_in。

## Code Review 检查项

- [ ] Agent Tool 输出格式稳定、可解析。
- [ ] 不暴露敏感字段（如 API key）给 LLM； skill prompt 本身可暴露。
- [ ] fork shadow 时保留原 built_in 的 templates/permissions 作为默认值。
- [ ] scope 校验在 Tool 与 REST 层都生效。
- [ ] 修改 built_in 产生的 shadow 在 manage UI 中有明确标识。

## 完成前验证

- 跑完 `go test` 并贴结果。
- 至少一次完整 LLM Tool CRUD 手动测试。
- mock regression 通过。

## OpenSpec / Git 收尾

- tasks 全部勾选
- openspec-verify-change
- openspec-archive-change
- commit: `Phase skill-agent-tool-crud: LLM 可完整 CRUD skill，built_in fork 为 local_db shadow`
