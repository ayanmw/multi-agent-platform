<script setup lang="ts">
/**
 * 状态指示灯
 *
 * props:
 *   - status: 状态枚举
 *   - label: 状态文字标签
 */
withDefaults(
  defineProps<{
    status: 'idle' | 'running' | 'paused' | 'completed' | 'failed' | 'pending'
    label?: string
  }>(),
  {
    label: '',
  },
)
</script>

<template>
  <span class="status-indicator" :class="status">
    <span class="status-dot"></span>
    <span v-if="label" class="status-label">{{ label }}</span>
  </span>
</template>

<style scoped>
.status-indicator {
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

.status-indicator.idle .status-dot {
  background: #888;
}

.status-indicator.running .status-dot {
  background: var(--accent-running, #00e5ff);
  box-shadow: 0 0 6px var(--accent-running, #00e5ff);
  animation: pulse 1.2s ease-in-out infinite;
}

.status-indicator.paused .status-dot {
  background: var(--accent-warning, #ffb800);
  animation: pulse 1.6s ease-in-out infinite;
}

.status-indicator.completed .status-dot {
  background: var(--accent-success, #39ff14);
}

.status-indicator.failed .status-dot {
  background: var(--accent-danger, #ff4d4d);
}

.status-indicator.pending .status-dot {
  background: var(--text-muted, #5c6675);
}

.status-label {
  color: var(--text-secondary, #9aa3b2);
  font-family: var(--font-mono, monospace);
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}
</style>
