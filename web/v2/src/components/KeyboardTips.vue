<!-- KeyboardTips — floating panel showing all keyboard shortcuts
     Props:
       visible: whether the panel is shown
       shortcuts: array of shortcuts to display
       isRunning: whether a task is currently running

     Emits:
       close: user clicked close or overlay
-->
<script setup lang="ts">
defineProps<{
  visible: boolean
  shortcuts: Array<{ keys: string; description: string; active: string }>
  isRunning: boolean
}>()

defineEmits<{
  close: []
}>()
</script>

<template>
  <Teleport to="body">
    <Transition name="tips">
      <div v-if="visible" class="tips-overlay" @click.self="$emit('close')">
        <div class="tips-panel">
          <div class="tips-header">
            <h2>⌨ Keyboard Shortcuts</h2>
            <button class="tips-close-btn" @click="$emit('close')" title="Close">✕</button>
          </div>

          <div class="tips-body">
            <div class="tips-status">
              <span class="tips-status-dot" :class="isRunning ? 'running' : 'idle'"></span>
              <span v-if="isRunning">Task is running — all shortcuts active</span>
              <span v-else>No task running — only "?" toggle is active</span>
            </div>

            <div class="tips-list">
              <div
                v-for="s in shortcuts"
                :key="s.keys"
                class="tips-item"
                :class="{ 'tips-disabled': !isRunning && s.keys !== '?' }"
              >
                <kbd class="tips-key">{{ s.keys }}</kbd>
                <span class="tips-desc">{{ s.description }}</span>
                <span class="tips-active">{{ s.active }}</span>
              </div>
            </div>
          </div>

          <div class="tips-footer">
            Press <kbd>?</kbd> to toggle this panel
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.tips-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  backdrop-filter: blur(2px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 10001;
  padding: 20px;
}

.tips-panel {
  background: #252525;
  border: 1px solid #444;
  border-radius: 12px;
  max-width: 440px;
  width: 100%;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
}

.tips-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 20px;
  border-bottom: 1px solid #333;
}

.tips-header h2 {
  font-size: 16px;
  font-weight: 600;
  color: #e0e0e0;
  margin: 0;
}

.tips-close-btn {
  background: none;
  border: none;
  color: #888;
  font-size: 18px;
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 4px;
  transition: color 0.2s, background 0.2s;
}

.tips-close-btn:hover {
  color: #fff;
  background: #333;
}

.tips-body {
  padding: 16px 20px;
}

.tips-status {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 16px;
  font-size: 12px;
  color: #999;
  padding: 8px 12px;
  background: #1e1e1e;
  border-radius: 6px;
}

.tips-status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.tips-status-dot.running {
  background: #51cf66;
}

.tips-status-dot.idle {
  background: #888;
}

.tips-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.tips-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  background: #1e1e1e;
  border-radius: 6px;
  border: 1px solid #333;
}

.tips-item.tips-disabled {
  opacity: 0.4;
}

.tips-key {
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 12px;
  font-weight: 600;
  color: #4a9eff;
  background: #1a2a3a;
  padding: 2px 8px;
  border-radius: 4px;
  border: 1px solid #2a3a4a;
  white-space: nowrap;
  min-width: 90px;
  text-align: center;
}

.tips-desc {
  flex: 1;
  font-size: 13px;
  color: #d4d4d4;
}

.tips-active {
  font-size: 10px;
  color: #888;
  white-space: nowrap;
}

.tips-footer {
  padding: 12px 20px;
  border-top: 1px solid #333;
  font-size: 12px;
  color: #888;
  text-align: center;
}

.tips-footer kbd {
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 11px;
  color: #4a9eff;
  background: #333;
  padding: 1px 6px;
  border-radius: 3px;
}

/* Transition */
.tips-enter-active,
.tips-leave-active {
  transition: all 0.2s ease;
}

.tips-enter-from,
.tips-leave-to {
  opacity: 0;
}

.tips-enter-from .tips-panel,
.tips-leave-to .tips-panel {
  transform: scale(0.95);
}
</style>