<!-- LLMModelManager.vue — Provider 与模型画像综合管理面板

     替代旧的 ModelPricesDialog，提供：
       - Provider 列表：健康状态、上次同步时间、同步按钮
       - 按 Provider 分组的模型表格
       - 模型字段 inline 编辑（display_name / tier / capabilities / prices /
         context/output limits / fallback_model / rate_limit_rpm / avg_latency_ms）
       - 缺失模型（missing=true）显示与恢复

     Usage:
       <LLMModelManager :visible="managerVisible" @update:visible="..." />
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useProviders } from '@/composables/useProviders'
import { useModelPrices, fullModelId, groupByProvider } from '@/composables/useModelPrices'
import type { LLMModel, LLMProvider } from '@/types/llm'

const props = defineProps<{
  visible: boolean
}>()
const emit = defineEmits<{
  'update:visible': [v: boolean]
}>()

const { providers, loading: providersLoading, syncing, syncError, syncProvider, loadProviders } = useProviders()
const { models, loading: modelsLoading, error: modelsError, loadModels, updateModel } = useModelPrices()

const dialogVisible = ref(props.visible)
watch(() => props.visible, v => {
  dialogVisible.value = v
  if (v) {
    loadProviders().catch(() => {})
    loadModels().catch(() => {})
    syncError.value = null
  }
})
watch(dialogVisible, v => emit('update:visible', v))

const groupped = computed(() => groupByProvider(models.value))
const providerNames = computed(() => Array.from(groupped.value.keys()).sort())

// Track which providers are expanded in the grouped view
const expandedProviders = ref<Set<string>>(new Set())
function toggleProvider(name: string) {
  const next = new Set(expandedProviders.value)
  if (next.has(name)) next.delete(name)
  else next.add(name)
  expandedProviders.value = next
}

// Inline editing state
const editingModel = ref<string | null>(null)
const draft = ref<Partial<LLMModel>>({})
const saving = ref<string | null>(null)

function isEditing(m: LLMModel): boolean {
  return editingModel.value === fullModelId(m)
}

function startEdit(m: LLMModel) {
  editingModel.value = fullModelId(m)
  draft.value = {
    display_name: m.display_name,
    tier: m.tier,
    capabilities: [...m.capabilities],
    input_price: m.input_price,
    output_price: m.output_price,
    max_context_window: m.max_context_window,
    max_output_tokens: m.max_output_tokens,
    fallback_model: m.fallback_model,
    rate_limit_rpm: m.rate_limit_rpm,
    avg_latency_ms: m.avg_latency_ms,
    missing: m.missing,
  }
}

function cancelEdit() {
  editingModel.value = null
  draft.value = {}
}

async function saveEdit(m: LLMModel) {
  const id = fullModelId(m)
  saving.value = id
  try {
    await updateModel(m.provider, m.model_id, {
      display_name: draft.value.display_name,
      tier: draft.value.tier,
      // capabilities are sent as string[] (already in LLMModel)
      capabilities: draft.value.capabilities as string[],
      input_price: draft.value.input_price,
      output_price: draft.value.output_price,
      max_context_window: draft.value.max_context_window,
      max_output_tokens: draft.value.max_output_tokens,
      fallback_model: draft.value.fallback_model,
      rate_limit_rpm: draft.value.rate_limit_rpm,
      avg_latency_ms: draft.value.avg_latency_ms,
      missing: draft.value.missing,
    })
    editingModel.value = null
    draft.value = {}
  } finally {
    saving.value = null
  }
}

function formatCapabilities(caps: string[]): string {
  return (caps || []).join(', ')
}

function parseCapabilities(s: string): string[] {
  return s.split(',').map(x => x.trim()).filter(Boolean)
}

function formatDate(iso?: string): string {
  if (!iso) return 'Never'
  const d = new Date(iso)
  return d.toLocaleString()
}

function providerHealthClass(p: LLMProvider): string {
  if (!p.healthy) return 'health-unhealthy'
  if (p.last_sync_at) return 'health-healthy'
  return 'health-unknown'
}

