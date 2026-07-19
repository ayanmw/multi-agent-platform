<script setup lang="ts">
import { ref } from 'vue'
import InspectorTabs from './InspectorTabs.vue'
import SkillPanel from './SkillPanel.vue'
import CaseFilter from './CaseFilter.vue'
import CaseCard from './CaseCard.vue'
import { useCaseStore } from '@/composables/useCaseStore'
import { useSkills } from '@/composables/useSkills'
import type { Case, CreateCaseRequest, UpdateCaseRequest } from '@/types/case'

/**
 * InspectorContent — 右侧 Inspector 面板内容
 *
 * 使用 InspectorTabs 切换多个信息面板的容器组件。
 * 未从 v1 迁移的复杂面板先用占位文本展示，保持整体可运行。
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

function handleCaseRun(caseId: string) {
  emit('run-case', caseId)
}

function handleCaseView(caseId: string) {
  // TODO: Phase 8 — 接入 CaseDetailModal（v1 组件尚未迁移到 v2）
  console.log('[InspectorContent] view case:', caseId)
}

function handleCaseEdit(caseId: string) {
  // TODO: Phase 8 — 接入 CaseForm（v1 组件尚未迁移到 v2）
  console.log('[InspectorContent] edit case:', caseId)
}

function handleCaseDelete(caseId: string) {
  if (!confirm('Are you sure you want to delete this case?')) return
  caseStore.deleteCase(caseId).catch((err: unknown) => {
    console.error('[InspectorContent] delete case failed:', err)
  })
}

function handleCaseSave(req: CreateCaseRequest | UpdateCaseRequest) {
  // TODO: Phase 8 — 接入 CaseForm 的 save 回调（当前 Inspector tab 不处理编辑）
  console.log('[InspectorContent] save case:', req)
}

function handleTriggerSkill(command: string) {
  emit('trigger-skill', command)
}
</script>

<template>
  <div class="inspector-content">
    <InspectorTabs v-model:active-tab="activeTab">
      <div v-if="activeTab === 'sessions'" class="tab-pane">
        <div class="placeholder">
          <div class="placeholder-title">Sessions</div>
          <p class="placeholder-hint">
            Session overview and quick navigation. For full session management use the left dock.
          </p>
        </div>
      </div>

      <div v-else-if="activeTab === 'memory'" class="tab-pane">
        <div class="placeholder">
          <div class="placeholder-title">Memory Browser</div>
          <p class="placeholder-hint">
            TODO: Phase 8 — migrate MemoryBrowser from v1.
          </p>
        </div>
      </div>

      <div v-else-if="activeTab === 'rag'" class="tab-pane">
        <div class="placeholder">
          <div class="placeholder-title">RAG Preview</div>
          <p class="placeholder-hint">
            TODO: Phase 8 — migrate RAGPreviewPanel from v1.
          </p>
        </div>
      </div>

      <div v-else-if="activeTab === 'context'" class="tab-pane">
        <div class="placeholder">
          <div class="placeholder-title">Context Window</div>
          <p class="placeholder-hint">
            TODO: Phase 8 — migrate ContextWindowPanel from v1.
          </p>
        </div>
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
        <div class="placeholder">
          <div class="placeholder-title">Agent Configuration</div>
          <p class="placeholder-hint">
            TODO: Phase 8 — migrate AgentConfig from v1.
          </p>
        </div>
      </div>

      <div v-else-if="activeTab === 'project'" class="tab-pane">
        <div class="placeholder">
          <div class="placeholder-title">Project Settings</div>
          <p class="placeholder-hint">
            TODO: Phase 8 — migrate ProjectConfig from v1.
          </p>
        </div>
      </div>

      <div v-else-if="activeTab === 'skills'" class="tab-pane">
        <SkillPanel :skills="skills" @trigger="handleTriggerSkill" />
      </div>

      <div v-else-if="activeTab === 'traces'" class="tab-pane">
        <div class="placeholder">
          <div class="placeholder-title">Trace Tree</div>
          <p class="placeholder-hint">
            TODO: Phase 8 — migrate TraceTreePanel from v1.
          </p>
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

.cases-header {
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

.case-count {
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--text-muted);
  background: var(--bg-elevated);
  padding: 2px 8px;
  border-radius: 10px;
}

.cases-loading,
.cases-empty {
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

@media (min-width: 1280px) {
  .cases-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}
</style>
