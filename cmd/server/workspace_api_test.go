// workspace_api_test.go — worktree REST API 集成测试。
//
// 用 httptest + 真实临时 SQLite（含 v28 migration）+ 真实临时 git 仓库 +
// 真实 *workspace.Manager，覆盖 spec: Worktree REST API 的全部 scenario：
//   - POST create 成功
//   - 重入 create 返回 409
//   - GET active 返回 worktree 信息
//   - 无 active 时 GET 返回 active:false
//   - WORKTREE_ENABLED=false（mgr 为 nil）→ 503
//   - exit 路由不存在 → 404
//
// 复用 setupSessionTreeDB 的 DB 初始化模式（见 workspace_tree_test.go）。
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/workspace"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	_ "modernc.org/sqlite"
)

// setupWorkspaceAPIDB 初始化临时 SQLite（含 v28 active_worktree_id 列）。
func setupWorkspaceAPIDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test_wt_api.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		db.DB = nil
	})
}

// wtAPIRepo 构造一个临时 git 仓库 + 指向其 .claude/worktrees 的 Manager，
// 设为 globalWorkspaceMgr，返回 repoRoot 供断言。同时确保 .claude/worktrees
// 被 gitignore（由 EnsureGitignored 完成）。
func wtAPIRepo(t *testing.T) string {
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
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n"), 0644)
	git("add", "README.md")
	git("commit", "-q", "-m", "init")

	rootDir := filepath.Join(dir, ".claude", "worktrees")
	os.MkdirAll(rootDir, 0755)
	mgr := workspace.NewManager(rootDir).SetRepoDir(dir)
	if err := mgr.EnsureGitignored(); err != nil {
		t.Fatalf("ensure gitignored: %v", err)
	}
	// 保存旧值，测试后恢复，避免污染其它测试。
	oldMgr := globalWorkspaceMgr
	globalWorkspaceMgr = mgr
	t.Cleanup(func() { globalWorkspaceMgr = oldMgr })
	return dir
}

// wtAPIHarness 构造 (httptest.Server + hub)，注册 RegisterWorkspaceAPI。
// 返回 server 与 hub（hub 用于断言事件）。
func wtAPIHarness(t *testing.T) (*httptest.Server, *ws.Hub) {
	t.Helper()
	hub := ws.NewHub()
	go hub.Run()
	mux := http.NewServeMux()
	RegisterWorkspaceAPI(mux, &appServer{hub: hub})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, hub
}

// wtCreateSession 插入一条 session 记录供 REST 路径使用。
func wtCreateSession(t *testing.T, sessionID string) {
	t.Helper()
	if err := db.InsertSession(db.SessionRecord{
		ID:     sessionID,
		Name:   "wt-api-test",
		Status: "active",
	}); err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

// TestWorktreeAPICreateAndGet 验证 create + get 端到端。
func TestWorktreeAPICreateAndGet(t *testing.T) {
	setupWorkspaceAPIDB(t)
	wtAPIRepo(t)
	ts, _ := wtAPIHarness(t)
	wtCreateSession(t, "sess-api-1")

	// POST create
	body := bytes.NewReader([]byte(`{"base_ref":"fresh"}`))
	resp, err := http.Post(ts.URL+"/api/sessions/sess-api-1/worktree", "application/json", body)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d, want 200", resp.StatusCode)
	}
	var created map[string]any
	json.NewDecoder(resp.Body).Decode(&created)
	if created["active"] != true || created["worktree_id"] == nil {
		t.Fatalf("unexpected create body: %+v", created)
	}
	wtID, _ := created["worktree_id"].(string)
	path, _ := created["path"].(string)
	if wtID == "" || path == "" {
		t.Fatalf("missing id/path: %+v", created)
	}

	// DB 绑定已设
	got, _ := db.GetSessionActiveWorktree("sess-api-1")
	if got != wtID {
		t.Fatalf("DB active = %q, want %q", got, wtID)
	}
	// worktree_created 事件广播已由 internal/tool/worktree_test.go（mockWtBus）
	// 在 tool 层覆盖；REST 层不重复断言（ws.Hub 无公开"列举全部事件"API，
	// ReplayEvents 需要有效的 sinceEventID）。

	// GET active
	resp2, err := http.Get(ts.URL + "/api/sessions/sess-api-1/worktree")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", resp2.StatusCode)
	}
	var info map[string]any
	json.NewDecoder(resp2.Body).Decode(&info)
	if info["active"] != true || info["worktree_id"] != wtID {
		t.Fatalf("unexpected get body: %+v", info)
	}
}

