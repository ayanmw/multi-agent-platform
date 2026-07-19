import { ref } from 'vue'

/**
 * useSessionFiles — session workspace 文件浏览器数据源
 *
 * 对应后端 GET /api/sessions/{id}/workspace-tree?path=<rel>。只列出单层节点，
 * 目录由前端按需展开（再次请求 path=<rel>），避免一次性返回整棵树。
 *
 * 设计取舍：
 *  - 状态按 sessionId + path 缓存已展开目录的子节点，切换 session 时整体重置。
 *  - loading/err 状态驱动文件树骨架与错误占位；err 为空字符串表示无错误。
 *  - 不在这里做任何 path 拼接校验，相对路径直接透传给后端，由后端做 traversal 防护。
 */
export interface SessionFileNode {
  name: string
  relative_path: string
  is_dir: boolean
  size: number
  mod_time: string
}

interface DirEntry {
  nodes: SessionFileNode[]
  loaded: boolean
  loading: boolean
  err: string
}

/** 缓存：sessionId -> relativePath -> 目录条目。空字符串 key 代表 workspace 根。 */
const cache = ref<Record<string, Record<string, DirEntry>>>({})

/** 当前正在浏览的 session id，用于切换时整体清空缓存。 */
const activeId = ref<string>('')

function ensureDir(sessionId: string, path: string): DirEntry {
  if (!cache.value[sessionId]) cache.value[sessionId] = {}
  if (!cache.value[sessionId][path]) {
    cache.value[sessionId][path] = { nodes: [], loaded: false, loading: false, err: '' }
  }
  return cache.value[sessionId][path]
}

/** 列出指定 session workspace 下相对子目录的单层节点；同一 session+path 只请求一次。 */
async function listDir(sessionId: string, path: string): Promise<void> {
  if (!sessionId) return
  const dir = ensureDir(sessionId, path)
  if (dir.loaded || dir.loading) return
  dir.loading = true
  dir.err = ''
  try {
    const url = `/api/sessions/${encodeURIComponent(sessionId)}/workspace-tree?path=${encodeURIComponent(path)}`
    const resp = await fetch(url)
    if (!resp.ok) {
      throw new Error(`Failed to list workspace: ${resp.status}`)
    }
    const data = (await resp.json()) as { entries: SessionFileNode[]; workspace_dir: string }
    dir.nodes = data.entries || []
    dir.loaded = true
  } catch (e) {
    dir.err = e instanceof Error ? e.message : String(e)
  } finally {
    dir.loading = false
  }
}

/** 强制重新拉取某个目录（例如 write_file 后刷新）。 */
async function refreshDir(sessionId: string, path: string): Promise<void> {
  if (!sessionId) return
  const dir = ensureDir(sessionId, path)
  dir.loaded = false
  await listDir(sessionId, path)
}

/** 切换当前浏览的 session；与之前不同则清空旧 session 的缓存。 */
function setActiveSession(sessionId: string): void {
  if (activeId.value === sessionId) return
  activeId.value = sessionId
  // 释放旧 session 的缓存，避免内存随切换累积。
  if (cache.value[sessionId]) {
    // 仅保留当前 session，其余丢弃。
    const keep = cache.value[sessionId]
    cache.value = { [sessionId]: keep }
  }
}

/** 取某目录的缓存条目（未加载时返回空骨架，便于模板统一渲染）。 */
function getDir(sessionId: string, path: string): DirEntry {
  return ensureDir(sessionId, path)
}

/** 拼接可在新标签打开的文件 URL：复用 /s/{session_id}/{relative_path} 静态服务。 */
function fileUrl(sessionId: string, relativePath: string): string {
  return `/s/${encodeURIComponent(sessionId)}/${relativePath}`
}

export function useSessionFiles() {
  return {
    cache,
    activeId,
    listDir,
    refreshDir,
    setActiveSession,
    getDir,
    fileUrl,
  }
}
