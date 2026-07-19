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
  (e: 'new-session', projectId?: string): void
  (e: 'delete-session', session: Session): void
  (e: 'rename-start', session: Session): void
  (e: 'rename-commit', session: Session): void
  (e: 'rename-cancel'): void
  (e: 'update:renameBuffer', value: string): void
}>()

/** 用户折叠的项目 ID 集合 */
const collapsedProjectIds = ref<Set<string>>(new Set())

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
    list.sort((a, b) => b.updatedAt - a.updatedAt)
    const isActive = p.id === props.activeProjectId
    groups.push({
      project: p,
      sessions: list,
      isCollapsed: collapsedProjectIds.value.has(p.id) && !isActive,
    })
  }

  // 包含未注册到 projects 的项目会话（兜底）
  for (const [pid, list] of sessionMap) {
    if (!seen.has(pid)) {
      list.sort((a, b) => b.updatedAt - a.updatedAt)
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
    <div class="dock-section-header">
      <span class="dock-section-title">Sessions</span>
      <button
        class="dock-action-btn"
        title="New session"
        @click="emit('new-session')"
      >
        +
      </button>
    </div>

    <div class="project-groups">
      <div
        v-for="group in projectGroups"
        :key="group.project.id"
        class="project-group"
      >
        <div
          class="project-header"
          :class="{ active: group.project.id === activeProjectId }"
          @click="emit('select-project', group.project.id)"
        >
          <button
            class="collapse-btn"
            :title="group.isCollapsed ? 'Expand' : 'Collapse'"
            @click.stop="toggleCollapse(group.project.id)"
          >
            {{ group.isCollapsed ? '▶' : '▼' }}
          </button>
          <span class="project-name">{{ group.project.name }}</span>
          <span class="project-session-count">{{ group.sessions.length }}</span>
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
              <span class="session-time">{{ formatTime(session.updatedAt) }}</span>
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

          <button
            class="new-session-inline"
            @click="emit('new-session', group.project.id)"
          >
            + New Session
          </button>
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

.dock-section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-sm) var(--space-md);
  border-bottom: 1px solid var(--border-default);
  background: var(--bg-elevated);
  flex-shrink: 0;
}

.dock-section-title {
  font-family: var(--font-display);
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--text-secondary);
}

.dock-action-btn {
  width: 24px;
  height: 24px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-default);
  background: var(--bg-panel);
  color: var(--text-secondary);
  font-size: 16px;
  line-height: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: all var(--transition-fast);
}

.dock-action-btn:hover {
  background: var(--bg-hover);
  color: var(--accent-running);
  border-color: var(--border-active);
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

.new-session-inline {
  width: 100%;
  text-align: left;
  padding: var(--space-sm) var(--space-md);
  border: 1px dashed var(--border-default);
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.75rem;
  cursor: pointer;
  transition: all var(--transition-fast);
}

.new-session-inline:hover {
  background: rgba(0, 229, 255, 0.04);
  border-color: var(--border-active);
  color: var(--text-secondary);
}

.empty-state {
  padding: var(--space-xl);
  text-align: center;
  color: var(--text-muted);
  font-size: 0.8rem;
}
</style>
