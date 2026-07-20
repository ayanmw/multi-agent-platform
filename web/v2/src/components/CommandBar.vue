<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useLayout } from '../composables/useLayout'

/**
 * 底部命令输入条（TaskInput 的 v2 升级版）
 *
 * props:
 *   - disabled: 输入框是否禁用
 *   - isRunning: 任务是否运行中，控制 Pause/Resume/Cancel 与进度条
 *   - isPending: 任务是否在启动Pending
 *   - prefill: 外部注入的预填充文本（例如 `/skill-id `）
 *
 * emits:
 *   - send(text, {maxSteps, timeoutSeconds}): 提交输入
 *   - pause / resume / cancel: 运行控制
 *   - toggleOptions: 切换 options drawer 显隐状态（可选）
 *   - update:multiAgent / multiAgentChange: multi-agent 开关变化
 *   - update:prefill: 预填充消费后重置
 */
const props = withDefaults(
  defineProps<{
    disabled?: boolean
    isRunning?: boolean
    isPending?: boolean
    prefill?: string
  }>(),
  {
    disabled: false,
    isRunning: false,
    isPending: false,
    prefill: '',
  },
)

const { isMobile } = useLayout()

const emit = defineEmits<{
  (e: 'send', text: string, options: { maxSteps: number; timeoutSeconds: number }): void
  (e: 'pause'): void
  (e: 'resume'): void
  (e: 'cancel'): void
  (e: 'toggleOptions', open: boolean): void
  (e: 'update:multiAgent', value: boolean): void
  (e: 'multiAgentChange', value: boolean): void
  (e: 'update:prefill', value: string): void
  // 打开 Case 窗口。用户希望在发送按钮左侧的 📋 按钮打开 Case Library，
  // 取代原 Inspector 右上角入口，便于在无 task 的空闲态快速挑 Case 跑。
  (e: 'openCases'): void
}>()

const text = ref('')
const optionsOpen = ref(false)
const maxSteps = ref(30)
const timeoutSeconds = ref(0)
const multiAgent = ref(false)
const textareaRef = ref<HTMLTextAreaElement | null>(null)

// 快速 options 预设
const stepPresets = [10, 30, 50, 100]
const timeoutPresets = [0, 60, 300, 600]

const progress = computed(() => {
  // TODO: Phase wire — 接入当前 task 真实 progress，目前作为占位
  if (props.isRunning || props.isPending) return 38
  return 0
})

function toggleOptions() {
  optionsOpen.value = !optionsOpen.value
  emit('toggleOptions', optionsOpen.value)
}

function submit() {
  const value = text.value.trim()
  if (!value || props.disabled) return
  emit('send', value, {
    maxSteps: maxSteps.value,
    timeoutSeconds: timeoutSeconds.value,
  })
  text.value = ''
  // 提交后自动折叠 options，避免遮挡内容
  optionsOpen.value = false
  nextTick(() => textareaRef.value?.focus())
}

function handleKeydown(e: KeyboardEvent) {
  // Enter 换行；Ctrl/Cmd + Enter 发送
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    submit()
  }
}

watch(
  () => props.isRunning,
  (running) => {
    if (!running) optionsOpen.value = false
  },
)

// multi-agent 开关同步到父组件（同时支持 v-model 和事件）
watch(multiAgent, (value) => {
  emit('update:multiAgent', value)
  emit('multiAgentChange', value)
})

// 外部预填充文本：非空且与当前内容不同时写入输入框，并通知父组件已消费
watch(
  () => props.prefill,
  (value) => {
    if (value && value !== text.value) {
      text.value = value
      emit('update:prefill', '')
      nextTick(() => textareaRef.value?.focus())
    }
  },
)
</script>

