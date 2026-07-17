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
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useContextWindow } from '../composables/useContextWindow'
import type { ContextSnapshotMessage } from '../types/events'

const props = defineProps<{
  activeTaskId: string
}>()

const emit = defineEmits<{
  refresh: []
}>()

const { currentSnapshot, setActiveTaskId } = useContextWindow()
const latest = computed(() => currentSnapshot.value)

const isLoading = ref(false)
let loadingTimer: ReturnType<typeof setTimeout> | null = null

function clearLoadingTimer() {
  if (loadingTimer) {
    clearTimeout(loadingTimer)
    loadingTimer = null
  }
}

async function requestRefresh() {
  if (!props.activeTaskId) return
  isLoading.value = true
  clearLoadingTimer()
  try {
    emit('refresh')
  } finally {
    // 保持 spinner 一小段时间，避免闪烁；组件卸载时由 onUnmounted 清理。
    loadingTimer = setTimeout(() => { isLoading.value = false }, 300)
  }
}

onUnmounted(() => {
  clearLoadingTimer()
})

// Keep the composable's active task in sync with the prop.
// Immediate: true also covers the initial mount, so we do not need an extra
// onMounted refresh. Both running would double-emit 'refresh' and fetch the
// snapshot twice for the same task.
watch(
  () => props.activeTaskId,
  (taskId) => {
    setActiveTaskId(taskId)
    if (taskId && !currentSnapshot.value) {
      requestRefresh()
    }
  },
  { immediate: true },
)

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
  if (stops.length === 0) return { background: 'rgba(255,255,255,0.06)' }
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
          <details
            v-for="(msg, idx) in latest.messages"
            :key="idx"
            class="message-item"
          >
            <summary class="message-summary">
              <span class="message-ordinal">{{ messageOrdinal(idx) }}</span>
              <span
                class="message-dot"
                :style="{ background: roleColor(msg.role), boxShadow: roleGlow(msg.role) }"
              />
              <span class="message-role">{{ roleMeta[msg.role]?.label || msg.role }}</span>
              <span class="message-preview">{{ truncate(msg.content, 58) }}</span>
              <span class="message-tokens">{{ formatTokens(msg.estimated_tokens) }} tok</span>
              <span class="message-ratio">{{ (messageRatio(msg) * 100).toFixed(0) }}%</span>
              <span class="message-chevron" />
            </summary>
            <div class="message-body">
              <div v-if="msg.reasoning" class="reasoning-block">
                <div class="block-label">Reasoning</div>
                <pre class="block-content reasoning-text">{{ msg.reasoning }}</pre>
              </div>
              <div v-if="msg.tool_call_id || (msg.tool_calls && msg.tool_calls.length)" class="tool-meta">
                <span v-if="msg.tool_call_id" class="tool-chip">
                  tool_call_id: {{ msg.tool_call_id }}
                </span>
                <span v-if="msg.tool_calls && msg.tool_calls.length" class="tool-chip">
                  tool_calls: {{ msg.tool_calls.length }}
                </span>
              </div>
              <div class="content-block">
                <div class="block-label">Content</div>
                <pre class="block-content content-text">{{ msg.content || '(empty content)' }}</pre>
              </div>
            </div>
          </details>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
/* ===================================================
   Context Window Panel — Laboratory telemetry theme
   =================================================== */
.context-panel {
  --panel-bg: #15151a;
  --glass-bg: rgba(255, 255, 255, 0.025);
  --glass-border: rgba(255, 255, 255, 0.08);
  --text-main: #e8e8ec;
  --text-dim: #909099;
  --text-faint: #5a5a62;
  --accent-ok: #40d386;
  --accent-warn: #f2b84b;
  --accent-critical: #ff5c5c;

  height: 100%;
  display: flex;
  flex-direction: column;
  gap: 18px;
  padding: 22px 26px 26px;
  overflow: hidden;
  background:
    radial-gradient(circle at 20% 0%, rgba(74, 158, 255, 0.06), transparent 35%),
    radial-gradient(circle at 80% 100%, rgba(64, 211, 134, 0.04), transparent 30%),
    var(--panel-bg);
  color: var(--text-main);
  font-family: var(--font-sans, system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif);
}

/* Header */
.context-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-shrink: 0;
}

.context-title-group {
  display: flex;
  align-items: baseline;
  gap: 14px;
  min-width: 0;
}

.context-title {
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.02em;
  color: var(--text-main);
}

.model-chip {
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11px;
  font-weight: 500;
  color: var(--text-dim);
  padding: 3px 10px;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.08);
  font-family: var(--font-mono, 'SFMono-Regular', Consolas, monospace);
}

