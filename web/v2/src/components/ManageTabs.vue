/**
 * ManageTabs.vue
 *
 * 管理（原 Inspector）面板的 Tab 容器。渲染顶部 tab bar，并通过默认 slot 暴露当前 tab 的内容区。
 * 父组件使用 v-model:activeTab 或监听 update:activeTab 来同步当前激活 tab。
 *
 * tab 列表包含 Memory/RAG/Context/Cases/Agents/Project/Skills/Traces。
 * Sessions tab 已移除——其信息与左侧 SessionDock 完全重复，无额外价值；
 * 默认 tab 由 ManageContent 设为 memory。
 */
<script setup lang="ts">
interface Props {
  activeTab: string
}

const props = defineProps<Props>()

const emit = defineEmits<{
  (e: 'update:activeTab', tab: string): void
}>()

const tabs = [
  { id: 'memory', label: 'Memory' },
  { id: 'rag', label: 'RAG' },
  { id: 'todos', label: 'TODOs' },
  { id: 'context', label: 'Context' },
  { id: 'cases', label: 'Cases' },
  { id: 'agents', label: 'Agents' },
  { id: 'project', label: 'Project' },
  { id: 'skills', label: 'Skills' },
  { id: 'traces', label: 'Traces' },
] as const

function selectTab(id: string) {
  emit('update:activeTab', id)
}
</script>

<template>
  <div class="manage-tabs">
    <div class="tab-bar" role="tablist">
      <button
        v-for="tab in tabs"
        :key="tab.id"
        type="button"
        class="tab-button focus-glow"
        :class="{ 'tab-button--active': activeTab === tab.id }"
        role="tab"
        :aria-selected="activeTab === tab.id"
        @click="selectTab(tab.id)"
      >
        {{ tab.label }}
      </button>
    </div>

    <div class="tab-content" role="tabpanel">
      <slot />
    </div>
  </div>
</template>

<style scoped>
.manage-tabs {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--bg-panel);
}

.tab-bar {
  display: flex;
  gap: var(--space-xs);
  padding: var(--space-sm) var(--space-sm) 0;
  border-bottom: 1px solid var(--border-default);
  background: var(--bg-elevated);
  overflow-x: auto;
}

.tab-button {
  position: relative;
  padding: var(--space-sm) var(--space-md);
  border: none;
  background: transparent;
  color: var(--text-secondary);
  font-family: var(--font-display);
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  white-space: nowrap;
  transition: color var(--transition-fast);
}

.tab-button:hover {
  color: var(--text-primary);
}

.tab-button--active {
  color: var(--accent-running);
}

.tab-button--active::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 2px;
  background: var(--accent-running);
  box-shadow: 0 -2px 8px var(--accent-running);
}

.tab-content {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
</style>
