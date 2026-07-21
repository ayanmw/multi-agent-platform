<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted, nextTick, computed } from 'vue'
import { useFlyoutResize } from '@/composables/useFlyoutResize'

/**
 * OptionsFlyout — CommandBar 的 Options 按钮弹出的浮窗面板
 *
 * 职责：
 *   - 聚合运行参数（max steps / timeout / multi-agent）
 *   - 展示当前可用 agent 选择、该 agent 可用工具与 model
 *   - 提供一键跳转到 Agents 管理页面
 *
 * Props:
 *   - open: 浮窗显隐
 *   - maxSteps: 当前最大步数
 *   - timeoutSeconds: 当前超时秒数
 *   - multiAgent: 是否启用多 agent
 *   - agents: 可选 agent 列表
 *   - availableTools: 可选工具列表
 *
 * Emits:
 *   - update:open
 *   - update:maxSteps
 *   - update:timeoutSeconds
 *   - update:multiAgent
 *   - openAgents: 请求打开 Agents 管理面板
 */
const props = defineProps<{
  open: boolean
  maxSteps: number
  timeoutSeconds: number
  multiAgent: boolean
  agents?: { id: string; name: string; model: string; tools: string[] }[]
  availableTools?: { name: string; description: string }[]
  anchorRect?: DOMRect | null
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'update:maxSteps', value: number): void
  (e: 'update:timeoutSeconds', value: number): void
  (e: 'update:multiAgent', value: boolean): void
  (e: 'openAgents'): void
}>()

const panelRef = ref<HTMLElement | null>(null)
const flyoutStyle = ref<Record<string, string>>({})
const maxStepsValue = ref(props.maxSteps)
const timeoutSecondsValue = ref(props.timeoutSeconds)
const multiAgentValue = ref(props.multiAgent)
const selectedAgentId = ref<string>(props.agents?.[0]?.id ?? '')

// === 可调节尺寸 ===
// 浮窗锚定在底部向上展开：顶部手柄上拖调高度，右手柄右拖调宽度。
// size 中为 null 的维度由 CSS max-width/max-height 兜底，按内容自适应。
const { size, isResizing, startResize, resetSize } = useFlyoutResize(
  'map_v2_options_flyout_size',
  { minWidth: 280, maxWidth: 720, minHeight: 220, maxHeightRatio: 0.86 },
  panelRef,
)

function computePosition() {
  const rect = props.anchorRect
  const el = panelRef.value
  if (!rect || !el) return
  const w = size.value.width
  // 自适应时给一个内容驱动的上限，避免横向溢出视口。
  const width = w ?? Math.min(320, window.innerWidth - 24)
  let left = rect.left
  if (left + width > window.innerWidth - 12) {
    left = window.innerWidth - width - 12
  }
  if (left < 12) left = 12
  const bottom = window.innerHeight - rect.top + 8

  flyoutStyle.value = {
    left: `${left}px`,
    bottom: `${bottom}px`,
    maxHeight: `${Math.floor(window.innerHeight * 0.86)}px`,
  }
  if (w != null) flyoutStyle.value.width = `${w}px`
}

watch(() => props.open, (open) => {
  if (open) {
    maxStepsValue.value = props.maxSteps
    timeoutSecondsValue.value = props.timeoutSeconds
    multiAgentValue.value = props.multiAgent
    nextTick(() => {
      if (!selectedAgentId.value && props.agents && props.agents.length > 0) {
        selectedAgentId.value = props.agents[0].id
      }
      computePosition()
    })
  }
})

watch(() => props.anchorRect, () => {
  if (props.open) nextTick(computePosition)
})

// 用户拖拽改变尺寸后重新计算定位，防止宽度变化后越界。
watch(() => size.value.width, () => {
  if (props.open) nextTick(computePosition)
})

watch(maxStepsValue, (v) => emit('update:maxSteps', v))
watch(timeoutSecondsValue, (v) => emit('update:timeoutSeconds', v))
watch(multiAgentValue, (v) => emit('update:multiAgent', v))

const stepPresets = [10, 30, 50, 100]
const timeoutPresets = [0, 60, 300, 600]

const selectedAgent = computed(() => {
  return props.agents?.find(a => a.id === selectedAgentId.value)
})

const agentTools = computed(() => {
  const tools = selectedAgent.value?.tools || []
  if (tools.length === 0) return []
  return tools.map((name) => {
    const info = props.availableTools?.find(t => t.name === name)
    return { name, desc: info?.description || '' }
  })
})

function close() {
  emit('update:open', false)
}

function handleDocClick(e: MouseEvent) {
  if (!props.open || isResizing.value) return
  const target = e.target as Node
  if (panelRef.value && !panelRef.value.contains(target)) {
    close()
  }
}

