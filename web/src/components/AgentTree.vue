<!-- AgentTree — recursive tree visualization of agent execution steps
     Props:
       agent: the AgentState to render (from useTaskStore)
       isRunning: whether this agent is currently executing

     Renders:
       - Agent header (name, model, status indicator)
       - Recursive step list with:
         • Think steps: TypeWriter with streaming text + token count
         • Tool call steps: expandable tool card (name, input, output, duration)
         • Observation steps: collapsible observation text
       - Each step has expand/collapse toggle
       - Running steps auto-expand, completed steps default to collapsed

     Design rationale:
       The tree structure makes the agent's reasoning visible at a glance.
       Think → ToolCall → Observation → Think → ... forms a natural chain
       that the user can follow step by step. This is the core of the
       "white-box" Agent philosophy.
-->
<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import type { AgentState, Step, ToolCallData, TokenUsage } from '../types/events'
import StatusIndicator from './StatusIndicator.vue'
import TypeWriter from './TypeWriter.vue'

/** Readonly version of AgentState — compatible with Vue's readonly() wrapper */
interface ReadonlyAgentState {
  readonly id: string
  readonly name: string
  readonly model: string
  readonly steps: readonly Step[]
  readonly color?: string
  readonly maxSteps?: number
  readonly currentStep?: number
  readonly tokenUsage?: import('../types/events').TokenUsage
  readonly durationMs?: number
}

const props = defineProps<{
  agent: AgentState | ReadonlyAgentState
  isRunning: boolean
  /** External control: when true, all steps should be expanded; when false, collapse all.
   *  The component may still auto-expand the latest running step independently. */
  expandAll?: boolean
  /** Whether to show per-agent control buttons (cancel/pause/resume) */
  showControls?: boolean
}>()

const emit = defineEmits<{
  cancel: [agentId: string]
  pause: [agentId: string]
  resume: [agentId: string]
}>()

/** Track which steps are expanded (by index) */
const expandedSteps = ref<Set<number>>(new Set())

// Sync global expand/collapse commands with local expandedSteps
watch(
  () => props.expandAll,
  (expand) => {
    if (expand === true) {
      // Expand every step in this agent
      expandedSteps.value = new Set(props.agent.steps.map(s => s.index))
    } else if (expand === false) {
      // Collapse every step
      expandedSteps.value = new Set()
    }
  },
  { immediate: true },
)
const showTokenDetail = ref(false)

/** Toggle step expand/collapse */
function toggleStep(index: number) {
  if (expandedSteps.value.has(index)) {
    expandedSteps.value.delete(index)
  } else {
    expandedSteps.value.add(index)
  }
  // Trigger reactivity by replacing the Set
  expandedSteps.value = new Set(expandedSteps.value)
}

/** Check if a step is currently streaming (the last step while agent is running) */
function isStreaming(step: Step): boolean {
  return (
    props.isRunning &&
    step.type === 'think' &&
    step.status === 'running' &&
    step.index === props.agent.steps.length - 1
  )
}

/** Format duration in human-readable form */
function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
}

/** Format token count */
function formatTokens(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return `${n}`
}

/** Get the status icon for a step type */
function stepIcon(type: Step['type']): string {
  switch (type) {
    case 'think': return '🧠'
    case 'tool_call': return '🔧'
    case 'observation': return '👁️'
    default: return '•'
  }
}

/** Get a summary line for a step (shown when collapsed) */
function stepSummary(step: Step): string {
  switch (step.type) {
    case 'think':
      // First line of thinking text, trimmed
      return step.thinking.split('\n')[0].slice(0, 120) || '(thinking...)'
    case 'tool_call':
      return step.toolCall
        ? `${step.toolCall.name}(${JSON.stringify(step.toolCall.input).slice(0, 60)})`
        : '(tool call...)'
    case 'observation':
      return step.thinking.slice(0, 120) || '(observation...)'
    default:
      return ''
  }
}

/** Check if tool input JSON can be formatted (range check for safety) */
function isFormattableJSON(input: Record<string, unknown>): boolean {
  try {
    const s = JSON.stringify(input)
    return s.length > 0 && s.length < 50000
  } catch {
    return false
  }
}

/** Try to detect if tool output looks like JSON */
function isJSONOutput(output: string): boolean {
  const trimmed = output.trim()
  return (trimmed.startsWith('{') || trimmed.startsWith('['))
}

