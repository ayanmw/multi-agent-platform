<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import ContextWindowPanel from './ContextWindowPanel.vue'
import { useFlyoutResize } from '@/composables/useFlyoutResize'
import { useTaskStore } from '@/composables/useTaskStore'
import type { TaskState } from '@/types/events'
import type { AgentRecord } from '@/composables/useAgentStore'

/**
 * ContextFlyout — 底部输入条右侧弹出的 Context Window 浮窗
 *
 * 设计意图：
 *   将 Context 从 Inspector 大面板中抽出，贴近输入框展示，便于用户
 *   在查看当前任务上下文时不需要先打开 Inspector 大 Dialog。
 *   浮窗内部渲染 ContextWindowPanel，并在顶部显示 session 级统计信息栏
 *   （Task 状态、Agents 数量、Session tokens、Duration）。
 *
 * Props:
 *   - activeTaskId: 当前激活任务 ID
 *   - sessionTotalTokens: 当前 session 总 token 数
 *   - sessionTotalDuration: 当前 session 总耗时（毫秒）
 *   - wsStatus: WebSocket 连接状态
 *   - agents: 当前已加载 agent 列表
 *   - anchorRect: 触发按钮的 DOMRect，用于定位浮窗
 *   - open: 是否显示浮窗
 *
 * Emits:
 *   - update:open: 浮窗显隐状态变化
 */
const props = defineProps<{
  activeTaskId: string
  sessionTotalTokens?: number
  sessionTotalDuration?: number
  wsStatus?: 'connected' | 'connecting' | 'disconnected'
  agents?: AgentRecord[]
  anchorRect?: DOMRect | null
  open: boolean
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
}>()

const { taskCache } = useTaskStore()

const currentTask = computed<TaskState | null>(() => {
  if (!props.activeTaskId) return null
  return taskCache.value[props.activeTaskId] || null
})

/** 当前任务下的 agent 实例列表，用于切换子任务快照 */
const subTaskOptions = computed(() => {
  const task = currentTask.value
  if (!task) return []
  return Object.keys(task.agents || {}).map(agentId => ({
    id: agentId,
    label: task.agents[agentId]?.name || agentId,
  }))
})

const selectedSubTaskId = ref('')

// 任务变化时重置子任务选择，避免展示旧 agent 快照。
watch(
  () => props.activeTaskId,
  () => {
    selectedSubTaskId.value = ''
  },
)

const panelRef = ref<HTMLElement | null>(null)
const flyoutStyle = ref<Record<string, string>>({})

// === 可调节尺寸 ===
// Context 内容通常较长，固定宽度极易截断；提供手柄让用户拖大并持久化。
const { size, isResizing, startResize, resetSize } = useFlyoutResize(
  'map_v2_context_flyout_size',
  { minWidth: 320, maxWidth: 860, minHeight: 280, maxHeightRatio: 0.88 },
  panelRef,
)

function computePosition() {
  const rect = props.anchorRect
  const el = panelRef.value
  if (!rect || !el) return
  const w = size.value.width
  const width = w ?? Math.min(420, window.innerWidth - 24)
  let left = rect.left
  if (left + width > window.innerWidth - 12) {
    left = window.innerWidth - width - 12
  }
  if (left < 12) left = 12
  const bottom = window.innerHeight - rect.top + 8

  flyoutStyle.value = {
    left: `${left}px`,
    bottom: `${bottom}px`,
    maxHeight: `${Math.floor(window.innerHeight * 0.88)}px`,
  }
  if (w != null) flyoutStyle.value.width = `${w}px`
}

watch(() => props.open, (open) => {
  if (open) nextTick(computePosition)
})
watch(() => props.anchorRect, () => {
  if (props.open) nextTick(computePosition)
})
// 用户拖拽改变宽度后重新计算定位，防止越界。
watch(() => size.value.width, () => {
  if (props.open) nextTick(computePosition)
})

function close() {
  emit('update:open', false)
}

