# Spec: 前端 SkillManager UI 改造

## 新增/修改文件

### 类型与 composables

1. 新增 `web/v2/src/types/skill.ts`
   - 定义 `SkillSource`、`SkillScope`、`SkillState`、`SkillTemplate`、`SkillParameter`、`Skill`、`SkillCommand`、`SkillBlock`。

2. 重写 `web/v2/src/composables/useSkills.ts`
   - 删除原有内部 `Skill` 类型，导入 `types/skill.ts`。
   - 实现 State：skills、loading、error、enabledIds、lastUpdated。
   - 实现 Methods：loadSkills、loadEnabled、getSkill、createSkill、updateSkill、deleteSkill、enableSkill、disableSkill、toggleSkill、searchSkills、refresh。
   - 事件监听：onMounted 时订阅 WS skill 事件并增量更新；onUnmounted 取消订阅。
   - 辅助：isEditable(skill)（source === 'local_db'），isReadOnly(skill)（source === 'local_file'），isBuiltIn(skill)（source === 'built_in'）。

3. 新增 `web/v2/src/composables/useSkillCommands.ts`（若 Spec 3 尚未完成则占位；否则复用）
   - loadCommands、getCommand、invokeCommand。

4. 新增/复用 `web/v2/src/composables/useSkillEvents.ts`
   - 监听 skill 生命周期事件 + `skill_rendered`。

### UI 组件

5. 新增 `web/v2/src/components/SkillManager.vue`
   - 替换 `SkillPanel.vue`。
   - Props：无（依赖 useSkills）。
   - Emits：`trigger-skill`（保持兼容，但语义改为直接启用并发送）。
   - 模板结构：
     - header：标题、搜索 input、source select、scope select、New Skill button。
     - body：虚拟滚动或普通 grid 卡片列表。
     - card：name、description、tags、source badge、enable switch、View button、Edit/Delete 按钮（条件渲染）。
     - empty state。
   - 点击 View → 打开 `SkillDetailModal`。
   - 点击 New / Edit → 打开 `SkillForm`。

6. 新增 `web/v2/src/components/SkillDetailModal.vue`
   - Props：`skill: Skill`。
   - Emits：`edit`、`close`。
   - Tabs：Overview / Templates / Parameters / Metadata。
   - 对 local_file：底部提示"只读，修改请编辑 SKILL.md"。
   - 对 built_in 无 shadow：提示"内置 skill 不可编辑"。
   - 对 built_in shadow 或 local_db：显示 Edit 按钮。

7. 新增 `web/v2/src/components/SkillForm.vue`
   - Props：`skill?: Skill`（编辑时传入）。
   - Emits：`save`、`cancel`。
   - 表单字段：
     - id（新建可编辑，编辑禁用）
     - display_name
     - description
     - tags：textarea，逗号/换行分隔
     - scope：select (global/project/session) — session 本次仅界面展示， Spec 4 才 tool 支持
     - project_id：输入框
     - templates：JSON textarea（至少包含 system_prompt）
     - parameters：JSON textarea（可空）
   - 提交前校验 JSON 并提示。

8. 新增/复用 `web/v2/src/components/SkillPicker.vue`
   - 浮层，支持 `/` 触发。
   - 列出 commands（优先）与 skills。
   - 键盘 ↑/↓/Enter/Esc 导航。
   - 分组：Commands / Skills / Built-in。

### 现有组件修改

9. 修改 `web/v2/src/components/ManageContent.vue`
   - skills tab 使用 `<SkillManager @trigger-skill="onTriggerSkill" />`。

10. 修改 `web/v2/src/components/CommandBar.vue`
    - 新增 `/` 触发逻辑。
    - 注入 `SkillPicker` 到 textarea 下方或 body portal。
    - 监听 `text`：若以 `/` 开头且光标前无空格/字符，显示 picker。
    - picker 选中后：填充 `text = '/' + selected.id + ' '`，关闭 picker，触发 `submit()` 或让外部发送。

11. 修改 `web/v2/src/App.vue`
    - `handleTriggerSkill(command)`：直接 `enableSkill(id)` 后调用 `handleSend(command)`，而不是仅 prefill。
    - 新增 `handleCommandSelect(command)`：调用 `useSkillCommands().invokeCommand(command.id)`，成功后调用 `handleSend(remaining)`。

12. 修改 `web/v2/src/components/ContextWindowPanel.vue`
    - 新增 Skill Injection 区块（Spec 5）。
    - 新增 InjectedSkillBadge。

13. 修改 `web/v2/src/types/events.ts`
    - 扩展 `ContextWindowSnapshotData`。
    - 新增 skill 生命周期事件类型。

### 路由与入口

14. 确认 `ManageTabs.vue` 中 skills tab 标签保留。
15. 确认移动端 `mobile-tab-view` 中 `ManageContent` 同样渲染 `SkillManager`。

### 测试

16. 新增/更新测试文件
    - `web/v2/src/composables/__tests__/useSkills.spec.ts`：mock API 与事件，验证 CRUD 与状态。
    - `web/v2/src/components/__tests__/SkillManager.spec.ts`：卡片渲染、搜索过滤、开关状态。
    - `web/v2/src/components/__tests__/SkillForm.spec.ts`：表单校验与提交。
    - `web/v2/src/components/__tests__/SkillPicker.spec.ts`：`/` 触发与选择。

## 非目标

- 不实现 skill 的市场/MCP 来源 UI。
- 不在本 Spec 中实现 skill 实时推荐/AI 自动发现。
