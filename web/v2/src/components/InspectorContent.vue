<script setup lang="ts">
import { computed, ref } from 'vue'
import InspectorTabs from './InspectorTabs.vue'
import SkillPanel from './SkillPanel.vue'
import CaseFilter from './CaseFilter.vue'
import CaseCard from './CaseCard.vue'
import MemoryBrowser from './MemoryBrowser.vue'
import RAGPreviewPanel from './RAGPreviewPanel.vue'
import ContextWindowPanel from './ContextWindowPanel.vue'
import AgentConfig from './AgentConfig.vue'
import ProjectConfig from './ProjectConfig.vue'
import { useCaseStore } from '@/composables/useCaseStore'
import { useSkills } from '@/composables/useSkills'
import { useTaskStore } from '@/composables/useTaskStore'
import { useSessionStore } from '@/composables/useSessionStore'
import { useProjectStore } from '@/composables/useProjectStore'
import { useTraceStore } from '@/composables/useTraceStore'
import type { Case, CreateCaseRequest, UpdateCaseRequest } from '@/types/case'
import type { SpanNode } from '@/composables/useTraceStore'

/**
 * InspectorContent — 右侧 Inspector 面板内容
 *
 * 使用 InspectorTabs 切换多个信息面板的容器组件。
 * 已迁移的面板直接渲染真实组件；暂时保留 traces / sessions 的最小化实现。
 *
 * Emits:
 *   - run-case: 从 Cases tab 运行指定 case
 *   - trigger-skill: 从 Skills tab 触发 skill 命令
 */
const emit = defineEmits<{
  (e: 'run-case', caseId: string): void
  (e: 'trigger-skill', command: string): void
}>()

/** 当前激活的 Inspector tab */
const activeTab = ref('sessions')

const caseStore = useCaseStore()
const { filteredCases, allTags, allCategories, selectedTags, selectedCategory, loading: casesLoading } = caseStore
const { skills } = useSkills()
const { activeTaskId, taskCache } = useTaskStore()
const { activeSession } = useSessionStore()
const { activeProject, projects } = useProjectStore()
const traceStore = useTraceStore()

/** 当前在 ContextWindowPanel 中查看的子任务 / Agent 实例 */
const selectedSubTaskId = ref('')

/** 将 trace tree 拍平为带缩进深度的行列表 */
const flattenedSpans = computed(() => {
  const rows: Array<SpanNode & { depth: number }> = []
  function walk(nodes: SpanNode[], depth: number) {
    for (const node of nodes) {
      rows.push({ ...node, depth })
      if (node.children?.length) {
        walk(node.children, depth + 1)
      }
    }
  }
  walk(traceStore.spans.value, 0)
  return rows
})

/** active task 下的子任务 / agent 列表，供 ContextWindowPanel 选择 */
const subTaskOptions = computed(() => {
  if (!activeTaskId.value) return []
  const task = taskCache.value[activeTaskId.value]
  if (!task) return []
  return Object.keys(task.agents).map(agentId => ({
    id: agentId,
    label: task.agents[agentId]?.name || agentId,
  }))
})

function handleCaseRun(caseId: string) {
  emit('run-case', caseId)
}

function handleCaseView(caseId: string) {
  console.log('[InspectorContent] view case:', caseId)
}

function handleCaseEdit(caseId: string) {
  console.log('[InspectorContent] edit case:', caseId)
}

function handleCaseDelete(caseId: string) {
  if (!confirm('Are you sure you want to delete this case?')) return
  caseStore.deleteCase(caseId).catch((err: unknown) => {
    console.error('[InspectorContent] delete case failed:', err)
  })
}

function handleCaseSave(req: CreateCaseRequest | UpdateCaseRequest) {
  console.log('[InspectorContent] save case:', req)
}

function handleTriggerSkill(command: string) {
  emit('trigger-skill', command)
}

function handleMemorySelect(id: string) {
  console.log('[InspectorContent] select memory:', id)
}

function handleProjectBack() {
  activeTab.value = 'sessions'
}
</script>

