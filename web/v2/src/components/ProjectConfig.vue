<!-- ProjectConfig — project 配置管理面板
     渲染在管理（原 Inspector）弹窗的 Project tab 内，无 Back 按钮（弹窗本身负责关闭）。

     重构后的结构（对齐 AgentConfig 的列表 + 弹窗 CRUD 模式）：
       - 顶部 header：标题 + "+ New Project" 按钮
       - project 列表表格：name / description / working dir / session 计数 / 操作（编辑/删除）
       - Create/Edit 表单弹窗：name / description / working_directory + rules 文本框
       - Delete 确认弹窗

     rules 字段说明：
       project 级规则文本，归属于该 project 的所有 session 在发起任务时都会把这段
       文本注入到 system prompt（类似记忆，避免每次重复说明上下文约定）。
       后端：projectRequest.Rules → project.config.rules；发起任务时
       projectRulesPrompt 读取并拼接为 "## Project Rules" 段落注入 system prompt。

     rules 实时预览（本组件）：
       编辑表单里的 rules 文本框下方提供"注入预览"，按后端 projectRulesPrompt
       的相同格式渲染（## Project Rules 标题 + 原文），让用户在保存前就能看到
       这段规则会被怎样拼进 system prompt，避免格式预期错位。
-->
<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useProjectStore, type Project, type ProjectRequest } from '../composables/useProjectStore'
import { useToast } from '../composables/useToast'

const { projects, activeProjectId, loadProjects, createProject, updateProject, deleteProject, setActiveProject } = useProjectStore()
const { showInfo } = useToast()

/** 默认的空表单值。rules 暂存在 project.config.rules（后端 config JSON）。 */
function emptyForm(): ProjectForm {
  return {
    name: '',
    description: '',
    working_directory: '',
    rules: '',
  }
}

interface ProjectForm {
  name: string
  description: string
  working_directory: string
  rules: string
}

// Create/Edit 表单状态
const showForm = ref(false)
const editingId = ref<string | null>(null)
const form = ref<ProjectForm>(emptyForm())
const formError = ref<string | null>(null)
const saving = ref(false)

// Delete 确认状态
const deleteTarget = ref<Project | null>(null)
const showDeleteConfirm = ref(false)
const deleting = ref(false)

const isEditing = computed(() => editingId.value !== null)

onMounted(() => {
  loadProjects().catch(() => {})
})

/** 从 project.config.rules 读取规则文本（config 为 map[string]any）。 */
function readRules(p: Project): string {
  const cfg = (p as Project & { config?: Record<string, unknown> }).config
  if (cfg && typeof cfg.rules === 'string') return cfg.rules
  return ''
}

/**
 * Rules 注入预览：按后端 projectRulesPrompt 的相同格式渲染当前表单中的
 * rules 文本，让用户在保存前就能看到这段规则会被怎样拼进 system prompt。
 *
 * 与后端 cmd/server/main.go::projectRulesPrompt 保持格式一致：
 *   "\n\n## Project Rules\n" + rules + "\n"
 * 这里只展示主体（标题 + 正文），不渲染首尾的空行占位。
 */
const rulesPreview = computed(() => {
  const text = form.value.rules
  if (!text.trim()) return ''
  return `## Project Rules\n${text}`
})

/** 切换预览面板展开/收起。默认收起，避免在不需要时占用表单空间。 */
const showRulesPreview = ref(false)

/** 打开新建表单 */
function openCreate() {
  editingId.value = null
  form.value = emptyForm()
  formError.value = null
  showForm.value = true
}

/** 打开编辑表单，预填充已有 project 字段 */
function openEdit(p: Project) {
  editingId.value = p.id
  form.value = {
    name: p.name,
    description: p.description || '',
    working_directory: p.working_directory || '',
    rules: readRules(p),
  }
  formError.value = null
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editingId.value = null
  formError.value = null
}

/** 提交表单：新建或更新。rules 通过 config 透传给后端。 */
async function handleSave() {
  formError.value = null
  if (!form.value.name.trim()) {
    formError.value = 'Name is required'
    return
  }
  const hadRulesBefore = editingId.value
    ? readRules(projects.value.find(p => p.id === editingId.value) as Project).trim() !== ''
    : false
  const hasRulesNow = form.value.rules.trim() !== ''
  const rulesChanged = hadRulesBefore !== hasRulesNow
  saving.value = true
  try {
    const req: ProjectRequest & { rules?: string } = {
      name: form.value.name.trim(),
      description: form.value.description,
      working_directory: form.value.working_directory,
    }
    if (form.value.rules.trim()) {
      req.rules = form.value.rules
    }
    let savedId: string
    if (editingId.value) {
      await updateProject(editingId.value, req)
      savedId = editingId.value
    } else {
      const created = await createProject(req)
      savedId = created.id
      setActiveProject(created.id)
    }
    showForm.value = false
    editingId.value = null
    // rules 变更提示：明确告诉用户注入何时生效——保存后新发起的任务才会读到
    // 新规则，正在运行/已完成的任务不受影响（system prompt 在任务启动时定版）。
    if (rulesChanged) {
      if (hasRulesNow) {
        showInfo(`Project rules 已保存，本 project 下新发起的任务将自动注入`)
      } else {
        showInfo(`Project rules 已清空，新发起的任务不再注入旧规则`)
      }
    }
  } catch (err) {
    formError.value = err instanceof Error ? err.message : 'Save failed'
  } finally {
    saving.value = false
  }
}

