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
       openWorkflowEditor: user clicked workflow editor button
-->
<script setup lang="ts">
import { ref, computed, onMounted, nextTick } from 'vue'
import SkillPicker, { type Skill } from './SkillPicker.vue'

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

const CONTRACT_LIMITS_STORAGE_KEY = 'map_contract_limits'

interface ContractLimits {
  max_steps: number
  max_tokens_per_step: number
  max_timeout_seconds: number
  max_sub_agents: number
  max_input_length: number
  scopes: string[]
}

const DEFAULT_CONTRACT_LIMITS: ContractLimits = {
  max_steps: 200,
  max_tokens_per_step: 8192,
  max_timeout_seconds: 7200,
  max_sub_agents: 5,
  max_input_length: 4000,
  scopes: [],
}

export interface SendOptions {
  maxSteps: number
  timeoutSeconds?: number
  scope?: string
}

const selectedScope = ref('')

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
  openWorkflowEditor: []
}>()

const inputText = ref('')
const showOptions = ref(false)
const maxSteps = ref(DEFAULT_MAX_STEPS)
const timeoutSeconds = ref(DEFAULT_TIMEOUT_SECONDS)
const contractLimits = ref<ContractLimits>(DEFAULT_CONTRACT_LIMITS)

// === Skill Picker 状态 ===
// showSkillPicker: 是否展示悬浮面板。当用户在输入框输入 `/` 触发字符时打开。
// skillQuery: 当前搜索关键词（已剥离前导 `/`）。空字符串表示列出全部。
// skillPickerRef: 持有 SkillPicker 组件实例，用于把键盘事件转发给它。
const showSkillPicker = ref(false)
const skillQuery = ref('')
const skillPickerRef = ref<InstanceType<typeof SkillPicker> | null>(null)
// textarea DOM 引用，用于在 skill 选中后把焦点切回输入框。
const textareaRef = ref<HTMLTextAreaElement | null>(null)

/** 解析当前 textarea 光标位置之前最近的 `/` 触发点。
 *  触发条件：`/` 必须位于行首或前面是空白字符，避免误匹配代码片段里的除法。
 *  返回该 `/` 在文本中的索引，找不到返回 -1。 */
function findSkillTriggerIndex(text: string, caret: number): number {
  // 从光标位置往前找最近的 `/`
  for (let i = caret - 1; i >= 0; i--) {
    const ch = text[i]
    if (ch === '/') {
      // 必须位于行首或前一个字符是空白
      if (i === 0 || /\s/.test(text[i - 1])) {
        return i
      }
      // 否则不是触发点，继续往前找
      continue
    }
    // 遇到换行/空白之外的非 `/` 字符且还没找到 `/`，说明当前段不会触发，停止。
    if (/\s/.test(ch)) {
      // 空白允许继续往前找（用户可能在中间打字）
      continue
    }
    // 遇到非 `/` 非空白字符，停止回溯（这段不可能是 skill 触发）
    break
  }
  return -1
}

/** 根据当前 inputText 与光标位置，决定是否显示/隐藏 skill picker 以及 query。 */
function updateSkillPickerState() {
  const ta = textareaRef.value
  if (!ta) return
  const caret = ta.selectionStart ?? inputText.value.length
  const text = inputText.value
  const triggerIdx = findSkillTriggerIndex(text, caret)
  if (triggerIdx < 0) {
    showSkillPicker.value = false
    skillQuery.value = ''
    return
  }
  // 取 `/` 之后到光标之间的内容作为搜索关键词。
  const q = text.slice(triggerIdx + 1, caret)
  // 如果关键词里出现空白，说明用户已经在 skill 后输入了别的内容，关闭面板。
  if (/\s/.test(q)) {
    showSkillPicker.value = false
    return
  }
  skillQuery.value = q
  showSkillPicker.value = true
}

/** 用户从 picker 选中一个 skill 后，把 `/skill-id ` 填入输入框。
 *  策略：替换从触发 `/` 到光标之间的内容为 `/skill-id `（尾部加空格），
 *  保留用户在 `/` 之前已有的文本和后续内容，并把光标移到空格之后。 */
function handleSkillSelect(skill: Skill) {
  const ta = textareaRef.value
  const caret = ta?.selectionStart ?? inputText.value.length
  const text = inputText.value
  const triggerIdx = findSkillTriggerIndex(text, caret)
  const start = triggerIdx < 0 ? caret : triggerIdx
  const before = text.slice(0, start)
  const after = text.slice(caret)
  const insert = `/${skill.id} `
  inputText.value = before + insert + after
  showSkillPicker.value = false
  skillQuery.value = ''
  // 把焦点切回 textarea 并把光标放到插入文本末尾。
  nextTick(() => {
    if (ta) {
      ta.focus()
      const pos = before.length + insert.length
      ta.setSelectionRange(pos, pos)
    }
  })
}