/** Format JSON string with indentation */
function formatJSON(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

/** Toggle JSON formatting for a tool input */
function toggleInputFormat(step: Step) {
  if (!step.toolCall) return
  const formatted = step.toolCall as ToolCallData & { _inputFormatted?: boolean; _inputCompact?: string }
  if (!formatted._inputFormatted) {
    formatted._inputCompact = JSON.stringify(formatted.input)
    formatted.input = JSON.parse(JSON.stringify(formatted.input)) // trigger reactivity
    formatted._inputFormatted = true
  } else {
    formatted._inputFormatted = false
    if (formatted._inputCompact) {
      formatted.input = JSON.parse(formatted._inputCompact)
    }
  }
  // Trigger reactivity by replacing the step
  expandedSteps.value = new Set(expandedSteps.value)
}

/** Toggle JSON formatting for tool output */
function toggleOutputFormat(step: Step) {
  if (!step.toolCall) return
  const formatted = step.toolCall as ToolCallData & { _outputFormatted?: boolean; _outputRaw?: string }
  if (!formatted._outputFormatted) {
    formatted._outputRaw = formatted.output
    formatted.output = formatJSON(formatted.output)
    formatted._outputFormatted = true
  } else {
    formatted._outputFormatted = false
    if (formatted._outputRaw) {
      formatted.output = formatted._outputRaw
    }
  }
  expandedSteps.value = new Set(expandedSteps.value)
}

/** Copy tool output to clipboard */
async function copyToolOutput(step: Step) {
  if (!step.toolCall) return
  try {
    await navigator.clipboard.writeText(step.toolCall.output)
  } catch {
    // Clipboard API not available
  }
}

/** Total tokens across all steps (prefer detailed token usage from agent_status) */
const totalTokens = computed(() => {
  if (props.agent.tokenUsage?.totalTokens) {
    return props.agent.tokenUsage.totalTokens
  }
  let total = 0
  for (const s of props.agent.steps) {
    total += s.tokens
  }
  return total
})

/** Total duration across all agent steps. */
const agentDuration = computed(() => {
  if (props.agent.durationMs && props.agent.durationMs > 0) {
    return props.agent.durationMs
  }
  if (props.agent.steps.length === 0) return 0
  let total = 0
  for (const s of props.agent.steps) {
    total += s.durationMs || 0
  }
  return total
})

/** Detailed token usage breakdown for the agent; fall back to step totals as input. */
const tokenUsage = computed<TokenUsage>(() => {
  if (props.agent.tokenUsage) {
    return props.agent.tokenUsage
  }
  const input = totalTokens.value
  return {
    promptTokens: input,
    promptCacheHitTokens: 0,
    promptCacheMissTokens: input,
    completionTokens: 0,
    totalTokens: input,
  }
})

// Auto-expand the latest step when new events arrive and agent is running.
// This is independent of expandAll so users retain control when reading history.
watch(
  () => props.agent.steps.length,
  () => {
    // Respect explicit Collapse All: do not auto-expand new steps.
    // When expandAll is null/undefined we keep the existing auto-expand-latest behavior.
    if (props.expandAll === false) return
    const lastStep = props.agent.steps[props.agent.steps.length - 1]
    if (lastStep) {
      expandedSteps.value.add(lastStep.index)
      expandedSteps.value = new Set(expandedSteps.value)
    }
  }
)
</script>

<template>
  <div class="agent-tree">
    <!-- Agent header -->
    <div class="agent-header" :style="{ borderLeftColor: agent.color || '#4a9eff' }">
      <div class="agent-header-left">
        <span class="agent-color-dot" :style="{ background: agent.color || '#4a9eff' }"></span>
        <span class="agent-name">{{ agent.name }}</span>
        <span class="agent-model">{{ agent.model }}</span>
      </div>
      <div class="agent-header-right">
        <span v-if="agent.maxSteps" class="agent-steps">
          step {{ agent.currentStep ?? 0 }} / {{ agent.maxSteps }}
        </span>
        <StatusIndicator
          :status="isRunning ? 'running' : 'completed'"
          :label="isRunning ? 'Running' : `${agent.steps.length} steps`"
        />
        <span
          v-if="totalTokens > 0"
          class="agent-tokens"
          @mouseenter="showTokenDetail = true"
          @mouseleave="showTokenDetail = false"
        >
          {{ formatTokens(totalTokens) }} tokens
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
          v-if="agentDuration > 0"
          class="agent-duration"
        >
          {{ formatDuration(agentDuration) }}
        </span>
        <div v-if="showControls && isRunning" class="agent-controls">
          <button class="agent-control-btn" @click.stop="emit('pause', agent.id)">Pause</button>
          <button class="agent-control-btn" @click.stop="emit('resume', agent.id)">Resume</button>
          <button class="agent-control-btn cancel" @click.stop="emit('cancel', agent.id)">Cancel</button>
        </div>
      </div>
    </div>

    <!-- Step list -->
    <div class="step-list">
      <!-- Skeleton screen — shown when agent is running but no steps yet -->
      <div v-if="agent.steps.length === 0 && isRunning" class="skeleton-list">
        <div class="skeleton-item">
          <div class="skeleton-line skeleton-line-short"></div>
          <div class="skeleton-line skeleton-line-long"></div>
        </div>
        <div class="skeleton-item">
          <div class="skeleton-line skeleton-line-medium"></div>
          <div class="skeleton-line skeleton-line-long"></div>
          <div class="skeleton-line skeleton-line-short"></div>
        </div>
        <div class="skeleton-item">
          <div class="skeleton-line skeleton-line-long"></div>
          <div class="skeleton-line skeleton-line-medium"></div>
        </div>
      </div>

      <div
        v-for="step in agent.steps"
        :key="step.index + '-' + step.type"
        class="step-item"
        :class="{
          'step-running': step.status === 'running',
          'step-completed': step.status === 'completed',
          'step-failed': step.status === 'failed',
          'step-expanded': expandedSteps.has(step.index),
        }"
      >
        <!-- Step header (always visible, clickable to expand) -->
        <div class="step-header" @click="toggleStep(step.index)">
          <span class="step-toggle">{{ expandedSteps.has(step.index) ? '▼' : '▶' }}</span>
          <span class="step-icon">{{ stepIcon(step.type) }}</span>
          <span class="step-index">#{{ step.index }}</span>
          <span class="step-type">{{ step.type }}</span>
          <StatusIndicator :status="step.status" />
          <span class="step-summary">{{ stepSummary(step) }}</span>
          <span v-if="step.toolCall?.duration" class="step-duration">
            {{ formatDuration(step.toolCall.duration) }}
          </span>
        </div>

        <!-- Step detail (visible when expanded) -->
        <div v-if="expandedSteps.has(step.index)" class="step-detail">
          <!-- Think step: show TypeWriter -->
          <div v-if="step.type === 'think' || step.type === 'observation'" class="step-thinking">
            <TypeWriter
              :text="step.thinking"
              :is-streaming="isStreaming(step)"
            />
          </div>

          <!-- Tool call step: show tool card -->
          <div v-if="step.type === 'tool_call' && step.toolCall" class="tool-card">
            <div class="tool-card-header">
              <strong>{{ step.toolCall.name }}</strong>
              <span v-if="step.toolCall.duration" class="tool-duration">
                {{ formatDuration(step.toolCall.duration) }}
              </span>
            </div>

            <!-- Tool input -->
            <details class="tool-detail-section" open>
              <summary>Input</summary>
              <div class="tool-input-actions">
                <button
                  v-if="isFormattableJSON(step.toolCall.input)"
                  class="tool-action-btn"
                  @click="toggleInputFormat(step)"
                >Format</button>
              </div>
              <pre class="tool-json">{{ JSON.stringify(step.toolCall.input, null, 2) }}</pre>
            </details>

            <!-- Tool output -->
            <details v-if="step.toolCall.output" class="tool-detail-section">
              <summary>Output</summary>
              <div class="tool-output-actions">
                <button
                  v-if="isJSONOutput(step.toolCall.output)"
                  class="tool-action-btn"
                  @click="toggleOutputFormat(step)"
                >Format</button>
                <button class="tool-action-btn" @click="copyToolOutput(step)">Copy</button>
              </div>
              <pre class="tool-output">{{ step.toolCall.output }}</pre>
            </details>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.agent-tree {
  background: #1e1e1e;
  border: 1px solid #333;
  border-radius: 8px;
  overflow: hidden;
  margin-bottom: 16px;
  position: relative;
}

