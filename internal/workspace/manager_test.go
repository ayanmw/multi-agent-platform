package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupGitRepo 在临时目录初始化一个真实 git 仓库，返回仓库根路径。
// 仓库有一个初始提交在 main 分支上，便于 worktree add 测试。
func setupGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	git("init", "-q")
	// 确保默认分支为 main
	git("symbolic-ref", "HEAD", "refs/heads/main")
	git("config", "user.email", "test@test.local")
	git("config", "user.name", "test")
	// 初始提交
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-q", "-m", "init")
	return dir
}

// gitAvailable 跳过无 git 的环境。
func gitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

// newManagerAtRepo 在给定仓库根下创建 Manager，rootDir 指向 .claude/worktrees，
// 并把 repoDir 设为仓库根，使 git 命令以仓库根为 CWD（不依赖进程 cwd）。
func newManagerAtRepo(t *testing.T, repoRoot string) *Manager {
	t.Helper()
	rootDir := filepath.Join(repoRoot, ".claude", "worktrees")
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		t.Fatal(err)
	}
	return NewManager(rootDir).SetRepoDir(repoRoot)
}

// runInRepo 让后续 git CLI 在 repoRoot 下执行（gitRun 默认在进程 cwd）。
// 我们通过切 cwd 实现，因为 gitRun 的 workDir="" 走进程 cwd。
func runInRepo(t *testing.T, repoRoot string, fn func()) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)
	fn()
}

func TestCreateFresh(t *testing.T) {
	gitAvailable(t)
	repo := setupGitRepo(t)
	mgr := newManagerAtRepo(t, repo)
	runInRepo(t, repo, func() {
		wt, warning, err := mgr.Create("sess-abc", "fresh")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if warning != "" {
			t.Logf("warning: %s", warning) // 离线无 origin 时回退，允许
		}
		if wt == nil || wt.ID == "" || wt.Path == "" || wt.Branch == "" {
			t.Fatalf("invalid worktree: %+v", wt)
		}
		if wt.BaseRef != "fresh" {
			t.Fatalf("BaseRef = %q, want fresh", wt.BaseRef)
		}
		if _, err := os.Stat(filepath.Join(wt.Path, "README.md")); err != nil {
			t.Fatalf("worktree does not contain README.md: %v", err)
		}
	})
}

func TestCreateHead(t *testing.T) {
	gitAvailable(t)
	repo := setupGitRepo(t)
	mgr := newManagerAtRepo(t, repo)
	runInRepo(t, repo, func() {
		wt, _, err := mgr.Create("sess-abc", "head")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if wt.BaseRef != "head" {
			t.Fatalf("BaseRef = %q, want head", wt.BaseRef)
		}
	})
}

func TestGetAndList(t *testing.T) {
	gitAvailable(t)
	repo := setupGitRepo(t)
	mgr := newManagerAtRepo(t, repo)
	runInRepo(t, repo, func() {
		wt, _, err := mgr.Create("sess-abc", "fresh")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := mgr.Get(wt.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got == nil || got.ID != wt.ID {
			t.Fatalf("Get returned %v, want id %s", got, wt.ID)
		}
		// 不存在
		missing, err := mgr.Get("nope")
		if err != nil || missing != nil {
			t.Fatalf("Get(missing) = %v, %v", missing, err)
		}
		// List 至少含一个
		all, err := mgr.List()
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(all) < 1 {
			t.Fatalf("List empty")
		}
	})
}

func TestRemoveGuardUncommitted(t *testing.T) {
	gitAvailable(t)
	repo := setupGitRepo(t)
	mgr := newManagerAtRepo(t, repo)
	runInRepo(t, repo, func() {
		wt, _, err := mgr.Create("sess-abc", "fresh")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		// 写一个未提交文件（不 git add，保持 untracked）
		if err := os.WriteFile(filepath.Join(wt.Path, "dirty.txt"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		// 丢弃 stderr 避免污染测试输出；确认 status 看到 untracked
		statusOut, _ := exec.Command("git", "-C", wt.Path, "status", "--porcelain").CombinedOutput()
		t.Logf("status after dirty: %q", statusOut)
		if !strings.Contains(string(statusOut), "dirty.txt") {
			t.Fatalf("git status does not see dirty.txt: %q", statusOut)
		}
		// discard=false → 阻塞
		rep, err := mgr.Remove(wt.ID, false)
		if err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if !rep.Blocked || len(rep.Uncommitted) == 0 {
			t.Fatalf("expected blocked with uncommitted, got %+v", rep)
		}
		// worktree 仍存在
		if _, err := os.Stat(wt.Path); err != nil {
			t.Fatalf("worktree removed despite block: %v", err)
		}
		// discard=true → 删除
		rep2, err := mgr.Remove(wt.ID, true)
		if err != nil {
			t.Fatalf("Remove(force): %v", err)
		}
		if !rep2.Removed {
			t.Fatalf("expected removed, got %+v", rep2)
		}
		if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
			t.Fatalf("worktree still exists after force remove: %v", err)
		}
	})
}

func TestRemoveClean(t *testing.T) {
	gitAvailable(t)
	repo := setupGitRepo(t)
	mgr := newManagerAtRepo(t, repo)
	runInRepo(t, repo, func() {
		wt, _, err := mgr.Create("sess-abc", "fresh")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		rep, err := mgr.Remove(wt.ID, false)
		if err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if !rep.Removed || rep.Blocked {
			t.Fatalf("expected clean removed, got %+v", rep)
		}
	})
}

func TestKeep(t *testing.T) {
	gitAvailable(t)
	repo := setupGitRepo(t)
	mgr := newManagerAtRepo(t, repo)
	runInRepo(t, repo, func() {
		wt, _, err := mgr.Create("sess-abc", "fresh")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := mgr.Keep(wt.ID); err != nil {
			t.Fatalf("Keep: %v", err)
		}
		// 仍存在
		if _, err := os.Stat(wt.Path); err != nil {
			t.Fatalf("worktree gone after Keep: %v", err)
		}
	})
}

func TestEnsureGitignored(t *testing.T) {
	gitAvailable(t)
	repo := setupGitRepo(t)
	mgr := newManagerAtRepo(t, repo)
	runInRepo(t, repo, func() {
		if err := mgr.EnsureGitignored(); err != nil {
			t.Fatalf("EnsureGitignored: %v", err)
		}
		// .gitignore 应含 .claude/worktrees
		data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
		if err != nil {
			t.Fatalf("read .gitignore: %v", err)
		}
		if !strings.Contains(string(data), ".claude/worktrees") {
			t.Fatalf(".gitignore does not contain worktrees entry: %s", data)
		}
		// 幂等：再调一次不应重复追加
		before := string(data)
		if err := mgr.EnsureGitignored(); err != nil {
			t.Fatalf("EnsureGitignored 2nd: %v", err)
		}
		after, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
		if strings.Count(string(after), ".claude/worktrees") != strings.Count(before, ".claude/worktrees") {
			t.Fatalf("EnsureGitignored not idempotent: before=%q after=%q", before, after)
		}
	})
}

func TestWorkdirHolder(t *testing.T) {
	h := NewWorkdirHolder("/tmp/initial")
	if h.Get() != "/tmp/initial" {
		t.Fatalf("Get = %q, want /tmp/initial", h.Get())
	}
	h.Set("/tmp/changed")
	if h.Get() != "/tmp/changed" {
		t.Fatalf("Get = %q, want /tmp/changed", h.Get())
	}
}
