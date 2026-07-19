// useMemoryStore — reactive memory state management
//
// Manages memory browsing, creation, editing, embedding, scope changes, and
// deletion against the backend /api/memories APIs. Stats can be loaded from
// the backend stats endpoint or computed locally from the filtered list.
//
// The backend API:
//   GET    /api/memories?project=default&scope=&tier=&type=&status=&limit=&offset=
//   POST   /api/memories              — create memory
//   GET    /api/memories/{id}
//   PUT    /api/memories/{id}         — update memory (content, confidence, status)
//   DELETE /api/memories/{id}
//   PUT    /api/memories/{id}/scope   — update scope (body: {scope, session_id})
//   POST   /api/memories/{id}/embed   — trigger embedding generation
//   GET    /api/memories/stats        — backend aggregation
//   GET    /api/memories/recall?query=xxx&max=10&project=default
//   POST   /api/memories/promote      — promote memory tier
//
// Memories are organized by scope (session/project/global), tier
// (consolidated/semantic), type (preference/rule/fact/lesson/reflection/session_summary),
// and status. This store provides reactive filtering and full CRUD operations
// for Phase 6-F memory management UI.
import { ref, computed, reactive } from 'vue'

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
  status: string      // active | archived | pending | deprecated
  source_task_ids: string[]
  source_event_ids: string[]
  promotion_reason: string
  access_count: number
  last_accessed: string | null
  last_reviewed: string | null
  created_at: string
  updated_at: string
  // Optional embedding details from backend
  embedding_model?: string
  embedding_dimensions?: number
  embedding_updated_at?: string
  // Optional backend stats aggregation
  total?: number
}

/** Payload for creating a new memory */
export interface CreateMemoryPayload {
  project_id?: string
  scope: string
  type: string
  tier: string
  content: string
  confidence: number
}

/** Payload for updating an existing memory */
export interface UpdateMemoryPayload {
  content?: string
  confidence?: number
  status?: string
}

/** Filter state for memory queries */
export interface MemoryFilter {
  scope: string      // '' = all, 'session' | 'project' | 'global'
  project: string     // project ID
  tier: string       // '' = all, 'consolidated' | 'semantic'
  type: string       // '' = all, or specific memory type
  status: string     // '' = all, or specific status
  limit: number
  offset: number
}

/** Pagination state */
export interface MemoryPagination {
  limit: number
  offset: number
  hasNext: boolean
  hasPrev: boolean
  total: number
}

/** Memory statistics — mirrors backend /api/memories/stats response */
export interface MemoryStats {
  total: number
  byScope: Record<string, number>
  byTier: Record<string, number>
  byType: Record<string, number>
  topAccessed?: Array<{ id: string; content: string; access_count: number }>
}

