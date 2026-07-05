<!-- MetricsPanel — displays task-level metrics (token usage, step count, duration)
     Props:
       task: the current TaskState from useTaskStore
       wsStatus: WebSocket connection status string
-->
<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import type { TaskState } from '../types/events'
import StatusIndicator from './StatusIndicator.vue'

const props = defineProps<{
  task: TaskState | null
  wsStatus: string
}>()

/** Elapsed time string (updated every second) */
const elapsed = ref('')
let elapsedTimer: ReturnType<typeof setInterval> | null = null

/** Total steps across all agents */
const totalSteps = computed(() => {
  if (!props.task) return 0
  return Object.values(props.task.agents).reduce(
    (sum, agent) => sum + agent.steps.length,
    0
  )
})

/** Agent count */
const agentCount = computed(() => {
  if (!props.task) return 0
  return Object.keys(props.task.agents).length
})

/** Format token count */
function formatTokens(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return `${n}`
}

/** Format elapsed time from ms */
function formatElapsed(ms: number): string {
  const s = Math.floor(ms / 1000)
  const m = Math.floor(s / 60)
  const h = Math.floor(m / 60)
  if (h > 0) return `${h}h ${m % 60}m ${s % 60}s`
  if (m > 0) return `${m}m ${s % 60}s`
  return `${s}s`
}

/** Update elapsed time every second while task is running */
function updateElapsed() {
  if (!props.task?.startedAt) {
    elapsed.value = ''
    return
  }
  const diff = Date.now() - props.task.startedAt
  elapsed.value = formatElapsed(diff)
}

// Start/stop timer based on task status
onMounted(() => {
  elapsedTimer = setInterval(updateElapsed, 1000)
})

onUnmounted(() => {
  if (elapsedTimer) clearInterval(elapsedTimer)
})

/** Connection status badge class */
const wsStatusClass = computed(() => {
  switch (props.wsStatus) {
    case 'connected': return 'ws-connected'
    case 'connecting': return 'ws-connecting'
    default: return 'ws-disconnected'
  }
})
</script>

<template>
  <div class="metrics-panel">
    <div class="metrics-row">
      <div class="metric">
        <span class="metric-label">Agent</span>
        <select class="agent-select" disabled title="Multi-agent selection coming in Phase 4">
          <option>Default Agent</option>
        </select>
      </div>

      <div class="metric">
        <span class="metric-label">Connection</span>
        <span class="metric-value" :class="wsStatusClass">{{ wsStatus }}</span>
      </div>

      <template v-if="task">
        <div class="metric">
          <span class="metric-label">Status</span>
          <StatusIndicator :status="task.status" :label="task.status" />
        </div>
        <div class="metric">
          <span class="metric-label">Agents</span>
          <span class="metric-value">{{ agentCount }}</span>
        </div>
        <div class="metric">
          <span class="metric-label">Steps</span>
          <span class="metric-value">{{ totalSteps }}</span>
        </div>
        <div class="metric">
          <span class="metric-label">Tokens</span>
          <span class="metric-value">{{ formatTokens(task.totalTokens) }}</span>
        </div>
        <div v-if="elapsed" class="metric">
          <span class="metric-label">Elapsed</span>
          <span class="metric-value">{{ elapsed }}</span>
        </div>
      </template>

      <template v-else>
        <div class="metric">
          <span class="metric-label">Status</span>
          <span class="metric-value metric-idle">Idle</span>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.metrics-panel {
  background: #252525;
  border: 1px solid #333;
  border-radius: 8px;
  padding: 8px 14px;
  margin-bottom: 16px;
}

.metrics-row {
  display: flex;
  gap: 24px;
  align-items: center;
  flex-wrap: wrap;
}

.metric {
  display: flex;
  align-items: center;
  gap: 6px;
}

.metric-label {
  font-size: 11px;
  color: #888;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.metric-value {
  font-size: 13px;
  color: #d4d4d4;
  font-weight: 600;
}

.metric-idle {
  color: #888;
}

.ws-connected {
  color: #51cf66;
}

.ws-connecting {
  color: #f0a030;
}

.ws-disconnected {
  color: #ff6b6b;
}

/* Agent selector (placeholder) */
.agent-select {
  background: #1e1e1e;
  border: 1px solid #444;
  border-radius: 4px;
  color: #d4d4d4;
  font-size: 12px;
  padding: 2px 8px;
  cursor: not-allowed;
  opacity: 0.6;
  font-family: inherit;
}
</style>