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
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.rag-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.rag-title {
  margin: 0;
  font-size: 1.3rem;
  color: #e0e0e0;
}

.rag-subtitle {
  margin: 0;
  font-size: 0.85rem;
  color: #888;
}

.rag-query-bar {
  display: flex;
  gap: 8px;
  align-items: center;
}

.rag-input {
  flex: 1;
  min-width: 0;
  padding: 8px 12px;
  background: #2a2a2a;
  border: 1px solid #444;
  border-radius: 6px;
  color: #ddd;
  font-size: 14px;
  outline: none;
}

.rag-input:focus {
  border-color: #4a9eff;
}

.rag-select {
  padding: 8px 10px;
  background: #2a2a2a;
  border: 1px solid #444;
  border-radius: 6px;
  color: #ddd;
  font-size: 14px;
  outline: none;
  cursor: pointer;
}

.rag-search-btn {
  padding: 8px 16px;
  background: #4a9eff;
  border: none;
  border-radius: 6px;
  color: #fff;
  font-size: 14px;
  cursor: pointer;
  transition: background 0.15s;
  white-space: nowrap;
}

.rag-search-btn:hover:not(:disabled) {
  background: #3a8eef;
}

.rag-search-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.rag-error {
  padding: 12px;
  background: #3a1a1a;
  color: #ff6b6b;
  border-radius: 6px;
  font-size: 13px;
}

.rag-loading {
  display: flex;
  align-items: center;
  gap: 10px;
  color: #888;
  font-size: 14px;
}

.rag-spinner {
  width: 18px;
  height: 18px;
  border: 2px solid #333;
  border-top-color: #4a9eff;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.rag-results {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.rag-results-header {
  font-size: 12px;
  color: #888;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.rag-result-card {
  background: #1e1e1e;
  border: 1px solid #333;
  border-radius: 8px;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.rag-result-header {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.rag-rank {
  font-size: 12px;
  color: #888;
  min-width: 28px;
}

.rag-score {
  font-size: 13px;
  font-weight: 700;
  color: #4a9eff;
}

.rag-tag {
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.3px;
  background: #333;
  color: #aaa;
  border: 1px solid #444;
}

.rag-tag.scope {
  background: #2a3a2a;
  border-color: #3a5a3a;
  color: #7fbf7f;
}

.rag-tag.type {
  background: #2a2a3a;
  border-color: #3a3a5a;
  color: #9f9fff;
}

.rag-id {
  margin-left: auto;
  font-size: 10px;
  color: #666;
  font-family: var(--font-mono, monospace);
}

.rag-score-bar-wrap {
  height: 4px;
  background: #333;
  border-radius: 2px;
  overflow: hidden;
}

.rag-score-bar {
  height: 100%;
  background: linear-gradient(90deg, #4a9eff, #7fbf7f);
  border-radius: 2px;
  transition: width 0.3s ease;
}

.rag-content {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: inherit;
  font-size: 13px;
  color: #ccc;
  line-height: 1.5;
  max-height: 160px;
  overflow-y: auto;
}

.rag-empty {
  text-align: center;
  padding: 40px 20px;
  color: #888;
  font-size: 14px;
}

.rag-breakdown {
  background: #1a1a1a;
  border: 1px solid #333;
  border-radius: 8px;
  padding: 12px;
}

.breakdown-title {
  margin: 0 0 6px;
  font-size: 12px;
  color: #aaa;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.breakdown-note {
  margin: 0;
  font-size: 12px;
  color: #666;
  line-height: 1.5;
}
</style>
