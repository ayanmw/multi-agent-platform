<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch, nextTick } from 'vue'
import TopBar from './components/TopBar.vue'
import DockPanel from './components/DockPanel.vue'
import SessionDock from './components/SessionDock.vue'
import ManageContent from './components/ManageContent.vue'
import SessionFiles from './components/SessionFiles.vue'
import ColumnResizer from './components/ColumnResizer.vue'
import RowResizer from './components/RowResizer.vue'
import CommandBar from './components/CommandBar.vue'
import ContextFlyout from './components/ContextFlyout.vue'
import ManageFlyout from './components/ManageFlyout.vue'
import CronDockPanel from './components/CronDockPanel.vue'
import MobileNav from './components/MobileNav.vue'
import TimelineTrack from './components/TimelineTrack.vue'
import Toast from './components/Toast.vue'
import KeyboardTips from './components/KeyboardTips.vue'
import ApprovalDialog from './components/ApprovalDialog.vue'
import RecentModsDialog from './components/RecentModsDialog.vue'
import ModelPricesDialog from './components/ModelPricesDialog.vue'
import MCPServerDialog from './components/MCPServerDialog.vue'
import NewSessionDialog from './components/NewSessionDialog.vue'
import SessionEditDialog from './components/SessionEditDialog.vue'
import { useLayout } from './composables/useLayout'
import { useTaskStore } from './composables/useTaskStore'
import { useSessionStore } from './composables/useSessionStore'
import { useAgentStore } from './composables/useAgentStore'
import { useProjectStore } from './composables/useProjectStore'
import { useCaseStore } from './composables/useCaseStore'
import { useToast } from './composables/useToast'
import { useRecentMods } from './composables/useRecentMods'
import { useContextWindow } from './composables/useContextWindow'
import { useKeyboard, SHORTCUTS } from './composables/useKeyboard'
import { useSkills } from './composables/useSkills'
import { useSessionFiles } from './composables/useSessionFiles'
import { useTheme } from './composables/useTheme'
import type { Session } from './composables/useSessionStore'
import type { TaskState } from './types/events'

/**
 * App.vue — v2 Observable Control Room 根布局
 *
 * 布局策略（v2 三栏可调 + 中栏合并 + 底部 Context/Manage 浮窗）：
 * 桌面端/平板端：
 *   TopBar + 左 Dock（Sessions）+ 中栏（舞台 + RowResizer + CommandBar）+ 右 Dock（Files）。
 *   中栏内部上下分区：舞台占 Flex 剩余空间，底部输入区高度可拖拽调节（min 64 / max 40vh）。
 *   Context（🪟）：直接展示当前任务 context window，默认展开，无 Expand 按钮。
 *   Manage（🎛）：位于 TopBar 最右侧，下拉菜单，点击"展开管理"打开 90vw 大 Inspector Dialog。
 * 移动端（<768px）：单一内容区 + 底部 CommandBar + MobileNav，输入区高度固定 64px。
 *
 * 三栏宽度、中栏输入区高度均持久化到 localStorage。
 */
const {
  isMobile,
  isTablet,
  isDesktop,
  leftDockOpen,
  rightFilesOpen,
  activeMobileTab,
  leftDockWidth,
  rightFilesWidth,
  commandAreaHeight,
  setLeftDockWidth,
  setRightFilesWidth,
  setCommandAreaHeight,
  commitWidths,
  commitCommandHeight,
  resetWidths,
  toggleLeftDock,
  toggleRightFiles,
} = useLayout()

const {
  taskCache,
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
  updateSessionFields,
} = useSessionStore()

const { agents, availableTools, loadAgents } = useAgentStore()
const { projects, activeProjectId, loadProjects, setActiveProject } = useProjectStore()
const { toasts, showError, showInfo, dismissToast } = useToast()
const { loadSkills, enableSkill } = useSkills()
const { theme } = useTheme()
const caseStore = useCaseStore()
const { setActiveSession: setFilesSession, refreshDir: refreshFilesRoot } = useSessionFiles()

// === Skill / Multi-Agent 状态 ===
const multiAgentEnabled = ref(false)
const prefilledCommand = ref('')

function onMultiAgentChange(v: boolean) {
  multiAgentEnabled.value = v
}

// === Session 编辑弹窗状态 ===
// sessionEditTarget：当前编辑的 session；sessionEditVisible：弹窗显隐。
// 用 ref 引用 SessionEditDialog 实例，保存失败时调用其 failWith 显示错误。
const sessionEditTarget = ref<Session | null>(null)
const sessionEditVisible = ref(false)
const sessionEditDialogRef = ref<InstanceType<typeof SessionEditDialog> | null>(null)

// === 弹窗可见性 ===
const recentModsVisible = ref(false)
const modelPricesVisible = ref(false)
const mcpServerDialogVisible = ref(false)
const newSessionDialogVisible = ref(false)
const newSessionProjectId = ref<string | undefined>(undefined)

const {
  items: recentMods,
  toggle: toggleRecentMods,
  clear: clearRecentMods,
} = useRecentMods()

function showRecentMods() {
  recentModsVisible.value = true
}

// === 上下文窗口 ===
const { setActiveTaskId: setContextWindowTaskId, clear: clearContextWindow } = useContextWindow()

watch(activeTaskId, (taskId) => {
  setContextWindowTaskId(taskId || '')
})

// 右栏文件浏览器跟随当前 session：切换 session 时通知 useSessionFiles 重置缓存。
watch(activeSessionId, (sid) => {
  if (sid) setFilesSession(sid)
}, { immediate: true })

