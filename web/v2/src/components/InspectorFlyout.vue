<script setup lang="ts">
import { computed } from 'vue'
import type { TaskState } from '@/types/events'
import type { AgentRecord } from '@/composables/useAgentStore'
import type { WSStatus } from '@/composables/useWebSocket'

/**
 * InspectorFlyout — 浮在主舞台右上角的迷你 Inspector 卡片
 *
 * 设计意图：
 *   右栏已让位给 session 文件浏览器，Inspector 的重面板（Memory/RAG/Context/Cases/…）
 *   改为点击本卡片后弹出 90vw 大 Dialog 查看。卡片本身只显示"小细节"：
 *     - 连接状态 + 当前任务状态
 *     - 当前 session token / 耗时
 *     - 当前任务上下文窗口占用比（若已有快照）
 *     - 一行 tab 入口按钮，点击直接以对应 tab 打开大 Dialog
 *
 * Props:
 *   - task: 当前激活任务（可为 null）
 *   - sessionTotalTokens / sessionTotalDuration: 会话级聚合
 *   - wsStatus: WS 连接状态
 *   - agents: 可用 agent 列表（用于显示数量）
 *
 * Emits:
 *   - open-dialog(tab?): 请求打开 Inspector 大 Dialog，可选指定初始 tab
 */
const props = defineProps<{
  task: TaskState | null
  sessionTotalTokens: number
  sessionTotalDuration: number
  wsStatus: WSStatus
  agents: AgentRecord[]
}>()

const emit = defineEmits<{
  (e: 'open-dialog', tab?: string): void
}>()

const quickTabs = [
  { id: 'context', label: 'Context', icon: '🪟' },
  { id: 'memory', label: 'Memory', icon: '🧠' },
  { id: 'cases', label: 'Cases', icon: '📋' },
  { id: 'skills', label: 'Skills', icon: '✨' },
  { id: 'agents', label: 'Agents', icon: '⚙' },
  { id: 'traces', label: 'Traces', icon: '📡' },
] as const

const statusLabel = computed(() => {
  if (!props.task) return 'Idle'
  switch (props.task.status) {
    case 'running': return 'Running'
    case 'completed': return 'Completed'
    case 'failed': return 'Failed'
    case 'idle': return 'Idle'
    default: return props.task.status
  }
})

const statusClass = computed(() => {
  if (!props.task) return 'idle'
  return props.task.status || 'idle'
})

const wsLabel = computed(() => {
  switch (props.wsStatus) {
    case 'connected': return 'Live'
    case 'connecting': return 'Connecting'
    default: return 'Offline'
  }
})

function formatTokens(n: number): string {
  if (!n) return '0'
  if (n >= 1000000) return `${(n / 1000000).toFixed(2)}M`
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return `${n}`
}

