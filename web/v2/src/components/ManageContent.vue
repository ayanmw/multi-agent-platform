<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import ManageTabs from './ManageTabs.vue'
import SkillPanel from './SkillPanel.vue'
import TodoPanel from './TodoPanel.vue'
import CaseFilter from './CaseFilter.vue'
import CaseCard from './CaseCard.vue'
import CaseDetailModal from './CaseDetailModal.vue'
import CaseForm from './CaseForm.vue'
import MemoryBrowser from './MemoryBrowser.vue'
import RAGPreviewPanel from './RAGPreviewPanel.vue'
import ContextWindowPanel from './ContextWindowPanel.vue'
import AgentConfig from './AgentConfig.vue'
import ProjectConfig from './ProjectConfig.vue'
import { useCaseStore } from '@/composables/useCaseStore'
import { useToast } from '@/composables/useToast'
import { useSkills } from '@/composables/useSkills'
import { useTaskStore } from '@/composables/useTaskStore'
import { useSessionStore } from '@/composables/useSessionStore'
import { useProjectStore } from '@/composables/useProjectStore'
import { useTraceStore } from '@/composables/useTraceStore'
import type { Case, CreateCaseRequest, UpdateCaseRequest } from '@/types/case'
import type { SpanNode } from '@/composables/useTraceStore'

/**
 * ManageContent — 管理（原 Inspector）面板内容
 *
 * 使用 ManageTabs 切换多个信息面板的容器组件。
 * 已迁移的面板直接渲染真实组件；暂时保留 traces 的最小化实现。
 *
 * 默认 tab 为 memory。
 * Sessions tab 已移除——其信息（当前 session name/status/token）与左侧
 * SessionDock、底部 ContextFlyout 完全重复，不提供额外价值。
 *
 * Emits:
 *   - run-case: 从 Cases tab 运行指定 case
 *   - trigger-skill: 从 Skills tab 触发 skill 命令
 */
const emit = defineEmits<{
  (e: 'run-case', caseId: string): void
  (e: 'trigger-skill', command: string): void
}>()

/** 当前激活的管理 tab。默认 memory。Sessions tab 已移除，不再作为可选项。 */
const props = defineProps<{
  /** 大 Dialog 打开时希望直接定位的 tab（一次性消费）。 */
  initialTab?: string
}>()

const activeTab = ref(props.initialTab || 'memory')

// 兼容历史：若传入的 initialTab 是已移除的 'sessions'，回退到默认 memory。
if (activeTab.value === 'sessions') activeTab.value = 'memory'

// 父级每次打开 Dialog 可能传入新的 initialTab，同步过去。
// 兼容历史：若传入的是已移除的 'sessions'，回退到默认 memory。
watch(
  () => props.initialTab,
  (t) => {
    if (!t) return
    activeTab.value = t === 'sessions' ? 'memory' : t
  },
)

const caseStore = useCaseStore()
const { showError, showInfo } = useToast()
const { filteredCases, allTags, allCategories, selectedTags, selectedCategory, loading: casesLoading } = caseStore
const { skills } = useSkills()
const { activeTaskId, taskCache } = useTaskStore()
const { activeProject } = useProjectStore()
const { activeSession } = useSessionStore()
const traceStore = useTraceStore()

/** 当前在 ContextWindowPanel 中查看的子任务 / Agent 实例 */
const selectedSubTaskId = ref('')

// Case Detail / Form modal 状态：
// - detailVisible / detailCase 驱动 CaseDetailModal，查看 case 详情
// - formVisible / formCase 驱动 CaseForm，caseData 为 null 表示新建
const detailVisible = ref(false)
const detailCase = ref<Case | null>(null)
const formVisible = ref(false)
const formCase = ref<Case | null>(null)

/** 从 store 中按 id 查找 case；store 未提供 getCase，这里本地 find */
function findCase(caseId: string): Case | undefined {
  return caseStore.cases.value.find(c => c.id === caseId)
}

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
  // detail 内点击 Run 时也需要关闭弹窗，再向上层 emit 运行请求
  detailVisible.value = false
  emit('run-case', caseId)
}

function handleCaseView(caseId: string) {
  // 从 Case Card 触发：装载 case 数据并打开 Detail Modal
  detailCase.value = findCase(caseId) ?? null
  detailVisible.value = true
}

