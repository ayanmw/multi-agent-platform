<script setup lang="ts">
import { ref, computed } from 'vue'
import type { Session } from '@/composables/useSessionStore'
import type { Project } from '@/composables/useProjectStore'

/**
 * SessionDock — 左侧 Session 导航面板
 *
 * 展示项目分组、会话列表、状态徽章，并提供新建/重命名/删除会话入口。
 * 使用 v2 设计 token，保持与 Observable Control Room 视觉一致。
 *
 * Props:
 *   - projects: 项目列表
 *   - activeProjectId: 当前激活项目 ID
 *   - sessions: 会话列表
 *   - activeSessionId: 当前激活会话 ID
 *   - renamingSessionId: 处于行内重命名状态的会话 ID
 *   - renameBuffer: 重命名输入框当前值
 *
 * Emits:
 *   - select-project: 切换项目
 *   - select-session: 切换会话
 *   - new-session: 在当前/指定项目下新建会话
 *   - delete-session: 删除会话
 *   - rename-start: 开始会话行内重命名
 *   - rename-commit: 提交重命名
 *   - rename-cancel: 取消重命名
 *   - update:renameBuffer: 同步重命名输入值（v-model 风格，避免直接修改 props）
 */
const props = defineProps<{
  projects: Project[]
  activeProjectId: string
  sessions: Session[]
  activeSessionId: string | null
  renamingSessionId: string | null
  renameBuffer: string
}>()

const emit = defineEmits<{
  (e: 'select-project', projectId: string): void
  (e: 'select-session', session: Session): void
  (e: 'new-session-request', projectId?: string): void
  (e: 'delete-session', session: Session): void
  (e: 'rename-start', session: Session): void
  (e: 'rename-commit', session: Session): void
  (e: 'rename-cancel'): void
  (e: 'update:renameBuffer', value: string): void
}>()

/**
 * 用户折叠的项目 ID 集合。
 *
 * 设计取舍：折叠状态完全由用户手动操作决定——切换/激活某个 project 不会自动展开它，
 * 也不会自动收起其它 project。这样多组可以同时展开对照查看。
 *
 * 状态持久化到 localStorage，刷新后保持上次的手动折叠状态。
 * 任意 project 在 store 中出现时即参与初始化（见 initCollapsed），不在集合里的
 * project 默认展开。
 */
const COLLAPSED_KEY = 'v2:session-dock:collapsed-projects'
const collapsedProjectIds = ref<Set<string>>(loadCollapsed())

/** 从 localStorage 读取上次保存的折叠集合。 */
function loadCollapsed(): Set<string> {
  if (typeof localStorage === 'undefined') return new Set()
  try {
    const raw = localStorage.getItem(COLLAPSED_KEY)
    if (!raw) return new Set()
    const arr = JSON.parse(raw)
    if (Array.isArray(arr)) return new Set(arr.filter((x): x is string => typeof x === 'string'))
  } catch {
    // 损坏数据忽略，回到默认全展开。
  }
  return new Set()
}

/** 把当前折叠集合写回 localStorage，刷新后保持一致。 */
function persistCollapsed() {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(COLLAPSED_KEY, JSON.stringify([...collapsedProjectIds.value]))
  } catch {
    // 配额或隐私模式下写入失败可忽略，仅影响下次刷新的折叠记忆。
  }
}

/** 按项目分组的会话 */
const projectGroups = computed(() => {
  const sessionMap = new Map<string, Session[]>()
  for (const s of props.sessions) {
    const pid = s.projectId || 'default'
    if (!sessionMap.has(pid)) sessionMap.set(pid, [])
    sessionMap.get(pid)!.push(s)
  }

  const groups: { project: Project; sessions: Session[]; isCollapsed: boolean }[] = []
  const seen = new Set<string>()

  for (const p of props.projects) {
    seen.add(p.id)
    const list = sessionMap.get(p.id) || []
    // 按 createdAt 降序：新建在最上。顺序不随点击/更新变化，避免列表反复跳动。
    list.sort((a, b) => b.createdAt - a.createdAt)
    groups.push({
      project: p,
      sessions: list,
      // 折叠状态直接由 collapsedProjectIds 决定，完全手动控制，不随激活状态自动展开。
      isCollapsed: collapsedProjectIds.value.has(p.id),
    })
  }

  // 包含未注册到 projects 的项目会话（兜底）
  for (const [pid, list] of sessionMap) {
    if (!seen.has(pid)) {
      list.sort((a, b) => b.createdAt - a.createdAt)
      groups.push({
        project: { id: pid, name: pid === 'default' ? 'Default' : pid, description: '', working_directory: '', created_at: '', updated_at: '' },
        sessions: list,
        isCollapsed: collapsedProjectIds.value.has(pid),
      })
    }
  }

  return groups
})

