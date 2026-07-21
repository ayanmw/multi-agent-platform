<!-- TodoPanel.vue — Session 级 TODO 列表面板

     功能：
       - 展示当前 session 的 TODO 列表（按活跃/优先级排序）
       - 支持新增 TODO
       - 支持快速改状态（pending → in_progress → done）
       - 支持点击展开编辑内容与删除
       - 支持清理已完成（done + cancelled）

     数据流：
       打开面板时调用 loadTodos(sessionId) 主动拉取；
       todo_list_changed WebSocket 事件通过 useTodoStore 实时同步。
-->
<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useTodoStore } from '../composables/useTodoStore'
import type { Todo, TodoStatus, TodoPriority } from '../types/todo'

const props = defineProps<{
  sessionId: string
}>()

const emit = defineEmits<{
  (e: 'close'): void
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
  <div class="todo-overlay" @click.self="emit('close')">
    <div class="todo-panel">
      <div class="todo-header">
        <h2 class="todo-title">📝 Session TODOs</h2>
        <div class="todo-header-actions">
          <button
            v-if="hasCompleted"
            class="todo-clear-btn"
            @click="handleClearCompleted"
            title="Clear completed"
          >
            Clear Done
          </button>
          <button class="todo-close-btn" @click="emit('close')" title="Close">× Close</button>
        </div>
      </div>

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
    </div>
  </div>
</template>

<style scoped>
.todo-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.72);
  z-index: 100;
  display: flex;
  justify-content: center;
  padding: 24px;
  backdrop-filter: blur(2px);
}

.todo-panel {
  width: 100%;
  max-width: 640px;
  height: calc(100vh - 48px);
  background: var(--bg-primary, #18181b);
  border: 1px solid var(--border-primary, #333);
  border-radius: 12px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.5);
}

.todo-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border-primary, #333);
  background: var(--bg-secondary, #1e1e22);
  flex-shrink: 0;
}

.todo-title {
  font-size: 16px;
  font-weight: 600;
  color: #e0e0e0;
  margin: 0;
}

.todo-header-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.todo-clear-btn {
  background: #2a2a2a;
  border: 1px solid #444;
  color: #aaa;
  font-size: 12px;
  padding: 5px 10px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.15s;
}
.todo-clear-btn:hover {
  background: #3a3a3a;
  color: #fff;
  border-color: #555;
}

.todo-close-btn {
  background: #2a2a2a;
  border: 1px solid #444;
  color: #ccc;
  font-size: 13px;
  padding: 5px 12px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.15s;
  font-weight: 500;
}
.todo-close-btn:hover {
  background: #444;
  color: #fff;
}

.todo-create {
  display: flex;
  gap: 8px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border-primary, #333);
  flex-shrink: 0;
}

.todo-create-input {
  flex: 1;
  background: var(--bg-secondary, #1e1e22);
  border: 1px solid #444;
  color: #ddd;
  font-size: 13px;
  padding: 8px 10px;
  border-radius: 6px;
  outline: none;
}
.todo-create-input:focus {
  border-color: #4a9eff;
}

.todo-create-priority,
.todo-edit-priority {
  background: var(--bg-secondary, #1e1e22);
  border: 1px solid #444;
  color: #ddd;
  font-size: 12px;
  padding: 6px 8px;
  border-radius: 6px;
  outline: none;
}

.todo-create-btn {
  background: #4a9eff;
  color: #fff;
  border: none;
  border-radius: 6px;
  padding: 8px 14px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.15s;
}
.todo-create-btn:hover:not(:disabled) {
  background: #3a8eef;
}
.todo-create-btn:disabled {
  background: #2a4a6a;
  cursor: not-allowed;
}

.todo-error {
  margin: 10px 18px 0;
  padding: 8px 12px;
  background: rgba(231, 76, 60, 0.12);
  border: 1px solid rgba(231, 76, 60, 0.3);
  color: #e74c3c;
  border-radius: 6px;
  font-size: 12px;
}

.todo-loading,
.todo-empty {
  padding: 40px 20px;
  text-align: center;
  color: #888;
  font-size: 13px;
}

.todo-list {
  list-style: none;
  margin: 0;
  padding: 8px 0;
  overflow-y: auto;
  flex: 1;
}

.todo-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 18px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.04);
  transition: background 0.1s;
}
.todo-item:hover {
  background: rgba(255, 255, 255, 0.02);
}
.todo-item-terminal {
  opacity: 0.65;
}

.todo-status-badge {
  flex-shrink: 0;
  margin-top: 2px;
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
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

.status-pending { background: rgba(74, 158, 255, 0.15); color: #4a9eff; }
.status-in_progress { background: rgba(240, 160, 48, 0.15); color: #f0a030; }
.status-done { background: rgba(81, 207, 102, 0.15); color: #51cf66; }
.status-cancelled { background: rgba(149, 149, 149, 0.15); color: #999; }

.todo-content {
  flex: 1;
  min-width: 0;
  cursor: pointer;
}

.todo-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.todo-item-title {
  font-size: 13px;
  color: #e0e0e0;
  word-break: break-word;
}
.todo-title-done {
  text-decoration: line-through;
  color: #888;
}

.todo-priority {
  font-size: 10px;
  font-weight: 600;
  padding: 1px 5px;
  border-radius: 8px;
  flex-shrink: 0;
}
.priority-3 { background: rgba(231, 76, 60, 0.15); color: #e74c3c; }
.priority-2 { background: rgba(240, 160, 48, 0.15); color: #f0a030; }
.priority-1 { background: rgba(149, 149, 149, 0.12); color: #aaa; }

.todo-item-desc {
  font-size: 11px;
  color: #888;
  margin-top: 4px;
  white-space: pre-wrap;
  word-break: break-word;
}

.todo-delete-btn {
  flex-shrink: 0;
  background: transparent;
  border: none;
  color: #666;
  font-size: 18px;
  line-height: 1;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s, color 0.15s;
  padding: 0 4px;
}
.todo-item:hover .todo-delete-btn {
  opacity: 1;
}
.todo-delete-btn:hover {
  color: #e74c3c;
}

.todo-edit-form {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.todo-edit-input {
  background: var(--bg-secondary, #1e1e22);
  border: 1px solid #444;
  color: #ddd;
  font-size: 13px;
  padding: 6px 8px;
  border-radius: 6px;
  outline: none;
}
.todo-edit-input:focus {
  border-color: #4a9eff;
}

.todo-edit-desc {
  background: var(--bg-secondary, #1e1e22);
  border: 1px solid #444;
  color: #ddd;
  font-size: 12px;
  padding: 6px 8px;
  border-radius: 6px;
  outline: none;
  resize: vertical;
  font-family: inherit;
}
.todo-edit-desc:focus {
  border-color: #4a9eff;
}

.todo-edit-actions {
  display: flex;
  gap: 8px;
}

.todo-save-btn {
  background: #4a9eff;
  color: #fff;
  border: none;
  border-radius: 6px;
  padding: 5px 12px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
}
.todo-save-btn:hover {
  background: #3a8eef;
}

.todo-cancel-btn {
  background: #2a2a2a;
  border: 1px solid #444;
  color: #aaa;
  border-radius: 6px;
  padding: 5px 12px;
  font-size: 12px;
  cursor: pointer;
}
.todo-cancel-btn:hover {
  background: #3a3a3a;
  color: #fff;
}
</style>
