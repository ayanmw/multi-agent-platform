// worktree.go — worktree 隔离 Agent Tools（LLM 主入口）。
//
// 三个工具位于 namespace "worktree"，供 LLM 在 run 中自主进入/退出/查询隔离工作区：
//   - worktree/create —— 创建 worktree 并切换该 run 的 CWD holder
//   - worktree/exit   —— keep（保留待复查）/ remove（删除，受未提交护栏）
//   - worktree/status —— 查询当前是否在 worktree
//
// 设计要点：
//   - holder 是 per-run 的 workspace.WorkdirHolder，由 AgentRunner 在 run 启动时
//     构造并经 EngineConfig.WorkdirHolder 注入 Engine；本工具持有 holder 引用以
//     在 run 中途改写 CWD。LLM 传入的 workdir 会被 Engine 无条件覆盖（防逃逸）。
//   - session 与 worktree 的活跃绑定记录在 sessions.active_worktree_id：一个
//     session 任一时刻最多一个 active worktree，已有 active 再 create 返回错误。
//   - WORKTREE_ENABLED=false（mgr 为 nil）时 create 返回错误 observation，status
//     返回 active:false，exit 视为 no-op。
//   - 事件经 hub.SendEvent WS 广播（不写 task steps），贴合白盒 Agent 可观测性约定。
//
// 本工具依赖 workspace.Manager 与 workspace.WorkdirHolder（原语包）、pkg/db
// （session 绑定读写）、pkg/event（事件常量）。db 与 event 通过接口注入以保持
// tool 包可测、避免直接 import pkg/db（与 cron/tools.go 的接口反转一致）。
package tool

import (
	"fmt"

	"github.com/anmingwei/multi-agent-platform/internal/workspace"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// WorktreeSessionStore 是 worktree 工具所需的 session↔worktree 绑定读写接口。
// 由 pkg/db 实现（GetSessionActiveWorktree / SetSessionActiveWorktree /
// ClearSessionActiveWorktree），在 cmd/server 注入。接口反转避免 tool → pkg/db
// 的编译期依赖。
type WorktreeSessionStore interface {
	GetActiveWorktree(sessionID string) (string, error)
	SetActiveWorktree(sessionID, worktreeID string) error
	ClearActiveWorktree(sessionID string) error
}

// WorktreeEventBroadcaster 把 worktree 事件广播到 WS。hubAdapter 实现之。
type WorktreeEventBroadcaster interface {
	SendEvent(evt event.Event)
}

// WorktoolDeps 聚合 worktree 工具运行所需的依赖。
// WorkdirHolder 是 per-run 可变 CWD 持有者，由 AgentRunner 在 run 启动时构造；
// 工具持有其引用以在 create/exit 时改写。SessionID 是当前 run 绑定的 session，
// 用于 active_worktree_id 读写。
type WorktoolDeps struct {
	Mgr       *workspace.Manager
	Store     WorktreeSessionStore
	Bus       WorktreeEventBroadcaster
	Holder    *workspace.WorkdirHolder
	SessionID string
}

// RegisterWorktreeTools 把三个 worktree 工具注册到 registry。mgr 为 nil
//（WORKTREE_ENABLED=false 或 DB 不可用）时不注册，worktree 能力关闭。
func RegisterWorktreeTools(registry *Registry, deps WorktoolDeps) {
	if deps.Mgr == nil {
		return
	}
	registry.Register(NewWorktreeCreateTool(deps))
	registry.Register(NewWorktreeExitTool(deps))
	registry.Register(NewWorktreeStatusTool(deps))
}

// NewWorktreeCreateTool 创建 worktree/create 工具。
//
// 参数：
//   - base_ref (string, optional)："fresh"（默认，从 origin/默认分支）或 "head"
//     （从当前 HEAD）。
//
// 成功时创建 worktree、改写 holder 为 worktree.Path、设 active_worktree_id、
// 广播 worktree_created。已有 active 或 WORKTREE_DISABLED 时返回错误 observation。
func NewWorktreeCreateTool(deps WorktoolDeps) *BuiltinTool {
	return NewBuiltinTool(
		"create",
		"worktree",
		"Create an isolated git worktree for the current session and switch this run's working directory to it. Subsequent file/shell tools will operate inside the worktree (a fresh branch). Use base_ref=\"fresh\" (default, from origin/default branch) or \"head\" (from current HEAD). Only one active worktree per session; call worktree/exit before creating another.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"base_ref": map[string]any{
					"type":        "string",
					"enum":        []string{"fresh", "head"},
					"description": "Starting point: \"fresh\" (default, from origin/default branch) or \"head\" (from current HEAD)",
				},
			},
			"required": []string{},
		},
		func(_ ExecuteContext, input map[string]any) (any, error) {
			return executeWorktreeCreate(deps, input)
		},
	).WithTags("workspace", "workspace:worktree")
}

