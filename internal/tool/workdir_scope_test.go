package tool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateWorkdirScope(t *testing.T) {
	workspace, err := os.MkdirTemp("", "run-shell-scope-*")
	if err != nil {
		t.Fatalf("create temp workspace: %v", err)
	}
	defer os.RemoveAll(workspace)

	worktree := filepath.Join(workspace, "worktree-01")
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	nested := filepath.Join(worktree, "nested")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("create nested: %v", err)
	}

	tests := []struct {
		name        string
		target      string
		ctxWorkdir  string
		sessionWorkdir string
		wantErr     bool
	}{
		{"exactly session workspace", workspace, workspace, workspace, false},
		{"nested under session workspace", nested, workspace, workspace, false},
		{"exactly worktree", worktree, worktree, workspace, false},
		{"nested under worktree", nested, worktree, workspace, false},
		{"outside both", `C:\\Windows`, workspace, workspace, true},
		{"path traversal sibling", filepath.Join(workspace, "..", "other"), workspace, workspace, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := map[string]any{"workdir": tt.sessionWorkdir}
			err := validateWorkdirScope(tt.target, tt.ctxWorkdir, input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestIsSubPath(t *testing.T) {
	tests := []struct {
		child  string
		parent string
		want   bool
	}{
		{"/a/b", "/a", true},
		{"/a/b/c", "/a/b", true},
		{"/a", "/a", true},
		{"/ab", "/a", false},
		{"/a/b", "/c", false},
	}
	for _, tt := range tests {
		if got := isSubPath(tt.child, tt.parent); got != tt.want {
			t.Errorf("isSubPath(%q, %q) = %v, want %v", tt.child, tt.parent, got, tt.want)
		}
	}
}
