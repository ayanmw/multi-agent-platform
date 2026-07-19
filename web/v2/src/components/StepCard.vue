/**
 * StepCard.vue
 *
 * 渲染 Agent 执行树中的单个 Step：think / tool_call / observation。
 * Step 入场时带有从左侧滑入 + 淡入的动画（通过宿主设置 animation-delay 实现错落）。
 */
<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Step, StepStatus } from '@/types/events'

interface Props {
  step: Step
  agentColor?: string
}

const props = defineProps<Props>()

const color = computed(() => props.agentColor || 'var(--accent-running)')

const statusClassMap: Record<StepStatus, string> = {
  running: 'status--running',
  completed: 'status--completed',
  failed: 'status--failed',
  paused: 'status--paused',
}

const cardClass = computed(() => {
  switch (props.step.type) {
    case 'think':
      return 'step-card--think'
    case 'tool_call':
      return 'step-card--tool'
    case 'observation':
      return 'step-card--observation'
    default:
      return ''
  }
})

const truncatedThinking = computed(() => {
  const t = props.step.thinking
  if (!t) return 'Thinking...'
  return t.length > 240 ? t.slice(0, 240) + '...' : t
})

const toolName = computed(() => {
  const tc = props.step.toolCall
  if (!tc) return 'tool'
  return tc.shortName || tc.name.split('/').pop() || tc.name
})

const toolNamespace = computed(() => {
  const tc = props.step.toolCall
  if (!tc) return ''
  if (tc.namespace) return tc.namespace
  if (tc.name.includes('/')) return tc.name.split('/').slice(0, -1).join('/')
  return ''
})

const paramSummary = computed(() => {
  const tc = props.step.toolCall
  if (!tc) return ''
  const keys = Object.keys(tc.input || {})
  if (!keys.length) return 'no args'
  return keys.slice(0, 3).join(', ') + (keys.length > 3 ? ` +${keys.length - 3}` : '')
})

const formattedInput = computed(() => {
  const tc = props.step.toolCall
  if (!tc) return ''
  return JSON.stringify(tc.input || {}, null, 2)
})

const formattedOutput = computed(() => {
  const tc = props.step.toolCall
  if (!tc) return ''
  return tc.output || ''
})

const showInput = ref(false)
const showOutput = ref(false)

function formatDuration(ms: number): string {
  if (!ms || ms < 0) return '0ms'
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}
</script>

<template>
  <div
    class="step-card animate-slide-in"
    :class="[cardClass, statusClassMap[step.status]]"
    :style="{ '--agent-color': color }"
  >
    <!-- Think step -->
    <template v-if="step.type === 'think'">
      <div class="step-header">
        <span class="step-type-icon">LIGHTBULB</span>
        <span class="step-type-label">THINK</span>
        <span class="step-meta">
          <span class="step-tokens">{{ step.tokens }}t</span>
          <span class="step-duration">{{ formatDuration(step.durationMs) }}</span>
        </span>
      </div>
      <p class="think-body">{{ truncatedThinking }}</p>
    </template>

    <!-- Tool call step -->
    <template v-else-if="step.type === 'tool_call' && step.toolCall">
      <div class="step-header">
        <span class="step-type-icon" :style="{ color: 'var(--accent-tool)' }">WRENCH</span>
        <div class="tool-name-block">
          <span v-if="toolNamespace" class="tool-namespace">{{ toolNamespace }}/</span>
          <span class="tool-name">{{ toolName }}</span>
        </div>
        <span class="step-meta">
          <span class="step-tags" v-if="step.toolCall.tags?.length">
            <span
              v-for="tag in step.toolCall.tags.slice(0, 2)"
              :key="tag"
              class="tool-tag"
            >{{ tag }}</span>
          </span>
          <span class="step-duration">{{ formatDuration(step.toolCall.duration) }}</span>
        </span>
      </div>

      <div class="tool-summary">{{ paramSummary }}</div>

      <div class="tool-toggles">
        <button
          type="button"
          class="tool-toggle focus-glow"
          :class="{ 'tool-toggle--active': showInput }"
          @click="showInput = !showInput"
        >
          Input
        </button>
        <button
          type="button"
          class="tool-toggle focus-glow"
          :class="{ 'tool-toggle--active': showOutput }"
          @click="showOutput = !showOutput"
        >
          Output
        </button>
      </div>

      <Transition name="expand">
        <div v-show="showInput" class="tool-block">
          <div class="tool-block-label">Input</div>
          <pre class="tool-code"><code>{{ formattedInput }}</code></pre>
        </div>
      </Transition>

      <Transition name="expand">
        <div v-show="showOutput" class="tool-block">
          <div class="tool-block-label">Output</div>
          <pre class="tool-code"><code>{{ formattedOutput }}</code></pre>
        </div>
      </Transition>
    </template>

    <!-- Observation step -->
    <template v-else-if="step.type === 'observation'">
      <div class="step-header">
        <span class="step-type-icon">EYE</span>
        <span class="step-type-label">OBSERVATION</span>
      </div>
      <div class="observation-body">{{ step.thinking }}</div>
    </template>
  </div>
