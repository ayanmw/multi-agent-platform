package harness

// policy_test.go — PolicyChain / PolicyGate 与各 PolicyRule 的单元测试。
//
// 覆盖：PathTraversalRule、FileScopeRule、DangerousCommandRule、ApprovalRule、
// TokenBudgetRule、ToolWhitelistRule、CostBudgetRule，以及 PolicyChain 的
// 顺序执行与短路语义、PolicyGate 的 contract 注入与 executor 回调。
//
// 全部为纯逻辑测试，不依赖网络与数据库。表驱动 + t.Run 子测试。

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// --- 辅助：判断 err 是否为 ErrBlockedByPolicy ------------------------------

func isPolicyBlock(err error) bool {
	var p *ErrBlockedByPolicy
	return errors.As(err, &p)
}

func isApprovalRequired(err error) bool {
	var a *ErrApprovalRequired
	return errors.As(err, &a)
}

// blockReason 提取 ErrBlockedByPolicy 的 Reason（若非 block 则返回空串）。
func blockReason(err error) string {
	var p *ErrBlockedByPolicy
	if errors.As(err, &p) {
		return p.Reason
	}
	return ""
}

// ============================================================================
// PathTraversalRule
// ============================================================================

func TestPathTraversalRule(t *testing.T) {
	rule := &PathTraversalRule{}

	// 文件工具的 path 字段
	t.Run("file tools with ..", func(t *testing.T) {
		cases := []struct {
			tool string
			path string
			want bool // true = 应被拦截
		}{
			{"write_file", "../etc/passwd", true},
			{"write_file", "a/../b", true},
			{"read_file", "../../secret", true},
			{"write_file", "normal/path.txt", false},
			{"read_file", "./sub/file.go", false},
			{"write_file", "file.go", false},
		}
		for _, c := range cases {
			_, err := rule.Check(c.tool, map[string]any{"path": c.path}, TaskContract{Scope: "."})
			got := isPolicyBlock(err)
			if got != c.want {
				t.Errorf("Check(%q, path=%q) block=%v, want %v (err=%v)", c.tool, c.path, got, c.want, err)
			}
		}
	})

	// run_shell 的 command 字段扫描 ../ 与 ..\ 模式
	t.Run("run_shell command traversal", func(t *testing.T) {
		cases := []struct {
			cmd  string
			want bool
		}{
			{"cat ../../../etc/passwd", true},
			{"ls ..\\..\\secret", true},
			{"echo hello", false},
			{"go build ./...", false}, // "..." 不含 "../"
		}
		for _, c := range cases {
			_, err := rule.Check("run_shell", map[string]any{"command": c.cmd}, TaskContract{Scope: "."})
			got := isPolicyBlock(err)
			if got != c.want {
				t.Errorf("run_shell cmd=%q block=%v, want %v (err=%v)", c.cmd, got, c.want, err)
			}
		}
	})

	// 非文件/非 shell 工具直接放行
	t.Run("unrelated tool passthrough", func(t *testing.T) {
		_, err := rule.Check("http_request", map[string]any{"path": "../x"}, TaskContract{Scope: "."})
		if err != nil {
			t.Errorf("unrelated tool should pass, got err=%v", err)
		}
	})

	// 缺 path 字段放行
	t.Run("missing path passthrough", func(t *testing.T) {
		_, err := rule.Check("write_file", map[string]any{}, TaskContract{Scope: "."})
		if err != nil {
			t.Errorf("missing path should pass, got err=%v", err)
		}
	})

	if rule.Name() != "PathTraversalRule" {
		t.Errorf("Name() = %q, want PathTraversalRule", rule.Name())
	}
}

// ============================================================================
// FileScopeRule
// ============================================================================

