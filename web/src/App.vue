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
import { onMounted, onUnmounted, ref } from 'vue'
import { useTaskStore } from './composables/useTaskStore'
import MetricsPanel from './components/MetricsPanel.vue'
import TaskInput from './components/TaskInput.vue'
import AgentTree from './components/AgentTree.vue'
import CaseCard from './components/CaseCard.vue'

// Preset case type
interface PresetCase {
  id: string
  name: string
  description: string
  icon: string
  category: string
  tags: string[]
  default_input: string
}

const {
  task,
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

// Preset cases loaded from /api/cases
const presetCases = ref<PresetCase[]>([])
const casesLoading = ref(false)

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
})

onUnmounted(() => {
  disconnect()
})

/** Handle task submission from TaskInput */
async function handleSend(text: string) {
  try {
    // Clear previous task state before starting a new one
    clearTask()
    await startTask(text)
  } catch (err) {
    console.error('Failed to start task:', err)
  }
}

/** Check if any agent is currently running */
function isAgentRunning(): boolean {
  if (!task.value) return false
  return task.value.status === 'running'
}

/** Handle running a preset case */
async function handleCaseRun(caseId: string) {
  try {
    clearTask()
    await startTaskWithCase(caseId)
  } catch (err) {
    console.error('Failed to start case:', err)
  }
}
</script>

<template>
  <div class="app">
    <!-- Header -->
    <header class="app-header">
      <h1 class="app-title">🤖 Multi-Agent Platform</h1>
      <span class="app-version">v0.3 Alpha</span>
    </header>

    <!-- Metrics bar -->
    <MetricsPanel :task="task" :ws-status="wsStatus" />

    <!-- Task input -->
    <TaskInput
      :disabled="isAgentRunning()"
      :is-running="isAgentRunning()"
      @send="handleSend"
      @pause="pauseTask"
      @resume="resumeTask"
      @cancel="cancelTask"
    />

    <!-- Preset case cards — shown when no task is running -->
    <div v-if="!task && presetCases.length > 0" class="cases-section">
      <h2 class="section-title">📋 预设任务</h2>
      <div v-if="casesLoading" class="cases-loading">Loading...</div>
      <div v-else class="cases-grid">
        <CaseCard
          v-for="c in presetCases"
          :key="c.id"
          :case-data="c"
          :disabled="isAgentRunning()"
          @run="handleCaseRun"
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

    <!-- Empty state: shown when no task is active -->
    <div v-else-if="presetCases.length === 0" class="empty-state">
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
    <div v-if="task?.status === 'failed' && task?.finalResult" class="final-result final-result-failed">
      <div class="final-result-header">❌ Task Failed</div>
      <pre class="final-result-text">{{ task.finalResult }}</pre>
    </div>
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

.app-title {
  font-size: 20px;
  font-weight: 700;
  color: #e0e0e0;
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
</style>