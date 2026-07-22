<script setup lang="ts">
import { ref, watch } from 'vue'

/**
 * ManageFlyout — TopBar 右侧 "管理" 按钮下拉浮窗
 *
 * 设计意图：
 *   将 Inspector 中除 Context 之外的入口（Memory、RAG、Cases、Agents、
 *   Project、Skills、Traces）集中到一个管理菜单。点击任一项目
 *   打开大 Inspector Dialog 并定位到对应 tab。
 *
 * Props:
 *   - open: 是否显示管理浮窗
 *
 * Emits:
 *   - update:open: 浮窗显隐状态变化
 *   - expand: 请求展开管理大 Dialog，可携带初始 tab
 */
const props = defineProps<{
  open: boolean
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'expand', tab?: string): void
}>()

const menuItems = [
  { id: 'memory', label: 'Memory', icon: '🧠' },
  { id: 'rag', label: 'RAG', icon: '📚' },
  { id: 'todos', label: 'TODOs', icon: '📝' },
  { id: 'cases', label: 'Cases', icon: '📋' },
  { id: 'agents', label: 'Agents', icon: '⚙' },
  { id: 'project', label: 'Project', icon: '🏗' },
  { id: 'skills', label: 'Skills', icon: '✨' },
  { id: 'cron', label: 'Cron', icon: '⏰' },
  { id: 'traces', label: 'Traces', icon: '📡' },
] as const

const panelRef = ref<HTMLElement | null>(null)
const anchorRef = ref<HTMLElement | null>(null)

function close() {
  emit('update:open', false)
}

function openTab(tab: string) {
  emit('expand', tab)
  close()
}

function expandAll() {
  // 不指定 tab：由 App.vue 保留上次 inspectorInitialTab（默认 memory）。
  emit('expand')
  close()
}

// 点击外部关闭浮窗
function handleDocClick(e: MouseEvent) {
  const target = e.target as Node
  if (panelRef.value && !panelRef.value.contains(target)) {
    close()
  }
}

// 打开时监听文档点击，关闭时移除；避免在 prop 变化之外还要手动清理。
watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      document.addEventListener('click', handleDocClick, true)
    } else {
      document.removeEventListener('click', handleDocClick, true)
    }
  },
)
</script>

<template>
  <div ref="anchorRef" class="manage-anchor">
    <Transition name="manage-flyout">
      <div
        v-if="open"
        ref="panelRef"
        class="manage-flyout"
        role="menu"
        aria-label="Manage"
      >
        <div class="manage-flyout-header">
          <span class="manage-title">🎛 管理</span>
          <button class="manage-expand" title="展开管理" @click="expandAll">
            ⤢ 展开管理
          </button>
        </div>
        <div class="manage-flyout-grid">
          <button
            v-for="item in menuItems"
            :key="item.id"
            class="manage-item"
            role="menuitem"
            @click="openTab(item.id)"
          >
            <span class="manage-item-icon">{{ item.icon }}</span>
            <span class="manage-item-label">{{ item.label }}</span>
          </button>
        </div>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.manage-anchor {
  position: fixed;
  top: var(--topbar-height, 48px);
  right: 12px;
  z-index: 50;
  pointer-events: none;
}

.manage-flyout {
  position: absolute;
  top: 8px;
  right: 0;
  width: 260px;
  background: var(--bg-elevated, #181c24);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 12px;
  box-shadow: 0 14px 44px rgba(0, 0, 0, 0.55);
  overflow: hidden;
  pointer-events: auto;
  font-family: var(--font-mono, monospace);
}

.manage-flyout-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  background: var(--bg-panel, #11141a);
}

.manage-title {
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  font-size: 0.78rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-primary, #e8ebf0);
}

.manage-expand {
  background: transparent;
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 6px;
  color: var(--accent-running, #00e5ff);
  padding: 3px 8px;
  font-size: 0.68rem;
  font-weight: 600;
  cursor: pointer;
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  transition: background 0.15s, border-color 0.15s;
}

.manage-expand:hover {
  background: rgba(0, 229, 255, 0.1);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}

.manage-flyout-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 6px;
  padding: 10px;
}

.manage-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  background: var(--bg-panel, #11141a);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.08));
  border-radius: 8px;
  color: var(--text-secondary, #9aa3b2);
  cursor: pointer;
  transition: background 0.15s, color 0.15s, border-color 0.15s, transform 0.1s;
}

.manage-item:hover {
  background: var(--bg-hover, #202632);
  color: var(--text-primary, #e8ebf0);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
  transform: translateY(-1px);
}

.manage-item-icon {
  font-size: 0.95rem;
}

.manage-item-label {
  font-size: 0.72rem;
  font-weight: 500;
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
}

@media (max-width: 767px) {
  .manage-flyout {
    right: 0;
    left: 0;
    width: auto;
  }
}

.manage-flyout-enter-active,
.manage-flyout-leave-active {
  transition: opacity 0.18s ease, transform 0.18s ease;
}

.manage-flyout-enter-from,
.manage-flyout-leave-to {
  opacity: 0;
  transform: translateY(8px);
}
</style>
