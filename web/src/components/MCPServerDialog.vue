<!-- MCPServerDialog.vue — manage external MCP Servers
     Renders as a Teleport modal overlay.

     Data flow:
       visible → loadServers() → list with status + loaded flag
       Create → POST /api/mcp/servers → reload list
       Enable / Disable / Delete → respective endpoints → reload list

     Why this exists:
       MCP servers extend the tool registry at runtime. Operators need a UI to
       inspect which servers are loaded, add new stdio/SSE servers, and toggle
       them without restarting the platform.
-->
<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="dialogVisible" class="mcp-overlay" @click.self="close">
        <div class="mcp-dialog">
          <div class="mcp-header">
            <h3>🔌 MCP Server 管理</h3>
            <span class="mcp-count">{{ countLabel }}</span>
            <button class="mcp-close" @click="close" title="关闭">✕</button>
          </div>

          <div v-if="error" class="mcp-error">
            ⚠️ {{ error }}
            <button @click="loadServers" class="btn-retry">刷新</button>
          </div>

          <div class="mcp-toolbar">
            <button class="btn-refresh" @click="loadServers" title="刷新列表">🔄</button>
            <button class="btn-create" @click="openCreate">+ 添加 Server</button>
          </div>

          <div v-if="loading" class="mcp-empty">加载中...</div>
          <div v-else-if="servers.length === 0" class="mcp-empty">
            暂无 MCP Server，点击右上角 + 添加一个。
          </div>

          <div v-else class="mcp-list">
            <div
              v-for="s in servers"
              :key="s.id"
              class="mcp-card"
              :class="{ loaded: s.loaded, disabled: !s.enabled, expanded: expandedId === s.id }"
            >
              <div class="mcp-card-header" @click="toggleExpand(s.id)">
                <span class="mcp-status-dot" :class="s.loaded ? 'on' : 'off'" />
                <div class="mcp-title">
                  <span class="mcp-name">{{ s.id }}</span>
                  <span class="mcp-source">{{ s.source }}</span>
                </div>
                <div class="mcp-tags">
                  <span class="mcp-tag" :class="s.enabled ? 'enabled' : 'disabled'">
                    {{ s.enabled ? 'enabled' : 'disabled' }}
                  </span>
                  <span class="mcp-tag transport">{{ s.config.transport }}</span>
                  <span v-if="s.loaded" class="mcp-tag loaded">loaded</span>
                </div>
              </div>

              <div v-if="expandedId === s.id" class="mcp-card-detail">
                <div class="mcp-meta">
                  <div class="meta-row">
                    <span class="meta-label">名称:</span>
                    <span class="meta-value">{{ s.config.name }}</span>
                  </div>
                  <div class="meta-row">
                    <span class="meta-label">Transport:</span>
                    <span class="meta-value">{{ s.config.transport }}</span>
                  </div>
                  <div v-if="s.config.command" class="meta-row">
                    <span class="meta-label">Command:</span>
                    <span class="meta-value">{{ s.config.command }}</span>
                  </div>
                  <div v-if="s.config.args && s.config.args.length > 0" class="meta-row">
                    <span class="meta-label">Args:</span>
                    <span class="meta-value">{{ s.config.args.join(' ') }}</span>
                  </div>
                  <div v-if="s.config.endpoint" class="meta-row">
                    <span class="meta-label">Endpoint:</span>
                    <span class="meta-value">{{ s.config.endpoint }}</span>
                  </div>
                  <div v-if="s.load_err" class="meta-row">
                    <span class="meta-label">Load Error:</span>
                    <span class="meta-value error-text">{{ s.load_err }}</span>
                  </div>
                  <div class="meta-row">
                    <span class="meta-label">Created:</span>
                    <span class="meta-value">{{ formatDate(s.created_at) }}</span>
                  </div>
                  <div class="meta-row">
                    <span class="meta-label">Updated:</span>
                    <span class="meta-value">{{ formatDate(s.updated_at) }}</span>
                  </div>
                </div>

                <div class="mcp-actions">
                  <template v-if="s.source !== 'static'">
                    <button
                      v-if="!s.enabled"
                      class="btn-action enable"
                      :disabled="pendingId === s.id"
                      @click="doEnable(s.id)"
                    >
                      {{ pendingId === s.id ? '启用中...' : '启用' }}
                    </button>
                    <button
                      v-else
                      class="btn-action disable"
                      :disabled="pendingId === s.id"
                      @click="doDisable(s.id)"
                    >
                      {{ pendingId === s.id ? '禁用中...' : '禁用' }}
                    </button>
                    <button
                      class="btn-action delete"
                      :disabled="pendingId === s.id"
                      @click="doDelete(s)"
                    >
                      {{ deleteConfirm === s.id ? '确认删除?' : '删除' }}
                    </button>
                  </template>
                  <span v-else class="mcp-static-note">静态 Server（不可删除）</span>
                </div>
              </div>
            </div>
          </div>

          <div class="mcp-footer">
            <span class="mcp-hint">工具命名空间: mcp__&lt;server&gt;__&lt;tool&gt;</span>
            <button class="mcp-close-btn" @click="close">关闭</button>
          </div>
        </div>
      </div>
    </Transition>

    <!-- Create dialog -->
    <Teleport to="body">
      <Transition name="fade">
        <div v-if="showCreate" class="mcp-overlay" @click.self="closeCreate">
          <div class="mcp-create-dialog">
            <div class="mcp-header">
              <h3>+ 添加 MCP Server</h3>
              <button class="mcp-close" @click="closeCreate" title="关闭">✕</button>
            </div>

            <div class="mcp-form">
              <label class="mcp-field">
                <span>ID (唯一标识)</span>
                <input v-model="form.id" type="text" placeholder="local-time" />
              </label>

              <label class="mcp-field">
                <span>名称 (可选, 默认与 ID 相同)</span>
                <input v-model="form.name" type="text" placeholder="local-time" />
              </label>

              <label class="mcp-field">
                <span>Transport</span>
                <select v-model="form.transport">
                  <option value="stdio">stdio (本地子进程)</option>
                  <option value="sse">sse (HTTP Server-Sent Events)</option>
                </select>
              </label>

              <template v-if="form.transport === 'stdio'">
                <label class="mcp-field">
                  <span>Command</span>
                  <input v-model="form.command" type="text" placeholder="node" />
                </label>
                <label class="mcp-field">
                  <span>Args (每行一个)</span>
                  <textarea v-model="form.args" rows="3" placeholder="examples/mcp/time/mcp-time-server.js" />
                </label>
              </template>

              <template v-if="form.transport === 'sse'">
                <label class="mcp-field">
                  <span>Endpoint URL</span>
                  <input v-model="form.endpoint" type="text" placeholder="http://localhost:3001/sse" />
                </label>
              </template>

              <label class="mcp-field">
                <span>Environment (每行 KEY=VALUE)</span>
                <textarea v-model="form.environment" rows="3" placeholder="PATH=/usr/local/bin&#10;NODE_ENV=production" />
              </label>

              <label class="mcp-field inline">
                <span>启用</span>
                <input v-model="form.enabled" type="checkbox" />
              </label>
            </div>

            <div v-if="createError" class="mcp-error">{{ createError }}</div>

            <div class="mcp-footer">
              <button class="mcp-close-btn" @click="closeCreate">取消</button>
              <button class="mcp-save-btn" :disabled="creating" @click="submitCreate">
                {{ creating ? '创建中...' : '创建' }}
              </button>
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch, computed, onMounted, onUnmounted } from 'vue'
import { useMCPStore, defaultMCPServerForm, type ManagedMCPServer } from '../composables/useMCPStore'

