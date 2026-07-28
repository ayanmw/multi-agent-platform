<script setup lang="ts">
import { computed } from 'vue'
import type { Skill, SkillScope, SkillSource } from '@/types/skill'

interface Props {
  skill: Skill | null
}

const props = defineProps<Props>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'edit'): void
  (e: 'trigger', id: string): void
}>()

const s = computed(() => props.skill)

const isReadOnly = computed(() => s.value?.source === 'local_file')
const isBuiltIn = computed(() => s.value?.source === 'built_in')
const canEdit = computed(() => s.value && (s.value.source === 'local_db' || s.value.is_local_editable))

const tabs = ['Overview', 'Templates', 'Parameters', 'Metadata'] as const
type Tab = typeof tabs[number]
const activeTab = defineModel<Tab>('activeTab', { default: 'Overview' })

function formatJSON(obj: unknown): string {
  try {
    return JSON.stringify(obj, null, 2)
  } catch {
    return String(obj)
  }
}

function formatTime(ts: number): string {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
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
</script>

<template>
  <Teleport to="body">
    <Transition name="skill-modal">
      <div v-if="s" class="skill-detail-overlay" @click.self="emit('close')">
        <div class="skill-detail-panel" role="dialog" aria-modal="true">
          <header class="skill-detail-header">
            <div>
              <h3 class="skill-detail-title">{{ s.display_name || s.id }}</h3>
              <div class="skill-detail-subtitle">{{ s.id }} · {{ sourceLabel(s.source) }} · {{ scopeLabel(s.scope) }}</div>
            </div>
            <button class="skill-detail-close" aria-label="关闭" @click="emit('close')">×</button>
          </header>

          <div class="skill-detail-tabs">
            <button
              v-for="tab in tabs"
              :key="tab"
              class="skill-detail-tab"
              :class="{ active: activeTab === tab }"
              @click="activeTab = tab"
            >
              {{ tab }}
            </button>
          </div>

          <div class="skill-detail-body">
            <template v-if="activeTab === 'Overview'">
              <div class="detail-section">
                <label>Description</label>
                <p class="detail-text">{{ s.description || '(no description)' }}</p>
              </div>
              <div v-if="s.tags.length" class="detail-section">
                <label>Tags</label>
                <div class="detail-tags">
                  <span v-for="tag in s.tags" :key="tag" class="detail-tag">{{ tag }}</span>
                </div>
              </div>
              <div class="detail-section">
                <label>State</label>
                <div class="detail-state" :class="'state--' + s.state">{{ s.state }}</div>
              </div>
              <div v-if="s.authors?.length" class="detail-section">
                <label>Authors</label>
                <div class="detail-mono">{{ s.authors.join(', ') }}</div>
              </div>
            </template>

            <template v-else-if="activeTab === 'Templates'">
              <div v-if="s.templates.length === 0" class="detail-empty">No templates.</div>
              <div v-for="tmpl in s.templates" :key="tmpl.name" class="detail-section">
                <div class="detail-section-title">
                  <span>{{ tmpl.name }}</span>
                  <span v-if="tmpl.is_required" class="detail-badge">required</span>
                </div>
                <pre class="detail-code">{{ tmpl.content }}</pre>
                <div v-if="tmpl.variables.length" class="detail-mono">Variables: {{ tmpl.variables.join(', ') }}</div>
              </div>
            </template>

            <template v-else-if="activeTab === 'Parameters'">
              <div v-if="s.parameters.length === 0" class="detail-empty">No parameters.</div>
              <div v-for="p in s.parameters" :key="p.name" class="detail-section">
                <div class="detail-section-title">
                  <span>{{ p.name }}</span>
                  <span class="detail-badge">{{ p.type }}</span>
                  <span v-if="p.required" class="detail-badge">required</span>
                </div>
                <div class="detail-text">{{ p.description || '(no description)' }}</div>
                <div v-if="p.default !== undefined" class="detail-mono">Default: {{ formatJSON(p.default) }}</div>
              </div>
            </template>

            <template v-else-if="activeTab === 'Metadata'">
              <div class="detail-section">
                <label>Source URL</label>
                <div class="detail-mono">{{ s.source_url || '-' }}</div>
              </div>
              <div class="detail-section">
                <label>Project ID</label>
                <div class="detail-mono">{{ s.project_id || '-' }}</div>
              </div>
              <div class="detail-section">
                <label>Workspace Dir</label>
                <div class="detail-mono">{{ s.workspace_dir || '-' }}</div>
              </div>
              <div class="detail-section">
                <label>Version</label>
                <div class="detail-mono">{{ s.version }}</div>
              </div>
              <div class="detail-section">
                <label>Created / Updated</label>
                <div class="detail-mono">{{ formatTime(s.created_at) }} / {{ formatTime(s.updated_at) }}</div>
              </div>
              <div v-if="s.invalid_reason" class="detail-section">
                <label>Invalid Reason</label>
                <div class="detail-error">{{ s.invalid_reason }}</div>
              </div>
            </template>
          </div>

          <footer class="skill-detail-footer">
            <div v-if="isReadOnly" class="detail-hint">Local file skill 只读，请直接编辑 SKILL.md 源文件。</div>
            <div v-else-if="isBuiltIn && !canEdit" class="detail-hint">内置 skill 不可编辑。</div>
            <div v-else-if="isBuiltIn && canEdit" class="detail-hint">已被 fork 为 local_db shadow，可编辑。</div>
            <div class="skill-detail-actions">
              <button class="skill-detail-btn" @click="emit('trigger', s.id)">Trigger</button>
              <button v-if="canEdit" class="skill-detail-btn skill-detail-btn--primary" @click="emit('edit')">{{ isBuiltIn ? 'Fork / Edit' : 'Edit' }}</button>
            </div>
          </footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.skill-detail-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.72);
  backdrop-filter: blur(3px);
  z-index: 220;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}