/** default project 不可删除 */
function confirmDelete(p: Project) {
  if (p.id === 'default') return
  deleteTarget.value = p
  showDeleteConfirm.value = true
}

function cancelDelete() {
  deleteTarget.value = null
  showDeleteConfirm.value = false
}

async function handleDelete() {
  if (!deleteTarget.value) return
  deleting.value = true
  try {
    await deleteProject(deleteTarget.value.id)
    showDeleteConfirm.value = false
    deleteTarget.value = null
  } catch (err) {
    formError.value = err instanceof Error ? err.message : 'Delete failed'
  } finally {
    deleting.value = false
  }
}

function formatDate(iso: string): string {
  if (!iso) return '-'
  const d = new Date(iso)
  return d.toLocaleString()
}

// 切换激活项目（点击表格行）
function handleRowClick(p: Project) {
  setActiveProject(p.id)
}

// 关闭弹窗时清掉残留错误，避免下次打开还显示旧报错
watch(showForm, (open) => { if (!open) formError.value = null })
</script>

<template>
  <div class="project-config">
    <!-- Header -->
    <div class="config-header">
      <h2 class="config-title">🏗 Project Configuration</h2>
      <button class="btn-add" @click="openCreate">+ New Project</button>
    </div>

    <!-- 列表 -->
    <div v-if="projects.length === 0" class="empty-state">
      <div class="empty-icon">🏗</div>
      <h3>No projects yet</h3>
      <p>Create a project to group sessions and inject shared rules.</p>
    </div>

    <div v-else class="table-wrapper">
      <table class="proj-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Working Dir</th>
            <th>Sessions</th>
            <th>Rules</th>
            <th>Updated</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="p in projects"
            :key="p.id"
            class="proj-row"
            :class="{ active: p.id === activeProjectId }"
            @click="handleRowClick(p)"
          >
            <td class="cell-name">
              <div class="proj-name">{{ p.name }}</div>
              <div v-if="p.description" class="proj-desc">{{ p.description }}</div>
            </td>
            <td class="cell-dir">{{ p.working_directory || '-' }}</td>
            <td class="cell-count">{{ p.session_count ?? 0 }}</td>
            <td class="cell-rules">
              <span v-if="readRules(p)" class="rules-badge" :title="readRules(p)">✓ rules</span>
              <span v-else class="text-muted">-</span>
            </td>
            <td class="cell-date">{{ formatDate(p.updated_at) }}</td>
            <td class="cell-actions" @click.stop>
              <button class="btn-action btn-edit" @click="openEdit(p)" title="Edit">✏</button>
              <button
                class="btn-action btn-delete"
                @click="confirmDelete(p)"
                :title="p.id === 'default' ? 'Default project cannot be deleted' : 'Delete'"
                :disabled="p.id === 'default'"
              >🗑</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Create / Edit 表单弹窗 -->
    <div v-if="showForm" class="modal-overlay" @click.self="closeForm">
      <div class="modal">
        <div class="modal-header">
          <h3>{{ isEditing ? 'Edit Project' : 'New Project' }}</h3>
          <button class="modal-close" @click="closeForm">&times;</button>
        </div>

        <div class="modal-body">
          <div v-if="formError" class="form-error">{{ formError }}</div>

          <div class="form-group">
            <label class="form-label">Name <span class="required">*</span></label>
            <input v-model="form.name" type="text" class="form-input" placeholder="My Project" />
          </div>

          <div class="form-group">
            <label class="form-label">Description</label>
            <textarea
              v-model="form.description"
              class="form-input form-textarea"
              rows="2"
              placeholder="What is this project about?"
            ></textarea>
          </div>

          <div class="form-group">
            <label class="form-label">Working Directory</label>
            <input
              v-model="form.working_directory"
              type="text"
              class="form-input"
              placeholder="/home/user/projects/my-app"
            />
            <span class="form-hint">Default working directory for shell commands in this project</span>
          </div>

          <!-- Project Rules：归属于此 project 的所有 session 自动注入到 system prompt -->
          <div class="form-group">
            <div class="form-label-row">
              <label class="form-label">Project Rules</label>
              <button
                v-if="rulesPreview"
                type="button"
                class="preview-toggle"
                @click="showRulesPreview = !showRulesPreview"
              >
                {{ showRulesPreview ? '隐藏注入预览' : '预览注入效果' }}
              </button>
            </div>
            <textarea
              v-model="form.rules"
              class="form-input form-textarea form-rules"
              rows="6"
              placeholder="Rules automatically injected into every session under this project, e.g.&#10;- Always respond in Chinese&#10;- Use Go standard library first&#10;- Commit with prefix 'feat(ui-v2):'"
            ></textarea>
            <span class="form-hint">
              自动注入到本 project 下所有 session 的 system prompt，类似项目级记忆，
              避免每次对话重复说明上下文约定。保存后仅对新发起的任务生效。
            </span>
            <!-- 注入预览：与后端 projectRulesPrompt 格式一致（## Project Rules 标题 + 原文） -->
            <div v-if="showRulesPreview && rulesPreview" class="rules-preview">
              <div class="rules-preview-title">system prompt 注入预览</div>
              <pre class="rules-preview-body">{{ rulesPreview }}</pre>
            </div>
          </div>
        </div>

        <div class="modal-footer">
          <button class="btn-cancel" @click="closeForm" :disabled="saving">Cancel</button>
          <button class="btn-save" @click="handleSave" :disabled="saving || !form.name.trim()">
            {{ saving ? 'Saving...' : (isEditing ? 'Update' : 'Create') }}
          </button>
        </div>
      </div>
    </div>

    <!-- Delete 确认弹窗 -->
    <div v-if="showDeleteConfirm" class="modal-overlay" @click.self="cancelDelete">
      <div class="modal modal-small">
        <div class="modal-header">
          <h3>Delete Project</h3>
          <button class="modal-close" @click="cancelDelete">&times;</button>
        </div>
        <div class="modal-body">
          <p class="confirm-text">
            Delete <strong>{{ deleteTarget?.name }}</strong> and all its sessions?
          </p>
          <p class="confirm-hint">This action cannot be undone.</p>
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
.project-config {
  padding: 1.25rem;
}

