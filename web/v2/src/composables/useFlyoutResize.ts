import { ref, type Ref } from 'vue'

/**
 * useFlyoutResize — CommandBar 浮窗（Options / Context）的可调节尺寸 Composable
 *
 * 设计意图：
 *   这些浮窗原本用固定宽度 + maxHeight 渲染，内容稍多就会被截断。
 *   此 composable 提供「默认内容自适应 + 用户可拖拽调节 + localStorage 持久化」
 *   的统一能力，供两个 Flyout 复用，保证交互一致。
 *
 * 核心规则：
 *   - width / height 为 null 时表示「自适应」：组件不写入内联尺寸，由 CSS 的
 *     max-width / max-height 兜底，宽度按内容收缩、高度按内容增长。
 *   - 用户一旦拖拽某条边，对应维度写入显式像素值并落盘；下次打开直接还原。
 *   - 浮窗锚定在底部（bottom: Xpx）向上展开，因此「顶部手柄上拖 = 增高」、
 *     「右手柄右拖 = 增宽」。
 *
 * 使用方式：
 *   const rootRef = ref<HTMLElement | null>(null)
 *   const { size, isResizing, startResize, resetSize } = useFlyoutResize(
 *     'map_v2_options_flyout_size',
 *     { minWidth: 280, maxWidth: 720, minHeight: 200, maxHeightRatio: 0.85 },
 *     rootRef,
 *   )
 *
 * 暴露：
 *   - size: { width: number | null; height: number | null }，null 表示自适应
 *   - isResizing: 是否正在拖拽（用于禁用过渡动画 / 屏蔽外部点击关闭）
 *   - startResize(e, dir): 手柄 pointerdown 时调用，dir ∈ 'w' | 'h' | 'wh'
 *   - resetSize: 清空持久化尺寸，回到内容自适应
 */

export interface FlyoutSize {
  /** 宽度（px），null 表示由内容自适应 */
  width: number | null
  /** 高度（px），null 表示由内容自适应 */
  height: number | null
}

export interface FlyoutResizeOptions {
  /** 最小宽度（px） */
  minWidth: number
  /** 最大宽度（px），同时受视口宽度约束 */
  maxWidth: number
  /** 最小高度（px） */
  minHeight: number
  /** 最大高度相对视口高度的比例（0~1） */
  maxHeightRatio: number
}

function clamp(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v))
}

function loadSize(storageKey: string): FlyoutSize {
  if (typeof window === 'undefined') return { width: null, height: null }
  try {
    const raw = window.localStorage.getItem(storageKey)
    if (!raw) return { width: null, height: null }
    const parsed = JSON.parse(raw) as { width?: number | null; height?: number | null }
    return {
      width: typeof parsed.width === 'number' ? parsed.width : null,
      height: typeof parsed.height === 'number' ? parsed.height : null,
    }
  } catch {
    return { width: null, height: null }
  }
}

export function useFlyoutResize(
  storageKey: string,
  opts: FlyoutResizeOptions,
  rootRef: Ref<HTMLElement | null>,
) {
  /** 当前尺寸；null 维度表示自适应，不写入内联样式。 */
  const size = ref<FlyoutSize>(loadSize(storageKey))
  /** 是否正在拖拽，供组件屏蔽过渡 / 外部点击关闭。 */
  const isResizing = ref(false)

  // 拖拽起始快照：起点坐标 + 起点尺寸 + 当前调整方向。
  let startX = 0
  let startY = 0
  let startW = 0
  let startH = 0
  let dir = ''

  function persist(): void {
    if (typeof window === 'undefined') return
    try {
      window.localStorage.setItem(storageKey, JSON.stringify(size.value))
    } catch {
      // 配额超限静默忽略，内存中的尺寸仍生效。
    }
  }

  /**
   * 手柄 pointerdown 入口。
   * 若起始维度为自适应（null），先用元素当前渲染尺寸作为起点，
   * 这样从自适应状态开始拖拽也能平滑过渡到显式尺寸。
   */
  function startResize(e: PointerEvent, direction: 'w' | 'h' | 'wh'): void {
    e.preventDefault()
    e.stopPropagation()
    dir = direction
    isResizing.value = true
    startX = e.clientX
    startY = e.clientY

    const rect = rootRef.value?.getBoundingClientRect()
    startW = size.value.width ?? rect?.width ?? opts.minWidth
    startH = size.value.height ?? rect?.height ?? opts.minHeight

    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }

  function onMove(e: PointerEvent): void {
    const dx = e.clientX - startX
    const dy = e.clientY - startY
    const maxW = Math.min(opts.maxWidth, window.innerWidth - 24)
    const maxH = Math.floor(window.innerHeight * opts.maxHeightRatio)

    if (dir.includes('w')) {
      // 右手柄右拖 → 增宽
      size.value.width = clamp(startW + dx, opts.minWidth, maxW)
    }
    if (dir.includes('h')) {
      // 顶部手柄上拖（dy 为负）→ 增高
      size.value.height = clamp(startH - dy, opts.minHeight, maxH)
    }
  }

  function onUp(): void {
    isResizing.value = false
    window.removeEventListener('pointermove', onMove)
    window.removeEventListener('pointerup', onUp)
    persist()
  }

  /** 清空持久化尺寸，回到内容自适应。 */
  function resetSize(): void {
    size.value = { width: null, height: null }
    persist()
  }

  return {
    size,
    isResizing,
    startResize,
    resetSize,
  }
}
