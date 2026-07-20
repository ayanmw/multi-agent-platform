<script setup lang="ts">
import { useLayout } from '../composables/useLayout'

/**
 * 移动端底部 3-tab 导航
 *
 * 直接使用 useLayout 同步 activeMobileTab；提供 prop fallback 兼容外部控制。
 */
const { activeMobileTab, setActiveMobileTab } = useLayout()

const tabs = [
  { id: 'stage', label: 'Stage', icon: '▣' },
  { id: 'sessions', label: 'Sessions', icon: '☰' },
  { id: 'files', label: 'Files', icon: '📁' },
] as const
</script>

<template>
  <nav class="mobile-nav" role="tablist" aria-label="Mobile navigation">
    <button
      v-for="tab in tabs"
      :key="tab.id"
      class="mobile-tab"
      :class="{ active: activeMobileTab === tab.id }"
      role="tab"
      :aria-selected="activeMobileTab === tab.id"
      @click="setActiveMobileTab(tab.id)"
    >
      <span class="tab-icon">{{ tab.icon }}</span>
      <span class="tab-label">{{ tab.label }}</span>
    </button>
  </nav>
</template>

<style scoped>
.mobile-nav {
  position: fixed;
  inset: auto 0 0 0;
  height: calc(var(--mobile-nav-height, 56px) + env(safe-area-inset-bottom, 0px));
  padding-bottom: env(safe-area-inset-bottom, 0px);
  padding-top: var(--space-xs);
  background: var(--bg-panel);
  border-top: 1px solid var(--border-default);
  display: flex;
  align-items: stretch;
  z-index: 40;
  display: none;
}

@media (max-width: 767px) {
  .mobile-nav {
    display: flex;
  }
}

.mobile-tab {
  flex: 1;
  background: transparent;
  border: none;
  color: var(--text-muted);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  cursor: pointer;
  transition: color 0.15s, background 0.15s;
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
}

.mobile-tab:hover {
  background: var(--bg-hover);
}

.mobile-tab.active {
  color: var(--accent-running);
  background: rgba(0, 229, 255, 0.06); /* accent-running 的低透明度 tint，无需单独 token */
}

.tab-icon {
  font-size: 18px;
  line-height: 1;
}

.tab-label {
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.3px;
}
</style>
