/**
 * CaseCard 组件测试
 *
 * 覆盖点（行为契约）：
 * - 点卡片 body → emit('view', id)
 * - 点 Run 按钮 → emit('run', id)，且不冒泡到 view
 * - 点 tag pill → emit('toggle-tag', tag)，且不冒泡到 view
 * - 非内置 case 显示 edit / delete 按钮，点击各自 emit
 * - 内置 case 隐藏 edit / delete，显示 builtin badge
 * - disabled prop 透传给 Run 按钮
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import CaseCard from './CaseCard.vue'
import type { Case } from '@/types/case'

function makeCase(overrides: Partial<Case> = {}): Case {
  return {
    id: 'c1',
    name: 'Demo Case',
    description: 'a demo',
    icon: '🚀',
    category: 'go',
    system_prompt: '',
    default_input: '',
    tags: ['code', 'backend'],
    is_builtin: false,
    created_at: '',
    updated_at: '',
    contract: { goal: '', scope: '', max_steps: 10 },
    ...overrides,
  }
}

describe('CaseCard — 行为契约', () => {
  it('点击卡片 body 触发 view', async () => {
    const wrapper = mount(CaseCard, {
      props: { caseData: makeCase(), disabled: false },
    })
    await wrapper.find('.case-card').trigger('click')
    expect(wrapper.emitted('view')).toBeTruthy()
    expect(wrapper.emitted('view')![0]).toEqual(['c1'])
  })

  it('点击 Run 按钮触发 run 且不冒泡到 view', async () => {
    const wrapper = mount(CaseCard, {
      props: { caseData: makeCase(), disabled: false },
    })
    await wrapper.find('.case-run-btn').trigger('click')
    expect(wrapper.emitted('run')).toBeTruthy()
    expect(wrapper.emitted('run')![0]).toEqual(['c1'])
    expect(wrapper.emitted('view')).toBeFalsy()
  })

  it('点击 tag pill 触发 toggle-tag 且不冒泡到 view', async () => {
    const wrapper = mount(CaseCard, {
      props: { caseData: makeCase(), disabled: false },
    })
    const tags = wrapper.findAll('.case-tag')
    await tags[0].trigger('click')
    expect(wrapper.emitted('toggle-tag')).toBeTruthy()
    // tags = ['code','backend']
    expect(wrapper.emitted('toggle-tag')![0]).toEqual(['code'])
    expect(wrapper.emitted('view')).toBeFalsy()
  })

  it('非内置 case 显示 edit / delete 按钮并触发对应事件', async () => {
    const wrapper = mount(CaseCard, {
      props: { caseData: makeCase({ is_builtin: false }), disabled: false },
    })
    const editBtn = wrapper.find('.case-action-btn.edit')
    const delBtn = wrapper.find('.case-action-btn.delete')
    expect(editBtn.exists()).toBe(true)
    expect(delBtn.exists()).toBe(true)

    await editBtn.trigger('click')
    expect(wrapper.emitted('edit')![0]).toEqual(['c1'])

    await delBtn.trigger('click')
    expect(wrapper.emitted('delete')![0]).toEqual(['c1'])
  })

  it('内置 case 隐藏 edit / delete 按钮并显示 builtin badge', () => {
    const wrapper = mount(CaseCard, {
      props: { caseData: makeCase({ is_builtin: true }), disabled: false },
    })
    expect(wrapper.find('.case-action-btn.edit').exists()).toBe(false)
    expect(wrapper.find('.case-action-btn.delete').exists()).toBe(false)
    expect(wrapper.find('.builtin-badge').exists()).toBe(true)
  })

  it('disabled=true 时 Run 按钮禁用且点击不 emit', async () => {
    const wrapper = mount(CaseCard, {
      props: { caseData: makeCase(), disabled: true },
    })
    const btn = wrapper.find('.case-run-btn')
    expect((btn.element as HTMLButtonElement).disabled).toBe(true)
    await btn.trigger('click')
    expect(wrapper.emitted('run')).toBeFalsy()
  })

  it('渲染 name / category / description / tags', () => {
    const wrapper = mount(CaseCard, {
      props: { caseData: makeCase({ name: 'My Case', category: 'rust' }), disabled: false },
    })
    expect(wrapper.find('.case-card-title h3').text()).toBe('My Case')
    expect(wrapper.find('.case-category').text()).toBe('rust')
    expect(wrapper.findAll('.case-tag')).toHaveLength(2)
  })
})
