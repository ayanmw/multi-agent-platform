/**
 * SkillForm 组件测试
 *
 * 验证：新建/编辑模式渲染、JSON 校验、提交调用 create/update。
 */
import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import SkillForm from '../SkillForm.vue'
import type { Skill } from '@/types/skill'

const baseSkill: Skill = {
  id: 'edit-skill',
  version: '1.0.0',
  display_name: 'Edit Me',
  description: 'desc',
  authors: [],
  tags: ['a', 'b'],
  source: 'local_db',
  source_url: '',
  is_local_editable: true,
  templates: [{ name: 'system_prompt', content: 'hello', variables: [], is_required: true }],
  parameters: [{ name: 'p1', type: 'string', required: true, description: 'param', default: undefined }],
  required_tools: [],
  suggested_tools: [],
  permissions: [],
  triggers: { keywords: [], intents: [], file_patterns: [] },
  state: 'enabled',
  invalid_reason: '',
  scope: 'global',
  project_id: '',
  workspace_dir: '',
  created_at: 1,
  updated_at: 2,
}

const createSkill = vi.fn(async (req: any) => ({ ...req, id: req.id } as any))
const updateSkill = vi.fn(async (id: string, changes: any) => ({ id, ...changes } as any))
const refresh = vi.fn()

vi.mock('@/composables/useSkills', () => ({
  useSkills: () => ({
    createSkill,
    updateSkill,
    refresh,
  }),
}))

vi.mock('@/composables/useToast', () => ({
  useToast: () => ({ showError: vi.fn(), showInfo: vi.fn() }),
}))

beforeEach(() => {
  document.body.innerHTML = ''
  vi.clearAllMocks()
})

describe('SkillForm', () => {
  it('新建模式显示空 ID 输入框', async () => {
    const wrapper = mount(SkillForm, { props: { skill: null }, attachTo: document.body })
    await flushPromises()
    const title = document.body.querySelector('.skill-form-title')
    expect(title).not.toBeNull()
    expect(title!.textContent).toBe('New Skill')
    const idInput = document.body.querySelector('input[type="text"]') as HTMLInputElement
    expect(idInput.value).toBe('')
  })

  it('编辑模式禁用 ID 并回填数据', async () => {
    const wrapper = mount(SkillForm, { props: { skill: baseSkill }, attachTo: document.body })
    await flushPromises()
    expect(document.body.querySelector('.skill-form-title')?.textContent).toBe('Edit Skill')
    const inputs = Array.from(document.body.querySelectorAll('input[type="text"]'))
    const idInput = inputs.find(i => (i as HTMLInputElement).value === 'edit-skill')
    expect(idInput?.hasAttribute('disabled')).toBe(true)
  })

  it('非法 JSON 显示错误且不提交', async () => {
    const wrapper = mount(SkillForm, { props: { skill: null }, attachTo: document.body })
    await flushPromises()
    const areas = Array.from(document.body.querySelectorAll('textarea'))
    const templatesArea = areas.find(a => a.value.includes('system_prompt'))
    if (templatesArea) {
      templatesArea.value = '{bad json}'
      templatesArea.dispatchEvent(new Event('input', { bubbles: true }))
    }
    await flushPromises()

    const buttons = Array.from(document.body.querySelectorAll('button'))
    const primaryBtn = buttons.find(b => b.textContent?.includes('Create') || b.textContent?.includes('Save'))
    expect(primaryBtn).toBeDefined()
    primaryBtn!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    const errorEl = document.body.querySelector('.skill-form-error')
    expect(errorEl).not.toBeNull()
    expect(errorEl!.textContent).toContain('JSON')
    expect(createSkill).not.toHaveBeenCalled()
    expect(updateSkill).not.toHaveBeenCalled()
  })
})