<template>
  <div class="inspector-content">
    <InspectorTabs v-model:active-tab="activeTab">
      <div v-if="activeTab === 'sessions'" class="tab-pane">
        <div class="session-card" v-if="activeSession">
          <div class="session-name">{{ activeSession.name }}</div>
          <div class="session-meta">
            <span class="session-status" :class="activeSession.status">{{ activeSession.status }}</span>
            <span class="session-tokens">{{ activeSession.totalTokens.toLocaleString() }} tokens</span>
          </div>
        </div>
        <div v-else class="placeholder">
          <div class="placeholder-title">Sessions</div>
          <p class="placeholder-hint">
            Session overview and quick navigation. For full session management use the left dock.
          </p>
        </div>
      </div>

      <div v-else-if="activeTab === 'memory'" class="tab-pane">
        <MemoryBrowser @select-memory="handleMemorySelect" />
      </div>

      <div v-else-if="activeTab === 'rag'" class="tab-pane">
        <RAGPreviewPanel :project-id="activeProject?.id || 'default'" />
      </div>

      <div v-else-if="activeTab === 'context'" class="tab-pane">
        <div class="context-subtask-bar" v-if="subTaskOptions.length > 0">
          <label class="context-label">Agent instance</label>
          <select v-model="selectedSubTaskId" class="context-select">
            <option value="">All / root</option>
            <option v-for="opt in subTaskOptions" :key="opt.id" :value="opt.id">{{ opt.label }}</option>
          </select>
        </div>
        <ContextWindowPanel :active-task-id="activeTaskId ?? ''" :sub-task-id="selectedSubTaskId" />
      </div>

      <div v-else-if="activeTab === 'cases'" class="tab-pane">
        <div class="cases-header">
          <h3 class="panel-title">Case Library</h3>
          <span class="case-count">{{ filteredCases.length }}</span>
        </div>
        <CaseFilter
          :selected-tags="selectedTags"
          :selected-category="selectedCategory"
          :all-tags="allTags"
          :all-categories="allCategories"
          @toggle-tag="caseStore.toggleTag"
          @set-category="caseStore.setCategory"
          @clear-filters="caseStore.clearFilters"
        />

        <div v-if="casesLoading" class="cases-loading">Loading...</div>
        <div v-else-if="filteredCases.length === 0" class="cases-empty">
          No cases match the current filters.
        </div>
        <div v-else class="cases-grid">
          <CaseCard
            v-for="c in filteredCases"
            :key="c.id"
            :case-data="c as Case"
            :disabled="false"
            @run="handleCaseRun"
            @view="handleCaseView"
            @toggle-tag="caseStore.toggleTag"
            @edit="handleCaseEdit"
            @delete="handleCaseDelete"
          />
        </div>
      </div>

      <div v-else-if="activeTab === 'agents'" class="tab-pane">
        <AgentConfig class="full-panel" @back="activeTab = 'sessions'" />
      </div>

      <div v-else-if="activeTab === 'project'" class="tab-pane">
        <ProjectConfig class="full-panel" :projects="projects" :active-project="activeProject" @back="handleProjectBack" />
      </div>

      <div v-else-if="activeTab === 'skills'" class="tab-pane">
        <SkillPanel :skills="skills" @trigger="handleTriggerSkill" />
      </div>

      <div v-else-if="activeTab === 'traces'" class="tab-pane">
        <div class="trace-header">
          <h3 class="panel-title">Trace Tree</h3>
          <span class="trace-count">{{ flattenedSpans.length }}</span>
        </div>
        <div v-if="flattenedSpans.length === 0" class="trace-empty">
          No trace spans received yet. Spans are emitted as <code>trace_span</code> events.
        </div>
        <div v-else class="trace-list">
          <div
            v-for="span in flattenedSpans"
            :key="span.span_id"
            class="trace-row"
            :style="{ paddingLeft: `${span.depth * 0.75}rem` }"
            :class="{ 'trace-row--root': span.depth === 0 }"
          >
            <span class="trace-op">{{ span.operation }}</span>
            <span class="trace-agent">{{ span.agent_id }}</span>
            <span class="trace-duration">{{ span.duration_ms }}ms</span>
            <span class="trace-status" :class="span.status">{{ span.status }}</span>
          </div>
        </div>
      </div>
    </InspectorTabs>
  </div>
