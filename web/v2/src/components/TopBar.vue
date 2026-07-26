<script setup lang="ts">
import { computed } from 'vue'
import StatusIndicator from './StatusIndicator.vue'
import VersionSwitcher from './VersionSwitcher.vue'
import ThemePalette from './ThemePalette.vue'
import { useTodoStore } from '@/composables/useTodoStore'
import { useSessionStore } from '@/composables/useSessionStore'
import { useLayout } from '@/composables/useLayout'

/**
 * 顶部状态栏
 *
 * props:
 *   - status: 连接状态
 *   - statusLabel: 连接状态文字
 *   - taskStatusLabel: 当前任务状态文字（可选）
 *   - showInspectorToggle: 是否显式提供右侧 Inspector 切换按钮（平板端使用）
 *   - manageOpen: 当前 Manage 下拉浮窗是否打开，用于高亮管理按钮
 *
 * emits:
 *   - toggle-inspector: 请求切换右侧 Inspector
 *   - toggle-left-dock: 请求切换左侧 Session Dock（平板端/紧凑模式）
 *   - toggle-recent-mods: 请求打开最近修改弹窗
 *   - toggle-model-prices: 请求打开 LLM 模型管理弹窗
 *   - toggle-mcp: 请求打开 MCP Server 管理弹窗
 *   - toggle-keyboard-tips: 请求打开键盘快捷键提示
 *   - toggle-manage: 请求切换 Manage 下拉浮窗
 *   - toggle-cron: 请求切换 Cron 侧边面板
 *   - open-mobile-more: 移动端展开 "More" 底部菜单
 */

const props = withDefaults(
  defineProps<{
    status?: 'idle' | 'running' | 'paused' | 'completed' | 'failed' | 'pending'
    statusLabel?: string
    taskStatusLabel?: string
    showInspectorToggle?: boolean
    manageOpen?: boolean
    /** Cron 侧边面板是否展开（用于高亮 TopBar 按钮） */
    cronOpen?: boolean
  }>(),
  {
    status: 'idle',
    statusLabel: '离线',
    taskStatusLabel: '',
    showInspectorToggle: false,
    manageOpen: false,
    cronOpen: false,
  },
)

const emit = defineEmits<{
  (e: 'toggle-inspector'): void
  (e: 'toggle-left-dock'): void
  (e: 'toggle-recent-mods'): void
  (e: 'toggle-model-prices'): void
  (e: 'toggle-mcp'): void
  (e: 'toggle-keyboard-tips'): void
  (e: 'toggle-manage'): void
  (e: 'open-mobile-more'): void
  /** 切换右侧 Cron 侧边面板 */
  (e: 'toggle-cron'): void
}>()

const { isMobile } = useLayout()

// TODO Badge: 展示当前 session 下未完成的 TODO 数量，高优先级数量 >0 时用危险色提示。
const { activeCount, highPriorityCount } = useTodoStore()
const { activeSession } = useSessionStore()
const todoBadgeCount = computed(() => activeSession.value ? activeCount(activeSession.value.id) : 0)
const todoBadgeUrgent = computed(() => activeSession.value ? highPriorityCount(activeSession.value.id) > 0 : false)

// 移动端 "More" 菜单项目：聚合非核心入口。Cron/Manage 已下沉到底部 tab，因此不进 More。
type MoreActionId = 'mcp' | 'mods' | 'prices' | 'keyboard'

const moreItems: { id: string; label: string; icon: string; emit: MoreActionId | null }[] = [
  { id: 'theme', label: 'Theme', icon: '🎨', emit: null },
  { id: 'mcp', label: 'MCP Server', icon: '🔌', emit: 'mcp' },
  { id: 'mods', label: 'Recent Mods', icon: '📝', emit: 'mods' },
  { id: 'prices', label: 'LLM Models', icon: '🧠', emit: 'prices' },
  { id: 'keyboard', label: 'Keyboard Tips', icon: '⌨', emit: 'keyboard' },
]

function emitMore(action: typeof moreItems[number]) {
  if (action.emit) {
    const map: Record<MoreActionId, () => void> = {
      mcp: () => emit('toggle-mcp'),
      mods: () => emit('toggle-recent-mods'),
      prices: () => emit('toggle-model-prices'),
      keyboard: () => emit('toggle-keyboard-tips'),
    }
    map[action.emit]()
  }
  emit('open-mobile-more')
}
</script>

