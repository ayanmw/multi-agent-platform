// worktree_run_integration_test.go — task 4.5：runner 级 per-run holder 切换集成测试。
//
// 本测试不启动真实 Engine，而是模拟 AgentRunner.runAgentLoopWithTurn 的接线：
//   1. 构造 per-run holder（初值 = session WorkspaceDir）
//   2. 克隆 registry，注册 write_file + worktree/* 工具，共享同一 holder
//   3. 按 Engine 的 tool 执行路径调 registry.ExecuteWithCtx，每次用
//      ExecuteContext{Workdir: holder.Get()} 注入 CWD（与 engine.go 一致）
//
// 覆盖 spec: Runner 接线 — per-run 可变 CWD holder 的 4.5 scenario：
//   - run 中途 create 切换 holder 后，后续 write_file 落 worktree.Path
//   - LLM 伪造 input["workdir"] 被忽略（ctx.Workdir 优先）
//   - exit{keep} 恢复 holder 为 ""，后续 write_file 回退 session WorkspaceDir
package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/workspace"
)

// setupRunIntegration 构造 runner 接线所需的全部组件：
//   - 真实临时 git 仓库 + 指向 .claude/worktrees 的 Manager
//   - per-run holder（初值 = session WorkspaceDir，一个独立临时目录模拟普通 workspace）
//   - 克隆的 registry，已注册 write_file + worktree/*，共享 holder
//
// 返回 registry、holder、sessionWorkspace（holder 初值）、worktree Manager、sessionID。
// 调用方用 execWithHolder 模拟 Engine 的 tool 执行路径。
func setupRunIntegration(t *testing.T) (*Registry, *workspace.WorkdirHolder, string, *workspace.Manager, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// 1. 真实 git 仓库
	repoDir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	git("config", "user.email", "test@test.local")
	git("config", "user.name", "test")
	os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# repo\n"), 0644)
	git("add", "README.md")
	git("commit", "-q", "-m", "init")

	wtRoot := filepath.Join(repoDir, ".claude", "worktrees")
	os.MkdirAll(wtRoot, 0755)
	mgr := workspace.NewManager(wtRoot).SetRepoDir(repoDir)

	// 2. session WorkspaceDir（普通目录，模拟 session.workspace_dir）
	sessionWS := t.TempDir()

	// 3. per-run holder，初值 = session WorkspaceDir
	holder := workspace.NewWorkdirHolder(sessionWS)

	// 4. registry：write_file + worktree/*，共享 holder
	reg := NewRegistry()
	reg.Register(NewWriteFileTool())
	RegisterWorktreeTools(reg, WorktoolDeps{
		Mgr:       mgr,
		Store:     newMockWtStore(),
		Bus:       &mockWtBus{},
		Holder:    holder,
		SessionID: "sess-run-1",
	})
	return reg, holder, sessionWS, mgr, "sess-run-1"
}

// execWithHolder 模拟 Engine 的 tool 执行路径：用 holder 当前值构造
// ExecuteContext.Workdir，再调 ExecuteWithCtx。这是 engine.go 在每次 tool
// 调用前做的事（见 engine.go: workdirCtx.Workdir = e.cfg.WorkdirHolder.Get()）。
// 传入 extra 允许 LLM 伪造 input 字段（如 workdir），验证它被 ctx.Workdir 覆盖。
func execWithHolder(t *testing.T, reg *Registry, holder *workspace.WorkdirHolder, name string, input map[string]any) (any, error) {
	t.Helper()
	ctx := ExecuteContext{Workdir: holder.Get()}
	return reg.ExecuteWithCtx(name, ctx, input)
}

