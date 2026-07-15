<!--
  ContextWindowPanel.vue — real-time context window observability

  Displays the latest backend `context_window_snapshot` as:
    1. A progress bar showing estimated total tokens / max context tokens.
    2. A role breakdown with per-message token estimates.
    3. Expandable cards for each message so the user can inspect exactly what
       the agent sees (system prompt, user input, assistant reasoning, tool results).

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
  if (p < 80) return 'bg-yellow-500'
  return 'bg-red-500'
})

const roleColors: Record<string, string> = {
  system: 'border-purple-400',
  user: 'border-blue-400',
  assistant: 'border-green-400',
  tool: 'border-orange-400',
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

function summary(messages: ContextSnapshotMessage[]) {
  const byRole: Record<string, { count: number; tokens: number }> = {}
  for (const m of messages) {
    const r = m.role
    if (!byRole[r]) byRole[r] = { count: 0, tokens: 0 }
    byRole[r].count++
    byRole[r].tokens += m.estimated_tokens
  }
  return byRole
}
</script>

<template>
  <div class="h-full flex flex-col p-4 space-y-4 overflow-hidden bg-gray-900 text-gray-100">
    <div class="flex items-center justify-between">
      <h2 class="text-lg font-semibold">🪟 Context Window</h2>
      <span v-if="latest" class="text-sm text-gray-400">
        {{ latest.model || 'unknown model' }}
      </span>
    </div>

    <div v-if="!latest" class="text-sm text-gray-400">
      Waiting for the next agent think step...
    </div>

    <template v-else>
      <!-- Progress card -->
      <div class="bg-gray-800 rounded-lg p-4 space-y-2">
        <div class="flex justify-between text-sm">
          <span>Estimated usage</span>
          <span class="font-mono">
            {{ formatTokens(latest.estimated_total_tokens) }} / {{ formatTokens(latest.max_context_tokens) }}
          </span>
        </div>
        <div class="w-full h-3 bg-gray-700 rounded-full overflow-hidden">
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

      <!-- Role breakdown -->
      <div class="bg-gray-800 rounded-lg p-4">
        <h3 class="text-sm font-medium mb-2">Role breakdown</h3>
        <div class="space-y-2">
          <div
            v-for="(meta, role) in summary(latest.messages)"
            :key="role"
            class="flex items-center justify-between text-sm"
          >
            <span class="capitalize">{{ role }}</span>
            <span class="font-mono text-gray-300">
              {{ meta.count }} msgs · {{ formatTokens(meta.tokens) }} tok
            </span>
          </div>
        </div>
      </div>

      <!-- Message list -->
      <div class="flex-1 overflow-y-auto space-y-2">
        <div
          v-for="(msg, idx) in latest.messages"
          :key="idx"
          class="bg-gray-800 rounded-lg border-l-4"
          :class="roleColors[msg.role] || 'border-gray-500'"
        >
          <details class="group">
            <summary class="cursor-pointer p-3 flex items-center justify-between select-none">
              <span class="text-sm font-medium">
                {{ roleLabels[msg.role] || msg.role }}
                <span class="ml-2 text-xs text-gray-400">
                  {{ formatTokens(msg.estimated_tokens) }} tok
                </span>
              </span>
              <span class="text-xs text-gray-500 group-open:rotate-90 transition-transform">
                ▶
              </span>
            </summary>
            <div class="px-3 pb-3 text-sm whitespace-pre-wrap break-words font-mono bg-gray-850 rounded-b-lg">
              <div v-if="msg.reasoning" class="mb-2 p-2 bg-gray-700 rounded text-xs">
                <strong>Reasoning:</strong>\n{{ msg.reasoning }}
              </div>
              <div v-if="msg.tool_call_id" class="mb-2 text-xs text-gray-400">
                tool_call_id: {{ msg.tool_call_id }}
              </div>
              <div>{{ msg.content || '(empty)' }}</div>
            </div>
          </details>
        </div>
      </div>
    </template>
  </div>
</template>
