<!-- TodoPanel.vue — v2 Session 级 TODO 列表面板

     功能：
       - 展示当前 session 的 TODO 列表（按活跃/优先级排序）
       - 支持新增 TODO
       - 支持快速改状态（pending → in_progress → done）
       - 支持点击展开编辑内容与删除
       - 支持清理已完成（done + cancelled）

     数据流：
       打开面板时调用 loadTodos(sessionId) 主动拉取；
       todo_list_changed WebSocket 事件通过 useTodoStore 实时同步。

     样式说明：
       复用 v2 设计系统变量：--bg-panel / --bg-elevated / --border-default /
       --text-primary / --text-secondary / --text-muted / --accent-running /
       --accent-success / --accent-warning / --accent-danger / --font-display /
       --font-mono / --space-* / --radius-*，与 ManageContent 内其它 tab 风格一致。
-->
<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useTodoStore } from '@/composables/useTodoStore'
import type { Todo, TodoStatus, TodoPriority } from '@/types/todo'

const props = defineProps<{
  sessionId: string
}>()

const { todosOf, loadTodos, createTodo, updateTodo, updateTodoStatus, deleteTodo, clearCompleted } = useTodoStore()

/** 当前 session 的 TODO 列表（已排序） */
const todos = computed(() => todosOf(props.sessionId))

/** 新增输入框绑定 */
const newTitle = ref('')
const newPriority = ref<TodoPriority>(1)
const isCreating = ref(false)

/** 当前处于编辑模式的 todo id */
const editingId = ref<string | null>(null)
const editTitle = ref('')
const editDescription = ref('')
const editPriority = ref<TodoPriority>(1)

/** 加载与错误状态 */
const loading = ref(false)
const error = ref('')

watch(() => props.sessionId, (sid) => {
  if (sid) {
    load(sid)
  }
}, { immediate: true })

async function load(sid: string) {
  loading.value = true
  error.value = ''
  try {
    await loadTodos(sid)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Load failed'
  } finally {
    loading.value = false
  }
}

/** 创建新 TODO */
async function handleCreate() {
  const title = newTitle.value.trim()
  if (!title || !props.sessionId) return
  isCreating.value = true
  try {
    await createTodo(props.sessionId, title, { priority: newPriority.value })
    newTitle.value = ''
    newPriority.value = 1
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Create failed'
  } finally {
    isCreating.value = false
  }
}

/** 快速切换一个 TODO 的状态：pending → in_progress → done。 */
async function handleQuickStatus(todo: Todo) {
  const next: Record<TodoStatus, TodoStatus> = {
    pending: 'in_progress',
    in_progress: 'done',
    done: 'pending',
    cancelled: 'in_progress',
  }
  try {
    await updateTodoStatus(props.sessionId, todo.id, next[todo.status])
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Status update failed'
  }
}

/** 进入编辑模式。 */
function startEdit(todo: Todo) {
  editingId.value = todo.id
  editTitle.value = todo.title
  editDescription.value = todo.description || ''
  editPriority.value = todo.priority
  nextTick(() => {
    const input = document.querySelector('.todo-edit-input') as HTMLInputElement | null
    input?.focus()
  })
}

/** 保存编辑。 */
async function handleSaveEdit(todo: Todo) {
  const title = editTitle.value.trim()
  if (!title) return
  try {
    await updateTodo(props.sessionId, todo.id, {
      title,
      description: editDescription.value.trim(),
      priority: editPriority.value,
    })
    editingId.value = null
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Update failed'
  }
}

/** 取消编辑。 */
function cancelEdit() {
  editingId.value = null
  editTitle.value = ''
  editDescription.value = ''
  editPriority.value = 1
}

/** 删除 TODO（带确认） */
async function handleDelete(todo: Todo) {
  if (!confirm(`Delete "${todo.title}"?`)) return
  try {
    await deleteTodo(props.sessionId, todo.id)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Delete failed'
  }
}

/** 清理已完成的 TODO。 */
async function handleClearCompleted() {
  if (!confirm('Clear all completed todos?')) return
  try {
    await clearCompleted(props.sessionId, true)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Clear failed'
  }
}

/** 状态对应的显示标签与样式。 */
function statusClass(status: TodoStatus): string {
  switch (status) {
    case 'pending': return 'status-pending'
    case 'in_progress': return 'status-in_progress'
    case 'done': return 'status-done'
    case 'cancelled': return 'status-cancelled'
  }
}

function statusLabel(status: TodoStatus): string {
  switch (status) {
    case 'pending': return 'Pending'
    case 'in_progress': return 'In Progress'
    case 'done': return 'Done'
    case 'cancelled': return 'Cancelled'
  }
}

function priorityLabel(priority: number): string {
  switch (priority) {
    case 3: return 'High'
    case 2: return 'Medium'
    default: return 'Low'
  }
}

/** 是否至少有一个终态 TODO，用于显示清理按钮。 */
const hasCompleted = computed(() =>
  todos.value.some(t => t.status === 'done' || t.status === 'cancelled')
)
</script>

