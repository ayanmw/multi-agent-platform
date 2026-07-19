<script setup lang="ts">
/**
 * DockPanel — 可折叠侧边面板容器
 *
 * props:
 *   - side: 'left' | 'right' — 面板位置，决定边框与展开方向视觉
 *   - title: 面板标题
 *   - open: 显式状态，桌面端决定是否渲染
 *
 * slots:
 *   - default: 滚动主体内容
 */
withDefaults(
  defineProps<{
    side?: 'left' | 'right'
    title: string
    open?: boolean
  }>(),
  {
    side: 'left',
    open: true,
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
  background: var(--bg-panel, #11141a);
  border-color: var(--border-default, rgba(255, 255, 255, 0.1));
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
  border-bottom: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  background: var(--bg-elevated, #181c24);
}

.dock-title {
  margin: 0;
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary, #9aa3b2);
  letter-spacing: 0.5px;
  text-transform: uppercase;
}

.dock-close {
  background: transparent;
  border: none;
  color: var(--text-muted, #5c6675);
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
  background: var(--bg-hover, #202632);
  color: var(--text-primary, #e8ebf0);
}

.dock-body {
  flex: 1;
  overflow-y: auto;
  padding: 12px;
}

@media (min-width: 1024px) {
  .dock-panel {
    width: var(--dock-width, 280px);
    min-width: var(--dock-width, 280px);
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
