/**
 * CronForm 组件测试
 *
 * 注意：CronForm 内部用 <Teleport to="body"> 渲染 Modal，teleported 内容
 * 不在 wrapper 根子树内，wrapper.find 找不到。这里通过 document.body 查询
 * 并用原生 dispatchEvent 触发 input/change，配合 v-model。
 *
 * 覆盖点：
 * - 新建模式：visible 打开时字段为默认值（preset/1m/notify_session）
 * - 编辑模式：从 cronData 还原字段
 * - 切换 displayType：preset → interval → cron → once
 * - preset 下拉切换同步 cronExpr
 * - 校验：缺 name 报错；notify_session 缺 session_id 报错
 * - save（新建）emit CreateCronInput；save（编辑）emit UpdateCronInput
 * - close emit close
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import type { Cron } from '@/types/cron'

vi.mock('@/composables/useAgentStore', () => ({
  useAgentStore: () => ({
    agents: { value: [{ id: 'agent_a', name: 'Agent A' }] },
    availableTools: { value: [{ name: 'run_shell' }, { name: 'read_file' }] },
    loadAgents: vi.fn(async () => {}),
    loadAvailableTools: vi.fn(async () => {}),
  }),
}))

import CronForm from './CronForm.vue'

function makeCron(overrides: Partial<Cron> = {}): Cron {
  return {
    id: 'cron_a', name: 'A', description: 'd', schedule_type: 'interval',
    cron_expr: '5m', display_type: 'interval', timezone: '', once_at: '',
    action_type: 'notify_session', action_payload: { session_id: 'sess1', message: 'hi' },
    status: 'enabled', allow_concurrent: false, source: 'user', owner: '',
    last_triggered_at: null, next_trigger_at: null, last_execution_id: '',
    trigger_count: 0, created_at: 0, updated_at: 0,
    ...overrides,
  }
}

/** Teleport 渲染到 document.body，故从 body 查询。 */
function q(selector: string): HTMLElement {
  return document.body.querySelector(selector) as HTMLElement
}
function val(selector: string): string {
  return (q(selector) as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement).value
}

/** 模拟用户输入：设置 value + 触发 input 事件（v-model 监听 input）。 */
function setInput(selector: string, value: string) {
  const el = q(selector) as HTMLInputElement
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
}
function setSelect(selector: string, value: string) {
  const el = q(selector) as HTMLSelectElement
  el.value = value
  el.dispatchEvent(new Event('change', { bubbles: true }))
}
function click(selector: string) {
  q(selector).dispatchEvent(new MouseEvent('click', { bubbles: true }))
}

async function openForm(cronData: Cron | null = null) {
  document.body.innerHTML = ''
  const wrapper = mount(CronForm, { props: { cronData, visible: false }, attachTo: document.body })
  await wrapper.setProps({ visible: true })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.resetModules()
  document.body.innerHTML = ''
})

