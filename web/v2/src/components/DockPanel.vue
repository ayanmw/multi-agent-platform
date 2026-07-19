<script setup lang="ts">
/**
 * DockPanel — 可折叠侧边面板容器
 *
 * props:
 *   - side: 'left' | 'right' — 面板位置，决定边框与展开方向视觉
 *   - title: 面板标题
 *   - open: 显式状态，桌面端决定是否渲染
 *   - width: 可选显式宽度（px）。App.vue 通过 --left-w / --right-w 注入 CSS 变量，
 *     这里仅作 fallback。优先级：width prop > CSS 变量 > 默认 --dock-width。
 *
 * slots:
 *   - default: 滚动主体内容
 */
withDefaults(
  defineProps<{
    side?: 'left' | 'right'
    title: string
    open?: boolean
    width?: number
  }>(),
  {
    side: 'left',
    open: true,
    width: 0,
  },
)

const emit = defineEmits<{
  (e: 'close'): void
}>()
</script>

<template>
  <aside
    class="dock-panel"
    :class="['dock-' + side, { 'dock-open': open }]"
    :style="width ? { width: width + 'px', minWidth: width + 'px' } : undefined"
    :aria-hidden="!open"
  >
    <div class="dock-header">
      <h3 class="dock-title">{{ title }}</h3>
      <button class="dock-close" title="Close panel" @click="emit('close')">×</button>
    </div>
    <div class="dock-body">
      <slot />
    </div>
  </aside>
</template>

<style scoped>
.dock-panel {
  display: flex;
  flex-direction: column;
  background: var(--bg-panel);
  border-color: var(--border-default);
  border-style: solid;
  border-width: 0;
  overflow: hidden;
}

.dock-left {
  border-right-width: 1px;
}

.dock-right {
  border-left-width: 1px;
}

.dock-header {
  flex-shrink: 0;
  height: var(--dock-header-height, 40px);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 12px;
  border-bottom: 1px solid var(--border-default);
  background: var(--bg-elevated);
}

.dock-title {
  margin: 0;
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
  letter-spacing: 0.5px;
  text-transform: uppercase;
}

.dock-close {
  background: transparent;
  border: none;
  color: var(--text-muted);
  font-size: 20px;
  line-height: 1;
  cursor: pointer;
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 4px;
  transition: background 0.15s, color 0.15s;
}

.dock-close:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.dock-body {
  flex: 1;
  overflow-y: auto;
  padding: 0;
}

/* 桌面端：宽度由 App.vue 注入的 --left-w / --right-w 决定；未注入时回退到 --dock-width。
   注意 width prop 会通过 inline style 覆盖此处，优先级最高。 */
@media (min-width: 1024px) {
  .dock-left {
    width: var(--left-w, var(--dock-width, 280px));
    min-width: var(--left-w, var(--dock-width, 280px));
  }
  .dock-right {
    width: var(--right-w, var(--inspector-width, 320px));
    min-width: var(--right-w, var(--inspector-width, 320px));
  }
}

/* 平板/移动端：作为 tab 内容全宽展示 */
@media (max-width: 1023px) {
  .dock-panel {
    width: 100%;
    height: 100%;
    min-width: 0;
    border-width: 0;
  }

  .mobile-tab-view .dock-panel {
    position: absolute;
    inset: 0;
    border-radius: 0;
    z-index: 10;
  }
}
</style>
