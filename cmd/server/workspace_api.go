// workspace_api.go — worktree 隔离子系统的 REST API、repoRoot 推断与孤儿扫描。
//
// 本文件做四件事：
//  1. repoRoot() —— 推断主仓库根目录（供 Manager.SetRepoDir 与 wtRoot 定位）。
//  2. reclaimOrphanWorktrees —— 启动孤儿扫描，对比 Manager.List() 与 DB 全表
//     active_worktree_id，清理 DB 不认得的 crash 残留 worktree，广播
//     worktree_orphan_removed。
//  3. RegisterWorkspaceAPI —— 注册 worktree REST 端点（仅 create + get），
//     挂载到传入 mux，便于测试隔离。**不注册 exit 路由**：退出需判定干净/合并
//     状态，风险高，仅 LLM 经 worktree/exit Agent Tool 执行。
//  4. worktreeSessionStoreAdapter —— 把 pkg/db 的 session↔worktree 绑定 helper
//     适配为 tool.WorktreeSessionStore 接口，供 Agent Tool 与 REST handler 共用。
//
// 设计要点（见 openspec/changes/add-workspace-worktree-isolation/proposal.md）：
//   - worktree 是主动触发的叠加能力，WORKTREE_ENABLED=false（mgr 为 nil）时
//     REST 返回 503，Agent Tool 不注册。
//   - 事件经 hub.SendEvent WS 广播（不写 task steps），贴合白盒可观测性约定。
//   - 不设 session 结束钩子；生命周期收敛为 LLM 主动 exit + 启动孤儿扫描兜底。
package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/observability"
	"github.com/anmingwei/multi-agent-platform/internal/workspace"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// repoRoot 推断主仓库根目录，供 worktree Manager 锚定 git 命令的 CWD。
//
// 优先级：
//  1. `git rev-parse --show-toplevel`（以进程 CWD 为基准，最可靠——server 进程
//     通常在仓库根或其子目录启动）。
//  2. 进程 CWD（os.Getwd，git 不可用时的兜底）。
//  3. 可执行文件所在目录（最后的兜底，覆盖从任意 CWD 启动的场景）。
//
// 返回的路径经 filepath.Clean 归一化。失败时返回空串，调用方负责处理
//（worktree 能力会因 Manager.repoDir 为空而退回进程 CWD 行为）。
func repoRoot() string {
	// 优先用 git 推断，以进程 CWD 为基准。
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").CombinedOutput(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			if abs, err := filepath.Abs(s); err == nil {
				return filepath.Clean(abs)
			}
			return filepath.Clean(s)
		}
	}
	// 兜底：进程 CWD。
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Clean(cwd)
	}
	// 最后兜底：可执行文件所在目录。
	if exe, err := os.Executable(); err == nil {
		return filepath.Clean(filepath.Dir(exe))
	}
	return ""
}

// reclaimOrphanWorktrees 在服务启动期清理孤儿 worktree：对比 Manager.List()
//（git worktree list 中位于 .claude/worktrees/ 下的全部条目）与 DB 全表
// active_worktree_id 绑定，凡 DB 不认得的 worktree 视为 crash 残留，强制删除
// 并广播 worktree_orphan_removed 事件。
//
// 这是"无 session 结束钩子"设计的兜底机制：LLM 忘记 exit 或进程崩溃时，
// worktree 会残留在磁盘上；启动扫描确保这些孤儿在下次启动时被回收，避免
// 泄漏。DB 认得的 worktree 保留不动——它们仍可能被 LLM 经 worktree/status
// 发现并继续使用。
func reclaimOrphanWorktrees(mgr *workspace.Manager, hub *ws.Hub) {
	if mgr == nil {
		return
	}
	all, err := mgr.List()
	if err != nil {
		observability.DefaultLogger.Warn("workspace", "orphan scan: list worktrees failed", map[string]any{"error": err.Error()})
		return
	}
	known, err := db.ListSessionActiveWorktrees()
	if err != nil {
		// DB 不可用时不做清理——避免误删仍被引用的 worktree。
		observability.DefaultLogger.Warn("workspace", "orphan scan: list DB active worktrees failed", map[string]any{"error": err.Error()})
		return
	}
	for _, w := range all {
		if _, ok := known[w.ID]; ok {
			continue // DB 认得，保留
		}
		// 孤儿：DB 不认得，强制删除。
		if err := mgr.RemoveOrphan(w.ID); err != nil {
			observability.DefaultLogger.Warn("workspace", "orphan scan: remove orphan failed", map[string]any{
				"worktree_id": w.ID,
				"error":        err.Error(),
			})
			continue
		}
		if hub != nil {
			hub.SendEvent(event.NewEvent(event.EventWorktreeOrphanRemoved, "orphan", "worktree", 0, map[string]any{
				"worktree_id": w.ID,
				"branch":      w.Branch,
				"path":        w.Path,
			}))
		}
		observability.DefaultLogger.Info("workspace", "orphan scan: removed orphan worktree", map[string]any{"worktree_id": w.ID})
	}
}

// ---------------------------------------------------------------------------
// worktreeSessionStoreAdapter —— pkg/db → tool.WorktreeSessionStore
// ---------------------------------------------------------------------------

// worktreeSessionStoreAdapter 把 pkg/db 的 session↔worktree 绑定 helper 适配为
// tool.WorktreeSessionStore 接口。无状态，可被 Agent Tool（per-run）与 REST
// handler 共用同一个实例。
type worktreeSessionStoreAdapter struct{}

