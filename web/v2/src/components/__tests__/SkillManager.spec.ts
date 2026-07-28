/**
 * SkillManager 组件测试
 *
 * 验证：卡片渲染、启用开关、搜索过滤、打开详情/编辑/新建。
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref, nextTick } from 'vue'
import SkillManager from '../SkillManager.vue'
import type { Skill } from '@/types/skill'

const mockSkills: Skill[] = [
  {
    id: 'builtin-code-helper',
    version: '1.0.0',
    display_name: 'Code Helper',
    description: 'help',
    authors: [],
    tags: ['code'],
    source: 'built_in',
    source_url: '',
    is_local_editable: false,
    templates: [],
    parameters: [],
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
  },
  {
    id: 'local-db-skill',
    version: '1.0.0',
    display_name: 'Local DB Skill',
    description: 'local',
    authors: [],
    tags: [],
    source: 'local_db',
    source_url: '',
    is_local_editable: true,
    templates: [],
    parameters: [],
    required_tools: [],
    suggested_tools: [],
    permissions: [],
    triggers: { keywords: [], intents: [], file_patterns: [] },
    state: 'disabled',
    invalid_reason: '',
    scope: 'global',
    project_id: '',
    workspace_dir: '',
    created_at: 1,
    updated_at: 2,
  },
]

const toggleSkill = vi.fn(async (id: string) => {
  const s = skills.value.find(x => x.id === id)
  if (s) {
    s.state = s.state === 'enabled' ? 'disabled' : 'enabled'
    if (s.state === 'enabled') enabledIds.value.add(id)
    else enabledIds.value.delete(id)
  }
})

vi.mock('@/composables/useSkills', () => {
  const skills = ref<Skill[]>([])
  const enabledIds = ref<Set<string>>(new Set())
  return {
    useSkills: () => ({
      skills,
      loading: ref(false),
      error: ref(null),
      enabledIds,
      filteredSkills: ref((filter?: { q?: string; source?: string }) => {
        let result = skills.value
        if (filter?.source) result = result.filter(s => s.source === filter.source)
        if (filter?.q) {
          const q = filter.q.toLowerCase()
          result = result.filter(s => s.id.toLowerCase().includes(q) || s.display_name.toLowerCase().includes(q))
        }
        return result
      }),
      deleteSkill: vi.fn(),
      toggleSkill,
      refresh: vi.fn(),
      loadSkills: vi.fn(),
    }),
  }
})

vi.mock('@/composables/useToast', () => ({
  useToast: () => ({ showError: vi.fn(), showInfo: vi.fn() }),
}))

async function mountWithSkills(list: Skill[]) {
  const { useSkills } = await import('@/composables/useSkills')
  const store = useSkills()
  store.skills.value = list
  store.enabledIds.value = new Set(list.filter(s => s.state === 'enabled').map(s => s.id))
  return mount(SkillManager, { attachTo: document.body })
}

beforeEach(() => {
  document.body.innerHTML = ''
})

afterEach(() => {
  vi.clearAllMocks()
})

describe('SkillManager', () => {
  it('渲染全部 skill 卡片', async () => {
    const wrapper = await mountWithSkills(mockSkills)
    expect(wrapper.findAll('.skill-card').length).toBe(2)
  })

  it('built_in 卡片显示 Fork 而非 Edit', async () => {
    const wrapper = await mountWithSkills([mockSkills[0]])
    const btns = wrapper.findAll('.skill-action-btn')
    const texts = btns.map(b => b.text())
    expect(texts).toContain('Fork')
    expect(texts).not.toContain('Edit')
  })

  it('local_db 卡片显示 Edit 和 Delete', async () => {
    const wrapper = await mountWithSkills([mockSkills[1]])
    const btns = wrapper.findAll('.skill-action-btn')
    const texts = btns.map(b => b.text())
    expect(texts).toContain('Edit')
    expect(texts).toContain('Delete')
  })

  it('点击开关调用 toggleSkill', async () => {
    const wrapper = await mountWithSkills([mockSkills[0]])
    const checkbox = wrapper.find('.skill-switch input')
    await checkbox.element.dispatchEvent(new Event('change'))
    await nextTick()
    const { useSkills } = await import('@/composables/useSkills')
    expect(useSkills().toggleSkill).toHaveBeenCalledWith('builtin-code-helper')
  })

  it('按 source 过滤只展示匹配项', async () => {
    const wrapper = await mountWithSkills(mockSkills)
    const sourceSelect = wrapper.find('.skill-select')
    await sourceSelect.setValue('local_db')
    expect(wrapper.findAll('.skill-card').length).toBe(1)
  })
})
