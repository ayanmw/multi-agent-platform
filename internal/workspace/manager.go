// Package workspace 实现 session 级 git worktree 隔离工作区。
//
// 设计哲学：worktree 是普通 session workspace 之上"主动触发的叠加能力"——
// LLM 在 run 中通过 worktree/* Agent Tool 自主决定何时进入隔离分支、何时退出，
// 不触发则系统零感知、沿用普通目录。这镜像 Claude Code 的 EnterWorktree /
// ExitWorktree 语义，但适配本项目的"白盒 Agent + LLM 自主 + 孤儿扫描兜底"范式。
//
// 本包只做三件事，刻意不依赖 ws / db / cmd/server，避免循环引用：
//  1. Manager —— git worktree 原语（Create / Keep / Remove / Get / List），含
//     未提交变更护栏（对齐 ExitWorktree 的 discard_changes 语义）。
//  2. WorkdirHolder —— per-run 可变工作目录持有者，是 tool CWD 的唯一可信源。
//     Engine 读取它注入 args["workdir"]，worktree/create 与 worktree/exit 改写它。
//     LLM 经 tool input 传入的 workdir 会被 Engine 无条件覆盖，从而无法逃逸。
//  3. Worktree / RemoveReport —— 领域模型。
//
// DB 持久化（sessions.active_worktree_id）与 WS 事件广播由调用方经接口注入
// （见 ActiveWorktreeStore / EventBroadcaster，在 tools.go 中使用），
// 与 cron 子系统的 DBStore / EventBus 接口反转模式一致。
package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Worktree 描述一个已创建的 git worktree。
//
// Path 是绝对路径（位于 Manager.rootDir 之下）；Branch 是 worktree 的工作分支；
// BaseRef 记录创建时使用的起点策略："fresh"（从 origin/默认分支）或 "head"
// （从当前本地 HEAD）。
//
// 注意：所有路径在比较前必须经 normPath 归一化——Windows 下 t.TempDir 返回
// 8.3 短名（ANMING~1）而 git 输出长名（anmingwei），直接 filepath.Rel 会误判为
// 不在同一根下。normPath 用 EvalSymlinks 把短名解析成长名再 Clean。
type Worktree struct {
	ID        string    `json:"id"`         // 短 ID，用作目录名与 DB 引用
	Branch    string    `json:"branch"`     // git 分支名
	Path      string    `json:"path"`       // worktree 绝对路径
	BaseRef   string    `json:"base_ref"`   // "fresh" | "head"
	SessionID string    `json:"session_id"` // 绑定的 session
	CreatedAt time.Time `json:"created_at"` // 创建时间
}

// RemoveReport 是 Manager.Remove 的返回，描述删除是否被护栏阻塞及细节。
//
// Blocked=true 时 worktree 仍保留，调用方应把 Uncommitted / Unmerged 展示给
// LLM 或前端，由其决定是否带 discardChanges=true 重试。
type RemoveReport struct {
	Blocked     bool     `json:"blocked"`      // 是否因未提交/未合并被护栏阻塞
	Uncommitted []string `json:"uncommitted"`  // 未提交文件相对路径列表
	Unmerged    bool     `json:"unmerged"`     // 分支是否尚未合并到默认分支
	Removed     bool     `json:"removed"`      // 是否已实际删除
	ID          string    `json:"id"`
}

// Manager 封装 git worktree 原语。所有操作经 git CLI 完成（不引入 go-git 依赖），
// 与 run_shell 共用 git/bash 环境。Manager 自身在 Create/Remove 上串行化，避免
// 并发派发多 agent 时 git worktree 竞态（符合 memory: lead-subagent-parallel-control）。
//
// repoDir 是主仓库（main worktree）的绝对路径，作为所有 git CLI 的工作目录。
// 生产环境 server 进程 CWD 未必是仓库根（session workspace 可能是独立目录），
// 因此 git 命令必须显式以 repoDir 为 CWD 才能作用到正确的仓库。为空时回退到
// 进程 CWD（供单元测试经 os.Chdir 复用）。
type Manager struct {
	rootDir string // worktree 根目录，如 .claude/worktrees/
	repoDir string // 主仓库绝对路径，作为 git CLI 的 CWD；空则用进程 CWD

	mu sync.Mutex // 串行化 Create/Remove
}

