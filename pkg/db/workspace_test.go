// workspace_test.go — sessions.active_worktree_id 列迁移与 worktree 绑定读写 helper 的测试。
package db

import (
	"testing"
)

// TestActiveWorktreeColumnMigrated 验证 v28 migration 为 sessions 表补了 active_worktree_id 列。
func TestActiveWorktreeColumnMigrated(t *testing.T) {
	freshDB(t)
	// 用 PRAGMA table_info 直接查列定义，避免空表 LIMIT 1 返回 ErrNoRows 干扰判定。
	rows, err := DB.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == "active_worktree_id" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("active_worktree_id column not found in sessions table")
	}
}

// TestActiveWorktreeColumnDefaultsNull 验证新建 session 时 active_worktree_id 默认 NULL（向后兼容）。
func TestActiveWorktreeColumnDefaultsNull(t *testing.T) {
	freshDB(t)
	if err := InsertSession(newSessionRecord("sess-wt-1", "WT Test")); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	got, err := GetSessionActiveWorktree("sess-wt-1")
	if err != nil {
		t.Fatalf("GetSessionActiveWorktree: %v", err)
	}
	if got != "" {
		t.Fatalf("new session active_worktree_id = %q, want empty (NULL default)", got)
	}
}

// TestSetClearActiveWorktree 验证 Set / Get / Clear 往返。
func TestSetClearActiveWorktree(t *testing.T) {
	freshDB(t)
	if err := InsertSession(newSessionRecord("sess-wt-2", "WT Test")); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	// Set
	if err := SetSessionActiveWorktree("sess-wt-2", "abc12345"); err != nil {
		t.Fatalf("SetSessionActiveWorktree: %v", err)
	}
	got, err := GetSessionActiveWorktree("sess-wt-2")
	if err != nil {
		t.Fatalf("Get after Set: %v", err)
	}
	if got != "abc12345" {
		t.Fatalf("got = %q, want abc12345", got)
	}
	// Clear
	if err := ClearSessionActiveWorktree("sess-wt-2"); err != nil {
		t.Fatalf("ClearSessionActiveWorktree: %v", err)
	}
	got, err = GetSessionActiveWorktree("sess-wt-2")
	if err != nil {
		t.Fatalf("Get after Clear: %v", err)
	}
	if got != "" {
		t.Fatalf("got after clear = %q, want empty", got)
	}
}

// TestListSessionActiveWorktrees 验证启动孤儿扫描用的全表列举。
func TestListSessionActiveWorktrees(t *testing.T) {
	freshDB(t)
	for _, id := range []string{"sess-a", "sess-b", "sess-c"} {
		if err := InsertSession(newSessionRecord(id, id)); err != nil {
			t.Fatalf("InsertSession %s: %v", id, err)
		}
	}
	// a 与 c 各有一个 active worktree，b 无。
	if err := SetSessionActiveWorktree("sess-a", "wt-a-0001"); err != nil {
		t.Fatal(err)
	}
	if err := SetSessionActiveWorktree("sess-c", "wt-c-0002"); err != nil {
		t.Fatal(err)
	}
	got, err := ListSessionActiveWorktrees()
	if err != nil {
		t.Fatalf("ListSessionActiveWorktrees: %v", err)
	}
	if got["wt-a-0001"] != "sess-a" {
		t.Fatalf("want wt-a-0001 -> sess-a, got %v", got)
	}
	if got["wt-c-0002"] != "sess-c" {
		t.Fatalf("want wt-c-0002 -> sess-c, got %v", got)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 active bindings, got %d (%v)", len(got), got)
	}
}
