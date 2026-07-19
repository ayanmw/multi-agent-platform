/**
 * TimelineTrack.vue
 *
 * 一个 Turn 的轨道容器。头部始终可见，可点击展开/折叠；
 * 展开后渲染该 Turn 内每个 Agent 的 AgentLane 泳道。
 */
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AgentLane from './AgentLane.vue'
import type { AgentState, TaskState, TaskStatus } from '@/types/events'

interface Props {
  task: TaskState
  turnIndex: number
  userInput: string
  expandAll?: boolean
  showAgentControls?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  expandAll: true,
  showAgentControls: false,
})

const emit = defineEmits<{
  (e: 'toggle', taskId: string): void
  (e: 'cancelAgent', payload: { taskId: string; agentId: string }): void
  (e: 'pauseAgent', payload: { taskId: string; agentId: string }): void
  (e: 'resumeAgent', payload: { taskId: string; agentId: string }): void
}>()

const expanded = ref(props.expandAll)

watch(
  () => props.expandAll,
  (v) => {
    expanded.value = v
  }
)

const agents = computed<AgentState[]>(() => {
  return Object.values(props.task.agents || {})
})

const isRunning = computed(() => props.task.status === 'running')

const statusIcon = computed(() => {
  const map: Record<TaskStatus, string> = {
    idle: '○',
    running: '▶',
    completed: '✓',
    failed: '✕',
  }
  return map[props.task.status] || '○'
})

const statusClass = computed(() => {
  const map: Record<TaskStatus, string> = {
    idle: 'track-status--idle',
    running: 'track-status--running',
    completed: 'track-status--completed',
    failed: 'track-status--failed',
  }
  return map[props.task.status] || 'track-status--idle'
})

const inputSummary = computed(() => {
  const input = props.userInput || props.task.userInput || '(empty input)'
  return input.length > 80 ? input.slice(0, 80) + '...' : input
})

const totalTokens = computed(() => {
  return props.task.totalTokens || 0
})

const durationMs = computed(() => {
  return props.task.durationMs || 0
})

function formatDuration(ms: number): string {
  if (!ms || ms < 0) return '0ms'
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function toggle() {
  expanded.value = !expanded.value
  emit('toggle', props.task.id)
}

function onPauseAgent(agentId: string) {
  emit('pauseAgent', { taskId: props.task.id, agentId })
}

function onResumeAgent(agentId: string) {
  emit('resumeAgent', { taskId: props.task.id, agentId })
}

function onCancelAgent(agentId: string) {
  emit('cancelAgent', { taskId: props.task.id, agentId })
}
</script>

<template>
  <section class="timeline-track" :class="statusClass">
    <header
      class="track-header"
      :class="{ 'track-header--expanded': expanded }"
      @click="toggle"
    >
      <div class="track-header-main">
        <span class="turn-index">#{{ turnIndex }}</span>
        <span class="status-icon" aria-hidden="true">{{ statusIcon }}</span>
        <span class="user-input" :title="userInput || task.userInput">
          {{ inputSummary }}
        </span>
      </div>

      <div class="track-header-meta">
        <span class="track-tokens">{{ totalTokens }}t</span>
        <span class="track-duration">{{ formatDuration(durationMs) }}</span>
        <button
          type="button"
          class="expand-toggle focus-glow"
          :aria-expanded="expanded"
          aria-label="Toggle turn track"
          @click.stop="toggle"
        >
          {{ expanded ? '−' : '+' }}
        </button>
      </div>
    </header>

    <Transition name="track-expand">
      <div v-show="expanded" class="track-body">
        <div class="lane-grid">
          <AgentLane
            v-for="agent in agents"
            :key="agent.id"
            :agent="agent"
            :is-running="isRunning"
            :expand-all="expandAll"
            :show-controls="showAgentControls"
            @pause="onPauseAgent"
            @resume="onResumeAgent"
            @cancel="onCancelAgent"
          />
        </div>

        <div v-if="task.finalResult" class="final-result">
          <div class="final-result-label">Result</div>
          <div class="final-result-body">{{ task.finalResult }}</div>
        </div>
      </div>
    </Transition>
  </section>
</template>

<style scoped>
.timeline-track {
  border: 1px solid var(--border-default);
  border-radius: var(--radius-lg);
  background: var(--bg-panel);
  overflow: hidden;
}

.timeline-track:not(:last-child) {
  margin-bottom: var(--space-md);
}

.track-status--running {
  border-color: rgba(0, 229, 255, 0.25);
}

.track-status--completed {
  border-color: rgba(57, 255, 20, 0.2);
}

.track-status--failed {
  border-color: rgba(255, 77, 77, 0.3);
}

.track-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-md);
  padding: var(--space-sm) var(--space-md);
  background: var(--bg-elevated);
  cursor: pointer;
  user-select: none;
  transition: background var(--transition-fast);
}

.track-header:hover {
  background: var(--bg-hover);
}

.track-header-main {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  min-width: 0;
}

.turn-index {
  flex-shrink: 0;
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--text-muted);
}

.status-icon {
  flex-shrink: 0;
  width: 1.2rem;
  text-align: center;
  font-size: 0.8rem;
}

.track-status--running .status-icon {
  color: var(--accent-running);
}

.track-status--completed .status-icon {
  color: var(--accent-success);
}

.track-status--failed .status-icon {
  color: var(--accent-danger);
}

.user-input {
  min-width: 0;
  font-family: var(--font-mono);
  font-size: 0.85rem;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.track-header-meta {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.track-tokens,
.track-duration {
  white-space: nowrap;
}

.expand-toggle {
  width: 22px;
  height: 22px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  background: var(--bg-panel);
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: 0.8rem;
}

.expand-toggle:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.track-body {
  padding: var(--space-md);
}

.lane-grid {
  display: grid;
  grid-template-columns: 1fr;
  gap: var(--space-md);
}

@media (max-width: 767px) {
  .track-header {
    padding: var(--space-xs) var(--space-sm);
    gap: var(--space-sm);
    flex-wrap: wrap;
  }

  .track-header-main {
    min-width: 0;
    flex: 1;
  }

  .user-input {
    font-size: 0.8rem;
  }

  .track-header-meta {
    font-size: 0.7rem;
    gap: var(--space-xs);
  }

  .track-body {
    padding: var(--space-sm);
  }

  .lane-grid {
    gap: var(--space-sm);
  }

  .final-result-label {
    font-size: 0.65rem;
  }

  .final-result-body {
    font-size: 0.8rem;
    padding: var(--space-xs) var(--space-sm);
  }
}

.final-result {
  margin-top: var(--space-md);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: rgba(57, 255, 20, 0.04);
  overflow: hidden;
}

.final-result-label {
  padding: var(--space-xs) var(--space-sm);
  background: var(--bg-elevated);
  color: var(--accent-success);
  font-family: var(--font-display);
  font-size: 0.7rem;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.final-result-body {
  padding: var(--space-sm) var(--space-md);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.85rem;
  line-height: 1.55;
  white-space: pre-wrap;
  word-break: break-word;
}

.track-expand-enter-active,
.track-expand-leave-active {
  transition:
    max-height 280ms ease,
    opacity 200ms ease,
    padding 280ms ease;
  max-height: 2000px;
  overflow: hidden;
}

.track-expand-enter-from,
.track-expand-leave-to {
  max-height: 0;
  opacity: 0;
  padding-top: 0;
  padding-bottom: 0;
}
</style>
