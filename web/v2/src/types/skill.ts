/**
 * Skill 与 SkillCommand 类型定义。
 *
 * 与后端 internal/skill/skill.go 的 JSON 标签对齐。
 */

/** Skill 作用域：决定启用状态的 Skill 是否被注入 Engine */
export type SkillScope = 'global' | 'project' | 'session'

/** Skill 来源 */
export type SkillSource = 'built_in' | 'local_file' | 'local_db' | 'market' | 'mcp'

/** Skill 生命周期状态 */
export type SkillState = 'discovered' | 'validated' | 'loaded' | 'enabled' | 'disabled' | 'invalid'

/** Skill prompt 模板 */
export interface SkillTemplate {
  name: string
  content: string
  variables: string[]
  is_required: boolean
}

/** Skill 参数定义 */
export interface SkillParameter {
  name: string
  type: string
  required: boolean
  default?: unknown
  description: string
}

/** Skill 自动触发规则 */
export interface SkillTriggers {
  keywords: string[]
  intents: string[]
  file_patterns: string[]
}

/** Skill 领域模型：可复用 prompt + 任务知识包 */
export interface Skill {
  id: string
  version: string
  display_name: string
  description: string
  authors: string[]
  tags: string[]
  source: SkillSource
  source_url: string
  is_local_editable: boolean
  templates: SkillTemplate[]
  parameters: SkillParameter[]
  required_tools: string[]
  suggested_tools: string[]
  permissions: string[]
  triggers: SkillTriggers
  state: SkillState
  invalid_reason: string
  scope: SkillScope
  project_id: string
  workspace_dir: string
  created_at: number
  updated_at: number
}

/** 后端创建 Skill 请求 */
export interface CreateSkillRequest {
  id: string
  display_name: string
  description?: string
  content: string
  parameters?: SkillParameter[]
  variables?: Record<string, unknown>
  tags?: string[]
  authors?: string[]
  scope?: SkillScope
  project_id?: string
  workspace_dir?: string
}

/** 后端更新 Skill 请求（字段全部可选） */
export interface UpdateSkillRequest {
  display_name?: string
  description?: string
  content?: string
  parameters?: SkillParameter[]
  scope?: SkillScope
  project_id?: string
  workspace_dir?: string
}

/** SkillCommand 作用域 */
export type SkillCommandScope = 'global' | 'project'

/** 后端返回的 SkillCommand 列表项 */
export interface SkillCommand {
  id: string
  name: string
  description: string
  scope: SkillCommandScope
  workspace_dir: string
  project_id: string
  source_path: string
  skill_id: string
  tags: string[]
  icon: string
}

/** SkillPicker 中同时展示 skill 还是 command */
export type PickerItemKind = 'skill' | 'command'

/** SkillCommand 详情，包含 prompt 全文 */
export interface SkillCommandDetail extends SkillCommand {
  prompt: string
}

/** invoke 接口响应 */
export interface InvokeSkillCommandResult {
  enabled_skill_ids: string[]
  temporary_skill_id: string
}

/** SkillPicker 统一展示项（skill 或 command） */
export interface SkillPickerItem {
  kind: PickerItemKind
  id: string
  name: string
  description: string
  source?: 'built_in' | 'local_file' | 'local_db'
  state?: SkillState
  /** command 专属字段 */
  command?: SkillCommand
  /** skill 专属字段 */
  skill?: Skill
}

/** 上下文窗口中 Skill 注入块（Spec 5 字段） */
export interface SkillBlock {
  skill_id: string
  template_name: string
  estimated_tokens: number
  char_count: number
}
