<!-- RAGPreviewPanel.vue — interactive vector recall preview for RAG operations.
     Performs a similarity search against /api/memories/recall and renders
     ranked results with score breakdown.
-->
<script setup lang="ts">
import { ref, computed } from 'vue'

interface RecallResult {
  id: string
  content: string
  score: number
  scope?: string
  type?: string
  tier?: string
}

const props = defineProps<{
  projectId?: string
}>()

const query = ref('')
const results = ref<RecallResult[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const maxResults = ref(10)

const hasResults = computed(() => results.value.length > 0)

function truncate(s: string, maxLen = 200): string {
  if (!s) return ''
  if (s.length <= maxLen) return s
  return s.slice(0, maxLen) + '...'
}

async function handleSearch() {
  const q = query.value.trim()
  if (!q) return
  loading.value = true
  error.value = null
  results.value = []

  const params = new URLSearchParams()
  params.set('query', q)
  params.set('max', String(maxResults.value))
  params.set('project', props.projectId || 'default')

  try {
    const resp = await fetch(`/api/memories/recall?${params.toString()}`)
    if (!resp.ok) throw new Error(`Recall failed: ${resp.status}`)
    const data = (await resp.json()) as { results?: RecallResult[] } | RecallResult[]
    const list = Array.isArray(data) ? data : data.results || []
    // Sort by score descending
    results.value = list.sort((a, b) => (b.score || 0) - (a.score || 0))
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unknown error'
  } finally {
    loading.value = false
  }
}

function scorePercent(score: number): string {
  return `${Math.round((score || 0) * 100)}%`
}
</script>

<template>
  <div class="rag-panel">
    <div class="rag-header">
      <h2 class="rag-title">🔍 RAG Recall Preview</h2>
      <p class="rag-subtitle">Test semantic recall against project memories</p>
    </div>

    <div class="rag-query-bar">
      <input
        v-model="query"
        type="text"
        class="rag-input"
        placeholder="Enter query..."
        @keydown.enter="handleSearch"
      />
      <select v-model="maxResults" class="rag-select">
        <option :value="5">Top 5</option>
        <option :value="10">Top 10</option>
        <option :value="20">Top 20</option>
      </select>
      <button
        class="rag-search-btn"
        :disabled="!query.trim() || loading"
        @click="handleSearch"
      >
        {{ loading ? 'Searching...' : 'Search' }}
      </button>
    </div>

    <div v-if="error" class="rag-error">
      ⚠️ {{ error }}
    </div>

    <div v-if="loading" class="rag-loading">
      <div class="rag-spinner"></div>
      <span>Running vector recall...</span>
    </div>

    <div v-else-if="hasResults" class="rag-results">
      <div class="rag-results-header">
        {{ results.length }} result{{ results.length === 1 ? '' : 's' }}
      </div>
      <div
        v-for="(r, idx) in results"
        :key="r.id"
        class="rag-result-card"
      >
        <div class="rag-result-header">
          <span class="rag-rank">#{{ idx + 1 }}</span>
          <span class="rag-score" :title="`Score: ${r.score}`">
            {{ scorePercent(r.score) }}
          </span>
          <span v-if="r.scope" class="rag-tag scope">{{ r.scope }}</span>
          <span v-if="r.type" class="rag-tag type">{{ r.type }}</span>
          <span class="rag-id">{{ r.id }}</span>
        </div>
        <div class="rag-score-bar-wrap">
          <div class="rag-score-bar" :style="{ width: scorePercent(r.score) }"></div>
        </div>
        <pre class="rag-content">{{ truncate(r.content) }}</pre>
      </div>
    </div>

    <div v-else-if="query && !loading" class="rag-empty">
      No memories recalled. Try a different query.
    </div>

    <div class="rag-breakdown">
      <h4 class="breakdown-title">Score Breakdown</h4>
      <p class="breakdown-note">
        Results are ranked by cosine similarity between the query embedding and each
        memory embedding. Score shown as percentage (score × 100).
      </p>
    </div>
  </div>
</template>

<style scoped>
.rag-panel {
  padding:1.25rem;
  display:flex;
  flex-direction:column;
  gap:1rem;
}

.rag-header {
  display:flex;
  flex-direction:column;
  gap:0.25rem;
}

.rag-title {
  margin:0;
  font-size:1.3rem;
  color:var(--text-primary);
}

.rag-subtitle {
  margin:0;
  font-size:0.85rem;
  color:var(--text-muted);
}

.rag-query-bar {
  display:flex;
  gap:0.5rem;
  align-items:center;
}

.rag-input {
  flex:1;
  min-width:0;
  padding:0.5rem 0.75rem;
  background:var(--bg-elevated);
  border:1px solid var(--border-default);
  border-radius: var(--radius-md);
  color:var(--text-primary);
  font-size:0.875rem;
  outline:none;
}

.rag-input:focus {
  border-color:var(--accent-running);
}

.rag-select {
  padding:0.5rem 0.625rem;
  background:var(--bg-elevated);
  border:1px solid var(--border-default);
  border-radius: var(--radius-md);
  color:var(--text-primary);
  font-size:0.875rem;
  outline:none;
  cursor:pointer;
}

.rag-search-btn {
  padding:0.5rem 1rem;
  background:var(--accent-running);
  border:none;
  border-radius: var(--radius-md);
  color:var(--text-primary);
  font-size:0.875rem;
  cursor:pointer;
  transition:background 0.15s;
  white-space:nowrap;
}

.rag-search-btn:hover:not(:disabled) {
  background:var(--accent-running);
}

.rag-search-btn:disabled {
  opacity:0.6;
  cursor:not-allowed;
}

.rag-error {
  padding:0.75rem;
  background:rgba(231, 76, 60, 0.18);
  color:var(--accent-danger);
  border-radius: var(--radius-md);
  font-size:0.812rem;
}

.rag-loading {
  display:flex;
  align-items:center;
  gap:0.625rem;
  color:var(--text-muted);
  font-size:0.875rem;
}

.rag-spinner {
  width:1.125rem;
  height:1.125rem;
  border:2px solid var(--border-default);
  border-top-color:var(--accent-running);
  border-radius:50%;
  animation:spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform:rotate(360deg); }
}

