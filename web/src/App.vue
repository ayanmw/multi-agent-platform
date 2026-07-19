<!-- App.vue — root layout component
     Structure:
       ┌──────────────────────────────────────────────────────────────┐
       │  Sidebar (280px): Project selector + grouped Session list    │
       │    ├─ Projects header + [+ New Project]                      │
       │    ├─ ▼ Project 1 (collapsible)                              │
       │    │    ├─ Working directory                                 │
       │    │    ├─ Session 1, Session 2...                           │
       │    │    └─ + New Session                                     │
       │    ├─ ▶ Project 2 (collapsed)                                │
       │    └─ ⚙ Project Settings                                    │
       │  Main:                                                        │
       │    MetricsPanel (connection, task, tokens)                    │
       │    TaskInput (chat input + control buttons)                   │
       │    AgentTree × N (one per agent)                              │
       │    Final result / Failed actions                              │
       └──────────────────────────────────────────────────────────────┘

     Lifecycle:
       onMounted → loadProjects → loadSessions(activeProjectId) → connect WebSocket
       project click → setActiveProject → reloadSessions → clear active task
       user input → startTask → POST /api/tasks → WS events → taskCache update
       session click → switch activeSessionId + load task from history
-->
<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch, computed, nextTick } from 'vue'
import { useTaskStore } from './composables/useTaskStore'
import { useSessionStore } from './composables/useSessionStore'
import { useAgentStore } from './composables/useAgentStore'
import { useProjectStore } from './composables/useProjectStore'
import MetricsPanel from './components/MetricsPanel.vue'
import TaskInput from './components/TaskInput.vue'
import TurnList from './components/TurnList.vue'
import AgentConfig from './components/AgentConfig.vue'
import ProjectConfig from './components/ProjectConfig.vue'
import MemoryBrowser from './components/MemoryBrowser.vue'
import RAGPreviewPanel from './components/RAGPreviewPanel.vue'
import MemoryEventsTimeline from './components/MemoryEventsTimeline.vue'
import ContextWindowPanel from './components/ContextWindowPanel.vue'
import CaseCard from './components/CaseCard.vue'
import MultiAgentWorkflowEditor from './components/MultiAgentWorkflowEditor.vue'
import { type WorkflowConfig } from './types/agent'
import CaseFilter from './components/CaseFilter.vue'
import CaseForm from './components/CaseForm.vue'
import CaseDetailModal from './components/CaseDetailModal.vue'
import Toast from './components/Toast.vue'
import KeyboardTips from './components/KeyboardTips.vue'
import ApprovalDialog from './components/ApprovalDialog.vue'
import RecentModsDialog from './components/RecentModsDialog.vue'
import ModelPricesDialog from './components/ModelPricesDialog.vue'
import MCPServerDialog from './components/MCPServerDialog.vue'
import { useToast } from './composables/useToast'
import { useKeyboard, SHORTCUTS } from './composables/useKeyboard'
import { useRecentMods } from './composables/useRecentMods'
import { useMemoryEvents } from './composables/useMemoryEvents'
import { useContextWindow } from './composables/useContextWindow'
import { useCaseStore } from './composables/useCaseStore'
import type { Session } from './composables/useSessionStore'
import type { TaskState } from './types/events'
import type { Case, CreateCaseRequest, UpdateCaseRequest } from './types/case'
import type { ContextWindowSnapshotData } from './types/events'

const {
  taskCache,
  subTaskSnapshots,
  activeTaskId,
  isTaskPending,
  wsStatus,
  lastUserInput,
  pendingApproval,
  connect,
  disconnect,
  startTask,
  startTurn,
  startTaskWithCase,
  startMultiAgentTask,
  clearActiveTask,
  setActiveTaskId,
  loadTask,
  loadSessionTurns,
  pruneOrphanTasks,
  pauseTask,
  resumeTask,
  cancelTask,
  approveTask,
  denyTask,
} = useTaskStore()

const {
  sessions,
  activeSessionId,
  activeSession,
  loadSessions,
  createSession,
  setActiveSession,
  deleteSession,
  refreshSession,
  renameSession,
} = useSessionStore()

const { showAgentConfig, loadAgents, agents } = useAgentStore()

const { projects, activeProjectId, activeProject, loadProjects, setActiveProject } = useProjectStore()

const { toasts, showError, showInfo, dismissToast } = useToast()

const caseStore = useCaseStore()
const { filteredCases, allTags, allCategories, selectedTags, selectedCategory, loading: casesLoading } = caseStore

// === 最近修改 Dialog 状态 ===
const recentModsVisible = ref(false)

// === 模型价格管理 Dialog 状态 ===
// 由顶部 header 的 💲 按钮触发，与"最近修改"共用 overlay/Teleport 模式。
const modelPricesVisible = ref(false)

// === MCP Server 管理 Dialog 状态 ===
const mcpServerDialogVisible = ref(false)

const {
  items: recentMods,
  show: openRecentMods,
  toggle: toggleRecentMods,
  clear: clearRecentMods,
  addItem: addRecentMod,
  hasTodayItems,
} = useRecentMods()

function showRecentMods() {
  recentModsVisible.value = true
  openRecentMods()
}

/** Whether a session is currently in inline rename mode. */
const renamingSessionId = ref<string | null>(null)
/** Buffer for the inline rename input field. */
const renameBuffer = ref('')

/** Default task timeout (0 = unlimited). Matches the default in TaskInput. */
function getPreferredTimeoutSeconds(): number {
  try {
    const saved = localStorage.getItem('map_default_timeout_seconds')
    if (saved) {
      const n = parseInt(saved, 10)
      if (!Number.isNaN(n) && n >= 0) return n
    }
  } catch {
    // ignore storage errors
  }
  return 0
}

// Selected agent ID for task execution
const selectedAgentId = ref('agent_default')

// Auto-approve policy blocks toggle (shared with MetricsPanel)
const autoApprovePolicy = ref(false)

// Project config view toggle
const showProjectConfig = ref(false)

// Multi-agent mode toggle
const useMultiAgent = ref(false)

// Multi-agent workflow editor
const showWorkflowEditor = ref(false)
const workflowConfig = ref<WorkflowConfig>({
  strategy: 'parallel',
  agents: [],
})

function handleOpenWorkflowEditor() {
  showWorkflowEditor.value = true
}

function handleCloseWorkflowEditor() {
  showWorkflowEditor.value = false
}

function handleSaveWorkflow(config: WorkflowConfig) {
  workflowConfig.value = config
  showWorkflowEditor.value = false
}

/** Currently selected sub-task/agent context window target. */
const selectedSubTaskId = ref<string>('')

/** Singleton context window snapshot listener */
const {
  setActiveTaskId: setContextWindowTaskId,
  currentSnapshot: latestContextSnapshot,
  setSnapshot: setContextWindowSnapshot,
  clear: clearContextWindow,
} = useContextWindow()

watch(activeTaskId, (taskId) => {
  setContextWindowTaskId(taskId || '')
  // Reset sub-task selection when the active task changes; the leader is default.
  selectedSubTaskId.value = ''
}, { immediate: true })

/** Refetch the context window snapshot on demand from the REST API. */
async function fetchContextWindowSnapshot() {
  const taskId = activeTaskId.value
  if (!taskId) return
  const subTaskId = selectedSubTaskId.value
  const url = subTaskId
    ? `/api/tasks/${taskId}/context_window?sub_task_id=${encodeURIComponent(subTaskId)}`
    : `/api/tasks/${taskId}/context_window`
  try {
    const resp = await fetch(url)
    if (!resp.ok) {
      if (resp.status === 404) {
        console.warn('[App] Context snapshot not found for task', taskId, 'subTask', subTaskId)
      } else {
        console.error('[App] Failed to fetch context snapshot:', resp.statusText)
      }
      return
    }
    const data = (await resp.json()) as ContextWindowSnapshotData
    // 通过 useContextWindow 提供的 API 回填快照，避免直接修改 composable 内部 ref。
    setContextWindowSnapshot(taskId, data)
  } catch (err) {
    console.error('[App] Network error fetching context snapshot:', err)
  }
}

/** List the available sub-tasks (agents) for the active root task. */
const activeSubTasks = computed(() => {
  const taskId = activeTaskId.value
  if (!taskId) return []
  const tasks = [taskCache.value[taskId]].filter(Boolean)
  const rootTask = tasks[0]
  if (!rootTask) return []
  // Derive sub-task IDs: each agent under the root task has its own snapshot key.
  // For the leader, key equals taskId; for child agents, key is task_id + '_' + agent_id.
  const agents = Object.values(rootTask.agents)
  const list: { subTaskId: string; agentId: string; label: string }[] = []
  for (const agent of agents) {
    const isLeader = agent.id === Object.keys(rootTask.agents)[0]
    // If there is only one agent it is the leader; its SubTaskID equals taskId.
    const subTaskId = isLeader ? taskId : `${taskId}_${agent.id}`
    list.push({
      subTaskId,
      agentId: agent.id,
      label: agent.name || agent.id,
    })
  }
  return list
})

