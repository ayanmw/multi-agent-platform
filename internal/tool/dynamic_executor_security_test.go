package tool

import (
	"runtime"
	"strings"
	"testing"
)

// TestDynamicExecutorShellNoShellInjection 验证 shell 类型 dynamic tool 使用
// program+args 直接执行，参数中的 shell metacharacter 不会被解释。
func TestDynamicExecutorShellNoShellInjection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh on windows")
	}

	dt := NewDynamicToolFromDescriptor(ToolDescriptor{
		Name:        "exec-shell-injection",
		Description: "test",
		Source:      ToolSourceLocalDB,
		ExecutionConfig: map[string]any{
			"type":    "shell",
			"command": "echo {name}",
		},
	})

	// 传入含分号的恶意输入；旧实现会把 "; rm -rf /" 交给 sh 解释，导致执行 rm。
	// 新实现会把整个字符串作为 echo 的单个参数输出。
	res, err := dt.Execute(map[string]any{"name": "; echo injected"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	stdout := strings.TrimSpace(res.(map[string]any)["stdout"].(string))
	if stdout != "; echo injected" {
		t.Fatalf("stdout = %q, want literal parameter", stdout)
	}
	if res.(map[string]any)["exit_code"] != 0 {
		t.Fatalf("exit_code = %v, want 0", res.(map[string]any)["exit_code"])
	}
}

// TestDynamicExecutorShellRejectsMetacharacters 验证含 shell metacharacter
// 的模板在没有 shell:true 标志时会被拒绝。
func TestDynamicExecutorShellRejectsMetacharacters(t *testing.T) {
	tests := []string{
		"echo {name} | cat",
		"cmd1 && cmd2",
		"cmd1; cmd2",
		"echo $(id)",
		"echo `id`",
		"cat > file",
	}
	for _, cmd := range tests {
		dt := NewDynamicToolFromDescriptor(ToolDescriptor{
			Name:        "exec-shell-meta",
			Description: "test",
			Source:      ToolSourceLocalDB,
			ExecutionConfig: map[string]any{
				"type":    "shell",
				"command": cmd,
			},
		})
		_, err := dt.Execute(nil)
		if err == nil || !strings.Contains(err.Error(), "complex shell syntax is not supported") {
			t.Fatalf("command %q: expected shell syntax rejection, got %v", cmd, err)
		}
	}
}

// TestDynamicExecutorShellMissingPlaceholder 验证缺失的 {param} 不会被替换，
// 命令仍按原样执行（不会报错）。
func TestDynamicExecutorShellMissingPlaceholder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh on windows")
	}

	dt := NewDynamicToolFromDescriptor(ToolDescriptor{
		Name:        "exec-shell-missing",
		Description: "test",
		Source:      ToolSourceLocalDB,
		ExecutionConfig: map[string]any{
			"type":    "shell",
			"command": "echo {missing}",
		},
	})
	res, err := dt.Execute(map[string]any{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	stdout := strings.TrimSpace(res.(map[string]any)["stdout"].(string))
	if stdout != "{missing}" {
		t.Fatalf("stdout = %q, want {missing}", stdout)
	}
}
