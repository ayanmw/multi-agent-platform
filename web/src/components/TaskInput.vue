<!-- TaskInput — chat input area with send button, control buttons, and task options
     Props:
       disabled: whether the input is disabled (during task execution)
       isRunning: whether a task is currently running
       isPending: whether a task is starting
       enableMultiAgent: whether multi-agent mode is enabled

     Emits:
       send: user clicked send with input text and selected options
       pause: user clicked pause
       resume: user clicked resume
       cancel: user clicked cancel
       update:enableMultiAgent: toggled multi-agent mode
-->
<script setup lang="ts">
import { ref, onMounted } from 'vue'

const MAX_STEPS_STORAGE_KEY = 'map_default_max_steps'
const DEFAULT_MAX_STEPS = 30

const TIMEOUT_STORAGE_KEY = 'map_default_timeout_seconds'
const DEFAULT_TIMEOUT_SECONDS = 0

const QUICK_TIMEOUTS_SECONDS = [
  { label: 'Unlimited', value: 0 },
  { label: '5 min', value: 5 * 60 },
  { label: '10 min', value: 10 * 60 },
  { label: '30 min', value: 30 * 60 },
  { label: '60 min', value: 60 * 60 },
  { label: '120 min', value: 120 * 60 },
]
const MAX_TIMEOUT_MINUTES = 120

export interface SendOptions {
  maxSteps: number
  timeoutSeconds?: number
}

const props = defineProps<{
  disabled: boolean
  isRunning: boolean
  isPending: boolean
  enableMultiAgent?: boolean
}>()

const emit = defineEmits<{
  send: [text: string, options: SendOptions]
  pause: []
  resume: []
  cancel: []
  toggleContextWindow: []
  'update:enableMultiAgent': [value: boolean]
}>()

const inputText = ref('')
const showOptions = ref(false)
const maxSteps = ref(DEFAULT_MAX_STEPS)
const timeoutSeconds = ref(DEFAULT_TIMEOUT_SECONDS)

// Load saved preference on mount so the user's choice survives refreshes.
onMounted(() => {
  try {
    const savedSteps = localStorage.getItem(MAX_STEPS_STORAGE_KEY)
    if (savedSteps) {
      const n = parseInt(savedSteps, 10)
      if (!Number.isNaN(n) && n > 0) {
        maxSteps.value = n
      }
    }
  } catch {
    // ignore storage errors
  }
  try {
    const savedTimeout = localStorage.getItem(TIMEOUT_STORAGE_KEY)
    if (savedTimeout) {
      const n = parseInt(savedTimeout, 10)
      if (!Number.isNaN(n) && n >= 0 && n <= MAX_TIMEOUT_MINUTES * 60) {
        timeoutSeconds.value = n
      }
    }
  } catch {
    // ignore storage errors
  }
})

// TODO: Phase 7 — 从后端读取 max_steps 合理范围
// 当前 quickSteps / 滑块上限 50 是前端硬编码，与后端实际允许的范围可能脱节。
// 后续应通过 GET /api/agents/:id 或 case 配置回读合理上限，避免用户设置一个
// 后端拒绝的值（或滑块范围远超实际可用区间）。
const quickSteps = [2, 5, 10, 15, 20, 30, 50]

function handleSend() {
  const text = inputText.value.trim()
  if (!text || props.disabled) return
  emit('send', text, { maxSteps: maxSteps.value, timeoutSeconds: timeoutSeconds.value })
  inputText.value = ''
}

/** Send on Enter (without Shift) */
function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    handleSend()
  }
}

