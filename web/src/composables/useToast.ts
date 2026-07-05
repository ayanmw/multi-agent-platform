// useToast — global toast notification composable
//
// Usage:
//   const { toasts, showError, showInfo } = useToast()
//   showError('Failed to start task: network error')
//   showInfo('Task completed successfully')
//
// Design rationale:
//   - Module-level state so toasts persist across component lifecycles
//   - Auto-incrementing ID for unique keys
//   - Max 5 toasts — oldest auto-dismissed when exceeded
//   - Each toast auto-dismisses after 5 seconds
import { ref } from 'vue'
import type { ToastItem } from '../components/Toast.vue'

const toasts = ref<ToastItem[]>([])
let nextId = 0
const MAX_TOASTS = 5
const AUTO_DISMISS_MS = 5000

export function useToast() {
  function addToast(message: string, type: 'error' | 'info') {
    const id = nextId++
    toasts.value.push({ id, message, type })

    // Trim excess toasts
    while (toasts.value.length > MAX_TOASTS) {
      toasts.value.shift()
    }

    // Auto-dismiss after 5 seconds
    setTimeout(() => {
      dismissToast(id)
    }, AUTO_DISMISS_MS)
  }

  function dismissToast(id: number) {
    const idx = toasts.value.findIndex(t => t.id === id)
    if (idx !== -1) {
      toasts.value.splice(idx, 1)
    }
  }

  function showError(message: string) {
    addToast(message, 'error')
  }

  function showInfo(message: string) {
    addToast(message, 'info')
  }

  return {
    toasts,
    showError,
    showInfo,
    dismissToast,
  }
}