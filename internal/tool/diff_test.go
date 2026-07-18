package tool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyDiffSimple(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "a.txt")
	_ = os.WriteFile(f, []byte("hello\nworld\n"), 0644)

	r := NewRegistry()
	RegisterBuiltins(r)
	res, err := r.Execute("core/apply_diff", map[string]any{
		"path": f,
		"diffs": []any{
			map[string]any{"old_string": "world", "new_string": "Go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(f)
	if string(got) != "hello\nGo\n" {
		t.Fatalf("unexpected content: %s", got)
	}
	if res.(map[string]any)["replacements"].(int) != 1 {
		t.Fatalf("expected 1 replacement")
	}
}

func TestApplyDiffByLineRange(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "a.txt")
	_ = os.WriteFile(f, []byte("a\nb\nc\n"), 0644)

	r := NewRegistry()
	RegisterBuiltins(r)
	_, err := r.Execute("core/apply_diff", map[string]any{
		"path": f,
		"diffs": []any{
			map[string]any{"line_start": 2, "line_end": 2, "new_string": "B\n"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(f)
	if string(got) != "a\nB\nc\n" {
		t.Fatalf("unexpected content: %s", got)
	}
}

func TestApplyDiffPathTraversal(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	_, err := r.Execute("core/apply_diff", map[string]any{
		"path":  "../x.txt",
		"diffs": []any{map[string]any{"old_string": "x", "new_string": "y"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestApplyDiffCreateIfMissing(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "new.txt")

	r := NewRegistry()
	RegisterBuiltins(r)
	_, err := r.Execute("core/apply_diff", map[string]any{
		"path":              f,
		"diffs":             []any{map[string]any{"new_string": "hello"}},
		"create_if_missing": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(f)
	if string(got) != "hello" {
		t.Fatalf("unexpected content: %s", got)
	}
}