.skill-detail-panel {
  width: 90vw;
  max-width: 720px;
  height: 80vh;
  background: var(--bg-canvas);
  border: 1px solid var(--border-default);
  border-radius: 14px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-shadow: 0 30px 90px rgba(0, 0, 0, 0.7);
}

.skill-detail-header {
  flex-shrink: 0;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-md);
  padding: var(--space-md) var(--space-lg);
  border-bottom: 1px solid var(--border-default);
  background: var(--bg-elevated);
}

.skill-detail-title {
  margin: 0;
  font-family: var(--font-display);
  font-size: 1rem;
  color: var(--text-primary);
}

.skill-detail-subtitle {
  margin-top: 4px;
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--text-muted);
}

.skill-detail-close {
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: 6px;
  color: var(--text-secondary);
  font-size: 1.2rem;
  width: 32px;
  height: 32px;
  cursor: pointer;
}

.skill-detail-close:hover {
  color: var(--text-primary);
  border-color: var(--accent-running);
}

.skill-detail-tabs {
  flex-shrink: 0;
  display: flex;
  border-bottom: 1px solid var(--border-default);
  background: var(--bg-elevated);
}

.skill-detail-tab {
  flex: 1;
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  color: var(--text-muted);
  font-family: var(--font-display);
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  padding: 0.625rem;
  cursor: pointer;
  transition: color 0.15s, border-color 0.15s;
}

.skill-detail-tab:hover,
.skill-detail-tab.active {
  color: var(--accent-running);
  border-bottom-color: var(--accent-running);
}

.skill-detail-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-md) var(--space-lg);
  display: flex;
  flex-direction: column;
  gap: var(--space-md);
}

.detail-section {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.detail-section label,
.detail-section-title {
  font-family: var(--font-display);
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  color: var(--text-muted);
  letter-spacing: 0.04em;
}

.detail-section-title {
  display: flex;
  align-items: center;
  gap: 6px;
}

.detail-text {
  margin: 0;
  color: var(--text-secondary);
  font-size: 0.82rem;
  line-height: 1.5;
}

.detail-mono {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--text-secondary);
  word-break: break-all;
}

.detail-code {
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  padding: var(--space-sm);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.75rem;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 12rem;
  overflow-y: auto;
}

.detail-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.detail-tag,
.detail-badge {
  font-size: 0.65rem;
  padding: 2px 6px;
  border-radius: 4px;
  border: 1px solid var(--border-subtle);
  color: var(--text-secondary);
}

.detail-badge {
  color: var(--accent-running);
  border-color: rgba(0, 229, 255, 0.25);
}

.detail-state {
  display: inline-block;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  padding: 2px 8px;
  border-radius: 4px;
  border: 1px solid var(--border-subtle);
  width: fit-content;
}

.state--enabled { color: var(--accent-success); border-color: rgba(57, 255, 20, 0.25); }
.state--disabled { color: var(--text-muted); }
.state--invalid { color: var(--accent-danger); border-color: rgba(255, 77, 77, 0.25); }

.detail-empty {
  color: var(--text-muted);
  font-size: 0.8rem;
  text-align: center;
  padding: var(--space-xl);
}

.detail-error {
  color: var(--accent-danger);
  font-size: 0.8rem;
}

.skill-detail-footer {
  flex-shrink: 0;
  padding: var(--space-sm) var(--space-lg);
  border-top: 1px solid var(--border-default);
  background: var(--bg-elevated);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-md);
}

.detail-hint {
  font-size: 0.72rem;
  color: var(--text-muted);
  font-style: italic;
}

.skill-detail-actions {
  display: flex;
  gap: var(--space-sm);
}

.skill-detail-btn {
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  font-family: var(--font-display);
  font-size: 0.75rem;
  font-weight: 600;
  padding: 6px 14px;
  cursor: pointer;
  transition: all 0.15s;
}

.skill-detail-btn:hover {
  border-color: var(--accent-running);
  color: var(--accent-running);
}

.skill-detail-btn--primary {
  color: var(--accent-running);
  border-color: var(--accent-running);
  background: rgba(0, 229, 255, 0.08);
}

.skill-modal-enter-active,
.skill-modal-leave-active {
  transition: opacity 0.2s ease;
}

.skill-modal-enter-from,
.skill-modal-leave-to {
  opacity: 0;
}

@media (max-width: 767px) {
  .skill-detail-overlay {
    padding: 0;
  }
  .skill-detail-panel {
    width: 100vw;
    height: 100vh;
    border-radius: 0;
  }
}
</style>
