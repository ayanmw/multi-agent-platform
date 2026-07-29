<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useLayout } from '../composables/useLayout'
import OptionsFlyout from './OptionsFlyout.vue'
import { useTodoStore } from '@/composables/useTodoStore'
import { useSessionStore } from '@/composables/useSessionStore'
import { useAutoApproval } from '@/composables/useAutoApproval'
import type { SkillPickerItem } from '@/types/skill'

/**
 * 底部命令输入条（TaskInput 的 v2 升级版）。
 *
 * 桌面/平板：作为中栏底部输入区，固定在中栏内，高度可拖拽。
 * 移动端：作为 layout-mobile flex item 被 App.vue 控制显隐；不再使用 fixed 定位。
 *
 * props:
 *   - disabled: 输入框是否禁用
 *   - isRunning: 任务是否运行中，控制 Pause/Resume/Cancel 与进度条
 *   - isPending: 任务是否在启动Pending
 *   - prefill: 外部注入的预填充文本（例如 `/skill-id `）
 *   - contextOpen: Context 浮窗是否打开，用于按钮高亮
 *   - contextAnchorRect: Context 按钮的 DOMRect，用于浮窗定位
 *   - agents: 可选 agent 列表（用于 Options 浮窗）
 *   - availableTools: 可选工具列表（用于 Options 浮窗）
 *
 * emits:
 *   - send(text, {maxSteps, timeoutSeconds, agentId?}): 提交输入
 *   - pause / resume / cancel: 运行控制
 *   - update:multiAgent / multiAgentChange: multi-agent 开关变化
 *   - update:prefill: 预填充消费后重置
 *   - openCases: 打开 Case Library
 *   - update:contextOpen: Context 浮窗显隐切换
 *   - openAgents: 打开 Agents 管理面板
 */
const props = withDefaults(
  defineProps<{
    disabled?: boolean
    isRunning?: boolean
    isPending?: boolean
    prefill?: string
    contextOpen?: boolean
    contextAnchorRect?: DOMRect | null
    agents?: { id: string; name: string; model: string; tools: string[] }[]
    availableTools?: { name: string; description: string }[]
    /** 控制 SkillPicker 是否打开（与 App.vue 同步） */
    pickerOpen?: boolean
    /** SkillPicker 当前选中下标 */
    pickerSelectedIndex?: number
    /** 当前激活任务，用于计算真实进度；不传则使用旧的 38% 占位回退。 */
    activeTask?: {
      status?: string
      maxSteps?: number
      agents?: Record<string, { currentStep?: number; maxSteps?: number; steps?: Array<{ status?: string }> }>
    } | null
  }>(),
  {
    disabled: false,
    isRunning: false,
    isPending: false,
    prefill: '',
    contextOpen: false,
    contextAnchorRect: null,
    agents: () => [],
    availableTools: () => [],
    pickerOpen: false,
    pickerSelectedIndex: 0,
    activeTask: null,
  },
)

const { isMobile } = useLayout()

// TODO 堆积提示：当前 session 存在未完成 TODO 时，在输入条上方显示提示条。
const { activeCount, highPriorityCount } = useTodoStore()
const { activeSession } = useSessionStore()
const todoActiveCount = computed(() => activeSession.value ? activeCount(activeSession.value.id) : 0)
const todoHighPriorityCount = computed(() => activeSession.value ? highPriorityCount(activeSession.value.id) : 0)
const showTodoNotice = computed(() => todoActiveCount.value > 0)

