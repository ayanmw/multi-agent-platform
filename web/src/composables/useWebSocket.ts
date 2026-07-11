// useWebSocket — WebSocket connection management composable
//
// Lifecycle:
//   connect() → ws.onopen → status='connected' → ws.onmessage → parse events
//   disconnect() → ws.close() → status='disconnected'
//   Auto-reconnect: exponential backoff (1s → 2s → 4s → ... → max 30s)
//
// Design rationale:
//   - Single WebSocket instance per app (shared via module-level ref)
//   - Event parsing is done here, not in the store, to keep the store pure
//   - Reconnect logic is built-in because agent tasks can run for minutes
//   - Dev server proxies /ws to the Go backend, so the URL is relative

import { ref, onUnmounted } from 'vue'
import type { AgentEvent } from '../types/events'

/** Connection status */
export type WSStatus = 'connecting' | 'connected' | 'disconnected'

/** Callback for incoming events — called before the store processes them */
type EventCallback = (event: AgentEvent) => void

// Module-level state — shared across all components that use this composable
const status = ref<WSStatus>('disconnected')
const listeners = new Set<EventCallback>()
let ws: WebSocket | null = null
let reconnectTimer: ReturnType<typeof setTimeout> | null = null
let reconnectDelay = 1000 // starts at 1s, exponential backoff to 30s

// F2 修复：未连接期间的控制消息队列。
// 当 WS 尚未 OPEN（初次握手或断线重连过程中），用户在前端点击
// cancel/approve/deny 等控制按钮时，sendControl 之前会静默丢失消息，
// 导致任务无法取消、审批永远超时。这里把消息入队，连接一建立就 flush。
const pendingControlQueue: Array<string> = []
const MAX_QUEUE = 100

/** Flush all queued control messages after the socket becomes open. */
function flushControlQueue() {
  while (pendingControlQueue.length > 0) {
    const msg = pendingControlQueue.shift()!
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(msg)
    } else {
      // socket closed again during flush — re-queue and stop
      pendingControlQueue.unshift(msg)
      break
    }
  }
}

export function useWebSocket() {
  /**
   * Connect to the WebSocket endpoint.
   * Uses relative URL so it works in dev (proxied) and production (same origin).
   */
  function connect() {
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
      return // already connected or connecting
    }

    status.value = 'connecting'

    // Build WebSocket URL from current location
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsURL = `${protocol}//${window.location.host}/ws`

    ws = new WebSocket(wsURL)

    ws.onopen = () => {
      status.value = 'connected'
      reconnectDelay = 1000 // reset backoff on successful connection
      console.log('[WS] Connected')
      // F2: 重连后自动 flush 在断线期间入队的控制消息
      flushControlQueue()
    }

    ws.onmessage = (evt: MessageEvent) => {
      try {
        const msg: AgentEvent = JSON.parse(evt.data as string)
        // Notify all listeners (typically the task store)
        for (const listener of listeners) {
          listener(msg)
        }
      } catch (err) {
        console.error('[WS] Failed to parse message:', err)
      }
    }

    ws.onclose = () => {
      status.value = 'disconnected'
      ws = null
      console.log('[WS] Disconnected')
      // Auto-reconnect with exponential backoff
      scheduleReconnect()
    }

    ws.onerror = (err: Event) => {
      console.error('[WS] Error:', err)
      // onclose will fire after onerror, triggering reconnect
    }
  }

  /** Schedule a reconnect attempt with exponential backoff */
  function scheduleReconnect() {
    if (reconnectTimer) return // already scheduled

    reconnectTimer = setTimeout(() => {
      reconnectTimer = null
      console.log(`[WS] Reconnecting (delay: ${reconnectDelay}ms)...`)
      connect()
      // Exponential backoff: double the delay, cap at 30s
      reconnectDelay = Math.min(reconnectDelay * 2, 30000)
    }, reconnectDelay)
  }

  /** Disconnect and stop reconnecting */
  function disconnect() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    if (ws) {
      ws.close()
      ws = null
    }
    status.value = 'disconnected'
  }

  /**
   * Register a callback for incoming events.
   * Returns an unsubscribe function.
   */
  function onEvent(callback: EventCallback): () => void {
    listeners.add(callback)
    return () => {
      listeners.delete(callback)
    }
  }

  /**
   * Send a control message to the server (pause / resume / cancel / approve / deny).
   * Extra fields (e.g. approval_id) are spread into the message body.
   *
   * F2 修复：如果 WS 未处于 OPEN 状态（连接中 / 断线 / 重连等待），
   * 将消息入队而不是静默丢弃。连接一建立（ws.onopen）就会 flush 队列，
   * 保证用户在断线窗口内点击的 cancel / approve / deny 也能送达后端。
   * 队列上限 MAX_QUEUE，超出后丢弃最旧的消息，避免无限增长。
   */
  function sendControl(msg: { action: string; task_id: string; agent_id: string; [key: string]: unknown }) {
    const payload = JSON.stringify(msg)
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(payload)
      return
    }
    // WS 未就绪：入队等待重连后 flush
    if (pendingControlQueue.length >= MAX_QUEUE) {
      pendingControlQueue.shift()
    }
    pendingControlQueue.push(payload)
    console.warn(`[WS] Not connected — queued control message (action=${msg.action}, queue=${pendingControlQueue.length})`)
  }

  // Cleanup on component unmount (only if the using component is destroyed)
  // Note: this is a no-op for the root App component, but useful for sub-components
  onUnmounted(() => {
    // Don't disconnect — the connection is shared across the app
    // Individual component cleanup is handled by the returned unsubscribe
  })

  return {
    status,
    connect,
    disconnect,
    onEvent,
    sendControl,
  }
}