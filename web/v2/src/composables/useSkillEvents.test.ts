/**
 * useSkillEvents composable 单元测试
 *
 * 覆盖点：
 * - 注册一次 WS 监听器（多次调用 useSkillEvents 复用同一 unsubscribe）
 * - skill_enabled / skill_disabled 更新 enabledSkillIds
 * - skill_rendered 解析 skill_blocks 并按 task_id 保存
 * - 计数器 stats 按类型累加
 * - 历史有界（MAX_EVENTS）+ filter 倒序
 * - clear 清空历史与计数
 *
 * 通过 vi.mock('./useWebSocket') 注入可控的 onEvent，捕获回调后手动派发事件。
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import type { AgentEvent } from '@/types/events'

let capturedCallback: ((event: AgentEvent) => void) | null = null
let registerCount = 0
const unsubscribeSpy = vi.fn()

vi.mock('./useWebSocket', () => ({
  useWebSocket: () => ({
    onEvent: (cb: (event: AgentEvent) => void) => {
      capturedCallback = cb
      registerCount++
      return unsubscribeSpy
    },
  }),
}))

vi.mock('./useSkills', () => ({
  useSkills: () => ({
    skills: { value: [] },
    loadSkills: vi.fn(),
  }),
}))

async function freshMod() {
  vi.resetModules()
  capturedCallback = null
  unsubscribeSpy.mockClear()
  const mod = await import('./useSkillEvents')
  return mod.useSkillEvents()
}

function makeEvent(type: AgentEvent['type'], data: Record<string, unknown> = {}, taskId = ''): AgentEvent {
  return {
    event_id: 'evt_' + Math.random().toString(36).slice(2),
    task_id: taskId,
    sub_task_id: '',
    agent_id: 'agent_1',
    step_index: 0,
    type,
    timestamp: 0,
    data,
  }
}

beforeEach(() => {
  vi.resetModules()
  vi.useFakeTimers()
  capturedCallback = null
  registerCount = 0
  unsubscribeSpy.mockClear()
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('useSkillEvents — 监听器注册', () => {
  it('首次调用注册 WS 监听器', async () => {
    const events = await freshMod()
    expect(capturedCallback).toBeTruthy()
    expect(events.skillEvents.value).toHaveLength(0)
  })

  it('多次调用复用同一监听器（不重复注册）', async () => {
    vi.resetModules()
    capturedCallback = null
    registerCount = 0
    const mod = await import('./useSkillEvents')
    mod.useSkillEvents()
    const firstCount = registerCount
    mod.useSkillEvents()
    expect(registerCount).toBe(firstCount)
  })
})

describe('useSkillEvents — 状态更新', () => {
  it('skill_enabled 把 id 加入 enabledSkillIds', async () => {
    const events = await freshMod()
    capturedCallback!(makeEvent('skill_enabled', { id: 'skill_a' }, 'task_1'))
    expect(events.enabledSkillIds.value.has('skill_a')).toBe(true)
  })

  it('skill_disabled 从 enabledSkillIds 移除', async () => {
    const events = await freshMod()
    capturedCallback!(makeEvent('skill_enabled', { id: 'skill_a' }, 'task_1'))
    capturedCallback!(makeEvent('skill_disabled', { id: 'skill_a' }, 'task_1'))
    expect(events.enabledSkillIds.value.has('skill_a')).toBe(false)
  })

  it('skill_rendered 保存 skill_blocks 并按 task_id 索引', async () => {
    const events = await freshMod()
    const blocks = [
      { skill_id: 'skill_a', template_name: 'system_prompt', estimated_tokens: 42, char_count: 168 },
    ]
    capturedCallback!(makeEvent('skill_rendered', { skill_blocks: blocks }, 'task_1'))
    expect(events.lastRenderedTaskId.value).toBe('task_1')
    expect(events.skillBlocksByTask.value['task_1']).toEqual(blocks)
  })
})

describe('useSkillEvents — stats 计数', () => {
  it('按事件类型累加', async () => {
    const events = await freshMod()
    capturedCallback!(makeEvent('skill_enabled', { id: 'skill_a' }, 'task_1'))
    capturedCallback!(makeEvent('skill_disabled', { id: 'skill_a' }, 'task_1'))
    capturedCallback!(makeEvent('skill_loaded', { id: 'skill_a' }, 'task_1'))
    capturedCallback!(makeEvent('skill_unloaded', { id: 'skill_a' }, 'task_1'))
    capturedCallback!(makeEvent('skill_changed', { id: 'skill_a' }, 'task_1'))
    capturedCallback!(makeEvent('skill_rendered', { skill_blocks: [] }, 'task_1'))
    expect(events.stats.value.enabled).toBe(1)
    expect(events.stats.value.disabled).toBe(1)
    expect(events.stats.value.loaded).toBe(1)
    expect(events.stats.value.unloaded).toBe(1)
    expect(events.stats.value.changed).toBe(1)
    expect(events.stats.value.rendered).toBe(1)
  })
})

describe('useSkillEvents — 非 skill 事件忽略', () => {
  it('task_started 不计入历史与计数', async () => {
    const events = await freshMod()
    capturedCallback!(makeEvent('task_started', {}, 'task_1'))
    expect(events.skillEvents.value).toHaveLength(0)
    expect(events.stats.value.enabled).toBe(0)
  })
})

describe('useSkillEvents — 历史有界 + filter + clear', () => {
  it('超过 MAX_EVENTS 时从头丢弃', async () => {
    const events = await freshMod()
    for (let i = 0; i < 60; i++) {
      capturedCallback!(makeEvent('skill_enabled', { id: `skill_${i}` }, 'task_1'))
    }
    expect(events.skillEvents.value.length).toBeLessThanOrEqual(50)
  })

  it('filter 按类型过滤并倒序返回', async () => {
    const events = await freshMod()
    capturedCallback!(makeEvent('skill_enabled', { tag: 1 }, 'task_1'))
    capturedCallback!(makeEvent('skill_disabled', {}, 'task_1'))
    capturedCallback!(makeEvent('skill_enabled', { tag: 2 }, 'task_1'))
    const enabled = events.filter('skill_enabled')
    expect(enabled).toHaveLength(2)
    expect(enabled[0].data.tag).toBe(2)
    expect(enabled[1].data.tag).toBe(1)
  })

  it('clear 清空历史与计数', async () => {
    const events = await freshMod()
    capturedCallback!(makeEvent('skill_enabled', { id: 'skill_a' }, 'task_1'))
    capturedCallback!(makeEvent('skill_rendered', { skill_blocks: [] }, 'task_1'))
    expect(events.skillEvents.value.length).toBeGreaterThan(0)
    events.clear()
    expect(events.skillEvents.value).toHaveLength(0)
    expect(events.stats.value.enabled).toBe(0)
    expect(events.enabledSkillIds.value.size).toBe(0)
    expect(Object.keys(events.skillBlocksByTask.value)).toHaveLength(0)
  })
})