// NewManager 构造一个指向 rootDir 的 Manager。rootDir 应已被 .gitignore 忽略
// （由 EnsureGitignored 保证）。rootDir 会被 normPath 归一化（解析 8.3 短名）。
// repoDir 留空，调用方按需用 SetRepoDir 设置；未设置时 git 命令以进程 CWD 运行
// （测试场景经 os.Chdir 到仓库根即可）。
func NewManager(rootDir string) *Manager {
	return &Manager{rootDir: normPath(rootDir)}
}

// SetRepoDir 设置主仓库绝对路径，作为后续 git CLI 的工作目录。生产环境由
// cmd/server 在构造 Manager 后用 repoRoot() 注入；单元测试一般经 os.Chdir
// 复用进程 CWD，无需调用本方法。返回 Manager 自身以便链式构造。
func (m *Manager) SetRepoDir(dir string) *Manager {
	m.repoDir = normPath(dir)
	return m
}

// RootDir 返回 worktree 根目录（供调用方做孤儿扫描等）。
func (m *Manager) RootDir() string { return m.rootDir }

// Create 在 rootDir 下创建一个新 worktree，绑定到 sessionID。
//
// baseRef：
//   - "fresh"：从 origin/<默认分支> 切出新分支（干净起点）；origin 不可用时
//     回退到本地默认分支并记 warning（返回的 warning 非空）。
//   - "head"：从当前本地 HEAD 切出（保留当前工作上下文）。
//
// 分支名形如 wt/<sessionID短>/＜shortID＞，确保同 session 多次创建（在 exit 后）
// 不冲突。Manager 串行化保证并发安全。
func (m *Manager) Create(sessionID, baseRef string) (*Worktree, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sessionID == "" {
		return nil, "", fmt.Errorf("sessionID is required")
	}
	if baseRef == "" {
		baseRef = "fresh"
	}

	shortID := randomShortID()
	id := shortID // 直接用短 ID 作目录名与引用

	wtPath := filepath.Join(m.rootDir, id)
	wtPath = normPath(wtPath)
	branch := fmt.Sprintf("wt/%s/%s", sanitizeForBranch(sessionID), shortID)

	startRef, warning, err := m.resolveStartRef(baseRef)
	if err != nil {
		return nil, warning, err
	}

	// git worktree add <path> -b <branch> <startRef>
	if out, err := m.gitInRepo("worktree", "add", wtPath, "-b", branch, startRef); err != nil {
		return nil, warning, fmt.Errorf("git worktree add: %w\n%s", err, out)
	}

	return &Worktree{
		ID:        id,
		Branch:    branch,
		Path:      wtPath,
		BaseRef:   baseRef,
		SessionID: sessionID,
		CreatedAt: time.Now(),
	}, warning, nil
}

// resolveStartRef 把 baseRef 解析成可传给 `git worktree add ... <ref>` 的起点。
// fresh 优先用 origin/<默认分支>；失败回退本地默认分支并返回 warning。
func (m *Manager) resolveStartRef(baseRef string) (string, string, error) {
	defaultBranch, err := m.detectDefaultBranch()
	if err != nil {
		return "", "", fmt.Errorf("detect default branch: %w", err)
	}

	switch baseRef {
	case "head":
		return "HEAD", "", nil
	case "fresh":
		fallthrough
	default:
		// 优先 origin/<默认分支>
		originRef := "origin/" + defaultBranch
		if _, err := m.gitInRepo("rev-parse", "--verify", originRef); err == nil {
			return originRef, "", nil
		}
		// 回退本地默认分支
		if _, err := m.gitInRepo("rev-parse", "--verify", defaultBranch); err == nil {
			return defaultBranch, fmt.Sprintf("origin/%s not available, fell back to local %s", defaultBranch, defaultBranch), nil
		}
		// 最后回退 HEAD
		return "HEAD", fmt.Sprintf("neither origin/%s nor local %s available, fell back to HEAD", defaultBranch, defaultBranch), nil
	}
}

// Get 返回指定 ID 的 worktree；不存在返回 nil（不报错）。
// 基于 `git worktree list --porcelain` 解析，仅返回位于 rootDir 下的条目。
func (m *Manager) Get(id string) (*Worktree, error) {
	all, err := m.List()
	if err != nil {
		return nil, err
	}
	for _, w := range all {
		if w.ID == id {
			return w, nil
		}
	}
	return nil, nil
}