function formatDuration(ms: number): string {
  if (!ms) return '0s'
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m${s % 60}s`
  const h = Math.floor(m / 60)
  return `${h}h${m % 60}m`
}

/** 上下文窗口占用比（来自最新 context_window_snapshot，若有）。 */
const ctxRatio = computed(() => {
  const agents = props.task?.agents
  if (!agents) return null
  // 取第一个 agent 的快照比例即可（单 agent 场景占绝大多数）。
  const first = Object.values(agents)[0]
  void first
  // ContextWindowPanel 通过 useContextWindow 读取快照；这里不直接依赖，
  // 仅用 task.tokenUsage 估算 prompt 占比，避免再引一个 composable。
  const tu = props.task?.tokenUsage
  if (!tu || !tu.totalTokens) return null
  return null
})

function open(tab?: string) {
  emit('open-dialog', tab)
}
</script>

<template>
  <div class="inspector-flyout">
    <div class="flyout-header">
      <span class="flyout-title">🧭 Inspector</span>
      <span class="flyout-ws" :class="wsStatus">{{ wsLabel }}</span>
    </div>

    <div class="flyout-body">
      <div class="metric-row">
        <div class="metric">
          <div class="metric-label">Task</div>
          <div class="metric-value" :class="'status-' + statusClass">{{ statusLabel }}</div>
        </div>
        <div class="metric">
          <div class="metric-label">Agents</div>
          <div class="metric-value">{{ agents.length }}</div>
        </div>
      </div>

      <div class="metric-row">
        <div class="metric">
          <div class="metric-label">Session tokens</div>
          <div class="metric-value">{{ formatTokens(sessionTotalTokens) }}</div>
        </div>
        <div class="metric">
          <div class="metric-label">Duration</div>
          <div class="metric-value">{{ formatDuration(sessionTotalDuration) }}</div>
        </div>
      </div>

      <div v-if="ctxRatio !== null" class="metric-row">
        <div class="metric full">
          <div class="metric-label">Context usage</div>
          <div class="ctx-bar"><div class="ctx-fill" :style="{ width: ctxRatio + '%' }" /></div>
        </div>
      </div>
    </div>

    <div class="flyout-tabs">
      <button
        v-for="t in quickTabs"
        :key="t.id"
        class="flyout-tab"
        :title="t.label"
        @click="open(t.id)"
      >
        <span class="flyout-tab-icon">{{ t.icon }}</span>
        <span class="flyout-tab-label">{{ t.label }}</span>
      </button>
    </div>

    <button class="flyout-expand" @click="open()">
      ⤢ Expand Inspector
    </button>
  </div>
</template>

<style scoped>
.inspector-flyout {
  position: absolute;
  top: var(--space-md);
  right: var(--space-md);
  width: 240px;
  background: var(--bg-elevated, #181c24);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 10px;
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.45);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  z-index: 20;
  font-family: var(--font-mono, monospace);
  backdrop-filter: blur(6px);
}

.flyout-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border-subtle, rgba(255, 255, 255, 0.06));
  background: rgba(0, 0, 0, 0.2);
}

.flyout-title {
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  font-size: 0.78rem;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-primary, #e8ebf0);
}

.flyout-ws {
  font-size: 0.62rem;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: 8px;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.flyout-ws.connected {
  color: var(--accent-success, #39ff14);
  background: rgba(57, 255, 20, 0.1);
}
.flyout-ws.connecting {
  color: var(--accent-warning, #ffb800);
  background: rgba(255, 184, 0, 0.1);
}
.flyout-ws.disconnected {
  color: var(--text-muted, #5c6675);
  background: rgba(255, 255, 255, 0.04);
}

.flyout-body {
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.metric-row {
  display: flex;
  gap: 12px;
}
.metric {
  flex: 1;
  min-width: 0;
}
.metric.full {
  flex: 1 1 100%;
}
.metric-label {
  font-size: 0.6rem;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-muted, #5c6675);
  margin-bottom: 2px;
}
.metric-value {
  font-size: 0.92rem;
  font-weight: 600;
  color: var(--text-primary, #e8ebf0);
}
.metric-value.status-running { color: var(--accent-running, #00e5ff); }
.metric-value.status-completed { color: var(--accent-success, #39ff14); }
.metric-value.status-failed { color: var(--accent-danger, #ff4d4d); }
.metric-value.status-idle { color: var(--text-muted, #5c6675); }

.ctx-bar {
  height: 6px;
  border-radius: 3px;
  background: var(--bg-panel, #11141a);
  overflow: hidden;
  margin-top: 4px;
}
.ctx-fill {
  height: 100%;
  background: var(--accent-running, #00e5ff);
  transition: width 0.4s ease;
}

.flyout-tabs {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 4px;
  padding: 6px 8px;
  border-top: 1px solid var(--border-subtle, rgba(255, 255, 255, 0.06));
}

.flyout-tab {
  background: transparent;
  border: 1px solid transparent;
  border-radius: 6px;
  color: var(--text-secondary, #9aa3b2);
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  font-size: 0.66rem;
  padding: 4px 2px;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  transition: background 0.12s, color 0.12s, border-color 0.12s;
}
.flyout-tab:hover {
  background: var(--bg-hover, #202632);
  color: var(--text-primary, #e8ebf0);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}
.flyout-tab-icon {
  font-size: 0.9rem;
}
.flyout-tab-label {
  letter-spacing: 0.04em;
}

.flyout-expand {
  margin: 6px 8px 8px;
  padding: 6px 10px;
  background: var(--bg-panel, #11141a);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 6px;
  color: var(--accent-running, #00e5ff);
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  cursor: pointer;
  transition: background 0.12s, border-color 0.12s;
}
.flyout-expand:hover {
  background: var(--bg-hover, #202632);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}

@media (max-width: 1023px) {
  .inspector-flyout {
    width: 220px;
  }
}
@media (max-width: 767px) {
  .inspector-flyout {
    display: none;
  }
}
</style>
