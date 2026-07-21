/**
 * CronExecutions 组件测试
 *
 * 覆盖点：
 * - visible 打开时调用 loadExecutions（带 cronId + 默认 limit/offset）
 * - 渲染执行历史行（triggered_at / status / rendered_input / result）
 * - 状态过滤变化触发重新加载
 * - 清理按钮调用 cleanExecutions 并重新加载
 * - 空态展示
 *
 * 通过 mock useCrons 隔离网络。
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { ref } from 'vue'
import type { CronExecution } from '@/types/cron'

// 用 vi.hoisted 确保 mock 工厂能引用到（vi.mock 被提升到文件顶部）。
const { useCronsMock, getStore } = vi.hoisted(() => {
  const useCronsMock = vi.fn()
  let store: any = null
  return { useCronsMock, getStore: () => store }
})

vi.mock('@/composables/useCrons', () => ({ useCrons: useCronsMock }))

import CronExecutions from './CronExecutions.vue'

function makeExec(overrides: Partial<CronExecution> = {}): CronExecution {
  return {
    id: 'exec_1', cron_id: 'cron_a', triggered_at: 1700000000000,
    status: 'completed', reason: '', rendered_input: 'ping 1', result_summary: 'ok',
    task_id: '', session_id: '', duration_ms: 500, error: '', created_at: 1700000000000,
    ...overrides,
  }
}

function makeStore() {
  const execsByCron = ref<Record<string, CronExecution[]>>({})
  return {
    crons: ref([]),
    loading: ref(false),
    stats: ref({ enabled: 0, disabled: 0, paused: 0, total: 0 }),
    loadCrons: vi.fn(async () => {}),
    refreshCrons: vi.fn(async () => {}),
    getCron: vi.fn(async () => ({})),
    loadExecutions: vi.fn(async (filter: { cron_id?: string }) => {
      const key = filter?.cron_id ?? ''
      return execsByCron.value[key] || []
    }),
    executionsOf: vi.fn((cronId: string) => execsByCron.value[cronId] || []),
    createCron: vi.fn(async () => ({})),
    updateCron: vi.fn(async () => ({})),
    deleteCron: vi.fn(async () => {}),
    setStatus: vi.fn(async () => ({})),
    triggerCron: vi.fn(async () => ({})),
    cleanExecutions: vi.fn(async () => 2),
    upsertLocal: vi.fn(),
    removeLocal: vi.fn(),
    setExecutions: vi.fn((cronId: string, execs: CronExecution[]) => {
      execsByCron.value[cronId] = execs
    }),
    // 内部 ref 暴露，便于测试直接写入
    _execsByCron: execsByCron,
  }
}

async function mountPanel(props: { cronId?: string; visible?: boolean } = {}) {
  vi.resetModules()
  const store = makeStore()
  useCronsMock.mockReturnValue(store as never)
  const wrapper = mount(CronExecutions, {
    props: { cronId: props.cronId ?? '', visible: props.visible ?? true },
  })
  await flushPromises()
  return { wrapper, store }
}

beforeEach(() => {
  vi.resetModules()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('CronExecutions — 加载', () => {
  it('visible 打开时调用 loadExecutions 带 cronId + 默认 limit', async () => {
    const { store } = await mountPanel({ cronId: 'cron_a' })
    expect(store.loadExecutions).toHaveBeenCalledWith(
      expect.objectContaining({ cron_id: 'cron_a', limit: 20, offset: 0 }),
    )
  })

  it('visible=false 时不加载', async () => {
    const { store } = await mountPanel({ cronId: 'cron_a', visible: false })
    expect(store.loadExecutions).not.toHaveBeenCalled()
  })
})

describe('CronExecutions — 渲染', () => {
  it('渲染执行历史行', async () => {
    const { wrapper, store } = await mountPanel({ cronId: 'cron_a' })
    store._execsByCron.value['cron_a'] = [makeExec(), makeExec({ id: 'exec_2', status: 'failed', error: 'boom' })]
    store.executionsOf = vi.fn(() => store._execsByCron.value['cron_a'])
    await flushPromises()
    await wrapper.vm.$nextTick()
    const rows = wrapper.findAll('.exec-row').filter(r => !r.classes('exec-row--head'))
    expect(rows.length).toBe(2)
    expect(wrapper.text()).toContain('ping 1')
    expect(wrapper.text()).toContain('boom')
  })

  it('空态展示', async () => {
    const { wrapper } = await mountPanel({ cronId: 'cron_a' })
    await flushPromises()
    expect(wrapper.find('.exec-empty').exists()).toBe(true)
  })
})

describe('CronExecutions — 过滤与清理', () => {
  it('状态过滤变化触发重新加载', async () => {
    const { wrapper, store } = await mountPanel({ cronId: 'cron_a' })
    store.loadExecutions.mockClear()
    await wrapper.find('.exec-select').setValue('failed')
    await flushPromises()
    expect(store.loadExecutions).toHaveBeenCalledWith(
      expect.objectContaining({ status: 'failed' }),
    )
  })

  it('清理按钮调用 cleanExecutions 并重新加载', async () => {
    const { wrapper, store } = await mountPanel({ cronId: 'cron_a' })
    store.loadExecutions.mockClear()
    await wrapper.find('.exec-clean-btn').trigger('click')
    await flushPromises()
    expect(store.cleanExecutions).toHaveBeenCalledWith(
      expect.objectContaining({ cron_id: 'cron_a' }),
    )
    expect(store.loadExecutions).toHaveBeenCalled()
  })
})
