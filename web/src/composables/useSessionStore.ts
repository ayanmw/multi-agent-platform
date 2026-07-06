import { ref, computed } from 'vue'

// Session status mirrors backend sessions table status
export type SessionStatus = 'empty' | 'running' | 'completed' | 'failed'

export interface Session {
  id: string
  name: string
  rootTaskId: string | null
  status: SessionStatus
  userInput: string
  totalTokens: number
  createdAt: number
  updatedAt: number
}

export interface CreateSessionResponse {
  session_id: string
  status: SessionStatus
}

const STORAGE_KEY = 'map_sessions'

/** Local cache of sessions loaded from server + created in this client */
const sessions = ref<Session[]>(loadFromStorage())
const activeSessionId = ref<string | null>(null)

function loadFromStorage(): Session[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) {
      return JSON.parse(raw)
    }
  } catch {
    // ignore
  }
  return []
}

function saveToStorage() {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(sessions.value))
  } catch {
    // ignore
  }
}

export function useSessionStore() {
  const activeSession = computed(() =>
    sessions.value.find(s => s.id === activeSessionId.value) || null
  )

  /** Load list of sessions from backend and replace local cache */
  async function loadSessions(): Promise<void> {
    const resp = await fetch('/api/sessions')
    if (!resp.ok) {
      throw new Error(`Failed to load sessions: ${resp.status}`)
    }
    const raw = (await resp.json()) as Array<{
      id: string
      name: string
      root_task_id: string
      status: string
      user_input: string
      total_tokens: number
      created_at: string
      updated_at: string
    }>
    console.log('[useSessionStore] loadSessions raw:', raw.map(s => ({
      id: s.id, name: s.name, root_task_id: s.root_task_id, status: s.status,
    })))
    // Server is the source of truth — replace local cache entirely.
    // This prevents stale localStorage entries from surviving after
    // a session is deleted on the server.
    // Map backend snake_case fields to frontend camelCase fields.
    sessions.value = raw
      .map((s): Session => ({
        id: s.id,
        name: s.name,
        rootTaskId: s.root_task_id || null,
        status: s.status as SessionStatus,
        userInput: s.user_input || '',
        totalTokens: s.total_tokens || 0,
        createdAt: new Date(s.created_at).getTime(),
        updatedAt: new Date(s.updated_at).getTime(),
      }))
      .sort((a, b) => b.updatedAt - a.updatedAt)
    console.log('[useSessionStore] loadSessions mapped:', sessions.value.map(s => ({
      id: s.id, name: s.name, rootTaskId: s.rootTaskId, status: s.status,
    })))
    saveToStorage()
  }

  /** Create a new empty session on the backend */
  async function createSession(name?: string, userInput?: string): Promise<Session> {
    const resp = await fetch('/api/sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: name || '', user_input: userInput || '' }),
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to create session: ${resp.status} ${text}`)
    }
    const data = (await resp.json()) as CreateSessionResponse
    const now = Date.now()
    const session: Session = {
      id: data.session_id,
      name: name || extractSessionName(userInput || ''),
      rootTaskId: null,
      status: data.status,
      userInput: userInput || '',
      totalTokens: 0,
      createdAt: now,
      updatedAt: now,
    }
    sessions.value.unshift(session)
    saveToStorage()
    return session
  }

  /** Set the active session by ID */
  function setActiveSession(id: string | null) {
    activeSessionId.value = id
  }

  /** Delete a session from backend + local cache, returns true if deleted */
  async function deleteSession(id: string): Promise<boolean> {
    const resp = await fetch(`/api/sessions/${id}`, { method: 'DELETE' })
    if (!resp.ok) {
      throw new Error(`Failed to delete session: ${resp.status}`)
    }
    sessions.value = sessions.value.filter(s => s.id !== id)
    if (activeSessionId.value === id) {
      activeSessionId.value = null
    }
    saveToStorage()
    return true
  }

  /** Update session metadata after a task starts/completes */
  function updateSession(sessionId: string, updates: Partial<Session>) {
    const s = sessions.value.find(x => x.id === sessionId)
    if (!s) return
    Object.assign(s, updates)
    s.updatedAt = Date.now()
    saveToStorage()
  }

  return {
    sessions,
    activeSessionId,
    activeSession,
    loadSessions,
    createSession,
    setActiveSession,
    deleteSession,
    updateSession,
  }
}

function extractSessionName(input: string): string {
  if (!input) return 'New Session'
  const clean = input.replace(/\s+/g, ' ').trim()
  return clean.length > 30 ? clean.slice(0, 30) + '...' : clean
}