function toggleCollapse(projectId: string) {
  const next = new Set(collapsedProjectIds.value)
  if (next.has(projectId)) next.delete(projectId)
  else next.add(projectId)
  collapsedProjectIds.value = next
  persistCollapsed()
}

/**
 * 计算某 project 的 session 数量。
 * 优先使用 project.session_count（来自后端 summary，统计该 project 下全部 session），
 * 仅在没有该字段时回退到当前已加载 sessions 的长度。
 * 这样在 sessions 列表只包含 active project 会话时，其它 project 仍能显示真实数量。
 */
function sessionCount(group: { project: Project; sessions: Session[] }): number {
  const fromProject = group.project.session_count
  if (typeof fromProject === 'number' && fromProject > 0) {
    return fromProject
  }
  // 当 group.sessions 非空（当前 active project），优先用实际加载的数据，
  // 因为它比 summary 计数更新（例如刚新建了一个 session）。
  if (group.sessions.length > 0) {
    return group.sessions.length
  }
  return fromProject ?? 0
}

/**
 * 点击项目 header 的行为：
 * - 若点击的是当前未激活项目，切换项目（useSessionStore 会加载该项目 sessions）。
 * - 若点击的是当前已激活项目，则切换该分组的折叠/展开，让用户可以一键收起已展开的项目。
 * 折叠/展开仍可通过左侧独立的 collapse-btn 操作。
 *
 * 注意：切换项目不再自动展开/收起其它分组——多组可同时展开，状态完全手动控制。
 */
function handleProjectHeaderClick(projectId: string) {
  if (projectId === props.activeProjectId) {
    toggleCollapse(projectId)
  } else {
    emit('select-project', projectId)
  }
}

function statusClass(status: Session['status']): string {
  switch (status) {
    case 'running':
      return 'status--running'
    case 'completed':
      return 'status--success'
    case 'failed':
      return 'status--danger'
    default:
      return 'status--idle'
  }
}

function formatTime(ts: number): string {
  if (!ts) return ''
  const d = new Date(ts)
  const now = new Date()
  const isSameDay =
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate()
  try {
    return isSameDay
      ? d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
      : d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
  } catch {
    return d.toISOString()
  }
}
</script>

<template>
  <div class="session-dock">
    <div class="project-groups">
      <div
        v-for="group in projectGroups"
        :key="group.project.id"
        class="project-group"
      >
        <div
          class="project-header"
          :class="{ active: group.project.id === activeProjectId }"
          @click="handleProjectHeaderClick(group.project.id)"
        >
          <button
            class="collapse-btn"
            :title="group.isCollapsed ? 'Expand' : 'Collapse'"
            @click.stop="toggleCollapse(group.project.id)"
          >
            {{ group.isCollapsed ? '▶' : '▼' }}
          </button>
          <span class="project-name">{{ group.project.name }}</span>
          <span
            class="project-session-count"
            :title="`${sessionCount(group)} session(s)`"
          >
            {{ sessionCount(group) }}
          </span>
          <button
            class="project-new-session-btn"
            title="New session in this project"
            @click.stop="emit('new-session-request', group.project.id)"
          >
            +
          </button>
        </div>

        <div v-if="!group.isCollapsed" class="project-sessions">
          <div
            v-for="session in group.sessions"
            :key="session.id"
            class="session-item"
            :class="{ active: session.id === activeSessionId }"
            @click="emit('select-session', session)"
          >
            <div class="session-main">
              <input
                v-if="renamingSessionId === session.id"
                :value="renameBuffer"
                type="text"
                class="rename-input"
                @click.stop
                @input="emit('update:renameBuffer', ($event.target as HTMLInputElement).value)"
                @keydown.enter="emit('rename-commit', session)"
                @keydown.escape="emit('rename-cancel')"
                @blur="emit('rename-commit', session)"
              />
              <span v-else class="session-name" :title="session.name">
                {{ session.name }}
              </span>
            </div>

            <div class="session-meta">
              <span class="status-badge" :class="statusClass(session.status)">
                {{ session.status }}
              </span>
              <span v-if="session.totalTokens > 0" class="session-tokens">
                {{ session.totalTokens }}t
              </span>
              <span class="session-time">{{ formatTime(session.createdAt) }}</span>
            </div>

            <div class="session-actions">
              <button
                class="session-action edit"
                title="Rename"
                @click.stop="emit('rename-start', session)"
              >
                ✎
              </button>
              <button
                class="session-action delete"
                title="Delete"
                @click.stop="emit('delete-session', session)"
              >
                ×
              </button>
            </div>
          </div>
        </div>
      </div>

      <div v-if="projectGroups.length === 0" class="empty-state">
        No projects loaded.
      </div>
    </div>
  </div>
