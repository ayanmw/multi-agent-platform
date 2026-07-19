<script setup lang="ts">
import StatusIndicator from './StatusIndicator.vue'

/**
 * 顶部状态栏
 *
 * props:
 *   - status: 连接状态
 *   - statusLabel: 连接状态文字
 *   - taskStatusLabel: 当前任务状态文字（可选）
 *   - showInspectorToggle: 是否显式提供右侧 Inspector 切换按钮（平板端使用）
 *   - inspectorOpen: 当前 Inspector 浮窗是否打开，用于高亮切换按钮
 *
 * emits:
 *   - toggle-inspector: 请求切换 Inspector 浮窗显隐
 *   - toggle-left-dock: 请求切换左侧 Session Dock（平板端/紧凑模式）
 *   - toggle-recent-mods: 请求打开最近修改弹窗
 *   - toggle-model-prices: 请求打开模型价格管理弹窗
 *   - toggle-mcp: 请求打开 MCP Server 管理弹窗
 *   - toggle-keyboard-tips: 请求打开键盘快捷键提示
 */
withDefaults(
  defineProps<{
    status?: 'idle' | 'running' | 'paused' | 'completed' | 'failed' | 'pending'
    statusLabel?: string
    taskStatusLabel?: string
    showInspectorToggle?: boolean
    inspectorOpen?: boolean
  }>(),
  {
    status: 'idle',
    statusLabel: '离线',
    taskStatusLabel: '',
    showInspectorToggle: false,
    inspectorOpen: false,
  },
)

const emit = defineEmits<{
  (e: 'toggle-inspector'): void
  (e: 'toggle-left-dock'): void
  (e: 'toggle-recent-mods'): void
  (e: 'toggle-model-prices'): void
  (e: 'toggle-mcp'): void
  (e: 'toggle-keyboard-tips'): void
}>()
</script>

<template>
  <header class="topbar">
    <div class="topbar-left">
      <button class="dock-toggle" title="Toggle Sessions" @click="emit('toggle-left-dock')">
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

    <div class="topbar-right">
      <button class="icon-btn" title="MCP Server" @click="emit('toggle-mcp')">🔌</button>
      <button class="icon-btn" title="Recent Mods (Ctrl+M)" @click="emit('toggle-recent-mods')">📝</button>
      <button class="icon-btn" title="Model Prices" @click="emit('toggle-model-prices')">💲</button>
      <button class="icon-btn" title="Keyboard Tips" @click="emit('toggle-keyboard-tips')">⌨</button>
      <button
        v-if="showInspectorToggle"
        class="icon-btn inspector-toggle"
        :class="{ active: inspectorOpen }"
        title="Toggle Inspector flyout"
        @click="emit('toggle-inspector')"
      >
        🧭
      </button>
      <span class="version-switch">v2</span>
    </div>
  </header>
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
}

.icon-btn:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
  border-color: var(--border-default, rgba(255, 255, 255, 0.1));
}

.inspector-toggle.active {
  color: var(--accent-running);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
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
  .icon-btn:nth-of-type(1),
  .icon-btn:nth-of-type(2),
  .icon-btn:nth-of-type(5) {
    display: none;
  }
}
</style>
