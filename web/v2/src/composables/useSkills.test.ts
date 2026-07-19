/**
 * useSkills composable 单元测试
 *
 * 覆盖点：
 * - loadSkills 从后端 GET /api/skills?source=built_in 加载，并按 display_name/command_prefix 字段映射
 * - 后端返回数组或 { skills: [...] } 两种形态都能解析
 * - 请求失败时保留 fallback 列表、loaded 不置位（允许重试）
 * - enableSkill：403 抛 'forbidden'；404 仅警告不抛；其它非 2xx 抛错；200 正常返回
 *
 * 注意：useSkills 的 skills / loaded 是模块级单例，每个 test 前需重置状态。
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { useSkills } from './useSkills'

// 动态 import 以便在每个 test 间重置模块状态
async function freshUseSkills() {
  vi.resetModules()
  const mod = await import('./useSkills')
  return mod.useSkills()
}

const BUILTIN_PAYLOAD = [
  {
    id: 'builtin-code-helper',
    display_name: '代码助手',
    description: '解释、重构或生成代码片段',
    command_prefix: '/builtin-code-helper',
  },
  {
    id: 'builtin-error-diagnosis',
    display_name: '错误诊断',
    description: '根据错误日志定位问题',
    command_prefix: '/builtin-error-diagnosis',
  },
]

function mockFetch(impl: typeof globalThis.fetch) {
  globalThis.fetch = vi.fn(impl) as unknown as typeof globalThis.fetch
}

beforeEach(() => {
  vi.resetModules()
  // jsdom 默认无 fetch，提供一个空实现由各 test 覆盖
  globalThis.fetch = vi.fn() as unknown as typeof globalThis.fetch
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('useSkills — loadSkills', () => {
  it('从后端数组形态加载并映射 display_name / command_prefix', async () => {
    mockFetch(async () =>
      new Response(JSON.stringify(BUILTIN_PAYLOAD), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }) as unknown as Response,
    )

    const { skills, loadSkills } = await freshUseSkills()
    await loadSkills()

    expect(skills.value).toHaveLength(2)
    expect(skills.value[0]).toEqual({
      id: 'builtin-code-helper',
      name: '代码助手',
      command: '/builtin-code-helper',
      description: '解释、重构或生成代码片段',
    })
  })

  it('后端返回 { skills: [...] } 形态也能解析', async () => {
    mockFetch(async () =>
      new Response(JSON.stringify({ skills: BUILTIN_PAYLOAD }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }) as unknown as Response,
    )

    const { skills, loadSkills } = await freshUseSkills()
    await loadSkills()
    expect(skills.value).toHaveLength(2)
    expect(skills.value[1].id).toBe('builtin-error-diagnosis')
  })

  it('非 200 响应时保留 fallback 列表、不标记为 loaded', async () => {
    mockFetch(async () =>
      new Response('err', { status: 500 }) as unknown as Response,
    )
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})

    const { skills, loadSkills } = await freshUseSkills()
    await loadSkills()

    // fallback 仍存在（graphify / verify / research）
    expect(skills.value.length).toBeGreaterThan(0)
    expect(skills.value.some(s => s.id === 'graphify')).toBe(true)
    expect(warn).toHaveBeenCalled()
  })

  it('网络异常时捕获错误并保留 fallback', async () => {
    mockFetch(async () => {
      throw new Error('network down')
    })
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})

    const { skills, loadSkills } = await freshUseSkills()
    await loadSkills()
    expect(skills.value.some(s => s.id === 'verify')).toBe(true)
    expect(warn).toHaveBeenCalled()
  })

  it('字段缺失时回退到空字符串而非 undefined', async () => {
    mockFetch(async () =>
      new Response(
        JSON.stringify([{ id: 'x' }]),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ) as unknown as Response,
    )
    const { skills, loadSkills } = await freshUseSkills()
    await loadSkills()
    expect(skills.value[0]).toEqual({
      id: 'x',
      name: '',
      command: '',
      description: '',
    })
  })
})

describe('useSkills — enableSkill', () => {
  it('200 时正常 resolve', async () => {
    mockFetch(async () =>
      new Response('', { status: 200 }) as unknown as Response,
    )
    const { enableSkill } = await freshUseSkills()
    await expect(enableSkill('builtin-code-helper')).resolves.toBeUndefined()
  })

  it('403 抛出 forbidden 错误（前端据此提示"不可启用"）', async () => {
    mockFetch(async () =>
      new Response('', { status: 403 }) as unknown as Response,
    )
    const { enableSkill } = await freshUseSkills()
    await expect(enableSkill('some-id')).rejects.toThrow('forbidden')
  })

  it('404 仅警告、不抛错（静默成功）', async () => {
    mockFetch(async () =>
      new Response('', { status: 404 }) as unknown as Response,
    )
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { enableSkill } = await freshUseSkills()
    await expect(enableSkill('missing')).resolves.toBeUndefined()
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('missing'))
  })

  it('其它非 2xx 抛出带状态信息的错误', async () => {
    mockFetch(async () =>
      new Response('boom', { status: 500 }) as unknown as Response,
    )
    const { enableSkill } = await freshUseSkills()
    await expect(enableSkill('x')).rejects.toThrow(/boom|500/)
  })

  it('对 id 做 URL 编码（特殊字符安全）', async () => {
    const calls: string[] = []
    mockFetch(async (input: RequestInfo | URL) => {
      calls.push(String(input))
      return new Response('', { status: 200 }) as unknown as Response
    })
    const { enableSkill } = await freshUseSkills()
    await enableSkill('a b/c')
    expect(calls[0]).toBe('/api/skills/a%20b%2Fc/enable')
  })
})
