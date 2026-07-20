import { createApp } from 'vue'
import App from './App.vue'
import './styles/global.css'
import './styles/responsive.css'
import { resolveEffectiveTheme, type ThemeId } from './composables/useTheme'

// 在 Vue 挂载之前就恢复主题，避免首屏默认色闪烁。
// 逻辑与 useTheme 内保持一致，这里只做一次性的 DOM 写入。
function restoreThemeOnLoad(): void {
  if (typeof document === 'undefined') return
  const saved = localStorage.getItem('map_ui_theme_id')
  const valid = new Set<ThemeId>([
    'obsidian',
    'terminal',
    'amber',
    'dusk',
    'solar',
    'ice',
    'auto',
  ])
  const raw = saved && valid.has(saved as ThemeId) ? (saved as ThemeId) : 'obsidian'
  const eff = resolveEffectiveTheme(raw)
  document.documentElement.setAttribute('data-theme', eff)
}

restoreThemeOnLoad()

createApp(App).mount('#app')
