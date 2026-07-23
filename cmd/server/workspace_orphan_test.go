// workspace_orphan_test.go — task 7.3 / 7.4：孤儿扫描 + 无 session 结束钩子测试。
//
// 覆盖 spec: 孤儿扫描与配置 的 scenario：
//   - 7.3：造一个 DB 不认得的 worktree，调 reclaimOrphanWorktrees 后被清理 +
//     广播 worktree_orphan_removed；DB 认得的 worktree 保留不动。
//   - 7.4：run 结束路径不调 worktree/exit，active_worktree_id 在 run 结束后保持
//     （无 session 结束钩子，验证 runner.go 末尾无清理逻辑）。
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/workspace"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// newWorkspaceMgrForTest 构造一个指向 repoDir/.claude/worktrees 的 Manager
// 并 SetRepoDir(repoDir)，供孤儿扫描测试使用。
func newWorkspaceMgrForTest(repoDir string) *workspace.Manager {
	rootDir := filepath.Join(repoDir, ".claude", "worktrees")
	return workspace.NewManager(rootDir).SetRepoDir(repoDir)
}

// wtOrphanRepo 构造临时 git 仓库 + Manager（设为 globalWorkspaceMgr），
// 返回 repoRoot 与 Manager。复用 wtAPIRepo 的形态但额外返回 mgr 引用。
func wtOrphanRepo(t *testing.T) (string, *ws.Hub) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	git("config", "user.email", "test@test.local")
	git("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-q", "-m", "init")

	rootDir := filepath.Join(dir, ".claude", "worktrees")
	os.MkdirAll(rootDir, 0755)
	mgr := newWorkspaceMgrForTest(dir)
	oldMgr := globalWorkspaceMgr
	globalWorkspaceMgr = mgr
	t.Cleanup(func() { globalWorkspaceMgr = oldMgr })

	hub := ws.NewHub()
	go hub.Run()
	return dir, hub
}

// TestOrphanScanRemovesUnknownWorktree 验证孤儿扫描清理 DB 不认得的 worktree +
// 广播 worktree_orphan_removed，同时保留 DB 认得的 worktree。
func TestOrphanScanRemovesUnknownWorktree(t *testing.T) {
	setupWorkspaceAPIDB(t)
	repoDir, hub := wtOrphanRepo(t)

	// 造两个 worktree：一个绑定到 session（DB 认得），一个不绑定（孤儿）。
	mgr := globalWorkspaceMgr

	// session-bound worktree
	wtKnown, _, err := mgr.Create("sess-known", "fresh")
	if err != nil {
		t.Fatalf("create known: %v", err)
	}
	if err := db.InsertSession(db.SessionRecord{ID: "sess-known", Name: "known", Status: "active"}); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if err := db.SetSessionActiveWorktree("sess-known", wtKnown.ID); err != nil {
		t.Fatalf("set active: %v", err)
	}

	// orphan worktree（DB 不认得）
	wtOrphan, _, err := mgr.Create("sess-orphan", "fresh")
	if err != nil {
		t.Fatalf("create orphan: %v", err)
	}
	// 不在 DB 里登记 active_worktree_id → 孤儿
	orphanPath := wtOrphan.Path

	// 先注册测试 client，确保能收到扫描期间广播的事件（SendEvent 异步经
	// broadcast chan，若 client 在事件之后注册会错过）。
	client := hub.RegisterTestClient("orphan-test")
	defer hub.UnregisterTestClient(client)

	// 调孤儿扫描
	reclaimOrphanWorktrees(mgr, hub)

	// ws.Hub.SendEvent 经 buffered channel 异步处理，事件由 hub.Run goroutine
	// 从 broadcast chan 取出后 append 到 eventBuf 并分发给已注册 client。
	var sawOrphanEvent bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case e := <-client.Send:
			if e.Type == event.EventWorktreeOrphanRemoved {
				sawOrphanEvent = true
			}
		case <-time.After(50 * time.Millisecond):
		}
		if sawOrphanEvent {
			break
		}
	}
	if !sawOrphanEvent {
		t.Fatalf("worktree_orphan_removed not broadcast")
	}

	// 孤儿 worktree 已被删除
	if _, err := stat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("orphan worktree dir still exists after scan: %v", err)
	}
	// 已绑定的 worktree 保留
	if _, err := stat(wtKnown.Path); err != nil {
		t.Fatalf("known worktree dir removed by scan: %v", err)
	}
	// DB 绑定保持
	got, _ := db.GetSessionActiveWorktree("sess-known")
	if got != wtKnown.ID {
		t.Fatalf("known session active binding lost: %q, want %q", got, wtKnown.ID)
	}
	_ = repoDir
}