/* ---- Header ---- */
.config-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-bottom: 1.25rem;
  padding-bottom: 0.75rem;
  border-bottom: 1px solid var(--border-default);
}

.config-title {
  flex: 1;
  font-size: 1.125rem;
  font-weight: 700;
  color: var(--text-primary);
  margin: 0;
}

.btn-add {
  background: var(--accent-running);
  color: var(--text-primary);
  border: none;
  border-radius: var(--radius-md);
  padding: 0.375rem 1rem;
  font-size: 0.812rem;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s;
}

.btn-add:hover {
  filter: brightness(1.1);
}

/* ---- Empty state ---- */
.empty-state {
  text-align: center;
  padding: 3.75rem 1.25rem;
  color: var(--text-secondary);
}

.empty-icon {
  font-size: 3rem;
  margin-bottom: 0.75rem;
}

.empty-state h3 {
  font-size: 1rem;
  color: var(--text-primary);
  margin-bottom: 0.375rem;
}

.empty-state p {
  font-size: 0.812rem;
}

/* ---- Table ---- */
.table-wrapper {
  overflow-x: auto;
}

.proj-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.812rem;
}

.proj-table thead {
  background: var(--bg-elevated);
  border-bottom: 2px solid var(--border-default);
}

.proj-table th {
  text-align: left;
  padding: 0.625rem 0.75rem;
  color: var(--text-muted);
  font-weight: 600;
  font-size: 0.688rem;
  text-transform: uppercase;
  letter-spacing: 0.03125rem;
  white-space: nowrap;
}

.proj-table td {
  padding: 0.625rem 0.75rem;
  border-bottom: 1px solid var(--border-subtle);
  vertical-align: top;
}

.proj-row {
  cursor: pointer;
  transition: background 0.15s;
}

.proj-row:hover {
  background: var(--bg-elevated);
}

.proj-row.active {
  background: rgba(0, 229, 255, 0.08);
}

.cell-name {
  min-width: 8.75rem;
}

.proj-name {
  color: var(--text-primary);
  font-weight: 600;
}

.proj-row.active .proj-name {
  color: var(--accent-running);
}

.proj-desc {
  font-size: 0.688rem;
  color: var(--text-muted);
  margin-top: 0.125rem;
  max-width: 15rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cell-dir {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--text-secondary);
  max-width: 14rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cell-count,
.cell-date {
  font-size: 0.75rem;
  color: var(--text-muted);
  white-space: nowrap;
}

.cell-rules .rules-badge {
  display: inline-block;
  padding: 0.125rem 0.5rem;
  border-radius: var(--radius-sm);
  background: rgba(0, 229, 255, 0.12);
  color: var(--accent-running);
  font-size: 0.688rem;
  font-weight: 600;
}

.cell-actions {
  white-space: nowrap;
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

.text-muted {
  color: var(--text-muted);
}

/* ---- Modal ---- */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  backdrop-filter: blur(0.125rem);
}

.modal {
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-lg);
  width: 38.75rem;
  max-width: 95vw;
  max-height: 85vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 0.5rem 2rem rgba(0, 0, 0, 0.5);
}

