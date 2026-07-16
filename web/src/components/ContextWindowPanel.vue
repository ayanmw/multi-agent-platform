<!--
  ContextWindowPanel.vue — real-time context window observability

  Displays the latest backend `context_window_snapshot` as:
    1. A compact header with model name, token totals, and refresh action.
    2. A progress bar showing estimated total tokens / max context tokens.
    3. A role-grouped bar chart with per-role token share.
    4. Expandable message cards so the user can inspect exactly what the
       agent sees (system prompt, user input, assistant reasoning, tool results).

  This component requests an on-demand snapshot from the API when it mounts/
  opens if no snapshot is available yet (auto-refresh). A manual refresh button
  is also available in the header.

  Props:
    activeTaskId: the task currently selected in the main UI.
    modelRegistry: optional registry object used by useContextWindow.
-->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
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

async function requestRefresh() {
  if (!props.activeTaskId) return
  isLoading.value = true
  try {
    emit('refresh')
  } finally {
    // Keep spinner visible briefly to avoid flicker.
    setTimeout(() => { isLoading.value = false }, 200)
  }
}

// Keep the composable's active task in sync with the prop.
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

// Auto-refresh once when mounted/opened if snapshot is empty but a task exists.
onMounted(() => {
  if (props.activeTaskId && !currentSnapshot.value) {
    requestRefresh()
  }
})

const usagePercent = computed(() => {
  if (!latest.value) return 0
  return Math.min(100, latest.value.estimated_usage_ratio * 100)
})

const usageColor = computed(() => {
  const p = usagePercent.value
  if (p < 50) return 'bg-emerald-500'
  if (p < 80) return 'bg-amber-500'
  return 'bg-red-500'
})

const usageTextColor = computed(() => {
  const p = usagePercent.value
  if (p < 50) return 'text-emerald-400'
  if (p < 80) return 'text-amber-400'
  return 'text-red-400'
})

const roleColors: Record<string, string> = {
  system: 'bg-purple-500',
  user: 'bg-blue-500',
  assistant: 'bg-green-500',
  tool: 'bg-orange-500',
}

const roleBars: Record<string, string> = {
  system: 'bg-purple-500/70',
  user: 'bg-blue-500/70',
  assistant: 'bg-green-500/70',
  tool: 'bg-orange-500/70',
}

const roleLabels: Record<string, string> = {
  system: '📋 system',
  user: '👤 user',
  assistant: '🤖 assistant',
  tool: '🔧 tool',
}

