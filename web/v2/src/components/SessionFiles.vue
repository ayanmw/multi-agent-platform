<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useSessionFiles, type SessionFileNode } from '../composables/useSessionFiles'

/**
 * SessionFiles — 当前 session workspace 文件浏览器
 *
 * 只展示属于当前 session 的工作目录（由后端按 session_id 限定），支持：
 *   - 单层列出，目录可点击展开/折叠（懒加载子目录）
 *   - 文件点击在新标签打开（/s/{session_id}/{relative_path}）
 *   - 路径栏显示当前目录，可逐级返回
 *   - 空目录 / 无 workspace / 加载中 / 出错四种占位
 *
 * 数据源 useSessionFiles 已按 sessionId+path 缓存；切换 session 时由 App 通知重置。
 *
 * Props:
 *   - sessionId: 当前 session id；为空时显示"未选择会话"占位
 */
const props = defineProps<{
  sessionId: string
}>()

const { listDir, getDir, fileUrl } = useSessionFiles()

/** 展开的目录 relative_path 集合；空字符串代表根。 */
const expanded = ref<Set<string>>(new Set(['']))

/** 当前进入的目录（面包屑末项）。空字符串代表根。 */
const currentPath = ref('')

/** 缓存：relative_path -> 其下展开的子节点快照，用于渲染树。每层目录独立懒加载。 */
const tree = ref<Record<string, SessionFileNode[]>>({})

/** 加载某层目录并刷新 tree 缓存。 */
async function loadPath(path: string): Promise<SessionFileNode[]> {
  if (!props.sessionId) return []
  await listDir(props.sessionId, path)
  const dir = getDir(props.sessionId, path)
  tree.value[path] = dir.nodes
  return dir.nodes
}

/** 展开/折叠某个目录节点。展开时懒加载子目录。 */
async function toggleDir(node: SessionFileNode): Promise<void> {
  const path = node.relative_path
  const next = new Set(expanded.value)
  if (next.has(path)) {
    next.delete(path)
  } else {
    next.add(path)
    await loadPath(path)
  }
  expanded.value = next
}

/** 进入某目录（设置 currentPath 并加载）。 */
async function enterDir(path: string): Promise<void> {
  currentPath.value = path
  await loadPath(path)
}

/** 返回上一级目录。 */
async function goUp(): Promise<void> {
  if (!currentPath.value) return
  const parts = currentPath.value.split('/')
  parts.pop()
  await enterDir(parts.join('/'))
}

/** 在新标签打开文件。 */
function openFile(node: SessionFileNode): void {
  if (!props.sessionId || node.is_dir) return
  window.open(fileUrl(props.sessionId, node.relative_path), '_blank')
}

/** 面包屑：把 currentPath 拆成可逐级点击的段。 */
const breadcrumbs = computed(() => {
  if (!currentPath.value) return [{ label: 'workspace', path: '' }]
  const parts = currentPath.value.split('/')
  const crumbs = [{ label: 'workspace', path: '' }]
  let acc = ''
  for (const p of parts) {
    acc = acc ? acc + '/' + p : p
    crumbs.push({ label: p, path: acc })
  }
  return crumbs
})

const rootDir = computed(() => getDir(props.sessionId, ''))
const currentDir = computed(() => getDir(props.sessionId, currentPath.value))

