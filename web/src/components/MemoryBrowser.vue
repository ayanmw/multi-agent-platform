<!-- MemoryBrowser.vue — memory browsing and management panel
     Displays a filterable list of memories organized by scope/project/tier,
     with expandable detail view and scope management controls.
-->
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useMemoryStore } from '../composables/useMemoryStore'
import { useProjectStore } from '../composables/useProjectStore'
import type { MemoryItem } from '../composables/useMemoryStore'

const {
  memories, loading, error, filter, stats,
  loadMemories, updateScope, deleteMemory,
} = useMemoryStore()
const { projects, loadProjects } = useProjectStore()

// Expanded memory detail
const expandedId = ref<string | null>(null)
const deleteConfirm = ref<string | null>(null)

// Scope label colors
const scopeColors: Record<string, string> = {
  session: '#3498db',
  project: '#2ecc71',
  global: '#e74c3c',
}

// Type icons
const typeIcons: Record<string, string> = {
  preference: '⭐',
  rule: '📋',
  fact: '📌',
  lesson: '💡',
  reflection: '🔍',
  session_summary: '📝',
}

// Tier labels
const tierLabels: Record<string, string> = {
  consolidated: 'Consolidated',
  semantic: 'Semantic',
}

// Scope labels
const scopeLabels: Record<string, string> = {
  session: 'Session',
  project: 'Project',
  global: 'Global',
}

onMounted(() => {
  loadProjects()
  loadMemories()
})

function toggleExpand(id: string) {
  expandedId.value = expandedId.value === id ? null : id
}

async function handleFilterChange() {
  await loadMemories()
}

async function handleScopeChange(mem: MemoryItem, newScope: string) {
  try {
    await updateScope(mem.id, newScope, mem.session_id)
  } catch {
    // Error handled by store
  }
}

async function handleDelete(mem: MemoryItem) {
  if (deleteConfirm.value === mem.id) {
    await deleteMemory(mem.id)
    deleteConfirm.value = null
    expandedId.value = null
  } else {
    deleteConfirm.value = mem.id
  }
}

function formatDate(dateStr: string | null): string {
  if (!dateStr) return 'N/A'
  return new Date(dateStr).toLocaleString()
}

function truncate(s: string, maxLen: number): string {
  if (s.length <= maxLen) return s
  return s.slice(0, maxLen) + '...'
}
</script>