// NewWorktreeExitTool 创建 worktree/exit 工具。
//
// 参数：
//   - action (string, required)："keep"（保留目录待复查）或 "remove"（删除，受护栏）
//   - discard_changes (boolean, optional)：action=remove 且 worktree 有未提交变更时，
//     false（默认）→ 护栏阻塞、返回未提交文件列表 + 广播 worktree_exit_blocked；
//     true → 强制删除。
//
// 两种 action 都清空 active_worktree_id 并把 holder 恢复为 session 原始 WorkspaceDir。
func NewWorktreeExitTool(deps WorktoolDeps) *BuiltinTool {
	return NewBuiltinTool(
		"exit",
		"worktree",
		"Exit the current worktree. action=\"keep\" retains the worktree directory on disk for later review; action=\"remove\" deletes it (subject to an uncommitted-changes guard). When remove is blocked, the observation lists uncommitted files — set discard_changes=true to force removal. Either action clears the session's active worktree and restores the run's working directory to the session workspace.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"keep", "remove"},
					"description": "keep = retain worktree on disk; remove = delete it (guarded)",
				},
				"discard_changes": map[string]any{
					"type":        "boolean",
					"description": "If true, force remove even when there are uncommitted changes (default false)",
				},
			},
			"required": []string{"action"},
		},
		func(_ ExecuteContext, input map[string]any) (any, error) {
			return executeWorktreeExit(deps, input)
		},
	).WithTags("workspace", "workspace:worktree")
}

// NewWorktreeStatusTool 创建 worktree/status 工具，查询当前是否在 worktree。
func NewWorktreeStatusTool(deps WorktoolDeps) *BuiltinTool {
	return NewBuiltinTool(
		"status",
		"worktree",
		"Report whether the current session has an active worktree. Returns active=true with worktree id/branch/path when active, or active=false otherwise.",
		map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		},
		func(_ ExecuteContext, input map[string]any) (any, error) {
			return executeWorktreeStatus(deps), nil
		},
	).WithTags("workspace", "workspace:worktree", "workspace:readonly")
}

// executeWorktreeCreate 实现 worktree/create 的业务逻辑。
func executeWorktreeCreate(deps WorktoolDeps, input map[string]any) (any, error) {
	if deps.Mgr == nil {
		// WORKTREE_ENABLED=false：返回错误 observation，不创建。
		return worktreeErrObs("worktree feature is disabled (WORKTREE_ENABLED=false)"), nil
	}
	if deps.SessionID == "" {
		return worktreeErrObs("session_id is required to bind a worktree"), nil
	}
	// 重入护栏：已有 active worktree → 不创建。
	if existing, _ := deps.Store.GetActiveWorktree(deps.SessionID); existing != "" {
		return worktreeErrObs(fmt.Sprintf("session %s already has active worktree %s; call worktree/exit first", deps.SessionID, existing)), nil
	}

	baseRef := getString(input, "base_ref", "fresh")
	wt, warning, err := deps.Mgr.Create(deps.SessionID, baseRef)
	if err != nil {
		return worktreeErrObs(fmt.Sprintf("create worktree: %v", err)), nil
	}
	// 设 session 活跃绑定。
	if err := deps.Store.SetActiveWorktree(deps.SessionID, wt.ID); err != nil {
		// 绑定失败则回滚刚创建的 worktree，避免泄漏。
		_, _ = deps.Mgr.Remove(wt.ID, true)
		return worktreeErrObs(fmt.Sprintf("bind active worktree: %v", err)), nil
	}
	// 切换 per-run CWD holder 到 worktree.Path。
	if deps.Holder != nil {
		deps.Holder.Set(wt.Path)
	}
	// 广播 worktree_created（不写 task steps）。
	if deps.Bus != nil {
		deps.Bus.SendEvent(event.NewEvent(event.EventWorktreeCreated, deps.SessionID, "worktree", 0, map[string]any{
			"worktree_id": wt.ID,
			"branch":      wt.Branch,
			"path":        wt.Path,
			"base_ref":    wt.BaseRef,
		}))
	}

	obs := map[string]any{
		"created":     true,
		"worktree_id": wt.ID,
		"branch":      wt.Branch,
		"path":        wt.Path,
		"base_ref":    wt.BaseRef,
		"message":     fmt.Sprintf("Created worktree %s on branch %s; this run's working directory is now the worktree", wt.ID, wt.Branch),
	}
	if warning != "" {
		obs["warning"] = warning
	}
	return obs, nil
}

