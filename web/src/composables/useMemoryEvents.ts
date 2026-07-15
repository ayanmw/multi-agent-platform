// useMemoryEvents — module-level memory event aggregator
//
// Subscribes to the shared WebSocket stream via useWebSocket() and collects
// memory/RAG events into a capped history. It also refreshes the memory list
// when mutations (created/updated/deleted) arrive, keeping the MemoryBrowser
// in sync without requiring a full page refresh.
//
// This module is intentionally singleton state so that multiple consumers
// (App.vue timeline, MemoryBrowser badge, etc.) share the same event feed.
import { ref } from 'vue'
import { useWebSocket } from './useWebSocket'
import { useMemoryStore } from './useMemoryStore'
import type { AgentEvent, EventType } from '../types/events'

const MAX_EVENTS = 200

const events = ref<AgentEvent[]>([])
const stats = ref({
  created: 0,
  updated: 0,
  deleted: 0,
  promoted: 0,
  summarized: 0,
  recalled: 0,
  heartbeats: 0,
})

/** Memory event types tracked by this composable */
const MEMORY_EVENT_TYPES: EventType[] = [
  'memory_created',
  'memory_updated',
  'memory_deleted',
  'memory_promoted',
  'memory_summarize_started',
  'memory_summarize_completed',
  'memory_summarize_failed',
  'memory_recall_performed',
  'heartbeat_beat',
]

function isMemoryEvent(event: AgentEvent): boolean {
  return MEMORY_EVENT_TYPES.includes(event.type)
}

function updateStats(event: AgentEvent) {
  switch (event.type) {
    case 'memory_created':
      stats.value.created++
      break
    case 'memory_updated':
      stats.value.updated++
      break
    case 'memory_deleted':
      stats.value.deleted++
      break
    case 'memory_promoted':
      stats.value.promoted++
      break
    case 'memory_summarize_started':
    case 'memory_summarize_completed':
    case 'memory_summarize_failed':
      stats.value.summarized++
      break
    case 'memory_recall_performed':
      stats.value.recalled++
      break
    case 'heartbeat_beat':
      stats.value.heartbeats++
      break
  }
}

let unsubscribe: (() => void) | null = null
let storeRefreshTimer: ReturnType<typeof setTimeout> | null = null

function onEvent(event: AgentEvent) {
  if (!isMemoryEvent(event)) return

  events.value.push(event)
  if (events.value.length > MAX_EVENTS) {
    events.value.shift()
  }
  updateStats(event)

  // Refresh memory list on mutations, but debounce rapid bursts.
  if (
    event.type === 'memory_created' ||
    event.type === 'memory_updated' ||
    event.type === 'memory_deleted'
  ) {
    if (storeRefreshTimer) clearTimeout(storeRefreshTimer)
    storeRefreshTimer = setTimeout(() => {
      try {
        useMemoryStore().loadMemories()
      } catch (err) {
        console.error('[useMemoryEvents] Failed to refresh memories:', err)
      }
    }, 300)
  }
}

/** Clear the accumulated event history and counters */
function clear(): void {
  events.value = []
  stats.value = {
    created: 0,
    updated: 0,
    deleted: 0,
    promoted: 0,
    summarized: 0,
    recalled: 0,
    heartbeats: 0,
  }
}

/** Return events filtered by optional type, most recent first */
function filter(type?: EventType): AgentEvent[] {
  if (!type) return [...events.value]
  return events.value.filter(e => e.type === type)
}

/** Register the module-level listener exactly once */
export function useMemoryEvents() {
  if (!unsubscribe) {
    const { onEvent: wsOnEvent } = useWebSocket()
    unsubscribe = wsOnEvent(onEvent)
  }

  return {
    events,
    stats,
    clear,
    filter,
  }
}
