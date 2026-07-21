// TODO 子系统类型定义 — 与后端 internal/todo 结构对齐

/** TODO 状态 */
export type TodoStatus = 'pending' | 'in_progress' | 'done' | 'cancelled'

/** TODO 优先级 */
export type TodoPriority = 1 | 2 | 3

/** Session 级 TODO 项 */
export interface Todo {
  id: string
  session_id: string
  created_by_task_id: string
  active_task_id: string
  parent_todo_id: string
  title: string
  description: string
  status: TodoStatus
  priority: TodoPriority
  sort_order: number
  created_at: number
  updated_at: number
  completed_at?: number | null
  /** 树形序列化时后端填充的子任务 */
  children?: Todo[]
}

/** todo_list_changed 事件 data 结构 */
export interface TodoListChangedData {
  session_id: string
  todos: Todo[]
}

/** 创建 TODO 请求 */
export interface CreateTodoRequest {
  session_id: string
  title: string
  description?: string
  task_id?: string
  parent_todo_id?: string
  priority?: TodoPriority
}

/** 更新 TODO 请求 */
export interface UpdateTodoRequest {
  title?: string
  description?: string
  priority?: TodoPriority
  sort_order?: number
  parent_todo_id?: string
}

/** 批量排序/层级调整请求 */
export interface ReorderTodosRequest {
  session_id: string
  moves: Array<{
    id: string
    parent_todo_id: string
    sort_order: number
  }>
}
export interface UpdateTodoStatusRequest {
  status: TodoStatus
}
