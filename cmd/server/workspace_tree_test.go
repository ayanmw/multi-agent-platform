package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/db"

	_ "modernc.org/sqlite"
)

// setupSessionTreeDB 初始化一个空 schema 的临时 SQLite，供 workspace-tree 测试复用。
func setupSessionTreeDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		db.DB = nil
	})
}

// TestHandleSessionWorkspaceTree 验证 workspace-tree 端点：
//   - 顶层列出文件 + 目录，目录在前
//   - 跳过隐藏文件
//   - path 参数指向子目录时只列该层
//   - 拒绝绝对路径与 .. 的 traversal 请求
func TestHandleSessionWorkspaceTree(t *testing.T) {
	setupSessionTreeDB(t)

	// 构造一个临时 workspace 目录：
	//   root/
	//     a.txt
	//     .hidden        (应被跳过)
	//     sub/
	//       b.go
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write .hidden: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.go"), []byte("package sub"), 0o644); err != nil {
		t.Fatalf("write sub/b.go: %v", err)
	}

	// 插入一条 session 记录，WorkspaceDir 指向上面构造的临时目录。
	sess := db.SessionRecord{
		ID:            "sess_tree_1",
		Name:          "tree-test",
		RootTaskID:    "",
		Status:        "running",
		UserInput:     "",
		ProjectID:     "default",
		TurnCount:     0,
		TotalTokens:   0,
		ContextSize:   0,
		WorkspaceDir:  root,
		WorkspaceAuto: false,
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
		UpdatedAt:     time.Now().UTC().Truncate(time.Second),
	}
	if err := db.InsertSession(sess); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	s := &appServer{}

	// 顶层请求：应得到 sub（目录） + a.txt（文件），且 .hidden 不出现。
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/sess_tree_1/workspace-tree", nil)
	rr := httptest.NewRecorder()
	s.handleSessionWorkspaceTree(rr, req, "sess_tree_1")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var top struct {
		Entries []workspaceFileNode `json:"entries"`
		Path    string              `json:"path"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &top); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(top.Entries) != 2 {
		t.Fatalf("top entries = %d, want 2 (sub + a.txt); %+v", len(top.Entries), top.Entries)
	}
	// 目录排在前。
	if !top.Entries[0].IsDir || top.Entries[0].Name != "sub" {
		t.Errorf("entries[0] = %+v, want dir 'sub'", top.Entries[0])
	}
	if top.Entries[1].IsDir || top.Entries[1].Name != "a.txt" {
		t.Errorf("entries[1] = %+v, want file 'a.txt'", top.Entries[1])
	}
	for _, e := range top.Entries {
		if e.Name == ".hidden" {
			t.Errorf("hidden file should be skipped, got %+v", e)
		}
	}

	// 子目录请求：path=sub，应只得到 b.go。
	req2 := httptest.NewRequest(http.MethodGet, "/api/sessions/sess_tree_1/workspace-tree?path=sub", nil)
	rr2 := httptest.NewRecorder()
	s.handleSessionWorkspaceTree(rr2, req2, "sess_tree_1")
	if rr2.Code != http.StatusOK {
		t.Fatalf("sub status = %d, want 200; body=%s", rr2.Code, rr2.Body.String())
	}
	var sub struct {
		Entries []workspaceFileNode `json:"entries"`
		Path    string              `json:"path"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &sub); err != nil {
		t.Fatalf("unmarshal sub: %v", err)
	}
	if len(sub.Entries) != 1 || sub.Entries[0].Name != "b.go" {
		t.Fatalf("sub entries = %+v, want [b.go]", sub.Entries)
	}
	if sub.Entries[0].RelativePath != "sub/b.go" {
		t.Errorf("RelativePath = %q, want 'sub/b.go'", sub.Entries[0].RelativePath)
	}
}

// TestHandleSessionWorkspaceTreeTraversal 验证 path traversal 请求被拒绝。
func TestHandleSessionWorkspaceTreeTraversal(t *testing.T) {
	setupSessionTreeDB(t)
	root := t.TempDir()
	// 在 root 外放一个 secret 文件，确保即使拼接成功也读不到。
	parent := filepath.Dir(root)
	secret := filepath.Join(parent, "outside_secret.txt")
	if err := os.WriteFile(secret, []byte("nope"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(secret) })

	sess := db.SessionRecord{
		ID:            "sess_tree_2",
		Name:          "traversal-test",
		WorkspaceDir:  root,
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
		UpdatedAt:     time.Now().UTC().Truncate(time.Second),
	}
	if err := db.InsertSession(sess); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	s := &appServer{}

	cases := []string{"..", "../outside_secret.txt", "/etc", "sub/../../.."}
	for _, p := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/sessions/sess_tree_2/workspace-tree?path="+p, nil)
		rr := httptest.NewRecorder()
		s.handleSessionWorkspaceTree(rr, req, "sess_tree_2")
		if rr.Code != http.StatusBadRequest && rr.Code != http.StatusForbidden {
			t.Errorf("path=%q: status = %d, want 400/403; body=%s", p, rr.Code, rr.Body.String())
		}
	}
}

// TestHandleSessionWorkspaceTreeEmptyWorkspace 无 workspace 目录时返回空列表而非 404。
func TestHandleSessionWorkspaceTreeEmptyWorkspace(t *testing.T) {
	setupSessionTreeDB(t)
	sess := db.SessionRecord{
		ID:            "sess_tree_3",
		Name:          "empty-ws",
		WorkspaceDir:  "",
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
		UpdatedAt:     time.Now().UTC().Truncate(time.Second),
	}
	if err := db.InsertSession(sess); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/sess_tree_3/workspace-tree", nil)
	rr := httptest.NewRecorder()
	s := &appServer{}
	s.handleSessionWorkspaceTree(rr, req, "sess_tree_3")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Entries []workspaceFileNode `json:"entries"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Entries) != 0 {
		t.Errorf("entries = %d, want 0 for empty workspace", len(got.Entries))
	}
}
