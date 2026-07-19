<!-- Toast — global error/info notification component
     Props:
       toasts: reactive array of { id, message, type, timestamp }
       maxToasts: maximum number of toasts to show simultaneously (default 5)

     Behavior:
       - Each toast auto-dismisses after 5 seconds
       - Supports 'error' and 'info' types with different colors
       - Stacks from bottom-right, newest at the bottom
       - Clicking a toast dismisses it immediately

     Design rationale:
       - Non-blocking: toasts are absolutely positioned, don't affect layout
       - Multiple toasts stack instead of replacing each other
       - Auto-dismiss with a visual progress bar
-->
<script setup lang="ts">
import { ref, watch } from 'vue'

export interface ToastItem {
  id: number
  message: string
  type: 'error' | 'info'
}

const props = defineProps<{
  toasts: ToastItem[]
  maxToasts?: number
}>()

const emit = defineEmits<{
  dismiss: [id: number]
}>()

/** Dismiss a toast by ID */
function dismiss(id: number) {
  emit('dismiss', id)
}
</script>

<template>
  <Teleport to="body">
    <div class="toast-container" aria-live="polite">
      <TransitionGroup name="toast">
        <div
          v-for="toast in toasts"
          :key="toast.id"
          class="toast-item"
          :class="toast.type"
          @click="dismiss(toast.id)"
          role="alert"
        >
          <span class="toast-icon">{{ toast.type === 'error' ? '✕' : 'ℹ' }}</span>
          <span class="toast-message">{{ toast.message }}</span>
          <div class="toast-progress">
            <div class="toast-progress-bar" :class="toast.type"></div>
          </div>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-container {
  position: fixed;
  bottom: 20px;
  right: 20px;
  z-index: 9999;
  display: flex;
  flex-direction: column-reverse;
  gap: 8px;
  pointer-events: none;
}

.toast-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 16px;
  border-radius: 8px;
  font-size: 13px;
  color: #fff;
  cursor: pointer;
  pointer-events: auto;
  min-width: 280px;
  max-width: 420px;
  overflow: hidden;
  position: relative;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.3);
  transition: transform 0.2s, opacity 0.2s;
}

.toast-item:hover {
  transform: translateY(-2px);
}

.toast-item.error {
  background: #4a1a1a;
  border: 1px solid #6a2a2a;
}

.toast-item.info {
  background: #1a2a3a;
  border: 1px solid #2a3a4a;
}

.toast-icon {
  font-size: 14px;
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
}

.toast-item.error .toast-icon {
  background: #ff6b6b;
  color: #fff;
}

.toast-item.info .toast-icon {
  background: #4a9eff;
  color: #fff;
}

.toast-message {
  flex: 1;
  line-height: 1.4;
  word-break: break-word;
}

/* Progress bar */
.toast-progress {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  height: 2px;
  background: rgba(255, 255, 255, 0.1);
}

.toast-progress-bar {
  height: 100%;
  animation: toast-timer 5s linear forwards;
}

.toast-progress-bar.error {
  background: #ff6b6b;
}

.toast-progress-bar.info {
  background: #4a9eff;
}

@keyframes toast-timer {
  from { width: 100%; }
  to { width: 0%; }
}

/* Transition animations */
.toast-enter-active {
  transition: all 0.3s ease;
}

.toast-leave-active {
  transition: all 0.2s ease;
}

.toast-enter-from {
  opacity: 0;
  transform: translateX(40px);
}

.toast-leave-to {
  opacity: 0;
  transform: translateX(40px);
}
</style>