.rag-results {
  display:flex;
  flex-direction:column;
  gap:0.625rem;
}

.rag-results-header {
  font-size:0.75rem;
  color:var(--text-muted);
  text-transform:uppercase;
  letter-spacing: 0.03125rem;
}

.rag-result-card {
  background:var(--bg-panel);
  border:1px solid var(--border-default);
  border-radius: var(--radius-md);
  padding:0.75rem;
  display:flex;
  flex-direction:column;
  gap:0.5rem;
}

.rag-result-header {
  display:flex;
  align-items:center;
  gap:0.5rem;
  flex-wrap:wrap;
}

.rag-rank {
  font-size:0.75rem;
  color:var(--text-muted);
  min-width:1.750rem;
}

.rag-score {
  font-size:0.812rem;
  font-weight:700;
  color:var(--accent-running);
}

.rag-tag {
  padding:0.125rem 0.375rem;
  border-radius: var(--radius-sm);
  font-size:0.625rem;
  text-transform:uppercase;
  letter-spacing:188rem;
  background:var(--bg-elevated);
  color:var(--text-secondary);
  border:1px solid var(--border-default);
}

.rag-tag.scope {
  background:rgba(127, 191, 127, 0.12);
  border-color:var(--accent-success);
  color:var(--accent-success);
}

.rag-tag.type {
  background:rgba(168, 85, 247, 0.12);
  border-color:var(--accent-tool);
  color:var(--accent-tool);
}

.rag-id {
  margin-left:auto;
  font-size:0.625rem;
  color:var(--text-muted);
  font-family:var(--font-mono, monospace);
}

.rag-score-bar-wrap {
  height:0.25rem;
  background:var(--bg-elevated);
  border-radius: var(--radius-sm);
  overflow:hidden;
}

.rag-score-bar {
  height:100%;
  background:linear-gradient(90deg, var(--accent-running), var(--accent-success));
  border-radius: var(--radius-sm);
  transition:width 0.3s ease;
}

.rag-content {
  margin:0;
  white-space:pre-wrap;
  word-break:break-word;
  font-family:inherit;
  font-size:0.812rem;
  color:var(--text-secondary);
  line-height:1.5;
  max-height:10rem;
  overflow-y:auto;
}

.rag-empty {
  text-align:center;
  padding:2.500rem 1.25rem;
  color:var(--text-muted);
  font-size:0.875rem;
}

.rag-breakdown {
  background:var(--bg-canvas);
  border:1px solid var(--border-default);
  border-radius: var(--radius-md);
  padding:0.75rem;
}

.breakdown-title {
  margin:0 0 0.375rem;
  font-size:0.75rem;
  color:var(--text-secondary);
  text-transform:uppercase;
  letter-spacing: 0.03125rem;
}

.breakdown-note {
  margin:0;
  font-size:0.75rem;
  color:var(--text-muted);
  line-height:1.5;
}
</style>