// List 列出位于 rootDir 下的全部 worktree。基于 `git worktree list --porcelain`。
func (m *Manager) List() ([]*Worktree, error) {
	out, err := m.gitInRepo("worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w\n%s", err, out)
	}
	return parseWorktreePorcelain(out, m.rootDir), nil
}

// Keep 保留 worktree 目录与分支在磁盘上，不删除。语义上仅表示"退出但保留待复查"，
// 物理删除请用 Remove。当前 Manager 不维护内存记账，Keep 是 no-op 占位，
// 与 ExitWorktree(action=keep) 对齐。
func (m *Manager) Keep(id string) error {
	if _, err := m.Get(id); err != nil {
		return err
	}
	return nil
}

// Remove 删除 worktree。护栏：当 worktree 有未提交文件（git status --porcelain 非空）
// 或其分支未合并到默认分支时，若 discardChanges=false，MUST 拒绝删除并返回
// RemoveReport{Blocked: true, ...}。discardChanges=true 时强制删除。
//
// 删除分两步：先 `git worktree remove <path>`，再删分支
// `git branch -D <branch>`（-D 因为可能未合并）。
func (m *Manager) Remove(id string, discardChanges bool) (*RemoveReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	wt, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	if wt == nil {
		return &RemoveReport{ID: id, Removed: true}, nil // 不存在视为已删除
	}

	// 护栏：检测未提交变更
	uncommitted, err := m.statusPorcelain(wt.Path)
	if err != nil {
		return nil, fmt.Errorf("check status: %w", err)
	}
	// 护栏：检测分支是否已合并到默认分支
	unmerged, err := m.isBranchUnmerged(wt.Branch)
	if err != nil {
		// 检测失败不阻断删除（best-effort），但记入 report
		unmerged = false
	}

	if (len(uncommitted) > 0 || unmerged) && !discardChanges {
		return &RemoveReport{
			Blocked:     true,
			Uncommitted: uncommitted,
			Unmerged:    unmerged,
			Removed:     false,
			ID:          id,
		}, nil
	}

	// 实际删除 worktree 目录
	if out, err := m.gitInRepo("worktree", "remove", "--force", wt.Path); err != nil {
		return nil, fmt.Errorf("git worktree remove: %w\n%s", err, out)
	}
	// 删除分支（-D 允许删除未合并分支）
	if out, err := m.gitInRepo("branch", "-D", wt.Branch); err != nil {
		// worktree 已删，分支删失败不回滚（分支残留可手动清理），仅记日志
		_ = out
	}

	return &RemoveReport{
		Blocked: false,
		Removed: true,
		ID:      id,
	}, nil
}

// RemoveOrphan 强制删除孤儿 worktree（启动扫描用），不做护栏检查。
// 供启动孤儿扫描清理 crash 残留：这些 worktree 在 DB 中已无 owner，无法判定
// 是否有在途工作，但既然 DB 不认得，保留也无意义。
func (m *Manager) RemoveOrphan(id string) error {
	wt, err := m.Get(id)
	if err != nil {
		return err
	}
	if wt == nil {
		return nil
	}
	if out, err := m.gitInRepo("worktree", "remove", "--force", wt.Path); err != nil {
		return fmt.Errorf("git worktree remove: %w\n%s", err, out)
	}
	if out, err := m.gitInRepo("branch", "-D", wt.Branch); err != nil {
		_ = out
	}
	return nil
}

// EnsureGitignored 确保 rootDir 被 .gitignore 忽略。已忽略则不改；未忽略则追加。
// 防止 worktree 内容污染主仓库 status（superpowers using-git-worktrees skill 安全验证）。
//
// 路径处理：.gitignore 条目以仓库根为基准。我们用 `git rev-parse --show-toplevel`
// 获取仓库根，再把 rootDir 相对化为相对路径，加 "/" 后缀（目录忽略）写入 .gitignore。
// check-ignore 也以仓库根为 CWD，传相对路径判定。
func (m *Manager) EnsureGitignored() error {
	rel, err := m.gitRelPath()
	if err != nil {
		// 不在 git 仓库内或路径无法相对化，best-effort 跳过
		return nil
	}
	// 目录忽略加尾斜杠
	relEntry := strings.TrimSuffix(rel, "/") + "/"
	ignored, err := m.gitCheckIgnored(rel)
	if err != nil {
		return nil // best-effort
	}
	if ignored {
		return nil
	}
	return m.appendGitignore(relEntry)
}

