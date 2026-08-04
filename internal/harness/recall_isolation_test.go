package harness

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ayanmw/multi-agent-platform/pkg/db"
)

// TestBuildWorkingMemorySessionIsolation 验证 E3 隔离增强（N3-02）：
// Memory recall 的 session 级 memory 严格按 session_id 过滤，绝不会把
// 其它 session 的私有 memory 召回进当前 session 的 Working Memory，
// 确保无跨 session 数据泄漏。
//
// 结构保证：recall.loadSessionMemories 经
// db.QueryMemoriesByScopeAndSession(projectID, sessionID, "session")
// 以 sessionID 为 WHERE 参数查询，跨 session 召回在 SQL 层即被阻断。
func TestBuildWorkingMemorySessionIsolation(t *testing.T) {
	// 用临时 SQLite 初始化全局 db.DB（本包无其它测试，互不干扰）。
	dbPath := filepath.Join(t.TempDir(), "mem-isolation.db")
	if err := db.Init(dbPath); err != nil {
		t.Fatalf("db.Init: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now()
	// session A 的私有 memory。
	if err := db.InsertMemory(db.MemoryRecord{
		ID: "m-A", ProjectID: "default", Scope: "session", SessionID: "sess-A",
		Type: "fact", Tier: "consolidated", Content: "secret-of-session-A",
		Status: "active", Confidence: 1.0, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert sess-A memory: %v", err)
	}
	// session B 的私有 memory —— 绝不应出现在 sess-A 的召回中。
	if err := db.InsertMemory(db.MemoryRecord{
		ID: "m-B", ProjectID: "default", Scope: "session", SessionID: "sess-B",
		Type: "fact", Tier: "consolidated", Content: "secret-of-session-B",
		Status: "active", Confidence: 1.0, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert sess-B memory: %v", err)
	}

	mr := NewMemoryRecall(&SqliteMemoryDB{})
	wm, err := mr.BuildWorkingMemory("default", "sess-A", "some goal", 3)
	if err != nil {
		t.Fatalf("BuildWorkingMemory: %v", err)
	}

	// 断言：sess-B 的私有内容绝未泄漏进 sess-A 的会话 memory。
	for _, m := range wm.SessionMemories {
		if m.Content == "secret-of-session-B" {
			t.Fatalf("cross-session memory leak: sess-B content surfaced for sess-A")
		}
	}
	// 断言：sess-A 的私有 memory 恰好召回 1 条且内容正确。
	if len(wm.SessionMemories) != 1 {
		t.Fatalf("expected exactly 1 session memory for sess-A, got %d: %+v",
			len(wm.SessionMemories), wm.SessionMemories)
	}
	if wm.SessionMemories[0].Content != "secret-of-session-A" {
		t.Fatalf("session memory content mismatch: got %q", wm.SessionMemories[0].Content)
	}
}
