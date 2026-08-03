<!-- AgentConfig — agent configuration management page
     Renders inside the Manage dialog (no back button — the dialog itself closes the view).

     Features:
       - Agent list table with name, model, temperature, tools, created_at
       - Create/Edit form with all agent fields
       - Delete with confirmation dialog
       - Test connection button to verify API endpoint/key
-->
<script setup lang="ts">
import { ref, computed, onMounted, watch, defineComponent, type PropType } from 'vue'
import { useAgentStore, type AgentRecord, type AgentRequest, defaultAgentRequest, type ToolInfo } from '../composables/useAgentStore'
import { useModelPrices, fullModelId } from '../composables/useModelPrices'
import { useToast } from '@/composables/useToast'
import type { LLMModel } from '../types/llm'

/**
 * ModelDropdown — small local reusable dropdown for picking an LLM model
 *
 * Data flow:
 *   - props.modelValue holds the currently selected model identity (provider/model_id)
 *   - props.availableModels supplies the selectable options
 *   - Typing in the filter narrows the list; pressing Enter when exactly one
 *     candidate remains auto-selects it; clicking an item selects it explicitly
 */
interface ModelDropdownProps {
  modelValue: string
  availableModels: LLMModel[]
  label?: string
  placeholder?: string
}

const ModelDropdown = defineComponent({
  // 未使用 interface 仅作类型注释；保留契约说明
  /* ModelDropdown props: modelValue, availableModels, label?, placeholder? */
  props: {
    modelValue: { type: String, required: true },
    availableModels: { type: Array as PropType<LLMModel[]>, required: true },
    label: { type: String, default: '' },
    placeholder: { type: String, default: 'Filter models...' },
  },
  emits: ['update:modelValue'],
  setup(props, { emit }) {
    // 过滤框初始为空 —— 默认展示全部可选模型，当前选中项通过列表高亮（active class）指示。
    // 关键修复：不要把 filter 预填为当前 model 值，也不要在 modelValue 变化时回写 filter，
    // 否则当模型标识是多级前缀（如 default/cbcn/deepseek-v4-flash）时，子串过滤只会命中自身一条，
    // 下拉列表塌缩成单项，用户无法切换到其它模型。
    const filter = ref('')

    const filteredModels = computed(() => {
      const q = filter.value.trim().toLowerCase()
      if (!q) return props.availableModels
      return props.availableModels.filter(m =>
        fullModelId(m).toLowerCase().includes(q) ||
        (m.display_name || '').toLowerCase().includes(q)
      )
    })

    function selectModel(identity: string) {
      emit('update:modelValue', identity)
    }

    function onKeydown(e: KeyboardEvent) {
      if (e.key === 'Enter') {
        if (filteredModels.value.length === 1) {
          selectModel(fullModelId(filteredModels.value[0]))
        }
      }
    }

    return { filter, filteredModels, selectModel, onKeydown }
  },
  template: `
    <div class="model-dropdown">
      <label v-if="label" class="form-label">{{ label }}</label>
      <input
        v-model="filter"
        type="text"
        class="form-input model-dropdown-filter"
        :placeholder="placeholder"
        @keydown="onKeydown"
      />
      <div v-if="filteredModels.length" class="model-dropdown-list">
        <div
          v-for="m in filteredModels"
          :key="fullModelId(m)"
          class="model-dropdown-item"
          :class="{ active: fullModelId(m) === modelValue }"
          @click="selectModel(fullModelId(m))"
        >
          {{ fullModelId(m) }}{{ m.display_name && m.display_name !== m.model_id ? ' (' + m.display_name + ')' : '' }}
        </div>
      </div>
      <div v-else-if="filter.trim()" class="model-dropdown-empty">No models match</div>
    </div>
  `,
})

const MODEL_TIERS = ['free', 'efficient', 'lightweight', 'standard', 'premium'] as const

// Permission definitions for UI: key maps to backend field name, label + risk shown to user
interface PermissionDef {
  key: keyof AgentRequest['config']['permissions']
  label: string
  risk: 'low' | 'medium' | 'high'
  description: string
}

const PERMISSIONS: PermissionDef[] = [
  { key: 'allow_network', label: 'Allow Network', risk: 'low', description: 'HTTP requests via web_search / web_research / MCP' },
  { key: 'allow_file_write', label: 'Allow File Write', risk: 'low', description: 'Create or overwrite files in the workspace' },
  { key: 'allow_file_delete', label: 'Allow File Delete', risk: 'medium', description: 'Delete files in the workspace' },
  { key: 'allow_shell', label: 'Allow Shell', risk: 'medium', description: 'Execute shell commands' },
  { key: 'allow_shell_dangerous', label: 'Allow Dangerous Shell', risk: 'high', description: 'Dangerous commands (e.g. rm -rf, force push)' },
]

const riskClass = (risk: PermissionDef['risk']) => {
  switch (risk) {
    case 'low': return 'risk-low'
    case 'medium': return 'risk-medium'
    case 'high': return 'risk-high'
  }
}

const {
  agents,
  availableTools,
  loading,
  error,
  loadAgents,
  loadAvailableTools,
  createAgent,
  updateAgent,
  deleteAgent,
  testConnection,
} = useAgentStore()

const { models: availableModels, loadModels: loadAvailableModels } = useModelPrices()

// Whether the current form is in auto_route mode
const isAutoRoute = computed(() => form.value.model_mode === 'auto_route')


// Reactive ref for show/hide API key toggle
const showApiKey = ref(false)

// Form state
const showForm = ref(false)
const editingId = ref<string | null>(null)
const form = ref<AgentRequest>(defaultAgentRequest())
const formError = ref<string | null>(null)
const saving = ref(false)

