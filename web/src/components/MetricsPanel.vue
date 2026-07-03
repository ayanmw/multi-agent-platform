<!-- MetricsPanel — displays task-level metrics (token usage, step count, duration)
     Props:
       task: the current TaskState from useTaskStore
       wsStatus: WebSocket connection status string
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { TaskState } from '../types/events'
import StatusIndicator from './StatusIndicator.vue'

const props = defineProps<{
  task: TaskState | null
  wsStatus: string
}>()

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
</style>