import { ref } from 'vue'
import { useToast } from './useToast'
import type { Todo, TodoStatus, TodoPriority } from '@/types/todo'

/** 模块级 todo 状态缓存，按 session_id 分组。 */
const todosBySession = ref<Record<string, Todo[]>>({})

/** Whether the store has been initialized */
let initialized = false

/** 当前正在加载的 session 集合，避免重复请求。 */
const loadingSessions = new Set<string>()

/**
 * Todo Store — 管理与 Session 绑定的 TODO 列表。
 *
 * 数据流：
 *  1) 组件打开 TodoPanel 时调用 loadTodos(sessionId) 主动拉取；
 *  2) todo.Service 写入操作后广播 `todo_list_changed`，v2 useTaskStore 收到后调用
 *     setTodos(sessionId, todos) 同步本 store，实现免轮询实时更新。
 */
export function useTodoStore() {
  if (!initialized) {
    initialized = true
  }

  const { showError } = useToast()

  /** 获取某个 session 下已排序的 todo 列表。 */
  function todosOf(sessionId: string): Todo[] {
    return todosBySession.value[sessionId] || []
  }

  /** 直接覆盖某个 session 的 todo 列表（一般由 WebSocket 事件触发）。 */
  function setTodos(sessionId: string, todos: Todo[]) {
    if (!sessionId) return
    todosBySession.value[sessionId] = sortTodos(todos)
  }

  /**
   * 从后端 GET /api/todos?session_id=xxx 加载 TODO 列表。
   * 默认过滤掉终态（done/cancelled），展示活跃 TODO。
   */
  async function loadTodos(sessionId: string): Promise<void> {
    if (!sessionId || loadingSessions.has(sessionId)) return
    loadingSessions.add(sessionId)
    try {
      const resp = await fetch(`/api/todos?session_id=${encodeURIComponent(sessionId)}`)
      if (!resp.ok) {
        const text = await resp.text()
        throw new Error(`Failed to load todos: ${resp.status} ${text}`)
      }
      const data = (await resp.json()) as { todos: Todo[] }
      setTodos(sessionId, data.todos || [])
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Failed to load todos')
      throw err
    } finally {
      loadingSessions.delete(sessionId)
    }
  }

  /** 创建 TODO。 */
  async function createTodo(
    sessionId: string,
    title: string,
    options: {
      description?: string
      taskId?: string
      parentTodoId?: string
      priority?: TodoPriority
    } = {}
  ): Promise<Todo> {
    const body = {
      session_id: sessionId,
      title: title.trim(),
      description: options.description || '',
      task_id: options.taskId || '',
      parent_todo_id: options.parentTodoId || '',
      priority: options.priority ?? 1,
    }
    const resp = await fetch('/api/todos', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to create todo: ${resp.status} ${text}`)
    }
    const todo = (await resp.json()) as Todo
    // 乐观更新：把新 todo 插入列表，随后 WebSocket 事件会再次同步权威状态
    prependTodo(sessionId, todo)
    return todo
  }

  /** 更新 TODO 的非 status 字段。 */
  async function updateTodo(
    sessionId: string,
    todoId: string,
    updates: {
      title?: string
      description?: string
      priority?: TodoPriority
      sortOrder?: number
      parentTodoId?: string
    }
  ): Promise<Todo> {
    const body: Record<string, unknown> = {}
    if (updates.title !== undefined) body.title = updates.title.trim()
    if (updates.description !== undefined) body.description = updates.description
    if (updates.priority !== undefined) body.priority = updates.priority
    if (updates.sortOrder !== undefined) body.sort_order = updates.sortOrder
    if (updates.parentTodoId !== undefined) body.parent_todo_id = updates.parentTodoId

    const resp = await fetch(`/api/todos/${encodeURIComponent(todoId)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to update todo: ${resp.status} ${text}`)
    }
    const todo = (await resp.json()) as Todo
    replaceTodo(sessionId, todo)
    return todo
  }

  /** 独立更新 TODO 状态。 */
  async function updateTodoStatus(
    sessionId: string,
    todoId: string,
    status: TodoStatus
  ): Promise<Todo> {
    const resp = await fetch(`/api/todos/${encodeURIComponent(todoId)}/status`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ status }),
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to update todo status: ${resp.status} ${text}`)
    }
    const todo = (await resp.json()) as Todo
    replaceTodo(sessionId, todo)
    return todo
  }

  /** 删除 TODO。 */
  async function deleteTodo(sessionId: string, todoId: string): Promise<void> {
    const resp = await fetch(`/api/todos/${encodeURIComponent(todoId)}`, {
      method: 'DELETE',
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to delete todo: ${resp.status} ${text}`)
    }
    removeTodo(sessionId, todoId)
  }

  /** 批量清理已完成（或全部）TODO。 */
  async function clearCompleted(sessionId: string, onlyCompleted = true): Promise<void> {
    const resp = await fetch('/api/todos/clear', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ session_id: sessionId, only_completed: onlyCompleted }),
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to clear todos: ${resp.status} ${text}`)
    }
    // WebSocket 事件会负责最终状态同步
  }

  /** 替换本地列表中的某条 todo。 */
  function replaceTodo(sessionId: string, todo: Todo) {
    const list = todosBySession.value[sessionId] || []
    const idx = list.findIndex(t => t.id === todo.id)
    if (idx >= 0) {
      const next = [...list]
      next[idx] = todo
      todosBySession.value[sessionId] = sortTodos(next)
    } else {
      prependTodo(sessionId, todo)
    }
  }

  /** 将新 todo 插入列表头部。 */
  function prependTodo(sessionId: string, todo: Todo) {
    const list = todosBySession.value[sessionId] || []
    todosBySession.value[sessionId] = sortTodos([todo, ...list])
  }

  /** 从本地列表移除某条 todo。 */
  function removeTodo(sessionId: string, todoId: string) {
    const list = todosBySession.value[sessionId]
    if (!list) return
    todosBySession.value[sessionId] = list.filter(t => t.id !== todoId)
  }

  /** 排序：未完成的优先，其次按优先级降序，再按 created_at 升序。 */
  function sortTodos(todos: Todo[]): Todo[] {
    const terminal: TodoStatus[] = ['done', 'cancelled']
    return [...todos].sort((a, b) => {
      const aDone = terminal.includes(a.status) ? 1 : 0
      const bDone = terminal.includes(b.status) ? 1 : 0
      if (aDone !== bDone) return aDone - bDone
      if (b.priority !== a.priority) return b.priority - a.priority
      return a.created_at - b.created_at
    })
  }

  return {
    todosBySession,
    todosOf,
    setTodos,
    loadTodos,
    createTodo,
    updateTodo,
    updateTodoStatus,
    deleteTodo,
    clearCompleted,
  }
}
