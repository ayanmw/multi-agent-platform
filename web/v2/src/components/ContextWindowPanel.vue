<!--
  ContextWindowPanel.vue — real-time context window observability

  Displays the latest backend `context_window_snapshot` with a refined,
  dashboard-like aesthetic. The visual direction is "laboratory telemetry":
  dark glass panels, subtle glows, calibrated typography, and calm motion.

  Sections:
    1. Header — title, model identifier, refresh action.
    2. Capacity ring — total tokens versus max context as a radial progress.
    3. Role composition — animated stacked spectrum + per-role cards.
    4. Message timeline — collapsible message cards with metadata chips.

  Props:
    activeTaskId: the task currently selected in the main UI.
-->
<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useContextWindow } from '../composables/useContextWindow'
import type { ContextSnapshotMessage } from '../types/events'

const props = defineProps<{
  activeTaskId: string
  /** 7-G: optional sub-task ID to show the snapshot for a specific agent instance. */
  subTaskId?: string
}>()

// 快照有两个来源，组件必须同时支持两者，否则历史/idle 会话会永远停在
// "Waiting for the next agent think step..." 空态：
//   1. WebSocket `context_window_snapshot` 事件 —— 仅 Engine 运行时才有，
//      由 useContextWindow 的 onEvent 写入 currentSnapshot / subTaskSnapshots。
//   2. REST `GET /api/tasks/:id/context_window` —— 任何时候都可拉取，是
//      历史/idle task 重建上下文窗口的唯一来源。
// 组件用 fetchSnapshot 自包含地走来源 2，不再依赖"父组件必须绑 @refresh
// 并自己实现 fetcher"这种隐式契约（该契约在 UI-v2 重构时丢失过一次）。
const { currentSnapshot, setActiveTaskId, subTaskSnapshots, fetchSnapshot } = useContextWindow()
// 7-G: When a sub-task is selected, prefer its isolated snapshot from the store.
const latest = computed(() => {
  if (props.subTaskId && subTaskSnapshots.value[props.subTaskId]) {
    return subTaskSnapshots.value[props.subTaskId]
  }
  return currentSnapshot.value
})

const isLoading = ref(false)
let loadingTimer: ReturnType<typeof setTimeout> | null = null

function clearLoadingTimer() {
  if (loadingTimer) {
    clearTimeout(loadingTimer)
    loadingTimer = null
  }
}

// 拉取当前视图对应的快照：subTaskId 非空走子 agent 槽位，否则走 root。
// 包一层 loading 态用于按钮 spinner；组件卸载时由 onUnmounted 清理定时器。
async function requestRefresh() {
  if (!props.activeTaskId) return
  isLoading.value = true
  clearLoadingTimer()
  try {
    await fetchSnapshot(props.activeTaskId, props.subTaskId || undefined)
  } finally {
    // 保持 spinner 一小段时间，避免闪烁；组件卸载时由 onUnmounted 清理。
    loadingTimer = setTimeout(() => { isLoading.value = false }, 300)
  }
}

onUnmounted(() => {
  clearLoadingTimer()
})

// activeTaskId 变化（含初次挂载）时同步 composable 的 active task，并立即
// 拉取一次快照。immediate:true 覆盖初始挂载，因此无需额外 onMounted。
// 注意：必须无条件拉取，不能只在 `!currentSnapshot.value` 时拉——否则从
// 已有快照的任务切到历史任务时，currentSnapshot 已被 setActiveTaskId 清空，
// 但若曾经缓存过同名 key 仍可能误判，且历史任务永远没有 WS 事件补填。
watch(
  () => props.activeTaskId,
  (taskId) => {
    setActiveTaskId(taskId)
    if (taskId) requestRefresh()
  },
  { immediate: true },
)

// 切换子 agent 实例时拉取该实例的快照；空值表示回到 root 视图。
watch(
  () => props.subTaskId,
  () => {
    if (props.activeTaskId) requestRefresh()
  },
)

// 当前点击 messages 列表中某条 prompt 时弹出的对话框状态。
const promptDialog = ref<{
  open: boolean
  role: string
  ordinal: string
  content: string
  reasoning: string | undefined
}>({
  open: false,
  role: '',
  ordinal: '',
  content: '',
  reasoning: undefined,
})