const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{ 'update:visible': [v: boolean] }>()

const { servers, loading, error, loadServers, createServer, enableServer, disableServer, deleteServer } = useMCPStore()

const dialogVisible = ref(props.visible)
watch(() => props.visible, v => {
  dialogVisible.value = v
  if (v) {
    loadServers().catch(() => {})
    expandedId.value = null
    deleteConfirm.value = null
    pendingId.value = null
  }
})
watch(dialogVisible, v => emit('update:visible', v))

const expandedId = ref<string | null>(null)
const deleteConfirm = ref<string | null>(null)
const pendingId = ref<string | null>(null)

const countLabel = computed(() => {
  const n = servers.value.length
  return n > 0 ? `${n} 个 server` : '暂无 server'
})

function toggleExpand(id: string) {
  expandedId.value = expandedId.value === id ? null : id
}

async function doEnable(id: string) {
  pendingId.value = id
  try { await enableServer(id) } finally { pendingId.value = null }
}

async function doDisable(id: string) {
  pendingId.value = id
  try { await disableServer(id) } finally { pendingId.value = null }
}

async function doDelete(s: ManagedMCPServer) {
  if (deleteConfirm.value === s.id) {
    pendingId.value = s.id
    try { await deleteServer(s.id) } finally { pendingId.value = null }
    deleteConfirm.value = null
    expandedId.value = null
  } else {
    deleteConfirm.value = s.id
    setTimeout(() => {
      if (deleteConfirm.value === s.id) deleteConfirm.value = null
    }, 3000)
  }
}

