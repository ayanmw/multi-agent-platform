/**
 * useSkills.ts
 *
 * Skill 系统前端复用逻辑：维护可用 skill 列表，提供 TypeScript 类型与触发入口。
 * 当前保留一份静态 fallback 列表，实际优先从后端 GET /api/skills 加载。
 */

import { ref } from 'vue'

/** 单个 Skill 的元数据 */
export interface Skill {
  id: string
  name: string
  command: string
  description: string
}

/** 后端 Skill 列表项字段 */
interface SkillApiItem {
  id?: string
  name?: string
  display_name?: string
  description?: string
  command_prefix?: string
}

/** 模块级 skill 列表，所有组件共享同一实例 */
const skills = ref<Skill[]>([
  {
    id: 'graphify',
    name: 'Graphify',
    command: '/graphify',
    description: '将任意输入转换为结构化知识图谱。',
  },
  {
    id: 'verify',
    name: 'Verify',
    command: '/verify',
    description: '对当前上下文中的声明进行多源核验。',
  },
  {
    id: 'research',
    name: 'Research',
    command: '/research',
    description: '深度研究并生成带引用的综合报告。',
  },
])

/** 加载状态 */
const loading = ref(false)
let loaded = false

/**
 * 从后端加载内置 Skill 列表。
 * 仅在初次调用时实际请求，重复调用为 no-op。
 * 请求失败时保留静态 fallback 列表。
 */
async function loadSkills(): Promise<void> {
  if (loaded) return
  loading.value = true
  try {
    const resp = await fetch('/api/skills?source=built_in')
    if (!resp.ok) {
      console.warn('[useSkills] Failed to load skills:', resp.status)
      return
    }
    const data = (await resp.json()) as { skills?: SkillApiItem[] } | SkillApiItem[]
    const items = Array.isArray(data) ? data : data?.skills ?? []
    skills.value = items.map((s) => ({
      id: s.id || '',
      name: s.display_name || s.name || '',
      command: s.command_prefix || '',
      description: s.description || '',
    }))
    loaded = true
  } catch (err) {
    console.warn('[useSkills] Error loading skills:', err)
  } finally {
    loading.value = false
  }
}

/**
 * 启用指定 Skill。
 * 403 视为禁用（抛错），404 仅警告并正常返回，其它非 2xx 抛错。
 */
async function enableSkill(id: string): Promise<void> {
  const resp = await fetch(`/api/skills/${encodeURIComponent(id)}/enable`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  })
  if (resp.status === 403) {
    throw new Error('forbidden')
  }
  if (resp.status === 404) {
    console.warn(`[useSkills] Skill ${id} not found (404)`)
    return
  }
  if (!resp.ok) {
    const text = await resp.text()
    throw new Error(text || `enable skill ${id} failed: ${resp.status}`)
  }
}

/**
 * triggerSkill — 触发一个 skill 命令。
 *
 * 当前仅打印日志并返回 Promise.resolve；
 * TODO: Phase 7 接入后端执行通道（WebSocket / REST）。
 */
async function triggerSkill(command: string): Promise<void> {
  // eslint-disable-next-line no-console
  console.log('[useSkills] trigger:', command)
  // TODO: Phase 7 — 调用后端 skill 执行 API
}

export function useSkills() {
  return {
    skills,
    loading,
    loadSkills,
    enableSkill,
    triggerSkill,
  }
}