function close() {
  dialogVisible.value = false
}
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="dialogVisible" class="lm-overlay" @click.self="close">
        <div class="lm-dialog">
          <div class="lm-header">
            <h3>🧠 LLM Model Manager</h3>
            <span class="lm-count">{{ models.length }} models · {{ providers.length }} providers</span>
            <button class="lm-close" @click="close" title="Close" aria-label="Close">✕</button>
          </div>

          <div v-if="providersLoading || modelsLoading" class="lm-empty">Loading...</div>
          <div v-else-if="modelsError" class="lm-error">Failed to load models: {{ modelsError }}</div>

          <!-- Provider list -->
          <div class="lm-providers">
            <h4 class="lm-section-title">Providers</h4>
            <div v-if="providers.length === 0" class="lm-hint">No configured providers found.</div>
            <div v-else class="lm-provider-list">
              <div v-for="p in providers" :key="p.name" class="lm-provider-card">
                <div class="lm-provider-main">
                  <span class="lm-provider-name">{{ p.name }}</span>
                  <span class="lm-provider-type">{{ p.type }}</span>
                  <span class="lm-health" :class="providerHealthClass(p)">
                    {{ p.healthy ? (p.last_sync_at ? 'healthy' : 'unknown') : 'unhealthy' }}
                  </span>
                </div>
                <div class="lm-provider-meta">
                  <span v-if="p.last_sync_at" class="lm-sync-time">synced {{ formatDate(p.last_sync_at) }}</span>
                  <span v-if="p.last_sync_error" class="lm-sync-error">{{ p.last_sync_error }}</span>
                </div>
                <button
                  class="lm-sync-btn"
                  :disabled="syncing === p.name"
                  @click="syncProvider(p.name)"
                >
                  {{ syncing === p.name ? 'Syncing...' : '↻ Sync' }}
                </button>
              </div>
            </div>
            <div v-if="syncError" class="lm-error lm-inline">Sync failed: {{ syncError }}</div>
          </div>

          <!-- Models grouped by provider -->
          <div class="lm-models">
            <h4 class="lm-section-title">Models</h4>
            <div v-if="models.length === 0" class="lm-hint">No models available. Sync a provider first.</div>
            <div v-else class="lm-groups">
              <div v-for="name in providerNames" :key="name" class="lm-group">
                <button class="lm-group-toggle" @click="toggleProvider(name)">
                  <span class="lm-caret" :class="{ open: expandedProviders.has(name) }">▶</span>
                  <span class="lm-group-name">{{ name }}</span>
                  <span class="lm-group-count">{{ groupped.get(name)?.length }}</span>
                </button>
                <div v-if="expandedProviders.has(name)" class="lm-group-body">
                  <table class="lm-table">
                    <thead>
                      <tr>
                        <th>ID</th>
                        <th>Display Name</th>
                        <th>Tier</th>
                        <th>Input $/1M</th>
                        <th>Output $/1M</th>
                        <th>Context / Output</th>
                        <th>Fallback</th>
                        <th>Capabilities</th>
                        <th>Actions</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr
                        v-for="m in groupped.get(name)"
                        :key="fullModelId(m)"
                        class="lm-row"
                        :class="{ 'lm-row--missing': m.missing }"
                      >
                        <template v-if="isEditing(m)">
                          <td>{{ fullModelId(m) }}</td>
                          <td><input v-model="draft.display_name" class="lm-input" /></td>
                          <td>
                            <select v-model="draft.tier" class="lm-input lm-select">
                              <option value="free">free</option>
                              <option value="efficient">efficient</option>
                              <option value="lightweight">lightweight</option>
                              <option value="standard">standard</option>
                              <option value="premium">premium</option>
                            </select>
                          </td>
                          <td><input v-model.number="draft.input_price" type="number" step="0.01" min="0" class="lm-input lm-num" /></td>
                          <td><input v-model.number="draft.output_price" type="number" step="0.01" min="0" class="lm-input lm-num" /></td>
                          <td>
                            <input v-model.number="draft.max_context_window" type="number" min="0" class="lm-input lm-num" />
                            /
                            <input v-model.number="draft.max_output_tokens" type="number" min="0" class="lm-input lm-num" />
                          </td>
                          <td><input v-model="draft.fallback_model" class="lm-input" /></td>
                          <td>
                            <input
                              :value="formatCapabilities(draft.capabilities || [])"
                              @input="draft.capabilities = parseCapabilities(($event.target as HTMLInputElement).value)"
                              class="lm-input"
                              placeholder="comma separated"
                            />
                          </td>
                          <td class="lm-actions">
                            <button class="lm-btn lm-btn--save" :disabled="saving === fullModelId(m)" @click="saveEdit(m)">Save</button>
                            <button class="lm-btn lm-btn--cancel" @click="cancelEdit">Cancel</button>
                          </td>
                        </template>
                        <template v-else>
                          <td :title="fullModelId(m)">
                            {{ m.model_id }}
                            <span v-if="m.missing" class="lm-missing-badge">missing</span>
                          </td>
                          <td>{{ m.display_name || m.model_id }}</td>
                          <td><span class="lm-tier" :data-tier="m.tier">{{ m.tier }}</span></td>
                          <td class="lm-mono">{{ m.input_price }}</td>
                          <td class="lm-mono">{{ m.output_price }}</td>
                          <td class="lm-mono">{{ m.max_context_window }} / {{ m.max_output_tokens }}</td>
                          <td class="lm-mono">{{ m.fallback_model || '-' }}</td>
                          <td class="lm-caps">{{ formatCapabilities(m.capabilities) }}</td>
                          <td class="lm-actions">
                            <button class="lm-btn lm-btn--edit" @click="startEdit(m)">Edit</button>
                          </td>
                        </template>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          </div>

          <div class="lm-footer">
            <span class="lm-hint">Models shown are actually available to the Router.</span>
            <button class="lm-btn lm-btn--primary" @click="close">Close</button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.lm-overlay {
  position: fixed;
  inset: 0;
  z-index: 950;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
}

