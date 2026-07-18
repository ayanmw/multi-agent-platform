package tool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListDir(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	tmp := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("hi"), 0644)
	_ = os.Mkdir(filepath.Join(tmp, "sub"), 0755)

	res, err := r.Execute("core/list_dir", map[string]any{"path": tmp})
	if err != nil {
		t.Fatal(err)
	}
	out, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", res)
	}
	if out["path"].(string) != tmp {
		t.Fatalf("wrong path: %v", out["path"])
	}
	if out["total"].(int) != 2 {
		t.Fatalf("expected 2 entries, got %v\nentries=%v", out["total"], out["entries"])
	}
}

func TestListDirRecursive(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "sub")
	_ = os.Mkdir(sub, 0755)
	_ = os.WriteFile(filepath.Join(sub, "b.txt"), []byte("b"), 0644)

	res, err := r.Execute("core/list_dir", map[string]any{"path": tmp, "recursive": true})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)
	if out["total"].(int) < 2 {
		t.Fatalf("expected >=2 entries, got %v", out["total"])
	}
}

func TestListDirPathTraversalSafety(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	_, err := r.Execute("core/list_dir", map[string]any{"path": "../outside"})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}