function handleKeydown(e: KeyboardEvent) {
  if (props.open && e.key === 'Escape') {
    close()
  }
}

function openAgentsPanel() {
  emit('openAgents')
  close()
}

onMounted(() => {
  document.addEventListener('click', handleDocClick, true)
  document.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  document.removeEventListener('click', handleDocClick, true)
  document.removeEventListener('keydown', handleKeydown)
})
</script>

<template>
  <Transition name="options-flyout">
    <div
      v-if="open"
      ref="panelRef"
      class="options-flyout"
      role="dialog"
      aria-label="Task options"
      :style="flyoutStyle"
      :class="{ 'is-resizing': isResizing }"
    >
      <!-- 顶部高度调节手柄：上拖增加高度 -->
      <div
        class="flyout-resize-handle flyout-resize-h"
        title="拖拽调节高度"
        @pointerdown="(e) => startResize(e, 'h')"
      />

      <div class="options-flyout-header">
        <span class="options-title">⚙ Options</span>
        <div class="options-header-actions">
          <button
            class="options-reset"
            :class="{ hidden: size.width == null && size.height == null }"
            title="恢复自适应大小"
            @click="resetSize"
          >⤢</button>
          <button class="options-close" title="关闭" @click="close">×</button>
        </div>
      </div>

      <div class="options-body">
        <div class="option-section">
          <div class="option-group">
            <span class="option-label">Max steps</span>
            <div class="option-pills">
              <button
                v-for="n in stepPresets"
                :key="n"
                class="option-pill"
                :class="{ active: maxStepsValue === n }"
                @click="maxStepsValue = n"
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
                :class="{ active: timeoutSecondsValue === n }"
                @click="timeoutSecondsValue = n"
              >
                {{ n === 0 ? '∞' : n + 's' }}
              </button>
            </div>
          </div>
          <label class="option-toggle">
            <input v-model="multiAgentValue" type="checkbox" />
            <span>Multi-Agent</span>
          </label>
        </div>

        <div class="option-divider" />

        <div class="option-section">
          <div class="agent-section-title">
            <span>Agent</span>
            <button class="agent-manage-link" @click="openAgentsPanel">Manage →</button>
          </div>

          <div v-if="agents && agents.length > 0" class="agent-select-row">
            <select v-model="selectedAgentId" class="agent-select">
              <option v-for="a in agents" :key="a.id" :value="a.id">{{ a.name }}</option>
            </select>
            <div class="agent-model" :title="selectedAgent?.model || ''">
              {{ selectedAgent?.model || '-' }}
            </div>
          </div>
          <div v-else class="agent-empty">
            暂无可用 agent
          </div>

          <div class="tools-list">
            <div v-for="t in agentTools" :key="t.name" class="tool-item" :title="t.desc">
              <span class="tool-dot" />
              <span class="tool-name">{{ t.name }}</span>
            </div>
            <div v-if="agentTools.length === 0 && agents && agents.length > 0" class="tools-empty">
              No tools enabled for this agent
            </div>
          </div>
        </div>
      </div>

      <!-- 右侧宽度调节手柄：右拖增加宽度 -->
      <div
        class="flyout-resize-handle flyout-resize-w"
        title="拖拽调节宽度"
        @pointerdown="(e) => startResize(e, 'w')"
      />
    </div>
  </Transition>
</template>

