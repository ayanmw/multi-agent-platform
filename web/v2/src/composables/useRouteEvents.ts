// useRouteEvents — 多模型路由事件聚合器。
//
// 订阅共享 WebSocket 流（useWebSocket）并把 model_routed / intent_classified /
// model_fallback_used / model_rate_limited / cost_budget_exceeded 事件收集到
// 有界历史里，供 Inspector Routing 面板展示。
import { computed, ref } from 'vue'
import { useWebSocket } from './useWebSocket'
import type { AgentEvent, EventType } from '@/types/events'

/** 最多保留最近 N 条事件。 */
const MAX_EVENTS = 100

const routeEvents = ref<AgentEvent[]>([])

const ROUTING_EVENT_TYPES: EventType[] = [
  'model_routed',
  'intent_classified',
  'model_fallback_used',
  'model_rate_limited',
  'cost_budget_exceeded',
]

function isRoutingEvent(event: AgentEvent): boolean {
  return ROUTING_EVENT_TYPES.includes(event.type)
}

let unsubscribe: (() => void) | null = null

function onEvent(event: AgentEvent): void {
  if (!isRoutingEvent(event)) return

  routeEvents.value.push(event)
  if (routeEvents.value.length > MAX_EVENTS) {
    routeEvents.value.shift()
  }
}

function clear(): void {
  routeEvents.value = []
}

/** 按任务聚合路由决策：每个 task 保留最新 model_routed 与当前决策链。 */
const decisionsByTask = computed(() => {
  const map: Record<
    string,
    {
      taskId: string
      subTaskId: string
      agentId: string
      intent?: string
      model?: string
      tier?: string
      fallback?: string
      reason?: string
      cheapFirst?: boolean
      events: AgentEvent[]
    }
  > = {}

  for (const ev of routeEvents.value) {
    const key = ev.sub_task_id || ev.task_id
    if (!map[key]) {
      map[key] = {
        taskId: ev.task_id,
        subTaskId: ev.sub_task_id,
        agentId: ev.agent_id,
        events: [],
      }
    }
    const entry = map[key]
    entry.events.push(ev)

    if (ev.type === 'model_routed') {
      const d = ev.data || {}
      entry.model = String(d.model || '')
      entry.intent = String(d.intent || '')
      entry.tier = String(d.tier || '')
      entry.reason = String(d.reason || '')
      entry.fallback = d.fallback ? String(d.fallback) : undefined
      entry.cheapFirst = Boolean(d.cheap_first_attempt)
    }
    if (ev.type === 'intent_classified') {
      const d = ev.data || {}
      entry.intent = String(d.primary_intent || entry.intent || '')
    }
    if (ev.type === 'model_fallback_used') {
      const d = ev.data || {}
      entry.fallback = String(d.fallback || '')
    }
  }

  return map
})

/** 最近事件流，按时间倒序。 */
const recentEvents = computed(() => [...routeEvents.value].reverse())

export function useRouteEvents() {
  if (!unsubscribe) {
    const { onEvent: wsOnEvent } = useWebSocket()
    unsubscribe = wsOnEvent(onEvent)
  }

  return {
    routeEvents: recentEvents,
    decisionsByTask,
    clear,
  }
}
