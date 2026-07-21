// todo.go — todos 表的迁移与 CRUD 操作。
//
// 本文件为待办事项 (Todo) 子系统提供 SQLite 持久化层：
//   1. 通过 init() 向 migrations 切片注册 v23 迁移，创建 todos 表。
//   2. 提供 InsertTodo、UpdateTodo、DeleteTodo、GetTodo、ListTodosBySession 等 CRUD 函数。
//   3. 负责在写入时自动维护 created_at / updated_at / completed_at 时间戳。
//
// 所有时间戳以 Unix 秒整数存储，与 Todo 内部类型保持一致。
package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/todo"
	"github.com/google/uuid"
)

// init 注册 todos 表的 schema 迁移（v23）。
// 使用 init() 注册可让 database.go 无需显式修改即可在 Init 时自动跑迁移。
func init() {
	migrations = append(migrations, Migration{
		Version:     23,
		Description: "Create todos table for todo subsystem",
		SQL: `CREATE TABLE IF NOT EXISTS todos (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			created_by_task_id TEXT NOT NULL,
			active_task_id TEXT,
			parent_todo_id TEXT,
			title TEXT NOT NULL,
			description TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			priority INTEGER DEFAULT 0,
			sort_order INTEGER DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			completed_at INTEGER
		);
		CREATE INDEX IF NOT EXISTS idx_todos_session_id ON todos(session_id);
		CREATE INDEX IF NOT EXISTS idx_todos_status ON todos(status);
		CREATE INDEX IF NOT EXISTS idx_todos_created_by_task_id ON todos(created_by_task_id);
		CREATE INDEX IF NOT EXISTS idx_todos_active_task_id ON todos(active_task_id);
		CREATE INDEX IF NOT EXISTS idx_todos_parent_todo_id ON todos(parent_todo_id);
		CREATE INDEX IF NOT EXISTS idx_todos_priority_sort_order_created_at ON todos(priority DESC, sort_order ASC, created_at ASC);`,
	})
}

