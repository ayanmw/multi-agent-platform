/**
 * useSkills.spec.ts
 *
 * 覆盖 useSkills composable 的核心行为：加载、过滤、CRUD、
 * enable/disable 与 WebSocket 事件同步。
 *
 * 注意：useSkills 是模块级单例，测试通过 vi.resetModules() 获取新实例。
 */
import { describe, it, expect, beforeEach, vi, afterEach, type MockedFunction } from 'vitest'
import type { AgentEvent } from '@/types/events'

/** 由 mock useWebSocket 捕获的 onEvent 处理器，供测试直接触发事件 */
let capturedHandler: ((event: AgentEvent) => void) | null = null

vi.mock('../useWebSocket', () => {
  return {
    useWebSocket: () => ({
      onEvent: (handler: (event: AgentEvent) => void) => {
        capturedHandler = handler
        return () => {
          capturedHandler = null
        }
      },
    }),
  }
})

async function freshUseSkills() {
  vi.resetModules()
  const mod = await import('../useSkills')
  return mod.useSkills()
}

const BASE_URL = 'http://localhost'
const SKILL_API = `${BASE_URL}/api/skills`

function fullUrl(path: string): string {
  return `${BASE_URL}${path}`
}

function mockSkill(id: string, overrides: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
  return {
    id,
    version: '1.0.0',
    display_name: `Skill ${id}`,
    description: 'desc',
    authors: [],
    tags: [],
    source: 'built_in',
    source_url: '',
    is_local_editable: false,
    templates: [],
    parameters: [],
    required_tools: [],
    suggested_tools: [],
    permissions: [],
    triggers: { keywords: [], intents: [], file_patterns: [] },
    state: 'enabled',
    invalid_reason: '',
    scope: 'global',
    project_id: '',
    workspace_dir: '',
    created_at: 1,
    updated_at: 2,
    ...overrides,
  }
}

function mockFetch(impl: typeof globalThis.fetch) {
  globalThis.fetch = vi.fn(impl) as unknown as typeof globalThis.fetch
}