/** Whether the current active task has multiple agents (true multi-agent). */
const hasMultipleAgents = computed(() => activeSubTasks.value.length > 1)

/** Singleton memory event listener + timeline state for Phase 6-F */
const { events: memoryEvents, clear: clearMemoryEvents } = useMemoryEvents()

/** Whether the Memory overlay is open */
const showMemoryBrowser = ref(false)

/** Whether the Context Window overlay is open */
const showContextWindow = ref(false)

/** Toggle the context window overlay */
function toggleContextWindow() {
  showContextWindow.value = !showContextWindow.value
}

/** Close the context window overlay */
function closeContextWindow() {
  showContextWindow.value = false
}

/** Active tab inside the memory overlay: 'browser' | 'rag' | 'events' */
const memoryTab = ref<'browser' | 'rag' | 'events'>('browser')

/** Toggle the Memory overlay open/closed without replacing main content. */
function toggleMemoryBrowser() {
  showMemoryBrowser.value = !showMemoryBrowser.value
}

/** Close the Memory overlay and return to the previous view. */
function closeMemoryBrowser() {
  showMemoryBrowser.value = false
}

/** Switch memory overlay tab and auto-scroll to selected memory from timeline. */
function switchMemoryTab(tab: 'browser' | 'rag' | 'events') {
  memoryTab.value = tab
}

function handleTimelineMemorySelect(id: string) {
  memoryTab.value = 'browser'
  // Let browser render then scroll to the selected card
  nextTick(() => {
    const el = document.getElementById(`memory-${id}`) as HTMLElement | null
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'center' })
      el.classList.add('timeline-highlight')
      setTimeout(() => el.classList.remove('timeline-highlight'), 2000)
    }
  })
}

// Collapsed state for project groups in sidebar
const collapsedProjects = ref<Set<string>>(new Set())

/** 控制侧边栏顶部 "+" 新建 Session 菜单的展开/折叠 */
const showNewSessionMenu = ref(false)

/** 自定义工作目录路径输入框的绑定值 */
const customWorkspacePath = ref('')

// === Global expand/collapse state for all agent trees in the current view ===
/** null = no explicit user command; true/false = expand/collapse all */
const expandAll = ref<boolean | null>(null)

/** User explicitly expands every turn and step in the current tree. */
function expandAllTrees() {
  expandAll.value = true
}

/** User explicitly collapses every turn and step in the current tree. */
function collapseAllTrees() {
  expandAll.value = false
}

// Reset expand/collapse command when the active task or session changes so
// a new task defaults to current behavior (latest turn/step expanded).
watch(activeTaskId, () => {
  expandAll.value = null
})
watch(activeSessionId, () => {
  expandAll.value = null
})

// === Smart auto-scroll for the main page ===
const BOTTOM_THRESHOLD = 50 // px
const mainRef = ref<HTMLElement | null>(null)
const isNearBottom = ref(true)
const autoScrollPaused = ref(false)

/** Check if the main scroll container is within BOTTOM_THRESHOLD px of the bottom. */
function checkNearBottom(): boolean {
  const el = mainRef.value
  if (!el) return true
  return el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_THRESHOLD
}

/** Smoothly scroll the main container to its bottom. */
function scrollToBottom() {
  const el = mainRef.value
  if (!el) return
  el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' })
  isNearBottom.value = true
  autoScrollPaused.value = false
}

/** Window-level fallback scroll to bottom (for Ctrl+End). */
function scrollWindowToBottom() {
  window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'smooth' })
}

/** Called on main scroll events to detect if the user has moved away from the bottom. */
function handleMainScroll() {
  const near = checkNearBottom()
  isNearBottom.value = near
  if (near) {
    autoScrollPaused.value = false
  } else if (!autoScrollPaused.value) {
    // User explicitly scrolled up — pause auto-scroll until they return to bottom.
    autoScrollPaused.value = true
  }
}

// When new content arrives (steps/thinking grow, new turns), auto-scroll only
// if the user is already near the bottom. Otherwise keep the paused indicator.
watch(
  () => sessionTurns.value.length,
  () => {
    if (isNearBottom.value) {
      nextTick(scrollToBottom)
    }
  }
)

watch(
  () => currentTask.value?.totalTokens,
  () => {
    if (isNearBottom.value) {
      nextTick(scrollToBottom)
    }
  }
)

// Global Ctrl+End handler to resume auto-scroll.
function handleKeydownGlobal(e: KeyboardEvent) {
  if (e.key === 'End' && e.ctrlKey && !e.shiftKey && !e.altKey && !e.metaKey) {
    e.preventDefault()
    isNearBottom.value = true
    autoScrollPaused.value = false
    scrollToBottom()
    scrollWindowToBottom()
  }
}

// 点击侧边栏外部区域时关闭新建 Session 菜单
function handleGlobalClick(e: MouseEvent) {
  const target = e.target as HTMLElement
  // 如果点击的不是菜单内部或其触发按钮，则关闭
  if (!target.closest('.new-session-menu') && !target.closest('.new-session-btn[title="New Session"]')) {
    showNewSessionMenu.value = false
  }
}

onMounted(() => {
  window.addEventListener('keydown', handleKeydownGlobal)
  window.addEventListener('click', handleGlobalClick)
})
onUnmounted(() => {
  window.removeEventListener('keydown', handleKeydownGlobal)
  window.removeEventListener('click', handleGlobalClick)
})

// Handlers for approval dialog
function handleApprove(approvalId: string) {
  if (!pendingApproval.value) return
  approveTask(approvalId, pendingApproval.value.taskId, pendingApproval.value.agentId)
}

function handleDeny(approvalId: string) {
  if (!pendingApproval.value) return
  denyTask(approvalId, pendingApproval.value.taskId, pendingApproval.value.agentId)
}

function handleApprovalClose() {
  // F1 修复：关闭对话框必须等同于拒绝，否则后端会一直等到 30s 审批超时
  // 才释放任务，期间该 task 仍显示 running，用户也无法重新提交。
  // 之前只清空 pendingApproval 而不发送 deny，导致 WS 队列里没有控制消息，
  // 后端 handleApprovalRequired.WaitForDecision 必然超时。
  if (pendingApproval.value) {
    denyTask(
      pendingApproval.value.approvalId,
      pendingApproval.value.taskId,
      pendingApproval.value.agentId,
    )
  }
  pendingApproval.value = null
}

// Keyboard shortcuts
const { isRunning: kbIsRunning, showTips } = useKeyboard({
  onCancel: cancelTask,
  onPause: () => {
    if (currentTask.value?.status === 'running') {
      pauseTask()
    } else {
      resumeTask()
    }
  },
  onResume: resumeTask,
})

// Case library / 任务库 loaded via useCaseStore
const appVersion = ref('v0.4 Alpha')
// Case detail modal state
const selectedCase = ref<Case | null>(null)
const showCaseModal = ref(false)
// Case form state (create/edit)
const editingCase = ref<Case | null>(null)
const showCaseForm = ref(false)

const currentTask = computed(() => {
  if (!activeTaskId.value) return null
  return taskCache.value[activeTaskId.value] || null
})

/** Total tokens across all turns in the active session.
 *  This is what the MetricsPanel header should show — the session-level cost,
 *  not just the currently visible turn. */
const sessionTotalTokens = computed(() => {
  const sid = activeSession.value?.id
  if (!sid) return 0
  return Object.values(taskCache.value)
    .filter(t => t.sessionId === sid)
    .reduce((sum, t) => sum + (t.totalTokens || 0), 0)
})

/** Total duration across all turns in the active session (ms).
 *  Mirrors the token strategy so MetricsPanel can display a single session-level
 *  elapsed time without re-summing client-side. */
const sessionTotalDuration = computed(() => {
  const sid = activeSession.value?.id
  if (!sid) return 0
  return Object.values(taskCache.value)
    .filter(t => t.sessionId === sid)
    .reduce((sum, t) => sum + (t.durationMs || 0), 0)
})

/**
 * Collect all loaded turns in the current session for the timeline view.
 * After handleSessionSelect calls loadSessionTurns, every task for the
 * active session will be present in taskCache; this computed slices them
 * out in chronological order so TurnList can render the full timeline.
 */
const sessionTurns = computed(() => {
  const sid = activeSession.value?.id
  const turns: Array<{ task: TaskState; userInput: string }> = []
  // If no session is selected, show nothing
  if (!sid) return turns

  const allTasks = Object.values(taskCache.value)
    .filter(t => t.sessionId === sid)
  // Sort by startedAt ASC so timeline is in conversation order
  allTasks.sort((a, b) => a.startedAt - b.startedAt)
  for (const t of allTasks) {
    turns.push({
      task: t,
      // Prefer the hydrated userInput; fall back to the last-submitted input
      // (handles the real-time case where DB hasn't been written yet).
      userInput: t.userInput || lastUserInput.value,
    })
  }
  return turns
})

const isAgentRunning = computed(() => {
  if (!currentTask.value) return false
  return currentTask.value.status === 'running'
})

