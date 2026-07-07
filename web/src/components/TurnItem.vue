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
import type { TaskState } from '../types/events'
import AgentTree from './AgentTree.vue'

const props = defineProps<{
  task: TaskState
  turnIndex: number
  userInput: string
  isDefaultExpanded: boolean
}>()

const expanded = ref(props.isDefaultExpanded)

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
        <span v-if="task.totalTokens > 0" class="turn-tokens">
          {{ formatTokens(task.totalTokens) }} tokens
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
        <AgentTree :agent="agent" :is-running="task.status === 'running'" />
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