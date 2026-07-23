// workspace.go — Workspace worktree 子系统的 SQLite 持久化层。
//
// 本文件做两件事：
//   1. 在 init() 中注册 v28 migration，为 sessions 表新增 active_worktree_id
//      列，记录 session 级 active worktree 状态机绑定（一个 session 任一时刻
//      最多一个 active worktree）。沿用 cron.go / skill.go 的 init() 注册模式，
//      使 database.go 的 createTables 无需为旧库额外改动（新库已在建表 SQL 内
//      带上该列，旧库经本 migration 补列；RunMigrations 对 duplicate column
//      静默跳过）。
//   2. 提供 session↔worktree 绑定的读写 helper：GetSessionActiveWorktree /
//      SetSessionActiveWorktree / ClearSessionActiveWorktree。
//
// active_worktree_id 为 NULL 表示该 session 当前无 active worktree（沿用普通
// 目录 workspace）；非空则指向 internal/workspace.Manager 管理的某个 worktree ID。
// 该列只存指针，worktree 的物理生命周期由 Manager 经 git worktree 原语管理，
// 不在 DB 侧级联——启动孤儿扫描负责清理 DB 不认得的 worktree。
package db

import (
	"database/sql"
	"fmt"
)

// init 注册 v28 migration：为 sessions 表新增 active_worktree_id 列。
//
// 新库由 database.go createTables() 直接建带该列的表；旧库经 ALTER 补列。
// SQLite 没有 ALTER ADD COLUMN IF NOT EXISTS，重复执行返回 "duplicate column
// name"，RunMigrations 已对这类错误静默跳过，故幂等安全。
func init() {
	migrations = append(migrations, Migration{
		Version:     28,
		Description: "Add active_worktree_id column to sessions table for worktree state machine",
		SQL:         `ALTER TABLE sessions ADD COLUMN active_worktree_id TEXT DEFAULT NULL`,
	})
}

// GetSessionActiveWorktree 返回 session 当前 active worktree 的 ID。
// 返回 ("", nil) 表示该 session 无 active worktree（NULL 或空串）。
// session 不存在时返回错误。
func GetSessionActiveWorktree(sessionID string) (string, error) {
	if DB == nil {
		return "", fmt.Errorf("db not initialized")
	}
	var wtID sql.NullString
	err := DB.QueryRow(
		`SELECT active_worktree_id FROM sessions WHERE id=?`, sessionID,
	).Scan(&wtID)
	if err != nil {
		return "", err
	}
	if !wtID.Valid {
		return "", nil
	}
	return wtID.String, nil
}

// SetSessionActiveWorktree 把 session 的 active worktree 绑定设为 worktreeID。
// 调用方负责保证此时 session 之前无 active worktree（状态机：一个 session 任一
// 时刻最多一个 active worktree）；本函数不做重入校验，由上层 service 统一判定。
func SetSessionActiveWorktree(sessionID, worktreeID string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`UPDATE sessions SET active_worktree_id=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		worktreeID, sessionID,
	)
	return err
}

// ClearSessionActiveWorktree 清空 session 的 active worktree 绑定（置 NULL）。
// 用于 worktree/exit（keep 或 remove 成功后）恢复 session 到普通目录 workspace。
// session 不存在或本就无 active worktree 时无副作用。
func ClearSessionActiveWorktree(sessionID string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`UPDATE sessions SET active_worktree_id=NULL, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		sessionID,
	)
	return err
}

// ListSessionActiveWorktrees 返回当前所有 active worktree 绑定的 (sessionID,
// worktreeID) 对，仅供启动孤儿扫描使用：对比 Manager.List() 与本表，清理 DB
// 不认得的 worktree。只返回 active_worktree_id 非空的行。
func ListSessionActiveWorktrees() (map[string]string, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, active_worktree_id FROM sessions WHERE active_worktree_id IS NOT NULL AND active_worktree_id <> ''`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var sessionID, wtID string
		if err := rows.Scan(&sessionID, &wtID); err != nil {
			return nil, err
		}
		result[wtID] = sessionID
	}
	return result, rows.Err()
}