.lm-dialog {
  background: var(--bg-panel, #1e1e2e);
  border: 1px solid var(--border-default, #313244);
  border-radius: 12px;
  width: min(960px, 96vw);
  max-height: 88vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.4);
}

.lm-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border-default, #313244);
}

.lm-header h3 {
  margin: 0;
  font-size: 15px;
  color: var(--text-primary, #cdd6f4);
  font-weight: 600;
}

.lm-count {
  flex: 1;
  font-size: 12px;
  color: var(--text-muted, #6c7086);
}

.lm-close {
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--text-muted, #a6adc8);
  font-size: 16px;
  cursor: pointer;
}

.lm-close:hover {
  background: var(--border-default, #313244);
  color: var(--text-primary, #cdd6f4);
}

.lm-providers,
.lm-models {
  padding: 12px 18px;
  border-bottom: 1px solid var(--border-default, #313244);
}

.lm-section-title {
  margin: 0 0 10px;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted, #a6adc8);
  font-weight: 600;
}

.lm-provider-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 10px;
}

.lm-provider-card {
  background: var(--bg-elevated, #181825);
  border: 1px solid var(--border-default, #313244);
  border-radius: 8px;
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.lm-provider-main {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.lm-provider-name {
  font-weight: 600;
  color: var(--text-primary, #cdd6f4);
}

.lm-provider-type {
  font-size: 11px;
  color: var(--text-muted, #6c7086);
}

.lm-health {
  font-size: 10px;
  text-transform: uppercase;
  padding: 2px 6px;
  border-radius: 8px;
  font-weight: 600;
}

.health-healthy { background: rgba(166, 227, 161, 0.18); color: #a6e3a1; }
.health-unhealthy { background: rgba(243, 139, 168, 0.18); color: #f38ba8; }
.health-unknown { background: rgba(108, 112, 134, 0.18); color: #6c7086; }

.lm-provider-meta {
  font-size: 11px;
  color: var(--text-muted, #6c7086);
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.lm-sync-error {
  color: #f38ba8;
}

.lm-sync-btn {
  align-self: flex-start;
  margin-top: 4px;
  background: #89b4fa;
  border: none;
  color: #1e1e2e;
  border-radius: 6px;
  padding: 5px 12px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
}

.lm-sync-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.lm-models {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

.lm-group {
  margin-bottom: 10px;
}

.lm-group-toggle {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 8px;
  background: var(--bg-elevated, #181825);
  border: 1px solid var(--border-default, #313244);
  border-radius: 8px;
  padding: 8px 12px;
  color: var(--text-primary, #cdd6f4);
  font-size: 13px;
  cursor: pointer;
}

.lm-caret {
  transition: transform 0.15s;
  font-size: 10px;
}

.lm-caret.open { transform: rotate(90deg); }

.lm-group-name { font-weight: 600; }

.lm-group-count {
  font-size: 10px;
  color: var(--text-muted, #6c7086);
  background: var(--bg-panel, #1e1e2e);
  padding: 1px 6px;
  border-radius: 8px;
}

.lm-group-body {
  margin-top: 6px;
  overflow-x: auto;
}

.lm-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}

.lm-table th,
.lm-table td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid var(--border-default, #313244);
  white-space: nowrap;
}

.lm-table th {
  color: var(--text-muted, #a6adc8);
  font-weight: 600;
  text-transform: uppercase;
  font-size: 10px;
  letter-spacing: 0.04em;
}

.lm-row:hover { background: var(--bg-elevated, #181825); }
.lm-row--missing { opacity: 0.7; }

.lm-missing-badge {
  display: inline-block;
  margin-left: 4px;
  font-size: 9px;
  text-transform: uppercase;
  color: #f38ba8;
  border: 1px solid rgba(243, 139, 168, 0.3);
  padding: 1px 4px;
  border-radius: 4px;
}

.lm-tier {
  font-size: 10px;
  text-transform: uppercase;
  padding: 2px 6px;
  border-radius: 8px;
  background: var(--bg-panel, #1e1e2e);
  border: 1px solid var(--border-default, #313244);
}

.lm-tier[data-tier="efficient"] { background: rgba(166, 227, 161, 0.18); color: #a6e3a1; }
.lm-tier[data-tier="standard"] { background: rgba(137, 180, 250, 0.18); color: #89b4fa; }
.lm-tier[data-tier="premium"] { background: rgba(245, 194, 231, 0.18); color: #f5c2e7; }
.lm-tier[data-tier="free"] { background: rgba(108, 112, 134, 0.18); color: #6c7086; }
.lm-tier[data-tier="lightweight"] { background: rgba(249, 226, 175, 0.18); color: #f9e2af; }

.lm-mono { font-family: 'SF Mono', 'Fira Code', monospace; }

.lm-caps { max-width: 160px; overflow: hidden; text-overflow: ellipsis; }

.lm-input {
  background: var(--bg-panel, #1e1e2e);
  border: 1px solid var(--border-default, #313244);
  border-radius: 6px;
  color: var(--text-primary, #cdd6f4);
  padding: 4px 6px;
  font-size: 12px;
  min-width: 60px;
}

.lm-select { min-width: 90px; }
.lm-num { min-width: 60px; width: 70px; }

.lm-actions { display: flex; gap: 6px; }

.lm-btn {
  background: var(--bg-elevated, #181825);
  border: 1px solid var(--border-default, #313244);
  color: var(--text-secondary, #a6adc8);
  border-radius: 6px;
  padding: 4px 10px;
  font-size: 12px;
  cursor: pointer;
}

.lm-btn--primary { background: #89b4fa; color: #1e1e2e; border: none; }
.lm-btn--save { background: rgba(166, 227, 161, 0.18); color: #a6e3a1; border-color: rgba(166, 227, 161, 0.3); }
.lm-btn--edit:hover { background: var(--bg-hover, #313244); color: var(--text-primary, #cdd6f4); }

.lm-empty,
.lm-error,
.lm-hint {
  padding: 12px 18px;
  font-size: 13px;
}

.lm-empty { color: var(--text-muted, #6c7086); text-align: center; }
.lm-error { color: #f38ba8; }
.lm-inline { padding: 6px 0; }
.lm-hint { color: var(--text-muted, #6c7086); font-size: 12px; }

.lm-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 18px;
  border-top: 1px solid var(--border-default, #313244);
}

/* Fade transition */
.fade-enter-active,
.fade-leave-active { transition: opacity 0.2s ease; }
.fade-enter-from,
.fade-leave-to { opacity: 0; }
</style>
