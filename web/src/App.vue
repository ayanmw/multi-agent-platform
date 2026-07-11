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
import { onMounted, onUnmounted, ref, watch, computed } from 'vue'
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
import CaseCard from './components/CaseCard.vue'
import CaseDetailModal from './components/CaseDetailModal.vue'
import Toast from './components/Toast.vue'
import KeyboardTips from './components/KeyboardTips.vue'
import ApprovalDialog from './components/ApprovalDialog.vue'
import { useToast } from './composables/useToast'
import { useKeyboard, SHORTCUTS } from './composables/useKeyboard'
import type { Session } from './composables/useSessionStore'
import type { TaskState } from './types/events'

// Preset case type
interface PresetCase {
  id: string
  name: string
  description: string
  icon: string
  category: string
  tags: string[]
  default_input: string
  system_prompt?: string
  contract?: {
    goal?: string
    scope?: string
    allowed_tools?: string[]
    token_budget?: number
    max_steps?: number
    acceptance_criteria?: Array<{
      type: string
      description: string
    }>
  }
}

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
  loadTask,
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
} = useSessionStore()

const { showAgentConfig, loadAgents, agents } = useAgentStore()

const { projects, activeProjectId, activeProject, loadProjects, setActiveProject } = useProjectStore()

const { toasts, showError, showInfo, dismissToast } = useToast()

// Selected agent ID for task execution
const selectedAgentId = ref('agent_default')

// Auto-approve policy blocks toggle (shared with MetricsPanel)
const autoApprovePolicy = ref(false)

// Project config view toggle
const showProjectConfig = ref(false)
const showMemoryBrowser = ref(false)

// Collapsed state for project groups in sidebar
const collapsedProjects = ref<Set<string>>(new Set())

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

// Preset cases loaded from /api/cases
const presetCases = ref<PresetCase[]>([])
const casesLoading = ref(false)
// App version loaded from /api/version
const appVersion = ref('v0.4 Alpha')
// Case detail modal state
const selectedCase = ref<PresetCase | null>(null)
const showCaseModal = ref(false)

const currentTask = computed(() => {
  if (!activeTaskId.value) return null
  return taskCache.value[activeTaskId.value] || null
})

/**
 * Collect all turns in the current session for the timeline view.
 * Currently wraps the active task as a single turn. In a full multi-turn
 * implementation, this would load all tasks from the session and assemble
 * the full conversation history.
 * TODO: 后续可以加载所有 turn 的 task 到 taskCache
 */
const sessionTurns = computed(() => {
  const turns: Array<{ task: TaskState; userInput: string }> = []
  if (currentTask.value) {
    turns.push({
      task: currentTask.value,
      userInput: lastUserInput.value,
    })
  }
  return turns
})