func TestFileScopeRule(t *testing.T) {
	rule := &FileScopeRule{}

	// 用临时目录作为 scope，避免依赖 CWD
	tmp := t.TempDir()
	contract := TaskContract{Scope: tmp}

	t.Run("relative path within scope allowed + normalized", func(t *testing.T) {
		out, err := rule.Check("write_file", map[string]any{"path": "sub/file.txt"}, contract)
		if err != nil {
			t.Fatalf("expected allow, got %v", err)
		}
		normalized, ok := out["path"].(string)
		if !ok || !strings.HasPrefix(normalized, tmp) {
			t.Errorf("expected normalized path under %s, got %v", tmp, out["path"])
		}
	})

	t.Run("absolute path within scope allowed", func(t *testing.T) {
		abs := tmp + "/nested/deep.txt" // TempDir 已是绝对路径
		out, err := rule.Check("read_file", map[string]any{"path": abs}, contract)
		if err != nil {
			t.Fatalf("expected allow, got %v", err)
		}
		if out["path"] != abs && out["path"] != strings.ReplaceAll(abs, "/", string(filepath.Separator)) {
			// Clean 可能调整分隔符，只要仍在 scope 内即可
			n, _ := out["path"].(string)
			if !strings.HasPrefix(n, tmp) {
				t.Errorf("normalized path %q not under scope %q", n, tmp)
			}
		}
	})

	t.Run("absolute path outside scope blocked", func(t *testing.T) {
		// 用 scope 的父目录作为"绝对但越界"路径，保证跨平台（Windows 上 /etc/passwd
		// 不被 filepath.IsAbs 识别为绝对路径，会被当相对路径并入 scope，无法测越界）。
		outside := filepath.Dir(tmp)
		_, err := rule.Check("write_file", map[string]any{"path": outside}, contract)
		if !isPolicyBlock(err) {
			t.Errorf("expected block for path outside scope (%q), got %v", outside, err)
		}
	})

	t.Run("unix absolute path blocked on every platform", func(t *testing.T) {
		// S1 修复：Unix 绝对路径（如 "/etc/passwd"）必须在所有平台上被识别为绝对路径，
		// 不允许被 filepath.Join 并入 scope 而放行。Windows 上 scope 是 "C:\\..."，
		// "/etc/passwd" 解析后不可能是其前缀，必须被拒绝。
		_, err := rule.Check("write_file", map[string]any{"path": "/etc/passwd"}, contract)
		if !isPolicyBlock(err) {
			t.Errorf("expected block for unix-absolute /etc/passwd, got %v (err=%v)", err, err)
		}
	})

	t.Run("relative traversal blocked by scope", func(t *testing.T) {
		// 注意：PathTraversalRule 会先拦 ".."，但 FileScopeRule 单独测时，
		// "../escape" 经 filepath.Join 后会落到 scope 之外，应被 FileScopeRule 拦。
		_, err := rule.Check("write_file", map[string]any{"path": "../escape.txt"}, contract)
		if !isPolicyBlock(err) {
			// 在某些 CWD 下 ../escape 可能仍解析到 tmp 的父目录，必在 scope 外
			t.Errorf("expected block for ../escape.txt under scoped rule, got %v", err)
		}
	})

	t.Run("non-file tool passthrough", func(t *testing.T) {
		_, err := rule.Check("run_shell", map[string]any{"command": "ls"}, contract)
		if err != nil {
			t.Errorf("run_shell should pass FileScopeRule, got %v", err)
		}
	})

	t.Run("empty path passthrough", func(t *testing.T) {
		_, err := rule.Check("write_file", map[string]any{"path": ""}, contract)
		if err != nil {
			t.Errorf("empty path should pass (let tool error), got %v", err)
		}
	})

	if rule.Name() != "FileScopeRule" {
		t.Errorf("Name() = %q, want FileScopeRule", rule.Name())
	}
}

// ============================================================================
// ToolWhitelistRule
// ============================================================================

func TestToolWhitelistRule(t *testing.T) {
	rule := &ToolWhitelistRule{}

	cases := []struct {
		name    string
		tool    string
		allowed []string
		wantBlk bool
	}{
		{"empty whitelist allows all", "run_shell", nil, false},
		{"empty slice allows all", "run_shell", []string{}, false},
		{"tool in whitelist", "run_shell", []string{"run_shell", "write_file"}, false},
		{"tool not in whitelist", "delete_file", []string{"run_shell", "write_file"}, true},
		{"single tool whitelist match", "write_file", []string{"write_file"}, false},
		{"single tool whitelist miss", "read_file", []string{"write_file"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			contract := TaskContract{AllowedTools: c.allowed}
			_, err := rule.Check(c.tool, map[string]any{}, contract)
			got := isPolicyBlock(err)
			if got != c.wantBlk {
				t.Errorf("block=%v, want %v (err=%v)", got, c.wantBlk, err)
			}
		})
	}

	if rule.Name() != "ToolWhitelistRule" {
		t.Errorf("Name() = %q, want ToolWhitelistRule", rule.Name())
	}
}