/* Agent header */
.agent-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 14px;
  background: #252525;
  border-bottom: 1px solid #333;
  border-left: 3px solid #4a9eff;
  transition: border-left-color 0.3s;
}

.agent-header-left {
  display: flex;
  align-items: center;
  gap: 10px;
}

.agent-color-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex-shrink: 0;
}

.agent-name {
  font-weight: 600;
  font-size: 14px;
  color: #e0e0e0;
}

.agent-model {
  font-size: 11px;
  color: #888;
  background: #333;
  padding: 2px 8px;
  border-radius: 10px;
}

.agent-header-right {
  display: flex;
  align-items: center;
  gap: 12px;
}

.agent-tokens {
  font-size: 11px;
  color: #888;
  cursor: help;
  position: relative;
}

.agent-duration {
  font-size: 11px;
  color: #888;
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

.agent-steps {
  font-size: 11px;
  color: #888;
  background: #2a2a2a;
  padding: 2px 8px;
  border-radius: 10px;
}

.agent-controls {
  display: flex;
  gap: 4px;
  margin-left: 6px;
}

.agent-control-btn {
  background: #333;
  border: 1px solid #444;
  color: #aaa;
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 11px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.agent-control-btn:hover {
  background: #4a9eff;
  color: #fff;
  border-color: #4a9eff;
}

.agent-control-btn.cancel:hover {
  background: #ff6b6b;
  border-color: #ff6b6b;
}

/* Step list */
.step-list {
  padding: 0;
  max-height: 500px;
  overflow-y: auto;
  position: relative;
}

.step-item {
  border-bottom: 1px solid #2a2a2a;
}

.step-item:last-child {
  border-bottom: none;
}

.step-item.step-running {
  background: #1a2a3a;
}

/* Step header (always visible) */
.step-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  cursor: pointer;
  user-select: none;
  font-size: 12px;
  color: #ccc;
  transition: background 0.15s;
}

.step-header:hover {
  background: #2a2a2a;
}

.step-toggle {
  font-size: 10px;
  color: #666;
  width: 12px;
  flex-shrink: 0;
}

.step-icon {
  flex-shrink: 0;
}

.step-type {
  font-weight: 600;
  color: #aaa;
  text-transform: uppercase;
  font-size: 10px;
  letter-spacing: 0.5px;
  flex-shrink: 0;
}

.step-index {
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 10px;
  color: #888;
  background: #2a2a2a;
  padding: 1px 5px;
  border-radius: 4px;
  flex-shrink: 0;
}

.step-summary {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #888;
  font-size: 11px;
}

.step-duration {
  font-size: 11px;
  color: #666;
  flex-shrink: 0;
}

/* Step detail (visible when expanded) */
.step-detail {
  padding: 8px 14px 12px 34px;
  background: #1a1a1a;
}

.step-thinking {
  max-height: 400px;
  overflow-y: auto;
}

/* Tool card */
.tool-card {
  background: #252525;
  border: 1px solid #333;
  border-radius: 6px;
  padding: 10px;
}

.tool-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
  font-size: 13px;
  color: #d4d4d4;
}