</template>

<style scoped>
.step-card {
  position: relative;
  padding: var(--space-sm) var(--space-md);
  margin-left: 2px;
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-panel);
  font-size: 0.85rem;
  transition:
    border-color var(--transition-base),
    box-shadow var(--transition-base);
}

.step-card::before {
  content: '';
  position: absolute;
  left: -3px;
  top: 8px;
  bottom: 8px;
  width: 3px;
  border-radius: 2px;
  background: var(--agent-color);
  opacity: 0.8;
}

.step-card--think {
  background: linear-gradient(90deg, rgba(255, 255, 255, 0.03) 0%, transparent 100%);
}

.step-card--tool {
  border-color: rgba(168, 85, 247, 0.25);
  background: rgba(168, 85, 247, 0.04);
}

.step-card--observation {
  border-style: dashed;
  background: rgba(255, 255, 255, 0.02);
}

.step-header {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  margin-bottom: var(--space-xs);
}

.step-type-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.2em;
  height: 1.2em;
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--agent-color);
}

.step-type-label {
  font-family: var(--font-display);
  font-size: 0.7rem;
  font-weight: 600;
  letter-spacing: 0.08em;
  color: var(--text-secondary);
  text-transform: uppercase;
}

.step-meta {
  display: inline-flex;
  align-items: center;
  gap: var(--space-sm);
  margin-left: auto;
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--text-muted);
}

.step-tokens,
.step-duration {
  white-space: nowrap;
}

.think-body {
  margin: 0;
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.8rem;
  line-height: 1.55;
  white-space: pre-wrap;
  word-break: break-word;
}

.tool-name-block {
  display: inline-flex;
  align-items: baseline;
  font-family: var(--font-mono);
}

.tool-namespace {
  color: var(--text-muted);
  font-size: 0.75rem;
}

.tool-name {
  color: var(--accent-tool);
  font-weight: 600;
}

.tool-summary {
  margin-bottom: var(--space-sm);
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: 0.75rem;
}

.step-tags {
  display: inline-flex;
  gap: 4px;
}

.tool-tag {
  padding: 0 4px;
  border-radius: var(--radius-sm);
  background: rgba(255, 77, 77, 0.12);
  color: var(--accent-danger);
  font-size: 0.6rem;
  text-transform: uppercase;
}

.tool-toggles {
  display: flex;
  gap: var(--space-sm);
  margin-bottom: var(--space-xs);
}

.tool-toggle {
  padding: 2px 8px;
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  background: var(--bg-elevated);
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: 0.7rem;
  transition: all var(--transition-fast);
}

.tool-toggle:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.tool-toggle--active {
  border-color: var(--accent-tool);
  color: var(--accent-tool);
  background: rgba(168, 85, 247, 0.1);
}

.tool-block {
  margin-top: var(--space-sm);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  overflow: hidden;
}

.tool-block-label {
  padding: 2px 8px;
  background: var(--bg-elevated);
  color: var(--text-muted);
  font-family: var(--font-display);
  font-size: 0.65rem;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

.tool-code {
  margin: 0;
  padding: var(--space-sm);
  max-height: 220px;
  overflow: auto;
  background: #0d0f13;
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.75rem;
  line-height: 1.5;
  white-space: pre;
  word-break: normal;
}

.observation-body {
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: 0.8rem;
  line-height: 1.55;
  white-space: pre-wrap;
  word-break: break-word;
}

@media (max-width: 767px) {
  .step-card {
    padding: var(--space-xs) var(--space-sm);
    font-size: 0.8rem;
  }

  .step-type-label {
    font-size: 0.65rem;
  }

  .think-body,
  .observation-body {
    font-size: 0.75rem;
  }

  .tool-name {
    font-size: 0.8rem;
  }

  .tool-summary {
    font-size: 0.7rem;
  }

  .tool-code {
    font-size: 0.7rem;
    max-height: 160px;
    -webkit-overflow-scrolling: touch;
  }
}
.status--failed {
  border-color: rgba(255, 77, 77, 0.35);
}

.status--failed::before {
  background: var(--accent-danger);
  box-shadow: 0 0 8px var(--accent-danger);
}

.status--paused {
  border-color: rgba(255, 184, 0, 0.35);
}

.status--paused::before {
  background: var(--accent-warning);
}

.status--completed .step-type-icon {
  color: var(--accent-success);
}

/* Expand transition used for code blocks */
.expand-enter-active,
.expand-leave-active {
  transition:
    max-height 240ms ease,
    opacity 200ms ease;
  max-height: 320px;
  overflow: hidden;
}

.expand-enter-from,
.expand-leave-to {
  max-height: 0;
  opacity: 0;
}
</style>
