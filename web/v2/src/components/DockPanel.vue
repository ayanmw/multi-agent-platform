<script setup lang="ts">
/**
 * DockPanel — 可折叠侧边面板容器
 *
 * props:
 *   - side: 'left' | 'right' — 面板位置，决定边框、折叠三角形与 rail 箭头的朝向
 *   - title: 面板标题
 *   - open: 显式状态。桌面端 open=false 时面板收成一条 18px 细 rail，
 *     rail 上有朝向内容区的三角形可重新展开；open=true 时渲染完整面板。
 *     平板/移动端调用方始终传 open=true 并在外层用 v-if 控制挂载，rail 分支不会触发。
 *   - width: 可选显式宽度（px）。App.vue 通过 --left-w / --right-w 注入 CSS 变量，
 *     这里仅作 fallback。优先级：width prop > CSS 变量 > 默认 --dock-width。
 *
 * emits:
 *   - close: 用户点击折叠三角形，请求收起面板（open → false）
 *   - reopen: 用户点击 rail 上的展开三角形，请求重新展开（open → true）
 *
 * slots:
 *   - default: 滚动主体内容（仅在 open=true 时渲染）
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
  (e: 'reopen'): void
}>()
</script>

<template>
  <!-- 展开态：完整面板，header 右侧是折叠三角形。
       左栏 ◀ 表示"收向左侧"，右栏 ▶ 表示"收向右侧"，方向即隐藏方向。 -->
  <aside
    v-if="open"
    class="dock-panel"
    :class="'dock-' + side"
    :style="width ? { width: width + 'px', minWidth: width + 'px' } : undefined"
  >
    <div class="dock-header">
      <h3 class="dock-title">{{ title }}</h3>
      <button
        class="dock-collapse"
        :title="`Hide ${title}`"
        @click="emit('close')"
      >
        {{ side === 'left' ? '◀' : '▶' }}
      </button>
    </div>
    <div class="dock-body">
      <slot />
    </div>
  </aside>

  <!-- 折叠态：18px 细 rail，点击整条 rail 重新展开。
       箭头朝向内容区（左栏 ▶ 向右展开回主区，右栏 ◀ 向左展开回主区），
       与折叠三角形方向相反，符合"箭头指向展开后出现的位置"的直觉。 -->
  <aside
    v-else
    class="dock-rail"
    :class="'dock-' + side"
    :title="`Show ${title}`"
    @click="emit('reopen')"
  >
    <span class="dock-rail-arrow">{{ side === 'left' ? '▶' : '◀' }}</span>
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

.dock-collapse {
  background: transparent;
  border: none;
  color: var(--text-muted);
  font-size: 14px;
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

.dock-collapse:hover {
  background: var(--bg-hover);
  color: var(--accent-running, #00e5ff);
}

.dock-body {
  flex: 1;
  overflow-y: auto;
  padding: 0;
}

/* 折叠态细 rail：18px 宽，纵向居中一个朝向内容区的三角形。
   左栏贴左边缘、右栏贴右边缘，主区在 rail 之间 flex 撑开。 */
.dock-rail {
  width: 18px;
  min-width: 18px;
  height: 100%;
  background: var(--bg-elevated);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  color: var(--text-muted);
  transition: background 0.15s, color 0.15s;
}

.dock-rail.dock-left {
  border-right: 1px solid var(--border-default);
}

.dock-rail.dock-right {
  border-left: 1px solid var(--border-default);
}

.dock-rail:hover {
  background: var(--bg-hover);
  color: var(--accent-running, #00e5ff);
}

.dock-rail-arrow {
  font-size: 12px;
  line-height: 1;
}

/* 桌面端：宽度由 App.vue 注入的 --left-w / --right-w 决定；未注入时回退到 --dock-width。
   注意 width prop 会通过 inline style 覆盖此处，优先级最高。
   只作用于展开态 .dock-panel —— 折叠态 .dock-rail 始终 18px，否则 min-width 会把
   rail 撑成整栏宽度的空白条，空间让不出来。 */
@media (min-width: 1024px) {
  .dock-panel.dock-left {
    width: var(--left-w, var(--dock-width, 280px));
    min-width: var(--left-w, var(--dock-width, 280px));
  }
  .dock-panel.dock-right {
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