// ============================================================================
// TokenBudgetRule
// ============================================================================

func TestTokenBudgetRule(t *testing.T) {
	rule := &TokenBudgetRule{}

	t.Run("zero budget means unlimited", func(t *testing.T) {
		rule.SetTokenUsage(999999)
		_, err := rule.Check("run_shell", map[string]any{}, TaskContract{TokenBudget: 0})
		if err != nil {
			t.Errorf("TokenBudget=0 should allow, got %v", err)
		}
	})

	t.Run("under budget allows", func(t *testing.T) {
		rule.SetTokenUsage(500)
		_, err := rule.Check("run_shell", map[string]any{}, TaskContract{TokenBudget: 1000})
		if err != nil {
			t.Errorf("under budget should allow, got %v", err)
		}
	})

	t.Run("at budget blocks", func(t *testing.T) {
		rule.SetTokenUsage(1000)
		_, err := rule.Check("run_shell", map[string]any{}, TaskContract{TokenBudget: 1000})
		if !isPolicyBlock(err) {
			t.Errorf("at budget should block, got %v", err)
		}
		if !strings.Contains(blockReason(err), "token budget exceeded") {
			t.Errorf("reason should mention token budget, got %q", blockReason(err))
		}
	})

	t.Run("over budget blocks", func(t *testing.T) {
		rule.SetTokenUsage(1500)
		_, err := rule.Check("run_shell", map[string]any{}, TaskContract{TokenBudget: 1000})
		if !isPolicyBlock(err) {
			t.Errorf("over budget should block, got %v", err)
		}
	})

	t.Run("SetTokenUsage via TokenAware interface", func(t *testing.T) {
		var _ TokenAwareRule = rule // 编译期确认实现 TokenAwareRule
		rule.SetTokenUsage(0)
		_, err := rule.Check("run_shell", map[string]any{}, TaskContract{TokenBudget: 100})
		if err != nil {
			t.Errorf("reset to 0 should allow, got %v", err)
		}
	})

	if rule.Name() != "TokenBudgetRule" {
		t.Errorf("Name() = %q, want TokenBudgetRule", rule.Name())
	}
}

// ============================================================================
// CostBudgetRule
// ============================================================================

func TestCostBudgetRule(t *testing.T) {
	rule := NewCostBudgetRule()

	t.Run("zero budget unlimited", func(t *testing.T) {
		rule.SetCost(999.0)
		_, err := rule.Check("run_shell", map[string]any{}, TaskContract{CostBudgetUSD: 0})
		if err != nil {
			t.Errorf("CostBudgetUSD=0 should allow, got %v", err)
		}
	})

	t.Run("under budget allows", func(t *testing.T) {
		rule.Reset()
		rule.SetCost(0.05)
		_, err := rule.Check("run_shell", map[string]any{}, TaskContract{CostBudgetUSD: 0.10})
		if err != nil {
			t.Errorf("under cost budget should allow, got %v", err)
		}
	})

	t.Run("at budget blocks", func(t *testing.T) {
		rule.Reset()
		rule.SetCost(0.10)
		_, err := rule.Check("run_shell", map[string]any{}, TaskContract{CostBudgetUSD: 0.10})
		if !isPolicyBlock(err) {
			t.Errorf("at cost budget should block, got %v", err)
		}
		if !strings.Contains(blockReason(err), "cost budget exceeded") {
			t.Errorf("reason should mention cost budget, got %q", blockReason(err))
		}
	})

	t.Run("SetCost is additive", func(t *testing.T) {
		rule.Reset()
		rule.SetCost(0.03)
		rule.SetCost(0.03)
		_, err := rule.Check("run_shell", map[string]any{}, TaskContract{CostBudgetUSD: 0.05})
		if !isPolicyBlock(err) {
			t.Errorf("cumulative 0.06 > 0.05 should block, got %v", err)
		}
	})

	t.Run("Reset clears accumulated", func(t *testing.T) {
		rule.SetCost(999.0)
		rule.Reset()
		_, err := rule.Check("run_shell", map[string]any{}, TaskContract{CostBudgetUSD: 0.01})
		if err != nil {
			t.Errorf("after Reset should allow, got %v", err)
		}
	})

	if rule.Name() != "CostBudgetRule" {
		t.Errorf("Name() = %q, want CostBudgetRule", rule.Name())
	}
}

