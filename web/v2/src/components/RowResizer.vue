<script setup lang="ts">
/**
 * RowResizer — 主舞台与底部输入区之间的纵向拖拽分隔条
 *
 * props:
 *   - height: 当前输入区高度（仅用于无障碍 aria-valuenow，不双向绑定）
 *
 * emits:
 *   - resize(px): 拖动中持续触发，参数为目标高度
 *   - resize-end(): 拖动结束，调用方据此落盘到 localStorage
 *
 * 实现说明：
 *  - 使用 pointer events + setPointerCapture，兼容鼠标/触屏。
 *  - 拖动期间给 body 加 .row-resizing 类禁用用户选中文本。
 *  - 不在这里 clamp，高度上下限由 App.vue 负责。
 */
const props = defineProps<{
  height: number
}>()

const emit = defineEmits<{
  (e: 'resize', px: number): void
  (e: 'resize-end'): void
}>()

function onPointerDown(e: PointerEvent) {
  e.preventDefault()
  const startY = e.clientY
  const startHeight = props.height
  const target = e.currentTarget as HTMLElement
  target.setPointerCapture(e.pointerId)
  document.body.classList.add('row-resizing')

  function onMove(ev: PointerEvent) {
    const delta = startY - ev.clientY
    const next = startHeight + delta
    emit('resize', Math.round(next))
  }
  function onUp(ev: PointerEvent) {
    target.releasePointerCapture(ev.pointerId)
    document.body.classList.remove('row-resizing')
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
    class="row-resizer"
    role="separator"
    aria-orientation="horizontal"
    :aria-valuenow="height"
    tabindex="0"
    @pointerdown="onPointerDown"
  >
    <div class="resizer-grip" />
  </div>
</template>

<style scoped>
.row-resizer {
  flex-shrink: 0;
  height: 6px;
  cursor: row-resize;
  position: relative;
  background: transparent;
  transition: background 0.15s;
  z-index: 5;
}

.row-resizer:hover,
.row-resizer:focus-visible {
  background: var(--border-active, rgba(0, 229, 255, 0.4));
  outline: none;
}

:global(body.row-resizing) {
  cursor: row-resize;
  user-select: none;
}

:global(body.row-resizing) iframe {
  pointer-events: none;
}

.resizer-grip {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  width: 28px;
  height: 2px;
  border-radius: 2px;
  background: var(--border-default, rgba(255, 255, 255, 0.1));
  transition: background 0.15s, width 0.15s;
}

.row-resizer:hover .resizer-grip {
  background: var(--accent-running, #00e5ff);
  width: 40px;
}
</style>
