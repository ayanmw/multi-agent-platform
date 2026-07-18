<template>
  <div class="trace-tree-panel">
    <h3>Trace Tree</h3>
    <ul class="trace-list">
      <TraceNode v-for="node in spans" :key="node.span_id" :node="node" />
    </ul>
  </div>
</template>

<script setup lang="ts">
import { useTraceStore } from '@/composables/useTraceStore'
import TraceNode from './TraceNode.vue'

const { spans } = useTraceStore()
</script>
