/**
 * AgentLane.vue
 *
 * 单个 Agent 的泳道：头部展示 Agent 名称、模型、状态指示灯、Token 统计；
 * 主体渲染 StepCard 列表。运行中且 showControls 时显示 pause/resume/cancel 控件。
 */
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import StepCard from './StepCard.vue'
import type { AgentState } from '@/types/events'

interface Props {
  agent: AgentState
  isRunning?: boolean
  expandAll?: boolean
  showControls?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  isRunning: false,
  expandAll: true,
  showControls: false,
})

const emit = defineEmits<{
  (e: 'pause', agentId: string): void
  (e: 'resume', agentId: string): void
  (e: 'cancel', agentId: string): void
}>()

const color = computed(() => props.agent.color || 'var(--accent-running)')
const status = computed(() => props.agent.status || 'idle')

const expanded = ref(props.expandAll)

watch(
  () => props.expandAll,
  (v) => {
    expanded.value = v
  }
)

const totalTokens = computed(() => {
  if (props.agent.tokenUsage) return props.agent.tokenUsage.totalTokens
  return props.agent.steps.reduce((sum, s) => sum + (s.tokens || 0), 0)
})

const durationMs = computed(() => {
  if (props.agent.durationMs) return props.agent.durationMs
  return props.agent.steps.reduce((sum, s) => sum + (s.durationMs || 0), 0)
})

const statusClass = computed(() => {
  switch (status.value) {
    case 'running':
      return 'badge--running'
    case 'paused':
      return 'badge--warning'
    case 'completed':
      return 'badge--success'
    case 'failed':
      return 'badge--danger'
    default:
      return 'badge--idle'
  }
})

function formatDuration(ms: number): string {
  if (!ms || ms < 0) return '0ms'
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function onPause() {
  emit('pause', props.agent.id)
}
function onResume() {
  emit('resume', props.agent.id)
}
function onCancel() {
  emit('cancel', props.agent.id)
}
</script>

<template>
  <div
    class="agent-lane"
    :class="{ 'agent-lane--collapsed': !expanded }"
    :style="{ '--agent-color': color }"
  >
    <header class="agent-header" @click="expanded = !expanded">
      <div class="agent-header-main">
        <span class="agent-pulse" aria-hidden="true" />
        <span class="agent-name">{{ agent.name }}</span>
        <span class="agent-model badge badge--idle">{{ agent.model }}</span>
      </div>

      <div class="agent-header-meta">
        <span class="agent-tokens">{{ totalTokens }}t</span>
        <span class="agent-duration">{{ formatDuration(durationMs) }}</span>
        <span class="agent-status badge" :class="statusClass">{{ status }}</span>
        <button
          type="button"
          class="expand-toggle focus-glow"
          :aria-expanded="expanded"
          aria-label="Toggle agent lane"
          @click.stop="expanded = !expanded"
        >
          {{ expanded ? '−' : '+' }}
        </button>
      </div>
    </header>

    <Transition name="lane-expand">
      <div v-show="expanded" class="agent-body">
        <div v-if="showControls && isRunning" class="agent-controls">
          <button
            v-if="status === 'running'"
            type="button"
            class="control-btn control-btn--pause focus-glow"
            @click="onPause"
          >
            Pause
          </button>
          <button
            v-else-if="status === 'paused'"
            type="button"
            class="control-btn control-btn--resume focus-glow"
            @click="onResume"
          >
            Resume
          </button>
          <button
            type="button"
            class="control-btn control-btn--cancel focus-glow"
            @click="onCancel"
          >
            Cancel
          </button>
        </div>

        <ul v-if="agent.steps.length" class="step-list">
          <li
            v-for="(step, idx) in agent.steps"
            :key="`${agent.id}-${step.index}-${idx}`"
            class="step-item"
            :style="{ animationDelay: `${idx * 40}ms` }"
          >
            <StepCard :step="step" :agent-color="color" />
          </li>
        </ul>

        <div v-else class="empty-steps">
          No steps yet.
        </div>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.agent-lane {
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-panel);
  overflow: hidden;
}

.agent-header {
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

.agent-header:hover {
  background: var(--bg-hover);
}

.agent-header-main {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  min-width: 0;
}

.agent-pulse {
  flex-shrink: 0;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--agent-color);
}

.agent-lane .agent-pulse {
  animation: status-pulse 1.6s ease-in-out infinite;
}

.agent-lane:has(.badge--success) .agent-pulse,
.agent-lane:has(.badge--idle) .agent-pulse,
.agent-lane:has(.badge--danger) .agent-pulse {
  animation: none;
}

.agent-name {
  font-family: var(--font-display);
  font-weight: 600;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.agent-model {
  flex-shrink: 0;
}

.agent-header-meta {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.agent-tokens,
.agent-duration {
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

.agent-body {
  padding: var(--space-md);
}

.agent-controls {
  display: flex;
  gap: var(--space-sm);
  margin-bottom: var(--space-md);
}

.control-btn {
  padding: 4px 12px;
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  background: var(--bg-elevated);
  color: var(--text-primary);
  font-family: var(--font-display);
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  transition: all var(--transition-fast);
}

.control-btn--pause:hover {
  border-color: var(--accent-warning);
  color: var(--accent-warning);
}

.control-btn--resume:hover {
  border-color: var(--accent-success);
  color: var(--accent-success);
}

.control-btn--cancel:hover {
  border-color: var(--accent-danger);
  color: var(--accent-danger);
}

.step-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-sm);
}

.step-item {
  /* stagger delay is set inline per index */
}

.empty-steps {
  padding: var(--space-md);
  text-align: center;
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.8rem;
}

@media (max-width: 767px) {
  .agent-header {
    padding: var(--space-xs) var(--space-sm);
    gap: var(--space-sm);
    flex-wrap: wrap;
  }

  .agent-name {
    font-size: 0.85rem;
    max-width: 120px;
  }

  .agent-model {
    display: none;
  }

  .agent-header-meta {
    font-size: 0.7rem;
    gap: var(--space-xs);
  }

  .agent-body {
    padding: var(--space-sm);
  }

  .agent-controls {
    margin-bottom: var(--space-sm);
  }

  .control-btn {
    flex: 1;
    padding: 6px 0;
    text-align: center;
  }

  .empty-steps {
    padding: var(--space-sm);
    font-size: 0.75rem;
  }
}
.lane-expand-leave-active {
  transition:
    max-height 260ms ease,
    opacity 200ms ease,
    padding 260ms ease;
  max-height: 1200px;
  overflow: hidden;
}

.lane-expand-enter-from,
.lane-expand-leave-to {
  max-height: 0;
  opacity: 0;
  padding-top: 0;
  padding-bottom: 0;
}
</style>
