import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SkillPicker from '../SkillPicker.vue'

describe('SkillPicker', () => {
  const commands = [
    { id: 'ops:new', name: 'New', description: 'Create', scope: 'project' as const, workspace_dir: '/p', project_id: '', source_path: '', skill_id: '', tags: [], icon: '' },
    { id: 'ops:fix', name: 'Fix', description: 'Repair', scope: 'project' as const, workspace_dir: '/p', project_id: '', source_path: '', skill_id: '', tags: [], icon: '' },
  ]

  it('renders command list', () => {
    const wrapper = mount(SkillPicker, {
      props: { open: true, commands, selectedIndex: 0 },
    })
    expect(wrapper.text()).toContain('ops:new')
    expect(wrapper.text()).toContain('ops:fix')
  })

  it('emits select on enter', async () => {
    const wrapper = mount(SkillPicker, {
      props: { open: true, commands, selectedIndex: 0 },
      attachTo: document.body,
    })
    const picker = wrapper.find('.skill-picker')
    expect(picker.exists()).toBe(true)
    await picker.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('select')).toHaveLength(1)
    expect(wrapper.emitted('select')![0]).toEqual([commands[0]])
  })

  it('emits close on esc', async () => {
    const wrapper = mount(SkillPicker, {
      props: { open: true, commands, selectedIndex: 0 },
      attachTo: document.body,
    })
    const picker = wrapper.find('.skill-picker')
    expect(picker.exists()).toBe(true)
    await picker.trigger('keydown', { key: 'Escape' })
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
})