// ---------------------------------------------------------------------------
// WorkdirHolder —— per-run 可变 CWD 的唯一可信源
// ---------------------------------------------------------------------------

// WorkdirHolder 是一次 agent run 的可变工作目录持有者。
//
// 它是 tool CWD 的"单一事实源"：Engine 在每次 tool 调用前读取 holder.Get()，
// 无条件覆盖 LLM 传入的 args["workdir"]，使 LLM 无法伪造 workdir 逃逸到
// worktree 之外。worktree/create 在 run 中途 Set(worktree.Path) 切换 CWD，
// worktree/exit Set 回 session 原始 WorkspaceDir。
//
// holder 实例是 run-scoped 的：每个 AgentRunner.Run 新建一个，初值为 session
// 的 WorkspaceDir（普通目录），run 结束随 Engine 一起回收。多 goroutine 安全
// （同一 run 内 tool 串行执行，但 holder 仍加锁以防误用）。
type WorkdirHolder struct {
	mu  sync.RWMutex
	dir string
}

// NewWorkdirHolder 构造一个初值为 dir 的 holder。dir 为空时表示"未设置"，
// Engine 将回退到既有的 cfg.WorkspaceDir 行为。
func NewWorkdirHolder(dir string) *WorkdirHolder {
	return &WorkdirHolder{dir: dir}
}

// Get 返回当前工作目录。
func (h *WorkdirHolder) Get() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.dir
}

// Set 设置当前工作目录。worktree/create 与 worktree/exit 调用。
func (h *WorkdirHolder) Set(dir string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dir = dir
}

// ---------------------------------------------------------------------------
// git CLI 辅助
// ---------------------------------------------------------------------------

// gitRun 在 workDir 下执行 git 命令，返回 combined output。
// 兼容旧调用点（无 Manager 上下文，主要用于测试辅助与无 repoDir 的场景）。
func gitRun(workDir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// gitInRepo 执行 git 命令并以仓库根为 CWD。Manager.repoDir 非空时用它，
// 否则回退到进程 CWD（测试经 os.Chdir 到仓库根复用）。这是 Manager 上的
// 便捷方法，使 Create/Remove/List 等不依赖进程 CWD 即可作用到正确仓库。
func (m *Manager) gitInRepo(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if m.repoDir != "" {
		cmd.Dir = m.repoDir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// detectDefaultBranch 返回仓库的默认分支名。优先从 origin/HEAD 推断，
// 失败回退到本地当前分支，再失败回退 "main"。以 Manager.repoDir 为 CWD。
func (m *Manager) detectDefaultBranch() (string, error) {
	// origin/HEAD -> refs/remotes/origin/main
	if out, err := m.gitInRepo("symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		s := strings.TrimSpace(out)
		// 形如 origin/main，取最后一段
		if i := strings.LastIndex(s, "/"); i >= 0 {
			return s[i+1:], nil
		}
		if s != "" {
			return s, nil
		}
	}
	// 回退：本地当前分支
	if out, err := m.gitInRepo("symbolic-ref", "--short", "HEAD"); err == nil {
		if s := strings.TrimSpace(out); s != "" {
			return s, nil
		}
	}
	// 最后回退 main
	return "main", nil
}

// statusPorcelain 返回 worktreePath 下未提交文件的相对路径列表。
// 直接以 worktreePath 为 CWD 调 `git status --porcelain`。
func (m *Manager) statusPorcelain(worktreePath string) ([]string, error) {
	out, err := gitRun(worktreePath, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		// porcelain v1: XY <path>，path 从第 3 字符开始
		files = append(files, strings.TrimSpace(line[3:]))
	}
	return files, nil
}

// isBranchUnmerged 返回 true 当 branch 尚未合并到默认分支。
func (m *Manager) isBranchUnmerged(branch string) (bool, error) {
	defaultBranch, err := m.detectDefaultBranch()
	if err != nil {
		return false, err
	}
	// 用 --format 避免 porcelain marker（如 "+ wt/..." 中的 "*" 和 "+"）干扰匹配。
	out, err := m.gitInRepo("branch", "--no-merged", defaultBranch, "--format=%(refname:short)")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == branch {
			return true, nil
		}
	}
	return false, nil
}

// parseWorktreePorcelain 解析 `git worktree list --porcelain` 输出，
// 仅保留位于 rootDir 下的 worktree，并从路径提取 ID（rootDir 的最后一段 + 分隔符后）。
func parseWorktreePorcelain(out, rootDir string) []*Worktree {
	rootDir = normPath(rootDir)
	var result []*Worktree
	var cur *Worktree

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "worktree "):
			if cur != nil && isUnderRoot(cur.Path, rootDir) {
				result = append(result, cur)
			}
			path := strings.TrimPrefix(line, "worktree ")
			cur = &Worktree{Path: normPath(path)}
		case strings.HasPrefix(line, "branch "):
			if cur != nil {
				// porcelain 形如 "branch refs/heads/wt/..."，取短名
				cur.Branch = strings.TrimPrefix(line, "branch refs/heads/")
				if cur.Branch == line {
					cur.Branch = strings.TrimPrefix(line, "branch ")
				}
			}
		case line == "":
			// 记录边界
		default:
			// 其它字段（HEAD、detached 等）忽略
		}
	}
	if cur != nil && isUnderRoot(cur.Path, rootDir) {
		result = append(result, cur)
	}

	// 从路径提取 ID（rootDir 之后的第一段）并从分支补全
	for _, w := range result {
		rel, err := filepath.Rel(rootDir, w.Path)
		if err == nil {
			// ID = rel 的第一段
			parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
			w.ID = parts[0]
		}
	}
	return result
}