/** Whether the current task is in idle state (exists but not yet executing).
 *  DB-backed sessions may surface tasks with status='idle' when loaded from history
 *  before any agent event has arrived. We treat this like an empty state for display. */
const isTaskIdle = computed(() => {
  return currentTask.value?.status === 'idle'
})

// Group sessions by project for sidebar display
interface ProjectGroup {
  project: { id: string; name: string; description: string; working_directory: string }
  sessions: Session[]
  isCollapsed: boolean
}

const projectGroups = computed<ProjectGroup[]>(() => {
  // Build a map of project_id → sessions
  const map = new Map<string, Session[]>()
  for (const s of sessions.value) {
    const pid = s.projectId || 'default'
    if (!map.has(pid)) map.set(pid, [])
    map.get(pid)!.push(s)
  }

  // Ensure all loaded projects appear, even if they have no sessions
  const result: ProjectGroup[] = []
  const seenProjects = new Set<string>()

  for (const p of projects.value) {
    seenProjects.add(p.id)
    const groupSessions = map.get(p.id) || []
    result.push({
      project: { id: p.id, name: p.name, description: p.description, working_directory: p.working_directory },
      sessions: groupSessions,
      isCollapsed: collapsedProjects.value.has(p.id) && p.id !== activeProjectId.value,
    })
  }

  // Add any sessions whose project is not in the projects list (e.g. 'default' before projects load)
  for (const [pid, ss] of map) {
    if (!seenProjects.has(pid)) {
      result.push({
        project: { id: pid, name: pid === 'default' ? 'Default' : pid, description: '', working_directory: '' },
        sessions: ss,
        isCollapsed: collapsedProjects.value.has(pid) && pid !== activeProjectId.value,
      })
    }
  }

  return result
})

// Connect WebSocket on mount, load projects and sessions
onMounted(async () => {
  connect()
  // Phase 6-F: subscribe memory events singleton before any component renders
  useMemoryEvents()
  console.log('[App] onMounted: loading projects...')
  await loadProjects().catch(err => console.error('Failed to load projects:', err))
  console.log('[App] onMounted: projects loaded, count:', projects.value.length)
  console.log('[App] onMounted: loading sessions for project:', activeProjectId.value)
  await loadSessions(activeProjectId.value).catch(err => console.error('Failed to load sessions:', err))
  console.log('[App] onMounted: sessions loaded, count:', sessions.value.length)
  console.log('[App] onMounted: activeSessionId:', activeSessionId.value)
  console.log('[App] onMounted: sessions detail:', sessions.value.map(s => ({
    id: s.id, name: s.name, rootTaskId: s.rootTaskId, status: s.status,
  })))
  await loadAgents().catch(err => console.error('Failed to load agents:', err))
  // Load cases via the case store
  await caseStore.loadCases().catch(err => console.error('Failed to load cases:', err))
  // Load version from server
  try {
    const resp = await fetch('/api/version')
    if (resp.ok) {
      const data = await resp.json()
      appVersion.value = data.version
    }
  } catch (err) {
    console.error('Failed to load version:', err)
  }
})

onUnmounted(() => {
  disconnect()
})

// 最近修改 Dialog: 页面加载时如果有今天的记录则自动弹出一次
if (hasTodayItems()) {
  showRecentMods()
}

// 最近修改 Dialog 快捷键: Ctrl+M / Cmd+M
onMounted(() => {
  const handler = (e: KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'm') {
      e.preventDefault()
      toggleRecentMods()
    }
  }
  window.addEventListener('keydown', handler)
  onUnmounted(() => window.removeEventListener('keydown', handler))
})

/** Handle task submission from TaskInput */
async function handleSend(text: string, options: { maxSteps?: number; timeoutSeconds?: number; scope?: string }) {
  try {
    // === Skill 前缀解析 ===
    // 用户通过 SkillPicker 选中的 skill 会在输入文本最前面留下 `/skill-id ` 前缀。
    // 这里在发送前做两件事：
    //   1) 调用 POST /api/skills/{id}/enable，让后端把该 skill 加入 active 列表，
    //      使 Engine 在渲染 system prompt 时注入该 skill 模板（幂等：已启用则不重复）。
    //   2) 从文本中剥离 `/skill-id ` 前缀，把剩余部分作为真实的 user_input 发给 chat API。
    // 选择前端控制 enable 的简单方案，避免改动 session/chat API 的契约。
    const { skillId, remaining } = parseSkillPrefix(text)
    if (skillId) {
      try {
        await enableSkill(skillId)
      } catch (err) {
        // enable 失败不阻塞发送，仅打日志——后端看到没有 active skill 仍能正常执行。
        console.error('[App] enable skill failed:', err)
      }
    }
    const finalText = remaining
    const session = activeSession.value
    if (useMultiAgent.value) {
      const targetSession = session || await createSession(undefined, finalText, activeProjectId.value)
      if (!session) {
        setActiveSession(targetSession.id)
      }
      const agents = workflowConfig.value.agents.length > 0 ? workflowConfig.value.agents.map(a => ({
        agent_id: a.agentId,
        name: a.name,
        system_prompt: a.systemPrompt,
        input: a.input || finalText,
        allowed_tools: a.allowedTools || [],
        output_to: a.outputTo || [],
        model: a.model,
      })) : undefined
      await startMultiAgentTask(finalText, {
        sessionId: targetSession.id,
        maxSteps: options.maxSteps,
        timeoutSeconds: options.timeoutSeconds,
        scope: options.scope,
        agents,
      })
      return
    }
    if (!session) {
      // No active session — create a new one in the current project
      const newSession = await createSession(undefined, finalText, activeProjectId.value)
      setActiveSession(newSession.id)
      await startTask(finalText, {
        timeoutSeconds: options.timeoutSeconds,
        maxSteps: options.maxSteps,
        scope: options.scope,
        sessionId: newSession.id,
        agentId: selectedAgentId.value !== 'agent_default' ? selectedAgentId.value : undefined,
      })
    } else if (!session.rootTaskId) {
      // Session exists but no root task yet — this is the first turn
      await startTask(finalText, {
        timeoutSeconds: options.timeoutSeconds,
        maxSteps: options.maxSteps,
        scope: options.scope,
        sessionId: session.id,
        agentId: selectedAgentId.value !== 'agent_default' ? selectedAgentId.value : undefined,
      })
    } else {
      // Session already has a root task — this is a subsequent turn
      // Use the multi-turn chat endpoint
      await startTurn(finalText, {
        timeoutSeconds: options.timeoutSeconds,
        sessionId: session.id,
        maxSteps: options.maxSteps,
        scope: options.scope,
        agentId: selectedAgentId.value !== 'agent_default' ? selectedAgentId.value : undefined,
      })
    }
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to start task')
  }
}

/**
 * 解析输入文本最前面的 `/skill-id ` 前缀。
 * 约定 skill id 由 ASCII 字母、数字、`-`、`_`、`.`、`/` 组成（与后端 skill.Skill.ID 实际字符集对齐）。
 * 返回剩余文本（保留后续输入），若无前缀则 remaining 等于原文、skillId 为空字符串。
 */
function parseSkillPrefix(text: string): { skillId: string; remaining: string } {
  // 正则：`/` 开头 + skill id 字符 + 一个空格结尾
  const m = /^\/([A-Za-z0-9][A-Za-z0-9\-_./]*)\s+(.*)$/s.exec(text)
  if (!m) {
    return { skillId: '', remaining: text }
  }
  return { skillId: m[1], remaining: m[2] }
}

/**
 * 调用后端启用 skill。幂等：后端在 skill 已启用时直接返回 200。
 * 失败抛出异常，由调用方决定是否继续发送。
 */
async function enableSkill(skillId: string): Promise<void> {
  const resp = await fetch(`/api/skills/${encodeURIComponent(skillId)}/enable`, {
    method: 'POST',
  })
  if (!resp.ok) {
    throw new Error(`enable skill ${skillId} failed: ${resp.status} ${resp.statusText}`)
  }
}


/** Compute the next max steps when continuing a failed task */
function nextMaxSteps(): number {
  const currentMax = Object.values(currentTask.value?.agents ?? {}).find(a => a.maxSteps)?.maxSteps ?? 30
  return currentMax * 2
}

/** Whether the failure was caused by max_steps_exceeded */
function isMaxStepsFailure(): boolean {
  return currentTask.value?.finalResult?.includes('max steps') ?? false
}

/** Continue a max-steps-exceeded task with doubled max_steps in the same session.
 *
 *  Instead of starting a brand-new root task (which loses the conversation
 *  context), this sends the original user input as a new turn in the current
 *  session. The backend's session-chat endpoint loads session_messages and
 *  prepends the full history to the system prompt, so the agent can continue
 *  as if it were the next round of the same conversation.
 */
