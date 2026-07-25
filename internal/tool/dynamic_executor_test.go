package tool

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDynamicExecutorShell 验证 shell 类型 dynamic tool 可以正常执行，
// 并且返回 stdout/exit_code 等结构化结果。
func TestDynamicExecutorShell(t *testing.T) {
	// Windows 无 sh，跳过真实 shell 执行。
	if runtime.GOOS == "windows" {
		t.Skip("no sh on windows")
	}

	dt := NewDynamicToolFromDescriptor(ToolDescriptor{
		Name:        "exec-shell-test",
		Description: "test",
		Source:      ToolSourceLocalDB,
		ExecutionConfig: map[string]any{
			"type":    "shell",
			"command": "echo {msg}",
		},
	})

	res, err := dt.Execute(map[string]any{"msg": "hello"})
	if err != nil {
		t.Fatalf("execute shell: %v", err)
	}
	result := res.(map[string]any)
	stdout := strings.TrimSpace(result["stdout"].(string))
	if stdout != "hello" {
		t.Fatalf("stdout = %q, want hello", stdout)
	}
	if result["exit_code"] != 0 {
		t.Fatalf("exit_code = %v, want 0", result["exit_code"])
	}
}

// TestDynamicExecutorShellWithWorkdir 验证 ExecuteContext.Workdir 会被用作 shell 的 CWD。
// 这是 worktree 隔离场景下 shell 工具能够落到正确目录的关键。
func TestDynamicExecutorShellWithWorkdir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh on windows")
	}

	dir := t.TempDir()
	dt := NewDynamicToolFromDescriptor(ToolDescriptor{
		Name:        "exec-shell-workdir",
		Description: "test",
		Source:      ToolSourceLocalDB,
		ExecutionConfig: map[string]any{
			"type":    "shell",
			"command": "pwd > out.txt && cat out.txt",
		},
	})

	res, err := dt.ExecuteWithCtx(ExecuteContext{Workdir: dir}, nil)
	if err != nil {
		t.Fatalf("execute with workdir: %v", err)
	}
	result := res.(map[string]any)
	stdout := strings.TrimSpace(result["stdout"].(string))
	if stdout != dir {
		t.Fatalf("shell CWD = %q, want %q", stdout, dir)
	}

	// 同时验证文件确实落在 workdir 下。
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); err != nil {
		t.Fatalf("expected out.txt in workdir: %v", err)
	}
}

// TestDynamicExecutorShellInputSubstitution 验证模板中的多个 {param} 会被替换。
func TestDynamicExecutorShellInputSubstitution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh on windows")
	}

	dt := NewDynamicToolFromDescriptor(ToolDescriptor{
		Name:        "exec-shell-subst",
		Description: "test",
		Source:      ToolSourceLocalDB,
		ExecutionConfig: map[string]any{
			"type":    "shell",
			"command": "printf '%s|%s' {a} {b}",
		},
	})

	res, err := dt.Execute(map[string]any{"a": "x", "b": "y"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	stdout := strings.TrimSpace(res.(map[string]any)["stdout"].(string))
	if stdout != "x|y" {
		t.Fatalf("stdout = %q, want x|y", stdout)
	}
}

// TestDynamicExecutorHTTP 验证 http 类型 dynamic tool 会向模板 URL 发起请求。
func TestDynamicExecutorHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "method=%s path=%s q=%s", r.Method, r.URL.Path, r.URL.Query().Get("q"))
	}))
	defer server.Close()

	dt := NewDynamicToolFromDescriptor(ToolDescriptor{
		Name:        "exec-http-test",
		Description: "test",
		Source:      ToolSourceLocalDB,
		ExecutionConfig: map[string]any{
			"type":   "http",
			"url":    server.URL + "/search?q={q}",
			"method": "GET",
		},
	})

	res, err := dt.Execute(map[string]any{"q": "go"})
	if err != nil {
		t.Fatalf("execute http: %v", err)
	}
	result := res.(map[string]any)
	body := result["body"].(string)
	if !strings.Contains(body, "method=GET") || !strings.Contains(body, "q=go") {
		t.Fatalf("unexpected body: %s", body)
	}
	if result["status_code"] != 200 {
		t.Fatalf("status_code = %v, want 200", result["status_code"])
	}
	if !strings.HasPrefix(result["url"].(string), server.URL) {
		t.Fatalf("url = %q, want prefix %s", result["url"], server.URL)
	}
}

// TestDynamicExecutorInline 验证 inline 类型返回占位结果，且保留输入。
func TestDynamicExecutorInline(t *testing.T) {
	dt := NewDynamicToolFromDescriptor(ToolDescriptor{
		Name:        "exec-inline-test",
		Description: "test",
		Source:      ToolSourceLocalDB,
		ExecutionConfig: map[string]any{
			"type": "inline",
			"code": "return input.x + 1",
		},
	})

	res, err := dt.Execute(map[string]any{"x": 42})
	if err != nil {
		t.Fatalf("execute inline: %v", err)
	}
	result := res.(map[string]any)
	if result["code"] != "return input.x + 1" {
		t.Fatalf("code = %q", result["code"])
	}
	msg := result["message"].(string)
	if !strings.Contains(msg, "not yet implemented") {
		t.Fatalf("unexpected message: %v", msg)
	}
}

// TestDynamicExecutorUnknownType 验证未识别 type 时返回错误。
func TestDynamicExecutorUnknownType(t *testing.T) {
	dt := NewDynamicToolFromDescriptor(ToolDescriptor{
		Name:        "exec-unknown",
		Description: "test",
		Source:      ToolSourceLocalDB,
		ExecutionConfig: map[string]any{
			"type": "wasm",
		},
	})

	_, err := dt.Execute(nil)
	if err == nil || !strings.Contains(err.Error(), "unknown dynamic tool type") {
		t.Fatalf("expected unknown type error, got: %v", err)
	}
}