// === 管理（原 Inspector）大 Dialog ===
// 7/20: Context 已经抽到 CommandBar 右侧浮窗；此处只保留用于"展开管理"的 90vw Dialog。
const inspectorDialogOpen = ref(false)
const inspectorInitialTab = ref<string>('memory')

// Context 与 Manage 浮窗开关状态
const contextFlyoutOpen = ref(false)
const manageFlyoutOpen = ref(false)
// 右侧 Cron 侧边面板（可折叠），桌面/平板均可用。
const rightCronOpen = ref(false)
const commandBarRef = ref<InstanceType<typeof CommandBar> | null>(null)
const contextAnchorRect = ref<DOMRect | null>(null)

// 当 Context 浮窗打开时，从 CommandBar 获取按钮位置。
watch(contextFlyoutOpen, (open) => {
  if (open) {
    nextTick(() => {
      contextAnchorRect.value = commandBarRef.value?.getContextAnchor?.() ?? null
    })
  }
})

function updateContextAnchor() {
  if (contextFlyoutOpen.value) {
    contextAnchorRect.value = commandBarRef.value?.getContextAnchor?.() ?? null
  }
}

// 窗口大小变化时，若 Context 浮窗打开则重新计算锚点。
if (typeof window !== 'undefined') {
  window.addEventListener('resize', updateContextAnchor)
}

// 打开管理大 Dialog，并可选地定位到指定 tab。
function openInspectorDialog(tab?: string) {
  // 菜单项明确指定 tab 时用该 tab；否则保留上次的 inspectorInitialTab（默认 memory），
  // 让"展开管理"按钮的行为稳定、不总是跳回 sessions。
  if (tab) inspectorInitialTab.value = tab
  inspectorDialogOpen.value = true
}
function closeInspectorDialog() {
  inspectorDialogOpen.value = false
}

/** 打开管理大 Dialog 并定位到 cron tab；可携带 focusCronId 供 CronManager 直接展开某条。 */
const inspectorFocusCronId = ref('')
function openCronManage(cronId?: string) {
  inspectorInitialTab.value = 'cron'
  inspectorFocusCronId.value = cronId || ''
  inspectorDialogOpen.value = true
}

// === 当前任务/会话派生状态 ===
const currentTask = computed(() => {
  if (!activeTaskId.value) return null
  return taskCache.value[activeTaskId.value] || null
})

const isAgentRunning = computed(() => currentTask.value?.status === 'running')
const isTaskIdle = computed(() => currentTask.value?.status === 'idle')

/** 当前会话级别总 token 数（跨所有 turn） */
const sessionTotalTokens = computed(() => {
  const sid = activeSession.value?.id
  if (!sid) return 0
  return Object.values(taskCache.value)
    .filter(t => t.sessionId === sid)
    .reduce((sum, t) => sum + (t.totalTokens || 0), 0)
})

/** 当前会话级别总耗时（ms） */
const sessionTotalDuration = computed(() => {
  const sid = activeSession.value?.id
  if (!sid) return 0
  return Object.values(taskCache.value)
    .filter(t => t.sessionId === sid)
    .reduce((sum, t) => sum + (t.durationMs || 0), 0)
})

/** 供 CommandBar / OptionsFlyout 使用的 agent 精简列表 */
const agentOptions = computed(() =>
  agents.value.map(a => ({
    id: a.id,
    name: a.name,
    model: a.model || '',
    tools: a.tools || [],
  })),
)

/** 供 OptionsFlyout 使用的可用工具列表 */
const availableToolOptions = computed(() =>
  availableTools.value.map(t => ({ name: t.name, description: t.description })),
)

/** 当前会话按时间排序的 turn 列表，用于主舞台 TimelineTrack */
const sessionTurns = computed(() => {
  const sid = activeSession.value?.id
  const turns: Array<{ task: TaskState; userInput: string }> = []
  if (!sid) return turns

  const tasks = Object.values(taskCache.value)
    .filter(t => t.sessionId === sid)
    .sort((a, b) => a.startedAt - b.startedAt)

  for (const t of tasks) {
    turns.push({
      task: t,
      userInput: t.userInput || lastUserInput.value,
    })
  }
  return turns
})

// === 任务运行状态同步键盘 shortcut ===
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

watch(
  () => currentTask.value?.status,
  (status) => {
    kbIsRunning.value = status === 'running'
  },
)

// === 自动滚动 ===
// v2 之前只在 sessionTurns.length 变化时滚动，但 step/agent_status/llm_delta 等事件
// 会让现有 turn 内部内容长高而不增加 turn 数，于是滚动失效。
// 现在改为：用 task 版本号指纹（status + steps 长度 + totalTokens + 各 agent 步数和）
// 触发滚动；若用户主动向上离开底部则暂停，直到回到底部或点回底部。
const mainRef = ref<HTMLElement | null>(null)
const autoScrollPaused = ref(false)
const BOTTOM_THRESHOLD = 80

function checkNearBottom(): boolean {
  const el = mainRef.value
  if (!el) return true
  return el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_THRESHOLD
}

function scrollToBottom() {
  const el = mainRef.value
  if (!el) return
  el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' })
  autoScrollPaused.value = false
}

function handleMainScroll() {
  const near = checkNearBottom()
  if (near) {
    autoScrollPaused.value = false
  } else if (!autoScrollPaused.value) {
    autoScrollPaused.value = true
  }
}

/** 任务内容指纹：任何让主舞台内容长高的事件都会改变它。 */
const taskFingerprint = computed(() => {
  const t = currentTask.value
  if (!t) return ''
  let steps = 0
  for (const a of Object.values(t.agents || {})) {
    steps += a.steps?.length || 0
  }
  return `${t.status}|${steps}|${t.totalTokens || 0}|${t.durationMs || 0}|${sessionTurns.value.length}`
})

