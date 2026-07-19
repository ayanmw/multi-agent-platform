// useKeyboard — global keyboard shortcut composable
//
// Design rationale:
//   - Shortcuts use Ctrl+Shift+* to prevent accidental triggers
//   - All shortcuts are listed in a tips panel accessible via "?" button
//   - Shortcuts only fire when the agent is running (except help toggle)
//   - Each shortcut can be individually disabled/enabled
//
// Shortcuts:
//   Ctrl+Shift+C  → Cancel current task
//   Ctrl+Shift+P  → Pause/Resume current task
//   ?             → Toggle keyboard tips panel (always active)
//
// Usage:
//   const { isRunning } = useKeyboard({ onCancel, onPause, onResume })
//   // Set isRunning ref to enable/disable task shortcuts
import { ref, onMounted, onUnmounted } from 'vue'

export interface KeyboardActions {
  onCancel: () => void
  onPause: () => void
  onResume: () => void
}

export interface KeyboardShortcut {
  keys: string
  description: string
  active: string // when this shortcut is active
}

export const SHORTCUTS: KeyboardShortcut[] = [
  { keys: 'Ctrl+Shift+C', description: 'Cancel current task', active: 'When task is running' },
  { keys: 'Ctrl+Shift+P', description: 'Pause / Resume task', active: 'When task is running' },
  { keys: '?', description: 'Toggle keyboard shortcuts panel', active: 'Always' },
]

export function useKeyboard(actions: KeyboardActions) {
  /** Whether a task is currently running (controls shortcut availability) */
  const isRunning = ref(false)
  /** Whether to show the keyboard shortcuts panel */
  const showTips = ref(false)

  function handleKeydown(e: KeyboardEvent) {
    // Ignore when typing in input/textarea
    const target = e.target as HTMLElement
    if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable) {
      // But allow "?" in non-input contexts — check if it's a form element
      if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA') {
        return
      }
    }

    // "?" key — toggle tips panel (always active)
    if (e.key === '?' && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault()
      showTips.value = !showTips.value
      return
    }

    // Task shortcuts — only when running
    if (!isRunning.value) return

    // Ctrl+Shift+C — Cancel
    if (e.key === 'C' && e.ctrlKey && e.shiftKey && !e.metaKey && !e.altKey) {
      e.preventDefault()
      actions.onCancel()
      return
    }

    // Ctrl+Shift+P — Pause / Resume
    if (e.key === 'P' && e.ctrlKey && e.shiftKey && !e.metaKey && !e.altKey) {
      e.preventDefault()
      actions.onPause()
      return
    }
  }

  onMounted(() => {
    window.addEventListener('keydown', handleKeydown)
  })

  onUnmounted(() => {
    window.removeEventListener('keydown', handleKeydown)
  })

  return {
    isRunning,
    showTips,
  }
}