// executeWorktreeExit 实现 worktree/exit 的业务逻辑。
func executeWorktreeExit(deps WorktoolDeps, input map[string]any) (any, error) {
	action := getString(input, "action", "")
	switch action {
	case "keep", "remove":
	default:
		return worktreeErrObs("action must be \"keep\" or \"remove\""), nil
	}

	if deps.SessionID == "" {
		return worktreeErrObs("session_id is required to exit a worktree"), nil
	}
	wtID, _ := deps.Store.GetActiveWorktree(deps.SessionID)
	if wtID == "" {
		return worktreeErrObs("no active worktree to exit"), nil
	}

	if action == "remove" {
		discard := getBool(input, "discard_changes", false)
		rep, err := deps.Mgr.Remove(wtID, discard)
		if err != nil {
			return worktreeErrObs(fmt.Sprintf("remove worktree: %v", err)), nil
		}
		if rep.Blocked {
			// 护栏阻塞：保留 worktree 与 active 绑定，返回未提交文件列表。
			if deps.Bus != nil {
				deps.Bus.SendEvent(event.NewEvent(event.EventWorktreeExitBlocked, deps.SessionID, "worktree", 0, map[string]any{
					"worktree_id": wtID,
					"uncommitted": rep.Uncommitted,
					"unmerged":    rep.Unmerged,
				}))
			}
			return map[string]any{
				"exited":      false,
				"blocked":     true,
				"worktree_id": wtID,
				"uncommitted": rep.Uncommitted,
				"unmerged":    rep.Unmerged,
				"message":     "worktree has uncommitted changes; set discard_changes=true to force removal, or commit/keep them first",
			}, nil
		}
		// 删除成功：清绑定 + 广播 worktree_removed。
		_ = deps.Store.ClearActiveWorktree(deps.SessionID)
		if deps.Holder != nil {
			deps.Holder.Set("") // Engine 会回退到 input["workdir"]（session WorkspaceDir）
		}
		if deps.Bus != nil {
			deps.Bus.SendEvent(event.NewEvent(event.EventWorktreeRemoved, deps.SessionID, "worktree", 0, map[string]any{
				"worktree_id": wtID,
			}))
		}
		return map[string]any{
			"exited":      true,
			"removed":     true,
			"worktree_id": wtID,
			"message":     fmt.Sprintf("Removed worktree %s; working directory restored to session workspace", wtID),
		}, nil
	}

	// action == keep：保留目录，仅清绑定 + 恢复 holder。
	_ = deps.Store.ClearActiveWorktree(deps.SessionID)
	if deps.Holder != nil {
		deps.Holder.Set("")
	}
	return map[string]any{
		"exited":      true,
		"kept":        true,
		"worktree_id": wtID,
		"message":     fmt.Sprintf("Exited worktree %s (kept on disk for review); working directory restored to session workspace", wtID),
	}, nil
}

// executeWorktreeStatus 实现 worktree/status 的业务逻辑。
func executeWorktreeStatus(deps WorktoolDeps) any {
	if deps.Mgr == nil || deps.SessionID == "" {
		return map[string]any{"active": false}
	}
	wtID, _ := deps.Store.GetActiveWorktree(deps.SessionID)
	if wtID == "" {
		return map[string]any{"active": false}
	}
	wt, _ := deps.Mgr.Get(wtID)
	if wt == nil {
		// DB 记了 active 但 worktree 已不在（可能被外部删）：报告 active 但标记 missing。
		return map[string]any{"active": true, "worktree_id": wtID, "missing": true}
	}
	return map[string]any{
		"active":      true,
		"worktree_id": wt.ID,
		"branch":      wt.Branch,
		"path":        wt.Path,
		"base_ref":    wt.BaseRef,
	}
}

// worktreeErrObs 构造一个错误 observation map（error 字段供 LLM 识别失败原因）。
func worktreeErrObs(msg string) map[string]any {
	return map[string]any{"error": true, "message": msg}
}
