<script setup lang="ts">
/**
 * FileTreeNode — SessionFiles 的递归目录树节点
 *
 * 渲染单个文件/目录节点；目录点击就地展开子节点（懒加载），子节点再递归渲染
 * 同一组件，从而支持任意深度的目录树。层级越深，行内左 padding 越大，形成
 * 传统文件树缩进的视觉。
 *
 * 自引用：`<script setup>` 下组件可按文件名在自身模板内递归引用，无需 import。
 *
 * Props:
 *   - node: 当前节点（文件或目录）
 *   - sessionId: 当前 session id，用于拉取子目录与拼接文件 URL
 *   - depth: 当前层级（根为 0），仅用于计算缩进 padding
 */
import { ref, computed } from 'vue'
import { useSessionFiles, type SessionFileNode } from '../composables/useSessionFiles'

const props = withDefaults(
  defineProps<{
    node: SessionFileNode
    sessionId: string
    depth?: number
  }>(),
  { depth: 0 },
)

const { getDir, listDir, fileUrl } = useSessionFiles()

/** 本节点是否展开。状态跟随节点实例（按 relative_path 作 key 稳定保持），
 *  所以折叠再展开不会丢失已加载的子目录缓存。 */
const expanded = ref(false)

/** 本节点的子目录条目（未加载时为空骨架）。 */
const dir = computed(() => getDir(props.sessionId, props.node.relative_path))
const children = computed(() => dir.value.nodes)
const childLoading = computed(() => dir.value.loading)
const childErr = computed(() => dir.value.err)

/** 点击目录：就地展开/折叠；首次展开时懒加载子目录。
 *  点击文件：在新标签打开。 */
async function onClick() {
  if (!props.node.is_dir) {
    openFile()
    return
  }
  if (!expanded.value) {
    // 首次展开确保子目录已加载；listDir 内部对已加载/加载中的目录会直接返回。
    if (!dir.value.loaded && !dir.value.loading) {
      await listDir(props.sessionId, props.node.relative_path)
    }
  }
  expanded.value = !expanded.value
}

function openFile() {
  if (props.node.is_dir || !props.sessionId) return
  window.open(fileUrl(props.sessionId, props.node.relative_path), '_blank')
}

function formatSize(n: number): string {
  if (!n) return ''
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)}M`
  if (n >= 1024) return `${(n / 1024).toFixed(1)}K`
  return `${n}B`
}

function fileIcon(node: SessionFileNode): string {
  if (node.is_dir) return expanded.value ? '📂' : '📁'
  const ext = node.name.split('.').pop()?.toLowerCase() || ''
  if (['html', 'htm'].includes(ext)) return '🌐'
  if (['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp'].includes(ext)) return '🖼'
  if (['json', 'yaml', 'yml', 'toml'].includes(ext)) return '⚙'
  if (['md', 'txt'].includes(ext)) return '📄'
  if (['go', 'ts', 'js', 'py', 'vue', 'css'].includes(ext)) return '📜'
  return '📃'
}
</script>

<template>
  <li class="tree-node">
    <div
      class="tree-row"
      :class="{ 'is-dir': node.is_dir, 'is-file': !node.is_dir }"
      :style="{ paddingLeft: depth * 14 + 6 + 'px' }"
      :title="node.relative_path"
      @click="onClick"
    >
      <span class="tree-chevron" :class="{ expanded }">
        {{ node.is_dir ? '▸' : '·' }}
      </span>
      <span class="tree-icon">{{ fileIcon(node) }}</span>
      <span class="tree-name">{{ node.name }}</span>
      <span v-if="!node.is_dir && node.size" class="tree-size">{{ formatSize(node.size) }}</span>
      <button
        v-if="!node.is_dir"
        class="tree-open"
        title="Open in new tab"
        @click.stop="openFile"
      >↗</button>
    </div>

    <!-- 子节点：递归渲染，逐级缩进。目录未加载时显示 loading，空目录显示 Empty。 -->
    <ul v-if="node.is_dir && expanded" class="tree-children">
      <li v-if="childLoading" class="tree-sub-status">
        <span class="tree-spinner sm" /> Loading…
      </li>
      <li v-else-if="childErr" class="tree-sub-status tree-sub-err">⚠ {{ childErr }}</li>
      <li v-else-if="children.length === 0" class="tree-sub-status">Empty</li>
      <FileTreeNode
        v-for="child in children"
        :key="child.relative_path"
        :node="child"
        :session-id="sessionId"
        :depth="depth + 1"
      />
    </ul>
  </li>
</template>

<style scoped>
.tree-node {
  list-style: none;
}

.tree-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding-top: 3px;
  padding-bottom: 3px;
  padding-right: 6px;
  border-radius: 4px;
  cursor: pointer;
  transition: background 0.12s;
}
.tree-row:hover {
  background: var(--bg-hover, #202632);
}

.tree-chevron {
  color: var(--text-muted, #5c6675);
  font-size: 0.7rem;
  width: 12px;
  flex-shrink: 0;
  transition: transform 0.12s;
  text-align: center;
}
.tree-chevron.expanded {
  transform: rotate(90deg);
}

.tree-icon {
  flex-shrink: 0;
  font-size: 0.85rem;
}

.tree-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.is-dir .tree-name {
  color: var(--text-primary, #e8ebf0);
  font-weight: 500;
}
.is-file .tree-name {
  color: var(--text-secondary, #9aa3b2);
}

.tree-size {
  font-size: 0.65rem;
  color: var(--text-muted, #5c6675);
  flex-shrink: 0;
}

.tree-open {
  background: transparent;
  border: none;
  color: var(--text-muted, #5c6675);
  font-size: 0.85rem;
  padding: 2px 6px;
  cursor: pointer;
  border-radius: 4px;
  opacity: 0;
  transition: opacity 0.12s, color 0.12s;
}
.tree-row:hover .tree-open {
  opacity: 1;
}
.tree-open:hover {
  color: var(--accent-running, #00e5ff);
}

.tree-children {
  list-style: none;
  margin: 0;
  padding: 0;
}

.tree-sub-status {
  padding: 3px 10px 3px 32px;
  font-size: 0.7rem;
  color: var(--text-muted, #5c6675);
}
.tree-sub-err {
  color: var(--accent-danger, #ff4d4d);
}

.tree-spinner {
  display: inline-block;
  vertical-align: middle;
  margin-right: 6px;
  width: 12px;
  height: 12px;
  border: 1.5px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-top-color: var(--accent-running, #00e5ff);
  border-radius: 50%;
  animation: tree-spin 0.8s linear infinite;
}
.tree-spinner.sm {
  width: 10px;
  height: 10px;
}
@keyframes tree-spin {
  to { transform: rotate(360deg); }
}
</style>