// InsertTodo 把 Todo 写入数据库。
// 若 t.ID 为空则自动生成 UUID；created_at / updated_at / completed_at 会自动维护。
func InsertTodo(t todo.Todo) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	now := time.Now().Unix()
	if t.CreatedAt == 0 {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	if t.Status == todo.StatusDone {
		t.CompletedAt = &now
	} else {
		t.CompletedAt = nil
	}
	_, err := DB.Exec(`INSERT INTO todos (
		id, session_id, created_by_task_id, active_task_id, parent_todo_id,
		title, description, status, priority, sort_order,
		created_at, updated_at, completed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.SessionID, t.CreatedByTaskID, stringOrNil(t.ActiveTaskID), stringOrNil(t.ParentTodoID),
		t.Title, t.Description, string(t.Status), t.Priority, t.SortOrder,
		t.CreatedAt, t.UpdatedAt, int64OrNil(t.CompletedAt),
	)
	return err
}

// UpdateTodo 用给定 Todo 对象覆盖数据库中的对应记录。
// 自动刷新 updated_at 与 completed_at。
func UpdateTodo(t todo.Todo) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now().Unix()
	t.UpdatedAt = now
	if t.Status == todo.StatusDone {
		t.CompletedAt = &now
	} else {
		t.CompletedAt = nil
	}
	_, err := DB.Exec(`UPDATE todos SET
		session_id = ?, created_by_task_id = ?, active_task_id = ?, parent_todo_id = ?,
		title = ?, description = ?, status = ?, priority = ?, sort_order = ?,
		updated_at = ?, completed_at = ?
	WHERE id = ?`,
		t.SessionID, t.CreatedByTaskID, stringOrNil(t.ActiveTaskID), stringOrNil(t.ParentTodoID),
		t.Title, t.Description, string(t.Status), t.Priority, t.SortOrder,
		t.UpdatedAt, int64OrNil(t.CompletedAt),
		t.ID,
	)
	return err
}

// DeleteTodo 按 id 删除单个 Todo。
func DeleteTodo(id string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`DELETE FROM todos WHERE id = ?`, id)
	return err
}

// GetTodo 按 id 读取单个 Todo。
// 返回 sql.ErrNoRows 表示未找到。
func GetTodo(id string) (todo.Todo, error) {
	if DB == nil {
		return todo.Todo{}, fmt.Errorf("db not initialized")
	}
	row := DB.QueryRow(`SELECT id, session_id, created_by_task_id, active_task_id, parent_todo_id,
		title, description, status, priority, sort_order,
		created_at, updated_at, completed_at
	 FROM todos WHERE id = ?`, id)
	return scanTodo(row)
}

// ListTodosBySession 按 session 列出 Todo。
// statusFilter 非空时仅返回指定状态；includeDone 为 false 时过滤掉终态（done 和 cancelled）。
// 结果默认按 priority DESC, sort_order ASC, created_at ASC 排序。
func ListTodosBySession(sessionID string, statusFilter []todo.TodoStatus, includeDone bool) ([]todo.Todo, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	query := `SELECT id, session_id, created_by_task_id, active_task_id, parent_todo_id,
		title, description, status, priority, sort_order,
		created_at, updated_at, completed_at
	 FROM todos WHERE session_id = ?`
	args := []any{sessionID}

	var conditions []string
	if len(statusFilter) > 0 {
		placeholders := make([]string, len(statusFilter))
		for i, s := range statusFilter {
			placeholders[i] = "?"
			args = append(args, string(s))
		}
		conditions = append(conditions, fmt.Sprintf("status IN (%s)", strings.Join(placeholders, ", ")))
	}
	if !includeDone {
		conditions = append(conditions, "status NOT IN ('done', 'cancelled')")
	}
	if len(conditions) > 0 {
		query += " AND " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY priority DESC, sort_order ASC, created_at ASC"

	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []todo.Todo
	for rows.Next() {
		t, err := scanTodo(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

// ListTodosByTask 返回由指定 task 创建的所有 Todo。
func ListTodosByTask(taskID string) ([]todo.Todo, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(`SELECT id, session_id, created_by_task_id, active_task_id, parent_todo_id,
		title, description, status, priority, sort_order,
		created_at, updated_at, completed_at
	 FROM todos WHERE created_by_task_id = ?
	 ORDER BY priority DESC, sort_order ASC, created_at ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []todo.Todo
	for rows.Next() {
		t, err := scanTodo(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

// Reorder 批量更新一组 todo 的 parent_todo_id 与 sort_order。
// 在同一个事务中执行：1) 读取当前记录校验 session；2) 逐一更新。
func Reorder(sessionID string, moves []todo.TodoMove) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for i, m := range moves {
		if m.ID == "" {
			return fmt.Errorf("move[%d]: id is required", i)
		}
		var existingSession string
		row := tx.QueryRow(`SELECT session_id FROM todos WHERE id = ?`, m.ID)
		if err := row.Scan(&existingSession); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("todo %s not found", m.ID)
			}
			return err
		}
		if existingSession != sessionID {
			return fmt.Errorf("todo %s does not belong to session %s", m.ID, sessionID)
		}
		parentID := sql.NullString{}
		if m.ParentTodoID != "" {
			parentID = sql.NullString{String: m.ParentTodoID, Valid: true}
		}
		if _, err := tx.Exec(`UPDATE todos SET parent_todo_id = ?, sort_order = ?, updated_at = ? WHERE id = ?`,
			parentID, m.SortOrder, time.Now().Unix(), m.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func DeleteCompletedTodosBySession(sessionID string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`DELETE FROM todos WHERE session_id = ? AND status = 'done'`, sessionID)
	return err
}

// DeleteAllTodosBySession 删除某 session 下的所有 Todo（包括未完成）。
func DeleteAllTodosBySession(sessionID string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`DELETE FROM todos WHERE session_id = ?`, sessionID)
	return err
}

// todoScanner 约束 scanTodo 可接受的行类型（*sql.Row 或 *sql.Rows）。
type todoScanner interface {
	Scan(dest ...any) error
}

// scanTodo 将一行 todos 表数据解析为 todo.Todo。
func scanTodo(scanner todoScanner) (todo.Todo, error) {
	var t todo.Todo
	var statusStr string
	var activeTaskID, parentTodoID sql.NullString
	var completedAt sql.NullInt64
	if err := scanner.Scan(
		&t.ID, &t.SessionID, &t.CreatedByTaskID, &activeTaskID, &parentTodoID,
		&t.Title, &t.Description, &statusStr, &t.Priority, &t.SortOrder,
		&t.CreatedAt, &t.UpdatedAt, &completedAt,
	); err != nil {
		return todo.Todo{}, err
	}
	t.Status = todo.TodoStatus(statusStr)
	if activeTaskID.Valid {
		t.ActiveTaskID = activeTaskID.String
	}
	if parentTodoID.Valid {
		t.ParentTodoID = parentTodoID.String
	}
	if completedAt.Valid {
		v := completedAt.Int64
		t.CompletedAt = &v
	}
	return t, nil
}

// stringOrNil 把空字符串转为 nil，使 SQLite 列保持 NULL。
func stringOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// int64OrNil 把 nil 指针转为 nil，使 SQLite 列保持 NULL。
func int64OrNil(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