const emit = defineEmits<{
  (e: 'send', text: string, options: { maxSteps: number; timeoutSeconds: number; agentId?: string }): void
  (e: 'pause'): void
  (e: 'resume'): void
  (e: 'cancel'): void
  (e: 'update:multiAgent', value: boolean): void
  (e: 'multiAgentChange', value: boolean): void
  (e: 'update:prefill', value: string): void
  // 打开 Case 窗口。
  (e: 'openCases'): void
  // Context 浮窗切换
  (e: 'update:contextOpen', value: boolean): void
  // 请求打开 Agents 管理面板
  (e: 'openAgents'): void
  // SkillPicker 显隐同步
  (e: 'update:pickerOpen', value: boolean): void
  // SkillPicker 选中下标同步
  (e: 'update:pickerSelectedIndex', value: number): void
  // SkillPicker 选中确认
  (e: 'pickerSelect', item: SkillPickerItem | undefined): void
}>()

const text = ref('')
const optionsOpen = ref(false)
// MAX STEPS / TIMEOUT 持久化到浏览器 localStorage，刷新后保留（同源同浏览器）。
const LS_MAX_STEPS = 'mcp.maxSteps'
const LS_TIMEOUT_SECONDS = 'mcp.timeoutSeconds'
const readLsNumber = (key: string, fallback: number): number => {
  const raw = typeof localStorage !== 'undefined' ? localStorage.getItem(key) : null
  if (raw === null) return fallback
  const n = Number(raw)
  return Number.isFinite(n) ? n : fallback
}
const maxSteps = ref(readLsNumber(LS_MAX_STEPS, 30))
const timeoutSeconds = ref(readLsNumber(LS_TIMEOUT_SECONDS, 0))
const multiAgent = ref(false)
const selectedAgentId = ref<string>('')
const textareaRef = ref<HTMLTextAreaElement | null>(null)
const optionsBtnRef = ref<HTMLElement | null>(null)
const contextBtnRef = ref<HTMLElement | null>(null)
const optionsAnchorRect = ref<DOMRect | null>(null)
const windowHeight = typeof window !== 'undefined' ? window.innerHeight : 0

const { selectedTags: autoApprovalTags } = useAutoApproval()

// 父组件可通过 ref 调用 getContextAnchor 获取 Context 按钮位置。
defineExpose({
  getContextAnchor: () => contextBtnRef.value?.getBoundingClientRect() ?? null,
})

// 快速 options 预设
const stepPresets = [10, 30, 50, 100]
const timeoutPresets = [0, 60, 300, 600]

const progress = computed(() => {
  const task = props.activeTask
  if (!task) return props.isRunning || props.isPending ? 38 : 0

  // 先尝试从 agent_status 聚合的 currentStep / maxSteps 计算整体进度。
  const agents = Object.values(task.agents || {})
  let totalStepsDone = 0
  let totalStepsMax = 0
  for (const a of agents) {
    const max = a.maxSteps ?? task.maxSteps ?? a.steps?.length ?? 0
    const current = a.currentStep ?? a.steps?.length ?? 0
    if (max > 0) {
      totalStepsDone += Math.min(current, max)
      totalStepsMax += max
    }
  }
  if (totalStepsMax > 0) {
    return Math.min(100, Math.round((totalStepsDone / totalStepsMax) * 100))
  }

  // 无步数数据时回退到占位百分比，让用户仍能感知“运行中”。
  if (props.isRunning || props.isPending) return 38
  return 0
})

/** 是否有可靠进度数据：决定进度条是否显示确定性动画还是 indeterminate 脉冲。 */
const hasReliableProgress = computed(() => {
  const task = props.activeTask
  if (!task) return false
  return Object.values(task.agents || {}).some(a =>
    (a.maxSteps && a.maxSteps > 0) ||
    (task.maxSteps && task.maxSteps > 0)
  )
})

function toggleOptions() {
  optionsOpen.value = !optionsOpen.value
  if (optionsOpen.value) {
    nextTick(() => {
      optionsAnchorRect.value = optionsBtnRef.value?.getBoundingClientRect() || null
    })
  }
}

function adjustTextareaHeight() {
  const el = textareaRef.value
  if (!el) return
  el.style.height = 'auto'
  const lineHeight = 20
  const minRows = 1
  const maxRows = 12
  const minHeight = lineHeight * minRows + 22
  const maxHeight = lineHeight * maxRows + 22
  const desired = Math.min(Math.max(el.scrollHeight, minHeight), maxHeight)
  el.style.height = `${desired}px`
}

