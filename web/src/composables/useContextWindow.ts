// useContextWindow — tracks the current task's LLM context window snapshot.
//
// The backend emits a `context_window_snapshot` event before every think()
// call, and we only keep the snapshot for the currently active task. This
// avoids accumulating long-term history in the frontend and prevents stale
// snapshots from one task leaking into another.
import { ref } from 'vue'
import { useWebSocket } from './useWebSocket'
import type { AgentEvent, ContextWindowSnapshotData } from '../types/events'

// The single snapshot for the currently active task.
const currentSnapshot = ref<ContextWindowSnapshotData | null>(null)
const activeTaskId = ref<string>('')

let unsubscribe: (() => void) | null = null

function onEvent(event: AgentEvent) {
  if (event.type !== 'context_window_snapshot') return
  if (event.task_id !== activeTaskId.value) return

  const data = event.data as unknown as ContextWindowSnapshotData
  if (!data) return
  currentSnapshot.value = data
}

/** Set the task ID whose snapshots should be tracked; clears any stale snapshot. */
function setActiveTaskId(taskId: string): void {
  if (activeTaskId.value === taskId) return
  activeTaskId.value = taskId
  currentSnapshot.value = null
}

/** Return the latest snapshot for a given task, or null if none. */
function latestFor(taskId: string): ContextWindowSnapshotData | null {
  if (activeTaskId.value !== taskId) return null
  return currentSnapshot.value
}

/** Clear the current snapshot (e.g. when leaving the active task). */
function clear(): void {
  currentSnapshot.value = null
}

/** Register the singleton listener and return reactive snapshot state */
export function useContextWindow() {
  if (!unsubscribe) {
    const { onEvent: wsOnEvent } = useWebSocket()
    unsubscribe = wsOnEvent(onEvent)
  }

  return {
    activeTaskId,
    currentSnapshot,
    setActiveTaskId,
    latestFor,
    clear,
  }
}
