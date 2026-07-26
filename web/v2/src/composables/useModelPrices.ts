// useModelPrices — reactive persistent model profile management
//
// Manages listing and editing LLM model profiles against the backend
// /api/models/prices API. Profiles are persisted to the llm_models table.
//
// Backend API:
//   GET  /api/models/prices                    — list all profiles (public-read)
//   PUT  /api/models/prices/{provider}/{model} — update editable fields (auth-protected)
import { ref, computed } from 'vue'
import type { LLMModel } from '../types/llm'

/** PUT request body for /api/models/prices/{provider}/{model}. Omitted fields are ignored. */
export interface ModelProfileUpdate {
  display_name?: string
  tier?: string
  capabilities?: string[]
  input_price?: number
  output_price?: number
  max_context_window?: number
  max_output_tokens?: number
  fallback_model?: string
  rate_limit_rpm?: number
  avg_latency_ms?: number
  missing?: boolean
}

/** Backend response for PUT — echoes the updated model. */
export interface ModelProfileUpdateResponse {
  model: LLMModel
  persistent: boolean
}

/** Singleton state shared across all consumers. */
const models = ref<LLMModel[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const initialized = ref(false)

/** Full identity used in selectors: provider/model_id. */
export function fullModelId(m: LLMModel): string {
  return `${m.provider}/${m.model_id}`
}

/** Models grouped by provider for UI rendering. */
export function groupByProvider(list: LLMModel[]): Map<string, LLMModel[]> {
  const map = new Map<string, LLMModel[]>()
  for (const m of list) {
    const arr = map.get(m.provider) || []
    arr.push(m)
    map.set(m.provider, arr)
  }
  return map
}

/** Load all model profiles from the backend. */
async function loadModels(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const resp = await fetch('/api/models/prices')
    if (!resp.ok) throw new Error(`Failed to load models: ${resp.status}`)
    const data = (await resp.json()) as { items: LLMModel[] }
    models.value = data.items || []
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unknown error'
    throw err
  } finally {
    loading.value = false
  }
}

/** Update a single model's editable fields. */
async function updateModel(provider: string, modelId: string, req: ModelProfileUpdate): Promise<ModelProfileUpdateResponse> {
  error.value = null
  const resp = await fetch(`/api/models/prices/${encodeURIComponent(provider)}/${encodeURIComponent(modelId)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!resp.ok) {
    const msg = `Failed to update model: ${resp.status}`
    error.value = msg
    throw new Error(msg)
  }
  const data = (await resp.json()) as ModelProfileUpdateResponse
  // Optimistically reflect the update in the local list.
  const identity = `${provider}/${modelId}`
  const idx = models.value.findIndex(m => fullModelId(m) === identity)
  if (idx >= 0) {
    models.value[idx] = data.model
  }
  return data
}

/** Composable entry point. */
export function useModelPrices() {
  if (!initialized.value) {
    initialized.value = true
    loadModels().catch(() => {})
  }
  return {
    models,
    loading,
    error,
    loadModels,
    updateModel,
    groupedModels: computed(() => groupByProvider(models.value)),
  }
}