// Delete confirmation
const deleteTarget = ref<AgentRecord | null>(null)
const showDeleteConfirm = ref(false)
const deleting = ref(false)

// Test connection state
const testing = ref(false)
const testResult = ref<{ ok: boolean; message: string } | null>(null)

// ---- Search & pagination (N1-05) ----
const { showError, showInfo } = useToast()

/** 搜索关键字：按 name / description / model 模糊匹配 */
const searchText = ref('')

/** 每页条数，默认 10 */
const pageSize = ref(10)

/** 当前页码（1-based） */
const currentPage = ref(1)

/**
 * 由 is_default 派生的角色标签。
 * 白盒哲学：is_default 是 agents 表中真实持久化的字段，这里只是把它翻译成
 * 人类可读的「角色」分类（系统默认 / 自定义），不引入任何非持久化状态。
 */
function roleOf(agent: AgentRecord): string {
  return agent.is_default ? 'Default' : 'Custom'
}

/** 客户端搜索：过滤 name / description / model 包含关键字的 agent */
const filteredAgents = computed<AgentRecord[]>(() => {
  const q = searchText.value.trim().toLowerCase()
  if (!q) return agents.value
  return agents.value.filter(a =>
    (a.name || '').toLowerCase().includes(q) ||
    (a.description || '').toLowerCase().includes(q) ||
    (a.model || '').toLowerCase().includes(q),
  )
})

/** 总页数（至少 1 页，避免除零） */
const totalPages = computed(() => {
  if (pageSize.value <= 0) return 1
  return Math.max(1, Math.ceil(filteredAgents.value.length / pageSize.value))
})

/**
 * 当前页切片。页码越界时夹紧到合法区间，保证翻页后不会出现空列表。
 */
const pagedAgents = computed<AgentRecord[]>(() => {
  const tp = totalPages.value
  if (currentPage.value > tp) currentPage.value = tp
  if (currentPage.value < 1) currentPage.value = 1
  const start = (currentPage.value - 1) * pageSize.value
  return filteredAgents.value.slice(start, start + pageSize.value)
})

// 搜索关键字变化时回到第一页，避免停留在越界页
watch(searchText, () => { currentPage.value = 1 })

// ---- Enable / disable toggle (启停, N1-05) ----
const togglingId = ref<string | null>(null)

/**
 * 切换 agent 的启用/禁用状态，经 PUT /api/agents/{id} 持久化 enabled 字段。
 * 系统默认 agent（is_default）不可停用，避免破坏平台基本功能力。
 */
async function toggleEnabled(agent: AgentRecord) {
  if (agent.is_default) return
  togglingId.value = agent.id
  try {
    // PUT 整体覆盖 agent，需从 AgentRecord 重建完整 AgentRequest。
    const req: AgentRequest = {
      name: agent.name,
      description: agent.description || '',
      system_prompt: agent.system_prompt || '',
      model: agent.model || '',
      preferred_model: agent.preferred_model || '',
      preferred_tier: agent.preferred_tier || 'standard',
      model_mode: agent.model_mode || 'single_model',
      allow_fallback: agent.allow_fallback ?? true,
      max_cost_usd: agent.max_cost_usd ?? 0,
      temperature: agent.temperature ?? 0.7,
      max_tokens: agent.max_tokens ?? 4096,
      api_endpoint: agent.api_endpoint || '',
      api_key: agent.api_key || '',
      tools: agent.tools ? [...agent.tools] : [],
      config: (agent.config as unknown as AgentRequest['config']) || {
        permissions: {
          allow_network: false,
          allow_file_write: false,
          allow_file_delete: false,
          allow_shell: false,
          allow_shell_dangerous: false,
        },
      },
      enabled: !agent.enabled,
    }
    await updateAgent(agent.id, req)
    showInfo(`${agent.name} 已${req.enabled ? '启用' : '停用'}`)
  } catch (err: unknown) {
    console.error('[AgentConfig] toggle enabled failed:', err)
    showError(`切换启用状态失败: ${err instanceof Error ? err.message : String(err)}`)
  } finally {
    togglingId.value = null
  }
}

// Computed: is the form in edit mode?
const isEditing = computed(() => editingId.value !== null)

// 按 namespace 对工具分组，方便展示与全选操作
const toolsByNamespace = computed(() => {
  const groups: Record<string, ToolInfo[]> = {}
  for (const t of availableTools.value) {
    const ns = t.namespace || '(global)'
    if (!groups[ns]) groups[ns] = []
    groups[ns].push(t)
  }
  return groups
})

const allToolNames = computed(() => availableTools.value.map(t => t.name))

const allSelected = computed(() => {
  if (allToolNames.value.length === 0) return false
  return allToolNames.value.every(name => form.value.tools.includes(name))
})

/** 全选或取消全选所有工具 */
function toggleSelectAll() {
  if (allSelected.value) {
    form.value.tools = []
  } else {
    form.value.tools = [...allToolNames.value]
  }
}

onMounted(() => {
  loadAgents().catch(() => {})
  loadAvailableTools().catch(() => {})
  loadAvailableModels().catch(() => {})
})

// Resolve display label for a model identity (provider/model_id)
function modelDisplayLabel(identity: string): string {
  if (!identity) return ''
  const m = availableModels.value.find(x => fullModelId(x) === identity)
  if (m) return `${fullModelId(m)}${m.display_name && m.display_name !== m.model_id ? ` (${m.display_name})` : ''}`
  return identity
}

// ---- Form handlers ----

/** Open the form for creating a new agent */
function openCreate() {
  editingId.value = null
  form.value = defaultAgentRequest()
  formError.value = null
  showForm.value = true
}