function setMaxSteps(n: number) {
  maxSteps.value = n
  try {
    localStorage.setItem(MAX_STEPS_STORAGE_KEY, String(n))
  } catch {
    // ignore storage errors
  }
}
function setTimeoutSeconds(seconds: number) {
  timeoutSeconds.value = Math.max(0, Math.min(seconds, MAX_TIMEOUT_MINUTES * 60))
  try {
    localStorage.setItem(TIMEOUT_STORAGE_KEY, String(timeoutSeconds.value))
  } catch {
    // ignore storage errors
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

    <!-- Task options toggle -->
    <div class="options-row">
      <button
        class="options-toggle"
        :class="{ active: showOptions }"
        @click="showOptions = !showOptions"
      >
        ⚙️ Options
      </button>
      <button
        class="options-toggle"
        title="Open context window panel"
        @click="emit('toggleContextWindow')"
      >
        🪟 Context
      </button>
      <button
        class="options-toggle"
        :class="{ active: props.enableMultiAgent }"
        title="Use multi-agent mode"
        @click="emit('update:enableMultiAgent', !props.enableMultiAgent)"
      >
        🤖 Multi-Agent
      </button>
      <span v-if="!showOptions" class="options-summary">
        Max steps: {{ maxSteps }} · Timeout: {{ timeoutSeconds === 0 ? 'Unlimited' : (timeoutSeconds / 60) + ' min' }}
      </span>
    </div>

    <!-- Task options panel -->
    <div v-if="showOptions" class="options-panel">
      <div class="option-group">
        <label class="option-label">Max Steps: {{ maxSteps }}</label>
        <div class="quick-steps">
          <button
            v-for="n in quickSteps"
            :key="n"
            class="quick-step-btn"
            :class="{ active: maxSteps === n }"
            @click="setMaxSteps(n)"
          >
            {{ n }}
          </button>
        </div>
        <input
          v-model.number="maxSteps"
          type="range"
          min="1"
          max="50"
          class="steps-slider"
        />
        <div class="option-hint">
          Maximum number of ReAct loop iterations. Increase for long tasks, decrease for quick tasks.
        </div>
      </div>

      <div class="option-group">
        <label class="option-label">
          Timeout: {{ timeoutSeconds === 0 ? 'Unlimited' : (timeoutSeconds / 60) + ' min' }}
        </label>
        <div class="quick-steps">
          <button
            v-for="item in QUICK_TIMEOUTS_SECONDS"
            :key="item.value"
            class="quick-step-btn"
            :class="{ active: timeoutSeconds === item.value }"
            @click="setTimeoutSeconds(item.value)"
          >
            {{ item.label }}
          </button>
        </div>
        <input
          :value="timeoutSeconds / 60"
          type="range"
          min="0"
          :max="MAX_TIMEOUT_MINUTES"
          step="1"
          class="steps-slider"
          @input="setTimeoutSeconds(Number(($event.target as HTMLInputElement).value) * 60)"
        />
        <div class="option-hint">
          Maximum execution time for this task. 0 means unlimited.
        </div>
      </div>
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

/* Options row */
.options-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 8px;
}

.options-toggle {
  background: transparent;
  border: 1px solid #444;
  color: #999;
  border-radius: 4px;
  padding: 3px 10px;
  font-size: 11px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
}

.options-toggle:hover,
.options-toggle.active {
  background: #333;
  color: #d4d4d4;
  border-color: #4a9eff;
}

.options-summary {
  font-size: 11px;
  color: #888;
}

/* Options panel */
.options-panel {
  margin-top: 10px;
  padding: 12px;
  background: #1e1e1e;
  border: 1px solid #333;
  border-radius: 6px;
}

.option-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.option-label {
  font-size: 12px;
  color: #aaa;
  font-weight: 600;
}

.quick-steps {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.quick-step-btn {
  background: #333;
  border: 1px solid #444;
  color: #999;
  border-radius: 4px;
  padding: 3px 10px;
  font-size: 11px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
}

.quick-step-btn:hover,
.quick-step-btn.active {
  background: #4a9eff;
  color: #fff;
  border-color: #4a9eff;
}

.steps-slider {
  width: 100%;
  accent-color: #4a9eff;
}

.option-hint {
  font-size: 11px;
  color: #666;
  line-height: 1.4;
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
