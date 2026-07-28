import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SkillPicker from '../SkillPicker.vue'

const commands = [
  { id: 'ops:new', name: 'New', description: 'Create', scope: 'project' as const, workspace_dir: '/p', project_id: '', source_path: '', skill_id: '', tags: [], icon: '' },
  { id: 'ops:fix', name: 'Fix', description: 'Repair', scope: 'project' as const, workspace_dir: '/p', project_id: '', source_path: '', skill_id: '', tags: [], icon: '' },
]

const skills = [
  {
    id: 'local-skill',
    display_name: 'Local Skill',
    description: 'local db skill',
    source: 'local_db',
    state: 'enabled',
  } as any,
  {
    id: 'builtin-skill',
    display_name: 'Built-in Skill',
    description: 'builtin skill',
    source: 'built_in',
    state: 'disabled',
  } as any,
]

describe('SkillPicker', () => {
  it('renders command and skill groups', () => {
    const wrapper = mount(SkillPicker, {
      props: { open: true, commands, skills, selectedIndex: 0 },
    })
    expect(wrapper.text()).toContain('ops:new')
    expect(wrapper.text()).toContain('local-skill')
    expect(wrapper.text()).toContain('Built-in')
  })

  it('emits select on enter for command', async () => {
    const wrapper = mount(SkillPicker, {
      props: { open: true, commands, skills, selectedIndex: 0 },
      attachTo: document.body,
    })
    const picker = wrapper.find('.skill-picker')
    expect(picker.exists()).toBe(true)
    await picker.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('select')).toHaveLength(1)
    expect((wrapper.emitted('select')![0] as any)[0]).toMatchObject({ kind: 'command', id: 'ops:new' })
  })

  it('emits select on click for skill', async () => {
    const wrapper = mount(SkillPicker, {
      props: { open: true, commands, skills, selectedIndex: 0 },
      attachTo: document.body,
    })
    const items = wrapper.findAll('.skill-picker-item')
    // 0,1 Commands; 2 local skill; 3 built-in skill
    const localSkillItem = items.find(i => i.text().includes('local-skill'))
    expect(localSkillItem).toBeDefined()
    await localSkillItem!.trigger('click')
    expect(wrapper.emitted('select')).toHaveLength(1)
    expect((wrapper.emitted('select')![0] as any)[0]).toMatchObject({ kind: 'skill', id: 'local-skill' })
  })

  it('emits close on esc', async () => {
    const wrapper = mount(SkillPicker, {
      props: { open: true, commands, skills: [], selectedIndex: 0 },
      attachTo: document.body,
    })
    const picker = wrapper.find('.skill-picker')
    expect(picker.exists()).toBe(true)
    await picker.trigger('keydown', { key: 'Escape' })
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
})
