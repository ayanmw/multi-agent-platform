<!-- TodoPanel.vue — v2 Session 级 TODO 列表面板

     功能：
       - 展示当前 session 的 TODO 树形列表（支持嵌套子任务）
       - 支持新增 TODO
       - 支持快速改状态（pending → in_progress → done），父任务可级联完成子任务
       - 支持点击展开编辑内容与删除
       - 支持拖拽排序与层级调整（顶层 ↔ 子任务）
       - 支持折叠/展开子任务
       - 支持清理已完成（done + cancelled）

     数据流：
       打开面板时调用 loadTodoTree(sessionId) 主动拉取树形结构；
       todo_list_changed WebSocket 事件通过 useTodoStore 实时同步。

     样式说明：
       复用 v2 设计系统变量：--bg-panel / --bg-elevated / --border-default /
       --text-primary / --text-secondary / --text-muted / --accent-running /
       --accent-success / --accent-warning / --accent-danger / --font-display /
       --font-mono / --space-* / --radius-*，与 ManageContent 内其它 tab 风格一致。
-->
<script setup lang="ts">
import { ref, computed, watch, nextTick, h, defineComponent, type PropType } from 'vue'
import { useTodoStore } from '@/composables/useTodoStore'
import type { Todo, TodoStatus, TodoPriority } from '@/types/todo'

const props = defineProps<{
  sessionId: string
}>()

const {
  todosOf,
  loadTodoTree,
  createTodo,
  updateTodo,
  updateTodoStatus,
  reorderTodos,
  deleteTodo,
  clearCompleted,
} = useTodoStore()

/** 当前 session 的 TODO 树形列表（已从 store 排序）。 */
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
    // 使用树形加载接口，支持嵌套子任务展示。
    await loadTodoTree(sid)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Load failed'
  } finally {
    loading.value = false
  }
}

