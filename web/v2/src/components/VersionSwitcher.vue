<script setup lang="ts">
/**
 * VersionSwitcher — v2 TopBar 上的通用 UI 版本切换下拉。
 *
 * 调用全局 window.switchUIVersion(version) 完成 localStorage 写入与跳转。
 */
import { ref } from 'vue'

declare global {
  interface Window {
    switchUIVersion?: (version: string) => void
  }
}

interface VersionOption {
  id: string
  label: string
}

const options: VersionOption[] = [
  { id: 'latest', label: 'Latest (v2)' },
  { id: 'v2', label: 'v2' },
  { id: 'v1', label: 'v1' },
]

const current = ref<string>(getCurrentVersion())

function getCurrentVersion(): string {
  const m = /^\/ui\/(v\d+)\//.exec(window.location.pathname)
  return m ? m[1] : 'latest'
}

function onSelect(version: string) {
  if (version === current.value || typeof window.switchUIVersion !== 'function') return
  window.switchUIVersion(version)
}
</script>

<template>
  <select
    class="version-switcher"
    :value="current"
    title="Switch UI version"
    @change="onSelect(($event.target as HTMLSelectElement).value)"
  >
    <option v-for="opt in options" :key="opt.id" :value="opt.id">
      {{ opt.label }}
    </option>
  </select>
</template>

<style scoped>
.version-switcher {
  appearance: none;
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  font-family: var(--font-display);
  font-size: 11px;
  padding: 4px 20px 4px 8px;
  cursor: pointer;
  transition: border-color var(--transition-fast), color var(--transition-fast);
}
.version-switcher:hover {
  border-color: var(--border-active);
  color: var(--text-primary);
}
</style>
