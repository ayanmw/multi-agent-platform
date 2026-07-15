// useContextWindow — tracks LLM context window snapshots from the backend
//
// The backend emits a `context_window_snapshot` event before every think()
// call. Each snapshot contains:
//   - the model name and its max context window
//   - an estimate of the total tokens consumed by the current messages
//   - per-message token estimates and usage ratios
//   - the full content of every system/user/assistant/tool message
//
// This composable caps history so long-running tasks don't leak memory,
// and exposes the latest snapshot for reactive UI rendering.
import { ref, computed } from 'vue'
import { useWebSocket } from './useWebSocket'
import type { AgentEvent, ContextWindowSnapshotData } from '../types/events'

const MAX_SNAPSHOTS = 50

/** Accumulated context window snapshots, oldest first */
const snapshots = ref<ContextWindowSnapshotData[]>([])

/** The most recent snapshot, or null if none has been received yet */
const latest = computed<ContextWindowSnapshotData | null>(() => {
  if (snapshots.value.length === 0) return null
  return snapshots.value[snapshots.value.length - 1]
})

let unsubscribe: (() => void) | null = null

function onEvent(event: AgentEvent) {
  if (event.type !== 'context_window_snapshot') return
  const data = event.data as unknown as ContextWindowSnapshotData
  if (!data) return

  snapshots.value.push(data)
  if (snapshots.value.length > MAX_SNAPSHOTS) {
    snapshots.value.shift()
  }
}

/** Reset the snapshot history */
function clear(): void {
  snapshots.value = []
}

/** Register the singleton listener and return reactive snapshot state */
export function useContextWindow() {
  if (!unsubscribe) {
    const { onEvent: wsOnEvent } = useWebSocket()
    unsubscribe = wsOnEvent(onEvent)
  }

  return {
    snapshots,
    latest,
    clear,
  }
}
