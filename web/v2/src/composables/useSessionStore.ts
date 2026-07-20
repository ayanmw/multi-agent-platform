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
  durationMs: number
  projectId: string
  turnCount: number
  createdAt: number
  updatedAt: number
  workspaceDir: string
  workspaceAuto: boolean
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

  /** Load ALL sessions across every project and replace local cache.
   *  不再按 project 过滤——一次加载所有 session，配合 SessionDock 的
   *  "多组同时展开 + 折叠状态手动持久化"策略，让用户刷新后仍能看到各 project
   *  的 session 而无需逐个点击加载。 */
  async function loadSessions(projectId?: string): Promise<void> {
    // 保留 projectId 入参以维持调用方签名兼容，但忽略它：始终加载全部 session。
    void projectId
    const url = '/api/sessions'
    const resp = await fetch(url)
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
      duration_ms: number
      project_id: string
      turn_count: number
      created_at: string
      updated_at: string
      workspace_dir: string
      workspace_auto: boolean
    }>
    // Server is the source of truth — replace local cache entirely.
    // This prevents stale localStorage entries from surviving after
    // a session is deleted on the server.
    // Map backend snake_case fields to frontend camelCase fields.
    // 排序规则：按创建时间降序（新建在最上）。不按 updatedAt 排序，避免点击/更新
    // session 时它被"挪到顶部"导致列表顺序反复跳动——这是用户明确反馈的痛点。
    sessions.value = raw
      .map((s): Session => ({
        id: s.id,
        name: s.name,
        rootTaskId: s.root_task_id || null,
        status: s.status as SessionStatus,
        userInput: s.user_input || '',
        totalTokens: s.total_tokens || 0,
        durationMs: s.duration_ms || 0,
        projectId: s.project_id || 'default',
        turnCount: s.turn_count || 0,
        createdAt: new Date(s.created_at).getTime(),
        updatedAt: new Date(s.updated_at).getTime(),
        workspaceDir: s.workspace_dir || '',
        workspaceAuto: s.workspace_auto !== false,
      }))
      .sort((a, b) => b.createdAt - a.createdAt)
    saveToStorage()
  }

  /** Create a new empty session on the backend.
   *  projectId is optional — defaults to the active project or 'default'.
   *  workspaceDir is optional — if provided, use as the session's working directory;
   *  if empty, the server auto-generates one. */
  async function createSession(name?: string, userInput?: string, projectId?: string, workspaceDir?: string): Promise<Session> {
    const body: Record<string, string> = { name: name || '', user_input: userInput || '' }
    if (projectId) {
      body.project_id = projectId
    }
    if (workspaceDir) {
      body.workspace_dir = workspaceDir
    }
    const resp = await fetch('/api/sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
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
      durationMs: 0,
      projectId: projectId || 'default',
      turnCount: 0,
      createdAt: now,
      updatedAt: now,
      workspaceDir: workspaceDir || '',
      workspaceAuto: !workspaceDir,
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

  /** Refresh a single session from the backend by id. */
  async function refreshSession(sessionId: string) {
    try {
      const resp = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}`)
      if (!resp.ok) return
      const data = (await resp.json()) as {
        session: {
          id: string
          name: string
          root_task_id: string
          status: string
          user_input: string
          total_tokens: number
          duration_ms: number
          project_id: string
          turn_count: number
          created_at: string
          updated_at: string
          workspace_dir: string
          workspace_auto: boolean
        }
      }
      const s = data.session
      updateSession(sessionId, {
        name: s.name,
        rootTaskId: s.root_task_id || null,
        status: s.status as SessionStatus,
        userInput: s.user_input || '',
        totalTokens: s.total_tokens || 0,
        durationMs: s.duration_ms || 0,
        projectId: s.project_id || 'default',
        turnCount: s.turn_count || 0,
        workspaceDir: s.workspace_dir || '',
        workspaceAuto: s.workspace_auto !== false,
      })
    } catch (err) {
      console.error('[useSessionStore] refreshSession failed:', err)
    }
  }

  /** Rename a session and persist the change to the backend.
   *  On success the local cache is updated so the UI reflects the
   *  new name immediately without a full reload. */
  async function renameSession(id: string, name: string): Promise<Session> {
    const trimmed = name.trim()
    if (!trimmed) {
      throw new Error('Session name cannot be empty')
    }
    return updateSessionFields(id, { name: trimmed })
  }

  /** 更新 session 的可编辑元数据（name + workspace_dir）。
   *
   *  workspaceDir 语义：
   *    - undefined：不修改 workspace（仅重命名，走旧 API 路径，body 只含 name）
   *    - 非空字符串：切换到自定义 workspace，后端会确保目录存在
   *    - 空字符串：清空 workspace，回退到 auto / project working_directory
   *
   *  返回更新后的 session，并同步本地缓存。 */
  async function updateSessionFields(
    id: string,
    opts: { name: string; workspaceDir?: string },
  ): Promise<Session> {
    const trimmedName = opts.name.trim()
    if (!trimmedName) {
      throw new Error('Session name cannot be empty')
    }
    // 仅当显式提供 workspaceDir（包括空串）时才带上 workspace_dir 字段，
    // 让后端区分"未提供（保留旧值）"与"显式清空"。
    const body: Record<string, unknown> = { name: trimmedName }
    if (opts.workspaceDir !== undefined) {
      body.workspace_dir = opts.workspaceDir
    }
    const resp = await fetch(`/api/sessions/${encodeURIComponent(id)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to update session: ${resp.status} ${text}`)
    }
    const s = (await resp.json()) as {
      id: string
      name: string
      root_task_id: string
      status: string
      user_input: string
      total_tokens: number
      duration_ms: number
      project_id: string
      turn_count: number
      created_at: string
      updated_at: string
      workspace_dir: string
      workspace_auto: boolean
    }
    const existing = sessions.value.find(x => x.id === id)
    const updates: Partial<Session> = {
      name: s.name,
      rootTaskId: s.root_task_id || null,
      status: s.status as SessionStatus,
      userInput: s.user_input || '',
      totalTokens: s.total_tokens || 0,
      durationMs: s.duration_ms || 0,
      projectId: s.project_id || 'default',
      turnCount: s.turn_count || 0,
      workspaceDir: s.workspace_dir || '',
      workspaceAuto: s.workspace_auto !== false,
      createdAt: new Date(s.created_at).getTime(),
      updatedAt: new Date(s.updated_at).getTime(),
    }
    if (!existing) {
      throw new Error('Session not found in local cache')
    }
    Object.assign(existing, updates)
    saveToStorage()
    return existing
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
    refreshSession,
    renameSession,
    updateSessionFields,
  }
}

function extractSessionName(input: string): string {
  if (!input) return 'New Session'
  const clean = input.replace(/\s+/g, ' ').trim()
  return clean.length > 30 ? clean.slice(0, 30) + '...' : clean
}
