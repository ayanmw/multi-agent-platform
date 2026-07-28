<script setup lang="ts">
import { computed, ref } from 'vue'
import { useSkills, type SkillFilter } from '@/composables/useSkills'
import { useToast } from '@/composables/useToast'
import SkillDetailModal from './SkillDetailModal.vue'
import SkillForm from './SkillForm.vue'
import type { Skill, SkillScope, SkillSource } from '@/types/skill'

/**
 * SkillManager — 完整 Skill 管理面板。
 *
 * 提供列表/搜索/过滤、启用开关、查看详情、新建/编辑/删除 local_db skill。
 * local_file 只读，built_in 不可编辑。
 *
 * Emits:
 *   - trigger-skill(id): 直接启用并发送该 skill。
 */
const emit = defineEmits<{
  (e: 'trigger-skill', id: string): void
}>()

const { skills, loading, error, enabledIds, filteredSkills, deleteSkill, toggleSkill, refresh, loadSkills } = useSkills()
const { showError, showInfo } = useToast()

const searchQuery = ref('')
const sourceFilter = ref<SkillSource | ''>('')
const scopeFilter = ref<SkillScope | ''>('')

const availableSources: { value: SkillSource | ''; label: string }[] = [
  { value: '', label: 'All sources' },
  { value: 'built_in', label: 'Built-in' },
  { value: 'local_file', label: 'Local file' },
  { value: 'local_db', label: 'Local DB' },
]

const availableScopes: { value: SkillScope | ''; label: string }[] = [
  { value: '', label: 'All scopes' },
  { value: 'global', label: 'Global' },
  { value: 'project', label: 'Project' },
  { value: 'session', label: 'Session' },
]

const displaySkills = computed(() => {
  const q = searchQuery.value.trim()
  return filteredSkills.value({
    source: sourceFilter.value || undefined,
    scope: scopeFilter.value || undefined,
    q: q || undefined,
  })
})

const detailSkill = ref<Skill | null>(null)
const formSkill = ref<Skill | null>(null)

function openDetail(skill: Skill) {
  detailSkill.value = skill
}

function openNewForm() {
  formSkill.value = null
}

function openEditForm(skill: Skill) {
  if (skill.source === 'local_file') {
    showError('Local file skill 不可编辑，请直接修改源文件')
    return
  }
  formSkill.value = skill
}

async function handleDelete(skill: Skill) {
  if (!confirm(`确认删除 skill「${skill.display_name || skill.id}」?`)) return
  try {
    await deleteSkill(skill.id)
    showInfo('Skill 已删除')
    if (detailSkill.value?.id === skill.id) detailSkill.value = null
    if (formSkill.value?.id === skill.id) formSkill.value = null
  } catch (err) {
    showError(err instanceof Error ? err.message : '删除失败')
  }
}

async function handleToggle(skill: Skill) {
  try {
    await toggleSkill(skill.id)
  } catch (err) {
    showError(err instanceof Error ? err.message : '切换状态失败')
  }
}

function triggerSkill(id: string) {
  // 向上发送纯 skill id,App.vue 会负责 enable 并预填充发送框
  emit('trigger-skill', id)
}

function sourceClass(source: SkillSource): string {
  switch (source) {
    case 'built_in': return 'source--built-in'
    case 'local_file': return 'source--local-file'
    case 'local_db': return 'source--local-db'
    default: return 'source--other'
  }
}

function sourceLabel(source: SkillSource): string {
  switch (source) {
    case 'built_in': return 'Built-in'
    case 'local_file': return 'Local file'
    case 'local_db': return 'Local DB'
    default: return source
  }
}

function scopeLabel(scope: SkillScope): string {
  return scope || 'global'
}

function formatTime(ts: number): string {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  return d.toLocaleString()
}

function clearFilters() {
  searchQuery.value = ''
  sourceFilter.value = ''
  scopeFilter.value = ''
  loadSkills({})
}
</script>