function formatSize(n: number): string {
  if (!n) return ''
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)}M`
  if (n >= 1024) return `${(n / 1024).toFixed(1)}K`
  return `${n}B`
}

function fileIcon(node: SessionFileNode): string {
  if (node.is_dir) return '📁'
  const ext = node.name.split('.').pop()?.toLowerCase() || ''
  if (['html', 'htm'].includes(ext)) return '🌐'
  if (['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp'].includes(ext)) return '🖼'
  if (['json', 'yaml', 'yml', 'toml'].includes(ext)) return '⚙'
  if (['md', 'txt'].includes(ext)) return '📄'
  if (['go', 'ts', 'js', 'py', 'vue', 'css'].includes(ext)) return '📜'
  return '📃'
}

// 切换 session：重置树、展开状态、当前路径，并加载根目录。
watch(
  () => props.sessionId,
  async (sid) => {
    tree.value = {}
    expanded.value = new Set([''])
    currentPath.value = ''
    if (sid) await loadPath('')
  },
  { immediate: true },
)
</script>

<template>
  <div class="session-files">
    <!-- 面包屑路径栏 -->
    <div class="files-breadcrumbs">
      <button class="crumb-up" :disabled="!currentPath" title="Up" @click="goUp">↑</button>
      <template v-for="(c, i) in breadcrumbs" :key="c.path">
        <span v-if="i > 0" class="crumb-sep">/</span>
        <button class="crumb" :class="{ active: i === breadcrumbs.length - 1 }" @click="enterDir(c.path)">
          {{ c.label }}
        </button>
      </template>
    </div>

    <!-- 未选择会话 -->
    <div v-if="!sessionId" class="files-placeholder">
      <div class="files-placeholder-icon">📂</div>
      <p>Select a session to browse its workspace.</p>
    </div>

    <template v-else>
      <!-- 根目录加载中 -->
      <div v-if="rootDir.loading && rootDir.nodes.length === 0" class="files-loading">
        <div class="files-spinner" />
        <span>Loading workspace…</span>
      </div>

      <!-- 无 workspace 或空目录 -->
      <div v-else-if="currentDir.nodes.length === 0 && !currentDir.loading" class="files-empty">
        <div class="files-empty-icon">🗂</div>
        <p v-if="!rootDir.nodes.length && currentPath === ''">This session has no workspace files yet.</p>
        <p v-else>Empty folder.</p>
      </div>

      <!-- 出错 -->
      <div v-else-if="currentDir.err" class="files-error">
        <p>⚠ {{ currentDir.err }}</p>
      </div>

      <!-- 文件列表 -->
      <ul v-else class="files-list">
        <li
          v-for="node in currentDir.nodes"
          :key="node.relative_path"
          class="file-row"
          :class="{ 'file-dir': node.is_dir, 'file-file': !node.is_dir }"
        >
          <button class="file-main" @click="node.is_dir ? toggleDir(node) : openFile(node)">
            <span class="file-chevron" :class="{ expanded: expanded.has(node.relative_path) }">
              {{ node.is_dir ? '▸' : '·' }}
            </span>
            <span class="file-icon">{{ fileIcon(node) }}</span>
            <span class="file-name" :title="node.name">{{ node.name }}</span>
          </button>
          <span v-if="!node.is_dir && node.size" class="file-size">{{ formatSize(node.size) }}</span>
          <button
            v-if="!node.is_dir"
            class="file-open"
            title="Open in new tab"
            @click.stop="openFile(node)"
          >↗</button>

          <!-- 子目录懒加载列表（仅展开时渲染） -->
          <ul
            v-if="node.is_dir && expanded.has(node.relative_path)"
            class="files-sublist"
          >
            <li v-if="getDir(sessionId, node.relative_path).loading" class="sub-loading">
              <span class="files-spinner sm" /> Loading…
            </li>
            <li v-else-if="getDir(sessionId, node.relative_path).nodes.length === 0" class="sub-empty">
              Empty
            </li>
            <li
              v-for="child in getDir(sessionId, node.relative_path).nodes"
              :key="child.relative_path"
              class="file-row sub"
            >
              <button class="file-main" @click="child.is_dir ? toggleDir(child) : openFile(child)">
                <span class="file-chevron" :class="{ expanded: expanded.has(child.relative_path) }">
                  {{ child.is_dir ? '▸' : '·' }}
                </span>
                <span class="file-icon">{{ fileIcon(child) }}</span>
                <span class="file-name" :title="child.name">{{ child.name }}</span>
              </button>
            </li>
          </ul>
        </li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.session-files {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  font-family: var(--font-mono, monospace);
  font-size: 0.78rem;
}

.files-breadcrumbs {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 6px 10px;
  border-bottom: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  background: var(--bg-elevated, #181c24);
  flex-shrink: 0;
  overflow-x: auto;
  white-space: nowrap;
}

.crumb-up {
  background: transparent;
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  color: var(--text-secondary, #9aa3b2);
  border-radius: 4px;
  width: 22px;
  height: 22px;
  cursor: pointer;
  flex-shrink: 0;
}
.crumb-up:hover:not(:disabled) {
  background: var(--bg-hover, #202632);
  color: var(--text-primary, #e8ebf0);
}
.crumb-up:disabled {
  opacity: 0.35;
  cursor: default;
}

.crumb {
  background: transparent;
  border: none;
  color: var(--text-secondary, #9aa3b2);
  cursor: pointer;
  padding: 2px 6px;
  border-radius: 4px;
  font-family: inherit;
  font-size: inherit;
}
.crumb:hover {
  background: var(--bg-hover, #202632);
  color: var(--text-primary, #e8ebf0);
}
.crumb.active {
  color: var(--accent-running, #00e5ff);
}
.crumb-sep {
  color: var(--text-muted, #5c6675);
}

.files-list {
  flex: 1;
  overflow-y: auto;
  padding: 6px 4px;
  list-style: none;
  margin: 0;
}

.file-row {
  display: flex;
  align-items: center;
  border-radius: 6px;
  position: relative;
}
.file-row:hover {
  background: var(--bg-hover, #202632);
}

.file-main {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  background: transparent;
  border: none;
  color: var(--text-primary, #e8ebf0);
  padding: 4px 8px;
  cursor: pointer;
  text-align: left;
  font-family: inherit;
  font-size: inherit;
}

.file-chevron {
  color: var(--text-muted, #5c6675);
  font-size: 0.7rem;
  width: 12px;
  flex-shrink: 0;
  transition: transform 0.12s;
}
.file-chevron.expanded {
  transform: rotate(90deg);
}

.file-icon {
  flex-shrink: 0;
  font-size: 0.85rem;
}

.file-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-dir .file-name {
  color: var(--text-primary, #e8ebf0);
  font-weight: 500;
}
.file-file .file-name {
  color: var(--text-secondary, #9aa3b2);
}

.file-size {
  font-size: 0.65rem;
  color: var(--text-muted, #5c6675);
  padding-right: 4px;
  flex-shrink: 0;
}

.file-open {
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
.file-row:hover .file-open {
  opacity: 1;
}
.file-open:hover {
  color: var(--accent-running, #00e5ff);
}

.files-sublist {
  list-style: none;
  margin: 0;
  padding: 0 0 0 22px;
  border-left: 1px dashed var(--border-subtle, rgba(255, 255, 255, 0.06));
  margin-left: 14px;
}
.sub {
  padding-left: 0;
}
.sub-loading,
.sub-empty {
  padding: 4px 10px;
  font-size: 0.7rem;
  color: var(--text-muted, #5c6675);
}

.files-placeholder,
.files-empty,
.files-error,
.files-loading {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  gap: 8px;
  color: var(--text-muted, #5c6675);
  padding: 24px;
}
.files-placeholder-icon,
.files-empty-icon {
  font-size: 2rem;
  opacity: 0.7;
}

.files-spinner {
  width: 18px;
  height: 18px;
  border: 2px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-top-color: var(--accent-running, #00e5ff);
  border-radius: 50%;
  animation: files-spin 0.8s linear infinite;
}
.files-spinner.sm {
  width: 12px;
  height: 12px;
  border-width: 1.5px;
  display: inline-block;
  vertical-align: middle;
  margin-right: 6px;
}
@keyframes files-spin {
  to { transform: rotate(360deg); }
}

.files-loading {
  flex-direction: row;
  gap: 10px;
}
</style>