/** Open the form for editing an existing agent */
function openEdit(agent: AgentRecord) {
  editingId.value = agent.id
  const perms = agent.config?.permissions as Record<string, boolean> | undefined
  form.value = {
    name: agent.name,
    description: agent.description || '',
    system_prompt: agent.system_prompt || '',
    model: agent.model || '',
    preferred_model: agent.preferred_model || '',
    preferred_tier: agent.preferred_tier || 'standard',
    model_mode: agent.model_mode || 'single_model',
    allow_fallback: agent.allow_fallback ?? true,
    enabled: agent.enabled ?? true,
    max_cost_usd: agent.max_cost_usd ?? 0,
    temperature: agent.temperature ?? 0.7,
    max_tokens: agent.max_tokens ?? 4096,
    api_endpoint: agent.api_endpoint || '',
    api_key: agent.api_key || '',
    tools: agent.tools ? [...agent.tools] : [],
    config: {
      permissions: {
        allow_network: perms?.allow_network ?? false,
        allow_file_write: perms?.allow_file_write ?? false,
        allow_file_delete: perms?.allow_file_delete ?? false,
        allow_shell: perms?.allow_shell ?? false,
        allow_shell_dangerous: perms?.allow_shell_dangerous ?? false,
      },
    },
  }
  formError.value = null
  showForm.value = true
}

/** Close the form without saving */
function closeForm() {
  showForm.value = false
  editingId.value = null
  formError.value = null
}

/** Toggle a tool in the form's tools array */
function toggleTool(toolName: string) {
  const idx = form.value.tools.indexOf(toolName)
  if (idx === -1) {
    form.value.tools.push(toolName)
  } else {
    form.value.tools.splice(idx, 1)
  }
}

/** Validate and save the form (create or update)
 * 仅在 single_model 模式下要求 model 必填，避免用户忘记选模型。 */
async function handleSave() {
  formError.value = null
  if (!form.value.name.trim()) {
    formError.value = 'Name is required'
    return
  }
  if (form.value.model_mode === 'single_model' && !form.value.model) {
    formError.value = 'Model is required in Single Model mode'
    return
  }
  saving.value = true
  try {
    if (editingId.value) {
      await updateAgent(editingId.value, form.value)
    } else {
      await createAgent(form.value)
    }
    showForm.value = false
    editingId.value = null
  } catch (err) {
    formError.value = err instanceof Error ? err.message : 'Save failed'
  } finally {
    saving.value = false
  }
}

// ---- Delete handlers ----

/** Show the delete confirmation dialog */
function confirmDelete(agent: AgentRecord) {
  if (agent.is_default) {
    return // Cannot delete default agent
  }
  deleteTarget.value = agent
  showDeleteConfirm.value = true
}

/** Cancel the delete confirmation */
function cancelDelete() {
  deleteTarget.value = null
  showDeleteConfirm.value = false
}

/** Execute the delete */
async function handleDelete() {
  if (!deleteTarget.value) return
  deleting.value = true
  try {
    await deleteAgent(deleteTarget.value.id)
    showDeleteConfirm.value = false
    deleteTarget.value = null
  } catch (err) {
    formError.value = err instanceof Error ? err.message : 'Delete failed'
  } finally {
    deleting.value = false
  }
}

// ---- Test connection ----

/** Test the API endpoint and key from the form */
async function handleTestConnection() {
  testing.value = true
  testResult.value = null
  try {
    testResult.value = await testConnection(
      form.value.api_endpoint,
      form.value.api_key,
      form.value.model,
    )
  } catch (err) {
    testResult.value = {
      ok: false,
      message: err instanceof Error ? err.message : 'Test failed',
    }
  } finally {
    testing.value = false
  }
}

/** Format a date string for display */
function formatDate(iso: string): string {
  if (!iso) return '-'
  const d = new Date(iso)
  return d.toLocaleString()
}
</script>

