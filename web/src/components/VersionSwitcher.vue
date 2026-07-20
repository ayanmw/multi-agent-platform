<script setup lang="ts">
/**
 * VersionSwitcher — v1 header 上的通用 UI 版本切换下拉。
 *
 * 调用全局 window.switchUIVersion(version) 完成 localStorage 写入与跳转。
 */

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

function getCurrentVersion(): string {
  const m = /^\/ui\/(v\d+)\//.exec(window.location.pathname)
  return m ? m[1] : 'latest'
}

const current = getCurrentVersion()

function onSelect(version: string) {
  if (version === current || typeof window.switchUIVersion !== 'function') return
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
  background: #2a2a2a;
  border: 1px solid #444;
  border-radius: 6px;
  color: #bbb;
  font-size: 12px;
  padding: 4px 18px 4px 8px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
}
.version-switcher:hover {
  background: #3a3a3a;
  border-color: #555;
  color: #fff;
}
</style>