watch(text, () => {
  nextTick(adjustTextareaHeight)
  // 检测 / 触发 SkillPicker：仅当光标在首段且以 / 开头，普通 // 文本不触发。
  if (textareaRef.value) {
    const val = text.value
    const selStart = textareaRef.value.selectionStart ?? 0
    const selEnd = textareaRef.value.selectionEnd ?? 0
    const firstLineEnd = val.indexOf('\n')
    const inFirstLine = firstLineEnd === -1 || selEnd <= firstLineEnd
    const startsWithSlash = inFirstLine && selStart === 0 && val.startsWith('/')
    const isDoubleSlash = startsWithSlash && val.startsWith('//')
    const shouldShow = startsWithSlash && !isDoubleSlash && !props.isRunning && !props.isPending
    if (shouldShow !== props.pickerOpen) {
      emit('update:pickerOpen', shouldShow)
      if (shouldShow) {
        emit('update:pickerSelectedIndex', 0)
      }
    }
  }
})

function submit() {
  if (props.pickerOpen) {
    emit('pickerSelect', undefined)
    return
  }
  const value = text.value.trim()
  if (!value || props.disabled) return
  emit('send', value, {
    maxSteps: maxSteps.value,
    timeoutSeconds: timeoutSeconds.value,
    agentId: selectedAgentId.value || undefined,
  })
  text.value = ''
  nextTick(adjustTextareaHeight)
  optionsOpen.value = false
  emit('update:pickerOpen', false)
  nextTick(() => textareaRef.value?.focus())
}

function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    submit()
    return
  }
  if (props.pickerOpen) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      emit('update:pickerSelectedIndex', props.pickerSelectedIndex + 1)
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      emit('update:pickerSelectedIndex', Math.max(0, props.pickerSelectedIndex - 1))
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      emit('update:pickerOpen', false)
      return
    }
  }
}

watch(
  () => props.isRunning,
  (running) => {
    if (!running) optionsOpen.value = false
  },
)

watch(multiAgent, (value) => {
  emit('update:multiAgent', value)
  emit('multiAgentChange', value)
})

// 选项变化时落库到 localStorage，确保刷新后保留。
watch(maxSteps, (value) => {
  try { localStorage.setItem(LS_MAX_STEPS, String(value)) } catch { /* 忽略隐私模式等写入失败 */ }
})
watch(timeoutSeconds, (value) => {
  try { localStorage.setItem(LS_TIMEOUT_SECONDS, String(value)) } catch { /* 忽略隐私模式等写入失败 */ }
})

watch(
  () => props.prefill,
  (value) => {
    if (value && value !== text.value) {
      text.value = value
      emit('update:prefill', '')
      nextTick(() => {
        adjustTextareaHeight()
        textareaRef.value?.focus()
      })
    }
  },
)
</script>