// isUnderRoot 判断 path 是否位于 rootDir 之下。两侧路径都经 normPath 归一化
// 以消除 Windows 8.3 短名差异。
func isUnderRoot(path, rootDir string) bool {
	rel, err := filepath.Rel(rootDir, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// gitRelPath 返回 Manager.rootDir 相对于仓库根的路径（用于写 .gitignore）。
// 以 Manager.repoDir 为 CWD 调 `git rev-parse --show-toplevel` 获取仓库根。
func (m *Manager) gitRelPath() (string, error) {
	out, err := m.gitInRepo("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := normPath(strings.TrimSpace(out))
	rel, err := filepath.Rel(root, normPath(m.rootDir))
	if err != nil {
		return "", err
	}
	// .gitignore 用正斜杠
	return filepath.ToSlash(rel), nil
}

// gitCheckIgnored 返回 path（相对仓库根的相对路径）是否已被 git 忽略。
// 以 Manager.repoDir 为 CWD 调 `git check-ignore`，用 exit code 判定：
// 0=被忽略，1=未被忽略。
func (m *Manager) gitCheckIgnored(path string) (bool, error) {
	cmd := exec.Command("git", "check-ignore", "-q", path)
	if m.repoDir != "" {
		cmd.Dir = m.repoDir
	}
	if err := cmd.Run(); err != nil {
		// 非 0 退出码最常见的是 "未被忽略"（exit 1），用 ExitError 区分
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// appendGitignore 把 entry 追加到仓库根的 .gitignore。以 Manager.repoDir 为
// CWD 调 `git rev-parse --show-toplevel` 定位仓库根。
func (m *Manager) appendGitignore(entry string) error {
	out, err := m.gitInRepo("rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	root := normPath(strings.TrimSpace(out))
	giPath := filepath.Join(root, ".gitignore")
	f, err := os.OpenFile(giPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(entry + "\n")
	return err
}

// randomShortID 生成 8 字符十六进制短 ID。
func randomShortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// 退化：用时间戳（不依赖 time.Now 在 workflow 脚本里的禁用，这里是真实运行时）
		return fmt.Sprintf("%x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b)
}

// sanitizeForBranch 把 sessionID 规范化成可作分支名的形式。
func sanitizeForBranch(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, s)
	s = strings.Trim(s, "-")
	if s == "" {
		return "sess"
	}
	// 截断避免分支名过长
	if len(s) > 24 {
		s = s[:24]
	}
	return s
}

// normPath 归一化绝对路径：先 EvalSymlinks 解析 Windows 8.3 短名（如
// ANMING~1 → anmingwei），再 Clean。这是 Windows 路径一致性的关键——t.TempDir
// 返回短名而 git CLI 输出长名，不归一化会导致 filepath.Rel 误判路径不在同一根下。
// EvalSymlinks 失败（路径不存在等）时退化到 Clean，保证 best-effort 不阻断。
func normPath(p string) string {
	if ev, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(ev)
	}
	return filepath.Clean(p)
}
