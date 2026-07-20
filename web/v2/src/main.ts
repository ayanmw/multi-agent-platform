import { createApp } from 'vue'
import App from './App.vue'
import './styles/global.css'
import './styles/responsive.css'

// 在 Vue 挂载之前就恢复主题，避免首屏默认色闪烁。
// 逻辑与 useTheme 内保持一致，这里只做一次性的 DOM 写入。
function restoreThemeOnLoad(): void {
  if (typeof document === 'undefined') return
  const saved = localStorage.getItem('map_ui_theme_id')
  const valid = saved === 'terminal' ? 'terminal' : 'obsidian'
  document.documentElement.setAttribute('data-theme', valid)
}

restoreThemeOnLoad()

createApp(App).mount('#app')