<template>
  <div class="agent-config">
    <!-- Header -->
    <div class="config-header">
      <h2 class="config-title">⚙ Agent Configuration</h2>
      <button class="btn-add" @click="openCreate">+ New Agent</button>
    </div>

    <!-- Search & pagination toolbar (N1-05) -->
    <div class="agent-toolbar">
      <input
        v-model="searchText"
        type="text"
        class="agent-search"
        placeholder="Search by name / description / model..."
        aria-label="Search agents"
      />
      <label class="page-size-label">
        Per page
        <select v-model.number="pageSize" class="page-size-select" aria-label="Agents per page">
          <option :value="5">5</option>
          <option :value="10">10</option>
          <option :value="20">20</option>
          <option :value="50">50</option>
        </select>
      </label>
      <span class="agent-count">{{ filteredAgents.length }} agent(s)</span>
    </div>

    <!-- Loading state -->
    <div v-if="loading" class="loading-area">
      <div class="loading-spinner"></div>
      <div class="loading-text">Loading agents...</div>
    </div>

    <!-- Error banner -->
    <div v-if="error" class="error-banner">{{ error }}</div>

    <!-- Agent list table -->
    <div v-if="!loading && !error && filteredAgents.length === 0" class="empty-state">
      <div class="empty-icon">🤖</div>
      <h3>{{ searchText.trim() ? 'No agents match your search' : 'No agents configured' }}</h3>
      <p>{{ searchText.trim() ? 'Try a different keyword.' : 'Create your first agent to get started.' }}</p>
    </div>

    <div v-else-if="!loading && !error" class="agent-table-wrapper">
      <table class="agent-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Role</th>
            <th>Model</th>
            <th>Temp</th>
            <th>Tools</th>
            <th>Status</th>
            <th>Created</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="agent in pagedAgents" :key="agent.id" class="agent-row">
            <td class="cell-name">
              <div class="agent-name">{{ agent.name }}</div>
              <div v-if="agent.description" class="agent-desc">{{ agent.description }}</div>
            </td>
            <td class="cell-role">
              <span class="role-badge" :class="agent.is_default ? 'role-default' : 'role-custom'">{{ roleOf(agent) }}</span>
            </td>
            <td class="cell-model">{{ agent.model || '-' }}</td>
            <td class="cell-temp">{{ agent.temperature ?? '-' }}</td>
            <td class="cell-tools">
              <span v-if="agent.tools && agent.tools.length > 0" class="tool-badges">
                <span v-for="t in agent.tools" :key="t" class="tool-badge">{{ t }}</span>
              </span>
              <span v-else class="text-muted">-</span>
            </td>
            <td class="cell-status">
              <button
                class="status-toggle"
                :class="agent.enabled ? 'status-on' : 'status-off'"
                :disabled="agent.is_default || togglingId === agent.id"
                :title="agent.is_default ? '系统默认 agent 不可停用' : (agent.enabled ? '点击停用' : '点击启用')"
                @click="toggleEnabled(agent)"
              >
                <span class="status-dot" :class="{ 'dot-pulse': togglingId === agent.id }"></span>
                {{ agent.enabled ? 'Enabled' : 'Disabled' }}
              </button>
            </td>
            <td class="cell-date">{{ formatDate(agent.created_at) }}</td>
            <td class="cell-actions">
              <button class="btn-action btn-edit" @click="openEdit(agent)" title="Edit" aria-label="Edit">✏</button>
              <button
                class="btn-action btn-delete"
                @click="confirmDelete(agent)"
                :title="agent.is_default ? 'Default agent cannot be deleted' : 'Delete'"
                :aria-label="agent.is_default ? 'Default agent cannot be deleted' : 'Delete'"
                :disabled="agent.is_default"
                :style="agent.is_default ? { opacity: '0.3', cursor: 'not-allowed' } : {}"
              >🗑</button>
            </td>
          </tr>
        </tbody>
      </table>

      <!-- Pagination controls (N1-05) -->
      <div v-if="totalPages > 1" class="pager">
        <button
          class="pager-btn"
          :disabled="currentPage <= 1"
          @click="currentPage--"
          type="button"
        >‹ Prev</button>
        <span class="pager-info">Page {{ currentPage }} / {{ totalPages }}</span>
        <button
          class="pager-btn"
          :disabled="currentPage >= totalPages"
          @click="currentPage++"
          type="button"
        >Next ›</button>
      </div>
    </div>

    <!-- Create/Edit form modal -->
    <div v-if="showForm" class="modal-overlay" @click.self="closeForm">
      <div class="modal">
        <div class="modal-header">
          <h3>{{ isEditing ? 'Edit Agent' : 'New Agent' }}</h3>
          <button class="modal-close" @click="closeForm">&times;</button>
        </div>

        <div class="modal-body">
          <!-- Form error -->
          <div v-if="formError" class="form-error">{{ formError }}</div>

          <!-- Name -->
          <div class="form-group">
            <label class="form-label">Name <span class="required">*</span></label>
            <input
              v-model="form.name"
              type="text"
              class="form-input"
              placeholder="My Agent"
            />
          </div>

          <!-- Description -->
          <div class="form-group">
            <label class="form-label">Description</label>
            <input
              v-model="form.description"
              type="text"
              class="form-input"
              placeholder="A brief description of this agent"
            />
          </div>

          <!-- System Prompt -->
          <div class="form-group">
            <label class="form-label">System Prompt</label>
            <textarea
              v-model="form.system_prompt"
              class="form-input form-textarea"
              rows="4"
              placeholder="You are a helpful AI assistant..."
            ></textarea>
          </div>

          <!-- Model Mode selector -->
          <div class="form-group">
            <label class="form-label">Model Mode</label>
            <select v-model="form.model_mode" class="form-input form-select">
              <option value="single_model">Single Model (fixed)</option>
              <option value="auto_route">Auto Route (by intent/tier)</option>
            </select>
            <div class="mode-help">
              <span v-if="isAutoRoute">Router selects the best model per request based on intent and budget.</span>
              <span v-else>Always use the selected model.</span>
            </div>
          </div>

          <!-- Single Model selector -->
          <div v-if="!isAutoRoute" class="form-group">
            <ModelDropdown
              v-model="form.model"
              :available-models="availableModels"
              label="Model"
              placeholder="Filter models (provider/model_id)..."
            />
          </div>

          <!-- Auto Route controls -->
          <template v-else>
            <div class="form-row">
              <div class="form-group form-group-half">
                <label class="form-label">Preferred Tier</label>
                <select v-model="form.preferred_tier" class="form-input form-select">
                  <option v-for="t in MODEL_TIERS" :key="t" :value="t">{{ t }}</option>
                </select>
              </div>
              <div class="form-group form-group-half">
                <label class="form-label">Max Cost (USD)</label>
                <input
                  v-model.number="form.max_cost_usd"
                  type="number"
                  class="form-input"
                  min="0"
                  step="0.0001"
                  placeholder="0 = unlimited"
                />
              </div>
            </div>
            <div class="form-group">
              <label class="permission-checkbox" title="Allow tier fallback when preferred tier has no candidate">
                <input type="checkbox" v-model="form.allow_fallback" />
                <div class="permission-meta">
                  <span class="permission-label">Allow Fallback</span>
                  <span class="permission-desc">If the preferred tier has no available model, allow Router to pick from other tiers.</span>
                </div>
              </label>
            </div>
          </template>

          <!-- Temperature (always visible) -->
          <div class="form-row">
            <div class="form-group form-group-half">
              <label class="form-label">Temperature</label>
              <input
                v-model.number="form.temperature"
                type="number"
                class="form-input"
                min="0"
                max="2"
                step="0.1"
              />
            </div>
            <div class="form-group form-group-half">
              <ModelDropdown
                v-model="form.preferred_model"
                :available-models="availableModels"
                label="Preferred Model (optional)"
                placeholder="Filter models (provider/model_id)..."
              />
            </div>
          </div>

          <!-- Max Tokens & Endpoint (side by side) -->
          <div class="form-row">
            <div class="form-group form-group-half">
              <label class="form-label">Max Tokens</label>
              <input
                v-model.number="form.max_tokens"
                type="number"
                class="form-input"
                min="1"
                step="1"
              />
            </div>
            <div class="form-group form-group-half">
              <label class="form-label">API Endpoint</label>
              <input
                v-model="form.api_endpoint"
                type="text"
                class="form-input"
                placeholder="https://api.example.com/v1"
              />
            </div>
          </div>

          <!-- API Key -->
          <div class="form-group">
            <label class="form-label">API Key</label>
            <div class="input-with-button">
              <input
                v-model="form.api_key"
                :type="showApiKey ? 'text' : 'password'"
                class="form-input"
                placeholder="sk-..."
              />
              <button class="btn-toggle-key" @click="showApiKey = !showApiKey" type="button">
                {{ showApiKey ? 'Hide' : 'Show' }}
              </button>
            </div>
          </div>

          <!-- Test Connection -->
          <div class="form-group">
            <button
              class="btn-test"
              @click="handleTestConnection"
              :disabled="testing || !form.api_endpoint"
              type="button"
            >
              <span v-if="testing" class="btn-spinner-sm"></span>
              <span v-else>🔌 Test Connection</span>
            </button>
            <div v-if="testResult" :class="['test-result', testResult.ok ? 'test-success' : 'test-fail']">
              {{ testResult.message }}
            </div>
          </div>

          <!-- Permissions -->
          <div class="form-group">
            <label class="form-label">Default Permissions</label>
            <div class="permissions-help">
              Permissions are OR-merged on top of case/request-level permissions.
              They cannot revoke an already-granted permission.
            </div>
            <div class="permissions-list">
              <label
                v-for="perm in PERMISSIONS"
                :key="perm.key"
                class="permission-checkbox"
                :title="perm.description"
              >
                <input
                  type="checkbox"
                  v-model="form.config.permissions[perm.key]"
                />
                <div class="permission-meta">
                  <span class="permission-label">{{ perm.label }}</span>
                  <span class="permission-desc">{{ perm.description }}</span>
                  <span class="permission-risk" :class="riskClass(perm.risk)">{{ perm.risk }} risk</span>
                </div>
              </label>
            </div>
          </div>

          <!-- Tools selection -->
          <div class="form-group">
            <label class="form-label">Tools</label>
            <div v-if="allToolNames.length === 0" class="text-muted" style="font-size:12px;">
              No tools available. Tools are provided by the server.
            </div>
            <div v-else>
              <div class="tools-actions">
                <button
                  type="button"
                  class="btn-select-all"
                  @click="toggleSelectAll"
                >
                  {{ allSelected ? 'Deselect All' : 'Select All' }}
                </button>
                <span class="tools-hint">
                  Empty selection = allow all tools
                </span>
              </div>
              <div
                v-for="(group, namespace) in toolsByNamespace"
                :key="namespace"
                class="tool-namespace-group"
              >
                <div class="tool-namespace-label">{{ namespace }}</div>
                <div class="tools-checkbox-grid">
                  <label
                    v-for="tool in group"
                    :key="tool.name"
                    class="tool-checkbox"
                    :title="tool.description"
                  >
                    <input
                      type="checkbox"
                      :checked="form.tools.includes(tool.name)"
                      @change="toggleTool(tool.name)"
                    />
                    <span>{{ tool.name }}</span>
                  </label>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div class="modal-footer">
          <button class="btn-cancel" @click="closeForm" :disabled="saving">Cancel</button>
          <button class="btn-save" @click="handleSave" :disabled="saving">
            {{ saving ? 'Saving...' : (isEditing ? 'Update' : 'Create') }}
          </button>
        </div>
      </div>
    </div>

    <!-- Delete confirmation modal -->
    <div v-if="showDeleteConfirm" class="modal-overlay" @click.self="cancelDelete">
      <div class="modal modal-small">
        <div class="modal-header">
          <h3>Delete Agent</h3>
          <button class="modal-close" @click="cancelDelete">&times;</button>
        </div>
        <div class="modal-body">
          <p class="confirm-text">
            Are you sure you want to delete <strong>{{ deleteTarget?.name }}</strong>?
          </p>
          <p class="confirm-hint">
            This action cannot be undone. Any tasks using this agent will not be affected.
          </p>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" @click="cancelDelete" :disabled="deleting">Cancel</button>
          <button class="btn-delete-confirm" @click="handleDelete" :disabled="deleting">
            {{ deleting ? 'Deleting...' : 'Delete' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.agent-config {
  /* fills the main content area */
}

/* ---- Header ---- */
.config-header {
  display:flex;
  align-items:center;
  gap:0.75rem;
  margin-bottom:1.25rem;
  padding-bottom:0.75rem;
  border-bottom:1px solid var(--border-default);
}

.config-title {
  flex:1;
  font-size:1.125rem;
  font-weight:700;
  color:var(--text-primary);
  margin:0;
}

.btn-add {
  background:var(--accent-running);
  color:var(--text-primary);
  border:none;
  border-radius: var(--radius-md);
  padding:0.375rem 1rem;
  font-size:0.812rem;
  font-weight:600;
  cursor:pointer;
  transition:background 0.2s;
}

.btn-add:hover {
  background:var(--accent-running);
}

/* ---- Loading ---- */
.loading-area {
  display:flex;
  flex-direction:column;
  align-items:center;
  justify-content:center;
  padding:3.750rem 1.25rem;
  gap:0.75rem;
}

.loading-spinner {
  width:2.250rem;
  height:2.250rem;
  border:3px solid var(--border-default);
  border-top-color:var(--accent-running);
  border-radius:50%;
  animation:spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform:rotate(360deg); }
}

.loading-text {
  font-size:0.875rem;
  color:var(--text-muted);
}

/* ---- Error banner ---- */
.error-banner {
  background:rgba(231, 76, 60, 0.15);
  border:1px solid rgba(255, 107, 107, 0.32);
  color:var(--accent-danger);
  padding:0.625rem 0.875rem;
  border-radius: var(--radius-md);
  font-size:0.812rem;
  margin-bottom:1rem;
}

/* ---- Empty state ---- */
.empty-state {
  text-align:center;
  padding:3.750rem 1.25rem;
  color:var(--text-secondary);
}

.empty-icon {
  font-size:3rem;
  margin-bottom:0.75rem;
}

.empty-state h3 {
  font-size:1rem;
  color:var(--text-primary);
  margin-bottom:0.375rem;
}

.empty-state p {
  font-size:0.812rem;
}

/* ---- Agent table ---- */
.agent-table-wrapper {
  overflow-x:auto;
}

.agent-table {
  width:100%;
  border-collapse:collapse;
  font-size:0.812rem;
}

.agent-table thead {
  background:var(--bg-elevated);
  border-bottom:2px solid var(--border-default);
}

.agent-table th {
  text-align:left;
  padding:0.625rem 0.75rem;
  color:var(--text-muted);
  font-weight:600;
  font-size:0.688rem;
  text-transform:uppercase;
  letter-spacing: 0.03125rem;
  white-space:nowrap;
}

.agent-table td {
  padding:0.625rem 0.75rem;
  border-bottom:1px solid var(--border-subtle);
  vertical-align:top;
}

.agent-row:hover {
  background:var(--bg-elevated);
}

.cell-name {
  min-width:8.750rem;
}

.agent-name {
  color:var(--text-primary);
  font-weight:600;
}

.agent-desc {
  font-size:0.688rem;
  color:var(--text-muted);
  margin-top:0.125rem;
  max-width:15rem;
  overflow:hidden;
  text-overflow:ellipsis;
  white-space:nowrap;
}

.cell-model {
  font-family:var(--font-mono);
  font-size:0.75rem;
  color:var(--text-secondary);
}

.cell-temp {
  font-family:var(--font-mono);
  font-size:0.75rem;
  color:var(--text-secondary);
}

.cell-tools {
  max-width:12.500rem;
}

.tool-badges {
  display:flex;
  flex-wrap:wrap;
  gap:0.25rem;
}

.tool-badge {
  background:var(--bg-elevated);
  color:var(--text-secondary);
  font-size:0.625rem;
  padding:0.125rem 0.375rem;
  border-radius: var(--radius-sm);
  font-family:var(--font-mono);
  white-space:nowrap;
}

.cell-date {
  font-size:0.688rem;
  color:var(--text-muted);
  white-space:nowrap;
}

.cell-actions {
  white-space:nowrap;
}

.btn-action {
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  font-size: 0.875rem;
  padding: 0.375rem 0.625rem;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s, color 0.15s;
  margin-right: 0.375rem;
  color: var(--text-secondary);
}

.btn-edit:hover {
  background: var(--bg-hover);
  border-color: var(--accent-running);
  color: var(--accent-running);
}

.btn-delete {
  color: var(--text-secondary);
}

.btn-delete:hover:not(:disabled) {
  background: rgba(231, 76, 60, 0.18);
  border-color: rgba(255, 107, 107, 0.55);
  color: var(--accent-danger);
}

.btn-action:disabled {
  opacity: 0.35;
  cursor: not-allowed;
  background: transparent;
  border-color: transparent;
}

/* ---- Search & pagination toolbar (N1-05) ---- */
.agent-toolbar {
  display:flex;
  align-items:center;
  gap:0.625rem;
  margin-bottom:1rem;
  flex-wrap:wrap;
}

.agent-search {
  flex:1;
  min-width:12rem;
  background:var(--bg-elevated);
  border:1px solid var(--border-default);
  border-radius: var(--radius-md);
  color:var(--text-primary);
  padding:0.438rem 0.625rem;
  font-size:0.812rem;
  font-family:var(--font-mono);
  outline:none;
  transition:border-color 0.2s;
}

.agent-search:focus {
  border-color:var(--accent-running);
}

.page-size-label {
  font-size:0.75rem;
  color:var(--text-muted);
  display:flex;
  align-items:center;
  gap:0.375rem;
  white-space:nowrap;
}

.page-size-select {
  background:var(--bg-elevated);
  border:1px solid var(--border-default);
  border-radius: var(--radius-md);
  color:var(--text-primary);
  padding:0.375rem 0.5rem;
  font-size:0.812rem;
  font-family:var(--font-mono);
}

.agent-count {
  font-family: var(--font-mono);
  font-size:0.7rem;
  color:var(--text-muted);
  background:var(--bg-elevated);
  padding:0.25rem 0.625rem;
  border-radius:10px;
  white-space:nowrap;
}

/* ---- Role column ---- */
.cell-role {
  white-space:nowrap;
}

.role-badge {
  display:inline-block;
  font-size:0.625rem;
  font-weight:600;
  text-transform:uppercase;
  letter-spacing:0.04em;
  padding:0.125rem 0.5rem;
  border-radius: var(--radius-sm);
  border:1px solid var(--border-subtle);
}

.role-default {
  color:var(--accent-running);
  border-color:rgba(0, 229, 255, 0.25);
  background:rgba(0, 229, 255, 0.08);
}

.role-custom {
  color:var(--text-secondary);
  border-color:var(--border-default);
  background:var(--bg-elevated);
}

/* ---- Status (enable/disable) column ---- */
.cell-status {
  white-space:nowrap;
}

.status-toggle {
  display:inline-flex;
  align-items:center;
  gap:0.375rem;
  font-size:0.687rem;
  font-weight:600;
  padding:0.25rem 0.625rem;
  border-radius: var(--radius-md);
  cursor:pointer;
  transition:background 0.15s, border-color 0.15s, color 0.15s;
  border:1px solid var(--border-default);
  background:var(--bg-elevated);
}

.status-toggle:disabled {
  opacity:0.45;
  cursor:not-allowed;
}

.status-on {
  color:var(--accent-success);
  border-color:rgba(57, 255, 20, 0.25);
  background:rgba(57, 255, 20, 0.08);
}

.status-off {
  color:var(--text-muted);
  border-color:var(--border-subtle);
  background:transparent;
}

.status-dot {
  width:0.5rem;
  height:0.5rem;
  border-radius:50%;
  background:currentColor;
}

.dot-pulse {
  animation:spin 0.6s linear infinite;
}

/* ---- Pagination controls ---- */
.pager {
  display:flex;
  align-items:center;
  justify-content:center;
  gap:1rem;
  margin-top:1rem;
  padding-top:0.75rem;
  border-top:1px solid var(--border-subtle);
}

.pager-btn {
  background:var(--bg-elevated);
  border:1px solid var(--border-default);
  color:var(--text-secondary);
  font-size:0.75rem;
  padding:0.375rem 0.875rem;
  border-radius: var(--radius-md);
  cursor:pointer;
  transition:background 0.15s, color 0.15s;
}

.pager-btn:hover:not(:disabled) {
  background:var(--bg-hover);
  color:var(--text-primary);
}

.pager-btn:disabled {
  opacity:0.4;
  cursor:not-allowed;
}

.pager-info {
  font-family:var(--font-mono);
  font-size:0.75rem;
  color:var(--text-muted);
}

/* ---- Modal overlay ---- */
.modal-overlay {
  position:fixed;
  inset:0;
  background:rgba(0, 0, 0, 0.6);
  display:flex;
  align-items:center;
  justify-content:center;
  z-index:1000;
  backdrop-filter:blur(0.125rem);
}

.modal {
  background:var(--bg-panel);
  border:1px solid var(--border-default);
  border-radius: var(--radius-lg);
  width:38.750rem;
  max-width:95vw;
  max-height:85vh;
  display:flex;
  flex-direction:column;
  box-shadow:0 0.5rem 2rem rgba(0, 0, 0, 0.5);
}

.modal-small {
  width:27.500rem;
}

.modal-header {
  display:flex;
  justify-content:space-between;
  align-items:center;
  padding:0.875rem 1.125rem;
  border-bottom:1px solid var(--border-default);
}

.modal-header h3 {
  font-size:0.938rem;
  font-weight:600;
  color:var(--text-primary);
  margin:0;
}

.modal-close {
  background:transparent;
  border:none;
  color:var(--text-muted);
  font-size:1.375rem;
  cursor:pointer;
  line-height:1;
  padding:0 0.25rem;
  transition:color 0.15s;
}

.modal-close:hover {
  color:var(--text-primary);
}

.modal-body {
  padding:1.125rem;
  overflow-y:auto;
  flex:1;
}

.modal-footer {
  display:flex;
  justify-content:flex-end;
  gap:0.625rem;
  padding:0.75rem 1.125rem;
  border-top:1px solid var(--border-default);
}

/* ---- Form elements ---- */
.form-group {
  margin-bottom:0.875rem;
}

.form-label {
  display:block;
  font-size:0.75rem;
  font-weight:600;
  color:var(--text-secondary);
  margin-bottom:0.312rem;
}

.required {
  color:var(--accent-danger);
}

.form-input {
  width:100%;
  background:var(--bg-elevated);
  border:1px solid var(--border-default);
  border-radius: var(--radius-md);
  color:var(--text-primary);
  padding:0.438rem 0.625rem;
  font-size:0.812rem;
  font-family:var(--font-mono);
  outline:none;
  transition:border-color 0.2s;
}

.form-input:focus {
  border-color:var(--accent-running);
}

.form-textarea {
  resize:vertical;
  font-family:var(--font-display);
  line-height:1.5;
}

.form-row {
  display:flex;
  gap:0.75rem;
}

.form-group-half {
  flex:1;
}

.form-error {
  background:rgba(231, 76, 60, 0.15);
  border:1px solid rgba(255, 107, 107, 0.32);
  color:var(--accent-danger);
  padding:0.5rem 0.75rem;
  border-radius: var(--radius-md);
  font-size:0.75rem;
  margin-bottom:0.875rem;
}

/* Input with inline button (API key show/hide) */
.input-with-button {
  display:flex;
  gap:0.375rem;
}

.input-with-button .form-input {
  flex:1;
}

.btn-toggle-key {
  background:var(--bg-elevated);
  border:1px solid var(--border-default);
  color:var(--text-muted);
  font-size:0.688rem;
  padding:0 0.625rem;
  border-radius: var(--radius-md);
  cursor:pointer;
  white-space:nowrap;
  transition:background 0.15s, color 0.15s;
}

.btn-toggle-key:hover {
  background:var(--bg-hover);
  color:var(--text-primary);
}

/* Test connection button */
.btn-test {
  background:var(--bg-elevated);
  border:1px solid var(--border-default);
  color:var(--text-secondary);
  font-size:0.75rem;
  padding:0.375rem 0.875rem;
  border-radius: var(--radius-md);
  cursor:pointer;
  transition:background 0.15s, color 0.15s;
}

.btn-test:hover:not(:disabled) {
  background:var(--bg-hover);
  color:var(--text-primary);
}

.btn-test:disabled {
  opacity:0.5;
  cursor:not-allowed;
}

.btn-spinner-sm {
  display:inline-block;
  width:0.75rem;
  height:0.75rem;
  border:2px solid rgba(255,255,255,0.3);
  border-top-color:var(--text-primary);
  border-radius:50%;
  animation:spin 0.6s linear infinite;
  vertical-align:middle;
}

.test-result {
  margin-top:0.5rem;
  padding:0.5rem 0.75rem;
  border-radius: var(--radius-md);
  font-size:0.75rem;
  line-height:1.4;
}

.test-success {
  background:rgba(127, 191, 127, 0.10);
  border:1px solid rgba(127, 191, 127, 0.18);
  color:var(--accent-success);
}

.test-fail {
  background:rgba(255, 107, 107, 0.10);
  border:1px solid rgba(255, 107, 107, 0.22);
  color:var(--accent-danger);
}

/* Tools selection */
.tools-actions {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-bottom: 0.5rem;
}

.btn-select-all {
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  color: var(--text-secondary);
  font-size: 0.75rem;
  padding: 0.25rem 0.625rem;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.btn-select-all:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.tools-hint {
  font-size: 0.75rem;
  color: var(--text-muted);
}

.tool-namespace-group {
  margin-bottom: 0.625rem;
}

.tool-namespace-label {
  font-size: 0.7rem;
  font-weight: 600;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.04em;
  margin-bottom: 0.25rem;
}

.tools-checkbox-grid {
  display:flex;
  flex-wrap:wrap;
  gap:0.5rem;
}

.tool-checkbox {
  display:flex;
  align-items:center;
  gap:0.312rem;
  background:var(--bg-elevated);
  border:1px solid var(--border-default);
  border-radius: var(--radius-md);
  padding:0.312rem 0.625rem;
  font-size:0.75rem;
  color:var(--text-secondary);
  cursor:pointer;
  transition:border-color 0.15s, background 0.15s;
  font-family:var(--font-mono);
}

.tool-checkbox:hover {
  border-color:var(--accent-running);
  background:var(--bg-hover);
}

.tool-checkbox input[type="checkbox"] {
  accent-color:var(--accent-running);
  cursor:pointer;
}

/* ---- Modal buttons ---- */
.btn-cancel {
  background:var(--bg-elevated);
  border:1px solid var(--border-default);
  color:var(--text-secondary);
  font-size:0.812rem;
  padding:0.438rem 1rem;
  border-radius: var(--radius-md);
  cursor:pointer;
  transition:background 0.15s;
}

.btn-cancel:hover:not(:disabled) {
  background:var(--bg-hover);
}

.btn-cancel:disabled {
  opacity:0.5;
  cursor:not-allowed;
}

.btn-save {
  background:var(--accent-running);
  color:var(--text-primary);
  border:none;
  border-radius: var(--radius-md);
  padding:0.438rem 1.25rem;
  font-size:0.812rem;
  font-weight:600;
  cursor:pointer;
  transition:background 0.2s;
}

.btn-save:hover:not(:disabled) {
  background:var(--accent-running);
}

.btn-save:disabled {
  opacity:0.5;
  cursor:not-allowed;
}

.btn-delete-confirm {
  background:var(--accent-danger);
  color:var(--text-primary);
  border:none;
  border-radius: var(--radius-md);
  padding:0.438rem 1.25rem;
  font-size:0.812rem;
  font-weight:600;
  cursor:pointer;
  transition:background 0.2s;
}

.btn-delete-confirm:hover:not(:disabled) {
  background:var(--accent-danger);
}

.btn-delete-confirm:disabled {
  opacity:0.5;
  cursor:not-allowed;
}

/* ---- Confirm dialog ---- */
.confirm-text {
  font-size:0.875rem;
  color:var(--text-primary);
  margin-bottom:0.5rem;
}

.confirm-text strong {
  color:var(--text-primary);
}

.confirm-hint {
  font-size:0.75rem;
  color:var(--text-muted);
  line-height:1.4;
}

/* Utility */
.text-muted {
  color:var(--text-muted);
}

/* ---- Model dropdown ---- */
.model-dropdown-list {
  max-height:12rem;
  overflow-y:auto;
  border:1px solid var(--border-default);
  border-radius: var(--radius-md);
  background:var(--bg-elevated);
  margin-top:0.25rem;
}

.model-dropdown-item {
  padding:0.438rem 0.625rem;
  font-size:0.812rem;
  color:var(--text-secondary);
  cursor:pointer;
  transition:background 0.15s, color 0.15s;
  font-family:var(--font-mono);
  border-bottom:1px solid var(--border-subtle);
}

.model-dropdown-item:last-child {
  border-bottom:none;
}

.model-dropdown-item:hover,
.model-dropdown-item.active {
  background:var(--bg-hover);
  color:var(--text-primary);
}

.model-dropdown-empty {
  padding:0.5rem 0.625rem;
  font-size:0.75rem;
  color:var(--text-muted);
}
</style>