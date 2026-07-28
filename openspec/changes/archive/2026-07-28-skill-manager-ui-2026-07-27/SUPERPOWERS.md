# Execution Discipline — superpowers

## TDD 顺序

1. 先写 `types/skill.ts` 与 `useSkills.spec.ts` 测试：mock API 返回数据，期望 composable 正确解析并维护状态（红）。
2. 实现 `types/skill.ts` 与 `useSkills.ts`（绿）。
3. 写 `SkillManager.spec.ts`：props/skills 列表渲染、搜索过滤（红）。
4. 实现 `SkillManager.vue`（绿）。
5. 写 `SkillForm.spec.ts`：表单提交 JSON 校验（红）。
6. 实现 `SkillForm.vue`（绿）。
7. 写 `SkillPicker.spec.ts`：`/` 触发、键盘选择（红）。
8. 实现 `SkillPicker.vue` 与修改 `CommandBar.vue`（绿）。
9. 集成到 `ManageContent.vue`、`App.vue`、`ContextWindowPanel.vue`。

## 调试 checklist

列表为空：
1. 检查 `loadSkills()` 是否被调用。
2. 检查 API 返回数据结构是否与新类型一致。
3. 检查 composable 中 skills ref 是否被赋值。

enable switch 不同步：
1. 检查 WS 事件 subscription 是否注册。
2. 检查 `skill_enabled` / `skill_disabled` 事件 data 中是否包含 skill_id。
3. 检查本地 enabledIds Set 是否响应式更新。

CommandBar `/` 不触发：
1. 检查输入框 `text` watch 是否以 `/` 开头。
2. 检查 picker 显示条件（光标位置、是否有前置字符）。
3. 检查 picker 选中后是否正确 emit 并调用 submit。

## Code Review 检查项

- [ ] 不破坏现有 `SkillPanel` 的 `trigger` emit 语义；`SkillManager` 的 emit 命名兼容。
- [ ] 所有 API 调用使用绝对 URL 或统一 baseURL。
- [ ] JSON 编辑失败有清晰错误提示，不直接 crash。
- [ ] 删除操作有二次确认。
- [ ] 移动端样式可用（卡片不过宽、表单可滚动）。
- [ ] 事件订阅在组件卸载时取消，避免内存泄漏。

## 完成前验证

- 跑完 `npm run test:unit` 并贴结果。
- 跑完 `npm run build`。
- 至少一次完整手动操作：创建 → 编辑 → 启用 → 发送 → 查看 ContextWindow → 删除。

## OpenSpec / Git 收尾

- tasks 全部勾选
- openspec-verify-change
- openspec-archive-change
- commit: `Phase skill-manager-ui: 完整 Skills 管理界面与 CommandBar / 触发器`
