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
