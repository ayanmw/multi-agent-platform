/**
 * useCaseStore composable 单元测试
 *
 * 覆盖点：
 * - loadCases: GET /api/cases → cases ref
 * - createCase: POST /api/cases → 触发 reload
 * - updateCase: PUT /api/cases/:id → 触发 reload
 * - deleteCase: DELETE /api/cases/:id → 触发 reload
 * - filteredCases: tags OR + category 精确匹配
 * - toggleTag / setCategory / clearFilters
 * - 对 id 做 URL 编码
 *
 * 模块级单例，每个 test 用 vi.resetModules + 动态 import 拿全新实例。
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import type { Case } from '@/types/case'

async function freshStore() {
  vi.resetModules()
  const mod = await import('./useCaseStore')
  return mod.useCaseStore()
}

const CASE_A: Case = {
  id: 'a', name: 'Case A', description: 'desc a', icon: '🚀', category: 'go',
  system_prompt: '', default_input: '', tags: ['code', 'backend'],
  is_builtin: true, created_at: '', updated_at: '',
  contract: { goal: '', max_steps: 10 },
}
const CASE_B: Case = {
  id: 'b', name: 'Case B', description: 'desc b', icon: '🐛', category: 'rust',
  system_prompt: '', default_input: '', tags: ['debug'],
  is_builtin: false, created_at: '', updated_at: '',
  contract: { goal: '', max_steps: 5 },
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }) as unknown as Response
}

/** 记录 fetch 调用的 method + url，便于断言 reload 与 URL 编码 */
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

describe('useCaseStore — loadCases', () => {
  it('GET /api/cases 成功后填充 cases', async () => {
    mockFetchSequence([() => jsonResponse([CASE_A, CASE_B])])
    const { cases, loadCases, loading } = await freshStore()
    await loadCases()
    expect(cases.value).toHaveLength(2)
    expect(loading.value).toBe(false)
  })

  it('请求失败时抛错并设置 error', async () => {
    mockFetchSequence([() => jsonResponse({ error: 'x' }, 500)])
    const { loadCases, error } = await freshStore()
    await expect(loadCases()).rejects.toThrow(/500/)
    expect(error.value).toBeTruthy()
  })
})

describe('useCaseStore — CRUD 触发 reload', () => {
  it('createCase 先 POST 再 reload (GET)', async () => {
    const calls = mockFetchSequence([
      (_u, init) => init?.method === 'POST' ? jsonResponse(CASE_B) : jsonResponse([CASE_A, CASE_B]),
    ])
    const { createCase, cases } = await freshStore()
    const created = await createCase({
      name: 'Case B', category: 'rust',
    } as never)
    expect(created.id).toBe('b')
    expect(cases.value).toHaveLength(2)
    // 第一次 POST，第二次 GET
    expect(calls[0].init?.method).toBe('POST')
    expect(calls[1].url).toBe('/api/cases')
  })

  it('updateCase PUT /api/cases/:id 后 reload', async () => {
    const calls = mockFetchSequence([
      (_u, init) => init?.method === 'PUT' ? jsonResponse(CASE_A) : jsonResponse([CASE_A]),
    ])
    const { updateCase } = await freshStore()
    await updateCase('a', { name: 'A2' } as never)
    expect(calls[0].url).toBe('/api/cases/a')
    expect(calls[0].init?.method).toBe('PUT')
    expect(calls[1].url).toBe('/api/cases')
  })

  it('updateCase 对 id 做 URL 编码', async () => {
    const calls = mockFetchSequence([
      () => jsonResponse(CASE_A),
      () => jsonResponse([CASE_A]),
    ])
    const { updateCase } = await freshStore()
    await updateCase('a b/c', { name: 'x' } as never)
    expect(calls[0].url).toBe('/api/cases/a%20b%2Fc')
    expect(calls[0].init?.method).toBe('PUT')
  })

  it('deleteCase DELETE /api/cases/:id 后 reload', async () => {
    const calls = mockFetchSequence([
      (_u, init) => init?.method === 'DELETE' ? jsonResponse({}) : jsonResponse([CASE_B]),
    ])
    const { deleteCase, cases } = await freshStore()
    await deleteCase('a')
    expect(calls[0].init?.method).toBe('DELETE')
    expect(calls[0].url).toBe('/api/cases/a')
    expect(cases.value).toHaveLength(1)
  })
})

describe('useCaseStore — filteredCases', () => {
  async function storeWithCases() {
    mockFetchSequence([() => jsonResponse([CASE_A, CASE_B])])
    const store = await freshStore()
    await store.loadCases()
    return store
  }

  it('无过滤时返回全部', async () => {
    const { filteredCases } = await storeWithCases()
    expect(filteredCases.value).toHaveLength(2)
  })

  it('按 tag 过滤 (OR 语义)', async () => {
    const { filteredCases, toggleTag } = await storeWithCases()
    toggleTag('code')
    expect(filteredCases.value.map(c => c.id)).toEqual(['a'])
    toggleTag('debug') // code OR debug
    expect(filteredCases.value.map(c => c.id).sort()).toEqual(['a', 'b'])
  })

  it('按 category 精确过滤', async () => {
    const { filteredCases, setCategory } = await storeWithCases()
    setCategory('rust')
    expect(filteredCases.value.map(c => c.id)).toEqual(['b'])
  })

  it('tag + category 同时生效 (AND)', async () => {
    const { filteredCases, toggleTag, setCategory } = await storeWithCases()
    toggleTag('backend')
    setCategory('go')
    expect(filteredCases.value.map(c => c.id)).toEqual(['a'])
  })

  it('clearFilters 清空所有过滤', async () => {
    const { filteredCases, toggleTag, setCategory, clearFilters } = await storeWithCases()
    toggleTag('code')
    setCategory('go')
    expect(filteredCases.value).toHaveLength(1)
    clearFilters()
    expect(filteredCases.value).toHaveLength(2)
  })
})

describe('useCaseStore — allTags / allCategories', () => {
  it('聚合去重并排序', async () => {
    mockFetchSequence([() => jsonResponse([CASE_A, CASE_B])])
    const { allTags, allCategories, loadCases } = await freshStore()
    await loadCases()
    expect(allTags.value).toEqual(['backend', 'code', 'debug'])
    expect(allCategories.value).toEqual(['go', 'rust'])
  })
})
