<!-- MetricsPanel — displays task-level metrics (token usage, step count, duration, token details)
     Props:
       task: the current TaskState from useTaskStore
       sessionTotalTokens: total tokens for the whole session across all turns
       wsStatus: WebSocket connection status string
       agents: list of available agents (from useAgentStore)
       selectedAgentId: currently selected agent ID
     Emits:
       update:selectedAgentId: user changed the agent selection

     Time tracking strategy:
       - Total elapsed = sum of all completed steps' durationMs + current running step's live elapsed
       - When task is completed/failed and no running step, timer stops
       - Hovering the elapsed time shows a tooltip with per-step time distribution
-->
<script setup lang="ts">
import { computed, ref, watch, onMounted, onUnmounted } from 'vue'
import type { TaskState, TokenUsage, Step } from '../types/events'
import type { AgentRecord } from '../composables/useAgentStore'
import StatusIndicator from './StatusIndicator.vue'

const props = defineProps<{
  task: TaskState | null
  sessionTotalTokens: number
  wsStatus: string
  agents: AgentRecord[]
  selectedAgentId: string
  autoApprove: boolean
}>()

const emit = defineEmits<{
  'update:selectedAgentId': [id: string]
  'update:autoApprove': [value: boolean]
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

/** Collect all steps from all agents, sorted by index */
const allSteps = computed<Step[]>(() => {
  if (!props.task) return []
  const steps: Step[] = []
  for (const agent of Object.values(props.task.agents)) {
    steps.push(...agent.steps)
  }
  return steps.sort((a, b) => a.index - b.index)
})

/** Find the currently running step (if any) */
const runningStep = computed<Step | null>(() => {
  for (const step of allSteps.value) {
    if (step.status === 'running') return step
  }
  return null
})

/** Sum of all completed steps' durationMs */
const completedDuration = computed(() => {
  return allSteps.value
    .filter(s => s.status === 'completed' || s.status === 'failed')
    .reduce((sum, s) => sum + s.durationMs, 0)
})

/** Live elapsed for the currently running step (updated every second) */
const liveElapsed = ref(0)
let liveTimer: ReturnType<typeof setInterval> | null = null

function updateLiveElapsed() {
  if (runningStep.value && runningStep.value.startedAt > 0) {
    liveElapsed.value = Date.now() - runningStep.value.startedAt
  } else {
    liveElapsed.value = 0
  }
}

/** Total elapsed = completed + live */
const totalElapsed = computed(() => {
  return completedDuration.value + liveElapsed.value
})

/** Whether the timer should be actively ticking */
const isTicking = computed(() => {
  return runningStep.value !== null && props.task?.status === 'running'
})

// Start/stop the live timer
function startTimer() {
  if (liveTimer) return
  liveTimer = setInterval(updateLiveElapsed, 200) // 200ms for smooth updates
}

function stopTimer() {
  if (liveTimer) {
    clearInterval(liveTimer)
    liveTimer = null
  }
  liveElapsed.value = 0
}

// Watch for changes in running step
watch(isTicking, (ticking) => {
  if (ticking) {
    startTimer()
  } else {
    stopTimer()
  }
}, { immediate: true })

onMounted(() => {
  if (isTicking.value) startTimer()
})

onUnmounted(() => {
  stopTimer()
})

/** Aggregate token usage across all agents (or fallback to task.totalTokens) */
const tokenUsage = computed<TokenUsage>(() => {
  if (props.task?.tokenUsage) {
    return props.task.tokenUsage
  }
  return {
    promptTokens: props.task?.totalTokens || 0,
    promptCacheHitTokens: 0,
    promptCacheMissTokens: props.task?.totalTokens || 0,
    completionTokens: 0,
    totalTokens: props.task?.totalTokens || 0,
  }
})

/** Format token count */
function formatTokens(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return `${n}`
}

/** Format elapsed time from ms */
function formatElapsed(ms: number): string {
  if (ms <= 0) return '0s'
  const s = Math.floor(ms / 1000)
  const m = Math.floor(s / 60)
  const h = Math.floor(m / 60)
  if (h > 0) return `${h}h ${m % 60}m ${s % 60}s`
  if (m > 0) return `${m}m ${s % 60}s`
  return `${s}s`
}

/** Format a single step's duration for the tooltip */
function formatStepDuration(ms: number): string {
  if (ms <= 0) return '—'
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

/** Step type label in Chinese */
function stepTypeLabel(step: Step): string {
  if (step.type === 'think') return '思考'
  if (step.type === 'tool_call') return step.toolCall?.name || '工具调用'
  if (step.type === 'observation') return '观察'
  return step.type
}

/** Connection status badge class */
const wsStatusClass = computed(() => {
  switch (props.wsStatus) {
    case 'connected': return 'ws-connected'
    case 'connecting': return 'ws-connecting'
    default: return 'ws-disconnected'
  }
})

/** Whether token detail tooltip is visible */
const showTokenDetail = ref(false)

/** Whether time distribution tooltip is visible */
const showTimeDetail = ref(false)
</script>

<template>
  <div class="metrics-panel">
    <div class="metrics-row">
      <div class="metric">
        <span class="metric-label">Agent</span>
        <select
          class="agent-select"
          :value="selectedAgentId"
          @change="(e: Event) => emit('update:selectedAgentId', (e.target as HTMLSelectElement).value)"
          title="Select the agent to use for the next task"
        >
          <option v-for="agent in agents" :key="agent.id" :value="agent.id">
            {{ agent.name }}
          </option>
          <option v-if="agents.length === 0" value="">No agents available</option>
        </select>
      </div>

      <div class="metric">
        <span class="metric-label">Connection</span>
        <span class="metric-value" :class="wsStatusClass">{{ wsStatus }}</span>
      </div>

      <div class="metric">
        <span class="metric-label">Auto-Approve</span>
        <label class="toggle-switch" title="When enabled, all policy blocks (DangerousCommandRule, etc.) are automatically approved">
          <input
            type="checkbox"
            :checked="autoApprove"
            @change="(e: Event) => emit('update:autoApprove', (e.target as HTMLInputElement).checked)"
          />
          <span class="toggle-slider"></span>
        </label>
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
        <div
          class="metric metric-tokens"
          @mouseenter="showTokenDetail = true"
          @mouseleave="showTokenDetail = false"
        >
          <span class="metric-label">Tokens</span>
          <span class="metric-value">{{ formatTokens(sessionTotalTokens) }}</span>

          <!-- Token detail tooltip -->
          <Transition name="token-tooltip">
            <div v-if="showTokenDetail" class="token-tooltip">
              <div class="token-row">
                <span class="token-key">Current Turn</span>
                <span class="token-val">{{ formatTokens(tokenUsage.totalTokens) }}</span>
              </div>
              <div class="token-row indent">
                <span class="token-key sub">input</span>
                <span class="token-val sub">{{ formatTokens(tokenUsage.promptTokens) }}</span>
              </div>
              <div class="token-row indent">
                <span class="token-key sub">output</span>
                <span class="token-val sub">{{ formatTokens(tokenUsage.completionTokens) }}</span>
              </div>
              <div class="token-row indent">
                <span class="token-key sub">cache</span>
                <span class="token-val sub">{{ formatTokens(tokenUsage.promptCacheHitTokens) }}</span>
              </div>
              <div class="token-row total">
                <span class="token-key">Session Total</span>
                <span class="token-val">{{ formatTokens(sessionTotalTokens) }}</span>
              </div>
            </div>
          </Transition>
        </div>
        <div
          v-if="totalElapsed > 0 || isTicking"
          class="metric metric-elapsed"
          @mouseenter="showTimeDetail = true"
          @mouseleave="showTimeDetail = false"
        >
          <span class="metric-label">Elapsed</span>
          <span class="metric-value" :class="{ 'elapsed-live': isTicking }">
            {{ formatElapsed(totalElapsed) }}
          </span>

          <!-- Time distribution tooltip -->
          <Transition name="token-tooltip">
            <div v-if="showTimeDetail && allSteps.length > 0" class="time-tooltip">
              <div class="time-tooltip-header">Time Distribution</div>
              <div
                v-for="step in allSteps"
                :key="`${step.type}-${step.index}`"
                class="time-row"
              >
                <span class="time-step-label">
                  <span :class="['time-dot', step.status === 'running' ? 'dot-running' : '']" />
                  #{{ step.index }} {{ stepTypeLabel(step) }}
                </span>
                <span :class="['time-step-val', step.status === 'running' ? 'running' : '']">
                  <template v-if="step.status === 'running'">
                    {{ formatElapsed(liveElapsed) }}
                  </template>
                  <template v-else>
                    {{ formatStepDuration(step.durationMs) }}
                  </template>
                </span>
              </div>
              <div class="time-row time-total">
                <span class="time-step-label">Total</span>
                <span class="time-step-val">{{ formatElapsed(totalElapsed) }}</span>
              </div>
            </div>
          </Transition>
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
  position: relative;
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

.metric-tokens {
  cursor: help;
  border-bottom: 1px dashed #666;
}

.metric-elapsed {
  cursor: help;
  border-bottom: 1px dashed #666;
}

.elapsed-live {
  color: #f0a030;
  animation: elapsed-pulse 2s ease-in-out infinite;
}

@keyframes elapsed-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.7; }
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

/* Token detail tooltip */
.token-tooltip {
  position: absolute;
  top: calc(100% + 8px);
  left: 50%;
  transform: translateX(-50%);
  background: #1e1e1e;
  border: 1px solid #444;
  border-radius: 8px;
  padding: 10px 12px;
  min-width: 160px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.5);
  z-index: 100;
}