// turn 数变化（新 turn 出现）→ 滚动到底
watch(
  () => sessionTurns.value.length,
  () => {
    if (!autoScrollPaused.value) {
      nextTick(scrollToBottom)
    }
  },
)

// 任务指纹变化（现有 turn 内部内容长高）→ 滚动到底
watch(
  taskFingerprint,
  () => {
    if (!autoScrollPaused.value) {
      nextTick(scrollToBottom)
    }
  },
)

// 切换 session/task 时重置暂停状态并尝试滚动到底
watch(activeTaskId, () => {
  autoScrollPaused.value = false
  nextTick(scrollToBottom)
})

// === 页面加载：连接 WS，加载项目/会话/Agent/Case ===
onMounted(async () => {
  connect()
  try {
    await loadProjects()
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to load projects')
  }
  try {
    await loadSessions(activeProjectId.value)
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to load sessions')
  }
  try {
    await loadAgents()
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to load agents')
  }
  try {
    await caseStore.loadCases()
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to load cases')
  }
  try {
    await loadSkills()
  } catch (err) {
    // Skill 列表加载失败不阻塞主流程，useSkills 内部已回退到静态列表
    console.warn('[App] Failed to load skills:', err)
  }

  // 最近修改弹窗自动提示
  if (recentMods.value.length > 0) {
    showRecentMods()
  }
})

onUnmounted(() => {
  disconnect()
})

// 快捷键：Ctrl+M 打开最近修改
function handleGlobalKeydown(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'm') {
    e.preventDefault()
    recentModsVisible.value = !recentModsVisible.value
  }
}
onMounted(() => {
  window.addEventListener('keydown', handleGlobalKeydown)
})
onUnmounted(() => {
  window.removeEventListener('keydown', handleGlobalKeydown)
})

// === 全局滚轮：在标题/空白处滚动时驱动主舞台滚动，
// 左右 Dock 与底部输入区保持自身滚动独立。
const SCROLLABLE_SELECTORS = ['.dock-body', '.command-area', '.context-flyout-body', '.context-flyout']
function findScrollableAncestor(el: EventTarget | null): HTMLElement | null {
  let node: Node | null = el as Node
  while (node && node instanceof HTMLElement) {
    if (node === mainRef.value) return null
    if (SCROLLABLE_SELECTORS.some(s => node && (node as Element).matches?.(s))) {
      return node as HTMLElement
    }
    const style = window.getComputedStyle(node as HTMLElement)
    if (style.overflowY === 'auto' || style.overflowY === 'scroll') {
      return node as HTMLElement
    }
    node = node.parentNode
  }
  return null
}
function handleGlobalWheel(e: WheelEvent) {
  if (e.deltaY === 0) return
  const target = e.target as HTMLElement
  if (target.closest('.inspector-dialog-overlay, .modal, .dialog, .manage-flyout, .context-flyout')) return

  const scrollable = findScrollableAncestor(target)
  if (scrollable) {
    const canScrollDown = scrollable.scrollHeight > scrollable.clientHeight
      && scrollable.scrollTop + scrollable.clientHeight < scrollable.scrollHeight - 2
    const canScrollUp = scrollable.scrollTop > 2
    if ((e.deltaY > 0 && canScrollDown) || (e.deltaY < 0 && canScrollUp)) {
      // 让子滚动区域自由处理
      return
    }
  }

  const main = mainRef.value
  if (!main) return
  e.preventDefault()
  main.scrollBy({ top: e.deltaY, behavior: 'auto' })
}
onMounted(() => {
  window.addEventListener('wheel', handleGlobalWheel, { passive: false })
})
onUnmounted(() => {
  window.removeEventListener('wheel', handleGlobalWheel)
})

// === 发送消息 ===
async function handleSend(text: string, options: { maxSteps: number; timeoutSeconds: number }) {
  // Skill 前缀解析：/skill-id <rest> 或 /skill-id
  const skillMatch = text.match(/^\/([a-zA-Z0-9_-]+)\s+(.*)$/) || text.match(/^\/([a-zA-Z0-9_-]+)$/)
  if (skillMatch) {
    const skillId = skillMatch[1]
    const remaining = skillMatch[2] || ''
    try {
      await enableSkill(skillId)
    } catch (err) {
      if (err instanceof Error && err.message === 'forbidden') {
        showError(`Skill ${skillId} cannot be enabled.`)
        return
      }
      showError(err instanceof Error ? err.message : `Failed to enable skill ${skillId}`)
      return
    }
    text = remaining
  }

  try {
    const session = activeSession.value
    if (!session) {
      const newSession = await createSession(undefined, text, activeProjectId.value)
      setActiveSession(newSession.id)
      if (multiAgentEnabled.value && !text.startsWith('/')) {
        await startMultiAgentTask(text, {
          sessionId: newSession.id,
          maxSteps: options.maxSteps,
          timeoutSeconds: options.timeoutSeconds,
        })
      } else {
        await startTask(text, {
          sessionId: newSession.id,
          maxSteps: options.maxSteps,
          timeoutSeconds: options.timeoutSeconds,
        })
      }
    } else if (!session.rootTaskId) {
      if (multiAgentEnabled.value && !text.startsWith('/')) {
        await startMultiAgentTask(text, {
          sessionId: session.id,
          maxSteps: options.maxSteps,
          timeoutSeconds: options.timeoutSeconds,
        })
      } else {
        await startTask(text, {
          sessionId: session.id,
          maxSteps: options.maxSteps,
          timeoutSeconds: options.timeoutSeconds,
        })
      }
    } else {
      if (multiAgentEnabled.value && !text.startsWith('/')) {
        // Phase 7-H2: multi-agent is also a root task; treat it like the
        // first-turn case so it starts a new leader-driven task.
        await startMultiAgentTask(text, {
          sessionId: session.id,
          maxSteps: options.maxSteps,
          timeoutSeconds: options.timeoutSeconds,
        })
      } else {
        await startTurn(text, {
          sessionId: session.id,
          maxSteps: options.maxSteps,
          timeoutSeconds: options.timeoutSeconds,
        })
      }
    }
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to start task')
  }
}

