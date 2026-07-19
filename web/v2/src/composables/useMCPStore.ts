// useMCPStore — reactive MCP server management
//
// Manages the lifecycle of external MCP (Model Context Protocol) servers against
// the backend /api/mcp/servers API and exposes a shared reactive state for UI
// components such as MCPServerDialog.
//
// Backend API:
//   GET    /api/mcp/servers              — list managed servers + load status
//   POST   /api/mcp/servers              — add a dynamic server
//   POST   /api/mcp/servers/:id/enable   — enable and connect
//   POST   /api/mcp/servers/:id/disable  — disable and disconnect
//   DELETE /api/mcp/servers/:id          — remove a dynamic server
//   GET    /api/mcp/markets              — list registered marketplaces
//   GET    /api/mcp/markets/:name/servers — list packages in a marketplace
//   POST   /api/mcp/markets/:name/servers/:id/install — install package as server
//
// All mutations reload the server list so the UI reflects the latest state.

import { ref } from 'vue'
import { useWebSocket } from './useWebSocket'
import { useAgentStore } from './useAgentStore'
import { useToast } from './useToast'
import type { AgentEvent } from '@/types/events'

/** Transport type supported by the MCP manager. */
export type MCPTransport = 'stdio' | 'sse'

/** Server configuration payload nested inside ManagedServer. */
export interface MCPServerConfig {
  name: string
  transport: MCPTransport
  command?: string
  args?: string[]
  endpoint?: string
  environment?: Record<string, string>
  enabled: boolean
}

/** Managed server as returned by the backend REST API. */
export interface ManagedMCPServer {
  id: string
  source: 'static' | 'db' | 'market'
  config: MCPServerConfig
  enabled: boolean
  created_at: string
  updated_at: string
  loaded?: boolean
  load_err?: string
}

/** Full GET /api/mcp/servers response. */
export interface MCPServersResponse {
  servers: ManagedMCPServer[]
}

/** Marketplace metadata as returned by the backend REST API. */
export interface MCPMarket {
  name: string
  display_name: string
}

/** Marketplace package summary as returned by the backend REST API. */
export interface MCPMarketPackage {
  id: string
  name: string
  description: string
  version?: string
  transport: MCPTransport
  source_url?: string
}

/** GET /api/mcp/markets response. */
export interface MCPMarketsResponse {
  markets: MCPMarket[]
}

/** GET /api/mcp/markets/:name/servers response. */
export interface MCPMarketServersResponse {
  market: string
  servers: MCPMarketPackage[]
}

/** Full POST /api/mcp/servers response. */
export interface MCPServerCreateResponse {
  server: ManagedMCPServer
}

/** Form state used by the create / edit dialog. */
export interface MCPServerForm {
  id: string
  name: string
  transport: MCPTransport
  command: string
  args: string
  endpoint: string
  environment: string
  enabled: boolean
}

/** Singleton state shared across all consumers */
const servers = ref<ManagedMCPServer[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
let wsUnsubscribe: (() => void) | null = null
let lastToolCount = 0

/** Load all managed MCP servers from the backend */
async function loadServers(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const resp = await fetch('/api/mcp/servers')
    if (!resp.ok) throw new Error(`Failed to load MCP servers: ${resp.status}`)
    const data = (await resp.json()) as MCPServersResponse
    servers.value = data.servers || []
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unknown error'
    throw err
  } finally {
    loading.value = false
  }
}

/** Create a new dynamic MCP server. */
async function createServer(form: MCPServerForm): Promise<ManagedMCPServer> {
  error.value = null
  const cfg = buildConfigFromForm(form)
  try {
    const resp = await fetch('/api/mcp/servers', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: form.id, config: cfg, enabled: form.enabled }),
    })
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({} as Record<string, unknown>))
      throw new Error((body.error as string) || `Failed to create MCP server: ${resp.status}`)
    }
    const data = (await resp.json()) as MCPServerCreateResponse
    await loadServers()
    return data.server
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unknown error'
    throw err
  }
}

/** Enable a managed MCP server. */
async function enableServer(id: string): Promise<void> {
  error.value = null
  try {
    const resp = await fetch(`/api/mcp/servers/${encodeURIComponent(id)}/enable`, {
      method: 'POST',
    })
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({} as Record<string, unknown>))
      throw new Error((body.error as string) || `Failed to enable MCP server: ${resp.status}`)
    }
    await loadServers()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unknown error'
    throw err
  }
}