function openPromptDialog(msg: ContextSnapshotMessage, idx: number) {
  promptDialog.value = {
    open: true,
    role: msg.role,
    ordinal: messageOrdinal(idx),
    content: msg.content || '(empty content)',
    reasoning: msg.reasoning,
  }
}
function closePromptDialog() {
  promptDialog.value.open = false
}

const usagePercent = computed(() => {
  if (!latest.value) return 0
  const ratio = latest.value.estimated_usage_ratio
  if (!Number.isFinite(ratio) || ratio < 0) return 0
  return Math.min(100, ratio * 100)
})

const usageColorClass = computed(() => {
  const p = usagePercent.value
  if (p < 50) return 'ok'
  if (p < 80) return 'warn'
  return 'critical'
})

const roleMeta: Record<string, { label: string; icon: string; hue: number }> = {
  system: { label: 'system', icon: '◈', hue: 260 },
  user: { label: 'user', icon: '◇', hue: 205 },
  assistant: { label: 'assistant', icon: '✦', hue: 145 },
  tool: { label: 'tool', icon: '⚙', hue: 32 },
}

function roleColor(role: string, alpha = 1): string {
  const hue = roleMeta[role]?.hue ?? 210
  return `hsla(${hue}, 85%, 62%, ${alpha})`
}

