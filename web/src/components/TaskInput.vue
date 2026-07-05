<!-- TaskInput — chat input area with send button and control buttons
     Props:
       disabled: whether the input is disabled (during task execution)
       isRunning: whether a task is currently running

     Emits:
       send: user clicked send with the input text
       pause: user clicked pause
       resume: user clicked resume
       cancel: user clicked cancel
-->
<script setup lang="ts">
import { ref } from 'vue'

const props = defineProps<{
  disabled: boolean
  isRunning: boolean
  isPending: boolean
}>()

const emit = defineEmits<{
  send: [text: string]
  pause: []
  resume: []
  cancel: []
}>()

const inputText = ref('')

function handleSend() {
  const text = inputText.value.trim()
  if (!text || props.disabled) return
  emit('send', text)
  inputText.value = ''
}

/** Send on Enter (without Shift) */
function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    handleSend()
  }
}
</script>

<template>
  <div class="task-input">
    <div class="input-row">
      <textarea
        v-model="inputText"
        class="input-textarea"
        :disabled="disabled"
        placeholder="Enter your task description... (e.g., 'Write a Python script to analyze a CSV file')"
        rows="2"
        @keydown="handleKeydown"
      ></textarea>
      <button
        class="btn btn-send"
        :disabled="disabled || !inputText.trim()"
        @click="handleSend"
      >
        <span v-if="isPending" class="btn-spinner"></span>
        <span v-else>Send</span>
      </button>
    </div>

    <!-- Control buttons — visible only when a task is running -->
    <div v-if="isRunning" class="control-row">
      <button class="btn btn-pause" @click="emit('pause')">⏸ Pause</button>
      <button class="btn btn-resume" @click="emit('resume')">▶ Resume</button>
      <button class="btn btn-cancel" @click="emit('cancel')">⏹ Cancel</button>
    </div>
  </div>
</template>

<style scoped>
.task-input {
  background: #252525;
  border: 1px solid #333;
  border-radius: 8px;
  padding: 12px;
}

.input-row {
  display: flex;
  gap: 10px;
  align-items: flex-end;
}

.input-textarea {
  flex: 1;
  background: #1e1e1e;
  border: 1px solid #444;
  border-radius: 6px;
  color: #d4d4d4;
  padding: 8px 12px;
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 13px;
  resize: vertical;
  outline: none;
  transition: border-color 0.2s;
}

.input-textarea:focus {
  border-color: #4a9eff;
}

.input-textarea:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn {
  padding: 8px 16px;
  border: none;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s, opacity 0.2s;
  white-space: nowrap;
}

.btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.btn-send {
  background: #4a9eff;
  color: #fff;
  min-width: 72px;
}

.btn-send:hover:not(:disabled) {
  background: #3a8eef;
}

/* Loading spinner inside Send button */
.btn-spinner {
  display: inline-block;
  width: 14px;
  height: 14px;
  border: 2px solid rgba(255,255,255,0.3);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.control-row {
  display: flex;
  gap: 8px;
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px solid #333;
}

.btn-pause {
  background: #f0a030;
  color: #fff;
}

.btn-pause:hover {
  background: #e09020;
}

.btn-resume {
  background: #51cf66;
  color: #fff;
}

.btn-resume:hover {
  background: #41bf56;
}

.btn-cancel {
  background: #ff6b6b;
  color: #fff;
}

.btn-cancel:hover {
  background: #ef5b5b;
}
</style>