// useModelPrices — reactive model price management
//
// Manages viewing and editing LLM model pricing profiles against the backend
// /api/models/prices API. Prices are advisory-only (used by CostTracker to
// compute USD cost from token usage) but must be non-zero so /api/costs
// returns meaningful totals.
//
// The backend API:
//   GET  /api/models/prices         — list all profiles (public-read)
//   PUT  /api/models/prices/{model} — update InputPrice/OutputPrice (write, auth-protected)
//
// Price edits are runtime-only: they overwrite the in-memory ModelRegistry
// entry and reset to DefaultProfiles on server restart. The GET response
// carries `persistent: false` and a `note` to make this clear; we surface
// the note to the UI so users know to re-apply edits after a restart.
import { ref } from 'vue'

/** Single model price row returned by GET /api/models/prices. */
export interface ModelPriceItem {
  name: string
  provider: string
  tier: string                 // free | efficient | lightweight | standard | premium
  input_price: number          // USD per 1M input tokens
  output_price: number         // USD per 1M output tokens
  max_context_window: number
  max_output_tokens: number
  fallback_model: string
  capabilities: string[]
}

/** PUT request body for /api/models/prices/{model}. Omitted fields are ignored. */
export interface ModelPriceUpdate {
  input_price?: number
  output_price?: number
}

/** Backend response for PUT — echoes the updated model plus advisory warnings. */
export interface ModelPriceUpdateResponse {
  model: ModelPriceItem
  warnings: string[]
  persistent: boolean
  note: string
}

/** Singleton state shared across all consumers (dialog opens from multiple places). */
const prices = ref<ModelPriceItem[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
/** Server-side note about runtime-only persistence, shown in the UI once loaded. */
const persistenceNote = ref<string>('')

/** Load all model profiles from the backend. Safe to call repeatedly. */
async function loadModelPrices(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const resp = await fetch('/api/models/prices')
    if (!resp.ok) throw new Error(`Failed to load model prices: ${resp.status}`)
    const data = await resp.json() as { items: ModelPriceItem[]; note?: string }
    prices.value = data.items || []
    persistenceNote.value = data.note || ''
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unknown error'
    throw err
  } finally {
    loading.value = false
  }
}

/** Update a single model's price(s). Returns the server's update response. */
async function updateModelPrice(model: string, req: ModelPriceUpdate): Promise<ModelPriceUpdateResponse> {
  error.value = null
  const resp = await fetch(`/api/models/prices/${encodeURIComponent(model)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!resp.ok) {
    const msg = `Failed to update price: ${resp.status}`
    error.value = msg
    throw new Error(msg)
  }
  const data = (await resp.json()) as ModelPriceUpdateResponse
  // Optimistically reflect the update in the local list so the UI updates
  // without requiring a full reload.
  const idx = prices.value.findIndex(p => p.name === model)
  if (idx >= 0) {
    prices.value[idx] = data.model
  }
  return data
}

/** Composable entry point — returns the shared state and the two API helpers. */
export function useModelPrices() {
  return {
    prices,
    loading,
    error,
    persistenceNote,
    loadModelPrices,
    updateModelPrice,
  }
}
