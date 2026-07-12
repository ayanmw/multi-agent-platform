<!-- TurnItem.vue — single conversation turn component
     Displays one turn in the multi-turn conversation timeline.
     Each turn shows a header (always visible, clickable) with:
       - Turn index and user input summary
       - Status indicator (running/completed/failed)
       - Token count
     When expanded, shows the agent trees for this turn.
     Default: latest turn expanded, historical turns collapsed.

     Used by: TurnList.vue
-->
<script setup lang="ts">
import { ref, computed } from 'vue'
import type { TaskState, TokenUsage } from '../types/events'
import AgentTree from './AgentTree.vue'

const props = defineProps<{
  task: TaskState
  turnIndex: number
  userInput: string
  isDefaultExpanded: boolean
  /** Forwarded from App.vue: controls all AgentTree step expansion */
  expandAll?: boolean
}>()

const expanded = ref(props.isDefaultExpanded)
const showTokenDetail = ref(false)
const showTimeDetail = ref(false)

function toggle() {
  expanded.value = !expanded.value
}

const statusIcon = computed(() => {
  switch (props.task.status) {
    case 'running': return '🔄'
    case 'completed': return '✅'
    case 'failed': return '❌'
    default: return '⏳'
  }
})

const statusLabel = computed(() => {
  switch (props.task.status) {
    case 'running': return 'running'
    case 'completed': return 'completed'
    case 'failed': return 'failed'
    default: return 'pending'
  }
})

const summaryText = computed(() => {
  const input = props.userInput || '(no input)'
  return input.length > 100 ? input.slice(0, 100) + '...' : input
})

const resultPreview = computed(() => {
  if (!props.task.finalResult) return ''
  const r = props.task.finalResult
  return r.length > 150 ? r.slice(0, 150) + '...' : r
})

function formatTokens(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return `${n}`
}

function formatDuration(ms: number): string {
  if (ms <= 0) return '0s'
  const s = Math.floor(ms / 1000)
  const m = Math.floor(s / 60)
  const h = Math.floor(m / 60)
  if (h > 0) return `${h}h ${m % 60}m ${s % 60}s`
  if (m > 0) return `${m}m ${s % 60}s`
  return `${s}s`
}

/** Prefer detailed tokenUsage breakdown; fall back to task.totalTokens. */
const tokenUsage = computed<TokenUsage>(() => {
  if (props.task.tokenUsage) {
    return props.task.tokenUsage
  }
  const total = props.task.totalTokens || 0
  return {
    promptTokens: total,
    promptCacheHitTokens: 0,
    promptCacheMissTokens: total,
    completionTokens: 0,
    totalTokens: total,
  }
})

/** Turn-level duration: prefer backend value, otherwise compute from agents' steps. */
const turnDuration = computed(() => {
  if (props.task.durationMs && props.task.durationMs > 0) {
    return props.task.durationMs
  }
  return Object.values(props.task.agents).reduce(
    (sum, agent) => sum + agent.steps.reduce((a, s) => a + (s.durationMs || 0), 0),
    0
  )
})
</script>

