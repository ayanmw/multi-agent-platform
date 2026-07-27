# Tasks: 前端 SkillManager UI 改造

- [ ] 1. 新增 `web/v2/src/types/skill.ts`
  - 完整 Skill / SkillCommand / SkillBlock 类型

- [ ] 2. 重写 `web/v2/src/composables/useSkills.ts`
  - CRUD、enable/disable、search、filter、event subscription

- [ ] 3. 新增/复用 `useSkillCommands.ts` 与 `useSkillEvents.ts`

- [ ] 4. 新增 `SkillManager.vue`
  - 列表、搜索、过滤、卡片、New/View/Edit/Delete

- [ ] 5. 新增 `SkillDetailModal.vue`
  - Tabs：Overview / Templates / Parameters / Metadata
  - 只读/可编辑状态提示

- [ ] 6. 新增 `SkillForm.vue`
  - 新建/编辑 local_db skill
  - JSON 编辑 templates / parameters

- [ ] 7. 新增 `SkillPicker.vue`
  - `/` 触发，command + skill 分组

- [ ] 8. 修改 `CommandBar.vue`
  - 监听 `/` 输入，显示 SkillPicker
  - 选中后预填充并发送

- [ ] 9. 修改 `ManageContent.vue`
  - skills tab 路由到 SkillManager

- [ ] 10. 修改 `App.vue`
  - handleTriggerSkill 直接启用并发送
  - handleCommandSelect 调用 invoke

- [ ] 11. 修改 `ContextWindowPanel.vue`
  - Skill Injection 区与 badge（Spec 5 联动）

- [ ] 12. 修改 `types/events.ts`
  - 扩展 snapshot，新增 skill 事件类型

- [ ] 13. 测试
  - useSkills 测试
  - SkillManager / SkillForm / SkillPicker 组件测试

- [ ] 14. 验证
  - `cd web/v2 && npm run test:unit`
  - `cd web/v2 && npm run build`
  - `go build ./...`
  - mock regression 21/21 PASS