<template>
  <div class="skill-manager">
    <header class="skill-header">
      <div class="skill-title-group">
        <h3 class="panel-title">Skills</h3>
        <span class="skill-count">{{ displaySkills.length }}</span>
      </div>
      <button class="skill-new-btn" @click="openNewForm">+ New Skill</button>
    </header>

    <div class="skill-filters">
      <input
        v-model="searchQuery"
        type="text"
        class="skill-search"
        placeholder="Search skills..."
        @input="loadSkills({ q: searchQuery.trim(), source: sourceFilter || undefined, scope: scopeFilter || undefined })"
      />
      <select v-model="sourceFilter" class="skill-select" @change="loadSkills({ source: sourceFilter || undefined, scope: scopeFilter || undefined })">
        <option v-for="opt in availableSources" :key="opt.value || 'all'" :value="opt.value">{{ opt.label }}</option>
      </select>
      <select v-model="scopeFilter" class="skill-select" @change="loadSkills({ scope: scopeFilter || undefined, source: sourceFilter || undefined })">
        <option v-for="opt in availableScopes" :key="opt.value || 'all'" :value="opt.value">{{ opt.label }}</option>
      </select>
      <button class="skill-refresh-btn" title="刷新" @click="refresh">↻</button>
      <button class="skill-clear-btn" title="清除过滤" @click="clearFilters">Clear</button>
    </div>

    <div v-if="loading" class="skill-loading">Loading...</div>
    <div v-else-if="error" class="skill-error">{{ error }}</div>
    <div v-else-if="displaySkills.length === 0" class="skill-empty">
      No skills match the current filters.
    </div>
    <div v-else class="skill-grid">
      <div
        v-for="skill in displaySkills"
        :key="skill.id"
        class="skill-card"
        :class="{ 'skill-card--enabled': enabledIds.has(skill.id) }"
      >
        <div class="skill-card-header">
          <div class="skill-card-name">{{ skill.display_name || skill.id }}</div>
          <span class="skill-card-source" :class="sourceClass(skill.source)">{{ sourceLabel(skill.source) }}</span>
        </div>
        <div class="skill-card-id">{{ skill.id }}</div>
        <div class="skill-card-desc">{{ skill.description }}</div>
        <div v-if="skill.tags.length" class="skill-card-tags">
          <span v-for="tag in skill.tags" :key="tag" class="skill-tag">{{ tag }}</span>
        </div>
        <div class="skill-card-meta">
          <span class="skill-scope">{{ scopeLabel(skill.scope) }}</span>
          <span class="skill-state" :class="'state--' + skill.state">{{ skill.state }}</span>
        </div>
        <div class="skill-card-actions">
          <label class="skill-switch">
            <input
              type="checkbox"
              :checked="enabledIds.has(skill.id)"
              @change="handleToggle(skill)"
            />
            <span class="skill-switch-slider" />
            <span class="skill-switch-label">{{ enabledIds.has(skill.id) ? 'On' : 'Off' }}</span>
          </label>
          <button class="skill-action-btn" @click="openDetail(skill)">View</button>
          <button v-if="skill.source === 'built_in' || skill.source === 'local_db'" class="skill-action-btn" @click="openEditForm(skill)">
            {{ skill.source === 'built_in' ? 'Fork' : 'Edit' }}
          </button>
          <button v-if="skill.source === 'local_db'" class="skill-action-btn skill-action-btn--danger" @click="handleDelete(skill)">Delete</button>
        </div>
      </div>
    </div>

    <SkillDetailModal
      :skill="detailSkill"
      @close="detailSkill = null"
      @edit="openEditForm(detailSkill!)"
      @trigger="triggerSkill"
    />

    <SkillForm
      :skill="formSkill"
      @close="formSkill = null"
      @saved="formSkill = null; refresh()"
    />
  </div>
</template>

<style scoped>
.skill-manager {
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: var(--space-md);
}

.skill-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-sm);
  flex-shrink: 0;
}

.skill-title-group {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
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

.skill-count {
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--text-muted);
  background: var(--bg-elevated);
  padding: 2px 8px;
  border-radius: 10px;
}

