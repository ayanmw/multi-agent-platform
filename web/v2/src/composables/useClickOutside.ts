import { ref, onMounted, onUnmounted, type Ref } from 'vue'

/**
 * 监听元素外部的鼠标按下事件，触发 handler。
 * 常用于 dropdown / popover / picker 等需要外部点击关闭的组件。
 */
export function useClickOutside(handler: () => void): Ref<HTMLElement | null> {
  const elRef = ref<HTMLElement | null>(null)

  function listener(event: MouseEvent) {
    if (elRef.value && !elRef.value.contains(event.target as Node)) {
      handler()
    }
  }

  onMounted(() => document.addEventListener('mousedown', listener))
  onUnmounted(() => document.removeEventListener('mousedown', listener))

  return elRef
}
