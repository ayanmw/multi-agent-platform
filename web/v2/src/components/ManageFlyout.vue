<script setup lang="ts">
import { computed, watch } from 'vue'
import MobileBottomSheet from './MobileBottomSheet.vue'
import { useLayout } from '@/composables/useLayout'

/**
 * ManageFlyout — 管理入口浮窗
 *
 * 桌面/平板：TopBar 右下角 anchored dropdown。
 * 移动端：渲染为底部抽屉（MobileBottomSheet），避免小屏定位失效与越界。
 *
 * Props:
 *   - open: 是否显示
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

const { isMobile } = useLayout()

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

function close() {
  emit('update:open', false)
}

function openTab(tab: string) {
  emit('expand', tab)
  close()
}

function expandAll() {
  emit('expand')
  close()
}

// 桌面端：点击外部关闭浮窗。移动端由 MobileBottomSheet 内部 overlay 处理。
function handleDocClick(e: MouseEvent) {
  if (!props.open || isMobile.value) return
  const target = e.target
  const el = target instanceof Element ? target : (target instanceof Node ? target.parentElement : null)
  if (panelRef.value && !panelRef.value.contains(e.target as Node) && !(el && el.closest('.mobile-sheet-overlay'))) {
    close()
  }
}

import { ref, onMounted, onUnmounted } from 'vue'

const panelRef = ref<HTMLElement | null>(null)
const anchorRef = ref<HTMLElement | null>(null)

onMounted(() => {
  if (!isMobile.value) {
    document.addEventListener('click', handleDocClick, true)
  }
})

onUnmounted(() => {
  if (!isMobile.value) {
    document.removeEventListener('click', handleDocClick, true)
  }
})

// ESC 关闭（桌面端）
function handleKeydown(e: KeyboardEvent) {
  if (props.open && e.key === 'Escape' && !isMobile.value) {
    close()
  }
}

watch(() => props.open, (isOpen) => {
  if (!isMobile.value) {
    if (isOpen) {
      document.addEventListener('keydown', handleKeydown)
    } else {
      document.removeEventListener('keydown', handleKeydown)
    }
  }
})

onUnmounted(() => {
  document.removeEventListener('keydown', handleKeydown)
})
</script>

<template>
  <template v-if="isMobile">
    <MobileBottomSheet
      :open="open"
      title="Manage"
      @update:open="emit('update:open', $event)"
    >
      <div class="manage-bottom-sheet-body">
        <button
          class="manage-expand-bottom"
          aria-label="展开管理"
          @click="expandAll"
        >
          <span>⤢</span>
          <span>展开管理</span>
        </button>
        <div class="manage-flyout-grid manage-flyout-grid--mobile">
          <button
            v-for="item in menuItems"
            :key="item.id"
            class="manage-item"
            :aria-label="item.label"
            @click="openTab(item.id)"
          >
            <span class="manage-item-icon">{{ item.icon }}</span>
            <span class="manage-item-label">{{ item.label }}</span>
          </button>
        </div>
      </div>
    </MobileBottomSheet>
  </template>

  <div v-else ref="anchorRef" class="manage-anchor">
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
          <button class="manage-expand" aria-label="展开管理" title="展开管理" @click="expandAll">
            ⤢ 展开管理
          </button>
        </div>
        <div class="manage-flyout-grid">
          <button
            v-for="item in menuItems"
            :key="item.id"
            class="manage-item"
            role="menuitem"
            :aria-label="item.label"
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

.manage-flyout-grid--mobile {
  grid-template-columns: repeat(3, 1fr);
}

.manage-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  background: var(--bg-panel, #11141a);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.08));
  border-radius: 8px;
  color: var(--text-secondary, #9aa3b2);
  cursor: pointer;
  transition: background 0.15s, color 0.15s, border-color 0.15s, transform 0.1s;
  min-height: 44px;
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
  font-size: 0.78rem;
  font-weight: 500;
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
}

.manage-bottom-sheet-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.manage-expand-bottom {
  align-self: flex-start;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: transparent;
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 8px;
  color: var(--accent-running, #00e5ff);
  padding: 8px 12px;
  font-size: 0.72rem;
  font-weight: 600;
  cursor: pointer;
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
}

.manage-expand-bottom:hover {
  background: rgba(0, 229, 255, 0.1);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}

@media (max-width: 767px) {
  .manage-anchor {
    display: none;
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