async function handleContinue() {
  if (!lastUserInput.value) {
    showError('No previous input to continue from')
    return
  }
  const sessionId = activeSessionId.value
  if (!sessionId) {
    showError('No active session to continue in')
    return
  }
  try {
    const newMaxSteps = nextMaxSteps()
    showInfo(`Continuing with max steps ×2 = ${newMaxSteps}`)
    await startTurn(lastUserInput.value, {
      sessionId,
      maxSteps: newMaxSteps,
      timeoutSeconds: getPreferredTimeoutSeconds(),
      agentId: selectedAgentId.value !== 'agent_default' ? selectedAgentId.value : undefined,
    })
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to continue task')
  }
}

// Sync keyboard shortcut state with task running state
watch(
  () => currentTask.value?.status,
  (status) => {
    kbIsRunning.value = status === 'running'
  }
)

/** Handle running a case from the case library */
async function handleCaseRun(caseId: string) {
  try {
    showCaseModal.value = false
    const session = activeSession.value
    if (!session) {
      const newSession = await createSession(undefined, `Case: ${caseId}`, activeProjectId.value)
      setActiveSession(newSession.id)
    }
    await startTaskWithCase(caseId, { sessionId: activeSessionId.value || undefined })
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to start case')
  }
}

/** Handle viewing case details */
function handleCaseView(caseId: string) {
  const c = caseStore.cases.value.find((p: Case) => p.id === caseId)
  if (c) {
    selectedCase.value = c
    showCaseModal.value = true
  }
}

/** Handle editing a case — open form in edit mode */
function handleCaseEdit(caseId: string) {
  const c = caseStore.cases.value.find((p: Case) => p.id === caseId)
  if (c) {
    editingCase.value = c
    showCaseForm.value = true
  }
  // Also close the detail modal if it is open so the form is visible
  showCaseModal.value = false
}

/** Handle deleting a case with confirmation */
async function handleCaseDelete(caseId: string) {
  if (!confirm('Are you sure you want to delete this case?')) return
  try {
    await caseStore.deleteCase(caseId)
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to delete case')
  }
}

/** Open the case form for creating a new case */
function openCreateCaseForm() {
  editingCase.value = null
  showCaseForm.value = true
}

/** Persist a create or update request from CaseForm */
async function handleCaseSave(req: CreateCaseRequest | UpdateCaseRequest) {
  try {
    if (editingCase.value) {
      await caseStore.updateCase(editingCase.value.id, req)
    } else {
      await caseStore.createCase(req as CreateCaseRequest)
    }
    showCaseForm.value = false
    editingCase.value = null
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to save case')
  }
}

/** Toggle a tag filter — shared by CaseFilter and CaseCard */
function handleToggleTag(tag: string) {
  caseStore.toggleTag(tag)
}
function toggleProjectCollapse(projectId: string) {
  if (collapsedProjects.value.has(projectId)) {
    collapsedProjects.value.delete(projectId)
  } else {
    collapsedProjects.value.add(projectId)
  }
  // Trigger reactivity
  collapsedProjects.value = new Set(collapsedProjects.value)
}

/** Switch to a different project — reload sessions and clear task/state */
async function handleProjectSelect(projectId: string) {
  if (projectId === activeProjectId.value) return
  setActiveProject(projectId)
  // Auto-expand the selected project
  collapsedProjects.value.delete(projectId)
  collapsedProjects.value = new Set(collapsedProjects.value)
  clearActiveTask()
  clearContextWindow()
  try {
    await loadSessions(projectId)
  } catch (err) {
    console.error('Failed to load sessions for project:', projectId, err)
  }
}

/** Switch to a different session from the sidebar */
async function handleSessionSelect(session: Session) {
  console.log('[App] handleSessionSelect:', JSON.stringify({
    id: session.id,
    name: session.name,
    rootTaskId: session.rootTaskId,
    status: session.status,
    userInput: session.userInput,
  }))
  setActiveSession(session.id)
  // Prune optimistic placeholders left behind when a task was accepted by the
  // backend but never confirmed via WebSocket. Without this, those orphans
  // (which carry no sessionId) leak into whichever session is active next.
  pruneOrphanTasks()
  if (session.rootTaskId) {
    console.log('[App] Loading session turns:', session.id)
    clearActiveTask()
    // 清理 taskCache 中不属于当前 session 的旧数据，避免长期切换导致内存累积
    const sid = session.id
    for (const tid of Object.keys(taskCache.value)) {
      const t = taskCache.value[tid]
      if (t.sessionId && t.sessionId !== sid) {
        delete taskCache.value[tid]
      }
    }
    // Also drop any remaining tasks that do not belong to this session. Tasks
    // loaded from the backend always carry a sessionId; orphan placeholders are
    // the only ones without one. Permitting them would let a stray turn bleed
    // into every session the user switches to.
    for (const tid of Object.keys(taskCache.value)) {
      const t = taskCache.value[tid]
      if (!t.sessionId) {
        delete taskCache.value[tid]
      }
    }
    try {
      // Load ALL turns (root + continuation turns) so sessionTurns shows
      // the full conversation timeline, not just the most recent task.
      await loadSessionTurns(session.id)
      // Set the active task to the latest turn according to sessionTurns'
      // chronological order (not Object.keys order), so the timeline defaults
      // to expanding the most recent turn instead of turn 1.
      const ordered = Object.values(taskCache.value)
        .filter(t => t.sessionId === sid)
        .sort((a, b) => a.startedAt - b.startedAt)
      if (ordered.length > 0) {
        activeTaskId.value = ordered[ordered.length - 1].id
      } else {
        // 当前 session 没有任何 task 时清空 context window，避免展示旧 session 快照。
        activeTaskId.value = ''
        clearContextWindow()
      }
      console.log('[App] loadSessionTurns done, taskCache keys:', Object.keys(taskCache.value))
    } catch (err) {
      console.error('[App] loadSessionTurns failed:', err)
    }
  } else {
    console.log('[App] No rootTaskId, clearing active task')
    clearActiveTask()
    clearContextWindow()
  }
}

/** Create a new empty session in the current project */
async function handleNewSession(projectId?: string) {
  try {
    const pid = projectId || activeProjectId.value
    const session = await createSession(undefined, undefined, pid)
    setActiveSession(session.id)
    clearActiveTask()
    clearContextWindow()
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to create session')
  }
}

/** 展开/折叠新建 Session 菜单，同时清空上次输入 */
function toggleNewSessionMenu() {
  showNewSessionMenu.value = !showNewSessionMenu.value
  if (!showNewSessionMenu.value) {
    customWorkspacePath.value = ''
  }
}

/** Auto 模式：不指定 workspace，后端自动生成 ./workspace/session-{id}/ */
async function handleNewSessionAuto() {
  showNewSessionMenu.value = false
  customWorkspacePath.value = ''
  try {
    const session = await createSession(undefined, undefined, activeProjectId.value)
    setActiveSession(session.id)
    clearActiveTask()
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to create session')
  }
}

/** Custom Path 模式：用户指定服务器路径作为 workspace */
async function handleNewSessionCustom() {
  customWorkspacePath.value = customWorkspacePath.value.trim()
  if (!customWorkspacePath.value) {
    showError('Please enter a valid server path')
    return
  }
  showNewSessionMenu.value = false
  try {
    const session = await createSession(undefined, undefined, activeProjectId.value, customWorkspacePath.value)
    setActiveSession(session.id)
    clearActiveTask()
    clearContextWindow()
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to create session')
  }
  customWorkspacePath.value = ''
}

/** 在新标签页打开 session 的 workspace 目录 */
function openWorkspace(session: Session) {
  if (session.workspaceDir) {
    window.open('/s/' + session.id + '/', '_blank')
  }
}

/** Delete current session and select the next one */
async function handleDeleteSession(session: Session) {
  try {
    await deleteSession(session.id)
    if (activeSessionId.value === session.id) {
      const first = sessions.value[0]
      if (first) {
        setActiveSession(first.id)
        if (first.rootTaskId) {
          await loadSessionTurns(first.id)
          const keys = Object.keys(taskCache.value)
          if (keys.length > 0) {
            activeTaskId.value = keys[keys.length - 1]
          }
        } else {
          clearActiveTask()
          clearContextWindow()
        }
      } else {
        const empty = await createSession(undefined, undefined, activeProjectId.value)
        setActiveSession(empty.id)
        clearActiveTask()
        clearContextWindow()
      }
    }
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to delete session')
  }
}

/** Open project settings page */
function handleOpenProjectSettings() {
  showProjectConfig.value = true
}

/** Return from project settings */
function handleProjectConfigBack() {
  showProjectConfig.value = false
  // Reload projects in case something changed
  loadProjects().catch(err => console.error('Failed to reload projects:', err))
}

/** Start inline rename for a session. */
function startRenameSession(session: Session) {
  renamingSessionId.value = session.id
  renameBuffer.value = session.name
}

/** Commit a session rename to the backend and exit rename mode. */
async function commitRenameSession(session: Session) {
  if (renamingSessionId.value !== session.id) return
  const newName = renameBuffer.value.trim()
  if (newName && newName !== session.name) {
    try {
      await renameSession(session.id, newName)
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Failed to rename session')
    }
  }
  renamingSessionId.value = null
  renameBuffer.value = ''
}

