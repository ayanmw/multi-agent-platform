// useContextWindow — tracks the current task's LLM context window snapshot.
//
// The backend emits a `context_window_snapshot` event before every think()
// call, and we only keep the snapshot for the currently active task. This
// avoids accumulating long-term history in the frontend and prevents stale
// snapshots from one task leaking into another.
import { ref } from 'vue'
import { useWebSocket } from './useWebSocket'
import { useTaskStore } from './useTaskStore'
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

// Re-export subTaskSnapshots from useTaskStore so ContextWindowPanel can read
// any agent instance's snapshot without duplicating state.
const { subTaskSnapshots } = useTaskStore()

// 设置当前追踪的 task ID；若变化则清空旧快照，防止跨任务污染。
function setActiveTaskId(taskId: string): void {
  if (activeTaskId.value === taskId) {
    return
  }
  activeTaskId.value = taskId
  currentSnapshot.value = null
}

// 用 REST 获取的快照回填当前快照；仅当 taskId 与当前 active 一致时才生效。
function setSnapshot(taskId: string, data: ContextWindowSnapshotData): void {
  if (taskId && activeTaskId.value === taskId) {
    currentSnapshot.value = data
  }
}

// 清空当前快照（例如切换 session 时调用）。
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
    subTaskSnapshots,
    setActiveTaskId,
    setSnapshot,
    clear,
  }
}