// === Multi-Agent lane helpers (leader-driven) ===
// Worker lanes are created from WebSocket events. The TODO below refers to
// a legacy workflow component migration and is no longer blocking.

// === 项目/会话选择 ===
async function handleProjectSelect(projectId: string) {
  if (projectId === activeProjectId.value) return
  // 切换前读取目标 project 的 rules，用于切换后的"规则已生效"提示。
  // 这里用切换前的 projects 快照即可：loadProjects 之后 projects 已是最新。
  const targetProject = projects.value.find(p => p.id === projectId) || null
  const targetRules = extractProjectRules(targetProject)
  setActiveProject(projectId)
  clearActiveTask()
  clearContextWindow()
  try {
    await loadSessions(projectId)
    // project 切换时若新 project 配有 rules，明确提示用户：后续在该 project 下
    // 新建/发起的 session 会自动注入这段规则，避免用户以为规则"没生效"。
    // 仅在有 rules 时提示，避免每次切换都弹 toast。
    if (targetRules) {
      const preview = targetRules.length > 60 ? targetRules.slice(0, 60) + '…' : targetRules
      showInfo(`已切换到 project「${targetProject?.name || projectId}」，其规则将注入新会话：${preview}`)
    }
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to load sessions')
  }
}

/** 从 project.config.rules 提取规则文本（与 ProjectConfig.readRules 同源）。 */
function extractProjectRules(p: { config?: Record<string, unknown> } | null): string {
  if (!p || !p.config) return ''
  const r = p.config.rules
  return typeof r === 'string' ? r.trim() : ''
}

async function handleSessionSelect(session: Session) {
  setActiveSession(session.id)
  pruneOrphanTasks()
  clearActiveTask()
  clearContextWindow()

  // 清理其他会话的缓存任务，避免污染当前时间线
  const sid = session.id
  for (const tid of Object.keys(taskCache.value)) {
    const t = taskCache.value[tid]
    if (t.sessionId && t.sessionId !== sid) {
      delete taskCache.value[tid]
    }
  }
  for (const tid of Object.keys(taskCache.value)) {
    const t = taskCache.value[tid]
    if (!t.sessionId) {
      delete taskCache.value[tid]
    }
  }

  if (session.rootTaskId) {
    try {
      await loadSessionTurns(sid)
      const ordered = Object.values(taskCache.value)
        .filter(t => t.sessionId === sid)
        .sort((a, b) => a.startedAt - b.startedAt)
      if (ordered.length > 0) {
        setActiveTaskId(ordered[ordered.length - 1].id)
      } else {
        setActiveTaskId(null)
      }
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Failed to load session turns')
    }
  }
}

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
            setActiveTaskId(keys[keys.length - 1])
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

// === Session 编辑弹窗 ===
// 从 SessionDock 的 ✎ 按钮打开，展示完整 session 信息并支持改名 + 改 workspace。
function openSessionEdit(session: Session) {
  sessionEditTarget.value = session
  sessionEditVisible.value = true
}

function closeSessionEdit() {
  sessionEditVisible.value = false
  // 稍延迟清空 target，避免关闭动画期间内容闪空。
  sessionEditTarget.value = null
}

/** SessionEditDialog @save：根据 workspaceMode 决定传给后端的 workspaceDir。
 *  - keep：不传 workspace_dir，后端保留旧值（仅重命名）
 *  - auto：传空串，后端清空 workspace_dir，回退 auto/project
 *  - custom：传自定义路径，后端确保目录存在并切换指针 */
async function handleSessionEditSave(payload: { name: string; workspaceMode: 'keep' | 'auto' | 'custom'; customPath: string }) {
  const target = sessionEditTarget.value
  if (!target) return
  try {
    let workspaceDir: string | undefined
    if (payload.workspaceMode === 'auto') workspaceDir = ''
    else if (payload.workspaceMode === 'custom') workspaceDir = payload.customPath
    // keep → workspaceDir 保持 undefined，updateSessionFields 不会带 workspace_dir 字段
    await updateSessionFields(target.id, { name: payload.name, workspaceDir })
    sessionEditVisible.value = false
    sessionEditTarget.value = null
    showInfo('Session 已更新')
  } catch (err) {
    const msg = err instanceof Error ? err.message : 'Failed to update session'
    // 通过 dialog 暴露的 failWith 在弹窗内显示错误并保持打开，让用户改完再试。
    sessionEditDialogRef.value?.failWith(msg)
  }
}

// === 审批对话框 ===
function handleApprove(approvalId: string) {
  if (!pendingApproval.value) return
  approveTask(approvalId, pendingApproval.value.taskId, pendingApproval.value.agentId)
}

function handleDeny(approvalId: string) {
  if (!pendingApproval.value) return
  denyTask(approvalId, pendingApproval.value.taskId, pendingApproval.value.agentId)
}