// Create dialog state
const showCreate = ref(false)
const form = ref(defaultMCPServerForm())
const creating = ref(false)
const createError = ref<string | null>(null)

function openCreate() {
  form.value = defaultMCPServerForm()
  createError.value = null
  showCreate.value = true
}

function closeCreate() {
  showCreate.value = false
}

async function submitCreate() {
  if (!form.value.id.trim()) {
    createError.value = 'ID 不能为空'
    return
  }
  const transport = form.value.transport
  if (transport === 'stdio' && !form.value.command.trim()) {
    createError.value = 'stdio transport 需要 Command'
    return
  }
  if (transport === 'sse' && !form.value.endpoint.trim()) {
    createError.value = 'sse transport 需要 Endpoint URL'
    return
  }
  creating.value = true
  createError.value = null
  try {
    await createServer(form.value)
    closeCreate()
    expandedId.value = null
  } catch (err) {
    createError.value = err instanceof Error ? err.message : '创建失败'
  } finally {
    creating.value = false
  }
}

function close() {
  dialogVisible.value = false
}

function formatDate(dateStr: string | undefined): string {
  if (!dateStr) return 'N/A'
  return new Date(dateStr).toLocaleString()
}

onMounted(() => {
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape' && dialogVisible.value) {
      if (showCreate.value) {
        closeCreate()
      } else {
        close()
      }
    }
  }
  window.addEventListener('keydown', onKey)
  onUnmounted(() => window.removeEventListener('keydown', onKey))
})
</script>

<style scoped>
.mcp-overlay {
  position: fixed;
  inset: 0;
  z-index: 950;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
}

.mcp-dialog {
  background: #1e1e2e;
  border: 1px solid #313244;
  border-radius: 12px;
  width: min(720px, 94vw);
  max-height: 82vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.4);
}

.mcp-create-dialog {
  background: #1e1e2e;
  border: 1px solid #313244;
  border-radius: 12px;
  width: min(520px, 94vw);
  max-height: 86vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.4);
}

.mcp-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 18px;
  border-bottom: 1px solid #313244;
  user-select: none;
}

.mcp-header h3 {
  margin: 0;
  font-size: 15px;
  color: #cdd6f4;
  font-weight: 600;
}

.mcp-count {
  flex: 1;
  font-size: 12px;
  color: #6c7086;
}

.mcp-close {
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: #a6adc8;
  font-size: 16px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background 0.15s;
}

.mcp-close:hover {
  background: #313244;
  color: #cdd6f4;
}

.mcp-toolbar {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px 18px;
  border-bottom: 1px solid #313244;
}

.btn-refresh,
.btn-create {
  padding: 6px 12px;
  border-radius: 6px;
  border: 1px solid #444;
  background: #2a2a3a;
  color: #cdd6f4;
  font-size: 13px;
  cursor: pointer;
}

.btn-create {
  background: #3a5a3a;
  border-color: #4a7a4a;
}

.mcp-error {
  padding: 10px 18px;
  color: #f38ba8;
  background: rgba(243, 139, 168, 0.08);
  border-bottom: 1px solid #313244;
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 13px;
}

.btn-retry {
  padding: 3px 10px;
  background: #f38ba8;
  color: #1e1e2e;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 12px;
}

.mcp-empty {
  padding: 40px 20px;
  text-align: center;
  color: #6c7086;
  font-size: 14px;
}

