import { ref, watch } from 'vue'

export interface RecentModification {
  key: string
  path: string
  timestamp: number
  success: boolean
  bytes?: number
  sessionId?: string
}

const STORAGE_KEY = 'map_recent_mods'
const MAX_ITEMS = 200

/** Local reactive cache — survives page refresh via localStorage */
export const items = ref<RecentModification[]>([])
const _listeners: Array<(v: boolean) => void> = []

watch(items, val => {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(val.slice(0, MAX_ITEMS)))
  } catch { /* quota exceeded — silently drop */ }
}, { deep: true })

function loadFromStorage(): RecentModification[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) return JSON.parse(raw) as RecentModification[]
  } catch { /* ignore */ }
  return []
}

/**
 * hasTodayItems — pure function, no state.
 */
export function hasTodayItems(): boolean {
  const now = new Date()
  return items.value.some(item => {
    const d = new Date(item.timestamp)
    return d.getFullYear() === now.getFullYear()
      && d.getMonth() === now.getMonth()
      && d.getDate() === now.getDate()
  })
}

/**
 * useRecentMods — shared "最近修改" log.
 *
 *   const { items, show, toggle, addItem, clear } = useRecentMods()
 *   // or use the exported items directly:
 *   // items: bind to v-model / :items directly (module-level ref)
 */
export function useRecentMods() {
  items.value = loadFromStorage()

  function addItem(entry: Omit<RecentModification, 'key' | 'timestamp'>) {
    const key = `${entry.path}|${Date.now()}|${Math.random().toString(36).slice(2, 8)}`
    items.value.unshift({
      ...entry,
      key,
      timestamp: Date.now(),
    })
    if (items.value.length > MAX_ITEMS) {
      items.value.length = MAX_ITEMS
    }
  }

  function clear() {
    items.value = []
  }

  function toggle() {
    // We allow toggling even when empty (closes if open, no-op if closed)
    _listeners.forEach(fn => fn(true))
  }

  function registerListener(fn: (v: boolean) => void) {
    _listeners.push(fn)
    return () => {
      const idx = _listeners.indexOf(fn)
      if (idx >= 0) _listeners.splice(idx, 1)
    }
  }

  return {
    items,
    show: toggle,
    toggle,
    addItem,
    clear,
    hasTodayItems,
    registerListener,
  }
}