/** Cancel an in-progress inline rename without saving. */
function cancelRenameSession() {
  renamingSessionId.value = null
  renameBuffer.value = ''
}

/** Format a timestamp as a short relative or absolute label for the sidebar.
 *  Returns only the most important fields: updated date/time, plus created
 *  when it differs by more than a few seconds. */
function formatSessionTime(ts: number): string {
  if (!ts || Number.isNaN(ts)) return ''
  const d = new Date(ts)
  try {
    return d.toLocaleString()
  } catch {
    return d.toISOString()
  }
}

/** Short-form timestamp for the sidebar list (only updated_at by default). */
function formatShortTime(ts: number): string {
  if (!ts || Number.isNaN(ts)) return ''
  const d = new Date(ts)
  const now = new Date()
  const isSameDay = d.getFullYear() === now.getFullYear()
    && d.getMonth() === now.getMonth()
    && d.getDate() === now.getDate()
  try {
    if (isSameDay) {
      return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
    }
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
  } catch {
    return d.toISOString()
  }
}
</script>

<template>
  <div class="app">
    <!-- Sidebar: Project list + grouped Session list -->
    <aside class="session-sidebar">
      <div class="sidebar-header">
        <h2 class="sidebar-title">Projects</h2>
        <div style="position: relative; display: inline-flex;">
          <!-- 新建 Session 菜单 -->
          <div v-if="showNewSessionMenu" class="new-session-menu">
            <button class="menu-auto-btn" @click="handleNewSessionAuto">
              📁 Auto — workspaces/session-xxx/
            </button>
            <div class="menu-separator"></div>
            <input v-model="customWorkspacePath"
                   class="menu-path-input"
                   type="text"
                   placeholder="Server path: /tmp/my-workspace"
                   @keydown.enter="handleNewSessionCustom" />
            <button class="menu-custom-btn" @click="handleNewSessionCustom">
              📂 Use This Path
            </button>
          </div>
          <!-- New Session 按钮 -->
          <button class="new-session-btn" @click="toggleNewSessionMenu" title="New Session">+</button>
          <!-- New Project 按钮（保持原有行为，长按或右键可设） -->
          <button class="new-session-btn project-btn" @click="showProjectConfig = true" title="New Project" style="margin-left: 4px;">⚙</button>
        </div>
      </div>
      <div class="project-list">
        <div
          v-for="group in projectGroups"
          :key="group.project.id"
          class="project-group"
        >
          <!-- Project header — click to select, arrow to toggle collapse -->
          <div
            :class="['project-header', { active: group.project.id === activeProjectId }]"
            @click="handleProjectSelect(group.project.id)"
          >
            <button
              class="project-toggle"
              @click.stop="toggleProjectCollapse(group.project.id)"
              :title="group.isCollapsed ? 'Expand' : 'Collapse'"
            >
              {{ group.isCollapsed ? '▶' : '▼' }}
            </button>
            <div class="project-info">
              <div class="project-name">🗂 {{ group.project.name }}</div>
              <div v-if="group.project.working_directory" class="project-dir">
                📁 {{ group.project.working_directory }}
              </div>
            </div>
          </div>

          <!-- Sessions under this project (hidden when collapsed) -->
          <div v-if="!group.isCollapsed" class="project-sessions">
            <div
              v-for="session in group.sessions"
              :key="session.id"
              :class="['session-item', { active: session.id === activeSessionId }]"
              @click="handleSessionSelect(session)"
            >
              <div class="session-name-wrap">
                <input
                  v-if="renamingSessionId === session.id"
                  v-model="renameBuffer"
                  class="session-rename-input"
                  type="text"
                  @click.stop
                  @keydown.enter="commitRenameSession(session)"
                  @keydown.escape="cancelRenameSession"
                  @blur="commitRenameSession(session)"
                  @vue:mounted="($refs['rename-input-' + session.id] as HTMLInputElement)?.focus()"
                />
                <span v-else class="session-name">💬 {{ session.name }}</span>
                <span v-if="session.workspaceDir && renamingSessionId !== session.id"
                      class="session-workspace-path"
                      :title="session.workspaceDir">
                  📁 {{ session.workspaceDir.split('/').pop() || session.workspaceDir.split('\\').pop() }}
                </span>
                <button
                  v-if="renamingSessionId !== session.id"
                  class="session-rename-btn"
                  @click.stop="startRenameSession(session)"
                  title="Rename session"
                >
                  ✎
                </button>
              </div>
              <button
                v-if="session.workspaceDir"
                class="session-workspace-btn"
                @click.stop="openWorkspace(session)"
                title="Open workspace: {{ session.workspaceDir }}"
              >
                📂
              </button>
              <div class="session-meta" :title="`Updated: ${formatSessionTime(session.updatedAt)}\nCreated: ${formatSessionTime(session.createdAt)}${session.workspaceDir ? '\nWorkspace: ' + session.workspaceDir : ''}`">
                <span :class="['session-status', session.status]">{{ session.status }}</span>
                <span v-if="session.totalTokens > 0" class="session-tokens">
                  {{ session.totalTokens }} tokens
                </span>
                <span v-if="session.updatedAt" class="session-timestamp">
                  {{ formatShortTime(session.updatedAt) }}
                </span>
              </div>
              <button
                class="session-delete"
                @click.stop="handleDeleteSession(session)"
                title="Delete session"
              >
                ×
              </button>
            </div>

            <!-- New Session button for this project -->
            <button class="new-session-inline" @click="handleNewSession(group.project.id)">
              + New Session
            </button>
          </div>
        </div>
      </div>

      <!-- Bottom: Project Settings -->
      <div class="sidebar-footer">
        <button class="project-settings-btn" @click="handleOpenProjectSettings">
          ⚙ Project Settings
        </button>
      </div>
    </aside>

    <!-- Main content -->
    <main ref="mainRef" class="main-content" @scroll="handleMainScroll">
      <!-- Agent Config view — replaces main content when active -->
      <AgentConfig v-if="showAgentConfig" @back="showAgentConfig = false" />

      <!-- Project Config view — replaces main content when active -->
      <ProjectConfig v-else-if="showProjectConfig" @back="handleProjectConfigBack" />

      <div v-if="showContextWindow" class="memory-overlay context-overlay" @click.self="closeContextWindow">
        <div class="memory-overlay-panel context-overlay-panel">
          <div class="memory-overlay-header context-overlay-header">
            <div class="memory-tabs">
              <button class="memory-tab active" disabled>🪟 Context Window</button>
            </div>
            <div class="memory-header-actions">
              <button class="memory-close-btn" @click="closeContextWindow" title="Close Context Window">
                × Close
              </button>
            </div>
          </div>
          <div class="memory-overlay-body">
            <div class="context-agent-selector" v-if="hasMultipleAgents">
              <label for="sub-task-select">Agent / SubTask</label>
              <select
                id="sub-task-select"
                v-model="selectedSubTaskId"
                @change="fetchContextWindowSnapshot"
              >
                <option value="">🧑‍✈️ Leader (root task)</option>
                <option
                  v-for="item in activeSubTasks"
                  :key="item.subTaskId"
                  :value="item.subTaskId"
                >
                  {{ item.label }}
                </option>
              </select>
            </div>
            <ContextWindowPanel
              class="context-overlay-content"
              :active-task-id="activeTaskId || ''"
              :sub-task-id="selectedSubTaskId"
              @refresh="fetchContextWindowSnapshot"
            />
          </div>
        </div>
      </div>

      <!-- Memory overlay with tabbed Browser / RAG / Events panels -->
      <div v-else-if="showMemoryBrowser" class="memory-overlay" @click.self="closeMemoryBrowser">
        <div class="memory-overlay-panel">
          <div class="memory-overlay-header">
            <div class="memory-tabs">
              <button
                :class="['memory-tab', { active: memoryTab === 'browser' }]"
                @click="switchMemoryTab('browser')"
              >
                🧠 Browser
              </button>
              <button
                :class="['memory-tab', { active: memoryTab === 'rag' }]"
                @click="switchMemoryTab('rag')"
              >
                🔍 RAG
              </button>
              <button
                :class="['memory-tab', { active: memoryTab === 'events' }]"
                @click="switchMemoryTab('events')"
              >
                📡 Events
                <span v-if="memoryEvents.length > 0" class="memory-tab-badge">{{ memoryEvents.length }}</span>
              </button>
            </div>
            <div class="memory-header-actions">
              <button
                v-if="memoryTab === 'events'"
                class="memory-action-btn"
                @click="clearMemoryEvents"
              >
                Clear
              </button>
              <button class="memory-close-btn" @click="closeMemoryBrowser" title="Close Memory Browser">
                × Close
              </button>
            </div>
          </div>
          <div class="memory-overlay-body">
            <MemoryBrowser
              v-if="memoryTab === 'browser'"
              class="memory-overlay-browser"
              @select-memory="handleTimelineMemorySelect"
            />
            <RAGPreviewPanel
              v-else-if="memoryTab === 'rag'"
              :project-id="activeProjectId"
              class="memory-overlay-rag"
            />
            <MemoryEventsTimeline
              v-else-if="memoryTab === 'events'"
              :events="memoryEvents"
              class="memory-overlay-events"
              @select-memory="handleTimelineMemorySelect"
            />
          </div>
        </div>
      </div>

      <!-- Normal main content -->
      <template v-else-if="!showContextWindow">
        <!-- Header -->
        <header class="app-header">
          <h1 class="app-title">🤖 Multi-Agent Platform</h1>
          <div class="app-header-right">
            <button class="agents-btn" @click="showAgentConfig = true" title="Agent Configuration">⚙ Agents</button>
            <button class="agents-btn" @click="toggleMemoryBrowser" title="Memory Browser">🧠 Memory</button>
            <button class="recent-mods-btn" @click="mcpServerDialogVisible = true" title="MCP Server 管理">🔌</button>
            <button class="recent-mods-btn" @click="toggleRecentMods" title="最近修改 (Ctrl+M)">📝</button>
            <button class="recent-mods-btn" @click="modelPricesVisible = true" title="模型价格管理">💲</button>
            <button class="tips-btn" @click="showTips = true" title="Keyboard shortcuts (?)">⌨</button>
            <span class="app-version">{{ appVersion }}</span>
          </div>
        </header>

        <!-- Metrics bar -->
        <MetricsPanel :task="currentTask" :session-total-tokens="sessionTotalTokens" :session-total-duration="sessionTotalDuration" :ws-status="wsStatus" :agents="agents" :selected-agent-id="selectedAgentId" :auto-approve="autoApprovePolicy" @update:selected-agent-id="(id: string) => selectedAgentId = id" @update:auto-approve="(v: boolean) => autoApprovePolicy = v" />

        <!-- View controls: expand/collapse all turns/steps -->
        <div v-if="currentTask && !isTaskIdle" class="view-controls">
          <button class="view-control-btn" @click="expandAllTrees" title="Expand all turns and steps">Expand All</button>
          <button class="view-control-btn" @click="collapseAllTrees" title="Collapse all turns and steps">Collapse All</button>
        </div>

        <!-- Auto-scroll paused indicator -->
        <div v-if="autoScrollPaused" class="scroll-paused-hint" @click="scrollToBottom">
          Auto-scroll paused — press Ctrl+End or click to resume
        </div>

        <!-- Turn List (multi-turn conversation timeline) -->
        <TurnList
          v-if="currentTask && !isTaskIdle"
          :turns="sessionTurns"
          :expand-all="expandAll ?? undefined"
          :show-agent-controls="isAgentRunning"
          @cancel-agent="(id: string) => cancelTask(id)"
          @pause-agent="(id: string) => pauseTask(id)"
          @resume-agent="(id: string) => resumeTask(id)"
        />

        <!-- Idle state — task exists in DB but hasn't executed (status='idle').
             Shown instead of the empty state when a session with an idle task is selected. -->
        <div v-else-if="isTaskIdle" class="empty-state">
          <div class="empty-icon">💤</div>
          <h2>Task Idle</h2>
          <p>This task hasn't started executing yet. Send a message above to resume.</p>
        </div>

        <!-- Loading indicator -->
        <div v-else-if="isTaskPending" class="loading-area">
          <div class="loading-spinner"></div>
          <div class="loading-text">Agent is starting...</div>
          <div class="loading-subtext">Waiting for LLM response</div>
        </div>

        <!-- Task input -->
        <TaskInput
          :disabled="isAgentRunning"
          :is-running="isAgentRunning"
          :is-pending="isTaskPending"
          :enable-multi-agent="useMultiAgent"
          @send="handleSend"
          @pause="pauseTask"
          @resume="resumeTask"
          @cancel="cancelTask"
          @toggle-context-window="toggleContextWindow"
          @update:enable-multi-agent="(v: boolean) => useMultiAgent = v"
          @open-workflow-editor="handleOpenWorkflowEditor"
        />

        <!-- Case library cards — shown when active session has no task / task is empty -->
        <div v-if="!currentTask && !isTaskPending" class="cases-section">
          <div class="cases-section-header">
            <h2 class="section-title">📋 Case Library / 任务库 ({{ filteredCases.length }})</h2>
            <button class="new-case-btn" @click="openCreateCaseForm">+ 新建 Case</button>
          </div>
          <CaseFilter
            :selected-tags="selectedTags"
            :selected-category="selectedCategory"
            :all-tags="allTags"
            :all-categories="allCategories"
            @toggle-tag="handleToggleTag"
            @set-category="caseStore.setCategory"
            @clear-filters="caseStore.clearFilters"
          />
          <div v-if="casesLoading" class="cases-loading">Loading...</div>
          <div v-else-if="filteredCases.length === 0" class="cases-empty">
            No cases match the current filters.
          </div>
          <div v-else class="cases-grid">
            <CaseCard
              v-for="c in filteredCases"
              :key="c.id"
              :case-data="c"
              :disabled="isAgentRunning"
              @run="handleCaseRun"
              @view="handleCaseView"
              @toggle-tag="handleToggleTag"
              @edit="handleCaseEdit"
              @delete="handleCaseDelete"
            />
          </div>
        </div>

        <!-- Empty state -->
        <div v-else-if="!isTaskPending" class="empty-state">
          <div class="empty-icon">🚀</div>
          <h2>Ready to start</h2>
          <p>Enter a task description above to see the agent in action.</p>
        </div>

        <!-- Evaluation result (shown whenever an evaluation exists, regardless of task status) -->
        <div v-if="currentTask?.evaluation" class="final-result evaluation-section" :class="{ 'evaluation-failed-section': !currentTask.evaluation.passed }">
          <div class="final-result-header">
            <template v-if="currentTask.evaluation.passed">✅ Evaluation Passed</template>
            <template v-else>❌ Evaluation Failed</template>
          </div>
          <div class="evaluation-body">
            <div class="evaluation-row">
              <span class="evaluation-score-label">Score</span>
              <span
                class="evaluation-score-value"
                :class="currentTask.evaluation.passed ? 'evaluation-passed-score' : 'evaluation-failed-score'"
              >
                {{ typeof currentTask.evaluation.score === 'number' ? currentTask.evaluation.score.toFixed(2) : currentTask.evaluation.score }}
              </span>
            </div>
            <div v-if="currentTask.evaluation.reason" class="evaluation-reason">
              {{ currentTask.evaluation.reason }}
            </div>
          </div>
        </div>

        <!-- Final result (shown when task completed) -->
        <div v-if="currentTask?.status === 'completed' && currentTask?.finalResult" class="final-result">
          <div class="final-result-header">✅ Task Complete</div>
          <pre class="final-result-text">{{ currentTask.finalResult }}</pre>
        </div>

        <!-- Task failed (shown when task failed) -->
        <div v-if="currentTask?.status === 'failed'" class="final-result final-result-failed">
          <div class="final-result-header">❌ Task Failed</div>
          <div v-if="currentTask.finalResult" class="final-result-text">{{ currentTask.finalResult }}</div>
          <div v-else class="final-result-text final-result-subtle">
            The task failed. Check the agent tree above for details.
          </div>
          <div v-if="isMaxStepsFailure()" class="failed-actions">
            <button class="btn-continue" @click="handleContinue">
              🚀 Continue with max steps ×2 ({{ nextMaxSteps() }})
            </button>
            <span class="continue-hint">Resume from the last input with doubled step budget</span>
          </div>
        </div>
      </template>

      <!-- Global toast notifications -->
      <Toast :toasts="toasts" @dismiss="dismissToast" />

      <!-- Case detail modal -->
      <CaseDetailModal
        :case-data="selectedCase"
        :visible="showCaseModal"
        @close="showCaseModal = false"
        @run="handleCaseRun"
        @edit="handleCaseEdit"
      />

      <!-- Case create/edit form -->
      <CaseForm
        :case-data="editingCase"
        :visible="showCaseForm"
        @close="showCaseForm = false; editingCase = null"
        @save="handleCaseSave"
      />

      <!-- Keyboard shortcuts panel -->
      <KeyboardTips
        :visible="showTips"
        :shortcuts="SHORTCUTS"
        :is-running="isAgentRunning"
        @close="showTips = false"
      />

      <!-- Multi-agent workflow editor -->
      <MultiAgentWorkflowEditor
        v-if="showWorkflowEditor"
        v-model="workflowConfig"
        @save="handleSaveWorkflow"
        @cancel="handleCloseWorkflowEditor"
      />

      <!-- Approval dialog for policy-blocked tool calls -->
      <ApprovalDialog
        :approval-id="pendingApproval?.approvalId ?? ''"
        :tool="pendingApproval?.tool ?? ''"
        :rule="pendingApproval?.rule"
        :namespace="pendingApproval?.namespace"
        :tags="pendingApproval?.tags"
        :reason="pendingApproval?.reason ?? ''"
        :input="pendingApproval?.input ?? {}"
        :auto-approve="autoApprovePolicy"
        :visible="pendingApproval !== null"
        :error="pendingApproval?.error"
        @approve="handleApprove"
        @deny="handleDeny"
        @close="handleApprovalClose"
      />

      <!-- 最近修改 Dialog: shows recent file write history -->
      <RecentModsDialog
        :visible="recentModsVisible"
        :items="recentMods"
        @update:visible="recentModsVisible = $event"
        @clear="clearRecentMods"
      />

      <!-- 模型价格管理 Dialog: 查看/编辑 ModelRegistry 的 InputPrice/OutputPrice。
           costs 仅供参考但必须非 0；此入口让运营在不重建二进制的前提下修正价格。 -->
      <ModelPricesDialog
        :visible="modelPricesVisible"
        @update:visible="modelPricesVisible = $event"
      />

      <!-- MCP Server 管理 Dialog: 列出 / 添加 / 启用 / 禁用 / 删除外部 MCP Server -->
      <MCPServerDialog
        :visible="mcpServerDialogVisible"
        @update:visible="mcpServerDialogVisible = $event"
      />
    </main>
  </div>