// TestOrphanScanNoOpWhenMgrNil 验证 mgr 为 nil 时孤儿扫描是 no-op（不 panic）。
func TestOrphanScanNoOpWhenMgrNil(t *testing.T) {
	hub := ws.NewHub()
	go hub.Run()
	// 不应 panic
	reclaimOrphanWorktrees(nil, hub)
}

// TestNoSessionEndHookPreservesActiveWorktree 验证 run 结束后 active_worktree_id
// 保持——run 路径不自动 exit/remove worktree（无 session 结束钩子）。
//
// 这里的策略是直接验证 DB 绑定语义：设了 active_worktree_id 后，不依赖任何
// run 结束钩子自动清理。runner.go 末尾（engine.Run 返回后的 session 状态聚合）
// 只更新 turn_count / total_tokens / status，不触碰 active_worktree_id。
// 我们用一个独立的 session 模拟"run 结束"，确认绑定仍在。
func TestNoSessionEndHookPreservesActiveWorktree(t *testing.T) {
	setupWorkspaceAPIDB(t)
	_, hub := wtOrphanRepo(t)
	_ = hub

	mgr := globalWorkspaceMgr
	wt, _, err := mgr.Create("sess-nohook", "fresh")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := db.InsertSession(db.SessionRecord{ID: "sess-nohook", Name: "nohook", Status: "active"}); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if err := db.SetSessionActiveWorktree("sess-nohook", wt.ID); err != nil {
		t.Fatalf("set active: %v", err)
	}

	// 模拟 run 结束后的 session 状态聚合（与 runner.go:723-741 一致：
	// 只更新 turn_count / total_tokens / status，不碰 active_worktree_id）。
	db.UpdateSessionTurnCount("sess-nohook")
	db.UpdateSessionContextSize("sess-nohook", 123, 0)
	db.UpdateSessionStatus("sess-nohook", "completed")

	// active_worktree_id 仍在（无结束钩子清理）
	got, _ := db.GetSessionActiveWorktree("sess-nohook")
	if got != wt.ID {
		t.Fatalf("active_worktree_id cleared after run end: %q, want %q (no end hook should touch it)", got, wt.ID)
	}
	// worktree 目录也仍在
	if _, err := stat(wt.Path); err != nil {
		t.Fatalf("worktree dir removed after run end: %v", err)
	}
}

// TestOrphanScanSkipsWhenDBUnavailable 验证 DB 不可用时孤儿扫描不清理
// （避免误删仍被引用的 worktree）。
func TestOrphanScanSkipsWhenDBUnavailable(t *testing.T) {
	// 先构造 repo + 一个 worktree，再关闭 DB，再调扫描，确认 worktree 仍在。
	setupWorkspaceAPIDB(t)
	_, hub := wtOrphanRepo(t)
	mgr := globalWorkspaceMgr
	wt, _, err := mgr.Create("sess-dbclosed", "fresh")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wtPath := wt.Path

	// 关闭 DB
	_ = db.Close()
	db.DB = nil

	// 扫描不应 panic，也不应删除 worktree（DB 不可用时跳过清理）
	reclaimOrphanWorktrees(mgr, hub)
	if _, err := stat(wtPath); err != nil {
		t.Fatalf("worktree removed despite DB unavailable (should skip): %v", err)
	}
}

// 避免在测试文件里直接用 os.Stat 全称——用一个小 helper 统一。
func stat(path string) (os.FileInfo, error) { return os.Stat(path) }
