// useContextWindow — tracks the current task's LLM context window snapshot.
//
// The backend emits a `context_window_snapshot` event before every think()
// call, and we only keep the snapshot for the currently active task. This
// avoids accumulating long-term history in the frontend and prevents stale
// snapshots from one task leaking into another.
//
// 快照有两个来源，必须都能填充视图，否则历史/idle 会话会永远停在空态：
//   1. WebSocket `context_window_snapshot` 事件 —— 仅在 Engine 运行时才有。
//   2. REST `GET /api/tasks/:id/context_window` —— 任何时候都可拉取，是
//      历史/idle task 重建上下文窗口的唯一来源（见 cmd/server/api.go
//      handleGetTaskContextWindow：live store 命中则直返，否则从
//      session_messages 重建）。
// `fetchSnapshot` 封装来源 2，供 ContextWindowPanel 自包含地回填，不再
// 依赖"父组件必须绑 @refresh 并自己实现 fetcher"这种易丢的隐式契约。
import { ref } from 'vue'
import { useWebSocket } from './useWebSocket'
import { useTaskStore } from './useTaskStore'
import type { AgentEvent, ContextWindowSnapshotData } from '@/types/events'

// The single snapshot for the currently active task.
const currentSnapshot = ref<ContextWindowSnapshotData | null>(null)
const activeTaskId = ref<string>('')

let unsubscribe: (() => void) | null = null

function onEvent(event: AgentEvent) {
  if (event.type !== 'context_window_snapshot') return
  if (event.task_id !== activeTaskId.value) return

  const data = event.data as unknown as ContextWindowSnapshotData
  if (!data) return

  // Leader / root 快照（sub_task_id 为空，或等于 root task id）才写入
  // currentSnapshot —— currentSnapshot 服务于“All / root”视图。子 agent
  // 实例的快照由 useTaskStore 按 sub_task_id 缓存到 subTaskSnapshots，
  // Panel 通过 subTaskId 读取，这里不应让子 agent 快照覆盖 leader 视图。
  // （早期实现无条件 `currentSnapshot.value = data`，会在多 agent 场景下
  // 用后到的子 agent 快照顶掉 leader 的 root 快照。）
  if (!event.sub_task_id || event.sub_task_id === event.task_id) {
    currentSnapshot.value = data
  }
}

// Re-export subTaskSnapshots from useTaskStore so ContextWindowPanel can read
// any agent instance's snapshot without duplicating state. 同一份 ref 也用于
// fetchSnapshot 写入子任务快照。
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

// 通过 REST 拉取并回填快照。subTaskId 为空时填充 root 视图
// （currentSnapshot），非空时填充该子 agent 实例的 subTaskSnapshots 槽位。
// 失败只记录日志，不抛错 —— 让 UI 保留在空态而非崩溃，与历史 404 行为一致。
async function fetchSnapshot(taskId: string, subTaskId?: string): Promise<void> {
  if (!taskId) return
  const url = subTaskId
    ? `/api/tasks/${encodeURIComponent(taskId)}/context_window?sub_task_id=${encodeURIComponent(subTaskId)}`
    : `/api/tasks/${encodeURIComponent(taskId)}/context_window`
  try {
    const resp = await fetch(url)
    if (!resp.ok) {
      if (resp.status === 404) {
        // 历史 task 无可重建消息时后端返回 404，属正常空态，降级为 warn。
        console.warn('[useContextWindow] snapshot not found for task', taskId, 'subTask', subTaskId)
      } else {
        console.error('[useContextWindow] fetch snapshot failed:', resp.status, resp.statusText)
      }
      return
    }
    const data = (await resp.json()) as ContextWindowSnapshotData
    if (subTaskId) {
      // 子 agent 视图走 subTaskSnapshots（Panel.latest 优先读这里）。
      subTaskSnapshots.value[subTaskId] = data
    } else {
      // root 视图走 currentSnapshot；setSnapshot 内部校验 activeTaskId 仍匹配，
      // 避免用户在请求 in-flight 时切走任务后回填到错误视图。
      setSnapshot(taskId, data)
    }
  } catch (err) {
    console.error('[useContextWindow] network error fetching snapshot:', err)
  }
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
    fetchSnapshot,
  }
}
