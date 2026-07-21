/**
 * useCrons composable 单元测试
 *
 * 覆盖点：
 * - loadCrons: GET /api/crons → crons ref + loading 状态
 * - createCron: POST /api/crons → 乐观插入本地列表头部
 * - updateCron: PUT /api/crons/:id → 替换本地条目
 * - deleteCron: DELETE /api/crons/:id → 移除本地条目 + 清执行历史缓存
 * - setStatus: POST /api/crons/:id/{enable|disable|pause|resume} → 替换本地条目
 * - triggerCron: POST /api/crons/:id/trigger → 返回 execution
 * - loadExecutions: GET /api/crons/:id/executions 与 GET /api/crons/executions
 * - cleanExecutions: DELETE /api/crons/executions → 返回 deleted 数 + 清缓存
 * - URL 编码 / 过滤参数拼接
 * - upsertLocal / removeLocal / setExecutions 事件回写辅助
 *
 * 模块级单例，每个 test 用 vi.resetModules + 动态 import 拿全新实例。
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import type { Cron, CronExecution } from '@/types/cron'

async function freshStore() {
  vi.resetModules()
  const mod = await import('./useCrons')
  return mod.useCrons()
}

const CRON_A: Cron = {
  id: 'cron_a', name: 'A', description: '', schedule_type: 'interval',
  cron_expr: '1h', display_type: 'interval', timezone: '', once_at: '',
  action_type: 'notify_session', action_payload: {}, status: 'enabled',
  allow_concurrent: false, source: 'user', owner: '', last_triggered_at: null,
  next_trigger_at: null, last_execution_id: '', trigger_count: 0,
  created_at: 0, updated_at: 0,
}
const CRON_B: Cron = { ...CRON_A, id: 'cron_b', name: 'B', status: 'paused' }

const EXEC_1: CronExecution = {
  id: 'exec_1', cron_id: 'cron_a', triggered_at: 100, status: 'completed',
  reason: '', rendered_input: '', result_summary: 'ok', task_id: '', session_id: '',
  duration_ms: 50, error: '', created_at: 100,
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }) as unknown as Response
}

/** 记录 fetch 调用的 url + init，按顺序返回预设响应。 */
function mockFetchSequence(responses: Array<(url: string, init?: RequestInit) => Response>) {
  const calls: Array<{ url: string; init?: RequestInit }> = []
  let idx = 0
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    calls.push({ url, init })
    const responder = responses[Math.min(idx, responses.length - 1)]
    idx++
    return responder(url, init)
  }) as unknown as typeof globalThis.fetch
  return calls
}