.tool-duration {
  font-size: 11px;
  color: #888;
}

.tool-detail-section {
  margin-top: 6px;
}

.tool-detail-section summary {
  cursor: pointer;
  font-size: 11px;
  color: #999;
  padding: 4px 0;
}

.tool-detail-section summary:hover {
  color: #ccc;
}

/* Tool action buttons (Format/Copy) */
.tool-input-actions,
.tool-output-actions {
  display: flex;
  gap: 6px;
  margin-bottom: 4px;
}

.tool-action-btn {
  background: #333;
  color: #999;
  border: 1px solid #444;
  border-radius: 4px;
  padding: 1px 8px;
  font-size: 10px;
  font-family: inherit;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.tool-action-btn:hover {
  background: #4a9eff;
  color: #fff;
  border-color: #4a9eff;
}

.tool-json {
  background: #1e1e1e;
  padding: 8px;
  border-radius: 4px;
  font-size: 11px;
  color: #ce9178;
  max-height: 200px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 4px 0;
}

.tool-output {
  background: #1e1e1e;
  padding: 8px;
  border-radius: 4px;
  font-size: 11px;
  color: #6a9955;
  max-height: 300px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 4px 0;
}

/* Skeleton screen — shown when agent is running but no steps yet */
.skeleton-list {
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.skeleton-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.skeleton-line {
  height: 10px;
  background: #333;
  border-radius: 4px;
  animation: skeleton-pulse 1.5s ease-in-out infinite;
}

.skeleton-line-short {
  width: 40%;
}

.skeleton-line-medium {
  width: 65%;
}

.skeleton-line-long {
  width: 85%;
}

@keyframes skeleton-pulse {
  0%, 100% { opacity: 0.4; }
  50% { opacity: 0.8; }
}
</style>