/** 创建新 TODO */
async function handleCreate(parentTodoId?: string) {
  const title = newTitle.value.trim()
  if (!title || !props.sessionId) return
  isCreating.value = true
  try {
    await createTodo(props.sessionId, title, { priority: newPriority.value, parentTodoId })
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
  const target = next[todo.status]
  try {
    const hasChildren = (todo.children?.length ?? 0) > 0
    const cascade = hasChildren && (target === 'done' || target === 'cancelled')
      ? confirm(`Also mark ${todo.children?.length} subtask(s) as ${statusLabel(target)}?`)
      : false
    await updateTodoStatus(props.sessionId, todo.id, target)
    if (cascade) {
      for (const child of todo.children || []) {
        await updateTodoStatus(props.sessionId, child.id, target)
      }
    }
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

// ========== Drag & Drop ==========
/** 当前被拖拽的 todo id。 */
const draggingId = ref<string | null>(null)
/** 拖拽放置时目标父级 id（空字符串表示顶层）。 */
const dragOverParentId = ref<string | null>(null)
/** 拖拽放置目标索引，用于占位线提示。 */
const dragOverIndex = ref<number | null>(null)

function onDragStart(todo: Todo, e: DragEvent) {
  draggingId.value = todo.id
  if (e.dataTransfer) {
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', todo.id)
  }
}

function onDragOver(parentId: string, index: number, e: DragEvent) {
  e.preventDefault()
  if (e.dataTransfer) {
    e.dataTransfer.dropEffect = 'move'
  }
  dragOverParentId.value = parentId
  dragOverIndex.value = index
}

function onDragLeave() {
  dragOverParentId.value = null
  dragOverIndex.value = null
}

async function onDrop(targetParentId: string, targetIndex: number, e: DragEvent) {
  e.preventDefault()
  const draggedId = draggingId.value
  if (!draggedId || !props.sessionId) {
    resetDrag()
    return
  }

  const moves = buildMoveList(draggedId, targetParentId, targetIndex)
  if (moves.length > 0) {
    try {
      await reorderTodos(props.sessionId, moves)
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Reorder failed'
    }
  }

  resetDrag()
}

function resetDrag() {
  draggingId.value = null
  dragOverParentId.value = null
  dragOverIndex.value = null
}

/**
 * 根据拖拽结果生成批量 moves。
 * 策略：将被拖拽项插入到目标位置，同 parent 下其它项重新计算 sort_order。
 * 为了简化，这里使用列表索引作为 sort_order；真实场景下可以预留更大间隔。
 */
function buildMoveList(draggedId: string, targetParentId: string, targetIndex: number): Array<{ id: string; parentTodoId: string; sortOrder: number }> {
  const flat = flattenTodos(todos.value)
  const draggedIndex = flat.findIndex(t => t.id === draggedId)
  if (draggedIndex < 0) return []

  const dragged = flat[draggedIndex]
  // 避免把父任务拖进自己的子任务里（形成环）。
  if (wouldCreateCycle(draggedId, targetParentId, flat)) return []

  // 从扁平列表中移除被拖拽项
  const withoutDragged = flat.filter(t => t.id !== draggedId)
  // 计算在目标 parent 下的插入位置
  const sameParent = withoutDragged.filter(t => t.parent_todo_id === targetParentId)
  const beforeTarget = sameParent.slice(0, targetIndex)
  const afterTarget = sameParent.slice(targetIndex)

  const reorderedSameParent = [...beforeTarget, dragged, ...afterTarget]
  const moves: Array<{ id: string; parentTodoId: string; sortOrder: number }> = []
  reorderedSameParent.forEach((t, idx) => {
    moves.push({ id: t.id, parentTodoId: targetParentId, sortOrder: idx })
  })
  return moves
}

/** 把树形 todo 列表拍平（仅顶层顺序 + 子任务）。 */
function flattenTodos(items: Todo[]): Todo[] {
  const result: Todo[] = []
  function walk(list: Todo[]) {
    for (const t of list) {
      result.push(t)
      if (t.children?.length) walk(t.children)
    }
  }
  walk(items)
  return result
}

/** 判断把 draggedId 设为 targetParentId 的子任务是否会形成循环。 */
function wouldCreateCycle(draggedId: string, targetParentId: string, flat: Todo[]): boolean {
  if (!targetParentId) return false
  let current = targetParentId
  const byId = new Map(flat.map(t => [t.id, t]))
  while (current) {
    if (current === draggedId) return true
    const t = byId.get(current)
    if (!t) break
    current = t.parent_todo_id
  }
  return false
}

/** 折叠状态管理：记录被收起 parent 的 id 集合。 */
const collapsed = ref<Set<string>>(new Set())

function toggleCollapse(todoId: string) {
  if (collapsed.value.has(todoId)) {
    collapsed.value.delete(todoId)
  } else {
    collapsed.value.add(todoId)
  }
}

/** 判断当前拖拽区域是否应该显示占位线。 */
function isDropIndicator(parentId: string, index: number): boolean {
  return dragOverParentId.value === parentId && dragOverIndex.value === index
}

// TodoItemList 是内部使用的树形子组件，复用同一文件减少拆分成本。
// 它负责渲染一层 todo 并递归渲染 children，同时处理本层的拖拽放置区。
const TodoItemList: any = defineComponent({
  name: 'TodoItemList',
  props: {
    items: { type: Array as PropType<Todo[]>, required: true },
    sessionId: { type: String, required: true },
    editingId: { type: String, default: null },
    draggingId: { type: String, default: null },
    collapsed: { type: Object as PropType<Set<string>>, required: true },
    parentId: { type: String, default: '' },
  },
  emits: ['dragStart', 'dragOver', 'dragLeave', 'drop', 'toggleStatus', 'startEdit', 'saveEdit', 'cancelEdit', 'delete', 'toggleCollapse', 'createSub'],
  setup(props, { emit }) {
    return () => {
      const list = props.items
      return h('ul', { class: 'todo-list', 'data-parent-id': props.parentId || 'root' }, [
        // 顶层放置区：在第一个项目之前插入。
        h('li', {
          class: ['todo-drop-zone', { active: isDropIndicator(props.parentId, 0) }],
          onDragover: (e: DragEvent) => emit('dragOver', props.parentId, 0, e),
          onDragleave: () => emit('dragLeave'),
          onDrop: (e: DragEvent) => emit('drop', props.parentId, 0, e),
        }, h('span', { class: 'todo-drop-line' })),
        ...list.flatMap((todo, index) => {
          const isEditing = props.editingId === todo.id
          const hasChildren = (todo.children?.length ?? 0) > 0
          const isCollapsed = hasChildren && props.collapsed.has(todo.id)
          return [
            h('li', {
              key: todo.id,
              class: ['todo-item', { 'todo-item-terminal': todo.status === 'done' || todo.status === 'cancelled', dragging: props.draggingId === todo.id }],
              draggable: true,
              onDragstart: (e: DragEvent) => emit('dragStart', todo, e),
            }, [
              // 折叠按钮
              hasChildren
                ? h('button', {
                    class: 'todo-collapse-btn',
                    title: isCollapsed ? 'Expand' : 'Collapse',
                    onClick: () => emit('toggleCollapse', todo.id),
                  }, isCollapsed ? '▶' : '▼')
                : h('span', { class: 'todo-collapse-placeholder' }),

              h('button', {
                class: ['todo-status-badge', statusClass(todo.status)],
                onClick: () => emit('toggleStatus', todo),
                title: `Click to change status (currently ${statusLabel(todo.status)})`,
              }, statusLabel(todo.status)),

              isEditing
                ? h('div', { class: 'todo-edit-form' }, [
                    h('input', {
                      class: 'todo-edit-input',
                      type: 'text',
                      value: editTitle.value,
                      onInput: (e: Event) => { editTitle.value = (e.target as HTMLInputElement).value },
                      onKeydown: (e: KeyboardEvent) => {
                        if (e.key === 'Enter') emit('saveEdit', todo)
                        if (e.key === 'Escape') emit('cancelEdit')
                      },
                    }),
                    h('textarea', {
                      class: 'todo-edit-desc',
                      rows: 2,
                      placeholder: 'Description (optional)',
                      value: editDescription.value,
                      onInput: (e: Event) => { editDescription.value = (e.target as HTMLTextAreaElement).value },
                    }),
                    h('select', {
                      class: 'todo-edit-priority',
                      value: editPriority.value,
                      onChange: (e: Event) => { editPriority.value = Number((e.target as HTMLSelectElement).value) as TodoPriority },
                    }, [
                      h('option', { value: 1 }, 'Low'),
                      h('option', { value: 2 }, 'Medium'),
                      h('option', { value: 3 }, 'High'),
                    ]),
                    h('div', { class: 'todo-edit-actions' }, [
                      h('button', { class: 'todo-save-btn', onClick: () => emit('saveEdit', todo) }, 'Save'),
                      h('button', { class: 'todo-cancel-btn', onClick: () => emit('cancelEdit') }, 'Cancel'),
                    ]),
                  ])
                : h('div', { class: 'todo-content', onClick: () => emit('startEdit', todo) }, [
                    h('div', { class: 'todo-title-row' }, [
                      h('span', { class: ['todo-item-title', { 'todo-title-done': todo.status === 'done' }] }, todo.title),
                      h('span', { class: ['todo-priority', `priority-${todo.priority}`] }, priorityLabel(todo.priority)),
                    ]),
                    todo.description ? h('div', { class: 'todo-item-desc' }, todo.description) : null,
                  ]),

              h('button', {
                class: 'todo-delete-btn',
                onClick: (e: Event) => { e.stopPropagation(); emit('delete', todo) },
                title: 'Delete',
              }, '×'),
            ]),

            // 子任务递归列表
            hasChildren && !isCollapsed
              ? h(TodoItemList, {
                  items: todo.children,
                  sessionId: props.sessionId,
                  editingId: props.editingId,
                  draggingId: props.draggingId,
                  collapsed: props.collapsed,
                  parentId: todo.id,
                  onDragStart: (todoArg: Todo, e: DragEvent) => emit('dragStart', todoArg, e),
                  onDragOver: (parentId: string, idx: number, e: DragEvent) => emit('dragOver', parentId, idx, e),
                  onDragLeave: () => emit('dragLeave'),
                  onDrop: (parentId: string, idx: number, e: DragEvent) => emit('drop', parentId, idx, e),
                  onToggleStatus: (t: Todo) => emit('toggleStatus', t),
                  onStartEdit: (t: Todo) => emit('startEdit', t),
                  onSaveEdit: (t: Todo) => emit('saveEdit', t),
                  onCancelEdit: () => emit('cancelEdit'),
                  onDelete: (t: Todo) => emit('delete', t),
                  onToggleCollapse: (id: string) => emit('toggleCollapse', id),
                  onCreateSub: (parentId: string) => emit('createSub', parentId),
                })
              : null,

            // 当前项目之后的放置区
            h('li', {
              class: ['todo-drop-zone', { active: isDropIndicator(props.parentId, index + 1) }],
              onDragover: (e: DragEvent) => emit('dragOver', props.parentId, index + 1, e),
              onDragleave: () => emit('dragLeave'),
              onDrop: (e: DragEvent) => emit('drop', props.parentId, index + 1, e),
            }, h('span', { class: 'todo-drop-line' })),
          ]
        }),
      ])
    }
  },
})
</script>

<template>
  <div class="todo-panel">
    <div class="todo-create">
      <input
        v-model="newTitle"
        class="todo-create-input"
        type="text"
        placeholder="Add a new todo..."
        @keydown.enter="handleCreate()"
      />
      <select v-model="newPriority" class="todo-create-priority" title="Priority">
        <option :value="1">Low</option>
        <option :value="2">Medium</option>
        <option :value="3">High</option>
      </select>
      <button
        class="todo-create-btn"
        :disabled="!newTitle.trim() || isCreating"
        @click="handleCreate()"
      >
        Add
      </button>
    </div>

    <div v-if="error" class="todo-error">{{ error }}</div>

    <div v-if="loading && todos.length === 0" class="todo-loading">Loading...</div>

    <div v-else-if="todos.length === 0" class="todo-empty">
      No active TODOs for this session.
    </div>

    <TodoItemList
      v-else
      :items="todos"
      :session-id="props.sessionId"
      :editing-id="editingId"
      :dragging-id="draggingId"
      :collapsed="collapsed"
      parent-id=""
      @drag-start="onDragStart"
      @drag-over="onDragOver"
      @drag-leave="onDragLeave"
      @drop="onDrop"
      @toggle-status="handleQuickStatus"
      @start-edit="startEdit"
      @save-edit="handleSaveEdit"
      @cancel-edit="cancelEdit"
      @delete="handleDelete"
      @toggle-collapse="toggleCollapse"
      @create-sub="handleCreate"
    />

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
.todo-item.dragging {
  opacity: 0.4;
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

.todo-collapse-btn,
.todo-collapse-placeholder {
  flex-shrink: 0;
  width: 1.1rem;
  text-align: center;
  background: transparent;
  border: none;
  color: var(--text-muted);
  font-size: 0.65rem;
  cursor: pointer;
  padding: 0;
  margin-top: 4px;
}
.todo-collapse-placeholder {
  cursor: default;
}

.todo-drop-zone {
  list-style: none;
  height: 4px;
  margin: 2px 0;
  padding: 0;
  position: relative;
  transition: height 0.1s;
}
.todo-drop-zone.active {
  height: 12px;
}
.todo-drop-line {
  position: absolute;
  left: 0;
  right: 0;
  top: 50%;
  height: 2px;
  background: transparent;
  border-radius: 1px;
  transform: translateY(-50%);
}
.todo-drop-zone.active .todo-drop-line {
  background: var(--accent-running, #00e5ff);
}
</style>
