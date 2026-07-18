package tool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteFile(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "del.txt")
	_ = os.WriteFile(f, []byte("x"), 0644)

	r := NewRegistry()
	RegisterBuiltins(r)
	res, err := r.Execute("core/delete_file", map[string]any{"path": f})
	if err != nil {
		t.Fatal(err)
	}
	if !res.(map[string]any)["success"].(bool) {
		t.Fatal("delete failed")
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("file still exists")
	}
}

func TestDeleteFileRecursive(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "nested")
	_ = os.MkdirAll(filepath.Join(dir, "inner"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "inner", "x.txt"), []byte("x"), 0644)

	r := NewRegistry()
	RegisterBuiltins(r)
	_, err := r.Execute("core/delete_file", map[string]any{"path": dir, "recursive": true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("directory still exists")
	}
}

func TestDeleteFilePathTraversal(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	_, err := r.Execute("core/delete_file", map[string]any{"path": "../outside.txt"})
	if err == nil {
		t.Fatal("expected error")
	}
}