</template>

<style scoped>
.session-dock {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
}

.project-groups {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-sm);
}

.project-group {
  margin-bottom: var(--space-sm);
}

.project-header {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  padding: var(--space-sm) var(--space-md);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background var(--transition-fast);
}

.project-header:hover {
  background: var(--bg-hover);
}

.project-header.active {
  background: rgba(0, 229, 255, 0.08);
}

.project-header.active .project-name {
  color: var(--accent-running);
}

.collapse-btn {
  background: transparent;
  border: none;
  color: var(--text-muted);
  font-size: 10px;
  cursor: pointer;
  padding: 0;
  width: 14px;
  line-height: 1;
}

.project-name {
  flex: 1;
  min-width: 0;
  font-family: var(--font-display);
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.project-session-count {
  font-family: var(--font-mono);
  font-size: 0.65rem;
  color: var(--text-muted);
  background: var(--bg-panel);
  padding: 1px 6px;
  border-radius: 8px;
}

.project-new-session-btn {
  width: 18px;
  height: 18px;
  border-radius: var(--radius-sm);
  border: 1px solid transparent;
  background: transparent;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: color var(--transition-fast), background var(--transition-fast), border-color var(--transition-fast);
}

.project-new-session-btn:hover {
  background: var(--bg-panel);
  border-color: var(--border-active);
  color: var(--accent-running);
}

.project-sessions {
  padding-left: var(--space-md);
  margin-top: var(--space-xs);
  display: flex;
  flex-direction: column;
  gap: var(--space-xs);
}

.session-item {
  position: relative;
  padding: var(--space-sm) var(--space-md);
  border-radius: var(--radius-md);
  cursor: pointer;
  border: 1px solid transparent;
  transition: background var(--transition-fast), border-color var(--transition-fast);
}

.session-item:hover {
  background: var(--bg-hover);
}

.session-item:hover .session-actions {
  opacity: 1;
}

.session-item.active {
  background: rgba(0, 229, 255, 0.08);
  border-color: rgba(0, 229, 255, 0.25);
}

.session-main {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  min-width: 0;
  margin-bottom: var(--space-xs);
}

.session-name {
  flex: 1;
  min-width: 0;
  font-family: var(--font-mono);
  font-size: 0.8rem;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.rename-input {
  flex: 1;
  min-width: 0;
  background: var(--bg-canvas);
  border: 1px solid var(--border-active);
  border-radius: var(--radius-sm);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.8rem;
  padding: 2px 6px;
  outline: none;
}

.session-meta {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
}

.status-badge {
  font-family: var(--font-mono);
  font-size: 0.6rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  padding: 1px 5px;
  border-radius: 8px;
}

.status-badge.status--running {
  color: var(--accent-running);
  background: rgba(0, 229, 255, 0.1);
}

.status-badge.status--success {
  color: var(--accent-success);
  background: rgba(57, 255, 20, 0.1);
}

.status-badge.status--danger {
  color: var(--accent-danger);
  background: rgba(255, 77, 77, 0.1);
}

.status-badge.status--idle {
  color: var(--text-muted);
  background: rgba(255, 255, 255, 0.04);
}

.session-tokens,
.session-time {
  font-family: var(--font-mono);
  font-size: 0.65rem;
  color: var(--text-muted);
}

.session-actions {
  position: absolute;
  top: var(--space-sm);
  right: var(--space-sm);
  display: flex;
  gap: 4px;
  opacity: 0;
  transition: opacity var(--transition-fast);
}

.session-action {
  background: transparent;
  border: none;
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
  padding: 2px 4px;
  border-radius: var(--radius-sm);
  transition: color var(--transition-fast);
}

.session-action.edit:hover {
  color: var(--accent-running);
}

.session-action.delete:hover {
  color: var(--accent-danger);
}

.empty-state {
  padding: var(--space-xl);
  text-align: center;
  color: var(--text-muted);
  font-size: 0.8rem;
}
</style>