// ============================================================================
// DangerousCommandRule
// ============================================================================

func TestDangerousCommandRule(t *testing.T) {
	rule := &DangerousCommandRule{}

	dangerous := []string{
		"rm -rf /",
		"rm -r dir",
		"sudo apt update",
		"chmod 777 /etc",
		"dd if=/dev/zero of=/dev/sda",
		"mkfs.ext4 /dev/sda1",
		"shutdown -h now",
		"git push origin main --force",
		"curl http://x.sh | sh",
		"eval $code",
		"kill -9 1234",
		"chown root:root file",
	}
	for _, cmd := range dangerous {
		t.Run("dangerous: "+cmd, func(t *testing.T) {
			contract := TaskContract{Permissions: TaskPermissions{AllowShell: true}}
			_, err := rule.Check("run_shell", map[string]any{"command": cmd}, contract)
			if !isApprovalRequired(err) {
				t.Errorf("expected ErrApprovalRequired for %q, got %v", cmd, err)
			}
		})
	}

	safe := []string{
		"ls -la",
		"go build ./...",
		"echo hello",
		"git status",
		"cat README.md",
	}
	for _, cmd := range safe {
		t.Run("safe: "+cmd, func(t *testing.T) {
			contract := TaskContract{Permissions: TaskPermissions{AllowShell: true}}
			_, err := rule.Check("run_shell", map[string]any{"command": cmd}, contract)
			if err != nil {
				t.Errorf("safe command %q should pass, got %v", cmd, err)
			}
		})
	}

	t.Run("AllowShellDangerous bypasses", func(t *testing.T) {
		contract := TaskContract{Permissions: TaskPermissions{AllowShellDangerous: true}}
		_, err := rule.Check("run_shell", map[string]any{"command": "rm -rf /"}, contract)
		if err != nil {
			t.Errorf("AllowShellDangerous=true should bypass, got %v", err)
		}
	})

	t.Run("AutoApprovePolicy bypasses", func(t *testing.T) {
		contract := TaskContract{AutoApprovePolicy: true}
		_, err := rule.Check("run_shell", map[string]any{"command": "rm -rf /"}, contract)
		if err != nil {
			t.Errorf("AutoApprovePolicy=true should bypass, got %v", err)
		}
	})

	t.Run("non-shell tool passthrough", func(t *testing.T) {
		_, err := rule.Check("write_file", map[string]any{"path": "x"}, TaskContract{})
		if err != nil {
			t.Errorf("non-shell tool should pass, got %v", err)
		}
	})

	t.Run("empty command passthrough", func(t *testing.T) {
		_, err := rule.Check("run_shell", map[string]any{"command": ""}, TaskContract{})
		if err != nil {
			t.Errorf("empty command should pass, got %v", err)
		}
	})

	if rule.Name() != "DangerousCommandRule" {
		t.Errorf("Name() = %q, want DangerousCommandRule", rule.Name())
	}
}

// ============================================================================
// ApprovalRule
// ============================================================================

