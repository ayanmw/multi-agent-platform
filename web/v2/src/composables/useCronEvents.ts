// useCronEvents — Cron 子系统事件聚合器。
//
// 订阅共享 WebSocket 流（useWebSocket）并把 cron_* 事件收集到一个有界历史里，
// 同时在状态变更（created/updated/deleted/状态切换）时回写 useCrons 本地缓存，
// 实现"事件驱动 + 免轮询"的实时同步。
//
// 与 useMemoryEvents 对齐：module-level singleton，多组件共享同一事件流。
import { ref } from 'vue'
import { useWebSocket } from './useWebSocket'
import { useCrons } from './useCrons'
import type { AgentEvent, EventType } from '@/types/events'
import type { Cron } from '@/types/cron'

/** 最多保留最近 N 条事件，防止内存无限增长。 */
const MAX_EVENTS = 50

/** 事件历史（最近 N 条），按到达顺序追加，溢出从头丢弃。 */
const cronEvents = ref<AgentEvent[]>([])

/** 计数器：各事件类型发生次数，供面板徽标/统计展示。 */
const stats = ref({
  created: 0,
  updated: 0,
  deleted: 0,
  toggled: 0, // enabled / disabled / paused / resumed 合计
  triggered: 0,
  executionStarted: 0,
  executionCompleted: 0,
  executionFailed: 0,
  executionSkipped: 0,
  missed: 0,
  notifications: 0,
})

/** 本 composable 关心的事件类型集合。 */
const CRON_EVENT_TYPES: EventType[] = [
  'cron_created',
  'cron_updated',
  'cron_deleted',
  'cron_enabled',
  'cron_disabled',
  'cron_paused',
  'cron_resumed',
  'cron_triggered',
  'cron_execution_started',
  'cron_execution_completed',
  'cron_execution_failed',
  'cron_execution_skipped',
  'cron_missed',
  'cron_notification',
]

function isCronEvent(event: AgentEvent): boolean {
  return CRON_EVENT_TYPES.includes(event.type)
}

/** 更新计数器。 */
function bumpStats(event: AgentEvent): void {
  switch (event.type) {
    case 'cron_created': stats.value.created++; break
    case 'cron_updated': stats.value.updated++; break
    case 'cron_deleted': stats.value.deleted++; break
    case 'cron_enabled':
    case 'cron_disabled':
    case 'cron_paused':
    case 'cron_resumed':
      stats.value.toggled++; break
    case 'cron_triggered': stats.value.triggered++; break
    case 'cron_execution_started': stats.value.executionStarted++; break
    case 'cron_execution_completed': stats.value.executionCompleted++; break
    case 'cron_execution_failed': stats.value.executionFailed++; break
    case 'cron_execution_skipped': stats.value.executionSkipped++; break
    case 'cron_missed': stats.value.missed++; break
    case 'cron_notification': stats.value.notifications++; break
  }
}

let unsubscribe: (() => void) | null = null
let refreshTimer: ReturnType<typeof setTimeout> | null = null

/** 状态变更后防抖刷新 cron 列表，避免短时间多次事件重复请求。 */
function scheduleRefresh(): void {
  if (refreshTimer) clearTimeout(refreshTimer)
  refreshTimer = setTimeout(() => {
    refreshTimer = null
    try {
      useCrons().refreshCrons()
    } catch (err) {
      console.error('[useCronEvents] refresh crons failed:', err)
    }
  }, 300)
}

/** 处理单条事件：追加历史 + 计数 + 回写本地缓存。 */
function onEvent(event: AgentEvent): void {
  if (!isCronEvent(event)) return

  cronEvents.value.push(event)
  if (cronEvents.value.length > MAX_EVENTS) {
    cronEvents.value.shift()
  }
  bumpStats(event)

  const data = (event.data || {}) as Record<string, unknown>
  const store = useCrons()

  // created / updated / enabled / disabled / paused / resumed：事件 data 里
  // 通常带完整 cron 对象（后端 cron.Event 构造 helper 写入），直接 upsert。
  if (
    event.type === 'cron_created' ||
    event.type === 'cron_updated' ||
    event.type === 'cron_enabled' ||
    event.type === 'cron_disabled' ||
    event.type === 'cron_paused' ||
    event.type === 'cron_resumed'
  ) {
    const cron = data.cron as Cron | undefined
    if (cron && cron.id) {
      store.upsertLocal(cron)
    } else {
      // 没有 cron 体的状态切换事件，退化为防抖全量刷新。
      scheduleRefresh()
    }
    return
  }

  // deleted：从本地移除。
  if (event.type === 'cron_deleted') {
    const id = (data.id as string | undefined) || event.task_id
    if (id) store.removeLocal(id)
    return
  }

  // execution_*：后端 cron_execution_started/completed/failed/skipped/missed 事件
  // 的 data 只带 execution_id（不含完整 execution 对象），无法直接回写缓存，
  // 因此退化为防抖全量刷新该 cron 的执行历史。
  // 触发链路上一次 cron_triggered → execution_started → execution_completed，
  // 用防抖合并连续事件，最后一次完成后刷新一次即可。
  if (
    event.type === 'cron_execution_started' ||
    event.type === 'cron_execution_completed' ||
    event.type === 'cron_execution_failed' ||
    event.type === 'cron_execution_skipped' ||
    event.type === 'cron_missed'
  ) {
    scheduleRefresh()
    return
  }

  // cron_triggered / cron_notification：仅记录历史，无需回写。
}

/** 清空事件历史与计数器。 */
function clear(): void {
  cronEvents.value = []
  stats.value = {
    created: 0,
    updated: 0,
    deleted: 0,
    toggled: 0,
    triggered: 0,
    executionStarted: 0,
    executionCompleted: 0,
    executionFailed: 0,
    executionSkipped: 0,
    missed: 0,
    notifications: 0,
  }
}

/** 按类型过滤事件历史，默认按时间倒序返回副本。 */
function filter(type?: EventType): AgentEvent[] {
  if (!type) return [...cronEvents.value].reverse()
  return cronEvents.value.filter(e => e.type === type).reverse()
}

/** 注册模块级监听器，仅注册一次。 */
export function useCronEvents() {
  if (!unsubscribe) {
    const { onEvent: wsOnEvent } = useWebSocket()
    unsubscribe = wsOnEvent(onEvent)
  }

  return {
    cronEvents,
    stats,
    clear,
    filter,
  }
}
