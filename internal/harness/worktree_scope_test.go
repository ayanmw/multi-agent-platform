// worktree_scope_test.go — task 8.x 集成回归：FileScopeRule 与 worktree 隔离的协同。
//
// 验证 spec: 集成回归 的关键 scenario：worktree 启用后，Engine 把 per-run holder
// 的值注入 args["workdir"]（见 engine.go executeToolCall 的注入逻辑），使
// FileScopeRule 的 scope 锚定跟随 holder 切到 worktree.Path，否则 worktree 内的
// write_file 会被误判为"超出 session WorkspaceDir scope"而被拦。
//
// 这里直接在 harness 包内测试 FileScopeRule 与模拟 holder 注入的协同，避免
// tool → harness 的循环依赖（harness 已被 tool 间接依赖）。覆盖：
//   - holder 切到 worktree 后，args["workdir"]=worktree.Path → FileScopeRule 放行 worktree 内写
//   - LLM 用绝对路径逃逸到 session workspace → 被拦（scope=worktree）
//   - holder 恢复后，args["workdir"]=sessionWS → FileScopeRule 放行 session 内写
//   - LLM 伪造 workdir 被 Engine 覆盖（holder 优先）→ scope 仍跟随 holder
package harness

import (
	"path/filepath"
	"testing"
)

// engineInjectWorkdirForTest 模拟 engine.go executeToolCall 的 args["workdir"]
// 注入逻辑：holderDir 非 "" 时用它覆盖 args["workdir"]（无论 LLM 是否伪造）；
// 为空时回退 sessionWS（仅当 args 尚未有 workdir）。
//
// 这是 worktree 隔离与 FileScopeRule 协同的关键——scope 根（args["workdir"]）
// 与 CWD（ExecuteContext.Workdir）同源，使 worktree 内的写不会因 scope 仍锚定
// session workspace 而被误拦。
func engineInjectWorkdirForTest(holderDir, sessionWS string, args map[string]any) {
	if holderDir != "" {
		args["workdir"] = holderDir
		return
	}
	if sessionWS != "" {
		if _, has := args["workdir"]; !has {
			args["workdir"] = sessionWS
		}
	}
}

// TestFileScopeRuleFollowsWorktree 验证 holder 切到 worktree 后，FileScopeRule
// 以 worktree.Path 为 scope，worktree 内 write_file 放行。
func TestFileScopeRuleFollowsWorktree(t *testing.T) {
	rule := &FileScopeRule{}
	contract := TaskContract{Scope: "."} // 默认 scope，回退到 args["workdir"]

	wtPath := t.TempDir()
	args := map[string]any{"path": "scoped.txt", "content": "x"}
	engineInjectWorkdirForTest(wtPath, "", args)

	out, err := rule.Check("write_file", args, contract)
	if err != nil {
		t.Fatalf("FileScopeRule blocked worktree-internal write: %v", err)
	}
	normalized, _ := out["path"].(string)
	if !filepath.IsAbs(normalized) || !isUnder(normalized, wtPath) {
		t.Fatalf("normalized path %q not under worktree %q", normalized, wtPath)
	}
}

// TestFileScopeRuleRejectsEscapeFromWorktree 验证 holder 切到 worktree 后，
// LLM 用绝对路径试图写到 worktree 之外（但仍在 session workspace）会被拦。
func TestFileScopeRuleRejectsEscapeFromWorktree(t *testing.T) {
	rule := &FileScopeRule{}
	contract := TaskContract{Scope: "."}

	wtPath := t.TempDir()
	sessionWS := t.TempDir()
	escapePath := filepath.Join(sessionWS, "escape.txt")
	args := map[string]any{"path": escapePath, "content": "x"}
	engineInjectWorkdirForTest(wtPath, "", args) // scope = worktree.Path

	_, err := rule.Check("write_file", args, contract)
	if err == nil {
		t.Fatalf("FileScopeRule allowed escape from worktree to session workspace — scope not following holder")
	}
}

// TestFileScopeRuleRestoresAfterExit 验证 holder 恢复 "" 后，Engine 注入
// args["workdir"]=sessionWS，FileScopeRule 以 sessionWS 为 scope 放行 session
// 内写。
func TestFileScopeRuleRestoresAfterExit(t *testing.T) {
	rule := &FileScopeRule{}
	contract := TaskContract{Scope: "."}

	sessionWS := t.TempDir()
	args := map[string]any{"path": "back.txt", "content": "x"}
	engineInjectWorkdirForTest("", sessionWS, args) // holder 空 → 回退 sessionWS

	out, err := rule.Check("write_file", args, contract)
	if err != nil {
		t.Fatalf("FileScopeRule blocked session-internal write after exit: %v", err)
	}
	normalized, _ := out["path"].(string)
	if !isUnder(normalized, sessionWS) {
		t.Fatalf("normalized path %q not under session workspace %q", normalized, sessionWS)
	}
}

// TestFileScopeRuleIgnoresLLMForgedWorkdir 验证 LLM 伪造的 workdir 被 Engine
// 注入覆盖——scope 仍跟随 holder，不被 LLM 伪造值带偏。
func TestFileScopeRuleIgnoresLLMForgedWorkdir(t *testing.T) {
	rule := &FileScopeRule{}
	contract := TaskContract{Scope: "."}

	wtPath := t.TempDir()
	fakeDir := t.TempDir()
	args := map[string]any{"path": "forged.txt", "content": "x", "workdir": fakeDir}
	engineInjectWorkdirForTest(wtPath, "", args)
	if args["workdir"] != wtPath {
		t.Fatalf("engine injection did not override forged workdir: got %v, want %q", args["workdir"], wtPath)
	}
	_, err := rule.Check("write_file", args, contract)
	if err != nil {
		t.Fatalf("FileScopeRule blocked after engine override: %v", err)
	}
}

// isUnder 报告 path 是否位于 base 之下（含 base 自身）。两侧均需为绝对路径。
func isUnder(path, base string) bool {
	if path == base {
		return true
	}
	return len(path) > len(base) && filepath.Clean(path[:len(base)+1]) == filepath.Clean(base+string(filepath.Separator))
}
