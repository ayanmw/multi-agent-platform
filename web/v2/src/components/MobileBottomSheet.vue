<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'

/**
 * MobileBottomSheet — 移动端底部抽屉
 *
 * 特性：
 * - 从底部滑入的遮罩抽屉，默认最大高度 92dvh，圆角顶部。
 * - 点击遮罩、按 Esc、点关闭按钮均可关闭。
 * - 通过 `open` prop 受控；支持默认 slot 和可选 header slot。
 * - 尊重 prefers-reduced-motion，关闭入场/出场动画。
 *
 * Props:
 *   - open: 是否显示
 *   - title: 标题（如不提供 header slot 则不渲染标题区）
 *   - fullScreen: 是否占满全屏（除安全区外），用于 Inspector 等重面板
 *
 * Emits:
 *   - update:open: 显隐状态变化
 */
const props = withDefaults(
  defineProps<{
    open: boolean
    title?: string
    fullScreen?: boolean
  }>(),
  {
    title: '',
    fullScreen: false,
  },
)

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
}>()

function close() {
  emit('update:open', false)
}

function handleOverlayClick() {
  close()
}

function handleKeydown(e: KeyboardEvent) {
  if (props.open && e.key === 'Escape') {
    close()
  }
}

onMounted(() => {
  document.addEventListener('keydown', handleKeydown)
})
onUnmounted(() => {
  document.removeEventListener('keydown', handleKeydown)
})

const panelRef = ref<HTMLElement | null>(null)

// 打开时阻止 body 滚动，避免底层内容随抽屉滚动。
watch(
  () => props.open,
  (isOpen) => {
    if (typeof document === 'undefined') return
    if (isOpen) {
      document.body.classList.add('mobile-sheet-open')
    } else {
      document.body.classList.remove('mobile-sheet-open')
    }
  },
)

onUnmounted(() => {
  if (typeof document !== 'undefined') {
    document.body.classList.remove('mobile-sheet-open')
  }
})
</script>

<template>
  <Teleport to="body">
    <Transition name="mobile-sheet">
      <div
        v-if="open"
        class="mobile-sheet-overlay"
        role="dialog"
        aria-modal="true"
        @click.self="handleOverlayClick"
      >
        <div
          ref="panelRef"
          class="mobile-bottom-sheet"
          :class="{ 'mobile-bottom-sheet--fullscreen': fullScreen }"
        >
          <div class="mobile-sheet-header-sticky">
            <slot name="header">
              <span class="mobile-sheet-title">{{ title }}</span>
            </slot>
            <button
              class="mobile-sheet-close"
              aria-label="关闭"
              @click="close"
            >
              ×
            </button>
          </div>

          <div class="mobile-sheet-body">
            <slot />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.mobile-sheet-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.7);
  backdrop-filter: blur(2px);
  z-index: 100;
  display: flex;
  align-items: flex-end;
  justify-content: center;
}

.mobile-bottom-sheet {
  position: relative;
  width: 100%;
  max-width: 100vw;
  max-height: 92dvh;
  background: var(--bg-elevated, #181c24);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-bottom: none;
  border-radius: 16px 16px 0 0;
  box-shadow: 0 -8px 40px rgba(0, 0, 0, 0.55);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.mobile-bottom-sheet--fullscreen {
  max-height: calc(100dvh - env(safe-area-inset-top, 0px));
  border-radius: 0;
}

.mobile-sheet-header-sticky {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  background: var(--bg-panel, #11141a);
}

/* 当调用方提供 header slot 时，内部元素由 slot 控制；仅保证整体容器布局。 */
.mobile-sheet-header-sticky :slotted(*) {
  min-width: 0;
}

.mobile-sheet-title {
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  font-size: 0.85rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-primary, #e8ebf0);
}

.mobile-sheet-close {
  flex-shrink: 0;
  width: 44px;
  height: 44px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: 8px;
  color: var(--text-secondary, #9aa3b2);
  font-size: 22px;
  line-height: 1;
  cursor: pointer;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
}

.mobile-sheet-close:hover {
  background: var(--bg-hover, #202632);
  color: var(--text-primary, #e8ebf0);
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}

.mobile-sheet-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 12px;
}

.mobile-sheet-enter-active,
.mobile-sheet-leave-active {
  transition: opacity 0.2s ease;
}

.mobile-sheet-enter-active .mobile-bottom-sheet,
.mobile-sheet-leave-active .mobile-bottom-sheet {
  transition: transform 0.25s ease;
}

.mobile-sheet-enter-from,
.mobile-sheet-leave-to {
  opacity: 0;
}

.mobile-sheet-enter-from .mobile-bottom-sheet,
.mobile-sheet-leave-to .mobile-bottom-sheet {
  transform: translateY(100%);
}

@media (prefers-reduced-motion: reduce) {
  .mobile-sheet-enter-active,
  .mobile-sheet-leave-active,
  .mobile-sheet-enter-active .mobile-bottom-sheet,
  .mobile-sheet-leave-active .mobile-bottom-sheet {
    transition-duration: 0.01ms !important;
  }
}
</style>
