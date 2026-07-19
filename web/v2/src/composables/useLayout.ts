import { ref, computed, onMounted, onUnmounted } from 'vue'

/**
 * 响应式布局状态管理 Composable
 *
 * 职责：
 * - 监听窗口宽度，给出 isMobile / isTablet / isDesktop 断点。
 * - 维护桌面端左右 Dock 的开合状态；移动端由 activeMobileTab 决定可见区域。
 * - 维护三栏宽度（左 Dock / 右 Files 栏），支持拖拽调整并写入 localStorage 持久化。
 * - 提供切换函数并在组件卸载时清理 resize 事件。
 *
 * 使用场景：
 * App.vue / DockPanel.vue / MobileNav.vue 等布局层组件。
 */
export function useLayout() {
  // 断点：与 Tailwind md/lg 对齐（md=768, lg=1024）
  const MOBILE_MAX = 767
  const TABLET_MAX = 1023

  // SSR 安全：服务端默认按桌面布局渲染
  const windowWidth = ref<number>(
    typeof window !== 'undefined' ? window.innerWidth : TABLET_MAX + 1,
  )

  /** 是否移动端（<768px） */
  const isMobile = computed(() => windowWidth.value <= MOBILE_MAX)

  /** 是否平板端（768px–1023px） */
  const isTablet = computed(
    () => windowWidth.value > MOBILE_MAX && windowWidth.value <= TABLET_MAX,
  )

  /** 是否桌面端（>=1024px） */
  const isDesktop = computed(() => windowWidth.value > TABLET_MAX)

  /** 桌面端左侧面板（Sessions）是否展开 */
  const leftDockOpen = ref(true)

  /** 桌面端右侧 Files 面板是否展开 */
  const rightFilesOpen = ref(true)

  /** 移动端当前 tab：stage / sessions / inspector */
  const activeMobileTab = ref<'stage' | 'sessions' | 'inspector'>('stage')

  // === 三栏宽度持久化 ===
  // 用户拖拽分隔条后会写入 localStorage，下次进入直接还原。
  // 限制在 [MIN, MAX] 之间，避免拖到看不见或挤掉主舞台。
  const STORAGE_KEY_WIDTHS = 'map_v2_column_widths'
  const MIN_LEFT = 200
  const MAX_LEFT = 480
  const MIN_RIGHT = 240
  const MAX_RIGHT = 560

  function clamp(v: number, min: number, max: number): number {
    return Math.min(max, Math.max(min, v))
  }

  function loadWidths(): { left: number; right: number } {
    const fallback = { left: 280, right: 320 }
    if (typeof window === 'undefined') return fallback
    try {
      const raw = window.localStorage.getItem(STORAGE_KEY_WIDTHS)
      if (!raw) return fallback
      const parsed = JSON.parse(raw) as { left?: number; right?: number }
      return {
        left: clamp(typeof parsed.left === 'number' ? parsed.left : fallback.left, MIN_LEFT, MAX_LEFT),
        right: clamp(typeof parsed.right === 'number' ? parsed.right : fallback.right, MIN_RIGHT, MAX_RIGHT),
      }
    } catch {
      return fallback
    }
  }

  const initial = loadWidths()
  /** 左 Dock（Sessions）宽度（px）。 */
  const leftDockWidth = ref<number>(initial.left)
  /** 右 Files 栏宽度（px）。 */
  const rightFilesWidth = ref<number>(initial.right)

  function persistWidths(): void {
    if (typeof window === 'undefined') return
    try {
      window.localStorage.setItem(
        STORAGE_KEY_WIDTHS,
        JSON.stringify({ left: leftDockWidth.value, right: rightFilesWidth.value }),
      )
    } catch {
      // 配额超限静默忽略，宽度仍可在内存中生效。
    }
  }

  /** 拖拽分隔条时由调用方持续调用（pointermove），更新宽度但不落盘。 */
  function setLeftDockWidth(px: number): void {
    leftDockWidth.value = clamp(px, MIN_LEFT, MAX_LEFT)
  }
  function setRightFilesWidth(px: number): void {
    rightFilesWidth.value = clamp(px, MIN_RIGHT, MAX_RIGHT)
  }

  /** 拖拽结束（pointerup）时落盘。 */
  function commitWidths(): void {
    persistWidths()
  }

  /** 一键还原默认宽度。 */
  function resetWidths(): void {
    leftDockWidth.value = 280
    rightFilesWidth.value = 320
    persistWidths()
  }

  function updateWidth() {
    if (typeof window !== 'undefined') {
      windowWidth.value = window.innerWidth
    }
  }

  function toggleLeftDock() {
    leftDockOpen.value = !leftDockOpen.value
  }

  function toggleRightFiles() {
    rightFilesOpen.value = !rightFilesOpen.value
  }

  function setActiveMobileTab(tab: 'stage' | 'sessions' | 'inspector') {
    activeMobileTab.value = tab
  }

  onMounted(() => {
    if (typeof window !== 'undefined') {
      window.addEventListener('resize', updateWidth)
      updateWidth()
    }
  })

  onUnmounted(() => {
    if (typeof window !== 'undefined') {
      window.removeEventListener('resize', updateWidth)
    }
  })

  return {
    windowWidth,
    isMobile,
    isTablet,
    isDesktop,
    leftDockOpen,
    rightFilesOpen,
    activeMobileTab,
    // 宽度与拖拽
    leftDockWidth,
    rightFilesWidth,
    setLeftDockWidth,
    setRightFilesWidth,
    commitWidths,
    resetWidths,
    // 开合切换
    toggleLeftDock,
    toggleRightFiles,
    setActiveMobileTab,
  }
}