<template>
  <div class="todo-panel">
    <div class="todo-create">
      <input
        v-model="newTitle"
        class="todo-create-input"
        type="text"
        placeholder="Add a new todo..."
        @keydown.enter="handleCreate"
      />
      <select v-model="newPriority" class="todo-create-priority" title="Priority">
        <option :value="1">Low</option>
        <option :value="2">Medium</option>
        <option :value="3">High</option>
      </select>
      <button
        class="todo-create-btn"
        :disabled="!newTitle.trim() || isCreating"
        @click="handleCreate"
      >
        Add
      </button>
    </div>

    <div v-if="error" class="todo-error">{{ error }}</div>

    <div v-if="loading && todos.length === 0" class="todo-loading">Loading...</div>

    <div v-else-if="todos.length === 0" class="todo-empty">
      No active TODOs for this session.
    </div>

    <ul v-else class="todo-list">
      <li
        v-for="todo in todos"
        :key="todo.id"
        :class="['todo-item', { 'todo-item-terminal': todo.status === 'done' || todo.status === 'cancelled' }]"
      >
        <button
          class="todo-status-badge"
          :class="statusClass(todo.status)"
          @click="handleQuickStatus(todo)"
          :title="`Click to change status (currently ${statusLabel(todo.status)})`"
        >
          {{ statusLabel(todo.status) }}
        </button>

        <div v-if="editingId === todo.id" class="todo-edit-form">
          <input
            v-model="editTitle"
            class="todo-edit-input"
            type="text"
            @keydown.enter="handleSaveEdit(todo)"
            @keydown.escape="cancelEdit"
          />
          <textarea
            v-model="editDescription"
            class="todo-edit-desc"
            rows="2"
            placeholder="Description (optional)"
          ></textarea>
          <select v-model="editPriority" class="todo-edit-priority">
            <option :value="1">Low</option>
            <option :value="2">Medium</option>
            <option :value="3">High</option>
          </select>
          <div class="todo-edit-actions">
            <button class="todo-save-btn" @click="handleSaveEdit(todo)">Save</button>
            <button class="todo-cancel-btn" @click="cancelEdit">Cancel</button>
          </div>
        </div>

        <div v-else class="todo-content" @click="startEdit(todo)">
          <div class="todo-title-row">
            <span class="todo-item-title" :class="{ 'todo-title-done': todo.status === 'done' }">
              {{ todo.title }}
            </span>
            <span class="todo-priority" :class="`priority-${todo.priority}`">
              {{ priorityLabel(todo.priority) }}
            </span>
          </div>
          <div v-if="todo.description" class="todo-item-desc">{{ todo.description }}</div>
        </div>

        <button
          v-if="editingId !== todo.id"
          class="todo-delete-btn"
          @click.stop="handleDelete(todo)"
          title="Delete"
        >
          ×
        </button>
      </li>
    </ul>

    <div v-if="hasCompleted" class="todo-footer">
      <button class="todo-clear-btn" @click="handleClearCompleted">
        Clear completed
      </button>
    </div>
  </div>
</template>

<style scoped>
.todo-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: var(--space-md);
  gap: var(--space-md);
  overflow: hidden;
}

.todo-create {
  display: flex;
  gap: var(--space-sm);
  flex-shrink: 0;
}

.todo-create-input {
  flex: 1;
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  color: var(--text-primary);
  font-size: 0.8rem;
  padding: var(--space-sm);
  border-radius: var(--radius-md);
  outline: none;
  font-family: var(--font-mono);
}
.todo-create-input:focus {
  border-color: var(--accent-running);
}

.todo-create-priority,
.todo-edit-priority {
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  color: var(--text-primary);
  font-size: 0.75rem;
  padding: var(--space-sm);
  border-radius: var(--radius-md);
  outline: none;
}