/** Singleton state shared across all consumers */
const memories = ref<MemoryItem[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const selectedMemoryId = ref<string | null>(null)

const filter = reactive<MemoryFilter>({
  scope: '',
  project: 'default',
  tier: '',
  type: '',
  status: '',
  limit: 20,
  offset: 0,
})

const pagination = reactive<MemoryPagination>({
  limit: filter.limit,
  offset: filter.offset,
  hasNext: false,
  hasPrev: false,
  total: 0,
})

const stats = ref<MemoryStats | null>(null)

export function useMemoryStore() {
  /** Computed memory statistics from currently loaded items (fallback when backend stats unavailable) */
  const localStats = computed<MemoryStats>(() => {
    const s: MemoryStats = {
      total: memories.value.length,
      byScope: {},
      byTier: {},
      byType: {},
      topAccessed: memories.value
        .slice()
        .sort((a, b) => (b.access_count || 0) - (a.access_count || 0))
        .slice(0, 5)
        .map(m => ({ id: m.id, content: m.content, access_count: m.access_count })),
    }
    for (const m of memories.value) {
      s.byScope[m.scope] = (s.byScope[m.scope] || 0) + 1
      s.byTier[m.tier] = (s.byTier[m.tier] || 0) + 1
      s.byType[m.type] = (s.byType[m.type] || 0) + 1
    }
    return s
  })

  /** Select a memory by id (used for scroll-to / highlight from event timeline) */
  function selectMemory(id: string | null) {
    selectedMemoryId.value = id
    // When a memory is selected from the timeline, expand it too
    if (id) {
      const mem = memories.value.find(m => m.id === id)
      if (mem) {
        // Ensure the item is visible by resetting pagination if needed
        // (simpler: just keep selection highlighted)
      }
    }
  }

  /** Build query params from current filter */
  function buildQueryParams(): URLSearchParams {
    const params = new URLSearchParams()
    params.set('project', filter.project)
    if (filter.scope) params.set('scope', filter.scope)
    if (filter.tier) params.set('tier', filter.tier)
    if (filter.type) params.set('type', filter.type)
    if (filter.status) params.set('status', filter.status)
    params.set('limit', String(filter.limit))
    params.set('offset', String(filter.offset))
    return params
  }

  /** Load memories from the backend with current filter + pagination */
  async function loadMemories(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const params = buildQueryParams()
      const resp = await fetch(`/api/memories?${params.toString()}`)
      if (!resp.ok) throw new Error(`Failed to load memories: ${resp.status}`)
      const data = (await resp.json()) as MemoryItem[] | { items: MemoryItem[]; total?: number }
      if (Array.isArray(data)) {
        memories.value = data
        pagination.total = data.length
      } else {
        memories.value = data.items || []
        pagination.total = data.total ?? memories.value.length
      }
      pagination.offset = filter.offset
      pagination.limit = filter.limit
      pagination.hasPrev = filter.offset > 0
      pagination.hasNext = filter.offset + memories.value.length < pagination.total
      // Sync selection if the selected item is no longer in the list
      if (selectedMemoryId.value && !memories.value.find(m => m.id === selectedMemoryId.value)) {
        selectedMemoryId.value = null
      }
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
    } finally {
      loading.value = false
    }
  }

  /** Create a new memory and prepend it to the local list */
  async function createMemory(payload: CreateMemoryPayload): Promise<MemoryItem> {
    error.value = null
    try {
      const resp = await fetch('/api/memories', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      })
      if (!resp.ok) throw new Error(`Failed to create memory: ${resp.status}`)
      const item = (await resp.json()) as MemoryItem
      memories.value.unshift(item)
      if (pagination.total !== undefined) pagination.total += 1
      return item
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
    }
  }

  /** Update an existing memory's content/confidence/status and replace locally */
  async function updateMemory(id: string, payload: UpdateMemoryPayload): Promise<MemoryItem> {
    error.value = null
    try {
      const resp = await fetch(`/api/memories/${id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      })
      if (!resp.ok) throw new Error(`Failed to update memory: ${resp.status}`)
      const item = (await resp.json()) as MemoryItem
      const idx = memories.value.findIndex(m => m.id === id)
      if (idx >= 0) {
        memories.value[idx] = item
      }
      return item
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
    }
  }

  /** Trigger embedding generation for a memory */
  async function embedMemory(id: string): Promise<MemoryItem> {
    error.value = null
    try {
      const resp = await fetch(`/api/memories/${id}/embed`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
      })
      if (!resp.ok) throw new Error(`Failed to embed memory: ${resp.status}`)
      const item = (await resp.json()) as MemoryItem
      const idx = memories.value.findIndex(m => m.id === id)
      if (idx >= 0) {
        memories.value[idx] = item
      }
      return item
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
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

  /** Load backend memory statistics */
  async function loadStats(): Promise<MemoryStats> {
    error.value = null
    try {
      const params = new URLSearchParams()
      if (filter.project) params.set('project', filter.project)
      const resp = await fetch(`/api/memories/stats?${params.toString()}`)
      if (!resp.ok) throw new Error(`Failed to load stats: ${resp.status}`)
      const data = (await resp.json()) as MemoryStats
      stats.value = data
      return data
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      // Fallback to local stats so consumers always have something
      stats.value = localStats.value
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
      if (selectedMemoryId.value === id) selectedMemoryId.value = null
      if (pagination.total > 0) pagination.total -= 1
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
    }
  }

  /** Paginate forward */
  async function nextPage(): Promise<void> {
    if (!pagination.hasNext) return
    filter.offset += filter.limit
    await loadMemories()
  }

  /** Paginate backward */
  async function prevPage(): Promise<void> {
    if (!pagination.hasPrev) return
    filter.offset = Math.max(0, filter.offset - filter.limit)
    await loadMemories()
  }

  return {
    memories,
    loading,
    error,
    filter,
    stats,
    localStats,
    pagination,
    selectedMemoryId,
    loadMemories,
    createMemory,
    updateMemory,
    embedMemory,
    updateScope,
    loadStats,
    deleteMemory,
    selectMemory,
    nextPage,
    prevPage,
  }
}
