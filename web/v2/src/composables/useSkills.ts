/**
 * useSkills.ts
 *
 * Skill 系统前端复用逻辑：维护可用 Skill 列表，提供完整 CRUD、启用/禁用、
 * 搜索过滤以及 WebSocket 事件同步。所有后端 API 交互都使用绝对路径 `/api/*`，
 * 避免在 test/jsdom 环境中因相对路径解析失败。
 */

import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useWebSocket } from './useWebSocket'
import type { AgentEvent, EventType } from '@/types/events'
import type { CreateSkillRequest, Skill, SkillBlock, SkillScope, SkillSource, SkillState, UpdateSkillRequest } from '@/types/skill'

/** Skill 列表过滤器 */
export interface SkillFilter {
  source?: SkillSource
  scope?: SkillScope
  projectId?: string
  workdir?: string
  q?: string
}

/** 模块级 Skill 列表，所有组件共享同一实例 */
const skills = ref<Skill[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

/**
 * 已启用 Skill 的 ID 集合。
 * 注意：skills[i].state 是真实来源；enabledIds 仅供快速渲染 switch，
 * 事件同步时两者一起更新。
 */
const enabledIds = ref<Set<string>>(new Set())

/** 某次 run 注入的 SkillBlocks，按 taskId 缓存 */
const injectedSkillBlocks = ref<Record<string, SkillBlock[]>>({})

/** 取消 WebSocket 监听函数 */
let unsubscribe: (() => void) | null = null

/**
 * 同步 enabledIds 与当前 skills 状态。
 * 在列表加载或事件更新后调用，保证集合与 state 一致。
 */
function syncEnabledIds() {
  const set = new Set<string>()
  for (const s of skills.value) {
    if (s.state === 'enabled') {
      set.add(s.id)
    }
  }
  enabledIds.value = set
}

/**
 * 更新单个 skill 的内存状态（替换数组中对应项）
 */
function upsertSkill(updated: Skill) {
  const idx = skills.value.findIndex(s => s.id === updated.id)
  if (idx >= 0) {
    skills.value[idx] = updated
  } else {
    skills.value.push(updated)
  }
  syncEnabledIds()
}

/**
 * 从 skills 数组中移除指定 skill。
 */
function removeSkill(id: string) {
  skills.value = skills.value.filter(s => s.id !== id)
  syncEnabledIds()
}

/**
 * 判断 skill 是否本地可编辑。
 */
export function isEditableSkill(skill: Skill): boolean {
  return skill.is_local_editable || skill.source === 'local_db'
}

/**
 * 判断 skill 是否只读（local_file）。
 */
export function isReadOnlySkill(skill: Skill): boolean {
  return skill.source === 'local_file'
}

/**
 * 判断 skill 是否为内置。
 */
export function isBuiltInSkill(skill: Skill): boolean {
  return skill.source === 'built_in'
}

/**
 * 注册 WebSocket 事件监听。
 * 幂等：只注册一次，重复调用不重复监听。
 */
function ensureSubscribed() {
  if (unsubscribe) return
  const { onEvent } = useWebSocket()

  const skillEventTypes: EventType[] = [
    'skill_enabled',
    'skill_disabled',
    'skill_loaded',
    'skill_unloaded',
    'skill_changed',
    'skill_rendered',
  ]

  unsubscribe = onEvent((event: AgentEvent) => {
    if (!skillEventTypes.includes(event.type)) return

    const data = event.data || {}
    const id = typeof data.id === 'string' ? data.id : ''
    const state = typeof data.state === 'string' ? (data.state as SkillState) : undefined

    if (event.type === 'skill_rendered') {
      const blocks = Array.isArray(data.skill_blocks)
        ? (data.skill_blocks as SkillBlock[])
        : []
      const taskId = event.task_id
      if (taskId) {
        injectedSkillBlocks.value[taskId] = blocks
      }
      return
    }

    if (event.type === 'skill_unloaded') {
      removeSkill(id)
      return
    }

    // enabled / disabled / loaded / changed：拉取完整 skill 数据以更新本地状态。
    if (id) {
      if (state) {
        // 先根据 events 快速改本地状态，随后异步刷新避免 stale
        const existing = skills.value.find(s => s.id === id)
        if (existing) {
          existing.state = state
          existing.updated_at = data.updated_at ? Number(data.updated_at) : Date.now() / 1000
          upsertSkill(existing)
        }
      }
      getSkill(id)
        .then(s => {
          if (s) upsertSkill(s)
        })
        .catch((err: unknown) => {
          console.warn('[useSkills] refresh skill after event failed:', err)
        })
    }
  })
}

/**
 * 从后端加载 Skill 列表。
 * 支持按 source / scope / project_id / workdir / q 过滤。
 */
async function loadSkills(filter: SkillFilter = {}): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const params = new URLSearchParams()
    if (filter.source) params.set('source', filter.source)
    if (filter.scope) params.set('scope', filter.scope)
    if (filter.projectId) params.set('project_id', filter.projectId)
    if (filter.workdir) params.set('workdir', filter.workdir)
    if (filter.q !== undefined) params.set('q', filter.q)

    const query = params.toString()
    const url = `/api/skills${query ? `?${query}` : ''}`
    const resp = await fetch(url)
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`load skills failed: ${resp.status} ${text}`)
    }
    const data = (await resp.json()) as Skill[]
    skills.value = Array.isArray(data) ? data : []
    syncEnabledIds()
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err)
    error.value = msg
    console.error('[useSkills] load failed:', msg)
  } finally {
    loading.value = false
  }
}

