/**
 * useCronEvents composable 单元测试
 *
 * 覆盖点：
 * - 注册一次 WS 监听器（多次调用 useCronEvents 复用同一 unsubscribe）
 * - onEvent 处理 cron_created/updated/enabled 等带 cron 体的事件 → upsertLocal
 * - cron_deleted → removeLocal
 * - cron_execution_* → 防抖刷新（不直接写缓存）
 * - 计数器 stats 按类型累加
 * - 事件历史有界（MAX_EVENTS）+ filter 倒序
 * - clear 清空历史与计数
 *
 * 通过 vi.mock('./useWebSocket') 注入可控的 onEvent，捕获回调后手动派发事件。
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import type { AgentEvent } from '@/types/events'
import type { Cron } from '@/types/cron'

// 捕获 useWebSocket().onEvent 注册的回调，供测试手动派发事件。
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

// useCrons 走真实实现（fetch 默认被 mock 成空 fn，refreshCrons 会失败但被 try/catch）。
// 我们主要验证 upsertLocal/removeLocal 的回写效果，不走网络。

async function freshMod() {
  vi.resetModules()
  capturedCallback = null
  unsubscribeSpy.mockClear()
  const mod = await import('./useCronEvents')
  const cronMod = await import('./useCrons')
  return { events: mod.useCronEvents(), cronStore: cronMod.useCrons() }
}
function makeEvent(type: AgentEvent['type'], data: Record<string, unknown> = {}, taskId = ''): AgentEvent {
  return {
    event_id: 'evt_' + Math.random().toString(36).slice(2),
    task_id: taskId,
    sub_task_id: '',
    agent_id: '',
    step_index: 0,
    type,
    timestamp: 0,
    data,
  }
}

const CRON_A: Cron = {
  id: 'cron_a', name: 'A', description: '', schedule_type: 'interval',
  cron_expr: '1h', display_type: 'interval', timezone: '', once_at: '',
  action_type: 'notify_session', action_payload: {}, status: 'enabled',
  allow_concurrent: false, source: 'user', owner: '', last_triggered_at: null,
  next_trigger_at: null, last_execution_id: '', trigger_count: 0,
  created_at: 0, updated_at: 0,
}

beforeEach(() => {
  vi.resetModules()
  vi.useFakeTimers()
  capturedCallback = null
  registerCount = 0
  unsubscribeSpy.mockClear()
  globalThis.fetch = vi.fn() as unknown as typeof globalThis.fetch
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('useCronEvents — 监听器注册', () => {
  it('首次调用注册 WS 监听器并拿到 unsubscribe', async () => {
    const { events } = await freshMod()
    expect(capturedCallback).toBeTruthy()
    expect(events.cronEvents.value).toHaveLength(0)
  })

  it('多次调用复用同一监听器（不重复注册）', async () => {
    // 注意：不能在两次调用之间 vi.resetModules，否则模块级 unsubscribe 会被重置。
    // 这里在同一模块实例下连续调用 useCronEvents 两次，验证不重复注册。
    vi.resetModules()
    capturedCallback = null
    registerCount = 0
    const mod = await import('./useCronEvents')
    mod.useCronEvents()
    const firstCount = registerCount
    mod.useCronEvents()
    expect(registerCount).toBe(firstCount)
  })
})

describe('useCronEvents — 事件回写 useCrons', () => {
  it('cron_created 带 cron 体时 upsert 到本地列表', async () => {
    const { cronStore } = await freshMod()
    expect(cronStore.crons.value).toHaveLength(0)
    capturedCallback!(makeEvent('cron_created', { cron: CRON_A }, 'cron_a'))
    expect(cronStore.crons.value).toHaveLength(1)
    expect(cronStore.crons.value[0].id).toBe('cron_a')
  })

  it('cron_updated 替换本地条目', async () => {
    const { cronStore } = await freshMod()
    cronStore.crons.value = [CRON_A]
    capturedCallback!(makeEvent('cron_updated', { cron: { ...CRON_A, name: 'A2' } }, 'cron_a'))
    expect(cronStore.crons.value[0].name).toBe('A2')
  })

  it('cron_enabled/disabled/paused/resumed 带 cron 体时 upsert', async () => {
    const { cronStore } = await freshMod()
    capturedCallback!(makeEvent('cron_paused', { cron: { ...CRON_A, status: 'paused' } }, 'cron_a'))
    expect(cronStore.crons.value[0].status).toBe('paused')
  })

  it('cron_deleted 从本地移除', async () => {
    const { cronStore } = await freshMod()
    cronStore.crons.value = [CRON_A]
    capturedCallback!(makeEvent('cron_deleted', { cron_id: 'cron_a' }, 'cron_a'))
    expect(cronStore.crons.value).toHaveLength(0)
  })

  it('cron_deleted 无 data.id 时回退到 task_id', async () => {
    const { cronStore } = await freshMod()
    cronStore.crons.value = [CRON_A]
    capturedCallback!(makeEvent('cron_deleted', {}, 'cron_a'))
    expect(cronStore.crons.value).toHaveLength(0)
  })
})

describe('useCronEvents — execution 事件防抖刷新', () => {
  it('cron_execution_completed 触发防抖 refreshCrons（不直接写缓存）', async () => {
    const fetchCalls: string[] = []
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      fetchCalls.push(String(input))
      return new Response(JSON.stringify([]), { status: 200 }) as unknown as Response
    }) as unknown as typeof globalThis.fetch

    const { cronStore } = await freshMod()
    capturedCallback!(makeEvent('cron_execution_completed', { execution_id: 'exec_1' }, 'cron_a'))
    // 尚未刷新（防抖未到）
    expect(fetchCalls).toHaveLength(0)
    // 推进定时器
    await vi.advanceTimersByTimeAsync(300)
    expect(fetchCalls.some(u => u.includes('/api/crons'))).toBe(true)
    // 不应直接写执行历史缓存
    expect(cronStore.executionsOf('cron_a')).toHaveLength(0)
  })

  it('连续多次 execution 事件只刷新一次（防抖合并）', async () => {
    const fetchCalls: string[] = []
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      fetchCalls.push(String(input))
      return new Response(JSON.stringify([]), { status: 200 }) as unknown as Response
    }) as unknown as typeof globalThis.fetch

    await freshMod()
    capturedCallback!(makeEvent('cron_execution_started', {}, 'cron_a'))
    capturedCallback!(makeEvent('cron_execution_completed', {}, 'cron_a'))
    capturedCallback!(makeEvent('cron_execution_failed', {}, 'cron_a'))
    await vi.advanceTimersByTimeAsync(300)
    // 只有一次 list 请求（refreshCrons 走 /api/crons）
    expect(fetchCalls.filter(u => u.includes('/api/crons')).length).toBe(1)
  })
})

describe('useCronEvents — 非 cron 事件忽略', () => {
  it('task_started 等非 cron 事件不计入历史与计数', async () => {
    const { events } = await freshMod()
    capturedCallback!(makeEvent('task_started', {}, 'task_x'))
    expect(events.cronEvents.value).toHaveLength(0)
    expect(events.stats.value.triggered).toBe(0)
  })
})

describe('useCronEvents — stats 计数', () => {
  it('按事件类型累加', async () => {
    const { events } = await freshMod()
    capturedCallback!(makeEvent('cron_created', { cron: CRON_A }, 'cron_a'))
    capturedCallback!(makeEvent('cron_triggered', {}, 'cron_a'))
    capturedCallback!(makeEvent('cron_execution_completed', {}, 'cron_a'))
    capturedCallback!(makeEvent('cron_execution_failed', {}, 'cron_a'))
    capturedCallback!(makeEvent('cron_execution_skipped', {}, 'cron_a'))
    capturedCallback!(makeEvent('cron_missed', {}, 'cron_a'))
    capturedCallback!(makeEvent('cron_notification', {}, 'cron_a'))
    capturedCallback!(makeEvent('cron_enabled', { cron: CRON_A }, 'cron_a'))
    expect(events.stats.value.created).toBe(1)
    expect(events.stats.value.triggered).toBe(1)
    expect(events.stats.value.executionCompleted).toBe(1)
    expect(events.stats.value.executionFailed).toBe(1)
    expect(events.stats.value.executionSkipped).toBe(1)
    expect(events.stats.value.missed).toBe(1)
    expect(events.stats.value.notifications).toBe(1)
    expect(events.stats.value.toggled).toBe(1)
  })
})

describe('useCronEvents — 历史有界 + filter + clear', () => {
  it('超过 MAX_EVENTS 时从头丢弃', async () => {
    const { events } = await freshMod()
    for (let i = 0; i < 60; i++) {
      capturedCallback!(makeEvent('cron_notification', { i }, 'cron_a'))
    }
    expect(events.cronEvents.value.length).toBeLessThanOrEqual(50)
  })

  it('filter 按类型过滤并倒序返回', async () => {
    const { events } = await freshMod()
    capturedCallback!(makeEvent('cron_notification', { tag: 1 }, 'cron_a'))
    capturedCallback!(makeEvent('cron_triggered', {}, 'cron_a'))
    capturedCallback!(makeEvent('cron_notification', { tag: 2 }, 'cron_a'))
    const notifs = events.filter('cron_notification')
    expect(notifs).toHaveLength(2)
    // 倒序：最新的在前
    expect(notifs[0].data.tag).toBe(2)
    expect(notifs[1].data.tag).toBe(1)
  })

  it('clear 清空历史与计数', async () => {
    const { events } = await freshMod()
    capturedCallback!(makeEvent('cron_created', { cron: CRON_A }, 'cron_a'))
    capturedCallback!(makeEvent('cron_triggered', {}, 'cron_a'))
    expect(events.cronEvents.value.length).toBeGreaterThan(0)
    events.clear()
    expect(events.cronEvents.value).toHaveLength(0)
    expect(events.stats.value.created).toBe(0)
    expect(events.stats.value.triggered).toBe(0)
  })
})
