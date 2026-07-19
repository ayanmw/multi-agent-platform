<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch, nextTick } from 'vue'
import TopBar from './components/TopBar.vue'
import DockPanel from './components/DockPanel.vue'
import SessionDock from './components/SessionDock.vue'
import InspectorContent from './components/InspectorContent.vue'
import InspectorFlyout from './components/InspectorFlyout.vue'
import SessionFiles from './components/SessionFiles.vue'
import ColumnResizer from './components/ColumnResizer.vue'
import CommandBar from './components/CommandBar.vue'
import MobileNav from './components/MobileNav.vue'
import TimelineTrack from './components/TimelineTrack.vue'
import Toast from './components/Toast.vue'
import KeyboardTips from './components/KeyboardTips.vue'
import ApprovalDialog from './components/ApprovalDialog.vue'
import RecentModsDialog from './components/RecentModsDialog.vue'
import ModelPricesDialog from './components/ModelPricesDialog.vue'
import MCPServerDialog from './components/MCPServerDialog.vue'
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
import type { Session } from './composables/useSessionStore'
import type { TaskState } from './types/events'

/**
 * App.vue — v2 Observable Control Room 根布局
 *
 * 布局策略（v2 三栏可调 + 右栏改为文件浏览器 + Inspector 浮窗）：
 * 桌面端（>=1024px）：
 *   TopBar + 左 Dock（Sessions，可调宽/隐藏）+ 主舞台 + 右 Dock（Files，可调宽/隐藏）+ 底部 CommandBar。
 *   Inspector 不再常驻右栏，改为浮在主舞台右上角的迷你卡片（InspectorFlyout），
 *   点击展开成 90vw 大 Dialog 显示 Memory/RAG/Context/Cases/Agents/Project/Skills/Traces 等重面板。
 * 平板端（768–1023px）：左 Dock 可折叠；右 Files 栏可折叠；Inspector 仍走浮窗+Dialog。
 * 移动端（<768px）：单一内容区，通过底部 MobileNav 切换 stage/sessions/inspector。
 *
 * 三栏宽度与开合状态均持久化到 localStorage，方便用户自由调整。
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
  setLeftDockWidth,
  setRightFilesWidth,
  commitWidths,
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
  renameSession,
} = useSessionStore()

const { agents, loadAgents } = useAgentStore()
const { projects, activeProjectId, loadProjects, setActiveProject } = useProjectStore()
const { toasts, showError, showInfo, dismissToast } = useToast()
const { loadSkills, enableSkill } = useSkills()
const caseStore = useCaseStore()
const { setActiveSession: setFilesSession, refreshDir: refreshFilesRoot } = useSessionFiles()

// === Skill / Multi-Agent 状态 ===
const multiAgentEnabled = ref(false)
const prefilledCommand = ref('')

function onMultiAgentChange(v: boolean) {
  multiAgentEnabled.value = v
}

// === 行内重命名状态 ===
const renamingSessionId = ref<string | null>(null)
const renameBuffer = ref('')

// === 弹窗可见性 ===
const recentModsVisible = ref(false)
const modelPricesVisible = ref(false)
const mcpServerDialogVisible = ref(false)

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

// === Inspector 浮窗 / 大 Dialog ===
const inspectorFlyoutOpen = ref(true)
const inspectorDialogOpen = ref(false)
const inspectorInitialTab = ref<string>('sessions')

function toggleInspectorFlyout() {
  inspectorFlyoutOpen.value = !inspectorFlyoutOpen.value
}
function openInspectorDialog(tab?: string) {
  if (tab) inspectorInitialTab.value = tab
  inspectorDialogOpen.value = true
}
function closeInspectorDialog() {
  inspectorDialogOpen.value = false
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

// === Multi-Agent（暂不实现） ===
// TODO: Phase 8 — 当 useMultiAgent workflow 组件迁移后再接入 startMultiAgentTask

// === 项目/会话选择 ===
async function handleProjectSelect(projectId: string) {
  if (projectId === activeProjectId.value) return
  setActiveProject(projectId)
  clearActiveTask()
  clearContextWindow()
  try {
    await loadSessions(projectId)
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to load sessions')
  }
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

// === 行内重命名 ===
function startRenameSession(session: Session) {
  renamingSessionId.value = session.id
  renameBuffer.value = session.name
}

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

function cancelRenameSession() {
  renamingSessionId.value = null
  renameBuffer.value = ''
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

const showInspectorToggle = computed(() => !isMobile.value)
</script>

<template>
  <div class="app-shell">
    <TopBar
      :status="connectionStatus"
      :status-label="statusLabel"
      :task-status-label="taskStatusLabel"
      :show-inspector-toggle="showInspectorToggle"
      :inspector-open="inspectorFlyoutOpen"
      @toggle-inspector="toggleInspectorFlyout"
      @toggle-left-dock="toggleLeftDock"
      @toggle-recent-mods="showRecentMods"
      @toggle-model-prices="modelPricesVisible = true"
      @toggle-mcp="mcpServerDialogVisible = true"
      @toggle-keyboard-tips="showTips = true"
    />

    <!-- 桌面三栏布局：左 Sessions | 主舞台 | 右 Files，宽度可拖拽 -->
    <div v-if="isDesktop" class="layout-desktop" :style="{ '--left-w': leftDockWidth + 'px', '--right-w': rightFilesWidth + 'px' }">
      <DockPanel side="left" title="Sessions" :open="leftDockOpen" @close="toggleLeftDock">
        <SessionDock
          :projects="projects"
          :active-project-id="activeProjectId"
          :sessions="sessions"
          :active-session-id="activeSessionId"
          :renaming-session-id="renamingSessionId"
          :rename-buffer="renameBuffer"
          @update:rename-buffer="renameBuffer = $event"
          @select-project="handleProjectSelect"
          @select-session="handleSessionSelect"
          @new-session="handleNewSession"
          @delete-session="handleDeleteSession"
          @rename-start="startRenameSession"
          @rename-commit="commitRenameSession"
          @rename-cancel="cancelRenameSession"
        />
      </DockPanel>

      <ColumnResizer
        v-if="leftDockOpen"
        side="left"
        :width="leftDockWidth"
        @resize="setLeftDockWidth"
        @resize-end="commitWidths"
      />

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

      <ColumnResizer
        v-if="rightFilesOpen"
        side="right"
        :width="rightFilesWidth"
        @resize="setRightFilesWidth"
        @resize-end="commitWidths"
      />

      <DockPanel side="right" title="Files" :open="rightFilesOpen" @close="toggleRightFiles">
        <SessionFiles :session-id="activeSessionId || ''" />
      </DockPanel>

      <!-- Inspector 浮窗：浮在主舞台右上角，点击展开成大 Dialog -->
      <InspectorFlyout
        v-if="inspectorFlyoutOpen"
        :task="currentTask"
        :session-total-tokens="sessionTotalTokens"
        :session-total-duration="sessionTotalDuration"
        :ws-status="wsStatus"
        :agents="agents"
        @open-dialog="openInspectorDialog"
      />
    </div>

    <!-- 平板双栏布局 -->
    <div v-else-if="isTablet" class="layout-tablet">
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
          :renaming-session-id="renamingSessionId"
          :rename-buffer="renameBuffer"
          @update:rename-buffer="renameBuffer = $event"
          @select-project="handleProjectSelect"
          @select-session="handleSessionSelect"
          @new-session="handleNewSession"
          @delete-session="handleDeleteSession"
          @rename-start="startRenameSession"
          @rename-commit="commitRenameSession"
          @rename-cancel="cancelRenameSession"
        />
      </DockPanel>

      <main ref="mainRef" class="main-stage" @scroll="handleMainScroll">
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

      <DockPanel
        v-if="rightFilesOpen"
        side="right"
        title="Files"
        :open="true"
        @close="toggleRightFiles"
      >
        <SessionFiles :session-id="activeSessionId || ''" />
      </DockPanel>

      <InspectorFlyout
        v-if="inspectorFlyoutOpen"
        :task="currentTask"
        :session-total-tokens="sessionTotalTokens"
        :session-total-duration="sessionTotalDuration"
        :ws-status="wsStatus"
        :agents="agents"
        @open-dialog="openInspectorDialog"
      />
    </div>

    <!-- 移动端单视图 -->
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
            :renaming-session-id="renamingSessionId"
            :rename-buffer="renameBuffer"
            @update:rename-buffer="renameBuffer = $event"
            @select-project="handleProjectSelect"
            @select-session="handleSessionSelect"
            @new-session="handleNewSession"
            @delete-session="handleDeleteSession"
            @rename-start="startRenameSession"
            @rename-commit="commitRenameSession"
            @rename-cancel="cancelRenameSession"
          />
        </DockPanel>
      </div>
      <div v-else-if="activeMobileTab === 'inspector'" class="mobile-tab-view">
        <DockPanel side="right" title="Files" :open="true" @close="activeMobileTab = 'stage'">
          <SessionFiles :session-id="activeSessionId || ''" />
        </DockPanel>
      </div>
    </div>

    <CommandBar
      v-if="activeMobileTab === 'stage' || !isMobile"
      :disabled="isAgentRunning"
      :is-running="isAgentRunning"
      :is-pending="isTaskPending"
      :prefill="prefilledCommand"
      @send="handleSend"
      @pause="pauseTask"
      @resume="resumeTask"
      @cancel="cancelTask"
      @update:prefill="prefilledCommand = ''"
      @update:multiAgent="onMultiAgentChange"
      @multiAgentChange="onMultiAgentChange"
    />

    <MobileNav v-if="isMobile" />

    <!-- Inspector 大 Dialog（90vw）：承载 Memory/RAG/Context/Cases/Agents/Project/Skills/Traces 重面板 -->
    <Teleport to="body">
      <Transition name="inspector-dialog">
        <div v-if="inspectorDialogOpen" class="inspector-dialog-overlay" @click.self="closeInspectorDialog">
          <div class="inspector-dialog-panel">
            <div class="inspector-dialog-header">
              <span class="inspector-dialog-title">🧭 Inspector</span>
              <div class="inspector-dialog-actions">
                <button class="inspector-dialog-reset" title="Reset column widths" @click="resetWidths">↺ Reset Layout</button>
                <button class="inspector-dialog-close" @click="closeInspectorDialog" title="Close">×</button>
              </div>
            </div>
            <div class="inspector-dialog-body">
              <InspectorContent
                :initial-tab="inspectorInitialTab"
                @run-case="handleRunCase"
                @trigger-skill="handleTriggerSkill"
              />
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>

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

.main-stage {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  padding: var(--space-md);
  background: var(--bg-canvas, #0b0d10);
  position: relative;
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

@media (min-width: 768px) {
  .layout-tablet,
  .layout-desktop {
    margin-bottom: var(--commandbar-height, 64px);
  }
}
</style>
