import { ref, onMounted, onUnmounted, nextTick, watch } from 'vue'

/**
 * useFocusTrap — 模态/抽屉的焦点捕获与键盘关闭
 *
 * 使用场景：Dialog、Modal、BottomSheet 等需要把焦点限制在容器内，并支持 Esc
 * 关闭的浮层。打开时自动聚焦到第一个可聚焦元素（或配置的 initialFocus），关闭
 * 时把焦点恢复到触发元素。
 *
 * @param options.containerRef - 焦点应被限制在其中的容器元素 ref
 * @param options.visible - 当前是否可见；切到 false 时恢复焦点并解绑监听
 * @param options.close - 关闭回调（Esc 或点击容器外部由父级处理时调用）
 * @param options.initialFocus - 可选：打开时应首先聚焦的元素 ref；否则找容器内
 *                               第一个可聚焦元素；都找不到则聚焦容器自身
 * @param options.restoreFocus - 是否关闭时恢复焦点，默认 true
 */
interface UseFocusTrapOptions {
  containerRef: { value: HTMLElement | null }
  visible: { value: boolean }
  close: () => void
  initialFocus?: { value: HTMLElement | null }
  restoreFocus?: boolean
}

// 可聚焦选择器，按 Tab 顺序出现。
const FOCUSABLE_SELECTORS = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
  '[contenteditable]',
].join(', ')

/**
 * 判断元素是否真正可见且可聚焦（排除 hidden / disabled / 尺寸为 0 的情况）。
 */
function isFocusable(el: Element): el is HTMLElement {
  if (!(el instanceof HTMLElement)) return false
  const disabled = (el as HTMLButtonElement | HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement).disabled
  if (disabled) return false
  if (el.getAttribute('tabindex') === '-1') return false
  const rect = el.getBoundingClientRect()
  if (rect.width === 0 || rect.height === 0) return false
  const style = window.getComputedStyle(el)
  if (style.display === 'none' || style.visibility === 'hidden') return false
  return true
}

export function useFocusTrap(options: UseFocusTrapOptions) {
  const restoreTarget = ref<HTMLElement | null>(null)
  let listenerAttached = false

  function getFocusableElements(): HTMLElement[] {
    const container = options.containerRef.value
    if (!container) return []
    return Array.from(container.querySelectorAll(FOCUSABLE_SELECTORS)).filter(isFocusable)
  }

  function setInitialFocus() {
    const container = options.containerRef.value
    if (!container) return

    if (options.initialFocus?.value) {
      options.initialFocus.value.focus()
      return
    }

    const focusables = getFocusableElements()
    if (focusables.length > 0) {
      // 优先聚焦第一个可聚焦元素（通常是关闭按钮或主操作）。
      focusables[0].focus()
    } else {
      container.tabIndex = -1
      container.focus({ preventScroll: true })
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      options.close()
      return
    }

    if (e.key !== 'Tab') return

    const focusables = getFocusableElements()
    if (focusables.length === 0) {
      e.preventDefault()
      return
    }

    const first = focusables[0]
    const last = focusables[focusables.length - 1]
    const active = document.activeElement

    if (e.shiftKey) {
      if (active === first || !containerContains(active)) {
        e.preventDefault()
        last.focus()
      }
    } else {
      if (active === last || !containerContains(active)) {
        e.preventDefault()
        first.focus()
      }
    }
  }

  function containerContains(el: Element | null): boolean {
    const container = options.containerRef.value
    if (!container || !el) return false
    return container === el || container.contains(el)
  }

  function restoreFocus() {
    if (options.restoreFocus === false) return
    const target = restoreTarget.value
    if (target && typeof target.focus === 'function') {
      nextTick(() => target.focus({ preventScroll: true }))
    }
    restoreTarget.value = null
  }

  function attach() {
    if (listenerAttached) return
    restoreTarget.value = document.activeElement as HTMLElement | null
    document.addEventListener('keydown', handleKeydown)
    listenerAttached = true
    nextTick(() => setInitialFocus())
  }

  function detach() {
    if (!listenerAttached) return
    document.removeEventListener('keydown', handleKeydown)
    listenerAttached = false
  }

  onMounted(() => {
    if (options.visible.value) {
      attach()
    }
  })

  onUnmounted(() => {
    detach()
  })

  // 监听 visible 变化：打开时 attach，关闭时 detach 并恢复焦点。
  watch(
    () => options.visible.value,
    (v) => {
      if (v) {
        attach()
      } else {
        detach()
        restoreFocus()
      }
    },
  )

  return {
    attach,
    detach,
    setInitialFocus,
    restoreFocus,
  }
}