</template>

<style scoped>
.app {
  display: flex;
  min-height: calc(100vh - 40px);
}

/* Session sidebar */
.session-sidebar {
  width: 280px;
  min-width: 280px;
  border-right: 1px solid var(--border-primary);
  background: var(--bg-secondary);
  display: flex;
  flex-direction: column;
  max-height: calc(100vh - 40px);
}

.sidebar-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 14px 16px;
  border-bottom: 1px solid var(--border-primary);
  flex-shrink: 0;
}

.sidebar-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0;
}

.new-session-btn {
  background: var(--accent-blue);
  color: #fff;
  border: none;
  border-radius: 6px;
  width: 28px;
  height: 28px;
  font-size: 18px;
  cursor: pointer;
  transition: background 0.2s;
}

.new-session-btn:hover {
  background: #3a8eef;
}

/* Project list — scrollable */
.project-list {
  flex: 1;
  overflow-y: auto;
  padding: 4px;
}

/* Project group */
.project-group {
  margin-bottom: 2px;
}

.project-header {
  display: flex;
  align-items: flex-start;
  padding: 8px 10px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.15s;
  gap: 6px;
}

.project-header:hover {
  background: var(--bg-tertiary);
}

.project-header.active {
  background: rgba(74, 158, 255, 0.12);
}