.todo-create-btn {
  background: var(--accent-running);
  color: var(--text-on-accent, #0b0d10);
  border: none;
  border-radius: var(--radius-md);
  padding: var(--space-sm) var(--space-md);
  font-size: 0.75rem;
  font-weight: 600;
  font-family: var(--font-display);
  cursor: pointer;
  transition: filter 0.15s;
}
.todo-create-btn:hover:not(:disabled) {
  filter: brightness(1.1);
}
.todo-create-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.todo-error {
  padding: var(--space-sm) var(--space-md);
  background: rgba(255, 82, 82, 0.1);
  border: 1px solid rgba(255, 82, 82, 0.3);
  color: var(--accent-danger, #ff5252);
  border-radius: var(--radius-md);
  font-size: 0.75rem;
  flex-shrink: 0;
}

.todo-loading,
.todo-empty {
  padding: var(--space-xl);
  text-align: center;
  color: var(--text-muted);
  font-size: 0.8rem;
  flex-shrink: 0;
}

.todo-list {
  list-style: none;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  flex: 1;
}

.todo-item {
  display: flex;
  align-items: flex-start;
  gap: var(--space-sm);
  padding: var(--space-sm) 0;
  border-bottom: 1px solid var(--border-default);
  transition: background 0.1s;
}
.todo-item:last-child {
  border-bottom: none;
}
.todo-item:hover {
  background: rgba(255, 255, 255, 0.02);
}
.todo-item-terminal {
  opacity: 0.6;
}

.todo-status-badge {
  flex-shrink: 0;
  margin-top: 2px;
  font-size: 0.6rem;
  font-weight: 700;
  font-family: var(--font-display);
  text-transform: uppercase;
  letter-spacing: 0.04em;
  padding: 2px 6px;
  border-radius: 10px;
  border: none;
  cursor: pointer;
  transition: transform 0.1s, filter 0.1s;
}
.todo-status-badge:hover {
  filter: brightness(1.15);
  transform: translateY(-1px);
}

.status-pending { background: rgba(0, 229, 255, 0.12); color: var(--accent-running, #00e5ff); }
.status-in_progress { background: rgba(255, 171, 0, 0.12); color: var(--accent-warning, #ffab00); }
.status-done { background: rgba(0, 230, 118, 0.12); color: var(--accent-success, #00e676); }
.status-cancelled { background: rgba(255, 255, 255, 0.08); color: var(--text-muted); }

.todo-content {
  flex: 1;
  min-width: 0;
  cursor: pointer;
}

.todo-title-row {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
}

.todo-item-title {
  font-size: 0.82rem;
  color: var(--text-primary);
  word-break: break-word;
}
.todo-title-done {
  text-decoration: line-through;
  color: var(--text-muted);
}

.todo-priority {
  font-size: 0.6rem;
  font-weight: 700;
  font-family: var(--font-display);
  padding: 1px 5px;
  border-radius: 8px;
  flex-shrink: 0;
  text-transform: uppercase;
  letter-spacing: 0.03em;
}
.priority-3 { background: rgba(255, 82, 82, 0.12); color: var(--accent-danger, #ff5252); }
.priority-2 { background: rgba(255, 171, 0, 0.12); color: var(--accent-warning, #ffab00); }
.priority-1 { background: rgba(255, 255, 255, 0.06); color: var(--text-muted); }

.todo-item-desc {
  font-size: 0.7rem;
  color: var(--text-secondary);
  margin-top: var(--space-xs);
  white-space: pre-wrap;
  word-break: break-word;
  font-family: var(--font-mono);
}

.todo-delete-btn {
  flex-shrink: 0;
  background: transparent;
  border: none;
  color: var(--text-muted);
  font-size: 1.1rem;
  line-height: 1;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s, color 0.15s;
  padding: 0 var(--space-xs);
}
.todo-item:hover .todo-delete-btn {
  opacity: 1;
}
.todo-delete-btn:hover {
  color: var(--accent-danger, #ff5252);
}

.todo-edit-form {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-xs);
}

.todo-edit-input {
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  color: var(--text-primary);
  font-size: 0.8rem;
  padding: var(--space-sm);
  border-radius: var(--radius-md);
  outline: none;
  font-family: var(--font-mono);
}
.todo-edit-input:focus {
  border-color: var(--accent-running);
}

.todo-edit-desc {
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  color: var(--text-primary);
  font-size: 0.75rem;
  padding: var(--space-sm);
  border-radius: var(--radius-md);
  outline: none;
  resize: vertical;
  font-family: var(--font-mono);
  min-height: 3rem;
}
.todo-edit-desc:focus {
  border-color: var(--accent-running);
}

.todo-edit-actions {
  display: flex;
  gap: var(--space-sm);
}

.todo-save-btn {
  background: var(--accent-running);
  color: var(--text-on-accent, #0b0d10);
  border: none;
  border-radius: var(--radius-md);
  padding: var(--space-xs) var(--space-md);
  font-size: 0.72rem;
  font-weight: 600;
  font-family: var(--font-display);
  cursor: pointer;
}
.todo-save-btn:hover {
  filter: brightness(1.1);
}

.todo-cancel-btn {
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  color: var(--text-secondary);
  border-radius: var(--radius-md);
  padding: var(--space-xs) var(--space-md);
  font-size: 0.72rem;
  cursor: pointer;
}
.todo-cancel-btn:hover {
  border-color: var(--border-active);
  color: var(--text-primary);
}

.todo-footer {
  flex-shrink: 0;
  display: flex;
  justify-content: flex-end;
  padding-top: var(--space-sm);
  border-top: 1px solid var(--border-default);
}

.todo-clear-btn {
  background: transparent;
  border: 1px solid var(--border-default);
  color: var(--text-secondary);
  border-radius: var(--radius-md);
  padding: var(--space-xs) var(--space-md);
  font-size: 0.72rem;
  font-weight: 600;
  font-family: var(--font-display);
  cursor: pointer;
  transition: all 0.15s;
}
.todo-clear-btn:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
  border-color: var(--border-active);
}
</style>
