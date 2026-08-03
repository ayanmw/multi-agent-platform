package tool

import (
	"strings"
	"testing"
)

// TestRunShellDeniedBySandbox 验证无 Docker 路径下 run_shell 对灾难性命令的
// 拦截：命中黑名单且默认 deny 策略时，命令不真正执行，返回 blocked 结果。
func TestRunShellDeniedBySandbox(t *testing.T) {
	res, err := NewRunShellTool().Execute(map[string]any{"command": "rm -rf /"})
	if err != nil {
		t.Fatalf("Execute should not return error on sandbox denial: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %T", res)
	}
	if blocked, _ := m["blocked"].(bool); !blocked {
		t.Fatalf("expected blocked=true for rm -rf /, got %v", m)
	}
	if ec, _ := m["exit_code"].(int); ec != -1 {
		t.Fatalf("expected exit_code=-1 for blocked command, got %v", ec)
	}
	if pol, _ := m["policy"].(string); pol != "deny" {
		t.Fatalf("expected policy=deny, got %v", pol)
	}
}

// TestRunShellBenignNotBlocked 验证良性命令在 deny 策略下不被误伤。
// 不依赖具体 shell 输出，只断言策略未拦截（结果中不含 blocked 标记）。
func TestRunShellBenignNotBlocked(t *testing.T) {
	res, err := NewRunShellTool().Execute(map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %T", res)
	}
	if blocked, _ := m["blocked"].(bool); blocked {
		t.Fatalf("benign echo must not be blocked, got %v", m)
	}
}

// TestExecuteProgramDeniedBySandbox 验证 execute_program 在 deny 策略下拦截
// 危险代码（checkDangerousCode 静态检查 + 黑名单双重门）。无需真实解释器。
func TestExecuteProgramDeniedBySandbox(t *testing.T) {
	// 命中 checkDangerousCode：os.system 静态检查。
	_, err := NewExecuteProgramTool().Execute(map[string]any{
		"language": "python",
		"code":     "os.system('rm -rf /')",
	})
	if err == nil {
		t.Fatal("expected error for dangerous os.system code under deny policy")
	}

	// 命中 Shell 沙箱黑名单但 checkDangerousCode 未覆盖：git push --force。
	_, err = NewExecuteProgramTool().Execute(map[string]any{
		"language": "bash",
		"code":     "git push --force origin main",
	})
	if err == nil {
		t.Fatal("expected error for git push --force under deny policy")
	}
}

// TestExecuteProgramAllowOverridesRisk 验证 allow 策略下既有的静态危险检查
// 被放开（不再返回 risk 错误）。实际解释执行可能仍因无解释器而失败，但那不是
// 安全策略拦截，故只断言不是 risk/sandbox 拒绝错误。
func TestExecuteProgramAllowOverridesRisk(t *testing.T) {
	_, err := NewExecuteProgramTool().WithShellSandbox(
		NewShellSandboxConfig(PolicyAllow, defaultShellBlacklist, nil),
	).Execute(map[string]any{
		"language": "python",
		"code":     "os.system('ls')",
	})
	if err != nil && (strings.Contains(err.Error(), "risk pattern detected") || strings.Contains(err.Error(), "blocked by shell sandbox")) {
		t.Fatalf("allow policy must not block dangerous code: %v", err)
	}
}