<template>
  <header class="topbar">
    <div class="topbar-left">
      <button
        class="dock-toggle"
        aria-label="Toggle Sessions"
        title="Toggle Sessions"
        @click="emit('toggle-left-dock')"
      >
        ≡
      </button>
      <span class="logo">◈ Multi-Agent Platform</span>
    </div>

    <div class="topbar-center">
      <StatusIndicator :status="status" :label="statusLabel" />
      <span v-if="taskStatusLabel" class="task-badge">
        {{ taskStatusLabel }}
      </span>
    </div>

    <!-- 桌面/平板：保留完整的右侧图标群 -->
    <div v-if="!isMobile" class="topbar-right">
      <ThemePalette />
      <button class="icon-btn" aria-label="MCP Server" title="MCP Server" @click="emit('toggle-mcp')">🔌</button>
      <button class="icon-btn" aria-label="Recent Mods (Ctrl+M)" title="Recent Mods (Ctrl+M)" @click="emit('toggle-recent-mods')">📝</button>
      <button class="icon-btn" aria-label="LLM Models" title="LLM Models" @click="emit('toggle-model-prices')">🧠</button>
      <button class="icon-btn" aria-label="Keyboard Tips" title="Keyboard Tips" @click="emit('toggle-keyboard-tips')">⌨</button>
      <button
        class="icon-btn"
        :class="{ active: cronOpen }"
        aria-label="Cron 定时器侧栏"
        title="Cron 定时器侧栏"
        @click="emit('toggle-cron')"
      >⏰</button>

      <!-- Manage 下拉按钮：位于 TopBar 最右侧，点击弹出管理浮窗 -->
      <button
        class="icon-btn manage-toggle"
        :class="{ active: manageOpen }"
        aria-label="Manage panels"
        title="Manage panels"
        @click="emit('toggle-manage')"
      >
        🎛
        <span
          v-if="todoBadgeCount > 0"
          class="todo-badge"
          :class="{ 'todo-badge--urgent': todoBadgeUrgent }"
          :title="`${todoBadgeCount} active TODO${todoBadgeCount > 1 ? 's' : ''}${todoBadgeUrgent ? ', high priority pending' : ''}`"
        >
          {{ todoBadgeCount }}
        </span>
        <span class="manage-caret">▼</span>
      </button>

      <span class="version-switch">v2</span>
      <VersionSwitcher />
    </div>

    <!-- 移动端：右侧只保留状态与 "More" 入口 -->
    <div v-else class="topbar-right topbar-right--mobile">
      <button
        v-if="todoBadgeCount > 0"
        class="icon-btn manage-toggle"
        :class="{ active: manageOpen }"
        aria-label="Manage panels"
        title="Manage panels"
        @click="emit('toggle-manage')"
      >
        🎛
        <span
          class="todo-badge"
          :class="{ 'todo-badge--urgent': todoBadgeUrgent }"
          :title="`${todoBadgeCount} active TODO${todoBadgeCount > 1 ? 's' : ''}${todoBadgeUrgent ? ', high priority pending' : ''}`"
        >
          {{ todoBadgeCount }}
        </span>
      </button>

      <button
        class="icon-btn more-toggle"
        aria-label="More options"
        title="More options"
        @click="emit('open-mobile-more')"
      >
        ⋮
      </button>
    </div>
  </header>

  <!-- 移动端 "More" 底部面板（bottom sheet），由 App.vue 注入到 Teleport -->
</template>

<style scoped>
.topbar {
  position: fixed;
  inset: 0 0 auto 0;
  height: var(--topbar-height, 48px);
  flex-shrink: 0;
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0 16px;
  background: var(--bg-panel);
  border-bottom: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  z-index: 30;
}

.topbar-left,
.topbar-center,
.topbar-right {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.dock-toggle {
  display: none;
  background: transparent;
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  color: var(--text-secondary);
  border-radius: 6px;
  width: 28px;
  height: 28px;
  cursor: pointer;
  font-size: 16px;
  line-height: 1;
}

.dock-toggle:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.logo {
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  font-size: 16px;
  font-weight: 600;
  color: var(--text-primary);
  letter-spacing: 0.3px;
  white-space: nowrap;
}

.topbar-center {
  position: absolute;
  left: 50%;
  transform: translateX(-50%);
  gap: 12px;
}

.task-badge {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 10px;
  background: var(--bg-elevated);
  color: var(--text-secondary);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  white-space: nowrap;
}

.icon-btn {
  background: transparent;
  border: 1px solid transparent;
  color: var(--text-secondary);
  border-radius: 6px;
  width: 30px;
  height: 30px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  font-size: 14px;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
  position: relative;
}

.icon-btn:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
  border-color: var(--border-default, rgba(255, 255, 255, 0.1));
}

.manage-toggle {
  width: auto;
  padding: 0 8px;
  gap: 3px;
  position: relative;
}

.todo-badge {
  position: absolute;
  top: -5px;
  right: -5px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: 8px;
  background: var(--accent-running);
  color: var(--text-on-accent);
  font-family: var(--font-mono);
  font-size: 0.65rem;
  font-weight: 700;
  line-height: 16px;
  text-align: center;
  pointer-events: none;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.35);
}

.todo-badge--urgent {
  background: var(--accent-danger);
  color: #fff;
}

.manage-toggle.active {
  color: var(--accent-running);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}

.manage-caret {
  font-size: 9px;
  opacity: 0.7;
}

.version-switch {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 10px;
  background: var(--bg-elevated);
  color: var(--text-muted);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  margin-left: 4px;
}

@media (max-width: 1023px) {
  .topbar {
    padding: 0 12px;
  }
  .topbar-center {
    position: static;
    transform: none;
    margin-left: auto;
    margin-right: 8px;
  }
  .dock-toggle {
    display: inline-flex;
    margin-right: 8px;
  }
}

@media (max-width: 767px) {
  .logo {
    font-size: 14px;
  }
  .task-badge,
  .version-switch {
    display: none;
  }

  .topbar-right--mobile {
    gap: 6px;
    flex-shrink: 0;
    margin-left: 8px;
  }

  .topbar-right--mobile .icon-btn,
  .topbar-right--mobile .manage-toggle,
  .topbar-right--mobile .more-toggle {
    min-width: 44px;
    min-height: 44px;
    width: 44px;
    height: 44px;
    font-size: 18px;
  }

  .topbar-right--mobile .manage-toggle {
    padding: 0;
  }

  .topbar-right--mobile .todo-badge {
    top: 2px;
    right: 2px;
  }

  .topbar-left {
    gap: 6px;
    min-width: 0;
    overflow: hidden;
  }
}
</style>