func TestApprovalRule(t *testing.T) {
	// handler 为 nil 也能检测，只是无法等待决定；Check 仍返回 ErrApprovalRequired
	rule := NewApprovalRule(nil)

	t.Run("high-risk shell requires approval", func(t *testing.T) {
		_, err := rule.Check("run_shell", map[string]any{"command": "rm -rf /"}, TaskContract{})
		if !isApprovalRequired(err) {
			t.Errorf("expected ErrApprovalRequired for rm -rf, got %v", err)
		}
	})

	t.Run("high-risk file path requires approval", func(t *testing.T) {
		_, err := rule.Check("write_file", map[string]any{"path": "/etc/passwd"}, TaskContract{})
		if !isApprovalRequired(err) {
			t.Errorf("expected ErrApprovalRequired for /etc/passwd write, got %v", err)
		}
	})

	t.Run("delete_file always requires approval", func(t *testing.T) {
		_, err := rule.Check("delete_file", map[string]any{}, TaskContract{})
		if !isApprovalRequired(err) {
			t.Errorf("expected ErrApprovalRequired for delete_file, got %v", err)
		}
	})

	t.Run("safe shell command passes", func(t *testing.T) {
		_, err := rule.Check("run_shell", map[string]any{"command": "ls -la"}, TaskContract{})
		if err != nil {
			t.Errorf("safe command should pass, got %v", err)
		}
	})

	t.Run("safe file path passes", func(t *testing.T) {
		_, err := rule.Check("write_file", map[string]any{"path": "./local.txt"}, TaskContract{})
		if err != nil {
			t.Errorf("safe path should pass, got %v", err)
		}
	})

	t.Run("relative ./etc/ path does NOT trigger high-risk approval", func(t *testing.T) {
		// 修复（TEST_REPORT 低危项）：原 isHighRiskFilePath 用 strings.Contains
		// 匹配 "/etc/"，"./etc/x" 不含 "/etc/" 子串可绕过审批。修复后用
		// HasPrefix 比较，"./etc/x" 不应被判定为高风险系统路径。
		_, err := rule.Check("write_file", map[string]any{"path": "./etc/policy_approval_test.txt"}, TaskContract{})
		if err != nil {
			t.Errorf("relative ./etc/ path should NOT trigger approval, got %v", err)
		}
	})

	t.Run("absolute /etc/ path DOES trigger high-risk approval", func(t *testing.T) {
		_, err := rule.Check("write_file", map[string]any{"path": "/etc/passwd"}, TaskContract{})
		if !isApprovalRequired(err) {
			t.Errorf("absolute /etc/passwd should trigger approval, got %v", err)
		}
	})

	t.Run("RequiresApproval public API", func(t *testing.T) {
		if !rule.RequiresApproval("run_shell", map[string]any{"command": "sudo x"}) {
			t.Error("RequiresApproval should return true for sudo")
		}
		if rule.RequiresApproval("run_shell", map[string]any{"command": "ls"}) {
			t.Error("RequiresApproval should return false for ls")
		}
		if !rule.RequiresApproval("delete_file", map[string]any{}) {
			t.Error("RequiresApproval should return true for delete_file")
		}
	})

	if rule.Name() != "ApprovalRule" {
		t.Errorf("Name() = %q, want ApprovalRule", rule.Name())
	}
}

// ============================================================================
// PolicyChain — 顺序执行与短路
// ============================================================================

// allowAllRule 是一个总是放行且不改 input 的测试用 rule。
type allowAllRule struct{ name string }

func (r *allowAllRule) Name() string { return r.name }
func (r *allowAllRule) Check(tool string, input map[string]any, _ TaskContract) (map[string]any, error) {
	return input, nil
}

// denyRule 总是拦截。
type denyRule struct{ name, reason string }

func (r *denyRule) Name() string { return r.name }
func (r *denyRule) Check(tool string, input map[string]any, _ TaskContract) (map[string]any, error) {
	return input, &ErrBlockedByPolicy{Rule: r.name, Reason: r.reason, Tool: tool}
}

// mutatingRule 给 input 注入一个字段，用于验证链式传递。
type mutatingRule struct{ name, key, val string }

func (r *mutatingRule) Name() string { return r.name }
func (r *mutatingRule) Check(_ string, input map[string]any, _ TaskContract) (map[string]any, error) {
	out := make(map[string]any, len(input)+1)
	for k, v := range input {
		out[k] = v
	}
	out[r.key] = r.val
	return out, nil
}

