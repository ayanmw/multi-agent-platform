package tool

import (
	"os/exec"
	"strings"
	"testing"
)

func TestExecuteProgramPython(t *testing.T) {
	if _, err := exec.LookPath("python"); err != nil {
		if _, err2 := exec.LookPath("python3"); err2 != nil {
			t.Skip("python not installed")
		}
	}
	r := NewRegistry()
	RegisterBuiltins(r)
	res, err := r.Execute("core/execute_program", map[string]any{"language": "python", "code": "print('hi')"})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)
	if !strings.Contains(out["stdout"].(string), "hi") {
		t.Fatalf("unexpected output: %v", out["stdout"])
	}
}

func TestExecuteProgramBash(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	res, err := r.Execute("core/execute_program", map[string]any{"language": "bash", "code": "echo ok"})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)
	if !strings.Contains(out["stdout"].(string), "ok") {
		t.Fatalf("unexpected output: %v", out["stdout"])
	}
}

func TestExecuteProgramUnsupported(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	_, err := r.Execute("core/execute_program", map[string]any{"language": "go", "code": "fmt.Println(1)"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExecuteProgramTimeout(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash 不可用，跳过超时测试")
	}
	// `sleep` 是外部命令（Git Bash / Linux 均自带）。某些受限沙箱会把 PATH
	// 收窄成白名单 shim，此时 `sleep` 缺失会让脚本以 exit 127 立即退出，
	// 超时逻辑得不到验证 —— 那种环境下直接跳过，而不是弱化测试本身。
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep 不可用（PATH 受限环境），跳过超时测试")
	}
	r := NewRegistry()
	RegisterBuiltins(r)
	res, err := r.Execute("core/execute_program", map[string]any{"language": "bash", "code": "sleep 5", "timeout_ms": 100})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)
	if !out["timed_out"].(bool) {
		t.Fatal("expected timed_out")
	}
}