function handleApprovalClose() {
  if (pendingApproval.value) {
    denyTask(
      pendingApproval.value.approvalId,
      pendingApproval.value.taskId,
      pendingApproval.value.agentId,
    )
  }
  pendingApproval.value = null
}

// === Agent 控制 ===
function handlePauseAgent(payload: { taskId: string; agentId: string }) {
  setActiveTaskId(payload.taskId)
  pauseTask(payload.agentId)
}
function handleResumeAgent(payload: { taskId: string; agentId: string }) {
  setActiveTaskId(payload.taskId)
  resumeTask(payload.agentId)
}
function handleCancelAgent(payload: { taskId: string; agentId: string }) {
  setActiveTaskId(payload.taskId)
  cancelTask(payload.agentId)
}

// === 运行 case ===
async function handleRunCase(caseId: string) {
  // 点 Run 后立即关闭 Inspector 大 Dialog，避免它遮挡刚启动的 timeline，
  // 也符合"运行 case 就是把它送进主舞台"的直觉。
  inspectorDialogOpen.value = false
  try {
    let session = activeSession.value
    if (!session) {
      session = await createSession(undefined, `Case: ${caseId}`, activeProjectId.value)
      setActiveSession(session.id)
    }
    await startTaskWithCase(caseId, { sessionId: session.id })
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to run case')
  }
}

// === Skill 触发 ===
async function handleTriggerSkill(command: string) {
  const match = command.match(/^\/([a-zA-Z0-9_-]+)(?:\s+.*)?$/)
  if (match) {
    prefilledCommand.value = '/' + match[1] + ' '
  }
}

// === TopBar 状态文字 ===
const statusLabel = computed(() => {
  switch (wsStatus.value) {
    case 'connected':
      return 'Connected'
    case 'connecting':
      return 'Connecting...'
    case 'disconnected':
    default:
      return 'Disconnected'
  }
})

const taskStatusLabel = computed(() => {
  if (isTaskPending.value) return 'Pending'
  if (isAgentRunning.value) return 'Running'
  if (currentTask.value?.status === 'failed') return 'Failed'
  if (currentTask.value?.status === 'completed') return 'Completed'
  return 'Ready'
})

const connectionStatus = computed<'idle' | 'running' | 'paused' | 'completed' | 'failed' | 'pending'>(() => {
  if (isAgentRunning.value) return 'running'
  if (wsStatus.value === 'connected') return 'completed'
  if (wsStatus.value === 'connecting') return 'pending'
  return 'idle'
})

// === 文件浏览器：write_file 后刷新当前 session 根目录 ===
// recentMods 是 write_file 成功的全局日志，每当它新增一项，说明有文件落盘，
// 顺手刷新当前 session 的根目录列表，让新文件立刻可见。
watch(
  () => recentMods.value.length,
  () => {
    const sid = activeSessionId.value
    if (!sid) return
    // 仅刷新根目录，避免打扰用户已展开的深层目录。
    refreshFilesRoot(sid, '')
  },
)

const showInspectorToggle = computed(() => false)

const newSessionTargetProject = computed(() => {
  const pid = newSessionProjectId.value || activeProjectId.value
  return projects.value.find(p => p.id === pid) || null
})

function openNewSessionDialog(projectId?: string) {
  newSessionProjectId.value = projectId
  newSessionDialogVisible.value = true
}

function closeNewSessionDialog() {
  newSessionDialogVisible.value = false
  newSessionProjectId.value = undefined
}

async function handleCreateSession(payload: { name: string; workspaceDir: string }) {
  const pid = newSessionProjectId.value || activeProjectId.value
  if (!pid) {
    showError('No project selected')
    return
  }
  const workspaceDir = payload.workspaceDir || undefined
  try {
    const session = await createSession(payload.name || undefined, undefined, pid, workspaceDir)
    setActiveSession(session.id)
    clearActiveTask()
    clearContextWindow()
    closeNewSessionDialog()
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to create session')
  }
}
</script>