.project-toggle {
  background: none;
  border: none;
  color: #888;
  font-size: 10px;
  cursor: pointer;
  padding: 2px;
  line-height: 1;
  flex-shrink: 0;
  margin-top: 2px;
}

.project-toggle:hover {
  color: #ccc;
}

.project-info {
  min-width: 0;
  flex: 1;
}

.project-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.project-dir {
  font-size: 10px;
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin-top: 2px;
  font-family: var(--font-mono);
}

/* Sessions under a project */
.project-sessions {
  padding-left: 10px;
  margin-bottom: 4px;
}

/* Session item */
.session-item {
  position: relative;
  padding: 6px 24px 6px 10px;
  border-radius: 6px;
  margin-bottom: 2px;
  cursor: pointer;
  transition: background 0.15s;
}

.session-item:hover {
  background: var(--bg-tertiary);
}

.session-item.active {
  background: rgba(74, 158, 255, 0.15);
  border: 1px solid rgba(74, 158, 255, 0.3);
}

.session-name {
  font-size: 12px;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  padding-right: 4px;
}

.session-name-wrap {
  display: flex;
  align-items: center;
  gap: 4px;
  padding-right: 20px;
}

.session-rename-input {
  flex: 1;
  min-width: 0;
  background: var(--bg-primary, #18181b);
  border: 1px solid var(--accent-blue, #4a9eff);
  color: var(--text-primary);
  font-size: 12px;
  padding: 2px 6px;
  border-radius: 4px;
  outline: none;
}

.session-rename-btn {
  background: transparent;
  border: none;
  color: #666;
  font-size: 11px;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s, color 0.15s;
  padding: 0 2px;
  line-height: 1;
}

.session-item:hover .session-rename-btn {
  opacity: 1;
}

.session-rename-btn:hover {
  color: #4a9eff;
}

.session-meta {
  display: flex;
  gap: 6px;
  margin-top: 2px;
  align-items: center;
}

.session-status {
  font-size: 9px;
  text-transform: uppercase;
  font-weight: 600;
  padding: 1px 5px;
  border-radius: 8px;
}

.session-status.empty { background: #333; color: #aaa; }
.session-status.running { background: rgba(74, 158, 255, 0.2); color: #4a9eff; }
.session-status.completed { background: rgba(81, 207, 102, 0.2); color: #51cf66; }
.session-status.failed { background: rgba(231, 76, 60, 0.2); color: #e74c3c; }

.session-tokens {
  font-size: 9px;
  color: var(--text-muted);
}

.session-timestamp {
  font-size: 9px;
  color: var(--text-muted);
  margin-left: auto;
}

.session-delete {
  position: absolute;
  top: 4px;
  right: 4px;
  background: transparent;
  border: none;
  color: #666;
  font-size: 14px;
  line-height: 1;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s, color 0.15s;
}

.session-item:hover .session-delete {
  opacity: 1;
}

.session-delete:hover {
  color: #e74c3c;
}

/* Inline New Session button */
.new-session-inline {
  display: block;
  width: 100%;
  background: transparent;
  border: 1px dashed #444;
  color: #888;
  font-size: 11px;
  padding: 5px 8px;
  border-radius: 6px;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
  margin-top: 2px;
}

.new-session-inline:hover {
  background: rgba(74, 158, 255, 0.08);
  color: #aaa;
  border-color: #555;
}

/* Sidebar footer */
.sidebar-footer {
  padding: 10px 12px;
  border-top: 1px solid var(--border-primary);
  flex-shrink: 0;
}

.project-settings-btn {
  width: 100%;
  background: #333;
  border: 1px solid #444;
  color: #ccc;
  font-size: 12px;
  padding: 6px 10px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.2s, color 0.2s;
  text-align: center;
}

.project-settings-btn:hover {
  background: #444;
  color: #fff;
}

/* Main content */
.main-content {
  flex: 1;
  min-width: 0;
  padding: 16px 20px;
  overflow-y: auto;
  max-height: calc(100vh - 40px);
}

/* View controls */
.view-controls {
  display: flex;
  gap: 8px;
  margin: 12px 0;
}

.view-control-btn {
  background: #2a2a2a;
  border: 1px solid #444;
  color: #bbb;
  font-size: 12px;
  padding: 4px 10px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
}

.view-control-btn:hover {
  background: #3a3a3a;
  color: #fff;
  border-color: #555;
}

/* Auto-scroll paused hint */
.scroll-paused-hint {
  position: sticky;
  top: 12px;
  z-index: 50;
  margin: 0 0 12px;
  padding: 8px 14px;
  background: #2a2a2a;
  border: 1px dashed #666;
  border-radius: 8px;
  color: #aaa;
  font-size: 12px;
  text-align: center;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.scroll-paused-hint:hover {
  background: #333;
  color: #fff;
}

/* Header */
.app-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border-primary);
}

.app-header-right {
  display: flex;
  align-items: center;
  gap: 10px;
}

.app-title {
  font-size: 20px;
  font-weight: 700;
  color: #e0e0e0;
}

.tips-btn {
  background: #333;
  border: 1px solid #444;
  color: #ccc;
  font-size: 14px;
  padding: 4px 8px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.2s, color 0.2s;
}

.tips-btn:hover {
  background: #444;
  color: #fff;
}

.agents-btn {
  background: #333;
  border: 1px solid #444;
  color: #ccc;
  font-size: 13px;
  padding: 4px 10px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.2s, color 0.2s;
  font-weight: 500;
}

.agents-btn:hover {
  background: #444;
  color: #fff;
}

.app-version {
  font-size: 11px;
  color: var(--text-muted);
  background: var(--bg-tertiary);
  padding: 2px 8px;
  border-radius: 10px;
}

/* Agent trees grid */
.agent-trees {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(500px, 1fr));
  gap: 16px;
  margin-top: 16px;
}

.agent-tree-wrapper {
  min-width: 0;
}

/* Empty state */
.empty-state {
  text-align: center;
  padding: 60px 20px;
  color: var(--text-secondary);
}

.empty-icon {
  font-size: 48px;
  margin-bottom: 16px;
}

.empty-state h2 {
  font-size: 18px;
  color: var(--text-primary);
  margin-bottom: 8px;
}

.empty-state p {
  font-size: 13px;
  margin-bottom: 4px;
}

/* Cases section */
.cases-section {
  margin-top: 20px;
}

.cases-section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}

.new-case-btn {
  background: #4a9eff;
  color: #fff;
  border: none;
  border-radius: 6px;
  padding: 6px 12px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s;
}

.new-case-btn:hover {
  background: #3a8eef;
}

.section-title {
  font-size: 15px;
  font-weight: 600;
  color: #e0e0e0;
  margin: 0;
}

.cases-loading {
  text-align: center;
  color: #888;
  padding: 20px;
  font-size: 13px;
}

.cases-empty {
  text-align: center;
  color: #888;
  padding: 20px;
  font-size: 13px;
}

.cases-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 12px;
}

/* Final result */
.final-result {
  margin-top: 16px;
  background: #1a2e1a;
  border: 1px solid #2a4a2a;
  border-radius: 8px;
  overflow: hidden;
}

.final-result-failed {
  background: #2e1a1a;
  border-color: #4a2a2a;
}

.final-result-header {
  padding: 10px 14px;
  font-weight: 600;
  font-size: 14px;
  color: var(--accent-green);
  background: #1e3a1e;
  border-bottom: 1px solid #2a4a2a;
}

.final-result-failed .final-result-header {
  color: var(--accent-red);
  background: #3a1e1e;
  border-bottom-color: #4a2a2a;
}

/* Loading area */
.loading-area {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 60px 20px;
  gap: 12px;
}

.loading-spinner {
  width: 36px;
  height: 36px;
  border: 3px solid #333;
  border-top-color: #4a9eff;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.loading-text {
  font-size: 15px;
  color: #d4d4d4;
  font-weight: 500;
}

.loading-subtext {
  font-size: 12px;
  color: #888;
}

.final-result-text {
  padding: 14px;
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--text-primary);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 400px;
  overflow-y: auto;
}

.final-result-subtle {
  color: #888;
  font-style: italic;
}

.failed-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  border-top: 1px solid #4a2a2a;
  background: #3a1e1e;
  flex-wrap: wrap;
}