<template>
  <div class="command-bar" :class="{ running: isRunning || isPending, 'command-bar--mobile': isMobile }">
    <div v-if="isRunning || isPending" class="progress-strip" :class="{ indeterminate: !hasReliableProgress }">
      <div class="progress-fill" :style="{ width: progress + '%' }" />
    </div>

    <div v-if="showTodoNotice" class="todo-notice" :class="{ 'todo-notice--urgent': todoHighPriorityCount > 0 }">
      <span class="todo-notice-icon">📝</span>
      <span class="todo-notice-text">
        {{ todoActiveCount }} active TODO{{ todoActiveCount > 1 ? 's' : '' }}
        <template v-if="todoHighPriorityCount > 0">
          · {{ todoHighPriorityCount }} high priority
        </template>
      </span>
      <span class="todo-notice-hint">open Manage → TODOs</span>
    </div>

    <div class="command-main">
      <div class="command-left">
        <button
          ref="optionsBtnRef"
          class="options-toggle"
          :class="{ open: optionsOpen }"
          aria-label="Options"
          title="Options"
          @click="toggleOptions"
        >
          ⚙
        </button>

        <button
          ref="contextBtnRef"
          class="options-toggle context-btn"
          :class="{ open: contextOpen }"
          aria-label="打开 Context Window"
          title="打开 Context Window"
          @click.stop="emit('update:contextOpen', !contextOpen)"
        >
          🪟
        </button>
      </div>

      <textarea
        ref="textareaRef"
        v-model="text"
        class="command-input"
        :disabled="disabled"
        placeholder="Type a task... (Ctrl+Enter to send)"
        rows="1"
        @input="adjustTextareaHeight"
        @keydown="handleKeydown"
      />

      <div class="command-right">
        <button
          class="options-toggle cases-btn"
          aria-label="Open Case Library"
          title="Open Case Library"
          @click="emit('openCases')"
        >
          📋
        </button>

        <template v-if="isRunning">
          <button class="control-btn pause" aria-label="Pause" title="Pause" @click="emit('pause')">⏸</button>
          <button class="control-btn cancel" aria-label="Cancel" title="Cancel" @click="emit('cancel')">✕</button>
        </template>
        <template v-else-if="isPending">
          <button class="control-btn pause" aria-label="Pause" title="Pause" disabled>⏸</button>
          <button class="control-btn cancel" aria-label="Cancel" title="Cancel" @click="emit('cancel')">✕</button>
        </template>
        <template v-else>
          <button
            class="send-btn"
            aria-label="Send"
            :disabled="!text.trim() || disabled"
            @click="submit"
          >
            ➤
          </button>
        </template>
      </div>
    </div>

    <OptionsFlyout
      :open="optionsOpen"
      v-model="selectedAgentId"
      :max-steps="maxSteps"
      :timeout-seconds="timeoutSeconds"
      :multi-agent="multiAgent"
      :agents="agents"
      :available-tools="availableTools"
      :anchor-rect="optionsAnchorRect"
      :auto-approval-tags="autoApprovalTags"
      @update:open="optionsOpen = $event"
      @update:maxSteps="maxSteps = $event"
      @update:timeoutSeconds="timeoutSeconds = $event"
      @update:multiAgent="multiAgent = $event"
      @update:autoApprovalTags="autoApprovalTags.splice(0, autoApprovalTags.length, ...$event)"
      @open-agents="emit('openAgents')"
    />
  </div>
</template>