<style scoped>
.options-flyout {
  position: fixed;
  /* 动态由 JS 计算 left / bottom / width / height */
  display: flex;
  flex-direction: column;
  /* 默认内容自适应：宽度按内容收缩但有 max 兜底，高度由内容增长。 */
  max-width: 720px;
  min-width: 280px;
  background: var(--bg-elevated, #181c24);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 12px;
  box-shadow: 0 14px 44px rgba(0, 0, 0, 0.55);
  overflow: hidden;
  z-index: 50;
  font-family: var(--font-mono, monospace);
}

/* 拖拽期间禁用过渡，避免尺寸跳变迟滞；放大点击关闭判定容差。 */
.options-flyout.is-resizing {
  transition: none !important;
}

.options-flyout.is-resizing * {
  cursor: ns-resize !important;
}

/* 调节手柄：极简的隐形热区，hover 才显形，避免打扰默认外观。 */
.flyout-resize-handle {
  position: absolute;
  z-index: 2;
  user-select: none;
}

.flyout-resize-h {
  top: -3px;
  left: 0;
  right: 0;
  height: 7px;
  cursor: ns-resize;
  display: flex;
  align-items: center;
  justify-content: center;
}

.flyout-resize-h::after {
  content: '';
  width: 32px;
  height: 3px;
  border-radius: 2px;
  background: var(--border-default, rgba(255, 255, 255, 0.18));
  opacity: 0;
  transition: opacity 0.15s;
}

.options-flyout:hover .flyout-resize-h::after {
  opacity: 1;
}

.flyout-resize-w {
  top: 0;
  bottom: 0;
  right: -3px;
  width: 7px;
  cursor: ew-resize;
}

.flyout-resize-w::after {
  content: '';
  position: absolute;
  top: 50%;
  right: 2px;
  transform: translateY(-50%);
  width: 3px;
  height: 32px;
  border-radius: 2px;
  background: var(--border-default, rgba(255, 255, 255, 0.18));
  opacity: 0;
  transition: opacity 0.15s;
}

.options-flyout:hover .flyout-resize-w::after {
  opacity: 1;
}

.options-header-actions {
  display: flex;
  align-items: center;
  gap: 6px;
}

.options-reset {
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 6px;
  color: var(--text-secondary, #9aa3b2);
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
  transition: background 0.15s, color 0.15s, border-color 0.15s, opacity 0.15s;
}

.options-reset.hidden {
  opacity: 0;
  pointer-events: none;
  width: 0;
  padding: 0;
  border: none;
  overflow: hidden;
}

.options-reset:hover {
  background: var(--bg-hover, #202632);
  color: var(--accent-running, #00e5ff);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}

.options-flyout-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  background: var(--bg-panel, #11141a);
}

.options-title {
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  font-size: 0.78rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-primary, #e8ebf0);
}

.options-close {
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 6px;
  color: var(--text-secondary, #9aa3b2);
  font-size: 18px;
  line-height: 1;
  cursor: pointer;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
}

.options-close:hover {
  background: var(--bg-hover, #202632);
  color: var(--text-primary, #e8ebf0);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}

.options-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.option-section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.option-divider {
  height: 1px;
  background: var(--border-default, rgba(255, 255, 255, 0.1));
  margin: 2px 0;
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
  width: 70px;
  flex-shrink: 0;
}

.option-pills {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  flex: 1;
}

.option-pill {
  background: var(--bg-panel, #11141a);
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
}

.option-toggle input[type='checkbox'] {
  accent-color: var(--accent-running, #00e5ff);
}

.agent-section-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  font-size: 0.72rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-primary, #e8ebf0);
}

.agent-manage-link {
  background: transparent;
  border: none;
  color: var(--accent-running, #00e5ff);
  font-size: 0.68rem;
  font-weight: 500;
  cursor: pointer;
  padding: 0;
  font-family: var(--font-mono, monospace);
}

.agent-manage-link:hover {
  text-decoration: underline;
}

.agent-select-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.agent-select {
  flex: 1;
  min-width: 0;
  background: var(--bg-canvas, #0b0d10);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 6px;
  color: var(--text-secondary, #9aa3b2);
  padding: 5px 24px 5px 8px;
  font-size: 0.75rem;
  font-family: var(--font-mono, monospace);
  appearance: none;
  background-image: url("data:image/svg+xml,%3Csvg width='10' height='6' viewBox='0 0 10 6' xmlns='http://www.w3.org/2000/svg'%3E%3Cpath d='M1 1l4 4 4-4' stroke='%239aa3b2' fill='none' stroke-width='1.5'/%3E%3C/svg%3E");
  background-repeat: no-repeat;
  background-position: right 6px center;
  cursor: pointer;
}

.agent-select:focus {
  outline: none;
  border-color: var(--accent-running, #00e5ff);
}

.agent-model {
  flex: 1;
  min-width: 0;
  font-size: 0.72rem;
  color: var(--text-muted, #5c6675);
  font-family: var(--font-mono, monospace);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  text-align: right;
}

.agent-empty,
.tools-empty {
  font-size: 0.72rem;
  color: var(--text-muted, #5c6675);
  text-align: center;
  padding: 8px 0;
}

.tools-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.tool-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  background: var(--bg-panel, #11141a);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.08));
  border-radius: 6px;
  padding: 3px 8px;
  font-size: 0.72rem;
  color: var(--text-secondary, #9aa3b2);
  font-family: var(--font-mono, monospace);
  cursor: default;
}

.tool-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--accent-running, #00e5ff);
}

@media (max-width: 767px) {
  .options-flyout {
    right: 12px !important;
    left: 12px !important;
    width: auto !important;
    bottom: calc(var(--commandbar-height, 64px) + var(--mobile-nav-height, 56px) + 10px) !important;
  }

  /* 移动端空间有限，隐藏手柄与重置按钮，保持全宽自适应。 */
  .flyout-resize-handle {
    display: none;
  }
}

.options-flyout-enter-active,
.options-flyout-leave-active {
  transition: opacity 0.18s ease, transform 0.18s ease;
}

.options-flyout-enter-from,
.options-flyout-leave-to {
  opacity: 0;
  transform: translateY(8px);
}
</style>