<template>
  <div class="app-shell" :data-theme="theme">
    <TopBar
      :status="connectionStatus"
      :status-label="statusLabel"
      :task-status-label="taskStatusLabel"
      :show-inspector-toggle="showInspectorToggle"
      :manage-open="manageFlyoutOpen"
      :cron-open="rightCronOpen"
      @toggle-left-dock="toggleLeftDock"
      @toggle-recent-mods="showRecentMods"
      @toggle-model-prices="modelPricesVisible = true"
      @toggle-mcp="mcpServerDialogVisible = true"
      @toggle-keyboard-tips="showTips = true"
      @toggle-manage="manageFlyoutOpen = !manageFlyoutOpen"
      @toggle-cron="rightCronOpen = !rightCronOpen"
    />

    <!-- 桌面三栏布局：左 Sessions | 主舞台 | 右 Files，宽度可拖拽 -->
    <div v-if="isDesktop" class="layout-desktop" :style="{ '--left-w': leftDockWidth + 'px', '--right-w': rightFilesWidth + 'px' }">
      <DockPanel side="left" title="Sessions" :open="leftDockOpen" @close="toggleLeftDock" @reopen="toggleLeftDock">
        <SessionDock
          :projects="projects"
          :active-project-id="activeProjectId"
          :sessions="sessions"
          :active-session-id="activeSessionId"
          @select-project="handleProjectSelect"
          @select-session="handleSessionSelect"
          @new-session-request="openNewSessionDialog"
          @delete-session="handleDeleteSession"
          @edit-session="openSessionEdit"
        />
      </DockPanel>

      <ColumnResizer
        v-if="leftDockOpen"
        side="left"
        :width="leftDockWidth"
        @resize="setLeftDockWidth"
        @resize-end="commitWidths"
      />

      <section class="center-column" :style="{ '--cmd-h': commandAreaHeight + 'px' }">
        <main ref="mainRef" class="main-stage" @scroll="handleMainScroll">
          <div v-if="autoScrollPaused" class="scroll-paused-hint" @click="scrollToBottom">
            Auto-scroll paused — click to resume
          </div>

          <div v-if="sessionTurns.length === 0" class="placeholder-panel">
            <div class="placeholder-title">Ready to start</div>
            <div class="placeholder-hint">
              Select or create a session from the left dock, then type a task below.
            </div>
          </div>

          <TimelineTrack
            v-for="(turn, idx) in sessionTurns"
            :key="turn.task.id"
            :task="turn.task"
            :turn-index="idx + 1"
            :user-input="turn.userInput"
            :expand-all="true"
            :show-agent-controls="isAgentRunning"
            @pause-agent="handlePauseAgent"
            @resume-agent="handleResumeAgent"
            @cancel-agent="handleCancelAgent"
          />
        </main>

        <RowResizer
          :height="commandAreaHeight"
          @resize="setCommandAreaHeight"
          @resize-end="commitCommandHeight"
        />

        <div class="command-area">
          <CommandBar
            ref="commandBarRef"
            :disabled="isAgentRunning"
            :is-running="isAgentRunning"
            :is-pending="isTaskPending"
            :prefill="prefilledCommand"
            v-model:context-open="contextFlyoutOpen"
            :context-anchor-rect="contextAnchorRect"
            :agents="agentOptions"
            :available-tools="availableToolOptions"
            @send="handleSend"
            @pause="pauseTask"
            @resume="resumeTask"
            @cancel="cancelTask"
            @update:prefill="prefilledCommand = ''"
            @update:multiAgent="onMultiAgentChange"
            @multiAgentChange="onMultiAgentChange"
            @open-cases="openInspectorDialog('cases')"
            @open-agents="openInspectorDialog('agents')"
          />
        </div>
      </section>

      <ColumnResizer
        v-if="rightFilesOpen"
        side="right"
        :width="rightFilesWidth"
        @resize="setRightFilesWidth"
        @resize-end="commitWidths"
      />

      <DockPanel side="right" title="Files" :open="rightFilesOpen" @close="toggleRightFiles" @reopen="toggleRightFiles">
        <SessionFiles :session-id="activeSessionId || ''" />
      </DockPanel>

      <!-- 右侧 Cron 侧边面板：只读相关定时器 + 实时触发流，可一键跳转管理 tab -->
      <CronDockPanel
        :open="rightCronOpen"
        :session-id="activeSessionId || ''"
        @update:open="rightCronOpen = $event"
        @open-manage="openCronManage"
      />
    </div>

    <!-- 平板双栏布局 -->
    <div v-else-if="isTablet" class="layout-tablet" :style="{ '--cmd-h': commandAreaHeight + 'px' }">
      <DockPanel
        v-if="leftDockOpen"
        side="left"
        title="Sessions"
        :open="true"
        @close="toggleLeftDock"
      >
        <SessionDock
          :projects="projects"
          :active-project-id="activeProjectId"
          :sessions="sessions"
          :active-session-id="activeSessionId"
          @select-project="handleProjectSelect"
          @select-session="handleSessionSelect"
          @new-session-request="openNewSessionDialog"
          @delete-session="handleDeleteSession"
          @edit-session="openSessionEdit"
        />
      </DockPanel>

      <section class="center-column center-column--tablet">
        <main ref="mainRef" class="main-stage" @scroll="handleMainScroll">
          <div v-if="autoScrollPaused" class="scroll-paused-hint" @click="scrollToBottom">
            Auto-scroll paused — click to resume
          </div>

          <div v-if="sessionTurns.length === 0" class="placeholder-panel">
            <div class="placeholder-title">Ready to start</div>
            <div class="placeholder-hint">Create a session and type a task below.</div>
          </div>

          <TimelineTrack
            v-for="(turn, idx) in sessionTurns"
            :key="turn.task.id"
            :task="turn.task"
            :turn-index="idx + 1"
            :user-input="turn.userInput"
            :expand-all="true"
            :show-agent-controls="isAgentRunning"
            @pause-agent="handlePauseAgent"
            @resume-agent="handleResumeAgent"
            @cancel-agent="handleCancelAgent"
          />
        </main>

        <RowResizer
          :height="commandAreaHeight"
          @resize="setCommandAreaHeight"
          @resize-end="commitCommandHeight"
        />

        <div class="command-area">
          <CommandBar
            ref="commandBarRef"
            :disabled="isAgentRunning"
            :is-running="isAgentRunning"
            :is-pending="isTaskPending"
            :prefill="prefilledCommand"
            v-model:context-open="contextFlyoutOpen"
            :context-anchor-rect="contextAnchorRect"
            :agents="agentOptions"
            :available-tools="availableToolOptions"
            @send="handleSend"
            @pause="pauseTask"
            @resume="resumeTask"
            @cancel="cancelTask"
            @update:prefill="prefilledCommand = ''"
            @update:multiAgent="onMultiAgentChange"
            @multiAgentChange="onMultiAgentChange"
            @open-cases="openInspectorDialog('cases')"
            @open-agents="openInspectorDialog('agents')"
          />
        </div>
      </section>

      <DockPanel
        v-if="rightFilesOpen"
        side="right"
        title="Files"
        :open="true"
        @close="toggleRightFiles"
      >
        <SessionFiles :session-id="activeSessionId || ''" />
      </DockPanel>

      <!-- 平板端同样提供 Cron 侧边面板 -->
      <CronDockPanel
        :open="rightCronOpen"
        :session-id="activeSessionId || ''"
        @update:open="rightCronOpen = $event"
        @open-manage="openCronManage"
      />
    </div>
    <div v-else class="layout-mobile">
      <main v-if="activeMobileTab === 'stage'" class="main-stage mobile-tab-view" @scroll="handleMainScroll">
        <div v-if="sessionTurns.length === 0" class="placeholder-panel">
          <div class="placeholder-title">Stage</div>
          <div class="placeholder-hint">Select a session and type a task.</div>
        </div>

        <TimelineTrack
          v-for="(turn, idx) in sessionTurns"
          :key="turn.task.id"
          :task="turn.task"
          :turn-index="idx + 1"
          :user-input="turn.userInput"
          :expand-all="true"
          :show-agent-controls="isAgentRunning"
          @pause-agent="handlePauseAgent"
          @resume-agent="handleResumeAgent"
          @cancel-agent="handleCancelAgent"
        />
      </main>
      <div v-else-if="activeMobileTab === 'sessions'" class="mobile-tab-view">
        <DockPanel side="left" title="Sessions" :open="true" @close="activeMobileTab = 'stage'">
          <SessionDock
            :projects="projects"
            :active-project-id="activeProjectId"
            :sessions="sessions"
            :active-session-id="activeSessionId"
            @select-project="handleProjectSelect"
            @select-session="handleSessionSelect"
            @new-session-request="openNewSessionDialog"
            @delete-session="handleDeleteSession"
            @edit-session="openSessionEdit"
          />
        </DockPanel>
      </div>
      <div v-else-if="activeMobileTab === 'files'" class="mobile-tab-view">
        <DockPanel side="right" title="Files" :open="true" @close="activeMobileTab = 'stage'">
          <SessionFiles :session-id="activeSessionId || ''" />
        </DockPanel>
      </div>
    </div>

    <!-- 移动端底部 CommandBar 单独放置，桌面/平板已由中栏承载 -->
    <CommandBar
      v-if="isMobile && activeMobileTab === 'stage'"
      ref="commandBarRef"
      class="command-bar-mobile"
      :disabled="isAgentRunning"
      :is-running="isAgentRunning"
      :is-pending="isTaskPending"
      :prefill="prefilledCommand"
      v-model:context-open="contextFlyoutOpen"
      :context-anchor-rect="contextAnchorRect"
      :agents="agentOptions"
      :available-tools="availableToolOptions"
      @send="handleSend"
      @pause="pauseTask"
      @resume="resumeTask"
      @cancel="cancelTask"
      @update:prefill="prefilledCommand = ''"
      @update:multiAgent="onMultiAgentChange"
      @multiAgentChange="onMultiAgentChange"
      @open-cases="openInspectorDialog('cases')"
      @open-agents="openInspectorDialog('agents')"
    />

    <ContextFlyout
      :active-task-id="activeTaskId ?? ''"
      :session-total-tokens="sessionTotalTokens"
      :session-total-duration="sessionTotalDuration"
      :ws-status="wsStatus"
      :agents="agents"
      :anchor-rect="contextAnchorRect"
      v-model:open="contextFlyoutOpen"
    />
    <ManageFlyout v-model:open="manageFlyoutOpen" @expand="openInspectorDialog" />

    <!-- Inspector 大 Dialog（90vw）：承载管理面板 -->
    <Teleport to="body">
      <Transition name="inspector-dialog">
        <div v-if="inspectorDialogOpen" class="inspector-dialog-overlay" @click.self="closeInspectorDialog">
          <div class="inspector-dialog-panel">
            <div class="inspector-dialog-header">
              <span class="inspector-dialog-title">🎛 管理</span>
              <div class="inspector-dialog-actions">
                <button class="inspector-dialog-reset" title="Reset column widths" @click="resetWidths">↺ Reset Layout</button>
                <button class="inspector-dialog-close" @click="closeInspectorDialog" title="Close">×</button>
              </div>
            </div>
            <div class="inspector-dialog-body">
              <ManageContent
                :initial-tab="inspectorInitialTab"
                :focus-cron-id="inspectorFocusCronId"
                @run-case="handleRunCase"
                @trigger-skill="handleTriggerSkill"
              />
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>

    <NewSessionDialog
      :visible="newSessionDialogVisible"
      :project-id="newSessionTargetProject?.id || activeProjectId"
      :project-name="newSessionTargetProject?.name || activeProjectId"
      :project-working-directory="newSessionTargetProject?.working_directory || ''"
      @close="closeNewSessionDialog"
      @create="handleCreateSession"
    />

    <!-- Session 编辑弹窗：完整 session 信息展示 + 改名/改 workspace。
         projectName 用于只读 badge；session 为 null 时弹窗内部不渲染内容。 -->
    <SessionEditDialog
      ref="sessionEditDialogRef"
      :visible="sessionEditVisible"
      :session="sessionEditTarget"
      :project-name="projects.find(p => p.id === sessionEditTarget?.projectId)?.name || sessionEditTarget?.projectId || ''"
      @close="closeSessionEdit"
      @save="handleSessionEditSave"
    />

    <!-- Toast / Dialogs -->
    <Toast :toasts="toasts" @dismiss="dismissToast" />

    <KeyboardTips
      :visible="showTips"
      :shortcuts="SHORTCUTS"
      :is-running="isAgentRunning"
      @close="showTips = false"
    />

    <ApprovalDialog
      :approval-id="pendingApproval?.approvalId ?? ''"
      :tool="pendingApproval?.tool ?? ''"
      :rule="pendingApproval?.rule"
      :namespace="pendingApproval?.namespace"
      :tags="pendingApproval?.tags"
      :reason="pendingApproval?.reason ?? ''"
      :input="pendingApproval?.input ?? {}"
      :auto-approve="false"
      :visible="pendingApproval !== null"
      :error="pendingApproval?.error"
      @approve="handleApprove"
      @deny="handleDeny"
      @close="handleApprovalClose"
    />

    <RecentModsDialog
      :visible="recentModsVisible"
      :items="recentMods"
      @update:visible="recentModsVisible = $event"
      @clear="clearRecentMods"
    />

    <ModelPricesDialog
      :visible="modelPricesVisible"
      @update:visible="modelPricesVisible = $event"
    />

    <MCPServerDialog
      :visible="mcpServerDialogVisible"
      @update:visible="mcpServerDialogVisible = $event"
    />
  </div>