function handleCaseEdit(caseId: string) {
  // 从 Case Card 或 Detail Modal 触发：装载 case 数据并打开 Form Modal
  formCase.value = findCase(caseId) ?? null
  formVisible.value = true
  detailVisible.value = false
}

function handleCaseDelete(caseId: string) {
  if (!confirm('Are you sure you want to delete this case?')) return
  caseStore.deleteCase(caseId)
    .then(() => showInfo('Case 已删除'))
    .catch((err: unknown) => {
      console.error('[ManageContent] delete case failed:', err)
      showError(`删除 Case 失败: ${err instanceof Error ? err.message : String(err)}`)
    })
}

// CaseForm @save：联合类型 req，根据 formCase 是否存在断言为 Update / Create
async function handleCaseSave(req: CreateCaseRequest | UpdateCaseRequest) {
  try {
    if (formCase.value) {
      await caseStore.updateCase(formCase.value.id, req as UpdateCaseRequest)
      showInfo('Case 已更新')
    } else {
      await caseStore.createCase(req as CreateCaseRequest)
      showInfo('Case 已创建')
    }
    formVisible.value = false
  } catch (err: unknown) {
    console.error('[ManageContent] save case failed:', err)
    showError(`保存 Case 失败: ${err instanceof Error ? err.message : String(err)}`)
  }
}

function handleTriggerSkill(command: string) {
  emit('trigger-skill', command)
}

function handleMemorySelect(id: string) {
  console.log('[ManageContent] select memory:', id)
}
</script>

<template>
  <div class="manage-content">
    <ManageTabs v-model:active-tab="activeTab">
      <div v-if="activeTab === 'memory'" class="tab-pane tab-pane--flush">
        <MemoryBrowser @select-memory="handleMemorySelect" />
      </div>

      <div v-else-if="activeTab === 'rag'" class="tab-pane">
        <RAGPreviewPanel :project-id="activeProject?.id || 'default'" />
      </div>

      <div v-else-if="activeTab === 'todos'" class="tab-pane">
        <TodoPanel :session-id="activeSession?.id || ''" />
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
          <div class="cases-title-row">
            <h3 class="panel-title">Case Library</h3>
            <span class="case-count">{{ filteredCases.length }}</span>
          </div>
          <!-- 新建 Case 按钮：formCase 置 null 进入 Form Modal 新建模式 -->
          <button
            class="case-new-btn"
            title="新建 Case"
            @click="formCase = null; formVisible = true"
          >+ New Case</button>
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

      <div v-else-if="activeTab === 'agents'" class="tab-pane tab-pane--flush">
        <AgentConfig class="full-panel" />
      </div>

      <div v-else-if="activeTab === 'project'" class="tab-pane tab-pane--flush">
        <ProjectConfig class="full-panel" />
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
    </ManageTabs>

    <!-- Cases tab 的两个 Modal：Detail（只读查看）与 Form（新建/编辑）。
         都通过 Teleport 渲染到 body，这里只负责数据与事件绑定。 -->
    <CaseDetailModal
      :case-data="detailCase"
      :visible="detailVisible"
      @close="detailVisible = false"
      @run="handleCaseRun"
      @edit="handleCaseEdit"
    />
    <CaseForm
      :case-data="formCase"
      :visible="formVisible"
      @close="formVisible = false"
      @save="handleCaseSave"
    />
  </div>
</template>

<style scoped>
.manage-content {
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.tab-pane {
  height: 100%;
  overflow-y: auto;
  /* 默认无 padding：内容面板自带留白时（如 MemoryBrowser）直接全宽铺开，
     避免双层 padding 在窄弹窗里挤压内容。需要留白的面板自行加 padding。 */
  padding: 0;
}

/* 全宽 flush tab 的占位类（保留语义，padding 已统一为 0）。 */
.tab-pane--flush {
  padding: 0;
}

.full-panel {
  height: 100%;
  overflow-y: auto;
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

.cases-title-row {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
}

/* 新建 Case 按钮：与 .context-select 等控件视觉一致，使用 v2 token 而非硬编码颜色 */
.case-new-btn {
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--accent-running);
  font-family: var(--font-display);
  font-size: 0.75rem;
  font-weight: 600;
  padding: 0.25rem 0.625rem;
  cursor: pointer;
  transition: border-color 0.2s, background 0.2s;
}

.case-new-btn:hover {
  border-color: var(--accent-running);
  background: rgba(0, 229, 255, 0.08);
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