function createOkResponse(payload: unknown) {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

function lastCallUrl(): string | undefined {
  const fn = globalThis.fetch as unknown as MockedFunction<typeof fetch>
  const calls = fn.mock.calls
  if (calls.length === 0) return undefined
  const input = calls[calls.length - 1][0]
  return String(input)
}

beforeEach(() => {
  vi.resetModules()
  capturedHandler = null
  globalThis.fetch = vi.fn() as unknown as typeof globalThis.fetch
  // 给 URL 构造函数一个完整 base，避免 jsdom 解析相对路径失败
  vi.stubGlobal('window', { location: { origin: BASE_URL, protocol: 'http:', host: 'localhost' } })
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('useSkills — load', () => {
  it('从后端加载 skill 数组并反映到 skills ref', async () => {
    mockFetch(async () => createOkResponse([mockSkill('a')]))

    const { skills, loadSkills } = await freshUseSkills()
    await loadSkills()

    expect(skills.value).toHaveLength(1)
    expect(skills.value[0].id).toBe('a')
  })

  it('支持 source 过滤，URL 包含 source 参数', async () => {
    mockFetch(async () => createOkResponse([]))

    const { loadSkills } = await freshUseSkills()
    await loadSkills({ source: 'local_db' })

    expect(lastCallUrl()?.replace(BASE_URL, '')).toBe('/api/skills?source=local_db')
  })

  it('支持搜索字符串 q', async () => {
    mockFetch(async () => createOkResponse([]))

    const { loadSkills } = await freshUseSkills()
    await loadSkills({ q: 'code' })

    expect(lastCallUrl()?.replace(BASE_URL, '')).toBe('/api/skills?q=code')
  })

  it('请求失败时写入 error ref', async () => {
    mockFetch(async () => new Response('boom', { status: 500 }))
    const { error, loadSkills } = await freshUseSkills()
    await loadSkills()

    expect(error.value).toContain('500')
  })
})

describe('useSkills — CRUD', () => {
  it('createSkill 调用 POST /api/skills 并 upsert 本地状态', async () => {
    const created = mockSkill('new', { source: 'local_db', state: 'enabled', display_name: 'New' })
    mockFetch(async () => createOkResponse(created))

    const { skills, createSkill } = await freshUseSkills()
    const result = await createSkill({
      id: 'new',
      display_name: 'New',
      content: 'hello {{name}}',
    })

    expect(result.id).toBe('new')
    expect(skills.value).toHaveLength(1)
    expect(lastCallUrl()?.replace(BASE_URL, '')).toBe('/api/skills')
  })

  it('updateSkill 调用 PUT /api/skills/:id 并更新本地状态', async () => {
    const updated = mockSkill('x', { display_name: 'Updated' })
    mockFetch(async () => createOkResponse(updated))

    const { skills, updateSkill } = await freshUseSkills()
    skills.value = [{ ...(mockSkill('x', { display_name: 'Old' }) as any) }]
    const result = await updateSkill('x', { display_name: 'Updated' })

    expect(result.display_name).toBe('Updated')
    expect(skills.value[0].display_name).toBe('Updated')
    expect(lastCallUrl()?.replace(BASE_URL, '')).toBe('/api/skills/x')
  })

  it('deleteSkill 调用 DELETE /api/skills/:id 并移除本地状态', async () => {
    mockFetch(async () => new Response(null, { status: 204 }))

    const { skills, deleteSkill } = await freshUseSkills()
    skills.value = [{ ...(mockSkill('del') as any) }]
    await deleteSkill('del')

    expect(skills.value).toHaveLength(0)
    expect(lastCallUrl()?.replace(BASE_URL, '')).toBe('/api/skills/del')
  })
})

describe('useSkills — enable/disable', () => {
  it('enableSkill 更新 state 并进入 enabledIds', async () => {
    const s = mockSkill('e', { state: 'enabled' })
    mockFetch(async () => createOkResponse(s))

    const { skills, enabledIds, enableSkill } = await freshUseSkills()
    skills.value = [{ ...(mockSkill('e', { state: 'disabled' }) as any) }]
    const result = await enableSkill('e')

    expect(result.state).toBe('enabled')
    expect(enabledIds.value.has('e')).toBe(true)
  })

  it('disableSkill 更新 state 并移出 enabledIds', async () => {
    const s = mockSkill('d', { state: 'disabled' })
    mockFetch(async () => createOkResponse(s))

    const { skills, enabledIds, disableSkill } = await freshUseSkills()
    skills.value = [{ ...(mockSkill('d', { state: 'enabled' }) as any) }]
    const result = await disableSkill('d')

    expect(result.state).toBe('disabled')
    expect(enabledIds.value.has('d')).toBe(false)
  })

  it('toggleSkill 根据当前状态切换', async () => {
    mockFetch(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/disable')) return createOkResponse(mockSkill('t', { state: 'disabled' }))
      return createOkResponse(mockSkill('t', { state: 'enabled' }))
    })

    const { skills, enabledIds, toggleSkill } = await freshUseSkills()
    skills.value = [{ ...(mockSkill('t', { state: 'enabled' }) as any) }]
    await toggleSkill('t')

    expect(enabledIds.value.has('t')).toBe(false)
  })
})

describe('useSkills — events', () => {
  it('skill_enabled 事件更新本地状态并刷新 /api/skills/:id', async () => {
    const skillAfter = mockSkill('evt', { state: 'enabled' })
    mockFetch(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/skills/evt')) return createOkResponse(skillAfter)
      return createOkResponse([])
    })

    const { skills, enabledIds } = await freshUseSkills()
    skills.value = [{ ...(mockSkill('evt', { state: 'disabled' }) as any) }]

    expect(capturedHandler).not.toBeNull()
    capturedHandler!({
      type: 'skill_enabled',
      task_id: '',
      agent_id: 'skill',
      step_index: 0,
      timestamp: Date.now(),
      data: { id: 'evt', state: 'enabled' },
    } as AgentEvent)

    // ensureSubscribed 内 getSkill 是异步的，flush 等待 then 回调完成
    await new Promise(r => setTimeout(r, 0))

    expect(skills.value[0].state).toBe('enabled')
    expect(enabledIds.value.has('evt')).toBe(true)
  })

  it('skill_unloaded 事件移除对应 skill', async () => {
    mockFetch(async () => createOkResponse([]))

    const { skills } = await freshUseSkills()
    skills.value = [
      { ...(mockSkill('keep') as any) },
      { ...(mockSkill('remove') as any) },
    ]

    expect(capturedHandler).not.toBeNull()
    capturedHandler!({
      type: 'skill_unloaded',
      task_id: '',
      agent_id: 'skill',
      step_index: 0,
      timestamp: Date.now(),
      data: { id: 'remove' },
    } as AgentEvent)

    expect(skills.value).toHaveLength(1)
    expect(skills.value[0].id).toBe('keep')
  })

  it('skill_rendered 事件写入 injectedSkillBlocks', async () => {
    mockFetch(async () => createOkResponse([]))

    const { injectedSkillBlocks } = await freshUseSkills()

    expect(capturedHandler).not.toBeNull()
    capturedHandler!({
      type: 'skill_rendered',
      task_id: 't1',
      agent_id: 'skill',
      step_index: 0,
      timestamp: Date.now(),
      data: {
        skill_blocks: [
          {
            skill_id: 's1',
            template_name: 'system_prompt',
            rendered_content: 'rendered',
            estimated_tokens: 10,
          },
        ],
      },
    } as AgentEvent)

    expect(injectedSkillBlocks.value['t1']).toHaveLength(1)
    expect(injectedSkillBlocks.value['t1'][0].skill_id).toBe('s1')
  })
})
