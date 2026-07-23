// workdir_ctx_test.go — 验证 ExecuteContext.Workdir（worktree 隔离注入）的优先级：
// LLM 传入的 input["workdir"] 被 ctx.Workdir 覆盖，相对路径解析到 holder 指向的目录。
package tool

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteFileCtxWorkdirOverridesInput 验证 ExecuteContext.Workdir 优先于 input["workdir"]。
// 这是 worktree 隔离防逃逸的关键：LLM 伪造 workdir 也无法让文件写到 worktree 之外。
func TestWriteFileCtxWorkdirOverridesInput(t *testing.T) {
	holderDir := t.TempDir()   // worktree holder 指向（可信源）
	fakeDir := t.TempDir()     // LLM 伪造的 workdir（应被忽略）

	tool := NewWriteFileTool()
	// LLM 传 workdir=fakeDir，但 ctx.Workdir=holderDir → 文件应落 holderDir。
	res, err := tool.ExecuteWithCtx(ExecuteContext{Workdir: holderDir}, map[string]any{
		"path":    "out.txt",
		"content": "x",
		"workdir": fakeDir, // LLM 伪造，应被忽略
	})
	if err != nil {
		t.Fatalf("ExecuteWithCtx: %v", err)
	}
	out := res.(map[string]any)
	gotPath := out["path"].(string)
	wantPath := filepath.Join(holderDir, "out.txt")
	if gotPath != wantPath {
		t.Fatalf("path = %q, want %q (LLM workdir should be ignored)", gotPath, wantPath)
	}
	// 文件确实落在 holderDir，而非 fakeDir。
	if _, err := os.Stat(filepath.Join(holderDir, "out.txt")); err != nil {
		t.Fatalf("file not in holder dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fakeDir, "out.txt")); !os.IsNotExist(err) {
		t.Fatalf("file leaked into fake (LLM) workdir — workdir escape!")
	}
}

// TestWriteFileFallsBackToInputWorkdir 验证无 ctx.Workdir 时回退 input["workdir"]（向后兼容）。
func TestWriteFileFallsBackToInputWorkdir(t *testing.T) {
	wd := t.TempDir()
	tool := NewWriteFileTool()
	res, err := tool.ExecuteWithCtx(ExecuteContext{}, map[string]any{
		"path":    "fallback.txt",
		"content": "y",
		"workdir": wd,
	})
	if err != nil {
		t.Fatalf("ExecuteWithCtx: %v", err)
	}
	out := res.(map[string]any)
	if got, want := out["path"].(string), filepath.Join(wd, "fallback.txt"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

// TestReadFileCtxWorkdir 验证 read_file 同样优先用 ctx.Workdir。
func TestReadFileCtxWorkdir(t *testing.T) {
	holderDir := t.TempDir()
	fakeDir := t.TempDir()
	// 真文件在 holderDir。
	if err := os.WriteFile(filepath.Join(holderDir, "real.txt"), []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	// fakeDir 放一个同名但内容不同的诱饵文件。
	if err := os.WriteFile(filepath.Join(fakeDir, "real.txt"), []byte("decoy"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool()
	res, err := tool.ExecuteWithCtx(ExecuteContext{Workdir: holderDir}, map[string]any{
		"path":    "real.txt",
		"workdir": fakeDir,
	})
	if err != nil {
		t.Fatalf("ExecuteWithCtx: %v", err)
	}
	out := res.(map[string]any)
	if out["content"].(string) != "payload" {
		t.Fatalf("content = %q, want %q (should read holder dir, ignoring LLM workdir)", out["content"], "payload")
	}
}

// TestRunShellCtxWorkdir 验证 run_shell 的 CWD 优先用 ctx.Workdir。
func TestRunShellCtxWorkdir(t *testing.T) {
	holderDir := t.TempDir()
	fakeDir := t.TempDir()
	// 在两个目录各放一个同名 marker 文件，用 ls 区分当前目录。
	if err := os.WriteFile(filepath.Join(holderDir, "HOLDER_MARKER"), []byte("h"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeDir, "FAKE_MARKER"), []byte("f"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewRunShellTool()
	res, err := tool.ExecuteWithCtx(ExecuteContext{Workdir: holderDir}, map[string]any{
		"command": "ls HOLDER_MARKER",
		"workdir": fakeDir,
	})
	if err != nil {
		t.Fatalf("ExecuteWithCtx: %v", err)
	}
	out := res.(map[string]any)
	// 应在 holderDir 下执行 → ls HOLDER_MARKER 成功（exit_code 0）。
	if ec, _ := out["exit_code"].(int); ec != 0 {
		stdout, _ := out["stdout"].(string)
		stderr, _ := out["stderr"].(string)
		t.Fatalf("run_shell should run in holder dir (HOLDER_MARKER exists there); exit=%v stdout=%q stderr=%q",
			ec, stdout, stderr)
	}
}

// TestPathTraversalStillBlockedInWorktree 验证即使 workdir 指向 worktree，
// ".." 路径仍被拒（path-traversal 防护作为第二道）。
func TestPathTraversalStillBlockedInWorktree(t *testing.T) {
	holderDir := t.TempDir()
	tool := NewWriteFileTool()
	_, err := tool.ExecuteWithCtx(ExecuteContext{Workdir: holderDir}, map[string]any{
		"path":    "../../../etc/evil.txt",
		"content": "x",
	})
	if err == nil {
		t.Fatalf("expected path traversal error, got nil")
	}
}