/** Disable a managed MCP server. */
async function disableServer(id: string): Promise<void> {
  error.value = null
  try {
    const resp = await fetch(`/api/mcp/servers/${encodeURIComponent(id)}/disable`, {
      method: 'POST',
    })
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({} as Record<string, unknown>))
      throw new Error((body.error as string) || `Failed to disable MCP server: ${resp.status}`)
    }
    await loadServers()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unknown error'
    throw err
  }
}

/** Remove a dynamic MCP server. Static servers cannot be removed via API. */
async function deleteServer(id: string): Promise<void> {
  error.value = null
  try {
    const resp = await fetch(`/api/mcp/servers/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    })
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({} as Record<string, unknown>))
      throw new Error((body.error as string) || `Failed to delete MCP server: ${resp.status}`)
    }
    await loadServers()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unknown error'
    throw err
  }
}

/** List registered marketplace providers. */
async function listMarkets(): Promise<MCPMarket[]> {
  const resp = await fetch('/api/mcp/markets')
  if (!resp.ok) throw new Error(`Failed to load MCP markets: ${resp.status}`)
  const data = (await resp.json()) as MCPMarketsResponse
  return data.markets || []
}

/** List packages available in a marketplace. */
async function listMarketServers(marketName: string): Promise<MCPMarketPackage[]> {
  const resp = await fetch(`/api/mcp/markets/${encodeURIComponent(marketName)}/servers`)
  if (!resp.ok) throw new Error(`Failed to load market servers: ${resp.status}`)
  const data = (await resp.json()) as MCPMarketServersResponse
  return data.servers || []
}

/** Install a marketplace package as a managed MCP server. */
async function installFromMarket(marketName: string, pkgId: string): Promise<ManagedMCPServer> {
  const resp = await fetch(
    `/api/mcp/markets/${encodeURIComponent(marketName)}/servers/${encodeURIComponent(pkgId)}/install`,
    { method: 'POST' }
  )
  if (!resp.ok) {
    const body = await resp.json().catch(() => ({} as Record<string, unknown>))
    throw new Error((body.error as string) || `Failed to install package: ${resp.status}`)
  }
  const data = (await resp.json()) as MCPServerCreateResponse
  await loadServers()
  return data.server
}

/** Convert the dialog form fields into the nested ServerConfig shape the API expects. */
function buildConfigFromForm(form: MCPServerForm): MCPServerConfig {
  const cfg: MCPServerConfig = {
    name: form.name || form.id,
    transport: form.transport,
    enabled: form.enabled,
  }
  if (form.transport === 'stdio') {
    cfg.command = form.command
    cfg.args = form.args
      .split('\n')
      .map(s => s.trim())
      .filter(Boolean)
  } else if (form.transport === 'sse') {
    cfg.endpoint = form.endpoint
  }
  if (form.environment.trim()) {
    cfg.environment = parseEnv(form.environment)
  }
  return cfg
}

/** Parse 'KEY=VALUE' lines into a record. */
function parseEnv(s: string): Record<string, string> {
  const env: Record<string, string> = {}
  for (const line of s.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) continue
    const idx = trimmed.indexOf('=')
    if (idx === -1) continue
    env[trimmed.slice(0, idx).trim()] = trimmed.slice(idx + 1).trim()
  }
  return env
}

/** Default empty form for the create dialog. */
export function defaultMCPServerForm(): MCPServerForm {
  return {
    id: '',
    name: '',
    transport: 'stdio',
    command: '',
    args: '',
    endpoint: '',
    environment: '',
    enabled: true,
  }
}

/** Composable entry point — returns shared state and mutation helpers. */
export function useMCPStore() {
  // Wire WebSocket listener exactly once. When the backend broadcasts
  // mcp_tools_changed we refresh the server list and the available tool
  // palette so agent configuration always shows current MCP tools.
  if (!wsUnsubscribe) {
    const { onEvent } = useWebSocket()
    const { loadAvailableTools } = useAgentStore()
    const { showInfo } = useToast()
    wsUnsubscribe = onEvent((evt: AgentEvent) => {
      if (evt.type !== 'mcp_tools_changed') return
      // Refresh MCP server list so the dialog shows latest load states.
      loadServers().catch(() => {})
      // Refresh available tools for AgentConfig / future tasks.
      loadAvailableTools().catch(() => {})
      // Toast only when the tool set actually changed.
      const count = (evt.data.tool_count as number) ?? 0
      if (count !== lastToolCount) {
        lastToolCount = count
        showInfo(`MCP 工具已更新，当前可用 ${count} 个工具`)
      }
    })
  }

  return {
    servers,
    loading,
    error,
    loadServers,
    createServer,
    enableServer,
    disableServer,
    deleteServer,
    listMarkets,
    listMarketServers,
    installFromMarket,
    defaultMCPServerForm,
  }
}
