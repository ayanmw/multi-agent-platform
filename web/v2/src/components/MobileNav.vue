<script setup lang="ts">
import { useLayout } from '../composables/useLayout'

/**
 * 移动端底部 5-tab 导航
 *
 * Stage（主舞台）、Sessions、Files 保留原布局语义；新增 Manage/Cron 直达入口，
 * 将原本藏在 TopBar 的长尾操作下沉到底部导航。
 */
const { activeMobileTab, setActiveMobileTab, mobileMoreOpen, setMobileMoreOpen } = useLayout()

const tabs = [
  { id: 'stage', label: 'Stage', icon: '▣' },
  { id: 'sessions', label: 'Sessions', icon: '☰' },
  { id: 'files', label: 'Files', icon: '📁' },
  { id: 'manage', label: 'Manage', icon: '🎛' },
  { id: 'cron', label: 'Cron', icon: '⏰' },
] as const

function onTabClick(id: typeof tabs[number]['id']) {
  // 点击同一个 manage/cron tab 在展开与最小化之间切换不太直观，因此只做切换。
  if (id === 'manage' || id === 'cron') {
    setMobileMoreOpen(false)
  }
  setActiveMobileTab(id)
}
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
      :aria-label="tab.label"
      @click="onTabClick(tab.id)"
    >
      <span class="tab-icon">{{ tab.icon }}</span>
      <span class="tab-label">{{ tab.label }}</span>
    </button>
  </nav>
</template>

<style scoped>
.mobile-nav {
  display: none;
}

@media (max-width: 767px) {
  .mobile-nav {
    position: relative;
    flex-shrink: 0;
    height: calc(var(--mobile-nav-height, 64px) + env(safe-area-inset-bottom, 0px));
    padding-bottom: env(safe-area-inset-bottom, 0px);
    padding-top: var(--space-xs);
    background: var(--bg-panel);
    border-top: 1px solid var(--border-default);
    display: flex;
    align-items: stretch;
    z-index: 40;
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
  gap: 3px;
  cursor: pointer;
  transition: color 0.15s, background 0.15s;
  font-family: var(--font-display, 'Chakra Petch', sans-serif);
  min-width: 44px;
  min-height: 44px;
}

.mobile-tab:hover {
  background: var(--bg-hover);
}

.mobile-tab.active {
  color: var(--accent-running);
  background: rgba(0, 229, 255, 0.06);
}

.tab-icon {
  font-size: 18px;
  line-height: 1;
}

.tab-label {
  font-size: 12px;
  font-weight: 500;
  letter-spacing: 0.2px;
}
</style>