afterEach(() => {
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

describe('CronForm — 新建模式默认值', () => {
  it('打开后字段为默认值', async () => {
    await openForm(null)
    expect(val('#cron-name')).toBe('')
    // 默认 preset 段是激活的
    const activeSeg = document.body.querySelector('.seg-btn.active') as HTMLElement
    expect(activeSeg.textContent).toBe('预设')
    expect(val('#cron-action')).toBe('notify_session')
  })
})

describe('CronForm — 编辑模式还原', () => {
  it('从 cronData 还原 name / schedule / action_payload', async () => {
    await openForm(makeCron())
    expect(val('#cron-name')).toBe('A')
    // interval 模式应展示 interval 输入框
    expect(q('#cron-interval')).toBeTruthy()
    expect(val('#cron-interval')).toBe('5m')
    expect(val('#ns-session')).toBe('sess1')
    expect(val('#ns-message')).toBe('hi')
  })
})

describe('CronForm — 调度类型切换', () => {
  it('切到 interval 显示 interval 输入', async () => {
    await openForm(null)
    const segs = Array.from(document.body.querySelectorAll('.seg-btn')) as HTMLElement[]
    const intervalBtn = segs.find(b => b.textContent === '间隔')!
    intervalBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    expect(q('#cron-interval')).toBeTruthy()
  })

  it('切到 cron 显示 cron 表达式输入', async () => {
    await openForm(null)
    const cronBtn = Array.from(document.body.querySelectorAll('.seg-btn')).find(b => b.textContent === 'Cron') as HTMLElement
    cronBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    expect(q('#cron-expr')).toBeTruthy()
  })

  it('切到 once 显示 datetime-local', async () => {
    await openForm(null)
    const onceBtn = Array.from(document.body.querySelectorAll('.seg-btn')).find(b => b.textContent === '一次') as HTMLElement
    onceBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    expect(q('#cron-once')).toBeTruthy()
  })

  it('preset 下拉切换同步 cronExpr', async () => {
    await openForm(null)
    expect(val('#cron-preset')).toBe('1m')
    setSelect('#cron-preset', 'daily')
    await flushPromises()
    // 切到 cron 段确认 expr 被同步成 daily 的表达式
    const cronBtn = Array.from(document.body.querySelectorAll('.seg-btn')).find(b => b.textContent === 'Cron') as HTMLElement
    cronBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    expect(val('#cron-expr')).toBe('0 0 0 * * *')
  })
})

describe('CronForm — 校验', () => {
  it('缺 name 时报错且不 emit save', async () => {
    const wrapper = await openForm(null)
    click('.modal-save-btn')
    await flushPromises()
    expect(q('.form-error').textContent).toContain('名称不能为空')
    expect(wrapper.emitted('save')).toBeFalsy()
  })

  it('notify_session 缺 session_id 报错', async () => {
    const wrapper = await openForm(null)
    setInput('#cron-name', 'Test')
    click('.modal-save-btn')
    await flushPromises()
    expect(q('.form-error').textContent).toContain('session_id')
    expect(wrapper.emitted('save')).toBeFalsy()
  })
})

describe('CronForm — 提交', () => {
  it('新建合法时 emit CreateCronInput', async () => {
    const wrapper = await openForm(null)
    setInput('#cron-name', 'My Cron')
    setInput('#ns-session', 'sess_x')
    click('.modal-save-btn')
    await flushPromises()
    const saveEvents = wrapper.emitted('save')
    expect(saveEvents).toBeTruthy()
    const req = saveEvents![0][0] as Record<string, unknown>
    expect(req.name).toBe('My Cron')
    expect(req.schedule_type).toBe('interval')
    expect(req.action_type).toBe('notify_session')
    expect(req.source).toBe('user')
  })

  it('编辑合法时 emit UpdateCronInput（不含 source）', async () => {
    const wrapper = await openForm(makeCron())
    setInput('#cron-name', 'Renamed')
    click('.modal-save-btn')
    await flushPromises()
    const req = wrapper.emitted('save')![0][0] as Record<string, unknown>
    expect(req.name).toBe('Renamed')
    expect(req).not.toHaveProperty('source')
  })

  it('切到 cron 并提交时 schedule_type=cron', async () => {
    const wrapper = await openForm(null)
    setInput('#cron-name', 'Cron Expr')
    const cronBtn = Array.from(document.body.querySelectorAll('.seg-btn')).find(b => b.textContent === 'Cron') as HTMLElement
    cronBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    setInput('#cron-expr', '0 */30 * * * *')
    setInput('#ns-session', 's1')
    click('.modal-save-btn')
    await flushPromises()
    const req = wrapper.emitted('save')![0][0] as Record<string, unknown>
    expect(req.schedule_type).toBe('cron')
    expect(req.cron_expr).toBe('0 */30 * * * *')
  })
})

describe('CronForm — 关闭', () => {
  it('点取消 emit close', async () => {
    const wrapper = await openForm(null)
    click('.modal-cancel-btn')
    await flushPromises()
    expect(wrapper.emitted('close')).toBeTruthy()
  })
})