<style scoped>
.command-bar {
  flex-shrink: 0;
  z-index: 35;
  background: var(--bg-panel, #11141a);
  border-top: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  padding: 10px 14px;
  padding-bottom: calc(10px + env(safe-area-inset-bottom, 0px));
  display: flex;
  flex-direction: column;
}

/* 桌面/平板：App.vue 的 .command-area 为 fixed 容器（≥1024px 不需要，≤1023px fixed 覆盖键盘）。 */
@media (min-width: 768px) {
  .command-bar {
    position: static;
    inset: auto;
    height: 100%;
  }
}

.todo-notice {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  min-height: 28px;
  margin: -4px -14px 10px;
  background: rgba(0, 229, 255, 0.08);
  border-top: 1px solid rgba(0, 229, 255, 0.18);
  border-bottom: 1px solid rgba(0, 229, 255, 0.10);
  color: var(--accent-running, #00e5ff);
  font-size: 0.72rem;
  font-family: var(--font-mono, monospace);
  flex-shrink: 0;
}

.todo-notice--urgent {
  background: rgba(255, 82, 82, 0.08);
  border-color: rgba(255, 82, 82, 0.25);
  color: var(--accent-danger, #ff5252);
}

.todo-notice-icon {
  flex-shrink: 0;
}

.todo-notice-text {
  flex: 1;
  min-width: 0;
}

.todo-notice-hint {
  color: var(--text-muted, #5c6675);
  font-size: 0.65rem;
}

.progress-strip {
  position: absolute;
  top: -3px;
  left: 0;
  right: 0;
  height: 3px;
  background: var(--bg-elevated, #181c24);
  overflow: hidden;
}

.progress-fill {
  height: 100%;
  background: var(--accent-running, #00e5ff);
  transition: width 0.3s ease;
  box-shadow: 0 0 8px var(--border-active, rgba(0, 229, 255, 0.4));
}

.progress-strip.indeterminate .progress-fill {
  width: 35% !important;
  animation: indeterminate-slide 1.1s ease-in-out infinite;
}

@keyframes indeterminate-slide {
  0% { transform: translateX(-100%); }
  50% { transform: translateX(calc(200%)); }
  100% { transform: translateX(-100%); }
}

.command-main {
  display: flex;
  align-items: stretch;
  gap: 8px;
  min-height: 42px;
  height: 100%;
}

.command-left,
.command-right {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  align-self: center;
}

.options-toggle {
  width: 32px;
  height: 32px;
  border-radius: 8px;
  background: transparent;
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  color: var(--text-secondary, #9aa3b2);
  cursor: pointer;
  font-size: 14px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
  flex-shrink: 0;
}

.options-toggle:hover,
.options-toggle.open,
.options-toggle.context-btn.open {
  background: var(--bg-hover, #202632);
  color: var(--accent-running, #00e5ff);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}

.command-input {
  flex: 1;
  min-width: 0;
  background: var(--bg-canvas, #0b0d10);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 10px;
  padding: 10px 14px;
  color: var(--text-primary, #e8ebf0);
  font-family: var(--font-mono, monospace);
  font-size: 14px;
  line-height: 1.4;
  resize: none;
  outline: none;
  min-height: 42px;
  height: auto;
  max-height: 100%;
  transition: border-color 0.2s, box-shadow 0.2s;
  overflow-y: auto;
}

.command-input:focus {
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
  box-shadow: 0 0 0 3px var(--glow-focus, rgba(0, 229, 255, 0.08));
}

.command-input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.send-btn,
.control-btn {
  width: 38px;
  height: 38px;
  border-radius: 10px;
  border: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  font-size: 16px;
  flex-shrink: 0;
  transition: background 0.15s, transform 0.1s;
}

.send-btn {
  background: var(--accent-running, #00e5ff);
  color: var(--text-on-accent, #000);
}

.send-btn:hover:not(:disabled) {
  filter: brightness(1.1);
}

.send-btn:disabled {
  background: var(--bg-elevated, #181c24);
  color: var(--text-muted, #5c6675);
  cursor: not-allowed;
}

.control-btn {
  background: var(--bg-elevated, #181c24);
  color: var(--text-secondary, #9aa3b2);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
}

.control-btn:hover:not(:disabled) {
  background: var(--bg-hover, #202632);
  color: var(--text-primary, #e8ebf0);
}

.control-btn.pause {
  color: var(--accent-warning, #ffb800);
}

.control-btn.cancel {
  color: var(--accent-danger, #ff4d4d);
}

@media (max-width: 767px) {
  .command-bar {
    position: static;
    inset: auto;
    border-top: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
    padding: 8px 12px;
    padding-bottom: calc(8px + env(safe-area-inset-bottom, 0px));
  }

  .command-main {
    flex-wrap: wrap;
    gap: 6px;
    min-height: 44px;
    align-items: stretch;
  }

  .command-input {
    min-height: 48px;
    font-size: 16px;
    order: 1;
    flex-basis: 100%;
  }

  .command-left,
  .command-right {
    order: 2;
  }

  .command-right {
    margin-left: auto;
  }

  .command-input::-webkit-scrollbar {
    display: none;
  }

  .options-toggle,
  .send-btn,
  .control-btn {
    min-width: 44px;
    min-height: 44px;
    width: 44px;
    height: 44px;
  }
}
</style>