function formatTokens(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return `${n}`
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
  // Stable order: system, user, assistant, tool, others
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

function roleBarStyle(ratio: number) {
  return { width: `${Math.max(0, Math.min(100, ratio * 100))}%` }
}

function messageRatio(msg: ContextSnapshotMessage): number {
  if (!latest.value) return 0
  const total = latest.value.estimated_total_tokens || 1
  return msg.estimated_tokens / total
}
</script>

<template>
  <div class="h-full flex flex-col gap-4 p-4 overflow-hidden bg-gray-900 text-gray-100">
    <!-- Header row: title + model chip + refresh -->
    <div class="flex items-center justify-between gap-3 min-w-0">
      <div class="flex items-center gap-3 min-w-0">
        <h2 class="text-lg font-semibold whitespace-nowrap">🪟 Context Window</h2>
        <span
          v-if="latest"
          class="shrink-0 inline-flex items-center px-2 py-1 rounded-full text-[10px] leading-none font-medium bg-gray-800 text-gray-400 border border-gray-700 truncate max-w-[150px]"
        >
          {{ latest.model || 'unknown model' }}
        </span>
      </div>
      <button
        class="shrink-0 inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md text-xs font-medium bg-gray-800 text-gray-300 border border-gray-700 hover:bg-gray-700 hover:text-white transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        title="Refresh context window snapshot"
        :disabled="!activeTaskId || isLoading"
        @click="requestRefresh"
      >
        <span v-if="isLoading" class="inline-block w-3 h-3 border-2 border-gray-500 border-t-transparent rounded-full animate-spin" />
        <span v-else>🔄</span>
        Refresh
      </button>
    </div>

    <div v-if="!latest" class="text-sm text-gray-400">
      {{ isLoading ? 'Loading snapshot...' : 'Waiting for the next agent think step...' }}
    </div>

    <template v-else>
      <!-- Total usage -->
      <div class="bg-gray-800 rounded-xl p-4 flex flex-col gap-3 border border-gray-700 shadow-sm">
        <div class="flex items-baseline justify-between gap-2">
          <span class="text-sm font-medium text-gray-200">Context Window</span>
          <span class="font-mono text-xs text-gray-400 shrink-0">
            {{ formatTokens(latest.estimated_total_tokens) }} / {{ formatTokens(latest.max_context_tokens) }} tokens
          </span>
        </div>
        <div class="flex items-end gap-3">
          <span class="text-3xl font-bold tracking-tight" :class="usageTextColor">
            {{ usagePercent.toFixed(1) }}%
          </span>
        </div>
        <div class="w-full h-5 bg-gray-700 rounded-full overflow-hidden">
          <div
            class="h-full transition-all duration-500 ease-out"
            :class="usageColor"
            :style="{ width: `${usagePercent}%` }"
          />
        </div>
        <p class="text-[11px] leading-snug text-gray-500">
          estimated (local heuristic, not API exact tokens)
        </p>
      </div>

      <!-- Role breakdown grid -->
      <div class="bg-gray-800 rounded-xl p-4 border border-gray-700 shadow-sm flex flex-col gap-3">
        <h3 class="text-xs font-semibold uppercase tracking-wider text-gray-500">Role breakdown</h3>
        <div class="grid grid-cols-2 gap-3">
          <div
            v-for="row in roleSummary"
            :key="row.role"
            class="bg-gray-900/60 rounded-lg p-3 border border-gray-700/60 flex flex-col gap-2"
          >
            <div class="flex items-center gap-2 min-w-0">
              <span
                class="inline-block w-2 h-2 rounded-full shrink-0"
                :class="roleColors[row.role] || 'bg-gray-500'"
              />
              <span class="text-xs font-medium text-gray-300 truncate">
                {{ roleLabels[row.role] || row.role }}
              </span>
            </div>
            <div class="flex items-baseline justify-between gap-2">
              <span class="text-xl font-bold text-gray-100">
                {{ (row.ratio * 100).toFixed(0) }}%
              </span>
              <span class="font-mono text-[10px] text-gray-500 shrink-0">
                {{ formatTokens(row.tokens) }} tok
              </span>
            </div>
            <div class="w-full h-1.5 bg-gray-700 rounded-full overflow-hidden">
              <div
                class="h-full rounded-full"
                :class="roleBars[row.role] || 'bg-gray-500'"
                :style="roleBarStyle(row.ratio)"
              />
            </div>
            <div class="text-[10px] text-gray-600">
              {{ row.count }} message{{ row.count > 1 ? 's' : '' }}
            </div>
          </div>
        </div>
      </div>

      <!-- Message list -->
      <div class="flex-1 overflow-y-auto min-h-0 pr-1 space-y-3">
        <div
          v-for="(msg, idx) in latest.messages"
          :key="idx"
          class="bg-gray-800 rounded-xl border border-gray-700 overflow-hidden shadow-sm"
        >
          <details class="group">
            <summary class="cursor-pointer p-3 flex items-center justify-between gap-3 select-none">
              <div class="flex items-center gap-2 min-w-0">
                <span
                  class="inline-block w-2 h-2 rounded-full shrink-0"
                  :class="roleColors[msg.role] || 'bg-gray-500'"
                />
                <span class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-gray-700 text-gray-200 truncate">
                  {{ roleLabels[msg.role] || msg.role }}
                </span>
                <span class="text-[10px] text-gray-500">
                  {{ (messageRatio(msg) * 100).toFixed(0) }}%
                </span>
              </div>
              <div class="flex items-center gap-3 shrink-0">
                <span class="text-[10px] font-mono text-gray-400">
                  {{ formatTokens(msg.estimated_tokens) }} tok
                </span>
                <span class="text-[10px] text-gray-500 group-open:rotate-90 transition-transform duration-200">
                  ▶
                </span>
              </div>
            </summary>
            <div class="px-3 pb-3 space-y-2 text-sm">
              <div
                v-if="msg.reasoning"
                class="p-2.5 rounded-lg bg-gray-700/50 border-l-2 border-purple-500/80 text-xs text-gray-300 whitespace-pre-wrap break-words"
              >
                <span class="block text-[10px] uppercase tracking-wider text-gray-500 mb-1">Reasoning</span>
                {{ msg.reasoning }}
              </div>
              <div
                v-if="msg.tool_call_id"
                class="p-2 rounded-lg bg-gray-900/60 border border-gray-700 text-[10px] text-gray-500 font-mono break-all"
              >
                <span class="text-gray-600">tool_call_id:</span> {{ msg.tool_call_id }}
              </div>
              <div
                v-if="msg.tool_calls && msg.tool_calls.length"
                class="p-2 rounded-lg bg-gray-900/60 border border-gray-700 text-[10px] text-gray-500 font-mono"
              >
                <span class="text-gray-600">tool_calls:</span> {{ msg.tool_calls.length }}
              </div>
              <div class="p-3 rounded-lg bg-gray-900/60 border border-gray-700 text-gray-200 font-mono text-xs whitespace-pre-wrap break-words">
                {{ msg.content || '(empty content)' }}
              </div>
            </div>
          </details>
        </div>
      </div>
    </template>
  </div>
</template>
