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
   */
  function sendControl(msg: { action: string; task_id: string; agent_id: string; [key: string]: unknown }) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(msg))
    } else {
      console.warn('[WS] Cannot send control message: not connected')
    }
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