func TestPolicyChainOrderAndShortCircuit(t *testing.T) {
	t.Run("all allow passes", func(t *testing.T) {
		chain := NewPolicyChain(&allowAllRule{"a"}, &allowAllRule{"b"})
		_, err := chain.Check("run_shell", map[string]any{"command": "ls"}, TaskContract{})
		if err != nil {
			t.Errorf("all-allow chain should pass, got %v", err)
		}
	})

	t.Run("deny short-circuits", func(t *testing.T) {
		chain := NewPolicyChain(&allowAllRule{"a"}, &denyRule{"deny1", "no"}, &allowAllRule{"b"})
		_, err := chain.Check("run_shell", map[string]any{}, TaskContract{})
		if !isPolicyBlock(err) {
			t.Errorf("expected block, got %v", err)
		}
		var p *ErrBlockedByPolicy
		if errors.As(err, &p) && p.Rule != "deny1" {
			t.Errorf("expected deny1 to block, got rule=%q", p.Rule)
		}
	})

	t.Run("first deny wins", func(t *testing.T) {
		chain := NewPolicyChain(&denyRule{"deny1", "first"}, &denyRule{"deny2", "second"})
		_, err := chain.Check("run_shell", map[string]any{}, TaskContract{})
		var p *ErrBlockedByPolicy
		if !errors.As(err, &p) || p.Rule != "deny1" {
			t.Errorf("expected deny1 to win, got %v", err)
		}
	})

	t.Run("mutation propagates downstream", func(t *testing.T) {
		chain := NewPolicyChain(
			&mutatingRule{"add-tag", "tag", "injected"},
			&captureRule{t: t, wantKey: "tag", wantVal: "injected"},
		)
		_, err := chain.Check("run_shell", map[string]any{"command": "ls"}, TaskContract{})
		if err != nil {
			t.Errorf("chain with mutation should pass, got %v", err)
		}
	})

	t.Run("empty chain passes", func(t *testing.T) {
		chain := NewPolicyChain()
		out, err := chain.Check("run_shell", map[string]any{"command": "ls"}, TaskContract{})
		if err != nil || out["command"] != "ls" {
			t.Errorf("empty chain should pass input through, got out=%v err=%v", out, err)
		}
	})

	t.Run("AddRule appends", func(t *testing.T) {
		chain := NewPolicyChain()
		chain.AddRule(&denyRule{"deny", "x"})
		_, err := chain.Check("run_shell", map[string]any{}, TaskContract{})
		if !isPolicyBlock(err) {
			t.Errorf("added rule should block, got %v", err)
		}
	})
}

// captureRule 验证上游 mutation 是否到达：Check 时断言 input 含指定键值，再放行。
type captureRule struct {
	t       *testing.T
	wantKey string
	wantVal string
}

func (r *captureRule) Name() string { return "capture" }
func (r *captureRule) Check(_ string, input map[string]any, _ TaskContract) (map[string]any, error) {
	v, ok := input[r.wantKey]
	if !ok || v != r.wantVal {
		r.t.Errorf("downstream rule did not see mutation %q=%q, got input=%v", r.wantKey, r.wantVal, input)
	}
	return input, nil
}

// ============================================================================
// PolicyGate — contract 注入 + executor 回调
// ============================================================================

