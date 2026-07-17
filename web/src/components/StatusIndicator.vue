<script setup lang="ts">
defineProps<{
  status: 'idle' | 'running' | 'paused' | 'completed' | 'failed' | 'pending'
  label?: string
}>()
</script>

<template>
  <span class="statusindicator" :class="status">
    <span class="status-dot"></span>
    <span v-if="label" class="status-label">{{ label }}</span>
  </span>
</template>

<style scoped>
.statusindicator {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
}
.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  display: inline-block;
}
.statusindicator.idle .status-dot {
  background: #888;
}
.statusindicator.running .status-dot {
  background: #4a9eff;
  animation: pulse 1.2s ease-in-out infinite;
}
.statusindicator.paused .status-dot {
  background: #ffa94d;
}
.statusindicator.completed .status-dot {
  background: #51cf66;
}
.statusindicator.failed .status-dot {
  background: #ff6b6b;
}
.statusindicator.pending .status-dot {
  background: #666;
}
.status-label {
  color: #999;
}
@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}
</style>