<template>
  <div class="memory-browser">
    <div class="memory-header">
      <h2>🧠 Memory Browser</h2>
      <div class="memory-stats">
        <span class="stat-badge total">{{ stats.total }} total</span>
        <span v-for="(count, scope) in stats.byScope" :key="scope"
          class="stat-badge" :style="{ borderColor: scopeColors[scope] || '#999' }">
          {{ scopeLabels[scope] || scope }}: {{ count }}
        </span>
      </div>
    </div>

    <!-- Filter bar -->
    <div class="memory-filters">
      <select v-model="filter.project" @change="handleFilterChange" class="filter-select">
        <option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option>
      </select>
      <select v-model="filter.scope" @change="handleFilterChange" class="filter-select">
        <option value="">All Scopes</option>
        <option value="session">Session</option>
        <option value="project">Project</option>
        <option value="global">Global</option>
      </select>
      <select v-model="filter.tier" @change="handleFilterChange" class="filter-select">
        <option value="">All Tiers</option>
        <option value="consolidated">Consolidated</option>
        <option value="semantic">Semantic</option>
      </select>
      <button @click="loadMemories" class="btn-refresh" title="Refresh">🔄</button>
    </div>

    <!-- Error state -->
    <div v-if="error" class="memory-error">
      ⚠️ {{ error }}
      <button @click="loadMemories" class="btn-retry">Retry</button>
    </div>

    <!-- Loading state -->
    <div v-if="loading" class="memory-loading">Loading memories...</div>

    <!-- Empty state -->
    <div v-if="!loading && memories.length === 0" class="memory-empty">
      <p>No memories found.</p>
      <p class="hint">Memories are generated automatically when tasks complete or when context compression runs.</p>
    </div>

    <!-- Memory list -->
    <div class="memory-list">
      <div v-for="mem in memories" :key="mem.id" class="memory-card"
        :class="{ expanded: expandedId === mem.id }">
        <!-- Card header -->
        <div class="memory-card-header" @click="toggleExpand(mem.id)">
          <span class="memory-type-icon">{{ typeIcons[mem.type] || '📄' }}</span>
          <div class="memory-card-main">
            <span class="memory-type">{{ mem.type }}</span>
            <span class="memory-content-preview">{{ truncate(mem.content, 150) }}</span>
          </div>
          <div class="memory-card-tags">
            <span class="tag scope-tag" :style="{ background: scopeColors[mem.scope] || '#999' }">
              {{ scopeLabels[mem.scope] || mem.scope }}
            </span>
            <span class="tag tier-tag">{{ tierLabels[mem.tier] || mem.tier }}</span>
            <span class="tag confidence-tag">{{ Math.round(mem.confidence * 100) }}%</span>
          </div>
        </div>

        <!-- Expanded detail -->
        <div v-if="expandedId === mem.id" class="memory-card-detail">
          <div class="detail-section">
            <h4>Full Content</h4>
            <pre class="detail-content">{{ mem.content }}</pre>
          </div>

          <div class="detail-meta">
            <div class="meta-row">
              <span class="meta-label">ID:</span>
              <span class="meta-value">{{ mem.id }}</span>
            </div>
            <div class="meta-row">
              <span class="meta-label">Project:</span>
              <span class="meta-value">{{ mem.project_id }}</span>
            </div>
            <div class="meta-row" v-if="mem.session_id">
              <span class="meta-label">Session:</span>
              <span class="meta-value">{{ mem.session_id }}</span>
            </div>
            <div class="meta-row">
              <span class="meta-label">Status:</span>
              <span class="meta-value">{{ mem.status }}</span>
            </div>
            <div class="meta-row">
              <span class="meta-label">Confidence:</span>
              <span class="meta-value">{{ Math.round(mem.confidence * 100) }}%</span>
            </div>
            <div class="meta-row">
              <span class="meta-label">Access Count:</span>
              <span class="meta-value">{{ mem.access_count }}</span>
            </div>
            <div class="meta-row" v-if="mem.promotion_reason">
              <span class="meta-label">Promotion:</span>
              <span class="meta-value">{{ mem.promotion_reason }}</span>
            </div>
            <div class="meta-row" v-if="mem.source_task_ids.length > 0">
              <span class="meta-label">Source Tasks:</span>
              <span class="meta-value">{{ mem.source_task_ids.join(', ') }}</span>
            </div>
            <div class="meta-row">
              <span class="meta-label">Last Accessed:</span>
              <span class="meta-value">{{ formatDate(mem.last_accessed) }}</span>
            </div>
            <div class="meta-row">
              <span class="meta-label">Created:</span>
              <span class="meta-value">{{ formatDate(mem.created_at) }}</span>
            </div>
            <div class="meta-row">
              <span class="meta-label">Updated:</span>
              <span class="meta-value">{{ formatDate(mem.updated_at) }}</span>
            </div>
          </div>

          <!-- Actions -->
          <div class="detail-actions">
            <div class="scope-change-group">
              <span class="action-label">Scope:</span>
              <button
                v-for="scope in ['session', 'project', 'global']" :key="scope"
                @click="handleScopeChange(mem, scope)"
                :class="['btn-scope', { active: mem.scope === scope }]"
                :style="mem.scope === scope ? { background: scopeColors[scope], color: '#fff' } : {}"
                :disabled="mem.scope === scope"
              >
                {{ scopeLabels[scope] }}
              </button>
            </div>
            <button
              @click="handleDelete(mem)"
              :class="['btn-delete', { confirm: deleteConfirm === mem.id }]"
            >
              {{ deleteConfirm === mem.id ? '⚠️ Confirm Delete' : '🗑 Delete' }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.memory-browser {
  padding: 20px;
  max-width: 1200px;
  margin: 0 auto;
}

.memory-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
  flex-wrap: wrap;
  gap: 12px;
}

.memory-header h2 {
  margin: 0;
  font-size: 1.5rem;
  color: #e0e0e0;
}