const isAgentRunning = computed(() => {
  if (!currentTask.value) return false
  return currentTask.value.status === 'running'
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
  // Load preset cases
  casesLoading.value = true
  try {
    const resp = await fetch('/api/cases')
    if (resp.ok) {
      presetCases.value = await resp.json()
    }
  } catch (err) {
    console.error('Failed to load cases:', err)
  } finally {
    casesLoading.value = false
  }
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

/** Handle task submission from TaskInput */
async function handleSend(text: string, options: { maxSteps?: number }) {
  try {
    const session = activeSession.value
    if (!session) {
      // No active session — create a new one in the current project
      const newSession = await createSession(undefined, text, activeProjectId.value)
      setActiveSession(newSession.id)
      await startTask(text, {
        maxSteps: options.maxSteps,
        sessionId: newSession.id,
        agentId: selectedAgentId.value !== 'agent_default' ? selectedAgentId.value : undefined,
      })
    } else if (!session.rootTaskId) {
      // Session exists but no root task yet — this is the first turn
      await startTask(text, {
        maxSteps: options.maxSteps,
        sessionId: session.id,
        agentId: selectedAgentId.value !== 'agent_default' ? selectedAgentId.value : undefined,
      })
    } else {
      // Session already has a root task — this is a subsequent turn
      // Use the multi-turn chat endpoint
      await startTurn(text, {
        sessionId: session.id,
        maxSteps: options.maxSteps,
        agentId: selectedAgentId.value !== 'agent_default' ? selectedAgentId.value : undefined,
      })
    }
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to start task')
  }
}

/** Compute the next max steps when continuing a failed task */
function nextMaxSteps(): number {
  const currentMax = Object.values(currentTask.value?.agents ?? {}).find(a => a.maxSteps)?.maxSteps ?? 10
  return currentMax * 2
}

/** Whether the failure was caused by max_steps_exceeded */
function isMaxStepsFailure(): boolean {
  return currentTask.value?.finalResult?.includes('max steps') ?? false
}

/** Continue a max-steps-exceeded task with doubled max_steps */
async function handleContinue() {
  if (!lastUserInput.value) {
    showError('No previous input to continue from')
    return
  }
  try {
    const newMaxSteps = nextMaxSteps()
    showInfo(`Continuing with max steps ×2 = ${newMaxSteps}`)
    await startTask(lastUserInput.value, {
      maxSteps: newMaxSteps,
      sessionId: activeSessionId.value || undefined,
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

/** Handle running a preset case */
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
  const c = presetCases.value.find(p => p.id === caseId)
  if (c) {
    selectedCase.value = c
    showCaseModal.value = true
  }
}

/** Toggle project collapse state in sidebar */
function toggleProjectCollapse(projectId: string) {
  if (collapsedProjects.value.has(projectId)) {
    collapsedProjects.value.delete(projectId)
  } else {
    collapsedProjects.value.add(projectId)
  }
  // Trigger reactivity
  collapsedProjects.value = new Set(collapsedProjects.value)
}

/** Switch to a different project — reload sessions and clear task */
async function handleProjectSelect(projectId: string) {
  if (projectId === activeProjectId.value) return
  setActiveProject(projectId)
  // Auto-expand the selected project
  collapsedProjects.value.delete(projectId)
  collapsedProjects.value = new Set(collapsedProjects.value)
  clearActiveTask()
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
  if (session.rootTaskId) {
    console.log('[App] Loading task:', session.rootTaskId)
    clearActiveTask()
    try {
      await loadTask(session.rootTaskId)
      console.log('[App] loadTask completed, taskCache keys:', Object.keys(taskCache.value))
      // TODO: 后续可以加载所有 turn 的 task 到 taskCache
    } catch (err) {
      console.error('[App] loadTask failed:', err)
    }
  } else {
    console.log('[App] No rootTaskId, clearing active task')
    clearActiveTask()
  }
}

/** Create a new empty session in the current project */
async function handleNewSession(projectId?: string) {
  try {
    const pid = projectId || activeProjectId.value
    const session = await createSession(undefined, undefined, pid)
    setActiveSession(session.id)
    clearActiveTask()
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to create session')
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
          await loadTask(first.rootTaskId)
        } else {
          clearActiveTask()
        }
      } else {
        const empty = await createSession(undefined, undefined, activeProjectId.value)
        setActiveSession(empty.id)
        clearActiveTask()
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
</script>

<template>
  <div class="app">
    <!-- Sidebar: Project list + grouped Session list -->
    <aside class="session-sidebar">
      <div class="sidebar-header">
        <h2 class="sidebar-title">Projects</h2>
        <button class="new-session-btn" @click="showProjectConfig = true" title="New Project">+</button>
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
              <div class="session-name">💬 {{ session.name }}</div>
              <div class="session-meta">
                <span :class="['session-status', session.status]">{{ session.status }}</span>
                <span v-if="session.totalTokens > 0" class="session-tokens">
                  {{ session.totalTokens }} tokens
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
    <main class="main-content">
      <!-- Agent Config view — replaces main content when active -->
      <AgentConfig v-if="showAgentConfig" @back="showAgentConfig = false" />

      <!-- Project Config view — replaces main content when active -->
      <ProjectConfig v-else-if="showProjectConfig" @back="handleProjectConfigBack" />

      <!-- Memory Browser view — replaces main content when active -->
      <MemoryBrowser v-else-if="showMemoryBrowser" />

      <!-- Normal main content -->
      <template v-else>
      <!-- Header -->
      <header class="app-header">
        <h1 class="app-title">🤖 Multi-Agent Platform</h1>
        <div class="app-header-right">
          <button class="agents-btn" @click="showAgentConfig = true" title="Agent Configuration">⚙ Agents</button>
          <button class="agents-btn" @click="showMemoryBrowser = true" title="Memory Browser">🧠 Memory</button>
          <button class="tips-btn" @click="showTips = true" title="Keyboard shortcuts (?)">⌨</button>
          <span class="app-version">{{ appVersion }}</span>
        </div>
      </header>

      <!-- Metrics bar -->
      <MetricsPanel :task="currentTask" :ws-status="wsStatus" :agents="agents" :selected-agent-id="selectedAgentId" :auto-approve="autoApprovePolicy" @update:selected-agent-id="(id: string) => selectedAgentId = id" @update:auto-approve="(v: boolean) => autoApprovePolicy = v" />

      <!-- Turn List (multi-turn conversation timeline) -->
      <TurnList
        v-if="currentTask"
        :turns="sessionTurns"
      />

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
        @send="handleSend"
        @pause="pauseTask"
        @resume="resumeTask"
        @cancel="cancelTask"
      />

      <!-- Preset case cards — shown when active session has no task / task is empty -->
      <div v-if="!currentTask && !isTaskPending && presetCases.length > 0" class="cases-section">
        <h2 class="section-title">📋 预设任务</h2>
        <div v-if="casesLoading" class="cases-loading">Loading...</div>
        <div v-else class="cases-grid">
          <CaseCard
            v-for="c in presetCases"
            :key="c.id"
            :case-data="c"
            :disabled="isAgentRunning"
            @run="handleCaseRun"
            @view="handleCaseView"
          />
        </div>
      </div>

      <!-- Empty state -->
      <div v-else-if="!isTaskPending && presetCases.length === 0" class="empty-state">
        <div class="empty-icon">🚀</div>
        <h2>Ready to start</h2>
        <p>Enter a task description above to see the agent in action.</p>
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

      <!-- Global toast notifications -->
      <Toast :toasts="toasts" @dismiss="dismissToast" />

      <!-- Case detail modal -->
      <CaseDetailModal
        :case-data="selectedCase"
        :visible="showCaseModal"
        @close="showCaseModal = false"
        @run="handleCaseRun"
      />

      <!-- Keyboard shortcuts panel -->
      <KeyboardTips
        :visible="showTips"
        :shortcuts="SHORTCUTS"
        :is-running="isAgentRunning"
        @close="showTips = false"
      />

      <!-- Approval dialog for policy-blocked tool calls -->
      <ApprovalDialog
        :approval-id="pendingApproval?.approvalId ?? ''"
        :tool="pendingApproval?.tool ?? ''"
        :reason="pendingApproval?.reason ?? ''"
        :input="pendingApproval?.input ?? {}"
        :auto-approve="autoApprovePolicy"
        :visible="pendingApproval !== null"
        @approve="handleApprove"
        @deny="handleDeny"
        @close="handleApprovalClose"
      />
      </template>
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

.section-title {
  font-size: 15px;
  font-weight: 600;
  color: #e0e0e0;
  margin-bottom: 12px;
}

.cases-loading {
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
</style>