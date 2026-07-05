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
import { ref, computed, watch, nextTick } from 'vue'
import type { AgentState, Step, ToolCallData } from '../types/events'
import StatusIndicator from './StatusIndicator.vue'
import TypeWriter from './TypeWriter.vue'

/** Readonly version of AgentState — compatible with Vue's readonly() wrapper */
interface ReadonlyAgentState {
  readonly id: string
  readonly name: string
  readonly model: string
  readonly steps: readonly Step[]
  readonly color?: string
}

const props = defineProps<{
  agent: AgentState | ReadonlyAgentState
  isRunning: boolean
}>()

/** Track which steps are expanded (by index) */
const expandedSteps = ref<Set<number>>(new Set())

/** Scroll container ref for auto-scroll logic */
const scrollContainer = ref<HTMLElement | null>(null)
/** Whether the user has manually scrolled up (disables auto-scroll) */
const userScrolledUp = ref(false)
/** Whether to show the "scroll to bottom" button */
const showScrollBtn = ref(false)

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

/** Total tokens across all steps */
const totalTokens = computed(() => {
  let total = 0
  for (const s of props.agent.steps) {
    total += s.tokens
  }
  return total
})

// === Smart scroll: auto-scroll to bottom on new steps, but pause if user scrolls up ===

/** Check if the scroll container is at the bottom (within 40px tolerance) */
function isAtBottom(): boolean {
  const el = scrollContainer.value
  if (!el) return true
  return el.scrollHeight - el.scrollTop - el.clientHeight < 40
}

/** Scroll to the bottom of the container */
function scrollToBottom() {
  const el = scrollContainer.value
  if (!el) return
  el.scrollTop = el.scrollHeight
  userScrolledUp.value = false
  showScrollBtn.value = false
}

/** Handle user scroll events — detect if user scrolled up */
function handleScroll() {
  const atBottom = isAtBottom()
  if (atBottom) {
    userScrolledUp.value = false
    showScrollBtn.value = false
  } else {
    userScrolledUp.value = true
    showScrollBtn.value = true
  }
}

// Auto-scroll when steps change (new step added or thinking text updated)
watch(
  () => props.agent.steps.length,
  () => {
    if (!userScrolledUp.value) {
      nextTick(() => scrollToBottom())
    }
  }
)

// Also auto-scroll when the last step's thinking text grows (streaming)
watch(
  () => {
    const last = props.agent.steps[props.agent.steps.length - 1]
    return last?.thinking?.length ?? 0
  },
  () => {
    if (!userScrolledUp.value) {
      nextTick(() => scrollToBottom())
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
        <StatusIndicator
          :status="isRunning ? 'running' : 'completed'"
          :label="isRunning ? 'Running' : `${agent.steps.length} steps`"
        />
        <span v-if="totalTokens > 0" class="agent-tokens">{{ formatTokens(totalTokens) }} tokens</span>
      </div>
    </div>

    <!-- Step list -->
    <div
      ref="scrollContainer"
      class="step-list"
      @scroll="handleScroll"
    >
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
        :key="step.index"
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

    <!-- Scroll-to-bottom button — shown when user has scrolled up -->
    <Transition name="scroll-btn">
      <button
        v-if="showScrollBtn"
        class="scroll-bottom-btn"
        @click="scrollToBottom"
        title="Scroll to bottom"
      >
        ↓ Bottom
      </button>
    </Transition>
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

/* Scroll-to-bottom button */
.scroll-bottom-btn {
  position: absolute;
  bottom: 12px;
  left: 50%;
  transform: translateX(-50%);
  background: #4a9eff;
  color: #fff;
  border: none;
  border-radius: 16px;
  padding: 6px 16px;
  font-size: 11px;
  font-weight: 600;
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.4);
  z-index: 10;
  transition: opacity 0.2s, transform 0.2s;
}

.scroll-bottom-btn:hover {
  background: #3a8eef;
  transform: translateX(-50%) translateY(-2px);
}

/* Scroll button transition */
.scroll-btn-enter-active,
.scroll-btn-leave-active {
  transition: all 0.2s ease;
}

.scroll-btn-enter-from,
.scroll-btn-leave-to {
  opacity: 0;
  transform: translateX(-50%) translateY(10px);
}
</style>