// GetActiveWorktree 返回 session 当前 active worktree ID（空串表示无）。
func (worktreeSessionStoreAdapter) GetActiveWorktree(sessionID string) (string, error) {
	return db.GetSessionActiveWorktree(sessionID)
}

// SetActiveWorktree 设置 session 的 active worktree 绑定。
func (worktreeSessionStoreAdapter) SetActiveWorktree(sessionID, worktreeID string) error {
	return db.SetSessionActiveWorktree(sessionID, worktreeID)
}

// ClearActiveWorktree 清空 session 的 active worktree 绑定（置 NULL）。
func (worktreeSessionStoreAdapter) ClearActiveWorktree(sessionID string) error {
	return db.ClearSessionActiveWorktree(sessionID)
}

// globalWorkspaceStore 是 worktree REST / Agent Tool 共用的 session 绑定 store。
// 无状态，启动期构造一次。
var globalWorkspaceStore = worktreeSessionStoreAdapter{}

// ---------------------------------------------------------------------------
// RegisterWorkspaceAPI —— REST 端点（仅 create + get）
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// RegisterWorkspaceAPI —— 测试用 mux 注册（生产路由在 server.go registerRoutes
// 内的 /api/sessions/ handler 中分发，避免同前缀 ServeMux 冲突）
// ---------------------------------------------------------------------------

// RegisterWorkspaceAPI 把 worktree REST 端点挂到传入 mux，供集成测试用
// httptest.NewServer 隔离。生产路由不由此函数注册——server.go 的
// /api/sessions/ handler 已内联分发 /worktree，避免两个 handler 争抢同一前缀。
//
// 端点仅 create + get，**不注册 exit**（见 spec: Worktree REST API）。
// globalWorkspaceMgr 为 nil 时端点返回 503。
func RegisterWorkspaceAPI(mux *http.ServeMux, hub *ws.Hub) {
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
		if !strings.HasSuffix(path, "/worktree") {
			http.NotFound(w, r)
			return
		}
		sessionID := strings.TrimSuffix(path, "/worktree")
		if sessionID == "" || strings.Contains(sessionID, "/") {
			http.NotFound(w, r)
			return
		}
		if globalWorkspaceMgr == nil {
			http.Error(w, "worktree feature disabled", http.StatusServiceUnavailable)
			return
		}
		switch r.Method {
		case http.MethodPost:
			handleWorktreeCreate(w, r, sessionID, hub)
		case http.MethodGet:
			handleWorktreeGet(w, r, sessionID)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// handleWorktreeCreate 处理 POST /api/sessions/:id/worktree。
// body: {base_ref?: "fresh"|"head"}。已有 active worktree 返回 409。
func handleWorktreeCreate(w http.ResponseWriter, r *http.Request, sessionID string, hub *ws.Hub) {
	var body struct {
		BaseRef string `json:"base_ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	baseRef := body.BaseRef
	if baseRef == "" {
		baseRef = "fresh"
	}
	// 重入护栏：已有 active worktree → 409。
	if existing, _ := globalWorkspaceStore.GetActiveWorktree(sessionID); existing != "" {
		writeJSONStatus(w, http.StatusConflict, map[string]any{
			"error":       true,
			"message":     "session already has an active worktree; exit it first",
			"worktree_id": existing,
			"active":      true,
		})
		return
	}
	wt, warning, err := globalWorkspaceMgr.Create(sessionID, baseRef)
	if err != nil {
		http.Error(w, "create worktree: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := globalWorkspaceStore.SetActiveWorktree(sessionID, wt.ID); err != nil {
		// 绑定失败回滚刚创建的 worktree。
		_, _ = globalWorkspaceMgr.Remove(wt.ID, true)
		http.Error(w, "bind active worktree: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// 广播 worktree_created（不写 task steps）。
	if hub != nil {
		hub.SendEvent(event.NewEvent(event.EventWorktreeCreated, sessionID, "worktree", 0, map[string]any{
			"worktree_id": wt.ID,
			"branch":      wt.Branch,
			"path":        wt.Path,
			"base_ref":    wt.BaseRef,
		}))
	}
	resp := map[string]any{
		"active":      true,
		"worktree_id": wt.ID,
		"branch":      wt.Branch,
		"path":        wt.Path,
		"base_ref":    wt.BaseRef,
	}
	if warning != "" {
		resp["warning"] = warning
	}
	writeJSONStatus(w, http.StatusOK, resp)
}

// handleWorktreeGet 处理 GET /api/sessions/:id/worktree。无 active 返回 active:false。
func handleWorktreeGet(w http.ResponseWriter, r *http.Request, sessionID string) {
	wtID, err := globalWorkspaceStore.GetActiveWorktree(sessionID)
	if err != nil || wtID == "" {
		writeJSONStatus(w, http.StatusOK, map[string]any{"active": false})
		return
	}
	wt, err := globalWorkspaceMgr.Get(wtID)
	if err != nil || wt == nil {
		// DB 记了 active 但 worktree 已不在磁盘：报告 active 但标记 missing。
		writeJSONStatus(w, http.StatusOK, map[string]any{
			"active":      true,
			"worktree_id": wtID,
			"missing":     true,
		})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{
		"active":      true,
		"worktree_id": wt.ID,
		"branch":      wt.Branch,
		"path":        wt.Path,
		"base_ref":    wt.BaseRef,
	})
}

// writeJSONStatus 以指定 HTTP 状态码写 JSON 响应。writeJSON（无状态码版本）
// 已在 cron_api.go 定义，这里用带状态码的新名避免重复声明。
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