// hubHasEventWithin 轮询 hub 的 ReplayEvents，在约 1s 内等待指定事件出现。
// ws.Hub.SendEvent 经 buffered channel 异步处理，测试需短暂等待 eventBuf 落盘。
// 保留供未来需要 REST 层事件断言的用例使用。
func hubHasEventWithin(hub *ws.Hub, eventType string) bool {
	for range 50 {
		evts, err := hub.ReplayEvents("", 200)
		if err == nil {
			for _, e := range evts {
				if e.Type == eventType {
					return true
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestWorktreeAPICreateReentry409 验证已有 active 再 create 返回 409。
func TestWorktreeAPICreateReentry409(t *testing.T) {
	setupWorkspaceAPIDB(t)
	wtAPIRepo(t)
	ts, _ := wtAPIHarness(t)
	wtCreateSession(t, "sess-api-2")

	body := bytes.NewReader([]byte(`{"base_ref":"fresh"}`))
	resp, err := http.Post(ts.URL+"/api/sessions/sess-api-2/worktree", "application/json", body)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first create = %d", resp.StatusCode)
	}
	// 第二次 → 409
	resp2, err := http.Post(ts.URL+"/api/sessions/sess-api-2/worktree", "application/json", body)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("reentry status = %d, want 409", resp2.StatusCode)
	}
}

// TestWorktreeAPIGetNoActive 验证无 active worktree 时 GET 返回 active:false。
func TestWorktreeAPIGetNoActive(t *testing.T) {
	setupWorkspaceAPIDB(t)
	wtAPIRepo(t)
	ts, _ := wtAPIHarness(t)
	wtCreateSession(t, "sess-api-3")

	resp, err := http.Get(ts.URL + "/api/sessions/sess-api-3/worktree")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var info map[string]any
	json.NewDecoder(resp.Body).Decode(&info)
	if info["active"] != false {
		t.Fatalf("expected active:false, got %+v", info)
	}
}

// TestWorktreeAPIDisabled503 验证 mgr 为 nil（worktree 未启用）时返回 503。
func TestWorktreeAPIDisabled503(t *testing.T) {
	setupWorkspaceAPIDB(t)
	// 不调 wtAPIRepo，故 globalWorkspaceMgr 保持 nil（前提：其它测试已 Cleanup 恢复）。
	// 显式置 nil 以防串测。
	oldMgr := globalWorkspaceMgr
	globalWorkspaceMgr = nil
	t.Cleanup(func() { globalWorkspaceMgr = oldMgr })

	ts, _ := wtAPIHarness(t)
	wtCreateSession(t, "sess-api-4")

	resp, err := http.Post(ts.URL+"/api/sessions/sess-api-4/worktree", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled status = %d, want 503", resp.StatusCode)
	}
}

// TestWorktreeAPIExitRoute404 验证 exit 路由不存在（POST .../worktree/exit → 404）。
func TestWorktreeAPIExitRoute404(t *testing.T) {
	setupWorkspaceAPIDB(t)
	wtAPIRepo(t)
	ts, _ := wtAPIHarness(t)
	wtCreateSession(t, "sess-api-5")

	resp, err := http.Post(ts.URL+"/api/sessions/sess-api-5/worktree/exit", "application/json", bytes.NewReader([]byte(`{"action":"keep"}`)))
	if err != nil {
		t.Fatalf("exit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("exit route status = %d, want 404", resp.StatusCode)
	}
}

// hubHasEvent 检查 hub 是否广播过指定类型的事件。ws.Hub 通过 ReplayEvents
// 暴露最近事件历史（sinceEventID="" 从头返回），用它做断言。
// 保留供未来需要 REST 层事件断言的用例使用。
func hubHasEventOnce(hub *ws.Hub, eventType string) bool {
	evts, err := hub.ReplayEvents("", 200)
	if err != nil {
		return false
	}
	for _, e := range evts {
		if e.Type == eventType {
			return true
		}
	}
	return false
}

var _ = hubHasEventOnce // 保留 helper，未来 REST 事件断言用

// hubHasEventWithin 见上方定义；用 var _ 抑制 unused 警告（当前 REST 测试
// 不做事件断言，事件由 tool 层 mockWtBus 覆盖）。
var _ = hubHasEventWithin