function roleGlow(role: string): string {
  const hue = roleMeta[role]?.hue ?? 210
  return `0 0 16px hsla(${hue}, 85%, 60%, 0.35)`
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10000 ? 1 : 2)}k`
  return `${n}`
}

function formatPercent(ratio: number): string {
  if (ratio < 0.01 && ratio > 0) return '<1%'
  return `${(ratio * 100).toFixed(ratio < 0.1 ? 1 : 0)}%`
}

interface RoleSummary {
  role: string
  count: number
  tokens: number
  ratio: number
}

const roleSummary = computed<RoleSummary[]>(() => {
  if (!latest.value) return []
  const byRole: Record<string, { count: number; tokens: number }> = {}
  for (const m of latest.value.messages) {
    const r = m.role
    if (!byRole[r]) byRole[r] = { count: 0, tokens: 0 }
    byRole[r].count++
    byRole[r].tokens += m.estimated_tokens
  }
  const total = latest.value.estimated_total_tokens || 1
  const rows: RoleSummary[] = []
  for (const [role, meta] of Object.entries(byRole)) {
    rows.push({ role, count: meta.count, tokens: meta.tokens, ratio: meta.tokens / total })
  }
  const order = ['system', 'user', 'assistant', 'tool']
  rows.sort((a, b) => {
    const ia = order.indexOf(a.role)
    const ib = order.indexOf(b.role)
    if (ia >= 0 && ib >= 0) return ia - ib
    if (ia >= 0) return -1
    if (ib >= 0) return 1
    return a.role.localeCompare(b.role)
  })
  return rows
})

const spectrumStyle = computed(() => {
  const stops: string[] = []
  let cursor = 0
  for (const row of roleSummary.value) {
    const start = cursor
    const end = cursor + row.ratio * 100
    stops.push(`${roleColor(row.role, 0.85)} ${start.toFixed(2)}% ${end.toFixed(2)}%`)
    cursor = end
  }
  if (stops.length === 0) return { background: 'var(--border-subtle)' }
  return { background: `linear-gradient(90deg, ${stops.join(', ')})` }
})

function messageRatio(msg: ContextSnapshotMessage): number {
  if (!latest.value) return 0
  const total = latest.value.estimated_total_tokens || 1
  return msg.estimated_tokens / total
}

function messageOrdinal(idx: number): string {
  return String(idx + 1).padStart(2, '0')
}

function truncate(text: string | undefined, max: number): string {
  if (!text) return ''
  return text.length <= max ? text : text.slice(0, max - 1) + '…'
}

const circumference = 2 * Math.PI * 52
const ringDash = computed(() => {
  const fraction = Math.min(1, usagePercent.value / 100)
  return `${circumference * fraction} ${circumference * (1 - fraction)}`
})
</script>

<template>
  <div class="context-panel">
    <!-- Header -->
    <header class="context-header">
      <div class="context-title-group">
        <div class="context-title">Context Window</div>
        <div v-if="latest" class="model-chip" :title="latest.model">
          {{ latest.model || 'unknown model' }}
        </div>
      </div>
      <button
        class="refresh-btn"
        title="Refresh context window snapshot"
        :disabled="!activeTaskId || isLoading"
        @click="requestRefresh"
      >
        <span v-if="isLoading" class="refresh-spinner" />
        <svg v-else class="refresh-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M21.5 2v6h-6M2.5 22v-6h6M19.5 11a8 8 0 0 1-15.2 2.5M4.5 13a8 8 0 0 1 15.2-2.5" />
        </svg>
        <span>Refresh</span>
      </button>
    </header>

    <div v-if="!latest" class="empty-state">
      <span class="empty-icon">◌</span>
      <p class="empty-title">
        {{ isLoading ? 'Loading snapshot...' : 'Waiting for the next agent think step...' }}
      </p>
      <p class="empty-hint">Snapshots are emitted before every LLM call for the active task.</p>
    </div>

    <template v-else>
      <!-- Top telemetry grid -->
      <section class="telemetry-grid">
        <!-- Capacity ring -->
        <div class="glass-card capacity-card">
          <div class="card-label">Capacity</div>
          <div class="ring-wrap">
            <svg class="capacity-ring" viewBox="0 0 120 120">
              <circle class="ring-track" cx="60" cy="60" r="52" />
              <circle
                class="ring-fill"
                :class="usageColorClass"
                cx="60"
                cy="60"
                r="52"
                :stroke-dasharray="ringDash"
                :stroke-dashoffset="0"
              />
            </svg>
            <div class="ring-center">
              <div class="ring-percent" :class="usageColorClass">{{ usagePercent.toFixed(1) }}%</div>
              <div class="ring-fraction">
                {{ formatTokens(latest.estimated_total_tokens) }} / {{ formatTokens(latest.max_context_tokens) }}
              </div>
              <div class="ring-unit">tokens</div>
            </div>
          </div>
          <p class="disclaimer">Local heuristic; not API-exact token counts.</p>
        </div>

        <!-- Role composition -->
        <div class="glass-card composition-card">
          <div class="card-label">Composition</div>
          <div class="spectrum-track">
            <div class="spectrum-fill" :style="spectrumStyle" />
          </div>
          <div class="role-grid">
            <div
              v-for="row in roleSummary"
              :key="row.role"
              class="role-item"
            >
              <div class="role-dot" :style="{ background: roleColor(row.role), boxShadow: roleGlow(row.role) }" />
              <div class="role-info">
                <div class="role-name">
                  <span class="role-icon">{{ roleMeta[row.role]?.icon || '•' }}</span>
                  {{ roleMeta[row.role]?.label || row.role }}
                </div>
                <div class="role-stats">
                  <strong>{{ formatPercent(row.ratio) }}</strong>
                  <span>{{ formatTokens(row.tokens) }} tok</span>
                  <span class="role-count">{{ row.count }} msg</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>

      <!-- Message timeline -->
      <section class="timeline-section">
        <div class="section-header">
          <h3 class="section-title">Messages</h3>
          <span class="section-meta">{{ latest.messages.length }} total</span>
        </div>
        <div class="timeline">
          <div
            v-for="(msg, idx) in latest.messages"
            :key="idx"
            class="message-item"
          >
            <div class="message-row" @click="openPromptDialog(msg, idx)">
              <span class="message-ordinal">{{ messageOrdinal(idx) }}</span>
              <span
                class="message-dot"
                :style="{ background: roleColor(msg.role), boxShadow: roleGlow(msg.role) }"
              />
              <span class="message-role">{{ roleMeta[msg.role]?.label || msg.role }}</span>
              <span class="message-preview" :title="msg.content">{{ truncate(msg.content, 58) }}</span>
              <span class="message-tokens">{{ formatTokens(msg.estimated_tokens) }} tok</span>
              <span class="message-ratio">{{ (messageRatio(msg) * 100).toFixed(0) }}%</span>
              <span class="message-icon">🔍</span>
            </div>
          </div>
        </div>
      </section>
    </template>

    <!-- Prompt 详情弹窗：点击 message preview 后打开，自动伸缩显示完整 prompt -->
    <Teleport to="body">
      <Transition name="prompt-dialog">
        <div
          v-if="promptDialog.open"
          class="prompt-dialog-overlay"
          @click.self="closePromptDialog"
        >
          <div class="prompt-dialog-panel">
            <div class="prompt-dialog-header">
              <div class="prompt-dialog-title">
                <span
                  class="prompt-dialog-dot"
                  :style="{ background: roleColor(promptDialog.role), boxShadow: roleGlow(promptDialog.role) }"
                />
                <span>{{ roleMeta[promptDialog.role]?.label || promptDialog.role }} Prompt</span>
                <span class="prompt-dialog-ordinal">#{{ promptDialog.ordinal }}</span>
              </div>
              <button class="prompt-dialog-close" title="关闭" @click="closePromptDialog">×</button>
            </div>
            <div class="prompt-dialog-body">
              <div v-if="promptDialog.reasoning" class="prompt-block reasoning-block">
                <div class="block-label">Reasoning</div>
                <pre class="block-content reasoning-text">{{ promptDialog.reasoning }}</pre>
              </div>
              <div class="prompt-block content-block">
                <div class="block-label">Content</div>
                <pre class="block-content content-text">{{ promptDialog.content }}</pre>
              </div>
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<style scoped>
/* ===================================================
   Context Window Panel — Laboratory telemetry theme
   =================================================== */
.context-panel {

  height:100%;
  display:flex;
  flex-direction:column;
  gap:1.125rem;
  padding:1.375rem 1.625rem 1.625rem;
  overflow:hidden;
  background:radial-gradient(circle at 20% 0%, rgba(74, 158, 255, 0.06), transparent 35%),
    radial-gradient(circle at 80% 100%, rgba(64, 211, 134, 0.04), transparent 30%),
    var(--bg-canvas);
  color:var(--text-primary);
  font-family:var(--font-display, system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif);
}

/* Header */
.context-header {
  display:flex;
  align-items:center;
  justify-content:space-between;
  gap:1rem;
  flex-shrink:0;
}

.context-title-group {
  display:flex;
  align-items:baseline;
  gap:0.875rem;
  min-width:0;
}

.context-title {
  font-size:1.25rem;
  font-weight:650;
  letter-spacing:-0.02em;
  color:var(--text-primary);
}

.model-chip {
  max-width:13.750rem;
  overflow:hidden;
  text-overflow:ellipsis;
  white-space:nowrap;
  font-size:0.688rem;
  font-weight:500;
  color:var(--text-secondary);
  padding:0.188rem 0.625rem;
  border-radius: var(--radius-lg);
  background:var(--border-subtle);
  border:1px solid var(--border-default);
  font-family:var(--font-mono, 'SFMono-Regular', Consolas, monospace);
}

.refresh-btn {
  display:inline-flex;
  align-items:center;
  gap:0.438rem;
  padding:0.438rem 0.75rem;
  border-radius: var(--radius-md);
  border:1px solid var(--border-default);
  background:var(--border-subtle);
  color:var(--text-secondary);
  font-size:0.75rem;
  font-weight:500;
  cursor:pointer;
  transition:all 0.18s ease;
  flex-shrink:0;
}

.refresh-btn:hover:not(:disabled) {
  background:rgba(255, 255, 255, 0.09);
  color:var(--text-primary);
  border-color:rgba(255, 255, 255, 0.16);
}

.refresh-btn:disabled {
  opacity:0.45;
  cursor:not-allowed;
}

.refresh-icon {
  width:0.875rem;
  height:0.875rem;
}

.refresh-spinner {
  width:0.875rem;
  height:0.875rem;
  border:2px solid rgba(255, 255, 255, 0.18);
  border-top-color:var(--text-primary);
  border-radius:50%;
  animation:spin 0.9s linear infinite;
}

@keyframes spin {
  to { transform:rotate(360deg); }
}

/* Empty state */
.empty-state {
  flex:1;
  display:flex;
  flex-direction:column;
  align-items:center;
  justify-content:center;
  text-align:center;
  color:var(--text-secondary);
  gap:0.625rem;
  min-height:15rem;
}

.empty-icon {
  font-size:2.750rem;
  line-height:1;
  color:var(--border-default);
  animation:pulse 2.4s ease-in-out infinite;
}

@keyframes pulse {
  0%, 100% { opacity:0.35; }
  50% { opacity:0.7; }
}

.empty-title {
  font-size:0.938rem;
  color:var(--text-primary);
  font-weight:500;
}

.empty-hint {
  font-size:0.75rem;
  color:var(--text-muted);
  max-width:20rem;
}

/* Glass card utility */
.glass-card {
  background:rgba(255, 255, 255, 0.025);
  border:1px solid var(--border-default);
  border-radius: var(--radius-lg);
  padding:1.125rem;
  backdrop-filter:blur(0.625rem);
  box-shadow:0 1px 0 transparent inset,
    0 0.625rem 1.875rem rgba(0, 0, 0, 0.25);
}

.card-label {
  font-size:0.688rem;
  font-weight:600;
  text-transform:uppercase;
  letter-spacing:0.08em;
  color:var(--text-muted);
  margin-bottom:0.875rem;
}

/* Telemetry grid */
.telemetry-grid {
  display:grid;
  grid-template-columns:17.500rem 1fr;
  gap:1.125rem;
  flex-shrink:0;
}

@media (max-width: 56.250rem) {
  .telemetry-grid {
    grid-template-columns:1fr;
  }
}

/* Capacity ring */
.capacity-card {
  display:flex;
  flex-direction:column;
  align-items:center;
  text-align:center;
}

.card-label {
  align-self:flex-start;
}

.ring-wrap {
  position:relative;
  width:10.625rem;
  height:10.625rem;
  margin:0.125rem auto 0.75rem;
}

.capacity-ring {
  width:100%;
  height:100%;
  transform:rotate(-90deg);
}

.ring-track {
  fill:none;
  stroke:var(--border-subtle);
  stroke-width:10;
  stroke-linecap:round;
}

.ring-fill {
  fill:none;
  stroke-width:10;
  stroke-linecap:round;
  transition:stroke-dasharray 0.6s ease;
}

.ring-fill.ok { stroke:var(--accent-success); filter:drop-shadow(0 0 0.375rem rgba(64, 211, 134, 0.35)); }
.ring-fill.warn { stroke:var(--accent-warning); filter:drop-shadow(0 0 0.375rem rgba(242, 184, 75, 0.35)); }
.ring-fill.critical { stroke:var(--accent-danger); filter:drop-shadow(0 0 0.375rem rgba(255, 92, 92, 0.35)); }

.ring-center {
  position:absolute;
  inset:0;
  display:flex;
  flex-direction:column;
  align-items:center;
  justify-content:center;
  pointer-events:none;
}

.ring-percent {
  font-size:1.875rem;
  font-weight:700;
  letter-spacing:-0.03em;
  line-height:1;
}

.ring-percent.ok { color:var(--accent-success); }
.ring-percent.warn { color:var(--accent-warning); }
.ring-percent.critical { color:var(--accent-danger); }

.ring-fraction {
  margin-top:0.375rem;
  font-size:0.75rem;
  font-weight:500;
  color:var(--text-primary);
  font-family:var(--font-mono, monospace);
}

.ring-unit {
  font-size:0.625rem;
  color:var(--text-muted);
  text-transform:uppercase;
  letter-spacing:0.08em;
  margin-top:0.125rem;
}

.disclaimer {
  font-size:0.625rem;
  color:var(--text-muted);
  margin-top:auto;
  padding-top:0.5rem;
}

/* Composition card */
.composition-card {
  display:flex;
  flex-direction:column;
}

.spectrum-track {
  height:0.625rem;
  border-radius: var(--radius-lg);
  background:var(--border-subtle);
  overflow:hidden;
  margin-bottom:1.125rem;
  box-shadow:inset 0 1px 0.125rem rgba(0, 0, 0, 0.25);
}

.spectrum-fill {
  height:100%;
  width:100%;
  border-radius: var(--radius-lg);
  transition:background 0.5s ease;
}

.role-grid {
  display:grid;
  grid-template-columns:repeat(2, minmax(0, 1fr));
  gap:0.75rem;
}

@media (max-width: 37.500rem) {
  .role-grid {
    grid-template-columns:1fr;
  }
}

.role-item {
  display:flex;
  align-items:flex-start;
  gap:0.75rem;
  padding:0.75rem 0.875rem;
  border-radius: var(--radius-lg);
  background:transparent;
  border:1px solid var(--border-subtle);
  transition:background 0.15s ease, transform 0.15s ease;
}

.role-item:hover {
  background:var(--border-subtle);
}

.role-dot {
  width:0.625rem;
  height:0.625rem;
  border-radius:50%;
  margin-top:0.312rem;
  flex-shrink:0;
}

.role-info {
  min-width:0;
  flex:1;
}

.role-name {
  font-size:0.812rem;
  font-weight:600;
  color:var(--text-primary);
  display:flex;
  align-items:center;
  gap:0.438rem;
}

.role-icon {
  color:var(--text-secondary);
  font-size:0.688rem;
}

.role-stats {
  display:flex;
  align-items:baseline;
  gap:0.625rem;
  margin-top:0.312rem;
  font-size:0.688rem;
  color:var(--text-secondary);
  flex-wrap:wrap;
}

.role-stats strong {
  font-size:1.125rem;
  font-weight:700;
  color:var(--text-primary);
}

.role-count {
  color:var(--text-muted);
}

/* Timeline */
.timeline-section {
  flex:1;
  min-height:0;
  display:flex;
  flex-direction:column;
}

.section-header {
  display:flex;
  align-items:baseline;
  justify-content:space-between;
  gap:0.75rem;
  margin-bottom:0.75rem;
  flex-shrink:0;
}

.section-title {
  font-size:0.75rem;
  font-weight:600;
  text-transform:uppercase;
  letter-spacing:0.08em;
  color:var(--text-muted);
}

.section-meta {
  font-size:0.688rem;
  color:var(--text-secondary);
}

.timeline {
  flex:1;
  overflow-y:auto;
  padding-right:0.375rem;
}

.message-item {
  border-radius: var(--radius-lg);
  background:var(--bg-elevated);
  border:1px solid var(--border-subtle);
  margin-bottom:0.625rem;
  overflow:hidden;
  transition:border-color 0.15s ease, background 0.15s ease;
}

.message-item:hover {
  border-color:var(--border-default);
  background:transparent;
}

.message-row {
  cursor:pointer;
  padding:0.75rem 0.875rem;
  display:grid;
  grid-template-columns:1.875rem 0.75rem auto 5rem 2.875rem 1.125rem;
  align-items:center;
  gap:0.75rem;
  user-select:none;
}

.message-ordinal {
  font-family:var(--font-mono, monospace);
  font-size:0.625rem;
  color:var(--text-muted);
  text-align:right;
}

.message-dot {
  width:0.625rem;
  height:0.625rem;
  border-radius:50%;
  flex-shrink:0;
}

.message-role {
  font-size:0.688rem;
  font-weight:600;
  text-transform:uppercase;
  letter-spacing:0.04em;
  color:var(--text-secondary);
  width:4.500rem;
  flex-shrink:0;
}

.message-preview {
  font-size:0.75rem;
  color:var(--text-primary);
  opacity:0.8;
  overflow:hidden;
  text-overflow:ellipsis;
  white-space:nowrap;
  min-width:0;
}

.message-tokens,
.message-ratio {
  font-family:var(--font-mono, monospace);
  font-size:0.625rem;
  color:var(--text-secondary);
  text-align:right;
}

.message-icon {
  width:1.125rem;
  height:1.125rem;
  display:grid;
  place-items:center;
  color:var(--text-muted);
  font-size:0.75rem;
  transition:color 0.15s;
}

.message-row:hover .message-icon {
  color:var(--accent-running);
}

/* Prompt 详情弹窗 */
.prompt-dialog-overlay {
  position:fixed;
  inset:0;
  background:rgba(0, 0, 0, 0.72);
  backdrop-filter:blur(3px);
  z-index:210;
  display:flex;
  align-items:center;
  justify-content:center;
  padding:24px;
}

.prompt-dialog-panel {
  width:auto;
  min-width:320px;
  max-width:min(900px, 90vw);
  max-height:min(800px, 85vh);
  background:var(--bg-canvas, #0b0d10);
  border:1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius:14px;
  display:flex;
  flex-direction:column;
  overflow:hidden;
  box-shadow:0 30px 90px rgba(0, 0, 0, 0.7);
}

.prompt-dialog-header {
  flex-shrink:0;
  display:flex;
  align-items:center;
  justify-content:space-between;
  gap:12px;
  padding:12px 18px;
  border-bottom:1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  background:var(--bg-elevated, #181c24);
}

.prompt-dialog-title {
  display:flex;
  align-items:center;
  gap:10px;
  font-family:var(--font-display, 'Chakra Petch', sans-serif);
  font-size:0.85rem;
  font-weight:600;
  letter-spacing:0.04em;
  text-transform:uppercase;
  color:var(--text-primary, #e8ebf0);
}

.prompt-dialog-dot {
  width:0.625rem;
  height:0.625rem;
  border-radius:50%;
  flex-shrink:0;
}

.prompt-dialog-ordinal {
  font-family:var(--font-mono, monospace);
  font-size:0.65rem;
  color:var(--text-muted);
  background:var(--border-subtle);
  padding:2px 7px;
  border-radius:10px;
  text-transform:none;
  letter-spacing:0;
}

.prompt-dialog-close {
  width:28px;
  height:28px;
  display:inline-flex;
  align-items:center;
  justify-content:center;
  background:transparent;
  border:1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius:6px;
  color:var(--text-secondary, #9aa3b2);
  font-size:18px;
  line-height:1;
  cursor:pointer;
  transition:background 0.15s, color 0.15s, border-color 0.15s;
}

.prompt-dialog-close:hover {
  background:var(--bg-hover, #202632);
  color:var(--text-primary, #e8ebf0);
  border-color:var(--border-active, rgba(0, 229, 255, 0.4));
}

.prompt-dialog-body {
  flex:1;
  min-height:0;
  overflow-y:auto;
  padding:18px;
  display:flex;
  flex-direction:column;
  gap:14px;
}

.prompt-block {
  border-radius: var(--radius-lg);
  overflow:hidden;
}

.prompt-dialog-enter-active,
.prompt-dialog-leave-active {
  transition:opacity 0.2s ease;
}

.prompt-dialog-enter-from,
.prompt-dialog-leave-to {
  opacity:0;
}

.reasoning-block {
  background:rgba(155, 89, 255, 0.055);
  border:1px solid rgba(155, 89, 255, 0.14);
}

.content-block {
  background:var(--border-subtle);
  border:1px solid var(--border-default);
}

.block-label {
  font-size:0.562rem;
  font-weight:700;
  text-transform:uppercase;
  letter-spacing:0.1em;
  color:var(--text-muted);
  padding:0.438rem 0.625rem;
  background:rgba(0, 0, 0, 0.15);
  border-bottom:1px solid var(--border-subtle);
}

.block-content {
  padding:0.625rem 0.75rem;
  margin:0;
  font-family:var(--font-mono, monospace);
  font-size:0.75rem;
  line-height:1.55;
  color:var(--text-primary);
  white-space:pre-wrap;
  word-break:break-word;
  overflow-y:auto;
}

.reasoning-text {
  color:var(--accent-tool);
}

/* Scrollbar for the panel */
.timeline::-webkit-scrollbar,
.block-content::-webkit-scrollbar,
.prompt-dialog-body::-webkit-scrollbar {
  width:0.375rem;
}

.timeline::-webkit-scrollbar-track,
.block-content::-webkit-scrollbar-track,
.prompt-dialog-body::-webkit-scrollbar-track {
  background:transparent;
}

.timeline::-webkit-scrollbar-thumb,
.block-content::-webkit-scrollbar-thumb,
.prompt-dialog-body::-webkit-scrollbar-thumb {
  background:var(--border-default);
  border-radius: var(--radius-sm);
}

.timeline::-webkit-scrollbar-thumb:hover,
.block-content::-webkit-scrollbar-thumb:hover,
.prompt-dialog-body::-webkit-scrollbar-thumb:hover {
  background:rgba(255, 255, 255, 0.16);
}

@media (max-width: 767px) {
  .prompt-dialog-overlay {
    padding:12px;
  }

  .prompt-dialog-panel {
    width:100vw;
    max-width:none;
    height:100vh;
    max-height:none;
    border-radius:0;
  }
}
</style>