<template>
  <div class="turn-item" :class="{ 'turn-expanded': expanded }">
    <!-- Turn Header (always visible, clickable) -->
    <div class="turn-header" @click="toggle">
      <span class="turn-toggle">{{ expanded ? '▼' : '▶' }}</span>
      <span class="turn-icon">{{ statusIcon }}</span>
      <div class="turn-info">
        <span class="turn-label">Turn {{ turnIndex + 1 }}</span>
        <span v-if="summaryText" class="turn-summary">: {{ summaryText }}</span>
      </div>
      <div class="turn-meta">
        <span :class="['turn-status', statusLabel]">{{ statusLabel }}</span>
        <span
          v-if="tokenUsage.totalTokens > 0"
          class="turn-tokens"
          @mouseenter="showTokenDetail = true"
          @mouseleave="showTokenDetail = false"
        >
          {{ formatTokens(tokenUsage.totalTokens) }} tokens
          <Transition name="token-tooltip">
            <div v-if="showTokenDetail" class="token-tooltip">
              <div class="token-row">
                <span class="token-key">Input</span>
                <span class="token-val">{{ formatTokens(tokenUsage.promptTokens) }}</span>
              </div>
              <div class="token-row indent">
                <span class="token-key sub">cache hit</span>
                <span class="token-val sub">{{ formatTokens(tokenUsage.promptCacheHitTokens) }}</span>
              </div>
              <div class="token-row indent">
                <span class="token-key sub">cache miss</span>
                <span class="token-val sub">{{ formatTokens(tokenUsage.promptCacheMissTokens) }}</span>
              </div>
              <div class="token-row">
                <span class="token-key">Output</span>
                <span class="token-val">{{ formatTokens(tokenUsage.completionTokens) }}</span>
              </div>
              <div class="token-row total">
                <span class="token-key">Total</span>
                <span class="token-val">{{ formatTokens(tokenUsage.totalTokens) }}</span>
              </div>
            </div>
          </Transition>
        </span>
        <span
          v-if="turnDuration > 0"
          class="turn-duration"
          @mouseenter="showTimeDetail = true"
          @mouseleave="showTimeDetail = false"
        >
          {{ formatDuration(turnDuration) }}
          <Transition name="token-tooltip">
            <div v-if="showTimeDetail" class="token-tooltip">
              <div class="token-row">
                <span class="token-key">Turn Time</span>
              </div>
              <div class="token-row total">
                <span class="token-key">Total</span>
                <span class="token-val">{{ formatDuration(turnDuration) }}</span>
              </div>
            </div>
          </Transition>
        </span>
      </div>
    </div>

    <!-- Turn Detail (visible when expanded) -->
    <div v-if="expanded" class="turn-body">
      <!-- Result preview (collapsed view shows final result) -->
      <div v-if="resultPreview && task.status !== 'running'" class="turn-result-preview">
        {{ resultPreview }}
      </div>

      <!-- Agent Trees -->
      <div v-for="agent in Object.values(task.agents)" :key="agent.id" class="turn-agent-tree">
        <AgentTree :agent="agent" :is-running="task.status === 'running'" :expand-all="expandAll" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.turn-item {
  border-bottom: 1px solid #333;
}

.turn-header {
  display: flex;
  align-items: center;
  padding: 10px 14px;
  cursor: pointer;
  transition: background 0.15s;
}

.turn-header:hover {
  background: #2a2a2a;
}

.turn-toggle {
  font-size: 10px;
  color: #666;
  width: 12px;
  flex-shrink: 0;
}

.turn-icon {
  font-size: 14px;
  margin-right: 8px;
  flex-shrink: 0;
}

.turn-info {
  flex: 1;
  overflow: hidden;
  display: flex;
  align-items: baseline;
  min-width: 0;
}

.turn-label {
  font-weight: 600;
  color: #e0e0e0;
  font-size: 13px;
  flex-shrink: 0;
}

.turn-summary {
  color: #888;
  font-size: 12px;
  text-overflow: ellipsis;
  overflow: hidden;
  white-space: nowrap;
}

.turn-meta {
  display: flex;
  gap: 12px;
  flex-shrink: 0;
  align-items: center;
}

.turn-status {
  font-size: 10px;
  text-transform: uppercase;
  padding: 2px 6px;
  border-radius: 10px;
  font-weight: 600;
}

.turn-status.running {
  background: rgba(74, 158, 255, 0.2);
  color: #4a9eff;
}

.turn-status.completed {
  background: rgba(81, 207, 102, 0.2);
  color: #51cf66;
}

.turn-status.failed {
  background: rgba(231, 76, 60, 0.2);
  color: #e74c3c;
}

.turn-status.pending {
  background: #333;
  color: #aaa;
}

.turn-tokens {
  font-size: 10px;
  color: #888;
  cursor: help;
  position: relative;
}

.turn-duration {
  font-size: 10px;
  color: #888;
  cursor: help;
  position: relative;
}

/* Token detail tooltip */
.token-tooltip {
  position: absolute;
  top: calc(100% + 8px);
  right: 0;
  background: #1e1e1e;
  border: 1px solid #444;
  border-radius: 8px;
  padding: 10px 12px;
  min-width: 150px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.5);
  z-index: 100;
}

.token-tooltip::before {
  content: '';
  position: absolute;
  bottom: 100%;
  right: 16px;
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
  white-space: nowrap;
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
  transform: translateY(-6px);
}

.turn-body {
  padding: 0 14px 12px 34px;
}

.turn-result-preview {
  font-size: 12px;
  color: #aaa;
  background: #252525;
  border-radius: 6px;
  padding: 8px 12px;
  margin-bottom: 10px;
  white-space: pre-wrap;
  max-height: 80px;
  overflow: hidden;
}

.turn-agent-tree {
  margin-top: 8px;
}
</style>