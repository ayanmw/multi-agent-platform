import { ref, computed, onMounted, onUnmounted } from 'vue'

/**
 * 响应式布局状态管理 Composable
 *
 * 职责：
 * - 监听窗口宽度，给出 isMobile / isTablet / isDesktop 断点。
 * - 维护桌面端左右 Dock 的开合状态；移动端由 activeMobileTab 决定可见区域。
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

  /** 桌面端右侧 Inspector 面板是否展开 */
  const rightInspectorOpen = ref(true)

  /** 移动端当前 tab：stage / sessions / inspector */
  const activeMobileTab = ref<'stage' | 'sessions' | 'inspector'>('stage')

  function updateWidth() {
    if (typeof window !== 'undefined') {
      windowWidth.value = window.innerWidth
    }
  }

  function toggleLeftDock() {
    leftDockOpen.value = !leftDockOpen.value
  }

  function toggleRightInspector() {
    rightInspectorOpen.value = !rightInspectorOpen.value
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
    rightInspectorOpen,
    activeMobileTab,
    toggleLeftDock,
    toggleRightInspector,
    setActiveMobileTab,
  }
}
