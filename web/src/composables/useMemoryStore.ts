// useMemoryStore — reactive memory state management
//
// Manages memory browsing, scope changes, and deletion against the backend
// /api/memories and /api/memories/{id}/scope APIs.
//
// The backend API:
//   GET    /api/memories?scope=session&project=default&tier=consolidated
//   PUT    /api/memories/{id}/scope  — update memory scope (body: {scope, session_id})
//   DELETE /api/memories/{id}        — delete memory by ID
//
// Memories are organized by scope (session/project/global) and tier
// (consolidated/semantic). This store provides reactive filtering and
// CRUD operations for the MemoryBrowser component.
import { ref, computed } from 'vue'

/** Memory record returned by GET /api/memories (matches backend MemoryRecord) */
export interface MemoryItem {
  id: string
  project_id: string
  scope: string       // session | project | global
  session_id?: string
  type: string        // preference | rule | fact | lesson | reflection | session_summary
  tier: string        // consolidated | semantic
  content: string
  confidence: number
  status: string
  source_task_ids: string[]
  source_event_ids: string[]
  promotion_reason: string
  access_count: number
  last_accessed: string | null
  last_reviewed: string | null
  created_at: string
  updated_at: string
}

/** Filter state for memory queries */
export interface MemoryFilter {
  scope: string      // '' = all, 'session' | 'project' | 'global'
  project: string     // project ID
  tier: string       // '' = all, 'consolidated' | 'semantic'
}

/** Memory statistics computed from the loaded list */
export interface MemoryStats {
  total: number
  byScope: Record<string, number>
  byTier: Record<string, number>
  byType: Record<string, number>
}

/** Singleton state shared across all consumers */
const memories = ref<MemoryItem[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const filter = ref<MemoryFilter>({
  scope: '',
  project: 'default',
  tier: '',
})

export function useMemoryStore() {
  /** Computed memory statistics */
  const stats = computed<MemoryStats>(() => {
    const s: MemoryStats = {
      total: memories.value.length,
      byScope: {},
      byTier: {},
      byType: {},
    }
    for (const m of memories.value) {
      s.byScope[m.scope] = (s.byScope[m.scope] || 0) + 1
      s.byTier[m.tier] = (s.byTier[m.tier] || 0) + 1
      s.byType[m.type] = (s.byType[m.type] || 0) + 1
    }
    return s
  })

  /** Load memories from the backend with current filter */
  async function loadMemories(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const params = new URLSearchParams()
      params.set('project', filter.value.project)
      if (filter.value.scope) params.set('scope', filter.value.scope)
      if (filter.value.tier) params.set('tier', filter.value.tier)

      const resp = await fetch(`/api/memories?${params.toString()}`)
      if (!resp.ok) throw new Error(`Failed to load memories: ${resp.status}`)
      memories.value = (await resp.json()) as MemoryItem[]
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
    } finally {
      loading.value = false
    }
  }

  /** Update a memory's scope (session → project → global) */
  async function updateScope(id: string, scope: string, sessionId?: string): Promise<void> {
    error.value = null
    try {
      const resp = await fetch(`/api/memories/${id}/scope`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ scope, session_id: sessionId || '' }),
      })
      if (!resp.ok) throw new Error(`Failed to update scope: ${resp.status}`)
      // Update local state
      const mem = memories.value.find(m => m.id === id)
      if (mem) {
        mem.scope = scope
        if (sessionId !== undefined) mem.session_id = sessionId
      }
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
    }
  }

  /** Delete a memory by ID */
  async function deleteMemory(id: string): Promise<void> {
    error.value = null
    try {
      const resp = await fetch(`/api/memories/${id}`, { method: 'DELETE' })
      if (!resp.ok) throw new Error(`Failed to delete memory: ${resp.status}`)
      memories.value = memories.value.filter(m => m.id !== id)
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
    }
  }

  return {
    memories,
    loading,
    error,
    filter,
    stats,
    loadMemories,
    updateScope,
    deleteMemory,
  }
}