/**
 * 同步刷新全部 skill，重置 filter。
 */
async function refresh(): Promise<void> {
  return loadSkills({})
}

/**
 * 搜索 Skill（按 id / display_name / description / tags）。
 */
async function searchSkills(q: string): Promise<void> {
  return loadSkills({ q })
}

/**
 * 获取单个 Skill 详情。
 */
async function getSkill(id: string): Promise<Skill | undefined> {
  try {
    const resp = await fetch(`/api/skills/${encodeURIComponent(id)}`)
    if (!resp.ok) {
      if (resp.status === 404) return undefined
      const text = await resp.text()
      throw new Error(`get skill failed: ${resp.status} ${text}`)
    }
    return (await resp.json()) as Skill
  } catch (err) {
    console.warn('[useSkills] get skill failed:', err)
    return undefined
  }
}

/**
 * 创建 local_db Skill。
 * 后端自动设置 source=local_db、is_local_editable=true、state=enabled。
 */
async function createSkill(draft: CreateSkillRequest): Promise<Skill> {
  const resp = await fetch('/api/skills', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(draft),
  })
  if (!resp.ok) {
    const text = await resp.text()
    throw new Error(text || `create skill failed: ${resp.status}`)
  }
  const created = (await resp.json()) as Skill
  upsertSkill(created)
  return created
}

/**
 * 更新 Skill。
 * 内置 skill 后端会自动 fork 为 local_db shadow；local_file 会 403。
 */
async function updateSkill(id: string, changes: UpdateSkillRequest): Promise<Skill> {
  const resp = await fetch(`/api/skills/${encodeURIComponent(id)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(changes),
  })
  if (!resp.ok) {
    const text = await resp.text()
    throw new Error(text || `update skill failed: ${resp.status}`)
  }
  const updated = (await resp.json()) as Skill
  upsertSkill(updated)
  return updated
}

/**
 * 删除 Skill。
 * local_db shadow 删除后会恢复内置版本；local_file 与 built_in 禁止删除。
 */
async function deleteSkill(id: string): Promise<void> {
  const resp = await fetch(`/api/skills/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  })
  if (!resp.ok) {
    const text = await resp.text()
    throw new Error(text || `delete skill failed: ${resp.status}`)
  }
  removeSkill(id)
}

/**
 * 启用指定 Skill。
 */
async function enableSkill(id: string): Promise<Skill> {
  const resp = await fetch(`/api/skills/${encodeURIComponent(id)}/enable`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  })
  if (!resp.ok) {
    const text = await resp.text()
    throw new Error(text || `enable skill failed: ${resp.status}`)
  }
  const updated = (await resp.json()) as Skill
  upsertSkill(updated)
  return updated
}

/**
 * 禁用指定 Skill。
 */
async function disableSkill(id: string): Promise<Skill> {
  const resp = await fetch(`/api/skills/${encodeURIComponent(id)}/disable`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  })
  if (!resp.ok) {
    const text = await resp.text()
    throw new Error(text || `disable skill failed: ${resp.status}`)
  }
  const updated = (await resp.json()) as Skill
  upsertSkill(updated)
  return updated
}

/**
 * 切换 Skill 启用状态。
 */
async function toggleSkill(id: string): Promise<Skill> {
  const s = skills.value.find(x => x.id === id)
  const isEnabled = enabledIds.value.has(id) || s?.state === 'enabled'
  return isEnabled ? disableSkill(id) : enableSkill(id)
}

/**
 * 按来源/作用域过滤的 Skill 列表。
 */
const filteredSkills = computed(() => (filter: SkillFilter = {}) => {
  let result = skills.value
  if (filter.source) {
    result = result.filter(s => s.source === filter.source)
  }
  if (filter.scope) {
    result = result.filter(s => s.scope === filter.scope)
  }
  if (filter.projectId) {
    result = result.filter(s => s.project_id === filter.projectId)
  }
  if (filter.workdir) {
    result = result.filter(s => s.workspace_dir === filter.workdir)
  }
  if (filter.q) {
    const q = filter.q.toLowerCase()
    result = result.filter(
      s =>
        s.id.toLowerCase().includes(q) ||
        s.display_name.toLowerCase().includes(q) ||
        s.description.toLowerCase().includes(q) ||
        s.tags.some(t => t.toLowerCase().includes(q)),
    )
  }
  return result
})

/**
 * 导出模块级 composable。
 * 使用时会自动订阅 WebSocket 事件，组件卸载时取消订阅。
 */
export function useSkills() {
  ensureSubscribed()

  onMounted(() => {
    if (skills.value.length === 0 && !loading.value) {
      loadSkills().catch((err: unknown) => {
        console.warn('[useSkills] auto load failed:', err)
      })
    }
  })

  onUnmounted(() => {
    // 注意：由于 skills ref 是全局单例，多个组件共享时不应真正取消订阅，
    // 否则最后一个组件卸载后全局状态就不再同步。这里保留监听器，
    // 因为它是幂等单例，且对整体应用有功。
  })

  return {
    skills,
    loading,
    error,
    enabledIds,
    filteredSkills,
    injectedSkillBlocks,
    loadSkills,
    refresh,
    getSkill,
    createSkill,
    updateSkill,
    deleteSkill,
    enableSkill,
    disableSkill,
    toggleSkill,
    searchSkills,
    isEditableSkill,
    isReadOnlySkill,
    isBuiltInSkill,
  }
}
