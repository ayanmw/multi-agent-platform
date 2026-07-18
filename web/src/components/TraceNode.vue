<template>
  <li class="trace-node">
    <span :class="['badge', node.status]">{{ node.status }}</span>
    <span class="op">{{ node.operation }}</span>
    <span class="dur">{{ node.duration_ms }}ms</span>
    <ul v-if="node.children?.length">
      <TraceNode v-for="child in node.children" :key="child.span_id" :node="child" />
    </ul>
  </li>
</template>

<script setup lang="ts">
import type { SpanNode } from '@/composables/useTraceStore'
defineProps<{ node: SpanNode }>()
</script>
