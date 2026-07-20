<script setup lang="ts">
/**
 * ThemePalette — 悬浮主题切换面板（列表形式，每项带名字与风格基调）。
 *
 * 交互：
 * - 鼠标移入触发按钮展开面板
 * - 点击列表项切换主题并关闭面板
 * - 当前 effectiveTheme 高亮显示
 */
import { ref } from 'vue'
import { useTheme, type ThemeId } from '../composables/useTheme'

interface ThemeMeta {
  id: ThemeId
  label: string
  tone: string
  preview: string // 2 个 CSS 颜色，空格分隔
}

const themes: ThemeMeta[] = [
  { id: 'obsidian', label: '黑曜石舱壁', tone: '深空黑 + 荧光青', preview: '#0b0d10 #00e5ff' },
  { id: 'terminal', label: '终端矩阵', tone: '复古深灰 + 青柠绿', preview: '#0c0e0a #39ff14' },
  { id: 'amber', label: '琥珀 CRT', tone: '深棕底 + 琥珀荧光', preview: '#120f08 #ffb000' },
  { id: 'dusk', label: '暮光洋红', tone: '暗紫舱壁 + 洋红', preview: '#0d0712 #ff69eb' },
  { id: 'solar', label: '日光纸业', tone: '纸白亮底 + 橙红', preview: '#f4f1ea #e05220' },
  { id: 'ice', label: '极地冰川', tone: '深蓝灰 + 冰蓝', preview: '#080e16 #6ec8ff' },
  { id: 'auto', label: '跟随系统', tone: '自动适配 dark / light', preview: '#111111 #f4f1ea' },
]

const { theme, effectiveTheme, setTheme } = useTheme()
const expanded = ref(false)

function select(id: ThemeId) {
  setTheme(id)
  expanded.value = false
}

function previewColors(preview: string): string[] {
  return preview.split(' ')
}

function itemTitle(t: ThemeMeta): string {
  const suffix = t.id === 'auto' ? `（当前：${effectiveTheme.value}）` : ''
  return `${t.label} — ${t.tone}${suffix}`
}
</script>

<template>
  <div
    class="theme-palette"
    @mouseenter="expanded = true"
    @mouseleave="expanded = false"
  >
    <button
      class="palette-trigger icon-btn"
      :class="{ active: expanded }"
      :title="`当前主题：${theme}（生效：${effectiveTheme}）`"
    >
      🎨
    </button>

    <transition name="palette">
      <div v-if="expanded" class="palette-panel">
        <button
          v-for="t in themes"
          :key="t.id"
          class="theme-item"
          :class="{ active: effectiveTheme === t.id }"
          :title="itemTitle(t)"
          @click="select(t.id)"
        >
          <span class="item-swatch" :style="{ background: previewColors(t.preview)[0] }">
            <span
              class="item-accent"
              :style="{ background: previewColors(t.preview)[1] }"
            />
          </span>
          <span class="item-text">
            <span class="item-label">{{ t.label }}</span>
            <span class="item-tone">{{ t.tone }}</span>
          </span>
        </button>
      </div>
    </transition>
  </div>
</template>

<style scoped>
.theme-palette {
  position: relative;
  display: inline-flex;
  align-items: center;
}

.palette-trigger {
  position: relative;
  z-index: 31;
}

.palette-panel {
  position: absolute;
  top: calc(100% + 6px);
  right: 0;
  min-width: 220px;
  max-width: 280px;
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-lg);
  padding: 6px;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.35);
  display: flex;
  flex-direction: column;
  gap: 2px;
  z-index: 32;
}

.theme-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  background: transparent;
  border: 1px solid transparent;
  border-radius: var(--radius-md);
  padding: 6px 8px;
  cursor: pointer;
  color: var(--text-secondary);
  text-align: left;
  transition:
    background var(--transition-fast),
    border-color var(--transition-fast),
    color var(--transition-fast);
}

.theme-item:hover {
  background: var(--bg-hover);
  border-color: var(--border-active);
  color: var(--text-primary);
}

.theme-item.active {
  background: var(--bg-elevated);
  border-color: var(--accent-running);
  color: var(--text-primary);
}

.item-swatch {
  width: 20px;
  height: 20px;
  border-radius: 50%;
  position: relative;
  flex-shrink: 0;
  box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.12);
}

.item-accent {
  position: absolute;
  inset: auto auto -2px -2px;
  width: 9px;
  height: 9px;
  border-radius: 50%;
  border: 2px solid var(--bg-panel);
}

.item-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.item-label {
  font-size: 12px;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.item-tone {
  font-size: 10px;
  opacity: 0.7;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.palette-enter-active,
.palette-leave-active {
  transition: opacity 150ms ease, transform 150ms ease;
}

.palette-enter-from,
.palette-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