.skill-new-btn {
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

.skill-new-btn:hover {
  border-color: var(--accent-running);
  background: rgba(0, 229, 255, 0.08);
}

.skill-filters {
  display: flex;
  gap: var(--space-sm);
  margin-bottom: var(--space-md);
  flex-shrink: 0;
  flex-wrap: wrap;
}

.skill-search {
  flex: 1;
  min-width: 8rem;
  background: var(--bg-canvas);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  padding: 0.375rem 0.625rem;
  font-size: 0.8rem;
  font-family: var(--font-mono);
}

.skill-select {
  background: var(--bg-canvas);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  padding: 0.375rem 0.5rem;
  font-size: 0.8rem;
  min-width: 7rem;
}

.skill-refresh-btn,
.skill-clear-btn {
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  font-size: 0.75rem;
  padding: 0.25rem 0.5rem;
  cursor: pointer;
  transition: border-color 0.15s, color 0.15s;
}

.skill-refresh-btn:hover,
.skill-clear-btn:hover {
  border-color: var(--accent-running);
  color: var(--accent-running);
}

.skill-loading,
.skill-error,
.skill-empty {
  padding: var(--space-xl);
  text-align: center;
  color: var(--text-muted);
  font-size: 0.8rem;
}

.skill-error {
  color: var(--accent-danger);
}

.skill-grid {
  display: grid;
  grid-template-columns: 1fr;
  gap: var(--space-sm);
  overflow-y: auto;
}

@media (min-width: 1280px) {
  .skill-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}

.skill-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-xs);
  padding: var(--space-sm) var(--space-md);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  transition: border-color 0.15s, background 0.15s;
}

.skill-card--enabled {
  border-color: rgba(57, 255, 20, 0.25);
  background: rgba(57, 255, 20, 0.04);
}

.skill-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-sm);
}

.skill-card-name {
  font-family: var(--font-display);
  font-weight: 600;
  color: var(--text-primary);
  font-size: 0.9rem;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-card-source {
  flex-shrink: 0;
  font-size: 0.65rem;
  font-family: var(--font-mono);
  text-transform: uppercase;
  padding: 2px 6px;
  border-radius: 4px;
  border: 1px solid var(--border-subtle);
}

.source--built-in { color: var(--accent-running); border-color: rgba(0, 229, 255, 0.25); }
.source--local-file { color: var(--accent-warning); border-color: rgba(255, 184, 0, 0.25); }
.source--local-db { color: var(--accent-skill, #ff6b35); border-color: rgba(255, 107, 53, 0.25); }
.source--other { color: var(--text-muted); }

.skill-card-id {
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--text-muted);
}

.skill-card-desc {
  color: var(--text-secondary);
  font-size: 0.78rem;
  line-height: 1.4;
  min-height: 1.2rem;
}

.skill-card-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.skill-tag {
  font-size: 0.65rem;
  color: var(--text-secondary);
  background: var(--bg-panel);
  padding: 2px 6px;
  border-radius: 4px;
  border: 1px solid var(--border-subtle);
}

.skill-card-meta {
  display: flex;
  gap: var(--space-sm);
  font-size: 0.7rem;
  color: var(--text-muted);
  font-family: var(--font-mono);
}

.skill-state {
  text-transform: uppercase;
  font-weight: 600;
}

.state--enabled { color: var(--accent-success); }
.state--disabled { color: var(--text-muted); }
.state--invalid { color: var(--accent-danger); }

.skill-card-actions {
  display: flex;
  gap: var(--space-sm);
  align-items: center;
  margin-top: var(--space-xs);
}

.skill-switch {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  font-size: 0.7rem;
  color: var(--text-muted);
}

.skill-switch input {
  display: none;
}

.skill-switch-slider {
  width: 34px;
  height: 18px;
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: 9px;
  position: relative;
  transition: background 0.2s, border-color 0.2s;
}

.skill-switch-slider::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 12px;
  height: 12px;
  background: var(--text-muted);
  border-radius: 50%;
  transition: transform 0.2s, background 0.2s;
}

.skill-switch input:checked + .skill-switch-slider {
  background: rgba(57, 255, 20, 0.12);
  border-color: rgba(57, 255, 20, 0.4);
}

.skill-switch input:checked + .skill-switch-slider::after {
  transform: translateX(16px);
  background: var(--accent-success);
}

.skill-action-btn {
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  padding: 4px 10px;
  cursor: pointer;
  transition: all 0.15s;
}

.skill-action-btn:hover {
  border-color: var(--accent-running);
  color: var(--accent-running);
}

.skill-action-btn--danger:hover {
  border-color: var(--accent-danger);
  color: var(--accent-danger);
}
</style>
