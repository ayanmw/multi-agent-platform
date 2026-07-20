/**
 * useTheme — 在 obsidian 与 terminal 两套主题色间切换。
 *
 * 优先级：
 * 1. 运行时代码调用 setTheme()
 * 2. localStorage 持久值
 * 3. 默认 'obsidian'
 */
import { ref, watch, nextTick, type Ref } from 'vue'

export type ThemeId = 'obsidian' | 'terminal'

const STORAGE_KEY = 'map_ui_theme_id'

const validThemes = new Set<ThemeId>(['obsidian', 'terminal'])

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

function applyThemeToDOM(theme: ThemeId): void {
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
applyThemeToDOM(initialTheme)

const theme: Ref<ThemeId> = ref(initialTheme)

/**
 * useTheme 返回当前主题与切换函数。
 * setTheme 会立即写入 DOM 并持久化；toggleTheme 在二者间循环。
 */
export function useTheme() {
  const setTheme = (value: ThemeId) => {
    theme.value = value
  }

  const toggleTheme = () => {
    setTheme(theme.value === 'obsidian' ? 'terminal' : 'obsidian')
  }

  watch(
    theme,
    (value) => {
      // 先写属性让浏览器立即重绘，再 storage，避免闪白
      nextTick(() => {
        applyThemeToDOM(value)
        saveTheme(value)
      })
    },
    { immediate: false },
  )

  return {
    theme,
    setTheme,
    toggleTheme,
  }
}
