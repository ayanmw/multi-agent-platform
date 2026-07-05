<!-- App.vue — root layout component
     Structure:
       ┌─────────────────────────────────────────────┐
       │  MetricsPanel (connection, task, tokens)      │
       ├─────────────────────────────────────────────┤
       │  TaskInput (chat input + control buttons)     │
       ├─────────────────────────────────────────────┤
       │  AgentTree × N (one per agent, side-by-side)  │
       ├─────────────────────────────────────────────┤
       │  Final result (shown when task completed)     │
       └─────────────────────────────────────────────┘

     Lifecycle:
       onMounted → connect WebSocket → onEvent → update task state
       user types → startTask → POST /api/tasks → WebSocket events flow
       events → handleEvent → task reactive → AgentTree re-renders
-->
<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { useTaskStore } from './composables/useTaskStore'
import MetricsPanel from './components/MetricsPanel.vue'
import TaskInput from './components/TaskInput.vue'
import AgentTree from './components/AgentTree.vue'
import CaseCard from './components/CaseCard.vue'
import CaseDetailModal from './components/CaseDetailModal.vue'
import Toast from './components/Toast.vue'
import KeyboardTips from './components/KeyboardTips.vue'
import { useToast } from './composables/useToast'
import { useKeyboard, SHORTCUTS } from './composables/useKeyboard'

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
  task,
  isTaskPending,
  wsStatus,
  connect,
  disconnect,
  startTask,
  startTaskWithCase,
  clearTask,
  pauseTask,
  resumeTask,
  cancelTask,
} = useTaskStore()

const { toasts, showError, showInfo, dismissToast } = useToast()

// Keyboard shortcuts
const { isRunning: kbIsRunning, showTips } = useKeyboard({
  onCancel: cancelTask,
  onPause: () => {
    if (task.value?.status === 'running') {
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

// Connect WebSocket on mount, disconnect on unmount
onMounted(async () => {
  connect()
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
    // Clear previous task state before starting a new one
    clearTask()
    await startTask(text, undefined, undefined, options.maxSteps)
  } catch (err) {
    showError(err instanceof Error ? err.message : 'Failed to start task')
  }
}

/** Check if any agent is currently running */
function isAgentRunning(): boolean {
  if (!task.value) return false
  return task.value.status === 'running'
}

// Sync keyboard shortcut state with task running state
watch(
  () => task.value?.status,
  (status) => {
    kbIsRunning.value = status === 'running'
  }
)

/** Handle running a preset case */
async function handleCaseRun(caseId: string) {
  try {
    showCaseModal.value = false
    clearTask()
    await startTaskWithCase(caseId)
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
</script>

<template>
  <div class="app">
    <!-- Header -->
    <header class="app-header">
      <h1 class="app-title">🤖 Multi-Agent Platform</h1>
      <div class="app-header-right">
        <button class="tips-btn" @click="showTips = true" title="Keyboard shortcuts (?)">⌨</button>
        <span class="app-version">{{ appVersion }}</span>
      </div>
    </header>

    <!-- Metrics bar -->
    <MetricsPanel :task="task" :ws-status="wsStatus" />

    <!-- Task input -->
    <TaskInput
      :disabled="isAgentRunning()"
      :is-running="isAgentRunning()"
      :is-pending="isTaskPending"
      @send="handleSend"
      @pause="pauseTask"
      @resume="resumeTask"
      @cancel="cancelTask"
    />

    <!-- Preset case cards — shown when no task is running and not pending -->
    <div v-if="!task && !isTaskPending && presetCases.length > 0" class="cases-section">
      <h2 class="section-title">📋 预设任务</h2>
      <div v-if="casesLoading" class="cases-loading">Loading...</div>
      <div v-else class="cases-grid">
        <CaseCard
          v-for="c in presetCases"
          :key="c.id"
          :case-data="c"
          :disabled="isAgentRunning()"
          @run="handleCaseRun"
          @view="handleCaseView"
        />
      </div>
    </div>

    <!-- Agent trees — one per agent, side by side if multiple -->
    <div v-if="task" class="agent-trees">
      <div
        v-for="agent in Object.values(task.agents)"
        :key="agent.id"
        class="agent-tree-wrapper"
      >
        <AgentTree
          :agent="agent"
          :is-running="task.status === 'running'"
        />
      </div>
    </div>

    <!-- Loading indicator — shown while waiting for WebSocket events after sending -->
    <div v-else-if="isTaskPending" class="loading-area">
      <div class="loading-spinner"></div>
      <div class="loading-text">Agent is starting...</div>
      <div class="loading-subtext">Waiting for LLM response</div>
    </div>

    <!-- Empty state: shown when no task is active and not pending -->
    <div v-else-if="!isTaskPending && presetCases.length === 0" class="empty-state">
      <div class="empty-icon">🚀</div>
      <h2>Ready to start</h2>
      <p>Enter a task description above to see the agent in action.</p>
      <p class="text-muted">
        The agent can write code, run shell commands, read files, and more.
        Every step of the ReAct loop is visualized in real time.
      </p>
    </div>

    <!-- Final result (shown when task completed) -->
    <div v-if="task?.status === 'completed' && task?.finalResult" class="final-result">
      <div class="final-result-header">✅ Task Complete</div>
      <pre class="final-result-text">{{ task.finalResult }}</pre>
    </div>

    <!-- Task failed (shown when task failed) -->
    <div v-if="task?.status === 'failed'" class="final-result final-result-failed">
      <div class="final-result-header">❌ Task Failed</div>
      <div v-if="task.finalResult" class="final-result-text">{{ task.finalResult }}</div>
      <div v-else class="final-result-text final-result-subtle">
        The task failed. Check the agent tree above for details.
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
      :is-running="isAgentRunning()"
      @close="showTips = false"
    />
  </div>
</template>

<style scoped>
.app {
  min-height: calc(100vh - 40px);
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
  min-width: 0; /* prevent grid blowout */
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

/* Loading area (waiting for WebSocket events) */
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
</style>