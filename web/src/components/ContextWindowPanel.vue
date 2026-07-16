<!--
  ContextWindowPanel.vue — real-time context window observability

  Displays the latest backend `context_window_snapshot` as:
    1. A compact header with model name and token totals.
    2. A progress bar showing estimated total tokens / max context tokens.
    3. A role-grouped bar chart with per-role token share.
    4. Expandable message cards so the user can inspect exactly what the
       agent sees (system prompt, user input, assistant reasoning, tool results).

  This component is deliberately lightweight: it receives all data through
  `useContextWindow` and only handles presentation.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useContextWindow } from '../composables/useContextWindow'
import type { ContextSnapshotMessage } from '../types/events'

const { latest } = useContextWindow()

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
  <div class="h-full flex flex-col p-4 space-y-4 overflow-hidden bg-gray-900 text-gray-100">
    <div class="flex items-center justify-between">
      <h2 class="text-lg font-semibold">🪟 Context Window</h2>
      <span v-if="latest" class="text-xs text-gray-400 truncate max-w-[180px]">
        {{ latest.model || 'unknown model' }}
      </span>
    </div>

    <div v-if="!latest" class="text-sm text-gray-400">
      Waiting for the next agent think step...
    </div>

    <template v-else>
      <!-- Total usage -->
      <div class="bg-gray-800 rounded-lg p-4 space-y-2">
        <div class="flex items-end justify-between">
          <span class="text-sm text-gray-300">Context Window</span>
          <span class="font-mono text-sm">
            {{ formatTokens(latest.estimated_total_tokens) }} / {{ formatTokens(latest.max_context_tokens) }}
          </span>
        </div>
        <div class="w-full h-4 bg-gray-700 rounded-full overflow-hidden">
          <div
            class="h-full transition-all duration-300"
            :class="usageColor"
            :style="{ width: `${usagePercent}%` }"
          />
        </div>
        <div class="text-xs text-gray-400">
          {{ usagePercent.toFixed(1) }}% estimated (local heuristic, not API exact tokens)
        </div>
      </div>

      <!-- Role breakdown bar chart -->
      <div class="bg-gray-800 rounded-lg p-4 space-y-3">
        <h3 class="text-sm font-medium text-gray-300">Role breakdown</h3>
        <div
          v-for="row in roleSummary"
          :key="row.role"
          class="space-y-1"
        >
          <div class="flex items-center justify-between text-sm">
            <span class="capitalize">{{ roleLabels[row.role] || row.role }}</span>
            <span class="font-mono text-gray-300">
              {{ (row.ratio * 100).toFixed(0) }}%
            </span>
          </div>
          <div class="w-full h-2 bg-gray-700 rounded-full overflow-hidden">
            <div
              class="h-full rounded-full"
              :class="roleBars[row.role] || 'bg-gray-500'"
              :style="roleBarStyle(row.ratio)"
            />
          </div>
          <div class="text-xs text-gray-500">
            {{ row.count }} message{{ row.count > 1 ? 's' : '' }} · {{ formatTokens(row.tokens) }} tok
          </div>
        </div>
      </div>

      <!-- Message list -->
      <div class="flex-1 overflow-y-auto space-y-2 min-h-0">
        <div
          v-for="(msg, idx) in latest.messages"
          :key="idx"
          class="bg-gray-800 rounded-lg border border-gray-700 overflow-hidden"
        >
          <details class="group">
            <summary class="cursor-pointer p-3 flex items-center justify-between select-none">
              <div class="flex items-center gap-2 min-w-0">
                <span
                  class="inline-block w-2 h-2 rounded-full shrink-0"
                  :class="roleColors[msg.role] || 'bg-gray-500'"
                />
                <span class="text-sm font-medium truncate">
                  {{ roleLabels[msg.role] || msg.role }}
                </span>
                <span class="text-xs text-gray-400">
                  {{ (messageRatio(msg) * 100).toFixed(0) }}%
                </span>
              </div>
              <div class="flex items-center gap-3 shrink-0">
                <span class="text-xs font-mono text-gray-400">
                  {{ formatTokens(msg.estimated_tokens) }} tok
                </span>
                <span class="text-xs text-gray-500 group-open:rotate-90 transition-transform">
                  ▶
                </span>
              </div>
            </summary>
            <div class="px-3 pb-3 text-sm whitespace-pre-wrap break-words font-mono bg-gray-850/50">
              <div v-if="msg.reasoning" class="mb-2 p-2 bg-gray-700 rounded text-xs border-l-2 border-gray-500">
                <strong>Reasoning:</strong>
{{ msg.reasoning }}
              </div>
              <div v-if="msg.tool_call_id" class="mb-2 text-xs text-gray-400">
                tool_call_id: {{ msg.tool_call_id }}
              </div>
              <div v-if="msg.tool_calls && msg.tool_calls.length" class="mb-2 text-xs text-gray-400">
                tool_calls: {{ msg.tool_calls.length }}
              </div>
              <div class="text-gray-200">{{ msg.content || '(empty content)' }}</div>
            </div>
          </details>
        </div>
      </div>
    </template>
  </div>
</template>