// 点击外部或按 Esc 关闭浮窗，提升操作效率。
function handleDocClick(e: MouseEvent) {
  if (!props.open || isResizing.value) return
  const target = e.target as Node | null
  if (!target) return
  // 二级弹窗（如 prompt 详情）通过 Teleport 渲染到 body，不在本浮窗 panelRef 内。
  // 点击二级弹窗（含其内部文字/按钮）时不应误关一级浮窗，否则关闭二级后还要重新打开 Context。
  // target 可能是文本节点（无 closest），先归一到其所属 Element 再判断。
  const el = target instanceof Element ? target : target.parentElement
  if (el && el.closest('.prompt-dialog-overlay')) return
  if (panelRef.value && !panelRef.value.contains(target)) {
    close()
  }
}

function handleKeydown(e: KeyboardEvent) {
  if (props.open && e.key === 'Escape') {
    close()
  }
}

// 任务状态文字。优先取当前 task 的 status，否则根据 wsStatus。
const taskStatusText = computed(() => {
  const t = currentTask.value
  if (t?.status) {
    const s = t.status
    return s === 'completed' ? 'Completed' : s.charAt(0).toUpperCase() + s.slice(1)
  }
  return 'Ready'
})

// 当前会话里的 agent 实例数；没有任务时展示全局已配置 agents 数量。
const agentCountText = computed(() => {
  const taskAgents = currentTask.value ? Object.keys(currentTask.value.agents || {}).length : 0
  if (taskAgents > 0) return String(taskAgents)
  return String(props.agents?.length ?? 0)
})

function formatTokens(n: number): string {
  if (n === 0) return '0'
  if (n < 1000) return String(n)
  return (n / 1000).toFixed(n >= 10000 ? 1 : 2) + 'k'
}

function formatDurationMs(ms: number): string {
  if (ms <= 0) return '0s'
  const totalSeconds = Math.floor(ms / 1000)
  const m = Math.floor(totalSeconds / 60)
  const s = totalSeconds % 60
  if (m === 0) return `${s}s`
  return `${m}m${s.toString().padStart(2, '0')}s`
}

const tokenText = computed(() => formatTokens(props.sessionTotalTokens || 0))
const durationText = computed(() => formatDurationMs(props.sessionTotalDuration || 0))

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
  <Transition name="context-flyout">
    <div
      v-if="open"
      ref="panelRef"
      class="context-flyout"
      role="dialog"
      aria-label="Context Window"
      :style="flyoutStyle"
      :class="{ 'is-resizing': isResizing }"
    >
      <!-- 顶部高度调节手柄：上拖增加高度 -->
      <div
        class="flyout-resize-handle flyout-resize-h"
        title="拖拽调节高度"
        @pointerdown="(e) => startResize(e, 'h')"
      />

      <div class="context-flyout-header">
        <div class="context-flyout-title">
          <span class="context-icon">🪟</span>
          <span>Context Window</span>
        </div>
        <div class="context-flyout-actions">
          <label v-if="subTaskOptions.length > 0" class="context-agent-select">
            <select v-model="selectedSubTaskId" title="选择 Agent 实例">
              <option value="">All / root</option>
              <option v-for="opt in subTaskOptions" :key="opt.id" :value="opt.id">{{ opt.label }}</option>
            </select>
          </label>
          <button
            class="context-reset-btn"
            :class="{ hidden: size.width == null && size.height == null }"
            title="恢复自适应大小"
            @click="resetSize"
          >⤢</button>
          <button class="context-close-btn" title="关闭" @click="close">×</button>
        </div>
      </div>

      <!-- Session 信息栏：Task / Agents / Tokens / Duration -->
      <div class="session-info-bar">
        <div class="info-cell" title="当前任务状态">
          <span class="info-value">{{ taskStatusText }}</span>
          <span class="info-label">Task</span>
        </div>
        <div class="info-cell" title="当前会话中的 Agents 数量">
          <span class="info-value">{{ agentCountText }}</span>
          <span class="info-label">Agents</span>
        </div>
        <div class="info-cell" title="当前会话总 Token 数">
          <span class="info-value">{{ tokenText }}</span>
          <span class="info-label">Tokens</span>
        </div>
        <div class="info-cell" title="当前会话总耗时">
          <span class="info-value">{{ durationText }}</span>
          <span class="info-label">Duration</span>
        </div>
      </div>

      <div class="context-flyout-body">
        <ContextWindowPanel
          :active-task-id="activeTaskId"
          :sub-task-id="selectedSubTaskId"
        />
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
.context-flyout {
  position: fixed;
  /* 动态由 JS 计算 left / bottom / width / height */
  display: flex;
  flex-direction: column;
  /* 默认内容自适应：宽度按内容收缩但有 max 兜底，高度由内容增长。 */
  max-width: 860px;
  min-width: 320px;
  background: var(--bg-elevated, #181c24);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 12px;
  box-shadow: 0 14px 44px rgba(0, 0, 0, 0.55);
  overflow: hidden;
  z-index: 50;
  font-family: var(--font-mono, monospace);
}

/* 拖拽期间禁用过渡，避免尺寸跳变迟滞。 */
.context-flyout.is-resizing {
  transition: none !important;
}

.context-flyout.is-resizing * {
  cursor: ns-resize !important;
}

/* 调节手柄：极简的隐形热区，hover 才显形。 */
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
  width: 36px;
  height: 3px;
  border-radius: 2px;
  background: var(--border-default, rgba(255, 255, 255, 0.18));
  opacity: 0;
  transition: opacity 0.15s;
}