.memory-stats {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.stat-badge {
  padding: 4px 10px;
  border-radius: 12px;
  font-size: 0.8rem;
  background: #2a2a2a;
  border: 2px solid #555;
  color: #aaa;
}

.stat-badge.total {
  border-color: #888;
  color: #fff;
  font-weight: 600;
}

.memory-filters {
  display: flex;
  gap: 8px;
  margin-bottom: 16px;
  align-items: center;
}

.filter-select {
  padding: 6px 12px;
  background: #2a2a2a;
  border: 1px solid #444;
  border-radius: 6px;
  color: #ddd;
  font-size: 0.85rem;
  cursor: pointer;
}

.filter-select:focus {
  outline: none;
  border-color: #3498db;
}

.btn-refresh {
  padding: 6px 10px;
  background: #2a2a2a;
  border: 1px solid #444;
  border-radius: 6px;
  cursor: pointer;
  font-size: 0.9rem;
}

.btn-refresh:hover {
  background: #3a3a3a;
}

.memory-error {
  padding: 16px;
  background: #3a1a1a;
  color: #ff6b6b;
  border-radius: 8px;
  margin-bottom: 16px;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.btn-retry {
  padding: 4px 12px;
  background: #ff6b6b;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
}

.memory-loading {
  text-align: center;
  padding: 40px;
  color: #888;
}

.memory-empty {
  text-align: center;
  padding: 60px 20px;
  color: #888;
}

.memory-empty .hint {
  font-size: 0.85rem;
  color: #666;
  margin-top: 8px;
}

.memory-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.memory-card {
  background: #1e1e1e;
  border: 1px solid #333;
  border-radius: 8px;
  overflow: hidden;
  transition: border-color 0.2s;
}

.memory-card:hover {
  border-color: #555;
}

.memory-card.expanded {
  border-color: #3498db;
}

.memory-card-header {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 12px 16px;
  cursor: pointer;
  user-select: none;
}

.memory-type-icon {
  font-size: 1.2rem;
  flex-shrink: 0;
  margin-top: 2px;
}

.memory-card-main {
  flex: 1;
  min-width: 0;
}

.memory-type {
  display: block;
  font-size: 0.75rem;
  color: #888;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 4px;
}

.memory-content-preview {
  display: block;
  color: #ccc;
  font-size: 0.9rem;
  line-height: 1.4;
  word-break: break-word;
}

.memory-card-tags {
  display: flex;
  gap: 6px;
  flex-shrink: 0;
  flex-wrap: wrap;
}

.tag {
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.3px;
}

.scope-tag {
  color: #fff;
}

.tier-tag {
  background: #333;
  color: #aaa;
  border: 1px solid #555;
}

.confidence-tag {
  background: #333;
  color: #f1c40f;
  border: 1px solid #555;
}

.memory-card-detail {
  padding: 0 16px 16px;
  border-top: 1px solid #333;
  margin-top: 4px;
}

.detail-section {
  margin-bottom: 16px;
}

.detail-section h4 {
  margin: 12px 0 8px;
  color: #aaa;
  font-size: 0.85rem;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.detail-content {
  background: #111;
  color: #ddd;
  padding: 12px;
  border-radius: 6px;
  font-size: 0.85rem;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 300px;
  overflow-y: auto;
}

.detail-meta {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(250px, 1fr));
  gap: 8px;
  margin-bottom: 16px;
}

.meta-row {
  display: flex;
  gap: 8px;
  font-size: 0.8rem;
}

.meta-label {
  color: #888;
  flex-shrink: 0;
  min-width: 90px;
}

.meta-value {
  color: #ccc;
  word-break: break-all;
}

.detail-actions {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding-top: 12px;
  border-top: 1px solid #333;
  flex-wrap: wrap;
  gap: 12px;
}

.scope-change-group {
  display: flex;
  align-items: center;
  gap: 6px;
}

.action-label {
  color: #888;
  font-size: 0.8rem;
  margin-right: 4px;
}

.btn-scope {
  padding: 4px 10px;
  background: #2a2a2a;
  border: 1px solid #555;
  border-radius: 4px;
  color: #aaa;
  font-size: 0.75rem;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-scope:hover:not(:disabled) {
  border-color: #888;
  color: #fff;
}

.btn-scope.active {
  font-weight: 600;
}

.btn-scope:disabled {
  opacity: 0.6;
  cursor: default;
}

.btn-delete {
  padding: 6px 14px;
  background: #2a1a1a;
  border: 1px solid #5a2a2a;
  border-radius: 4px;
  color: #ff6b6b;
  font-size: 0.8rem;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-delete:hover {
  background: #3a1a1a;
  border-color: #ff6b6b;
}

.btn-delete.confirm {
  background: #ff4444;
  color: white;
  border-color: #ff4444;
  animation: pulse 0.6s infinite alternate;
}

@keyframes pulse {
  from { opacity: 0.8; }
  to { opacity: 1; }
}
</style>