/** 关闭 picker（Esc 或外部点击）。 */
function handleSkillCancel() {
  showSkillPicker.value = false
  skillQuery.value = ''
}


// Load saved preference on mount so the user's choice survives refreshes.
onMounted(async () => {
  try {
    const savedSteps = localStorage.getItem(MAX_STEPS_STORAGE_KEY)
    if (savedSteps) {
      const n = parseInt(savedSteps, 10)
      if (!Number.isNaN(n) && n >= MIN_STEPS_ALLOWED) {
        maxSteps.value = clampSteps(n)
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

  // Try loading cached contract limits first to avoid waiting for the network.
  try {
    const cached = localStorage.getItem(CONTRACT_LIMITS_STORAGE_KEY)
    if (cached) {
      const parsed = JSON.parse(cached) as Partial<ContractLimits>
      contractLimits.value = { ...DEFAULT_CONTRACT_LIMITS, ...parsed }
    }
  } catch {
    // ignore malformed cache
  }

  try {
    const response = await fetch('/api/contract-limits')
    if (response.ok) {
      const data = (await response.json()) as Partial<ContractLimits>
      contractLimits.value = { ...DEFAULT_CONTRACT_LIMITS, ...data }
      try {
        localStorage.setItem(CONTRACT_LIMITS_STORAGE_KEY, JSON.stringify(contractLimits.value))
      } catch {
        // ignore storage errors
      }
    }
  } catch {
    // Network or parse error: keep default (or cached) limits.
  }
})

// 后端允许的 max_steps 范围，与 API 校验保持一致。
const MIN_STEPS_ALLOWED = 1

const quickSteps = [2, 5, 10, 15, 20, 30, 50, 100, 200]

function clampSteps(n: number): number {
  return Math.max(MIN_STEPS_ALLOWED, Math.min(n, contractLimits.value.max_steps))
}

function handleSend() {
  const text = inputText.value.trim()
  if (!text || props.disabled) return
  const options: SendOptions = { maxSteps: clampSteps(maxSteps.value), timeoutSeconds: timeoutSeconds.value }
  if (selectedScope.value) {
    options.scope = selectedScope.value
  }
  emit('send', text, options)
  inputText.value = ''
}

/** Send on Enter (without Shift). 当 SkillPicker 打开时，Enter / 方向键 / Esc
 *  全部交给 picker 处理，不触发发送。 */
function handleKeydown(e: KeyboardEvent) {
  if (showSkillPicker.value && skillPickerRef.value) {
    const handled = skillPickerRef.value.handleKeydown(e)
    if (handled) return
  }
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    handleSend()
  }
}

/** 监听输入变化，动态判断是否需要弹出/关闭 SkillPicker。 */
function handleInput() {
  updateSkillPickerState()
}

function setMaxSteps(n: number) {
  maxSteps.value = clampSteps(n)
  try {
    localStorage.setItem(MAX_STEPS_STORAGE_KEY, String(maxSteps.value))
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
      <!-- SkillPicker 悬浮在 textarea 上方。visible 由输入文本中的 `/` 触发。 -->
      <SkillPicker
        ref="skillPickerRef"
        :visible="showSkillPicker"
        :query="skillQuery"
        @select="handleSkillSelect"
        @cancel="handleSkillCancel"
      />
      <textarea
        ref="textareaRef"
        v-model="inputText"
        class="input-textarea"
        :disabled="disabled"
        placeholder="Enter your task description... (输入 / 触发 Skill 选择；e.g., 'Write a Python script to analyze a CSV file')"
        rows="2"
        @keydown="handleKeydown"
        @input="handleInput"
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
      <button
        v-if="props.enableMultiAgent"
        class="options-toggle"
        title="Configure multi-agent workflow"
        @click="emit('openWorkflowEditor')"
      >
        🛠 Workflow
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
          :max="contractLimits.max_steps"
          class="steps-slider"
          @change="setMaxSteps(maxSteps)"
        />
        <div class="option-hint">
          Maximum number of ReAct loop iterations. Backend accepted range: {{ MIN_STEPS_ALLOWED }}–{{ contractLimits.max_steps }}.
        </div>
      </div>

      <div v-if="contractLimits.scopes.length > 0" class="option-group">
        <label class="option-label">Scope</label>
        <select v-model="selectedScope" class="scope-select">
          <option value="">Default</option>
          <option v-for="s in contractLimits.scopes" :key="s" :value="s">{{ s }}</option>
        </select>
        <div class="option-hint">
          Restrict file operations to the selected scope. Empty uses the session workspace.
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