beforeEach(() => {
  vi.resetModules()
  globalThis.fetch = vi.fn() as unknown as typeof globalThis.fetch
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('useCrons — loadCrons', () => {
  it('GET /api/crons 成功后填充 crons', async () => {
    mockFetchSequence([() => jsonResponse([CRON_A, CRON_B])])
    const { crons, loadCrons, loading } = await freshStore()
    await loadCrons()
    expect(crons.value).toHaveLength(2)
    expect(loading.value).toBe(false)
  })

  it('带过滤参数拼接 query string', async () => {
    const calls = mockFetchSequence([() => jsonResponse([])])
    const { loadCrons } = await freshStore()
    await loadCrons({ status: 'enabled', action_type: 'notify_session', q: 'foo' })
    expect(calls[0].url).toContain('status=enabled')
    expect(calls[0].url).toContain('action_type=notify_session')
    expect(calls[0].url).toContain('q=foo')
  })

  it('请求失败时抛错', async () => {
    mockFetchSequence([() => jsonResponse({ error: 'x' }, 500)])
    const { loadCrons } = await freshStore()
    await expect(loadCrons()).rejects.toThrow(/500/)
  })
})

describe('useCrons — createCron', () => {
  it('POST /api/crons 成功后乐观插入列表头部', async () => {
    const calls = mockFetchSequence([
      (_u, init) => init?.method === 'POST' ? jsonResponse(CRON_A) : jsonResponse([CRON_B]),
    ])
    const { createCron, crons } = await freshStore()
    const created = await createCron({
      name: 'A', schedule_type: 'interval', cron_expr: '1h',
      action_type: 'notify_session', action_payload: {},
    })
    expect(created.id).toBe('cron_a')
    expect(crons.value[0].id).toBe('cron_a')
    expect(calls[0].init?.method).toBe('POST')
    expect(calls[0].url).toBe('/api/crons')
  })

  it('非 2xx 抛错', async () => {
    mockFetchSequence([() => jsonResponse('bad', 400)])
    const { createCron } = await freshStore()
    await expect(createCron({
      name: 'A', schedule_type: 'interval', action_type: 'notify_session', action_payload: {},
    })).rejects.toThrow(/400/)
  })
})

describe('useCrons — updateCron', () => {
  it('PUT /api/crons/:id 成功后替换本地条目', async () => {
    const calls = mockFetchSequence([
      (_u, init) => init?.method === 'PUT' ? jsonResponse({ ...CRON_A, name: 'A2' }) : jsonResponse([]),
    ])
    const { updateCron, crons } = await freshStore()
    // 预置一条旧数据
    crons.value = [CRON_A]
    const updated = await updateCron('cron_a', { name: 'A2' })
    expect(updated.name).toBe('A2')
    expect(crons.value[0].name).toBe('A2')
    expect(calls[0].url).toBe('/api/crons/cron_a')
    expect(calls[0].init?.method).toBe('PUT')
  })

  it('对 id 做 URL 编码', async () => {
    const calls = mockFetchSequence([() => jsonResponse(CRON_A)])
    const { updateCron } = await freshStore()
    await updateCron('a b/c', { name: 'x' })
    expect(calls[0].url).toBe('/api/crons/a%20b%2Fc')
  })
})

describe('useCrons — deleteCron', () => {
  it('DELETE /api/crons/:id 成功后移除本地条目', async () => {
    const calls = mockFetchSequence([
      (_u, init) => init?.method === 'DELETE' ? jsonResponse({ deleted: 'cron_a' }) : jsonResponse([]),
    ])
    const { deleteCron, crons } = await freshStore()
    crons.value = [CRON_A, CRON_B]
    await deleteCron('cron_a')
    expect(crons.value).toHaveLength(1)
    expect(crons.value[0].id).toBe('cron_b')
    expect(calls[0].init?.method).toBe('DELETE')
    expect(calls[0].url).toBe('/api/crons/cron_a')
  })
})

describe('useCrons — setStatus', () => {
  it('disable 映射到 POST /api/crons/:id/disable', async () => {
    const calls = mockFetchSequence([
      () => jsonResponse({ ...CRON_A, status: 'disabled' }),
    ])
    const { setStatus, crons } = await freshStore()
    crons.value = [CRON_A]
    await setStatus('cron_a', 'disabled')
    expect(crons.value[0].status).toBe('disabled')
    expect(calls[0].url).toBe('/api/crons/cron_a/disable')
    expect(calls[0].init?.method).toBe('POST')
  })

  it('paused 映射到 /pause，enabled 映射到 /enable', async () => {
    const calls = mockFetchSequence([
      () => jsonResponse({ ...CRON_A, status: 'paused' }),
      () => jsonResponse({ ...CRON_A, status: 'enabled' }),
    ])
    const { setStatus } = await freshStore()
    await setStatus('cron_a', 'paused')
    expect(calls[0].url).toBe('/api/crons/cron_a/pause')
    await setStatus('cron_a', 'enabled')
    expect(calls[1].url).toBe('/api/crons/cron_a/enable')
  })
})

describe('useCrons — triggerCron', () => {
  it('POST /api/crons/:id/trigger 返回 execution', async () => {
    const calls = mockFetchSequence([() => jsonResponse(EXEC_1)])
    const { triggerCron } = await freshStore()
    const exec = await triggerCron('cron_a')
    expect(exec.id).toBe('exec_1')
    expect(calls[0].url).toBe('/api/crons/cron_a/trigger')
    expect(calls[0].init?.method).toBe('POST')
    const body = JSON.parse(calls[0].init?.body as string)
    expect(body.override_input).toBe('')
  })

  it('带 override_input 时写入请求体', async () => {
    const calls = mockFetchSequence([() => jsonResponse(EXEC_1)])
    const { triggerCron } = await freshStore()
    await triggerCron('cron_a', 'custom input')
    const body = JSON.parse(calls[0].init?.body as string)
    expect(body.override_input).toBe('custom input')
  })
})

describe('useCrons — loadExecutions', () => {
  it('带 cron_id 时走 /api/crons/:id/executions', async () => {
    const calls = mockFetchSequence([() => jsonResponse([EXEC_1])])
    const { loadExecutions, executionsOf } = await freshStore()
    await loadExecutions({ cron_id: 'cron_a' })
    expect(calls[0].url).toContain('/api/crons/cron_a/executions')
    expect(executionsOf('cron_a')).toHaveLength(1)
  })

  it('不带 cron_id 时走全局 /api/crons/executions', async () => {
    const calls = mockFetchSequence([() => jsonResponse([EXEC_1])])
    const { loadExecutions } = await freshStore()
    await loadExecutions({ limit: 50, offset: 10 })
    expect(calls[0].url).toContain('/api/crons/executions')
    expect(calls[0].url).toContain('limit=50')
    expect(calls[0].url).toContain('offset=10')
  })
})

describe('useCrons — cleanExecutions', () => {
  it('DELETE /api/crons/executions 返回 deleted 数', async () => {
    const calls = mockFetchSequence([() => jsonResponse({ deleted: 3 })])
    const { cleanExecutions } = await freshStore()
    const n = await cleanExecutions({ cron_id: 'cron_a' })
    expect(n).toBe(3)
    expect(calls[0].init?.method).toBe('DELETE')
    expect(calls[0].url).toContain('cron_id=cron_a')
  })

  it('不带 cron_id 时清空全部缓存', async () => {
    mockFetchSequence([() => jsonResponse({ deleted: 5 })])
    const { cleanExecutions, setExecutions, executionsOf } = await freshStore()
    setExecutions('cron_a', [EXEC_1])
    setExecutions('cron_b', [EXEC_1])
    await cleanExecutions({})
    expect(executionsOf('cron_a')).toHaveLength(0)
    expect(executionsOf('cron_b')).toHaveLength(0)
  })
})

describe('useCrons — 事件回写辅助', () => {
  it('upsertLocal 命中已有则替换，未命中则前置', async () => {
    const { upsertLocal, crons } = await freshStore()
    crons.value = [CRON_A]
    upsertLocal({ ...CRON_A, name: 'A2' })
    expect(crons.value).toHaveLength(1)
    expect(crons.value[0].name).toBe('A2')
    upsertLocal(CRON_B)
    expect(crons.value).toHaveLength(2)
    expect(crons.value[0].id).toBe('cron_b') // 前置
  })

  it('removeLocal 按 id 移除', async () => {
    const { removeLocal, crons } = await freshStore()
    crons.value = [CRON_A, CRON_B]
    removeLocal('cron_a')
    expect(crons.value).toHaveLength(1)
    expect(crons.value[0].id).toBe('cron_b')
  })

  it('setExecutions / executionsOf 读写缓存', async () => {
    const { setExecutions, executionsOf } = await freshStore()
    expect(executionsOf('cron_a')).toHaveLength(0)
    setExecutions('cron_a', [EXEC_1])
    expect(executionsOf('cron_a')).toHaveLength(1)
  })
})

describe('useCrons — stats', () => {
  it('按 status 分桶统计', async () => {
    const { stats, crons } = await freshStore()
    crons.value = [CRON_A, CRON_B, { ...CRON_A, id: 'c', status: 'disabled' }]
    expect(stats.value).toEqual({ enabled: 1, disabled: 1, paused: 1, total: 3 })
  })
})