</template>

<style scoped>
.inspector-content {
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.tab-pane {
  height: 100%;
  overflow-y: auto;
}

.full-panel {
  height: 100%;
  overflow-y: auto;
}

.placeholder {
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: flex-start;
  padding-top: 20%;
  color: var(--text-muted);
  text-align: center;
  gap: var(--space-sm);
}

.placeholder-title {
  font-family: var(--font-display);
  font-size: 1rem;
  font-weight: 600;
  color: var(--text-secondary);
}

.placeholder-hint {
  margin: 0;
  font-size: 0.75rem;
  font-family: var(--font-mono);
  max-width: 240px;
  line-height: 1.5;
}

.session-card {
  padding: var(--space-md);
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  margin: var(--space-md);
}

.session-name {
  font-family: var(--font-display);
  font-size: 1rem;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: var(--space-sm);
}

.session-meta {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
}

.session-status {
  font-family: var(--font-mono);
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  padding: 0.125rem 0.5rem;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-subtle);
  color: var(--text-muted);
}

.session-status.running {
  color: var(--accent-running);
  border-color: rgba(0, 229, 255, 0.25);
  background: rgba(0, 229, 255, 0.08);
}

.session-status.completed {
  color: var(--accent-success);
  border-color: rgba(57, 255, 20, 0.25);
  background: rgba(57, 255, 20, 0.08);
}

.session-status.failed {
  color: var(--accent-danger);
  border-color: rgba(255, 77, 77, 0.25);
  background: rgba(255, 77, 77, 0.08);
}

.session-tokens {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.context-subtask-bar {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  padding: var(--space-sm) var(--space-md);
  border-bottom: 1px solid var(--border-default);
  background: var(--bg-elevated);
}

.context-label {
  font-family: var(--font-display);
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
}

.context-select {
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  padding: 0.25rem 0.5rem;
  font-size: 0.8rem;
  min-width: 8rem;
}

.cases-header,
.trace-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-sm);
}

.panel-title {
  margin: 0;
  font-family: var(--font-display);
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--text-primary);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.case-count,
.trace-count {
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--text-muted);
  background: var(--bg-elevated);
  padding: 2px 8px;
  border-radius: 10px;
}

.cases-loading,
.cases-empty,
.trace-empty {
  padding: var(--space-xl);
  text-align: center;
  color: var(--text-muted);
  font-size: 0.8rem;
}

.cases-grid {
  display: grid;
  grid-template-columns: 1fr;
  gap: var(--space-sm);
}

.trace-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-xs);
}

.trace-row {
  display: grid;
  grid-template-columns: 1fr auto auto auto;
  align-items: center;
  gap: var(--space-sm);
  padding: 0.5rem 0.75rem;
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  font-size: 0.8rem;
}

.trace-row--root {
  background: var(--bg-panel);
  border-color: var(--border-default);
}

.trace-op {
  color: var(--text-primary);
  font-weight: 500;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.trace-agent {
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--text-secondary);
}

.trace-duration {
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--text-muted);
}

.trace-status {
  font-family: var(--font-mono);
  font-size: 0.65rem;
  font-weight: 600;
  text-transform: uppercase;
  padding: 0.125rem 0.375rem;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  border: 1px solid var(--border-subtle);
}

.trace-status.ok,
.trace-status.success {
  color: var(--accent-success);
  border-color: rgba(57, 255, 20, 0.25);
  background: rgba(57, 255, 20, 0.08);
}

.trace-status.error,
.trace-status.failed {
  color: var(--accent-danger);
  border-color: rgba(255, 77, 77, 0.25);
  background: rgba(255, 77, 77, 0.08);
}

@media (min-width: 1280px) {
  .cases-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}
</style>
