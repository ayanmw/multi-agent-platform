// useSkillEvents — Skill 子系统事件聚合器。
//
// 订阅共享 WebSocket 流并把 skill_enabled / skill_disabled / skill_loaded /
// skill_unloaded / skill_changed / skill_rendered 事件收集到有界历史里，同时
// 维护当前已启用 skill ID 集合与最近一次渲染的 skill blocks，供 ContextWindowPanel
// 等组件直接消费。
//
// 与 useCronEvents / useMemoryEvents 对齐：module-level singleton，多组件共享
// 同一事件流。
import { ref } from 'vue'
import { useWebSocket } from './useWebSocket'
import { useSkills } from './useSkills'
import type { AgentEvent, EventType } from '@/types/events'
import type { SkillBlock } from '@/types/skill'

/** 最多保留最近 N 条事件，防止内存无限增长。 */
const MAX_EVENTS = 50

/** 本 composable 关心的事件类型集合。 */
const SKILL_EVENT_TYPES: EventType[] = [
  'skill_enabled',
  'skill_disabled',
  'skill_loaded',
  'skill_unloaded',
  'skill_changed',
  'skill_rendered',
]

function isSkillEvent(event: AgentEvent): boolean {
  return SKILL_EVENT_TYPES.includes(event.type)
}

/** 事件历史（最近 N 条），按到达顺序追加，溢出从头丢弃。 */
const skillEvents = ref<AgentEvent[]>([])

/** 当前已启用的 skill ID 集合（运行时事件驱动）。 */
const enabledSkillIds = ref<Set<string>>(new Set())

/** 最近一次 skill_rendered 事件携带的 skill blocks，按 task_id 索引。 */
const skillBlocksByTask = ref<Record<string, SkillBlock[]>>({})

/** 最近一次 skill_rendered 的 task_id，方便面板直接读取。 */
const lastRenderedTaskId = ref<string>('')

/** 计数器：各事件类型发生次数，供面板徽标/统计展示。 */
const stats = ref({
  enabled: 0,
  disabled: 0,
  loaded: 0,
  unloaded: 0,
  changed: 0,
  rendered: 0,
})

/** 更新计数器。 */
function bumpStats(event: AgentEvent): void {
  switch (event.type) {
    case 'skill_enabled':
      stats.value.enabled++
      break
    case 'skill_disabled':
      stats.value.disabled++
      break
    case 'skill_loaded':
      stats.value.loaded++
      break
    case 'skill_unloaded':
      stats.value.unloaded++
      break
    case 'skill_changed':
      stats.value.changed++
      break
    case 'skill_rendered':
      stats.value.rendered++
      break
  }
}

let unsubscribe: (() => void) | null = null

/** 处理单条事件：追加历史 + 计数 + 更新启用集合与 blocks。 */
function onEvent(event: AgentEvent): void {
  if (!isSkillEvent(event)) return

  skillEvents.value.push(event)
  if (skillEvents.value.length > MAX_EVENTS) {
    skillEvents.value.shift()
  }
  bumpStats(event)

  const data = (event.data || {}) as Record<string, unknown>
  const id = (data.id as string | undefined) || event.task_id

  // enabled / disabled：同步 enabledSkillIds 集合，并刷新 useSkills 列表状态。
  if (event.type === 'skill_enabled' && id) {
    enabledSkillIds.value.add(id)
    refreshSkillState(id, 'enabled')
    return
  }
  if (event.type === 'skill_disabled' && id) {
    enabledSkillIds.value.delete(id)
    refreshSkillState(id, 'disabled')
    return
  }

  // skill_rendered：保存 blocks 供 ContextWindowPanel 展示。
  if (event.type === 'skill_rendered') {
    const taskId = event.task_id
    if (taskId) {
      lastRenderedTaskId.value = taskId
      const blocks = data.skill_blocks as SkillBlock[] | undefined
      skillBlocksByTask.value[taskId] = blocks ?? []
    }
    return
  }

  // loaded / unloaded / changed：尝试刷新 useSkills 本地列表，保持元数据一致。
  if (event.type === 'skill_loaded' || event.type === 'skill_changed') {
    refreshSkillList()
    return
  }
  if (event.type === 'skill_unloaded' && id) {
    enabledSkillIds.value.delete(id)
    refreshSkillList()
  }
}

/** 刷新 useSkills 本地缓存中的单个 skill 状态（best-effort）。 */
function refreshSkillState(id: string, state: string): void {
  try {
    const store = useSkills()
    const found = store.skills.value.find(s => s.id === id)
    if (found && 'state' in found) {
      ;(found as Record<string, unknown>).state = state
    }
  } catch (err) {
    console.error('[useSkillEvents] refresh skill state failed:', err)
  }
}

/** 全量刷新 skill 列表（best-effort）。 */
function refreshSkillList(): void {
  try {
    const store = useSkills()
    const promise = store.loadSkills?.()
    if (promise && typeof promise.catch === 'function') {
      promise.catch((err: unknown) => {
        console.error('[useSkillEvents] loadSkills failed:', err)
      })
    }
  } catch (err) {
    console.error('[useSkillEvents] refresh skill list failed:', err)
  }
}

/** 清空事件历史与计数器。 */
function clear(): void {
  skillEvents.value = []
  enabledSkillIds.value.clear()
  skillBlocksByTask.value = {}
  lastRenderedTaskId.value = ''
  stats.value = {
    enabled: 0,
    disabled: 0,
    loaded: 0,
    unloaded: 0,
    changed: 0,
    rendered: 0,
  }
}

/** 按类型过滤事件历史，默认按时间倒序返回副本。 */
function filter(type?: EventType): AgentEvent[] {
  if (!type) return [...skillEvents.value].reverse()
  return skillEvents.value.filter(e => e.type === type).reverse()
}

/** 注册模块级监听器，仅注册一次。 */
export function useSkillEvents() {
  if (!unsubscribe) {
    const { onEvent: wsOnEvent } = useWebSocket()
    unsubscribe = wsOnEvent(onEvent)
  }

  return {
    skillEvents,
    enabledSkillIds,
    skillBlocksByTask,
    lastRenderedTaskId,
    stats,
    clear,
    filter,
  }
}
