# Proposal: 前端 SkillManager UI 改造

## 问题

当前 `web/v2/src/components/SkillPanel.vue` 是一个占位组件：只展示 3 个硬编码 skill 卡片，带语义错误的 "Run" 按钮，没有状态、搜索、详情、CRUD。`useSkills.ts` 类型狭窄，只支持内置 skill 加载。前端无法查看 skill 来源、tags、parameters；无法新建/编辑/删除 local_db skill；无法查看 local_file 文件系统 skill；CommandBar 不支持 `/` 调用 command。本 Spec 负责完整重写前端 skill 管理 UI 与相关 composables。

## 目标

1. 新增 `web/v2/src/types/skill.ts`，完整定义后端返回的 Skill / SkillCommand / SkillBlock 类型。
2. 重写 `web/v2/src/composables/useSkills.ts`：
   - 加载所有来源 skill；
   - enable/disable/toggle；
   - create/update/delete local_db skill；
   - search / filter by source/scope/tags；
   - get skill detail；
   - 订阅 skill 生命周期事件并更新本地状态。
3. 新增 `SkillManager.vue` 替换 `SkillPanel.vue`，包含列表/搜索/过滤/启用/详情入口。
4. 新增 `SkillDetailModal.vue` / 抽屉，展示任意 skill 详情；local_file 只读，built_in 不可编辑，local_db 可编辑。
5. 新增 `SkillForm.vue`，新建/编辑 local_db skill，支持 templates、parameters、tags JSON 编辑。
6. 接入 CommandBar `/` SkillPicker（Spec 3 的 `SkillPicker.vue`）。
7. 接入 ContextWindow skill 统计展示（Spec 5）。
8. 更新 `ManageContent.vue` 路由到 `SkillManager`。
9. 更新 `App.vue` 的 `handleTriggerSkill`：点击 skill/command 直接启用并发送，而不是仅 prefill。

## 成功标准

- Manage 面板 Skills tab 列出所有 skill，local_file 只读，built_in 无编辑按钮。
- 搜索/按 source、scope、tags 过滤可用。
- local_db skill 可新建/编辑/删除；编辑 built_in 自动 fork 为 shadow（文案提示）。
- enable/disable 开关即时生效并 WS 同步。
- CommandBar `/` 弹出 picker，选中后发送。
- ContextWindowPanel 展示 skill 注入统计。
- `cd web/v2 && npm run test:unit` 通过；mock regression 21/21 PASS。

## 关联变更

- 依赖 Spec 1-5 全部后端 API 与事件。
- 最后实施的纯前端 Spec。
