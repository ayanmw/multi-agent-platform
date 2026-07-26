// useProviders — reactive LLM provider management
//
// Manages listing configured providers and triggering manual model discovery sync.
//
// Backend API:
//   GET  /api/providers            — list providers (public-read)
//   POST /api/providers/{name}/sync — manual sync (auth-protected)
import { ref } from 'vue'
import type { LLMProvider } from '../types/llm'

/** Shared reactive state across all consumers. */
const providers = ref<LLMProvider[]>([])
const loading = ref(false)
const syncing = ref<string | null>(null)
const error = ref<string | null>(null)
const syncError = ref<string | null>(null)
const initialized = ref(false)

/** Load all configured providers from the backend. */
async function loadProviders(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const resp = await fetch('/api/providers')
    if (!resp.ok) throw new Error(`Failed to load providers: ${resp.status}`)
    const data = (await resp.json()) as { providers: LLMProvider[] }
    providers.value = data.providers || []
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unknown error'
    throw err
  } finally {
    loading.value = false
  }
}

/** Trigger manual sync for a named provider. Returns discovered model IDs. */
async function syncProvider(name: string): Promise<string[]> {
  syncError.value = null
  syncing.value = name
  try {
    const resp = await fetch(`/api/providers/${encodeURIComponent(name)}/sync`, {
      method: 'POST',
    })
    const data = (await resp.json()) as { model_ids?: string[]; error?: string; status?: string }
    if (!resp.ok) {
      throw new Error(data.error || `Failed to sync provider: ${resp.status}`)
    }
    // Refresh provider list so last_sync_at/error are up to date.
    await loadProviders().catch(() => {})
    return data.model_ids || []
  } catch (err) {
    const msg = err instanceof Error ? err.message : 'Unknown error'
    syncError.value = msg
    throw err
  } finally {
    syncing.value = null
  }
}

/** Composable entry point. */
export function useProviders() {
  if (!initialized.value) {
    initialized.value = true
    loadProviders().catch(() => {})
  }
  return {
    providers,
    loading,
    syncing,
    error,
    syncError,
    loadProviders,
    syncProvider,
  }
}