// TestRunHolderSwitchToWorktree 验证 run 中途 create 切换 holder 后，后续
// write_file 落 worktree.Path（而非 session WorkspaceDir）。
func TestRunHolderSwitchToWorktree(t *testing.T) {
	reg, holder, sessionWS, _, _ := setupRunIntegration(t)

	// step 1：LLM 调 worktree/create → holder 切换到 worktree.Path
	out, err := execWithHolder(t, reg, holder, "worktree/create", map[string]any{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	obs := out.(map[string]any)
	wtPath, _ := obs["path"].(string)
	if wtPath == "" {
		t.Fatalf("missing worktree path: %+v", obs)
	}
	if holder.Get() != wtPath {
		t.Fatalf("holder = %q, want worktree path %q", holder.Get(), wtPath)
	}

	// step 2：后续 write_file 应落 worktree.Path
	out2, err := execWithHolder(t, reg, holder, "write_file", map[string]any{
		"path":    "inside_wt.txt",
		"content": "from-worktree",
	})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	gotPath := out2.(map[string]any)["path"].(string)
	wantPath := filepath.Join(wtPath, "inside_wt.txt")
	if gotPath != wantPath {
		t.Fatalf("write_file path = %q, want %q (should land in worktree)", gotPath, wantPath)
	}
	// 文件确实在 worktree 内，不在 session workspace
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("file not in worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionWS, "inside_wt.txt")); !os.IsNotExist(err) {
		t.Fatalf("file leaked into session workspace — holder switch did not take effect")
	}
}

// TestRunHolderIgnoresLLMForgedWorkdir 验证 holder 切到 worktree 后，LLM 在
// write_file input 里伪造 workdir（指向 session workspace）也无法逃逸——
// ctx.Workdir 优先，文件仍落 worktree。
func TestRunHolderIgnoresLLMForgedWorkdir(t *testing.T) {
	reg, holder, sessionWS, _, _ := setupRunIntegration(t)

	// create 切 holder 到 worktree
	out, _ := execWithHolder(t, reg, holder, "worktree/create", map[string]any{})
	wtPath := out.(map[string]any)["path"].(string)

	// LLM 伪造 workdir = sessionWS，试图逃逸
	out2, err := execWithHolder(t, reg, holder, "write_file", map[string]any{
		"path":    "no_escape.txt",
		"content": "x",
		"workdir": sessionWS, // 伪造，应被忽略
	})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	gotPath := out2.(map[string]any)["path"].(string)
	if gotPath != filepath.Join(wtPath, "no_escape.txt") {
		t.Fatalf("forged workdir escaped: path = %q, want %q", gotPath, filepath.Join(wtPath, "no_escape.txt"))
	}
	// 伪造目录里不应有文件
	if _, err := os.Stat(filepath.Join(sessionWS, "no_escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("file leaked into forged workdir — LLM workdir escape!")
	}
}

// TestRunHolderExitRestoresWorkspace 验证 exit{keep} 把 holder 恢复为 ""，
// 后续 write_file 在 ctx.Workdir 为空时回退 input["workdir"]（Engine 注入的
// session WorkspaceDir），落点回到 session workspace。
func TestRunHolderExitRestoresWorkspace(t *testing.T) {
	reg, holder, sessionWS, _, _ := setupRunIntegration(t)

	// create → holder 切到 worktree
	execWithHolder(t, reg, holder, "worktree/create", map[string]any{})
	if holder.Get() == "" || holder.Get() == sessionWS {
		t.Fatalf("holder should point to worktree after create, got %q", holder.Get())
	}

	// exit{keep} → holder 恢复 ""
	out, err := execWithHolder(t, reg, holder, "worktree/exit", map[string]any{"action": "keep"})
	if err != nil {
		t.Fatalf("exit: %v", err)
	}
	if exited, _ := out.(map[string]any)["exited"].(bool); !exited {
		t.Fatalf("exit did not succeed: %+v", out)
	}
	if holder.Get() != "" {
		t.Fatalf("holder = %q after exit, want empty (Engine falls back to WorkspaceDir)", holder.Get())
	}

	// 后续 write_file：ctx.Workdir 为空，Engine 已注入 input["workdir"]=sessionWS
	// （见 engine.go:1823 args["workdir"] = WorkspaceDir）。模拟该注入。
	out2, err := execWithHolder(t, reg, holder, "write_file", map[string]any{
		"path":    "after_exit.txt",
		"content": "back-to-session",
		"workdir": sessionWS, // Engine 在 exit 后注入的 session WorkspaceDir
	})
	if err != nil {
		t.Fatalf("write_file after exit: %v", err)
	}
	gotPath := out2.(map[string]any)["path"].(string)
	if gotPath != filepath.Join(sessionWS, "after_exit.txt") {
		t.Fatalf("write_file after exit = %q, want %q (should fall back to session workspace)", gotPath, filepath.Join(sessionWS, "after_exit.txt"))
	}
	if _, err := os.Stat(filepath.Join(sessionWS, "after_exit.txt")); err != nil {
		t.Fatalf("file not in session workspace after exit: %v", err)
	}
}

// TestRunHolderNoWorktreeToolsWhenMgrNil 验证 Manager 为 nil（worktree 未启用）
// 时 runner 不注册 worktree 工具，holder 始终保持 session WorkspaceDir，
// write_file 行为与无 worktree 的旧路径完全一致。
func TestRunHolderNoWorktreeToolsWhenMgrNil(t *testing.T) {
	sessionWS := t.TempDir()
	holder := workspace.NewWorkdirHolder(sessionWS)
	reg := NewRegistry()
	reg.Register(NewWriteFileTool())
	// 不注册 worktree 工具（Mgr 为 nil 时 RegisterWorktreeTools 是 no-op）
	RegisterWorktreeTools(reg, WorktoolDeps{Mgr: nil, Holder: holder})

	if _, ok := reg.Get("worktree/create"); ok {
		t.Fatalf("worktree/create should not be registered when Mgr is nil")
	}

	// write_file 仍用 holder 初值（sessionWS）
	out, err := execWithHolder(t, reg, holder, "write_file", map[string]any{
		"path":    "plain.txt",
		"content": "x",
	})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	gotPath := out.(map[string]any)["path"].(string)
	if gotPath != filepath.Join(sessionWS, "plain.txt") {
		t.Fatalf("write_file = %q, want %q", gotPath, filepath.Join(sessionWS, "plain.txt"))
	}
}
