/**
 * useTheme — 在 7 套主题色间切换。
 *
 * 支持的主题：obsidian / terminal / amber / dusk / solar / ice / auto。
 * auto 模式下根据系统 prefers-color-scheme 自动映射：
 *   dark -> obsidian
 *   light -> solar
 *
 * 优先级：
 * 1. 运行时代码调用 setTheme()
 * 2. localStorage 持久值
 * 3. 默认 'obsidian'
 */
import { ref, watch, nextTick, onMounted, onUnmounted, type Ref } from 'vue'

export type ThemeId = 'obsidian' | 'terminal' | 'amber' | 'dusk' | 'solar' | 'ice' | 'auto'

export type ConcreteThemeId = Exclude<ThemeId, 'auto'>

const STORAGE_KEY = 'map_ui_theme_id'

const validThemes = new Set<ThemeId>([
  'obsidian',
  'terminal',
  'amber',
  'dusk',
  'solar',
  'ice',
  'auto',
])

function readSavedTheme(): ThemeId | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw && validThemes.has(raw as ThemeId)) {
      return raw as ThemeId
    }
  } catch {
    // storage 不可用则忽略
  }
  return null
}

/**
 * 当 themeId 为 'auto' 时，根据系统 prefers-color-scheme 返回实际主题。
 */
export function resolveEffectiveTheme(themeId: ThemeId): ConcreteThemeId {
  if (themeId !== 'auto') return themeId
  const prefersDark =
    typeof window !== 'undefined' &&
    window.matchMedia('(prefers-color-scheme: dark)').matches
  return prefersDark ? 'obsidian' : 'solar'
}

function applyThemeToDOM(theme: ConcreteThemeId): void {
  if (typeof document === 'undefined') return
  document.documentElement.setAttribute('data-theme', theme)
}

function saveTheme(theme: ThemeId): void {
  try {
    localStorage.setItem(STORAGE_KEY, theme)
  } catch {
    // 忽略 storage 错误
  }
}

const initialTheme = readSavedTheme() || 'obsidian'
const initialEffective = resolveEffectiveTheme(initialTheme)
applyThemeToDOM(initialEffective)

const theme: Ref<ThemeId> = ref(initialTheme)
const effectiveTheme: Ref<ConcreteThemeId> = ref(initialEffective)

/**
 * useTheme 返回当前主题与切换函数。
 * theme 可能是 'auto'；effectiveTheme 是实际应用到 DOM 的 concrete 主题。
 */
export function useTheme() {
  const setTheme = (value: ThemeId) => {
    theme.value = value
  }

  const toggleTheme = () => {
    const order: ConcreteThemeId[] = ['obsidian', 'terminal', 'amber', 'dusk', 'solar', 'ice']
    const idx = order.indexOf(effectiveTheme.value)
    const next = order[(idx + 1) % order.length]
    setTheme(next)
  }

  watch(
    theme,
    (value) => {
      // 先写属性让浏览器立即重绘，再 storage，避免闪白
      nextTick(() => {
        const eff = resolveEffectiveTheme(value)
        effectiveTheme.value = eff
        applyThemeToDOM(eff)
        saveTheme(value)
      })
    },
    { immediate: false },
  )

  // auto 模式下监听系统主题变化，实时切换
  let mediaQuery: MediaQueryList | null = null
  const handleSystemChange = () => {
    if (theme.value === 'auto') {
      const eff = resolveEffectiveTheme('auto')
      effectiveTheme.value = eff
      applyThemeToDOM(eff)
    }
  }

  onMounted(() => {
    if (typeof window !== 'undefined') {
      mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
      mediaQuery.addEventListener('change', handleSystemChange)
    }
  })

  onUnmounted(() => {
    if (mediaQuery) {
      mediaQuery.removeEventListener('change', handleSystemChange)
    }
  })

  return {
    theme,
    effectiveTheme,
    setTheme,
    toggleTheme,
  }
}