</template>

<style scoped>
.app-shell {
  display: flex;
  flex-direction: column;
  width: 100vw;
  height: 100dvh;
  height: 100vh;
  overflow: hidden;
  background: var(--bg-canvas, #0b0d10);
}

.layout-desktop,
.layout-tablet,
.layout-mobile {
  flex: 1;
  display: flex;
  min-height: 0;
  margin-top: var(--topbar-height, 48px);
}

.layout-mobile {
  margin-bottom: calc(var(--commandbar-height, 64px) + var(--mobile-nav-height, 56px));
}

.center-column {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  height: calc(100dvh - var(--topbar-height, 48px));
  height: calc(100vh - var(--topbar-height, 48px));
}

.center-column--tablet {
  /* 平板端 width 由 layout-tablet 的 flex 分配，这里不需要额外限制 */
}

.main-stage {
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-md);
  background: var(--bg-canvas, #0b0d10);
  position: relative;
}

.command-area {
  flex-shrink: 0;
  height: var(--cmd-h, 64px);
  min-height: var(--cmd-h, 64px);
  overflow: hidden;
  background: var(--bg-panel, #11141a);
  border-top: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
}

.command-area :deep(.command-bar) {
  position: static;
  inset: auto;
  border-top: none;
  height: 100%;
  padding-bottom: 0;
}

.command-area :deep(.command-main) {
  align-items: flex-start;
  height: 100%;
  padding-top: 6px;
  padding-bottom: 6px;
  box-sizing: border-box;
}

.command-area :deep(.command-input) {
  min-height: 0;
  max-height: 100%;
  align-self: stretch;
}

.mobile-tab-view {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.placeholder-panel {
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: flex-start;
  padding-top: 20%;
  color: var(--text-muted, #5c6675);
  text-align: center;
  gap: var(--space-sm);
}

.placeholder-title {
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  font-size: 18px;
  color: var(--text-secondary, #9aa3b2);
}

.placeholder-hint {
  font-size: 13px;
  font-family: var(--font-mono, monospace);
  max-width: 280px;
  line-height: 1.5;
}

.scroll-paused-hint {
  position: sticky;
  top: var(--space-sm);
  z-index: 10;
  margin-bottom: var(--space-sm);
  padding: var(--space-sm) var(--space-md);
  background: var(--bg-elevated);
  border: 1px dashed var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-muted);
  font-size: 0.75rem;
  text-align: center;
  cursor: pointer;
}

/* === Inspector 大 Dialog：90vw 弹窗承载重面板 === */
.inspector-dialog-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.72);
  backdrop-filter: blur(3px);
  z-index: 200;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}

.inspector-dialog-panel {
  width: 90vw;
  max-width: 1600px;
  height: 90vh;
  background: var(--bg-canvas, #0b0d10);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 14px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-shadow: 0 30px 90px rgba(0, 0, 0, 0.7);
}

.inspector-dialog-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 18px;
  border-bottom: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  background: var(--bg-elevated, #181c24);
}

.inspector-dialog-title {
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  font-size: 14px;
  font-weight: 600;
  letter-spacing: 0.06em;
  color: var(--text-primary, #e8ebf0);
  text-transform: uppercase;
}

.inspector-dialog-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.inspector-dialog-reset,
.inspector-dialog-close {
  background: var(--bg-panel, #11141a);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  color: var(--text-secondary, #9aa3b2);
  border-radius: 6px;
  padding: 4px 10px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
}

.inspector-dialog-reset:hover,
.inspector-dialog-close:hover {
  background: var(--bg-hover, #202632);
  color: var(--text-primary, #e8ebf0);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}

.inspector-dialog-body {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.inspector-dialog-enter-active,
.inspector-dialog-leave-active {
  transition: opacity 0.2s ease;
}
.inspector-dialog-enter-from,
.inspector-dialog-leave-to {
  opacity: 0;
}

@media (max-width: 767px) {
  .main-stage {
    padding: var(--space-sm);
    padding-bottom: calc(var(--commandbar-height) + var(--mobile-nav-height) + var(--space-sm));
  }

  .mobile-tab-view .dock-panel,
  .mobile-tab-view :deep(.dock-panel) {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    border: none;
    border-radius: 0;
    background: var(--bg-panel, #11141a);
    z-index: 10;
  }

  .inspector-dialog-panel {
    width: 100vw;
    max-width: 100vw;
    height: 100vh;
    border-radius: 0;
  }
}

@media (max-width: 1023px) {
  .command-area {
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    height: var(--cmd-h, 64px);
    z-index: 35;
  }
}

@media (min-width: 768px) {
  .layout-tablet,
  .layout-desktop {
    margin-bottom: 0;
  }
}
</style>