.token-tooltip::before {
  content: '';
  position: absolute;
  bottom: 100%;
  left: 50%;
  transform: translateX(-50%);
  border: 6px solid transparent;
  border-bottom-color: #444;
}

.token-row {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  font-size: 12px;
  color: #d4d4d4;
  padding: 2px 0;
}

.token-row.indent {
  padding-left: 12px;
}

.token-row.total {
  margin-top: 6px;
  padding-top: 6px;
  border-top: 1px solid #333;
  font-weight: 600;
}

.token-key.sub,
.token-val.sub {
  color: #888;
  font-size: 11px;
}

/* Token tooltip transition */
.token-tooltip-enter-active,
.token-tooltip-leave-active {
  transition: opacity 0.15s, transform 0.15s;
}

.token-tooltip-enter-from,
.token-tooltip-leave-to {
  opacity: 0;
  transform: translateX(-50%) translateY(-6px);
}

/* Time distribution tooltip */
.time-tooltip {
  position: absolute;
  top: calc(100% + 8px);
  left: 50%;
  transform: translateX(-50%);
  background: #1e1e1e;
  border: 1px solid #444;
  border-radius: 8px;
  padding: 10px 12px;
  min-width: 220px;
  max-width: 320px;
  max-height: 360px;
  overflow-y: auto;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.5);
  z-index: 100;
}

