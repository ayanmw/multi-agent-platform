<!-- AgentConfig — agent configuration management page
     Replaces the main content area to manage agent CRUD.

     Features:
       - Agent list table with name, model, temperature, tools, created_at
       - Create/Edit form with all agent fields
       - Delete with confirmation dialog
       - Test connection button to verify API endpoint/key
       - Back button to return to main view

     Emits:
       back: user clicked the back button to return to main view
-->
<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useAgentStore, type AgentRecord, type AgentRequest, defaultAgentRequest } from '../composables/useAgentStore'

const emit = defineEmits<{
  back: []
}>()

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

// Computed: is the form in edit mode?
const isEditing = computed(() => editingId.value !== null)

// Available tool names as a simple array for checkboxes
const toolNames = computed(() => availableTools.value.map(t => t.name))

onMounted(() => {
  loadAgents().catch(() => {})
  loadAvailableTools().catch(() => {})
})

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
  form.value = {
    name: agent.name,
    description: agent.description || '',
    system_prompt: agent.system_prompt || '',
    model: agent.model || '',
    temperature: agent.temperature ?? 0.7,
    max_tokens: agent.max_tokens ?? 4096,
    api_endpoint: agent.api_endpoint || '',
    api_key: agent.api_key || '',
    tools: agent.tools ? [...agent.tools] : [],
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

/** Validate and save the form (create or update) */
async function handleSave() {
  formError.value = null
  if (!form.value.name.trim()) {
    formError.value = 'Name is required'
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
      <button class="btn-back" @click="emit('back')">← Back</button>
      <h2 class="config-title">⚙ Agent Configuration</h2>
      <button class="btn-add" @click="openCreate">+ New Agent</button>
    </div>

    <!-- Loading state -->
    <div v-if="loading" class="loading-area">
      <div class="loading-spinner"></div>
      <div class="loading-text">Loading agents...</div>
    </div>

    <!-- Error banner -->
    <div v-if="error" class="error-banner">{{ error }}</div>

    <!-- Agent list table -->
    <div v-if="!loading && agents.length === 0" class="empty-state">
      <div class="empty-icon">🤖</div>
      <h3>No agents configured</h3>
      <p>Create your first agent to get started.</p>
    </div>

    <div v-else-if="!loading" class="agent-table-wrapper">
      <table class="agent-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Model</th>
            <th>Temp</th>
            <th>Tools</th>
            <th>Created</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="agent in agents" :key="agent.id" class="agent-row">
            <td class="cell-name">
              <div class="agent-name">{{ agent.name }}</div>
              <div v-if="agent.description" class="agent-desc">{{ agent.description }}</div>
            </td>
            <td class="cell-model">{{ agent.model || '-' }}</td>
            <td class="cell-temp">{{ agent.temperature ?? '-' }}</td>
            <td class="cell-tools">
              <span v-if="agent.tools && agent.tools.length > 0" class="tool-badges">
                <span v-for="t in agent.tools" :key="t" class="tool-badge">{{ t }}</span>
              </span>
              <span v-else class="text-muted">-</span>
            </td>
            <td class="cell-date">{{ formatDate(agent.created_at) }}</td>
            <td class="cell-actions">
              <button class="btn-action btn-edit" @click="openEdit(agent)" title="Edit">✏</button>
              <button
                class="btn-action btn-delete"
                @click="confirmDelete(agent)"
                :title="agent.is_default ? 'Default agent cannot be deleted' : 'Delete'"
                :disabled="agent.is_default"
                :style="agent.is_default ? { opacity: '0.3', cursor: 'not-allowed' } : {}"
              >🗑</button>
            </td>
          </tr>
        </tbody>
      </table>
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

          <!-- Model & Temperature (side by side) -->
          <div class="form-row">
            <div class="form-group form-group-half">
              <label class="form-label">Model</label>
              <input
                v-model="form.model"
                type="text"
                class="form-input"
                placeholder="deepseek-v4-flash"
              />
            </div>
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

          <!-- Tools selection -->
          <div class="form-group">
            <label class="form-label">Tools</label>
            <div v-if="toolNames.length === 0" class="text-muted" style="font-size:12px;">
              No tools available. Tools are provided by the server.
            </div>
            <div v-else class="tools-checkbox-grid">
              <label
                v-for="tool in toolNames"
                :key="tool"
                class="tool-checkbox"
              >
                <input
                  type="checkbox"
                  :checked="form.tools.includes(tool)"
                  @change="toggleTool(tool)"
                />
                <span>{{ tool }}</span>
              </label>
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

.btn-back {
  background:var(--bg-elevated);
  border:1px solid var(--border-default);
  color:var(--text-secondary);
  font-size:0.812rem;
  padding:0.375rem 0.875rem;
  border-radius: var(--radius-md);
  cursor:pointer;
  transition:background 0.2s, color 0.2s;
}

.btn-back:hover {
  background:var(--bg-hover);
  color:var(--text-primary);
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
  background:transparent;
  border:1px solid transparent;
  font-size:0.875rem;
  padding:0.25rem 0.5rem;
  border-radius: var(--radius-sm);
  cursor:pointer;
  transition:background 0.15s, border-color 0.15s;
  margin-right:0.25rem;
}

.btn-edit:hover {
  background:var(--bg-elevated);
  border-color:var(--border-default);
}

.btn-delete:hover {
  background:rgba(231, 76, 60, 0.18);
  border-color:rgba(255, 107, 107, 0.32);
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

/* Tools checkboxes */
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
</style>