.context-flyout:hover .flyout-resize-h::after {
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
  height: 36px;
  border-radius: 2px;
  background: var(--border-default, rgba(255, 255, 255, 0.18));
  opacity: 0;
  transition: opacity 0.15s;
}

.context-flyout:hover .flyout-resize-w::after {
  opacity: 1;
}

.context-flyout-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  background: var(--bg-panel, #11141a);
}

.context-flyout-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  font-size: 0.78rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-primary, #e8ebf0);
}

.context-icon {
  font-size: 0.9rem;
}

.context-flyout-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.context-agent-select select {
  background: var(--bg-canvas, #0b0d10);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 6px;
  color: var(--text-secondary, #9aa3b2);
  padding: 3px 20px 3px 8px;
  font-size: 0.72rem;
  font-family: var(--font-mono, monospace);
  appearance: none;
  background-image: url("data:image/svg+xml,%3Csvg width='10' height='6' viewBox='0 0 10 6' xmlns='http://www.w3.org/2000/svg'%3E%3Cpath d='M1 1l4 4 4-4' stroke='%239aa3b2' fill='none' stroke-width='1.5'/%3E%3C/svg%3E");
  background-repeat: no-repeat;
  background-position: right 6px center;
  cursor: pointer;
}

.context-agent-select select:focus {
  outline: none;
  border-color: var(--accent-running, #00e5ff);
}

.context-close-btn {
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

.context-reset-btn {
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

.context-reset-btn.hidden {
  opacity: 0;
  pointer-events: none;
  width: 0;
  padding: 0;
  border: none;
  overflow: hidden;
}

.context-reset-btn:hover {
  background: var(--bg-hover, #202632);
  color: var(--accent-running, #00e5ff);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}

.context-close-btn:hover {
  background: var(--bg-hover, #202632);
  color: var(--text-primary, #e8ebf0);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}

.session-info-bar {
  flex-shrink: 0;
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 1px;
  padding: 0;
  background: var(--border-default, rgba(255, 255, 255, 0.1));
  border-bottom: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
}

.info-cell {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  padding: 8px 4px;
  background: var(--bg-panel, #11141a);
  min-width: 0;
}

.info-value {
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--text-primary, #e8ebf0);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 100%;
}

.info-label {
  font-size: 0.65rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted, #5c6675);
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
}

.context-flyout-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.context-flyout-body :deep(.context-panel) {
  border-radius: 0;
}

@media (max-width: 767px) {
  .context-flyout {
    right: 12px !important;
    left: 12px !important;
    width: auto !important;
    bottom: calc(var(--commandbar-height, 64px) + var(--mobile-nav-height, 56px) + 10px) !important;
  }

  .context-flyout-title span:last-child {
    display: none;
  }

  /* 移动端空间有限，隐藏手柄，保持全宽自适应。 */
  .flyout-resize-handle {
    display: none;
  }
}

.context-flyout-enter-active,
.context-flyout-leave-active {
  transition: opacity 0.18s ease, transform 0.18s ease;
}

.context-flyout-enter-from,
.context-flyout-leave-to {
  opacity: 0;
  transform: translateY(8px);
}
</style>
