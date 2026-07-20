<script setup lang="ts">
/**
 * ThemePalette — 悬浮主题切换面板。
 *
 * 特点：
 * - 3 种形态：bar（水平色块条）、grid（网格卡片）、radar（扇形展开）
 * - 点击主按钮循环切换形态；hover 主按钮自动展开面板
 * - 每个主题支持 title tooltip：名字 + 风格基调
 * - 当前 effectiveTheme 高亮显示
 */
import { ref, computed } from 'vue'
import { useTheme, type ThemeId } from '../composables/useTheme'

type PanelShape = 'bar' | 'grid' | 'radar'

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
const shape = ref<PanelShape>('bar')

const shapeIcon = computed(() =>
  ({
    bar: '▬',
    grid: '▦',
    radar: '◈',
  }[shape.value]),
)

const shapeCycler: PanelShape[] = ['bar', 'grid', 'radar']

function cycleShape() {
  const idx = shapeCycler.indexOf(shape.value)
  shape.value = shapeCycler[(idx + 1) % shapeCycler.length]
}

function select(id: ThemeId) {
  setTheme(id)
  expanded.value = false
}

function previewColors(preview: string): string[] {
  return preview.split(' ')
}

function chipTitle(t: ThemeMeta): string {
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
      @click="cycleShape"
    >
      <span class="trigger-emoji">🎨</span>
      <span class="trigger-shape">{{ shapeIcon }}</span>
    </button>

    <transition name="palette">
      <div v-if="expanded" class="palette-panel" :class="`shape-${shape}`">
        <button
          v-for="t in themes"
          :key="t.id"
          class="theme-chip"
          :class="{ active: effectiveTheme === t.id }"
          :title="chipTitle(t)"
          @click="select(t.id)"
        >
          <span class="chip-swatch" :style="{ background: previewColors(t.preview)[0] }">
            <span
              class="chip-accent"
              :style="{ background: previewColors(t.preview)[1] }"
            />
          </span>
          <span v-if="shape === 'grid'" class="chip-label">{{ t.label }}</span>
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
  display: inline-flex;
  align-items: center;
  gap: 2px;
  position: relative;
  z-index: 31;
}

.trigger-shape {
  font-size: 9px;
  opacity: 0.7;
}

.palette-panel {
  position: absolute;
  top: calc(100% + 6px);
  right: 0;
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-lg);
  padding: 8px;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.35);
  display: flex;
  gap: 8px;
  z-index: 32;
}

.palette-panel.shape-bar {
  flex-direction: row;
}

.palette-panel.shape-grid {
  width: 220px;
  flex-wrap: wrap;
}

.palette-panel.shape-radar {
  --radius: 92px;
  width: 0;
  height: 0;
  padding: 0;
  background: transparent;
  border: none;
  box-shadow: none;
}

.theme-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  padding: 4px 6px;
  cursor: pointer;
  color: var(--text-secondary);
  transition:
    border-color var(--transition-fast),
    background var(--transition-fast),
    transform var(--transition-fast);
}

.theme-chip:hover {
  border-color: var(--border-active);
  background: var(--bg-hover);
  color: var(--text-primary);
  transform: translateY(-1px);
}

.theme-chip.active {
  border-color: var(--accent-running);
  box-shadow: 0 0 0 2px var(--glow-focus);
}

.chip-swatch {
  width: 18px;
  height: 18px;
  border-radius: 50%;
  position: relative;
  display: inline-block;
  box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.1);
}

.chip-accent {
  position: absolute;
  inset: auto auto -2px -2px;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  border: 2px solid var(--bg-panel);
}

.shape-grid .theme-chip {
  width: calc(50% - 4px);
  justify-content: flex-start;
}

.shape-grid .chip-label {
  font-size: 11px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Radar：主题气泡围绕触发按钮扇形展开 */
.palette-panel.shape-radar .theme-chip {
  position: absolute;
  right: 50%;
  top: 50%;
  margin-top: -14px;
  margin-right: -14px;
  transform-origin: center center;
}

.palette-panel.shape-radar .theme-chip:nth-child(1) { transform: rotate(-72deg) translateX(var(--radius)) rotate(72deg); }
.palette-panel.shape-radar .theme-chip:nth-child(2) { transform: rotate(-48deg) translateX(var(--radius)) rotate(48deg); }
.palette-panel.shape-radar .theme-chip:nth-child(3) { transform: rotate(-24deg) translateX(var(--radius)) rotate(24deg); }
.palette-panel.shape-radar .theme-chip:nth-child(4) { transform: rotate(0deg) translateX(var(--radius)) rotate(0deg); }
.palette-panel.shape-radar .theme-chip:nth-child(5) { transform: rotate(24deg) translateX(var(--radius)) rotate(-24deg); }
.palette-panel.shape-radar .theme-chip:nth-child(6) { transform: rotate(48deg) translateX(var(--radius)) rotate(-48deg); }
.palette-panel.shape-radar .theme-chip:nth-child(7) { transform: rotate(72deg) translateX(var(--radius)) rotate(-72deg); }

.palette-enter-active,
.palette-leave-active {
  transition: opacity 150ms ease, transform 150ms ease;
}

.palette-enter-from,
.palette-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

.palette-panel.shape-radar.palette-enter-from,
.palette-panel.shape-radar.palette-leave-to {
  opacity: 0;
  transform: none;
}
</style>