.time-tooltip::before {
  content: '';
  position: absolute;
  bottom: 100%;
  left: 50%;
  transform: translateX(-50%);
  border: 6px solid transparent;
  border-bottom-color: #444;
}

.time-tooltip-header {
  font-size: 11px;
  color: #888;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 8px;
  padding-bottom: 6px;
  border-bottom: 1px solid #333;
}

.time-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  font-size: 12px;
  color: #d4d4d4;
  padding: 3px 0;
}

.time-row.time-total {
  margin-top: 6px;
  padding-top: 6px;
  border-top: 1px solid #333;
  font-weight: 600;
}

.time-step-label {
  display: flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.time-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #666;
  flex-shrink: 0;
}

.time-dot.dot-running {
  background: #f0a030;
  animation: dot-pulse 1s ease-in-out infinite;
}

@keyframes dot-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}

.time-step-val {
  color: #888;
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}

.time-step-val.running {
  color: #f0a030;
}

/* Agent selector */
.agent-select {
  background: #1e1e1e;
  border: 1px solid #444;
  border-radius: 4px;
  color: #d4d4d4;
  font-size: 12px;
  padding: 2px 8px;
  font-family: inherit;
  cursor: pointer;
}

/* Auto-Approve toggle switch */
.toggle-switch {
  position: relative;
  display: inline-block;
  width: 36px;
  height: 20px;
  cursor: pointer;
}

.toggle-switch input {
  opacity: 0;
  width: 0;
  height: 0;
}

.toggle-slider {
  position: absolute;
  inset: 0;
  background: #444;
  border-radius: 20px;
  transition: background 0.25s;
}

.toggle-slider::before {
  content: '';
  position: absolute;
  height: 14px;
  width: 14px;
  left: 3px;
  bottom: 3px;
  background: #d4d4d4;
  border-radius: 50%;
  transition: transform 0.25s;
}

.toggle-switch input:checked + .toggle-slider {
  background: #4a9eff;
}

.toggle-switch input:checked + .toggle-slider::before {
  transform: translateX(16px);
  background: #fff;
}
</style>