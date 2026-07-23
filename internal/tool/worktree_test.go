// worktree_test.go — worktree/create、worktree/exit、worktree/status 三个 Agent Tool
// 的单元测试，覆盖 spec: Worktree Agent Tools 的全部 scenario。
//
// 测试用真实的临时 git 仓库 + mock store/bus，验证：
//   - create 成功改写 holder、设 active 绑定、广播 worktree_created
//   - 重入护栏（已有 active 再 create → 错误 observation）
//   - WORKTREE_ENABLED=false（Mgr 为 nil）→ create 错误、status active:false
//   - status 查询
//   - exit keep 清绑定 + 恢复 holder
//   - exit remove 干净 → 删除 + 广播 worktree_removed
//   - exit remove 有未提交 → 护栏阻塞 + 广播 worktree_exit_blocked
package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/workspace"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// wtTestRepo 用临时 git 仓库构造一个 Manager + holder，供 worktree 工具测试。
// 返回 Manager、holder、repoRoot、sessionID 与一个清理函数。
func wtTestRepo(t *testing.T) (*workspace.Manager, *workspace.WorkdirHolder, string, string) {
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
	holder := workspace.NewWorkdirHolder(dir)
	return mgr, holder, dir, "sess-test-1"
}

// mockWtStore 是 WorktreeSessionStore 的内存实现，供测试注入。
type mockWtStore struct {
	active map[string]string // sessionID -> worktreeID
	setErr error
}

func newMockWtStore() *mockWtStore { return &mockWtStore{active: map[string]string{}} }

func (s *mockWtStore) GetActiveWorktree(sessionID string) (string, error) {
	return s.active[sessionID], nil
}
func (s *mockWtStore) SetActiveWorktree(sessionID, worktreeID string) error {
	if s.setErr != nil {
		return s.setErr
	}
	s.active[sessionID] = worktreeID
	return nil
}
func (s *mockWtStore) ClearActiveWorktree(sessionID string) error {
	delete(s.active, sessionID)
	return nil
}

// mockWtBus 收集广播的 worktree 事件，供测试断言。
type mockWtBus struct{ events []event.Event }

func (b *mockWtBus) SendEvent(evt event.Event) { b.events = append(b.events, evt) }

func (b *mockWtBus) hasType(t string) bool {
	for _, e := range b.events {
		if e.Type == t {
			return true
		}
	}
	return false
}

// registerWt 把三个 worktree 工具注册到新 Registry，返回 registry 与依赖引用。
func registerWt(t *testing.T, mgr *workspace.Manager, holder *workspace.WorkdirHolder, sessionID string) (*Registry, *mockWtStore, *mockWtBus) {
	t.Helper()
	reg := NewRegistry()
	store := newMockWtStore()
	bus := &mockWtBus{}
	RegisterWorktreeTools(reg, WorktoolDeps{
		Mgr:       mgr,
		Store:     store,
		Bus:       bus,
		Holder:    holder,
		SessionID: sessionID,
	})
	return reg, store, bus
}

// TestWorktreeCreateSuccess 验证 create 成功：holder 被改写到 worktree.Path、
// active 绑定被设置、worktree_created 事件被广播。
func TestWorktreeCreateSuccess(t *testing.T) {
	mgr, holder, repo, sid := wtTestRepo(t)
	reg, store, bus := registerWt(t, mgr, holder, sid)

	out, err := reg.Execute("worktree/create", map[string]any{})
	if err != nil {
		t.Fatalf("execute create: %v", err)
	}
	obs, ok := out.(map[string]any)
	if !ok || obs["created"] != true {
		t.Fatalf("unexpected create output: %+v", out)
	}
	wtID, _ := obs["worktree_id"].(string)
	path, _ := obs["path"].(string)
	if wtID == "" || path == "" {
		t.Fatalf("missing worktree_id/path: %+v", obs)
	}
	// holder 被改写到 worktree.Path
	if holder.Get() != path {
		t.Fatalf("holder = %q, want worktree path %q", holder.Get(), path)
	}
	// active 绑定已设置
	if got := store.active[sid]; got != wtID {
		t.Fatalf("active binding = %q, want %q", got, wtID)
	}
	// 广播了 worktree_created
	if !bus.hasType(event.EventWorktreeCreated) {
		t.Fatalf("worktree_created not broadcast; events=%v", bus.events)
	}
	// holder 不再是 repo 根
	if holder.Get() == repo {
		t.Fatalf("holder still points to repo root")
	}
}