.refresh-btn {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 7px 12px;
  border-radius: 8px;
  border: 1px solid rgba(255, 255, 255, 0.1);
  background: rgba(255, 255, 255, 0.05);
  color: var(--text-dim);
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.18s ease;
  flex-shrink: 0;
}

.refresh-btn:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.09);
  color: var(--text-main);
  border-color: rgba(255, 255, 255, 0.16);
}

.refresh-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.refresh-icon {
  width: 14px;
  height: 14px;
}

.refresh-spinner {
  width: 14px;
  height: 14px;
  border: 2px solid rgba(255, 255, 255, 0.18);
  border-top-color: var(--text-main);
  border-radius: 50%;
  animation: spin 0.9s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

/* Empty state */
.empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  color: var(--text-dim);
  gap: 10px;
  min-height: 240px;
}

.empty-icon {
  font-size: 44px;
  line-height: 1;
  color: rgba(255, 255, 255, 0.08);
  animation: pulse 2.4s ease-in-out infinite;
}

@keyframes pulse {
  0%, 100% { opacity: 0.35; }
  50% { opacity: 0.7; }
}

.empty-title {
  font-size: 15px;
  color: var(--text-main);
  font-weight: 500;
}

.empty-hint {
  font-size: 12px;
  color: var(--text-faint);
  max-width: 320px;
}

/* Glass card utility */
.glass-card {
  background: var(--glass-bg);
  border: 1px solid var(--glass-border);
  border-radius: 16px;
  padding: 18px;
  backdrop-filter: blur(10px);
  box-shadow:
    0 1px 0 rgba(255, 255, 255, 0.03) inset,
    0 10px 30px rgba(0, 0, 0, 0.25);
}

.card-label {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-faint);
  margin-bottom: 14px;
}

/* Telemetry grid */
.telemetry-grid {
  display: grid;
  grid-template-columns: 280px 1fr;
  gap: 18px;
  flex-shrink: 0;
}

@media (max-width: 900px) {
  .telemetry-grid {
    grid-template-columns: 1fr;
  }
}

/* Capacity ring */
.capacity-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
}

.card-label {
  align-self: flex-start;
}

.ring-wrap {
  position: relative;
  width: 170px;
  height: 170px;
  margin: 2px auto 12px;
}

.capacity-ring {
  width: 100%;
  height: 100%;
  transform: rotate(-90deg);
}

.ring-track {
  fill: none;
  stroke: rgba(255, 255, 255, 0.06);
  stroke-width: 10;
  stroke-linecap: round;
}

.ring-fill {
  fill: none;
  stroke-width: 10;
  stroke-linecap: round;
  transition: stroke-dasharray 0.6s ease;
}

.ring-fill.ok { stroke: var(--accent-ok); filter: drop-shadow(0 0 6px rgba(64, 211, 134, 0.35)); }
.ring-fill.warn { stroke: var(--accent-warn); filter: drop-shadow(0 0 6px rgba(242, 184, 75, 0.35)); }
.ring-fill.critical { stroke: var(--accent-critical); filter: drop-shadow(0 0 6px rgba(255, 92, 92, 0.35)); }

.ring-center {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  pointer-events: none;
}

.ring-percent {
  font-size: 30px;
  font-weight: 700;
  letter-spacing: -0.03em;
  line-height: 1;
}

.ring-percent.ok { color: var(--accent-ok); }
.ring-percent.warn { color: var(--accent-warn); }
.ring-percent.critical { color: var(--accent-critical); }

.ring-fraction {
  margin-top: 6px;
  font-size: 12px;
  font-weight: 500;
  color: var(--text-main);
  font-family: var(--font-mono, monospace);
}

.ring-unit {
  font-size: 10px;
  color: var(--text-faint);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  margin-top: 2px;
}

.disclaimer {
  font-size: 10px;
  color: var(--text-faint);
  margin-top: auto;
  padding-top: 8px;
}

/* Composition card */
.composition-card {
  display: flex;
  flex-direction: column;
}

.spectrum-track {
  height: 10px;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.06);
  overflow: hidden;
  margin-bottom: 18px;
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.25);
}

.spectrum-fill {
  height: 100%;
  width: 100%;
  border-radius: 999px;
  transition: background 0.5s ease;
}

.role-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

@media (max-width: 600px) {
  .role-grid {
    grid-template-columns: 1fr;
  }
}

.role-item {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 12px 14px;
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(255, 255, 255, 0.05);
  transition: background 0.15s ease, transform 0.15s ease;
}

