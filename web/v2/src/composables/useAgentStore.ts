// useAgentStore — reactive agent configuration store
//
// Manage agent CRUD operations against the backend /api/agents API.
// Provides reactive state (agents list, editing state, loading flags) and
// async actions (load, create, update, delete, test connection).
//
// The backend API:
//   GET    /api/agents       — list all agents
//   POST   /api/agents       — create agent (body: AgentRequest)
//   PUT    /api/agents/{id}  — update agent by ID
//   DELETE /api/agents/{id}  — delete agent by ID
//   GET    /api/tools        — list available tools
import { ref } from 'vue'

// AgentRecord matches the backend's pkg/db AgentRecord struct
export interface AgentRecord {
  id: string
  name: string
  description: string
  system_prompt: string
  model: string
  temperature: number
  max_tokens: number
  api_endpoint: string
  api_key: string
  tools: string[]
  config: Record<string, unknown>
  is_default: boolean
  created_at: string
  updated_at: string
}

// AgentRequest is the JSON body sent to POST/PUT /api/agents
export interface AgentRequest {
  name: string
  description: string
  system_prompt: string
  model: string
  temperature: number
  max_tokens: number
  api_endpoint: string
  api_key: string
  tools: string[]
}

// Default values for a new agent form
export function defaultAgentRequest(): AgentRequest {
  return {
    name: '',
    description: '',
    system_prompt: '',
    model: 'deepseek-v4-flash',
    temperature: 0.7,
    max_tokens: 4096,
    api_endpoint: '',
    api_key: '',
    tools: [],
  }
}

// Available tool names loaded from GET /api/tools
export interface ToolInfo {
  name: string
  description: string
  namespace?: string
  short_name?: string
}

/** Singleton state shared across all consumers */
const agents = ref<AgentRecord[]>([])
const availableTools = ref<ToolInfo[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const showAgentConfig = ref(false)
let initialized = false

export function useAgentStore() {
  // Prevent duplicate init on multiple calls
  if (!initialized) {
    initialized = true
  }

  /** Load all agents from the backend */
  async function loadAgents(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const resp = await fetch('/api/agents')
      if (!resp.ok) {
        throw new Error(`Failed to load agents: ${resp.status}`)
      }
      agents.value = (await resp.json()) as AgentRecord[]
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
    } finally {
      loading.value = false
    }
  }

  /** Load available tools from GET /api/tools */
  async function loadAvailableTools(): Promise<void> {
    try {
      const resp = await fetch('/api/tools')
      if (resp.ok) {
        availableTools.value = (await resp.json()) as ToolInfo[]
      }
    } catch {
      // Non-critical; tools will just be empty
      console.warn('Failed to load available tools')
    }
  }

  /** Create a new agent via POST /api/agents */
  async function createAgent(req: AgentRequest): Promise<AgentRecord> {
    error.value = null
    const resp = await fetch('/api/agents', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    })
    if (!resp.ok) {
      const msg = await resp.text()
      throw new Error(`Failed to create agent: ${resp.status} ${msg}`)
    }
    const created = (await resp.json()) as AgentRecord
    agents.value.unshift(created)
    return created
  }

  /** Update an existing agent via PUT /api/agents/{id} */
  async function updateAgent(id: string, req: AgentRequest): Promise<AgentRecord> {
    error.value = null
    const resp = await fetch(`/api/agents/${encodeURIComponent(id)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    })
    if (!resp.ok) {
      const msg = await resp.text()
      throw new Error(`Failed to update agent: ${resp.status} ${msg}`)
    }
    const updated = (await resp.json()) as AgentRecord
    // Replace the agent in the local list
    const idx = agents.value.findIndex(a => a.id === id)
    if (idx !== -1) {
      agents.value[idx] = updated
    }
    return updated
  }

  /** Delete an agent via DELETE /api/agents/{id} */
  async function deleteAgent(id: string): Promise<void> {
    error.value = null
    const resp = await fetch(`/api/agents/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    })
    if (!resp.ok) {
      const msg = await resp.text()
      throw new Error(`Failed to delete agent: ${resp.status} ${msg}`)
    }
    agents.value = agents.value.filter(a => a.id !== id)
  }

  /** Test an agent's API endpoint and key by sending a minimal chat completion request */
  async function testConnection(endpoint: string, apiKey: string, model: string): Promise<{ ok: boolean; message: string }> {
    const url = endpoint.replace(/\/+$/, '') + '/chat/completions'
    try {
      const resp = await fetch(url, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${apiKey}`,
        },
        body: JSON.stringify({
          model,
          messages: [{ role: 'user', content: 'Hi' }],
          max_tokens: 5,
        }),
        // Use a short timeout — the browser will enforce this
        // signal: AbortSignal.timeout(10000),
      })
      if (resp.ok) {
        return { ok: true, message: 'Connection successful — API responded with 200' }
      }
      const text = await resp.text()
      return { ok: false, message: `API returned ${resp.status}: ${text.slice(0, 200)}` }
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Unknown error'
      return { ok: false, message: `Connection failed: ${msg}` }
    }
  }

  return {
    // Reactive state
    agents,
    availableTools,
    loading,
    error,
    showAgentConfig,

    // Actions
    loadAgents,
    loadAvailableTools,
    createAgent,
    updateAgent,
    deleteAgent,
    testConnection,
  }
}