.mcp-list {
  overflow-y: auto;
  flex: 1;
  padding: 6px 0;
  max-height: 56vh;
}

.mcp-card {
  border-bottom: 1px solid #313244;
}

.mcp-card-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 18px;
  cursor: pointer;
  user-select: none;
}

.mcp-card-header:hover {
  background: #181825;
}

.mcp-status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
  background: #585b70;
}
.mcp-status-dot.on { background: #a6e3a1; box-shadow: 0 0 6px #a6e3a166; }
.mcp-status-dot.off { background: #f38ba8; }

.mcp-title {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}

.mcp-name {
  font-family: 'SF Mono', 'Fira Code', 'Cascadia Code', monospace;
  color: #cdd6f4;
  font-size: 13px;
}

.mcp-source {
  font-size: 10px;
  color: #585b70;
  text-transform: uppercase;
}

.mcp-tags {
  display: flex;
  gap: 6px;
  flex-shrink: 0;
}

.mcp-tag {
  font-size: 10px;
  padding: 2px 7px;
  border-radius: 8px;
  text-transform: uppercase;
  background: #313244;
  color: #a6adc8;
}

.mcp-tag.enabled { background: rgba(166, 227, 161, 0.18); color: #a6e3a1; }
.mcp-tag.disabled { background: rgba(243, 139, 168, 0.18); color: #f38ba8; }
.mcp-tag.loaded { background: rgba(137, 180, 250, 0.18); color: #89b4fa; }

.mcp-card-detail {
  padding: 0 18px 14px;
  border-top: 1px solid #252536;
}

.mcp-meta {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 8px;
  padding: 12px 0;
}

.meta-row {
  display: flex;
  gap: 8px;
  font-size: 12px;
}

.meta-label {
  color: #6c7086;
  flex-shrink: 0;
  min-width: 70px;
}

.meta-value {
  color: #cdd6f4;
  word-break: break-all;
}

.error-text { color: #f38ba8; }

.mcp-actions {
  display: flex;
  gap: 8px;
  padding-top: 10px;
  border-top: 1px solid #252536;
}

.btn-action {
  padding: 5px 12px;
  border-radius: 6px;
  border: none;
  font-size: 12px;
  cursor: pointer;
  transition: opacity 0.15s;
}

.btn-action:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-action.enable { background: #a6e3a1; color: #1e1e2e; }
.btn-action.disable { background: #f9e2af; color: #1e1e2e; }
.btn-action.delete { background: #f38ba8; color: #1e1e2e; }
.btn-action:hover:not(:disabled) { opacity: 0.85; }

.mcp-static-note {
  font-size: 12px;
  color: #6c7086;
}

.mcp-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 12px 18px;
  border-top: 1px solid #313244;
}

.mcp-hint {
  font-size: 11px;
  color: #585b70;
}

.mcp-close-btn {
  background: #45475a;
  border: none;
  color: #cdd6f4;
  border-radius: 6px;
  padding: 6px 16px;
  font-size: 13px;
  cursor: pointer;
}

.mcp-close-btn:hover { background: #585b70; }

.mcp-form {
  padding: 16px 18px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  overflow-y: auto;
  max-height: 60vh;
}

.mcp-field {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.mcp-field.inline {
  flex-direction: row;
  align-items: center;
  gap: 8px;
}

.mcp-field span {
  font-size: 12px;
  color: #a6adc8;
}

.mcp-field input,
.mcp-field select,
.mcp-field textarea {
  background: #181825;
  border: 1px solid #313244;
  border-radius: 6px;
  color: #cdd6f4;
  padding: 8px 10px;
  font-size: 13px;
  outline: none;
  font-family: inherit;
}

.mcp-field input:focus,
.mcp-field select:focus,
.mcp-field textarea:focus {
  border-color: #89b4fa;
}

.mcp-save-btn {
  background: #89b4fa;
  border: none;
  color: #1e1e2e;
  border-radius: 6px;
  padding: 6px 18px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}

.mcp-save-btn:disabled { opacity: 0.5; cursor: not-allowed; }

.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}
.fade-enter-from,
.fade-leave-to { opacity: 0; }
</style>
