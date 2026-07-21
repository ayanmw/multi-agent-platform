// store.go — Todo Store 接口与实现。
//
// 本包通过 DBStore 接口把底层 CRUD 抽象出来，默认由 pkg/db 的同名函数实现。
// 这样做有两个目的：
//   1. internal/todo 不直接 import pkg/db，从而打破
//      tool -> todo -> db -> skill -> tool 的 import cycle。
//   2. 单元测试可以注入 mock DBStore，无需启动真实 SQLite。
package todo

import (
	"database/sql"
)

// DBStore 声明 Todo 持久化所需的全部 CRUD 操作。
// 默认由 pkg/db 中的 InsertTodo/UpdateTodo/... 函数实现。
type DBStore interface {
	InsertTodo(t Todo) error
	UpdateTodo(t Todo) error
	DeleteTodo(id string) error
	GetTodo(id string) (Todo, error)
	ListTodosBySession(sessionID string, statusFilter []TodoStatus, includeDone bool) ([]Todo, error)
	ListTodosByTask(taskID string) ([]Todo, error)
	DeleteCompletedTodosBySession(sessionID string) error
	DeleteAllTodosBySession(sessionID string) error
}

// Store 是 DBStore 之上的一层薄封装，提供与业务语义更贴近的方法。
type Store struct {
	db DBStore
}

// NewStore 创建基于给定 DBStore 的 Store。
func NewStore(db DBStore) *Store {
	return &Store{db: db}
}

// Create 创建新的 Todo 记录。
func (s *Store) Create(t Todo) error {
	return s.db.InsertTodo(t)
}

// Update 更新 Todo 记录。
func (s *Store) Update(t Todo) error {
	return s.db.UpdateTodo(t)
}

// Delete 删除指定 Todo。
func (s *Store) Delete(id string) error {
	return s.db.DeleteTodo(id)
}

// Get 读取单个 Todo；找不到时返回 sql.ErrNoRows。
func (s *Store) Get(id string) (Todo, error) {
	return s.db.GetTodo(id)
}

// ListBySession 列出某 session 的 Todo。
func (s *Store) ListBySession(sessionID string, statusFilter []TodoStatus, includeDone bool) ([]Todo, error) {
	return s.db.ListTodosBySession(sessionID, statusFilter, includeDone)
}

// ListByTask 列出由某 task 创建的 Todo。
func (s *Store) ListByTask(taskID string) ([]Todo, error) {
	return s.db.ListTodosByTask(taskID)
}

// DeleteCompletedBySession 删除某 session 下已完成的 Todo。
func (s *Store) DeleteCompletedBySession(sessionID string) error {
	return s.db.DeleteCompletedTodosBySession(sessionID)
}

// DeleteAllBySession 删除某 session 下全部 Todo。
func (s *Store) DeleteAllBySession(sessionID string) error {
	return s.db.DeleteAllTodosBySession(sessionID)
}

// Ensure sql.ErrNoRows 在 Store 层仍可被外部比较（不引入外部包）。
var _ = sql.ErrNoRows
