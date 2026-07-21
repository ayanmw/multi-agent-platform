// model.go — Todo 领域模型与常量。
//
// 本包为简化依赖而设计：internal/todo 只定义类型和行为契约，
// 不引用 pkg/db 或任何其它内部包。具体持久化实现放在 pkg/db/todo.go，
// 两者通过导出类型组合，以打破 tool -> todo -> db -> skill -> tool 的 import cycle。
package todo

// TodoStatus 表示一项待办事项的当前生命周期状态。
type TodoStatus string

const (
	// StatusPending 表示待办已创建但尚未开始执行。
	StatusPending TodoStatus = "pending"
	// StatusInProgress 表示待办正在处理中。
	StatusInProgress TodoStatus = "in_progress"
	// StatusDone 表示待办已完成。
	StatusDone TodoStatus = "done"
	// StatusCancelled 表示待办已取消。
	StatusCancelled TodoStatus = "cancelled"
)

// Todo 表示一个可被 Agent 创建、更新和完成的待办事项。
//
// 它既可以关联到 Session（多轮对话维度），也可以关联到某个具体任务树
// （created_by_task_id / active_task_id）以及父待办（parent_todo_id），
// 从而支持子任务分解与层级展示。
type Todo struct {
	ID              string     `json:"id"`
	SessionID       string     `json:"session_id"`
	CreatedByTaskID string     `json:"created_by_task_id"`
	ActiveTaskID    string     `json:"active_task_id"`
	ParentTodoID    string     `json:"parent_todo_id"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	Status          TodoStatus `json:"status"`
	Priority        int        `json:"priority"`
	SortOrder       int        `json:"sort_order"`
	CreatedAt       int64      `json:"created_at"`
	UpdatedAt       int64      `json:"updated_at"`
	CompletedAt     *int64     `json:"completed_at,omitempty"`
	// Children 是树形序列化时的派生子任务，数据库不直接存储该字段。
	Children []Todo `json:"children,omitempty"`
}

// TodoMove 表示一次拖拽排序/层级调整操作。
// 用于批量更新 todo 的 parent_todo_id 与 sort_order。
type TodoMove struct {
	ID           string `json:"id"`
	ParentTodoID string `json:"parent_todo_id"`
	SortOrder    int    `json:"sort_order"`
}

// IsTerminal 返回该状态是否为终态（done 或 cancelled）。
func (s TodoStatus) IsTerminal() bool {
	return s == StatusDone || s == StatusCancelled
}

// BuildTree 将扁平 Todo 列表构建为按 parent_todo_id 分组的树形结构。
// TopLevel 包含所有 parent_todo_id 为空或者不存在的 todo；Children 按给定的顺序展开。
func BuildTree(todos []Todo) []Todo {
	byParent := make(map[string][]Todo)
	for _, t := range todos {
		byParent[t.ParentTodoID] = append(byParent[t.ParentTodoID], t)
	}
	var walk func(parentID string) []Todo
	walk = func(parentID string) []Todo {
		children := byParent[parentID]
		for i := range children {
			children[i].Children = walk(children[i].ID)
		}
		return children
	}
	return walk("")
}