.evaluation-badge {
  padding: 10px 14px;
  border-top: 1px solid #2a4a2a;
  background: #1e3a1e;
}

/* Evaluation section styles */
.evaluation-section {
  margin-top: 16px;
  background: #1a2e1a;
  border: 1px solid #2a4a2a;
  border-radius: 8px;
  overflow: hidden;
}

.evaluation-section.evaluation-failed-section {
  background: #2e1a1a;
  border-color: #4a2a2a;
}

.evaluation-section .final-result-header {
  background: #1e3a1e;
  border-bottom: 1px solid #2a4a2a;
  color: var(--accent-green);
}

.evaluation-section.evaluation-failed-section .final-result-header {
  background: #3a1e1e;
  border-bottom-color: #4a2a2a;
  color: var(--accent-red);
}

.evaluation-body {
  padding: 14px;
}

.evaluation-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.evaluation-score-label {
  font-size: 12px;
  color: #aaa;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.evaluation-score-value {
  font-size: 16px;
  font-weight: 700;
}

.evaluation-passed-score {
  color: #51cf66;
}

.evaluation-failed-score {
  color: #e74c3c;
}

.btn-continue {
  background: #4a9eff;
  color: #fff;
  border: none;
  border-radius: 6px;
  padding: 8px 14px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s, transform 0.15s;
}

.btn-continue:hover {
  background: #3a8eef;
  transform: translateY(-1px);
}

.continue-hint {
  font-size: 11px;
  color: #888;
}

/* Memory Browser overlay — rendered on top of main content with a dark
   backdrop. The panel takes most of the viewport and hosts the existing
   MemoryBrowser component. */
.memory-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.72);
  z-index: 100;
  display: flex;
  justify-content: center;
  padding: 24px;
  backdrop-filter: blur(2px);
}

.memory-overlay-panel {
  width: 100%;
  max-width: 1200px;
  height: calc(100vh - 40px);
  background: var(--bg-primary, #18181b);
  border: 1px solid var(--border-primary, #333);
  border-radius: 10px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.5);
}

.memory-overlay-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 14px;
  border-bottom: 1px solid var(--border-primary, #333);
  flex-shrink: 0;
  background: var(--bg-secondary, #1e1e22);
}

.memory-tabs {
  display: flex;
  gap: 6px;
}

.memory-tab {
  padding: 6px 12px;
  background: #2a2a2a;
  border: 1px solid #444;
  border-radius: 6px;
  color: #aaa;
  font-size: 13px;
  cursor: pointer;
  transition: all 0.15s;
  display: flex;
  align-items: center;
  gap: 6px;
}

.memory-tab:hover {
  background: #3a3a3a;
  color: #ddd;
}

.memory-tab.active {
  background: #4a9eff;
  border-color: #4a9eff;
  color: #fff;
}

.memory-tab-badge {
  padding: 1px 6px;
  background: rgba(0, 0, 0, 0.25);
  border-radius: 10px;
  font-size: 10px;
}

.memory-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.memory-action-btn {
  padding: 5px 12px;
  background: #2a2a2a;
  border: 1px solid #444;
  border-radius: 6px;
  color: #aaa;
  font-size: 12px;
  cursor: pointer;
  transition: all 0.15s;
}

.memory-action-btn:hover {
  background: #3a3a3a;
  color: #fff;
}

.memory-overlay-body {
  flex: 1;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.memory-overlay-browser,
.memory-overlay-rag,
.memory-overlay-events {
  flex: 1;
  overflow-y: auto;
}

/* Highlight generated when timeline selects a memory */
.timeline-highlight {
  animation: memory-flash 2s ease;
}

@keyframes memory-flash {
  0% { background: rgba(241, 196, 15, 0.2); }
  100% { background: transparent; }
}

.memory-close-btn {
  background: #2a2a2a;
  border: 1px solid #444;
  color: #ccc;
  font-size: 13px;
  padding: 5px 12px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.2s, color 0.2s;
  font-weight: 500;
}

.memory-close-btn:hover {
  background: #444;
  color: #fff;
}

.memory-overlay-browser {
  flex: 1;
  overflow-y: auto;
  padding: 0;
}

.memory-overlay.context-overlay {
  align-items: center;
  padding: 32px;
}

.memory-overlay.context-overlay .memory-overlay-panel.context-overlay-panel {
  max-width: 1100px;
  height: calc(100vh - 56px);
  border-radius: 18px;
  border: 1px solid rgba(255, 255, 255, 0.1);
  background: #121216;
  box-shadow: 0 30px 90px rgba(0, 0, 0, 0.7);
}

.memory-overlay.context-overlay .memory-overlay-header.context-overlay-header {
  padding: 14px 18px;
  background: rgba(255, 255, 255, 0.02);
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
}

.memory-overlay.context-overlay .context-overlay-content {
  flex: 1;
  overflow: hidden;
  border-bottom-left-radius: 18px;
  border-bottom-right-radius: 18px;
}

.memory-overlay.context-overlay .memory-tab.active {
  background: rgba(255, 255, 255, 0.08);
  border-color: rgba(255, 255, 255, 0.12);
  color: #fff;
}

/* Vue transition classes for the overlay fade/slide effect. */
.memory-overlay-enter-active,
.memory-overlay-leave-active {
  transition: opacity 0.25s ease;
}

.memory-overlay-enter-from,
.memory-overlay-leave-to {
  opacity: 0;
}

/* New Session dropdown menu */
.new-session-menu {
  position: absolute;
  top: 100%;
  left: 0;
  margin-top: 4px;
  background: #2a2a2e;
  border: 1px solid #444;
  border-radius: 8px;
  padding: 8px;
  z-index: 200;
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 260px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
}

.menu-auto-btn {
  background: #333;
  border: 1px solid #444;
  color: #ccc;
  font-size: 12px;
  padding: 7px 10px;
  border-radius: 6px;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s, color 0.15s;
}
.menu-auto-btn:hover {
  background: #3a3a3a;
  color: #fff;
}

.menu-separator {
  height: 1px;
  background: #444;
  margin: 2px 0;
}

.menu-path-input {
  background: #1a1a1e;
  border: 1px solid #444;
  color: #ddd;
  font-size: 12px;
  padding: 6px 8px;
  border-radius: 6px;
  outline: none;
  width: 100%;
  box-sizing: border-box;
  font-family: var(--font-mono, monospace);
}
.menu-path-input:focus {
  border-color: #4a9eff;
}

.menu-custom-btn {
  background: #4a9eff;
  border: none;
  color: #fff;
  font-size: 12px;
  font-weight: 600;
  padding: 7px 10px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.15s;
}
.menu-custom-btn:hover {
  background: #3a8eef;
}

.session-workspace-btn {
  background: transparent;
  border: none;
  font-size: 12px;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s, color 0.15s;
  padding: 0 3px;
  line-height: 1;
}
.session-item:hover .session-workspace-btn {
  opacity: 1;
}
.session-workspace-btn:hover {
  color: #4a9eff;
}

.session-workspace-path {
  font-size: 9px;
  color: #888;
  font-family: var(--font-mono, monospace);
  margin-left: 4px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 确保 handleGlobalClick 在 App.vue 中可用 */
.new-session-btn[title="New Session"] {
  position: relative;
}

.new-session-btn.project-btn {
  width: 28px;
  height: 28px;
  font-size: 14px;
  background: #444;
}
.new-session-btn.project-btn:hover {
  background: #555;
}
</style>