// TestWorktreeCreateReentryBlocked 验证已有 active worktree 再 create 返回错误 observation。
func TestWorktreeCreateReentryBlocked(t *testing.T) {
	mgr, holder, _, sid := wtTestRepo(t)
	reg, store, bus := registerWt(t, mgr, holder, sid)

	// 第一次 create 成功
	if _, err := reg.Execute("worktree/create", map[string]any{}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	firstPath := holder.Get()
	// 第二次 create 应返回错误 observation，不切换 holder
	out, err := reg.Execute("worktree/create", map[string]any{})
	if err != nil {
		t.Fatalf("execute second create: %v", err)
	}
	obs := out.(map[string]any)
	if obs["error"] != true {
		t.Fatalf("expected error observation, got %+v", obs)
	}
	if holder.Get() != firstPath {
		t.Fatalf("holder changed on blocked reentry: %q -> %q", firstPath, holder.Get())
	}
	_ = store
	_ = bus
}

// TestWorktreeCreateDisabled 验证 Mgr 为 nil（WORKTREE_ENABLED=false）时 create
// 工具本身返回错误 observation（防御性兜底：实际生产中 Mgr 为 nil 时不注册工具，
// LLM 看不到 worktree/create；这里直接构造工具验证其内部 nil 守卫）。
func TestWorktreeCreateDisabled(t *testing.T) {
	reg := NewRegistry()
	holder := workspace.NewWorkdirHolder("/tmp/whatever")
	// 直接注册单个 create 工具（绕过 RegisterWorktreeTools 的 nil 守卫），
	// 验证 executor 内部对 Mgr=nil 的兜底返回错误 observation。
	reg.Register(NewWorktreeCreateTool(WorktoolDeps{
		Mgr:       nil, // worktree 未启用
		Holder:    holder,
		SessionID: "sess-x",
	}))
	out, err := reg.Execute("worktree/create", map[string]any{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	obs := out.(map[string]any)
	if obs["error"] != true {
		t.Fatalf("expected error observation when disabled, got %+v", obs)
	}
	if !strings.Contains(obs["message"].(string), "disabled") {
		t.Fatalf("error message should mention disabled: %+v", obs)
	}
}

// TestWorktreeStatus 验证 status 在有/无 active 时的返回。
func TestWorktreeStatus(t *testing.T) {
	mgr, holder, _, sid := wtTestRepo(t)
	reg, store, _ := registerWt(t, mgr, holder, sid)

	// 无 active → active:false
	out, _ := reg.Execute("worktree/status", map[string]any{})
	obs := out.(map[string]any)
	if obs["active"] != false {
		t.Fatalf("expected active:false, got %+v", obs)
	}

	// create 后 → active:true
	reg.Execute("worktree/create", map[string]any{})
	out, _ = reg.Execute("worktree/status", map[string]any{})
	obs = out.(map[string]any)
	if obs["active"] != true {
		t.Fatalf("expected active:true after create, got %+v", obs)
	}
	if obs["worktree_id"] != store.active[sid] {
		t.Fatalf("status worktree_id mismatch: %+v", obs)
	}
}

// TestWorktreeExitKeep 验证 exit{keep} 清绑定 + 恢复 holder，但不删目录。
func TestWorktreeExitKeep(t *testing.T) {
	mgr, holder, _, sid := wtTestRepo(t)
	reg, store, bus := registerWt(t, mgr, holder, sid)

	reg.Execute("worktree/create", map[string]any{})
	wtID := store.active[sid]
	wtPath := holder.Get()

	out, err := reg.Execute("worktree/exit", map[string]any{"action": "keep"})
	if err != nil {
		t.Fatalf("exit keep: %v", err)
	}
	obs := out.(map[string]any)
	if obs["exited"] != true || obs["kept"] != true {
		t.Fatalf("unexpected exit keep output: %+v", obs)
	}
	// 绑定已清
	if store.active[sid] != "" {
		t.Fatalf("active binding not cleared: %q", store.active[sid])
	}
	// holder 恢复为 ""（Engine 回退 WorkspaceDir）
	if holder.Get() != "" {
		t.Fatalf("holder = %q, want empty after exit", holder.Get())
	}
	// worktree 目录仍存在（keep 保留）
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("worktree dir removed on keep: %v", err)
	}
	_ = wtID
	_ = bus
}

// TestWorktreeExitRemoveClean 验证 exit{remove} 在干净 worktree 上删除 + 广播 removed。
func TestWorktreeExitRemoveClean(t *testing.T) {
	mgr, holder, _, sid := wtTestRepo(t)
	reg, store, bus := registerWt(t, mgr, holder, sid)

	reg.Execute("worktree/create", map[string]any{})
	wtID := store.active[sid]
	wtPath := holder.Get()

	out, err := reg.Execute("worktree/exit", map[string]any{"action": "remove"})
	if err != nil {
		t.Fatalf("exit remove: %v", err)
	}
	obs := out.(map[string]any)
	if obs["exited"] != true || obs["removed"] != true {
		t.Fatalf("unexpected exit remove output: %+v", obs)
	}
	// 绑定已清
	if store.active[sid] != "" {
		t.Fatalf("active binding not cleared after remove")
	}
	// 目录已删
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still exists after remove: %v", err)
	}
	// 广播了 worktree_removed
	if !bus.hasType(event.EventWorktreeRemoved) {
		t.Fatalf("worktree_removed not broadcast; events=%v", bus.events)
	}
	_ = wtID
}

// TestWorktreeExitRemoveGuarded 验证 exit{remove} 在有未提交变更时护栏阻塞 +
// 广播 exit_blocked，目录与绑定保留。
func TestWorktreeExitRemoveGuarded(t *testing.T) {
	mgr, holder, _, sid := wtTestRepo(t)
	reg, store, bus := registerWt(t, mgr, holder, sid)

	reg.Execute("worktree/create", map[string]any{})
	wtID := store.active[sid]
	wtPath := holder.Get()

	// 写一个未提交文件
	if err := os.WriteFile(filepath.Join(wtPath, "dirty.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := reg.Execute("worktree/exit", map[string]any{"action": "remove"})
	if err != nil {
		t.Fatalf("exit remove: %v", err)
	}
	obs := out.(map[string]any)
	if obs["exited"] != false || obs["blocked"] != true {
		t.Fatalf("expected blocked, got %+v", obs)
	}
	uncommitted, _ := obs["uncommitted"].([]string)
	if len(uncommitted) == 0 {
		t.Fatalf("expected uncommitted file list, got %+v", obs)
	}
	// 绑定保留
	if store.active[sid] != wtID {
		t.Fatalf("active binding cleared despite block: %q", store.active[sid])
	}
	// 目录保留
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("worktree dir removed despite block: %v", err)
	}
	// 广播了 worktree_exit_blocked
	if !bus.hasType(event.EventWorktreeExitBlocked) {
		t.Fatalf("worktree_exit_blocked not broadcast; events=%v", bus.events)
	}

	// discard=true 强制删除 → 成功
	out2, err := reg.Execute("worktree/exit", map[string]any{"action": "remove", "discard_changes": true})
	if err != nil {
		t.Fatalf("exit remove force: %v", err)
	}
	obs2 := out2.(map[string]any)
	if obs2["removed"] != true {
		t.Fatalf("expected forced removal, got %+v", obs2)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still exists after force remove")
	}
}

// TestWorktreeExitInvalidAction 验证 action 非法时返回错误 observation。
func TestWorktreeExitInvalidAction(t *testing.T) {
	mgr, holder, _, sid := wtTestRepo(t)
	reg, _, _ := registerWt(t, mgr, holder, sid)

	out, err := reg.Execute("worktree/exit", map[string]any{"action": "bogus"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	obs := out.(map[string]any)
	if obs["error"] != true {
		t.Fatalf("expected error for invalid action, got %+v", obs)
	}
}

// TestRegisterWorktreeToolsNilMgr 验证 Mgr 为 nil 时不注册任何工具。
func TestRegisterWorktreeToolsNilMgr(t *testing.T) {
	reg := NewRegistry()
	RegisterWorktreeTools(reg, WorktoolDeps{Mgr: nil})
	if _, ok := reg.Get("worktree/create"); ok {
		t.Fatalf("worktree/create should not be registered when Mgr is nil")
	}
}
