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
import { ref, computed } from 'vue'
import type { AgentState, Step } from '../types/events'
import StatusIndicator from './StatusIndicator.vue'
import TypeWriter from './TypeWriter.vue'

/** Readonly version of AgentState — compatible with Vue's readonly() wrapper */
interface ReadonlyAgentState {
  readonly id: string
  readonly name: string
  readonly model: string
  readonly steps: readonly Step[]
}

const props = defineProps<{
  agent: AgentState | ReadonlyAgentState
  isRunning: boolean
}>()

/** Track which steps are expanded (by index) */
const expandedSteps = ref<Set<number>>(new Set())

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

/** Total tokens across all steps */
const totalTokens = computed(() => {
  let total = 0
  for (const s of props.agent.steps) {
    total += s.tokens
  }
  return total
})
</script>

<template>
  <div class="agent-tree">
    <!-- Agent header -->
    <div class="agent-header">
      <div class="agent-header-left">
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
    <div class="step-list">
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
              <pre class="tool-json">{{ JSON.stringify(step.toolCall.input, null, 2) }}</pre>
            </details>

            <!-- Tool output -->
            <details v-if="step.toolCall.output" class="tool-detail-section">
              <summary>Output</summary>
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
}

/* Agent header */
.agent-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 14px;
  background: #252525;
  border-bottom: 1px solid #333;
}

.agent-header-left {
  display: flex;
  align-items: center;
  gap: 10px;
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
</style>