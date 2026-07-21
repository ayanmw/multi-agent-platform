/**
 * useContextWindow 回归测试
 *
 * 锁死的契约（修复 "Waiting for the next agent think step..." 再次回归）：
 * 1. fetchSnapshot(taskId)（无 subTaskId）走 root 视图：命中
 *    GET /api/tasks/:id/context_window，成功后写入 currentSnapshot。
 * 2. fetchSnapshot(taskId, subTaskId) 走子 agent 视图：命中带 query 的同
 *    端点，成功后写入 subTaskSnapshots[subTaskId]。
 * 3. setActiveTaskId 切换 task 时清空旧 currentSnapshot，防跨任务污染。
 * 4. setSnapshot 仅在 activeTaskId 匹配时回填，切走任务后 in-flight 响应
 *    不会污染新视图。
 * 5. 404 / 非 2xx / 网络异常都不抛错，UI 保留空态而非崩溃。
 *
 * 关键回归点：历史/idle task 没有 WS context_window_snapshot 事件，上下文
 * 窗口只能靠 REST 重建。组件必须在挂载/切换任务时主动 fetchSnapshot，
 * 而不是只依赖 WS 事件 —— 否则永远停在空态。
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import type { ContextWindowSnapshotData } from '@/types/events'

async function fresh() {
  vi.resetModules()
  const mod = await import('./useContextWindow')
  return mod.useContextWindow()
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }) as unknown as Response
}

const ROOT_SNAPSHOT: ContextWindowSnapshotData = {
  model: 'deepseek-v4-flash',
  max_context_tokens: 200000,
  estimated_total_tokens: 1234,
  estimated_usage_ratio: 0.00617,
  messages: [
    { role: 'system', content: 'sys', estimated_tokens: 10, usage_ratio: 0.5 },
    { role: 'user', content: 'hi', estimated_tokens: 2, usage_ratio: 0.5 },
  ],
}

const SUB_SNAPSHOT: ContextWindowSnapshotData = {
  model: 'deepseek-v4-flash',
  max_context_tokens: 200000,
  estimated_total_tokens: 999,
  estimated_usage_ratio: 0.005,
  messages: [
    { role: 'system', content: 'sub-sys', estimated_tokens: 5, usage_ratio: 1 },
  ],
}

function mockFetchOnce(responder: (url: string) => Response) {
  const calls: string[] = []
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    calls.push(url)
    return responder(url)
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

describe('useContextWindow — fetchSnapshot (root 视图)', () => {
  it('成功后写入 currentSnapshot，请求 URL 不带 sub_task_id', async () => {
    const calls = mockFetchOnce(() => jsonResponse(ROOT_SNAPSHOT))
    const { fetchSnapshot, currentSnapshot, setActiveTaskId } = await fresh()
    setActiveTaskId('task-1')

    await fetchSnapshot('task-1')

    expect(calls).toEqual(['/api/tasks/task-1/context_window'])
    expect(currentSnapshot.value).toEqual(ROOT_SNAPSHOT)
  })

  it('404 时不抛错、不写入快照（历史 task 无可重建消息属正常空态）', async () => {
    mockFetchOnce(() => jsonResponse({ error: 'not found' }, 404))
    const { fetchSnapshot, currentSnapshot, setActiveTaskId } = await fresh()
    setActiveTaskId('task-2')

    await expect(fetchSnapshot('task-2')).resolves.toBeUndefined()
    expect(currentSnapshot.value).toBeNull()
  })

  it('500 时不抛错、不写入快照', async () => {
    mockFetchOnce(() => jsonResponse({ error: 'boom' }, 500))
    const { fetchSnapshot, currentSnapshot, setActiveTaskId } = await fresh()
    setActiveTaskId('task-3')

    await expect(fetchSnapshot('task-3')).resolves.toBeUndefined()
    expect(currentSnapshot.value).toBeNull()
  })

  it('网络异常时不抛错、不写入快照', async () => {
    globalThis.fetch = vi.fn(async () => {
      throw new Error('network down')
    }) as unknown as typeof globalThis.fetch
    const { fetchSnapshot, currentSnapshot, setActiveTaskId } = await fresh()
    setActiveTaskId('task-4')

    await expect(fetchSnapshot('task-4')).resolves.toBeUndefined()
    expect(currentSnapshot.value).toBeNull()
  })

  it('taskId 为空时直接返回，不发请求', async () => {
    const calls = mockFetchOnce(() => jsonResponse(ROOT_SNAPSHOT))
    const { fetchSnapshot } = await fresh()
    await fetchSnapshot('')
    expect(calls).toHaveLength(0)
  })
})

describe('useContextWindow — fetchSnapshot (子 agent 视图)', () => {
  it('带 subTaskId 时 URL 带 query，写入 subTaskSnapshots 而非 currentSnapshot', async () => {
    const calls = mockFetchOnce(() => jsonResponse(SUB_SNAPSHOT))
    const { fetchSnapshot, currentSnapshot, subTaskSnapshots, setActiveTaskId } = await fresh()
    setActiveTaskId('task-1')

    await fetchSnapshot('task-1', 'task-1_agent-7')

    expect(calls).toEqual([
      '/api/tasks/task-1/context_window?sub_task_id=task-1_agent-7',
    ])
    expect(subTaskSnapshots.value['task-1_agent-7']).toEqual(SUB_SNAPSHOT)
    // root 视图不应被子 agent 快照污染
    expect(currentSnapshot.value).toBeNull()
  })

  it('subTaskId 会被 URL 编码', async () => {
    const calls = mockFetchOnce(() => jsonResponse(SUB_SNAPSHOT))
    const { fetchSnapshot, setActiveTaskId } = await fresh()
    setActiveTaskId('task-1')
    await fetchSnapshot('task-1', 'a b/c')
    expect(calls[0]).toContain('sub_task_id=a%20b%2Fc')
  })
})

describe('useContextWindow — 跨任务隔离', () => {
  it('setActiveTaskId 切换 task 时清空 currentSnapshot', async () => {
    const { currentSnapshot, setActiveTaskId } = await fresh()
    setActiveTaskId('task-1')
    // 模拟 WS 事件已写入
    currentSnapshot.value = ROOT_SNAPSHOT
    setActiveTaskId('task-2')
    expect(currentSnapshot.value).toBeNull()
  })

  it('setSnapshot 仅在 activeTaskId 匹配时回填（防 in-flight 污染）', async () => {
    const { setSnapshot, currentSnapshot, setActiveTaskId } = await fresh()
    setActiveTaskId('task-1')
    setActiveTaskId('task-2') // 用户在请求 in-flight 时切走

    setSnapshot('task-1', ROOT_SNAPSHOT) // 旧请求回来，不该写入新视图
    expect(currentSnapshot.value).toBeNull()

    setSnapshot('task-2', ROOT_SNAPSHOT) // 当前 active，应写入
    expect(currentSnapshot.value).toEqual(ROOT_SNAPSHOT)
  })

  it('clear 清空 currentSnapshot', async () => {
    const { currentSnapshot, clear, setActiveTaskId } = await fresh()
    setActiveTaskId('task-1')
    currentSnapshot.value = ROOT_SNAPSHOT
    clear()
    expect(currentSnapshot.value).toBeNull()
  })
})
