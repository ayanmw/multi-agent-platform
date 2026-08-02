package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ayanmw/multi-agent-platform/internal/tool"
	"github.com/ayanmw/multi-agent-platform/pkg/db"

	_ "modernc.org/sqlite"
)

// setupToolTestDB 初始化一个带完整 schema 的临时 SQLite 数据库。
func setupToolTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		db.DB = nil
	})
}

// newToolRegistryForTest 创建一个已注册内置工具、并从空 DB 加载动态工具的 registry。
func newToolRegistryForTest(t *testing.T) *tool.Registry {
	t.Helper()
	reg := tool.NewRegistry()
	tool.RegisterBuiltins(reg)
	return reg
}

// newTestAppServer 构造一个仅包含 tool 子系统的最小 appServer。
func newTestAppServer(t *testing.T, reg *tool.Registry) *appServer {
	t.Helper()
	return &appServer{toolRegistry: reg}
}

// TestRegisterAndReloadDynamicShellTool 验证：
// 1. 注册 shell 动态工具写入 v27 表；2. 用新 registry 从 DB 加载后工具存在且可执行。
func TestRegisterAndReloadDynamicShellTool(t *testing.T) {
	setupToolTestDB(t)

	reg1 := newToolRegistryForTest(t)
	srv := newTestAppServer(t, reg1)

	body, _ := json.Marshal(map[string]any{
		"name":    "test-echo",
		"type":    "shell",
		"command": "echo hello",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/tools", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.handleRegisterTool(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register tool: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	if _, ok := reg1.Get("test-echo"); !ok {
		t.Fatalf("tool should be in initial registry")
	}

	// 模拟服务重启：构造新 registry 并从 DB 加载。
	reg2 := newToolRegistryForTest(t)
	loadDynamicToolsFromDB(t, reg2)

	dt, ok := reg2.Get("test-echo")
	if !ok {
		t.Fatalf("reloaded registry should contain test-echo")
	}
	if dt.Source() != "local_db" {
		t.Fatalf("reloaded tool source = %q, want local_db", dt.Source())
	}

	// 非 Windows 下验证 shell 执行；Windows 无 sh，仅验证存在与类型。
	if runtime.GOOS == "windows" {
		return
	}
	dir := t.TempDir()
	res, err := reg2.ExecuteWithCtx("test-echo", tool.ExecuteContext{Workdir: dir}, nil)
	if err != nil {
		t.Fatalf("execute reloaded tool: %v", err)
	}
	result := res.(map[string]any)
	stdout := strings.TrimSpace(result["stdout"].(string))
	if stdout != "hello" {
		t.Fatalf("expected 'hello', got %q", stdout)
	}
}

// TestDeleteDynamicTool 验证删除工具会从 registry 与 DB 同时移除。
func TestDeleteDynamicTool(t *testing.T) {
	setupToolTestDB(t)

	reg := newToolRegistryForTest(t)
	srv := newTestAppServer(t, reg)

	// 先注册
	body, _ := json.Marshal(map[string]any{
		"name":    "test-del",
		"type":    "shell",
		"command": "echo x",
	})
	srv.handleRegisterTool(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/tools", bytes.NewReader(body)))
	if _, ok := reg.Get("test-del"); !ok {
		t.Fatalf("tool should be registered before delete")
	}

	// 再删除
	req := httptest.NewRequest(http.MethodDelete, "/api/tools?name=test-del", nil)
	rr := httptest.NewRecorder()
	srv.handleDeleteTool(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete tool: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	if _, ok := reg.Get("test-del"); ok {
		t.Fatalf("tool should be removed from registry")
	}

	reg2 := newToolRegistryForTest(t)
	loadDynamicToolsFromDB(t, reg2)
	if _, ok := reg2.Get("test-del"); ok {
		t.Fatalf("tool should be removed from DB")
	}
}

// loadDynamicToolsFromDB 复用 main.go 的加载逻辑：从 v27 表加载 local_db 工具到 registry。
func loadDynamicToolsFromDB(t *testing.T, reg *tool.Registry) {
	t.Helper()
	loader := tool.NewDBToolLoader(func() ([]map[string]any, error) {
		records, err := db.QueryToolsV2()
		if err != nil {
			return nil, err
		}
		maps := make([]map[string]any, 0, len(records))
		for _, tr := range records {
			if tr.ExecutionConfig == nil {
				continue
			}
			if typ, _ := tr.ExecutionConfig["type"].(string); typ == "" {
				continue
			}
			maps = append(maps, map[string]any{
				"namespace":        tr.Namespace,
				"name":             tr.Name,
				"version":          tr.Version,
				"source":           tr.Source,
				"description":      tr.Description,
				"parameters":       tr.Schema,
				"execution_config": tr.ExecutionConfig,
			})
		}
		return maps, nil
	})
	loaded, err := loader.Load(t.Context())
	if err != nil {
		t.Fatalf("load dynamic tools: %v", err)
	}
	for _, dt := range loaded {
		if dt.Source() == "local_db" {
			reg.Register(dt)
		}
	}
}
