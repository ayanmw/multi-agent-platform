<!-- StatusIndicator — renders a colored dot + label for step/task status
     Props:
       status: 'idle' | 'running' | 'completed' | 'failed' | 'pending'
       label: optional text label (shown next to the dot)

     Color scheme:
       idle      — gray (task exists but not running, e.g. fresh session)
       running   — blue pulsing
       completed — green
       failed    — red
       pending   — gray (alias of idle, kept for backward compat)
-->
<script setup lang="ts">
defineProps<{
  status: 'idle' | 'running' | 'completed' | 'failed' | 'pending'
  label?: string
}>()
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

/* Idle — gray dot (task exists but not running) */
.status-indicator.idle .status-dot {
  background: #888;
}

/* Running — blue pulsing dot */
.status-indicator.running .status-dot {
  background: #4a9eff;
  animation: pulse 1.2s ease-in-out infinite;
}

/* Completed — green dot */
.status-indicator.completed .status-dot {
  background: #51cf66;
}

/* Failed — red dot */
.status-indicator.failed .status-dot {
  background: #ff6b6b;
}

/* Pending — gray dot (legacy alias of idle) */
.status-indicator.pending .status-dot {
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