.modal-small {
  width: 27.5rem;
}

.modal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0.875rem 1.125rem;
  border-bottom: 1px solid var(--border-default);
}

.modal-header h3 {
  font-size: 0.938rem;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0;
}

.modal-close {
  background: transparent;
  border: none;
  color: var(--text-muted);
  font-size: 1.375rem;
  cursor: pointer;
  line-height: 1;
  padding: 0 0.25rem;
  transition: color 0.15s;
}

.modal-close:hover {
  color: var(--text-primary);
}

.modal-body {
  padding: 1.125rem;
  overflow-y: auto;
  flex: 1;
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 0.625rem;
  padding: 0.75rem 1.125rem;
  border-top: 1px solid var(--border-default);
}

/* ---- Form ---- */
.form-group {
  margin-bottom: 0.875rem;
}

.form-label {
  display: block;
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--text-secondary);
  margin-bottom: 0.312rem;
}

.required {
  color: var(--accent-danger);
}

.form-input {
  width: 100%;
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  padding: 0.438rem 0.625rem;
  font-size: 0.812rem;
  font-family: var(--font-mono);
  outline: none;
  transition: border-color 0.2s;
  box-sizing: border-box;
}

.form-input:focus {
  border-color: var(--accent-running);
}

.form-textarea {
  resize: vertical;
  font-family: var(--font-display);
  line-height: 1.5;
  min-height: 3rem;
}

.form-rules {
  font-family: var(--font-mono);
  line-height: 1.6;
  min-height: 9rem;
}

/* ---- Rules 预览 ---- */
.form-label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 0.312rem;
}

.preview-toggle {
  background: transparent;
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  color: var(--accent-running);
  font-family: var(--font-display);
  font-size: 0.688rem;
  font-weight: 600;
  padding: 0.125rem 0.5rem;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}

.preview-toggle:hover {
  background: rgba(0, 229, 255, 0.08);
  border-color: var(--accent-running);
}

.rules-preview {
  margin-top: 0.5rem;
  background: var(--bg-canvas);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.rules-preview-title {
  padding: 0.312rem 0.625rem;
  font-family: var(--font-display);
  font-size: 0.625rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
  border-bottom: 1px solid var(--border-subtle);
  background: var(--bg-elevated);
}

.rules-preview-body {
  margin: 0;
  padding: 0.625rem 0.75rem;
  font-family: var(--font-mono);
  font-size: 0.75rem;
  line-height: 1.5;
  color: var(--text-secondary);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 14rem;
  overflow-y: auto;
}

.form-hint {
  display: block;
  font-size: 0.688rem;
  color: var(--text-muted);
  margin-top: 0.312rem;
  line-height: 1.4;
}

.form-error {
  background: rgba(231, 76, 60, 0.15);
  border: 1px solid rgba(255, 107, 107, 0.32);
  color: var(--accent-danger);
  padding: 0.5rem 0.75rem;
  border-radius: var(--radius-md);
  font-size: 0.75rem;
  margin-bottom: 0.875rem;
}

/* ---- Modal buttons ---- */
.btn-cancel {
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  color: var(--text-secondary);
  font-size: 0.812rem;
  padding: 0.438rem 1rem;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background 0.15s;
}

.btn-cancel:hover:not(:disabled) {
  background: var(--bg-hover);
}

.btn-cancel:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-save {
  background: var(--accent-running);
  color: var(--text-primary);
  border: none;
  border-radius: var(--radius-md);
  padding: 0.438rem 1.25rem;
  font-size: 0.812rem;
  font-weight: 600;
  cursor: pointer;
  transition: filter 0.2s;
}

.btn-save:hover:not(:disabled) {
  filter: brightness(1.1);
}

.btn-save:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-delete-confirm {
  background: var(--accent-danger);
  color: var(--text-primary);
  border: none;
  border-radius: var(--radius-md);
  padding: 0.438rem 1.25rem;
  font-size: 0.812rem;
  font-weight: 600;
  cursor: pointer;
  transition: filter 0.2s;
}

.btn-delete-confirm:hover:not(:disabled) {
  filter: brightness(1.1);
}

.btn-delete-confirm:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.confirm-text {
  font-size: 0.875rem;
  color: var(--text-primary);
  margin-bottom: 0.5rem;
}

.confirm-hint {
  font-size: 0.75rem;
  color: var(--text-muted);
  line-height: 1.4;
}
</style>