<template>
  <div class="command-bar" :class="{ running: isRunning || isPending }">
    <div v-if="isRunning || isPending" class="progress-strip">
      <div class="progress-fill" :style="{ width: progress + '%' }" />
    </div>

    <div class="command-main">
      <button
        class="options-toggle"
        :class="{ open: optionsOpen }"
        title="Options"
        @click="toggleOptions"
      >
        ⚙
      </button>

      <textarea
        ref="textareaRef"
        v-model="text"
        class="command-input"
        :disabled="disabled"
        placeholder="Type a task... (Ctrl+Enter to send)"
        rows="1"
        @keydown="handleKeydown"
      />

      <!-- Case 入口：放在发送按钮左侧，点开 Inspector 并直接定位 Cases tab。
           空闲态没有 task 在跑时最常用，所以不论 running/pending/idle 都常驻可见。 -->
      <button
        class="options-toggle cases-btn"
        title="Open Case Library"
        @click="emit('openCases')"
      >
        📋
      </button>

      <template v-if="isRunning">
        <button class="control-btn pause" title="Pause" @click="emit('pause')">⏸</button>
        <button class="control-btn cancel" title="Cancel" @click="emit('cancel')">✕</button>
      </template>
      <template v-else-if="isPending">
        <button class="control-btn pause" title="Pause" disabled>⏸</button>
        <button class="control-btn cancel" title="Cancel" @click="emit('cancel')">✕</button>
      </template>
      <template v-else>
        <button class="send-btn" :disabled="!text.trim() || disabled" @click="submit">
          ➤
        </button>
      </template>
    </div>

    <Transition name="sheet">
      <div
        v-if="optionsOpen"
        class="command-options"
        :class="{ 'command-options--sheet': isMobile }"
        @click.stop
      >
        <div v-if="isMobile" class="sheet-handle" @click="toggleOptions" />
        <div class="option-group">
          <span class="option-label">Max steps</span>
          <div class="option-pills">
            <button
              v-for="n in stepPresets"
              :key="n"
              class="option-pill"
              :class="{ active: maxSteps === n }"
              @click="maxSteps = n"
            >
              {{ n }}
            </button>
          </div>
        </div>
        <div class="option-group">
          <span class="option-label">Timeout</span>
          <div class="option-pills">
            <button
              v-for="n in timeoutPresets"
              :key="n"
              class="option-pill"
              :class="{ active: timeoutSeconds === n }"
              @click="timeoutSeconds = n"
            >
              {{ n === 0 ? '∞' : n + 's' }}
            </button>
          </div>
        </div>
        <label class="option-toggle">
          <input v-model="multiAgent" type="checkbox" />
          <span>Multi-Agent</span>
        </label>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.command-bar {
  position: fixed;
  inset: auto 0 0 0;
  z-index: 35;
  background: var(--bg-panel, #11141a);
  border-top: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  padding: 10px 14px;
  padding-bottom: calc(10px + env(safe-area-inset-bottom, 0px));
  display: flex;
  flex-direction: column;
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
  box-shadow: 0 0 8px rgba(0, 229, 255, 0.4);
}

.command-main {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 42px;
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
.options-toggle.open {
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
  max-height: 160px;
  transition: border-color 0.2s, box-shadow 0.2s;
}

.command-input:focus {
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
  box-shadow: 0 0 0 3px rgba(0, 229, 255, 0.08);
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
  color: #000;
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

.command-options {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  align-items: center;
}

.option-group {
  display: flex;
  align-items: center;
  gap: 8px;
}

.option-label {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-muted, #5c6675);
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
}

.option-pills {
  display: flex;
  gap: 4px;
}

.option-pill {
  background: var(--bg-elevated, #181c24);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  color: var(--text-secondary, #9aa3b2);
  border-radius: 6px;
  padding: 3px 10px;
  font-size: 12px;
  cursor: pointer;
  font-family: var(--font-mono, monospace);
  transition: background 0.15s, color 0.15s, border-color 0.15s;
}

.option-pill:hover,
.option-pill.active {
  background: rgba(0, 229, 255, 0.12);
  color: var(--accent-running, #00e5ff);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}

.option-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-secondary, #9aa3b2);
  cursor: pointer;
  user-select: none;
  margin-left: auto;
}

.option-toggle input[type='checkbox'] {
  accent-color: var(--accent-running, #00e5ff);
}

@media (max-width: 767px) {
  .command-bar {
    bottom: var(--mobile-nav-height, 56px);
    padding: 8px 12px;
    padding-bottom: calc(8px + env(safe-area-inset-bottom, 0px));
  }

  .command-main {
    flex-wrap: wrap;
    gap: 6px;
    min-height: 40px;
  }

  .command-options--sheet {
    position: fixed;
    inset: auto 0 var(--mobile-nav-height, 56px) 0;
    z-index: 45;
    margin-top: 0;
    padding: var(--space-md);
    padding-bottom: calc(14px + env(safe-area-inset-bottom, 0px));
    background: var(--bg-elevated, #181c24);
    border-top: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
    border-bottom: none;
    border-radius: var(--radius-lg) var(--radius-lg) 0 0;
    box-shadow: 0 -4px 30px rgba(0, 0, 0, 0.5);
    flex-direction: column;
    align-items: stretch;
    gap: 14px;
  }

  .sheet-handle {
    width: 36px;
    height: 4px;
    border-radius: 2px;
    background: var(--border-default, rgba(255, 255, 255, 0.1));
    margin: 0 auto;
    cursor: pointer;
  }

  .command-options--sheet .option-group {
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  .command-options--sheet .option-pills {
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .command-options--sheet .option-toggle {
    margin-left: 0;
  }

  .command-input {
    min-height: 48px;
    min-width: 0;
    font-size: 16px; /* prevent iOS zoom */
  }

  .command-input::-webkit-scrollbar {
    display: none;
  }
}

.sheet-enter-active,
.sheet-leave-active {
  transition: transform 240ms ease, opacity 200ms ease;
}

.sheet-enter-from,
.sheet-leave-to {
  transform: translateY(100%);
  opacity: 0;
}
</style>
