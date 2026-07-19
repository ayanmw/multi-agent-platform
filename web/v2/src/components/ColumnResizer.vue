<script setup lang="ts">
/**
 * ColumnResizer — 三栏之间的拖拽分隔条
 *
 * props:
 *   - side: 'left' | 'right'
 *       'left'  → 左 Dock 右边缘的分隔条，拖动改变左 Dock 宽度
 *       'right' → 右 Files 栏左边缘的分隔条，拖动改变右栏宽度
 *   - width: 当前宽度（仅用于无障碍 aria-valuenow，不双向绑定）
 *
 * emits:
 *   - resize(px): 拖动中持续触发，参数为目标宽度
 *   - resize-end(): 拖动结束，调用方据此落盘到 localStorage
 *
 * 实现说明：
 *  - 使用 pointer events（pointerdown/move/up）+ setPointerCapture，兼容鼠标/触屏。
 *  - 拖动期间给 body 加 .column-resizing 类禁用用户选中文本与 iframe 指针事件，
 *    避免拖过 TimelineTrack 时误触发卡片交互。
 *  - 不在这里 clamp，宽度上下限由 useLayout 负责，resizer 只透传原始像素。
 */
const props = defineProps<{
  side: 'left' | 'right'
  width: number
}>()

const emit = defineEmits<{
  (e: 'resize', px: number): void
  (e: 'resize-end'): void
}>()

function onPointerDown(e: PointerEvent) {
  e.preventDefault()
  const startX = e.clientX
  const startWidth = props.width
  const target = e.currentTarget as HTMLElement
  target.setPointerCapture(e.pointerId)
  document.body.classList.add('column-resizing')

  function onMove(ev: PointerEvent) {
    const delta = ev.clientX - startX
    // 左分隔条：向右拖增大宽度；右分隔条：向左拖（delta 负）增大宽度。
    const next = props.side === 'left' ? startWidth + delta : startWidth - delta
    emit('resize', Math.round(next))
  }
  function onUp(ev: PointerEvent) {
    target.releasePointerCapture(ev.pointerId)
    document.body.classList.remove('column-resizing')
    window.removeEventListener('pointermove', onMove)
    window.removeEventListener('pointerup', onUp)
    emit('resize-end')
  }
  window.addEventListener('pointermove', onMove)
  window.addEventListener('pointerup', onUp)
}
</script>

<template>
  <div
    class="column-resizer"
    :class="['resizer-' + side]"
    role="separator"
    :aria-orientation="'vertical'"
    :aria-valuenow="width"
    tabindex="0"
    @pointerdown="onPointerDown"
  >
    <div class="resizer-grip" />
  </div>
</template>

<style scoped>
.column-resizer {
  flex-shrink: 0;
  width: 6px;
  cursor: col-resize;
  position: relative;
  background: transparent;
  transition: background 0.15s;
  z-index: 5;
}

.column-resizer:hover,
.column-resizer:focus-visible {
  background: var(--border-active, rgba(0, 229, 255, 0.4));
  outline: none;
}

/* 拖拽中（body.column-resizing 由 JS 加上）放大命中区，避免快速拖动脱手 */
:global(body.column-resizing) {
  cursor: col-resize;
  user-select: none;
}

:global(body.column-resizing) iframe {
  pointer-events: none;
}

.resizer-grip {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  width: 2px;
  height: 28px;
  border-radius: 2px;
  background: var(--border-default, rgba(255, 255, 255, 0.1));
  transition: background 0.15s, height 0.15s;
}

.column-resizer:hover .resizer-grip {
  background: var(--accent-running, #00e5ff);
  height: 40px;
}
</style>
