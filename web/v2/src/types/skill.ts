/**
 * Skill 与 SkillCommand 类型定义。
 */

/** Skill 作用域 */
export type SkillScope = 'global' | 'project' | 'session'

/** Skill 来源 */
export type SkillSource = 'built_in' | 'local_file' | 'local_db' | 'market' | 'mcp'

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

/** Skill 领域模型 */
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
  state: string
  scope: SkillScope
  project_id: string
  workspace_dir: string
  created_at: number
  updated_at: number
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

/** SkillCommand 详情，包含 prompt 全文 */
export interface SkillCommandDetail extends SkillCommand {
  prompt: string
}

/** Skill 注入到上下文窗口中的单个模板块（与后端 llm.SkillBlock 对齐） */
export interface SkillBlock {
  skill_id: string
  template_name: string
  estimated_tokens: number
  char_count: number
}

/** invoke 接口响应 */
export interface InvokeSkillCommandResult {
  enabled_skill_ids: string[]
  temporary_skill_id: string
}
