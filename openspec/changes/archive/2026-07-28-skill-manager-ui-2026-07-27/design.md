# Design: 前端 SkillManager UI 改造

## 关键决策

1. **类型定义**
   新增 `web/v2/src/types/skill.ts`，与后端 `internal/skill/skill.go` 对齐：
   ```ts
   export type SkillSource = 'built_in' | 'local_file' | 'local_db' | 'market' | 'mcp'
   export type SkillScope = 'global' | 'project' | 'session'
   export type SkillState = 'discovered' | 'validated' | 'loaded' | 'enabled' | 'disabled' | 'invalid'

   export interface SkillTemplate { name: string; content: string }
   export interface SkillParameter { name: string; type: string; required: boolean; default?: string; description?: string }
   export interface Skill {
     id: string
     version: string
     display_name: string
     description: string
     authors?: string[]
     tags: string[]
     source: SkillSource
     source_url?: string
     scope: SkillScope
     project_id?: string
     workspace_dir?: string
     is_local_editable: boolean
     templates: SkillTemplate[]
     parameters: SkillParameter[]
     required_tools?: string[]
     suggested_tools?: string[]
     permissions?: string[]
     state: SkillState
     invalid_reason?: string
     created_at?: number
     updated_at?: number
   }
   ```
   原有 `useSkills.ts` 内的自定义 `Skill` 类型删除，统一使用新类型。

2. **useSkills.ts 重写**
   State:
   ```ts
   const skills = ref<Skill[]>([])
   const loading = ref(false)
   const error = ref<string | null>(null)
   const enabledIds = ref<Set<string>>(new Set())
   ```
   Methods:
   - `loadSkills(params?: {source?, scope?, project_id?, workdir?, q?})`
   - `getSkill(id): Skill | undefined`
   - `createSkill(draft): Promise<Skill>`
   - `updateSkill(id, changes): Promise<Skill>`
   - `deleteSkill(id): Promise<void>`
   - `enableSkill(id)` / `disableSkill(id)` / `toggleSkill(id)`
   - `searchSkills(q)`
   - `refresh()`
   事件订阅：
   - 监听 `skill_enabled` / `skill_disabled` / `skill_loaded` / `skill_unloaded` / `skill_changed` 并增量更新 `skills` 与 `enabledIds`。

3. **SkillManager.vue**
   布局：
   - 顶部：标题、搜索框、source/scope 下拉过滤、"New Skill" 按钮。
   - 中部：卡片网格或紧凑列表。每张卡片显示：
     - name / description / tags chips
     - source badge（built_in / local_file / local_db）
     - scope badge
     - enable/disable 开关
     - "View" 按钮
     - 对 local_db 显示 "Edit" / "Delete"；对 built_in shadow 显示 "Forked" 提示。
   - 空态：提示可创建 skill 或扫描目录。

4. **SkillDetailModal.vue**
   - Tabs：Overview / Templates / Parameters / Metadata。
   - 展示 source_url、scope、project_id、created_at。
   - local_file 只读，文案提示"文件系统 skill，请直接修改文件"。
   - built_in 不可编辑；若存在 shadow，提示"已被 fork 为 local_db"。
   - local_db / shadow 可点击 "Edit" 打开 `SkillForm.vue`。

5. **SkillForm.vue**
   - 表单字段：ID（新建时）、Display Name、Description、Tags（逗号/JSON）、Scope、Project ID、Templates JSON、Parameters JSON。
   - Templates 提供简单 textarea 编辑 JSON；后续可升级为 key-value。
   - 校验：至少一个 `system_prompt` 或 `task_prompt` 模板。
   - 提交调用 `createSkill` 或 `updateSkill`。

6. **与 SkillPicker / CommandBar 集成**
   - `SkillPicker` 调用 `useSkills().skills` 与 `useSkillCommands().commands`。
   - 按 source 分组展示（Commands / Skills）。
   - 选中 command 时 emit `select-command`；选中 skill 时 emit `select-skill`。
   - `CommandBar.vue` 输入 `/` 显示 picker；选中后处理：
     - command → 调用 `invokeCommand(id)`，然后 `emit('send', remainingText)`。
     - skill → 调用 `enableSkill(id)`，然后 `emit('send', remainingText)`。

7. **App.vue 调整**
   - 现有 `handleSend` 解析 `/skill-id` 保留。
   - 新增 `handleCommandSelect(command)`：调用 `POST /api/skill-commands/:id/invoke`，成功后发送剩余文本。
   - `handleTriggerSkill` 不再仅 prefill，而是直接启用并发送。

8. **事件同步**
   - 新增 `useSkillEvents.ts`（也可合并到 `useSkills.ts`）：监听 skill 事件，更新 skills 与 enabledIds；可被 `SkillManager` 与 `ContextWindowPanel` 复用。

9. **样式方向**
   - 采用 `frontend-design` skill 的"白盒 + 工具感"美学：深色面板、锐利边框、高对比 accent、monospace 细节、清晰的信息层级。
   - 与现有 v2 控制室 UI（深色、cyber-brutalist）保持一致。