func TestPolicyGateExecute(t *testing.T) {
	t.Run("nil chain defaults to permissive", func(t *testing.T) {
		gate := NewPolicyGate(nil, TaskContract{})
		called := false
		res, err := gate.Execute("run_shell", map[string]any{"command": "ls"}, func(in map[string]any) (any, error) {
			called = true
			return "ok", nil
		})
		if err != nil || !called || res != "ok" {
			t.Errorf("nil chain should execute, got res=%v err=%v called=%v", res, err, called)
		}
	})

	t.Run("block prevents executor", func(t *testing.T) {
		gate := NewPolicyGate(NewPolicyChain(&denyRule{"deny", "no"}), TaskContract{})
		called := false
		_, err := gate.Execute("run_shell", map[string]any{}, func(in map[string]any) (any, error) {
			called = true
			return nil, nil
		})
		if !isPolicyBlock(err) {
			t.Errorf("expected block, got %v", err)
		}
		if called {
			t.Error("executor must not be called when policy blocks")
		}
	})

	t.Run("contract is read by rules", func(t *testing.T) {
		// ToolWhitelistRule 读取 contract.AllowedTools
		contract := TaskContract{AllowedTools: []string{"run_shell"}}
		gate := NewPolicyGate(NewPolicyChain(&ToolWhitelistRule{}), contract)
		// 白名单内：执行
		if _, err := gate.Execute("run_shell", map[string]any{}, func(in map[string]any) (any, error) { return nil, nil }); err != nil {
			t.Errorf("whitelisted tool should execute, got %v", err)
		}
		// 白名单外：拦截
		_, err := gate.Execute("delete_file", map[string]any{}, func(in map[string]any) (any, error) { return nil, nil })
		if !isPolicyBlock(err) {
			t.Errorf("non-whitelisted tool should block, got %v", err)
		}
	})

	t.Run("mutation reaches executor", func(t *testing.T) {
		gate := NewPolicyGate(NewPolicyChain(&mutatingRule{"norm", "path", "/abs/x"}), TaskContract{})
		var seen map[string]any
		gate.Execute("write_file", map[string]any{"path": "x"}, func(in map[string]any) (any, error) {
			seen = in
			return nil, nil
		})
		if seen["path"] != "/abs/x" {
			t.Errorf("executor should see mutated input, got %v", seen)
		}
	})

	t.Run("Contract accessor", func(t *testing.T) {
		c := TaskContract{Goal: "g", MaxSteps: 5}
		gate := NewPolicyGate(nil, c)
		if gate.Contract().Goal != "g" || gate.Contract().MaxSteps != 5 {
			t.Errorf("Contract() did not return the configured contract")
		}
	})

	t.Run("SetTokenUsage propagates to TokenAware rules", func(t *testing.T) {
		tbr := &TokenBudgetRule{}
		gate := NewPolicyGate(NewPolicyChain(tbr), TaskContract{})
		gate.SetTokenUsage(42)
		if tbr.totalTokens != 42 {
			t.Errorf("SetTokenUsage should propagate, got %d", tbr.totalTokens)
		}
	})
}

// ============================================================================
// DefaultContract — 冒烟确认默认契约字段
// ============================================================================

func TestDefaultContract(t *testing.T) {
	c := DefaultContract("do something")
	if c.Goal != "do something" {
		t.Errorf("Goal = %q", c.Goal)
	}
	if c.Scope != "." {
		t.Errorf("Scope = %q, want '.'", c.Scope)
	}
	if c.MaxSteps != 30 {
		t.Errorf("MaxSteps = %d, want 30", c.MaxSteps)
	}
	if !c.Permissions.AllowFileWrite || !c.Permissions.AllowShell {
		t.Errorf("default permissions should allow file write + shell, got %+v", c.Permissions)
	}
	if c.Permissions.AllowShellDangerous {
		t.Error("default should NOT allow dangerous shell")
	}
	if c.TimeoutSeconds != 0 {
		t.Errorf("TimeoutSeconds = %d, want 0 (unlimited)", c.TimeoutSeconds)
	}
}

// ============================================================================
// ErrBlockedByPolicy / ErrApprovalRequired — error 类型确认
// ============================================================================

func TestPolicyErrors(t *testing.T) {
	t.Run("ErrBlockedByPolicy message", func(t *testing.T) {
		e := &ErrBlockedByPolicy{Rule: "R", Tool: "T", Reason: "because"}
		if !strings.Contains(e.Error(), "R") || !strings.Contains(e.Error(), "T") || !strings.Contains(e.Error(), "because") {
			t.Errorf("Error() = %q, missing fields", e.Error())
		}
	})

	t.Run("errors.As recognizes block in chain", func(t *testing.T) {
		chain := NewPolicyChain(&denyRule{"d", "r"})
		_, err := chain.Check("run_shell", map[string]any{}, TaskContract{})
		var p *ErrBlockedByPolicy
		if !errors.As(err, &p) {
			t.Errorf("errors.As should match ErrBlockedByPolicy, got %T", err)
		}
	})
}
