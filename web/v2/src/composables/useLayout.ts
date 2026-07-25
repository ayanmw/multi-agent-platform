import { ref, computed, onMounted, onUnmounted, watch } from 'vue'

/**
 * 响应式布局状态管理 Composable
 *
 * 职责：
 * - 监听窗口宽度，给出 isMobile / isTablet / isDesktop 断点。
 * - 维护桌面端左右 Dock 的开合状态；移动端由 activeMobileTab 决定可见区域。
 * - 维护三栏宽度（左 Dock / 右 Files 栏），支持拖拽调整并写入 localStorage 持久化。
 * - 治理右侧 Dock 区（Files + Cron）的总宽度与主舞台可用宽度：
 *   当主舞台宽度不足时自动收起 Cron / Files，并阻止在不足空间下重新展开。
 * - 提供切换函数并在组件卸载时清理 resize 事件。
 *
 * 使用场景：
 * App.vue / DockPanel.vue / MobileNav.vue 等布局层组件。
 */
export function useLayout() {
  // 断点：与 Tailwind md/lg 对齐（md=768, lg=1024）
  const MOBILE_MAX = 767
  const TABLET_MAX = 1023

  // 布局常量
  const RESIZER_WIDTH = 6
  const CRON_DOCK_WIDTH = 280
  const RIGHT_ZONE_MAX_PERCENT = 0.45
  const RIGHT_ZONE_MAX_ABS = 720
  const STAGE_MIN_WIDTH = 600

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

  /** 桌面/平板端右侧 Cron 面板是否展开 */
  const rightCronOpen = ref(false)

  /** 移动端当前 tab：stage / sessions / files / manage / cron */
  const activeMobileTab = ref<'stage' | 'sessions' | 'files' | 'manage' | 'cron'>('stage')

  /** 移动端 More 菜单是否打开（TopBar 右侧折叠入口） */
  const mobileMoreOpen = ref(false)

  /** 当前激活的移动端 tab 是否应当显示 CommandBar：只有 stage 需要输入条。 */
  const isCommandBarVisible = computed(() => !isMobile.value || activeMobileTab.value === 'stage')

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

  /** 右侧 Dock 区（Files + Cron）总宽度上限（px）。 */
  const rightZoneMax = computed(() =>
    Math.min(Math.floor(windowWidth.value * RIGHT_ZONE_MAX_PERCENT), RIGHT_ZONE_MAX_ABS),
  )

  /** 当前右侧区域实际因展开状态占用的宽度（不含 resizer）。 */
  const rightDockOccupied = computed(() => {
    let w = 0
    if (rightFilesOpen.value) w += rightFilesWidth.value
    if (rightCronOpen.value) w += CRON_DOCK_WIDTH
    return w
  })

  /** 主舞台可用宽度 = 视口宽 - 左 dock + resizer - 右侧占用 - 右侧 resizer */
  const availableStageWidth = computed(() => {
    let used = 0
    if (leftDockOpen.value) used += leftDockWidth.value + RESIZER_WIDTH
    if (rightFilesOpen.value) used += rightFilesWidth.value + RESIZER_WIDTH
    if (rightCronOpen.value) used += CRON_DOCK_WIDTH
    return Math.max(0, windowWidth.value - used)
  })

  /** 是否有足够空间展开 Cron dock（在保持主舞台至少 STAGE_MIN_WIDTH 的前提下）。 */
  const canOpenCron = computed(() => {
    let used = 0
    if (leftDockOpen.value) used += leftDockWidth.value + RESIZER_WIDTH
    if (rightFilesOpen.value) used += rightFilesWidth.value + RESIZER_WIDTH
    return windowWidth.value - used - CRON_DOCK_WIDTH >= STAGE_MIN_WIDTH
  })

  /** 是否有足够空间展开 Files dock。 */
  const canOpenFiles = computed(() => {
    let used = 0
    if (leftDockOpen.value) used += leftDockWidth.value + RESIZER_WIDTH
    if (rightCronOpen.value) used += CRON_DOCK_WIDTH + RESIZER_WIDTH
    return windowWidth.value - used - rightFilesWidth.value >= STAGE_MIN_WIDTH
  })

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

  // === 输入区高度持久化 ===
  // 用户拖拽中栏纵向分隔条后写入 localStorage。
  const STORAGE_KEY_COMMAND_HEIGHT = 'map_v2_command_area_height'
  const MIN_COMMAND = 64
  const MAX_COMMAND_RATIO = 0.4

  function loadCommandHeight(): number {
    const fallback = 88
    if (typeof window === 'undefined') return fallback
    try {
      const raw = window.localStorage.getItem(STORAGE_KEY_COMMAND_HEIGHT)
      const parsed = raw ? Number(raw) : NaN
      if (!Number.isFinite(parsed)) return fallback
      return clamp(parsed, MIN_COMMAND, Math.floor(window.innerHeight * MAX_COMMAND_RATIO))
    } catch {
      return fallback
    }
  }

  const commandAreaHeight = ref<number>(loadCommandHeight())

  function persistCommandHeight(): void {
    if (typeof window === 'undefined') return
    try {
      window.localStorage.setItem(STORAGE_KEY_COMMAND_HEIGHT, String(commandAreaHeight.value))
    } catch {
      // 配额超限静默忽略
    }
  }

  /** 拖拽时更新高度但不落盘 */
  function setCommandAreaHeight(px: number): void {
    const max = Math.floor(window.innerHeight * MAX_COMMAND_RATIO)
    commandAreaHeight.value = clamp(px, MIN_COMMAND, max)
  }

  /** 拖拽结束落盘 */
  function commitCommandHeight(): void {
    persistCommandHeight()
  }

  /** 窗口变化时自动压回上限 */
  function capCommandHeightOnResize(): void {
    const max = Math.floor(window.innerHeight * MAX_COMMAND_RATIO)
    if (commandAreaHeight.value > max) {
      commandAreaHeight.value = max
      persistCommandHeight()
    }
  }

  function updateLayout() {
    if (typeof window !== 'undefined') {
      windowWidth.value = window.innerWidth
      capCommandHeightOnResize()
      autoCollapseRightIfNeeded()
    }
  }

  // 用户展开右侧 dock 时同步检测空间，必要时自动折叠另一侧。
  watch(rightCronOpen, () => autoCollapseRightIfNeeded())
  watch(rightFilesOpen, () => autoCollapseRightIfNeeded())

  function toggleLeftDock() {
    leftDockOpen.value = !leftDockOpen.value
  }

  function toggleRightFiles() {
    rightFilesOpen.value = !rightFilesOpen.value
  }

  /** 切换 Cron dock；若当前空间不足则保持关闭并返回 false。 */
  function toggleRightCron(): boolean {
    const desired = !rightCronOpen.value
    if (!desired) {
      rightCronOpen.value = false
      return true
    }
    if (!canOpenCron.value) return false
    rightCronOpen.value = true
    return true
  }

  /** 设置 Cron dock 显式状态；空间不足时强制设为 false 并返回 false。 */
  function setRightCronOpen(open: boolean): boolean {
    if (!open) {
      rightCronOpen.value = false
      return true
    }
    if (!canOpenCron.value) {
      rightCronOpen.value = false
      return false
    }
    rightCronOpen.value = true
    return true
  }

  /** 窗口变化时自动收起右侧 dock，保证主舞台始终有可用宽度。 */
  function autoCollapseRightIfNeeded() {
    if (!isDesktop.value && !isTablet.value) return
    // 主舞台不足时，先折叠 Cron，再折叠 Files（优先级：Cron 辅助面板优先于文件列表）。
    if (availableStageWidth.value < STAGE_MIN_WIDTH) {
      if (rightCronOpen.value) {
        rightCronOpen.value = false
      } else if (rightFilesOpen.value) {
        rightFilesOpen.value = false
      }
    }
  }

  function setActiveMobileTab(tab: 'stage' | 'sessions' | 'files' | 'manage' | 'cron') {
    activeMobileTab.value = tab
    // 切换 tab 时自动关闭辅助浮层，避免层级堆叠。
    mobileMoreOpen.value = false
  }

  function setMobileMoreOpen(open: boolean) {
    mobileMoreOpen.value = open
  }

  function toggleMobileMore() {
    mobileMoreOpen.value = !mobileMoreOpen.value
  }

  onMounted(() => {
    if (typeof window !== 'undefined') {
      window.addEventListener('resize', updateLayout)
      updateLayout()
    }
  })

  onUnmounted(() => {
    if (typeof window !== 'undefined') {
      window.removeEventListener('resize', updateLayout)
    }
  })

  return {
    windowWidth,
    isMobile,
    isTablet,
    isDesktop,
    leftDockOpen,
    rightFilesOpen,
    rightCronOpen,
    activeMobileTab,
    mobileMoreOpen,
    isCommandBarVisible,
    // 宽度与空间治理
    leftDockWidth,
    rightFilesWidth,
    rightZoneMax,
    rightDockOccupied,
    availableStageWidth,
    canOpenCron,
    canOpenFiles,
    setLeftDockWidth,
    setRightFilesWidth,
    setRightCronOpen,
    commitWidths,
    resetWidths,
    // 输入区高度
    commandAreaHeight,
    setCommandAreaHeight,
    commitCommandHeight,
    capCommandHeightOnResize,
    // 开合切换
    toggleLeftDock,
    toggleRightFiles,
    toggleRightCron,
    setActiveMobileTab,
    setMobileMoreOpen,
    toggleMobileMore,
  }
}
