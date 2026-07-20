<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useSessionFiles, type SessionFileNode } from '../composables/useSessionFiles'
import FileTreeNode from './FileTreeNode.vue'

/**
 * SessionFiles — 当前 session workspace 文件浏览器（目录树视图）
 *
 * 以传统目录树方式展示 workspace：根目录节点列表，每个目录点击就地展开，
 * 子节点递归缩进渲染（见 FileTreeNode.vue），支持任意层级深度。
 *
 * 视图策略：
 *   - 单根树：直接列出 workspace 根下的节点；目录展开后子节点就地嵌套。
 *   - 懒加载：目录首次展开时才请求 /api/sessions/{id}/workspace-tree?path=<rel>，
 *     结果按 sessionId+path 缓存（见 useSessionFiles），折叠再展开不重新请求。
 *   - 顶部面包屑保留，便于对深层目录快速跳转/返回；点击面包屑进入该目录的
 *     “以该目录为根”的视图（仍走同一棵树，只是滚动定位）。当前实现面包屑只
 *     控制根层 currentPath，深层展开仍靠点击目录节点本身。
 *
 * Props:
 *   - sessionId: 当前 session id；为空时显示"未选择会话"占位
 */
const props = defineProps<{
  sessionId: string
}>()

const { listDir, getDir, fileUrl } = useSessionFiles()

/** 根列表缓存（直接读 store 即可，这里保留别名便于模板语义清晰）。 */
const rootDir = computed(() => getDir(props.sessionId, ''))
const rootNodes = computed(() => rootDir.value.nodes)

/** 当前进入的"视图根"（面包屑末项），用于空态文案。空字符串代表 workspace 根。 */
const currentPath = ref('')

/** 进入某目录：作为视图根加载。目录树本身不依赖 currentPath，这里仅为面包屑服务。 */
async function enterDir(path: string): Promise<void> {
  currentPath.value = path
  if (path) await listDir(props.sessionId, path)
}

/** 返回上一级。 */
async function goUp(): Promise<void> {
  if (!currentPath.value) return
  const parts = currentPath.value.split('/')
  parts.pop()
  await enterDir(parts.join('/'))
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

// 切换 session：重置视图根并加载根目录。
watch(
  () => props.sessionId,
  async (sid) => {
    currentPath.value = ''
    if (sid) await listDir(sid, '')
  },
  { immediate: true },
)

// fileUrl 保留导出语义（FileTreeNode 自带，这里不再直接用，留作未来扩展）。
void fileUrl
</script>

<template>
  <div class="session-files">
    <!-- 面包屑路径栏：快速跳转到某层目录作为视图根 -->
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
      <div v-if="rootDir.loading && rootNodes.length === 0" class="files-loading">
        <div class="files-spinner" />
        <span>Loading workspace…</span>
      </div>

      <!-- 无 workspace -->
      <div v-else-if="rootNodes.length === 0 && !rootDir.loading" class="files-empty">
        <div class="files-empty-icon">🗂</div>
        <p>This session has no workspace files yet.</p>
      </div>

      <!-- 出错 -->
      <div v-else-if="rootDir.err" class="files-error">
        <p>⚠ {{ rootDir.err }}</p>
      </div>

      <!-- 目录树：根节点列表，每个目录递归展开 -->
      <ul v-else class="files-tree">
        <FileTreeNode
          v-for="node in rootNodes"
          :key="node.relative_path"
          :node="node"
          :session-id="sessionId"
          :depth="0"
        />
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

.files-tree {
  flex: 1;
  overflow-y: auto;
  padding: 6px 4px;
  list-style: none;
  margin: 0;
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
@keyframes files-spin {
  to { transform: rotate(360deg); }
}

.files-loading {
  flex-direction: row;
  gap: 10px;
}
</style>