.role-item:hover {
  background: rgba(255, 255, 255, 0.06);
}

.role-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  margin-top: 5px;
  flex-shrink: 0;
}

.role-info {
  min-width: 0;
  flex: 1;
}

.role-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-main);
  display: flex;
  align-items: center;
  gap: 7px;
}

.role-icon {
  color: var(--text-dim);
  font-size: 11px;
}

.role-stats {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-top: 5px;
  font-size: 11px;
  color: var(--text-dim);
  flex-wrap: wrap;
}

.role-stats strong {
  font-size: 18px;
  font-weight: 700;
  color: var(--text-main);
}

.role-count {
  color: var(--text-faint);
}

/* Timeline */
.timeline-section {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.section-header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
  flex-shrink: 0;
}

.section-title {
  font-size: 12px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-faint);
}

.section-meta {
  font-size: 11px;
  color: var(--text-dim);
}

.timeline {
  flex: 1;
  overflow-y: auto;
  padding-right: 6px;
}

.message-item {
  border-radius: 14px;
  background: rgba(255, 255, 255, 0.02);
  border: 1px solid rgba(255, 255, 255, 0.06);
  margin-bottom: 10px;
  overflow: hidden;
  transition: border-color 0.15s ease, background 0.15s ease;
}

.message-item:hover {
  border-color: rgba(255, 255, 255, 0.1);
  background: rgba(255, 255, 255, 0.03);
}

.message-summary {
  cursor: pointer;
  padding: 12px 14px;
  display: grid;
  grid-template-columns: 30px 12px auto 80px 46px 18px;
  align-items: center;
  gap: 12px;
  list-style: none;
  user-select: none;
}

.message-summary::-webkit-details-marker {
  display: none;
}

.message-ordinal {
  font-family: var(--font-mono, monospace);
  font-size: 10px;
  color: var(--text-faint);
  text-align: right;
}

.message-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex-shrink: 0;
}

.message-role {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-dim);
  width: 72px;
  flex-shrink: 0;
}

.message-preview {
  font-size: 12px;
  color: var(--text-main);
  opacity: 0.8;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.message-tokens,
.message-ratio {
  font-family: var(--font-mono, monospace);
  font-size: 10px;
  color: var(--text-dim);
  text-align: right;
}

.message-chevron {
  width: 18px;
  height: 18px;
  display: grid;
  place-items: center;
  color: var(--text-faint);
  transition: transform 0.2s ease;
}

.message-chevron::before {
  content: '▸';
  font-size: 12px;
}

.message-item[open] .message-chevron {
  transform: rotate(90deg);
}

.message-body {
  padding: 0 14px 14px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  animation: expand 0.25s ease;
  transform-origin: top;
}

@keyframes expand {
  from { opacity: 0; transform: translateY(-6px); }
  to { opacity: 1; transform: translateY(0); }
}

.reasoning-block,
.content-block {
  border-radius: 10px;
  overflow: hidden;
}

.reasoning-block {
  background: rgba(155, 89, 255, 0.055);
  border: 1px solid rgba(155, 89, 255, 0.14);
}

.content-block {
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.07);
}

.block-label {
  font-size: 9px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.1em;
  color: var(--text-faint);
  padding: 7px 10px;
  background: rgba(0, 0, 0, 0.15);
  border-bottom: 1px solid rgba(255, 255, 255, 0.04);
}

.block-content {
  padding: 10px 12px;
  margin: 0;
  font-family: var(--font-mono, monospace);
  font-size: 11.5px;
  line-height: 1.55;
  color: var(--text-main);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 400px;
  overflow-y: auto;
}

.reasoning-text {
  color: #d4b8ff;
}

.tool-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.tool-chip {
  font-family: var(--font-mono, monospace);
  font-size: 10px;
  color: var(--text-dim);
  padding: 5px 8px;
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.07);
}

/* Scrollbar for the panel */
.timeline::-webkit-scrollbar,
.block-content::-webkit-scrollbar {
  width: 6px;
}

.timeline::-webkit-scrollbar-track,
.block-content::-webkit-scrollbar-track {
  background: transparent;
}

.timeline::-webkit-scrollbar-thumb,
.block-content::-webkit-scrollbar-thumb {
  background: rgba(255, 255, 255, 0.1);
  border-radius: 3px;
}

.timeline::-webkit-scrollbar-thumb:hover,
.block-content::-webkit-scrollbar-thumb:hover {
  background: rgba(255, 255, 255, 0.16);
}
</style>
