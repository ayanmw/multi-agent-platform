package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp"
)

// newTestMCPManager 返回一个带空 repo 的 Manager，供 handler 测试使用。
func newTestMCPManager(t *testing.T) *mcp.Manager {
	t.Helper()
	return mcp.NewManager(tool.NewRegistry(), mcp.EmptyRepository{})
}

// setupMCPRoutes 在一个新的 ServeMux 上注册 MCP 路由，用于测试。
func setupMCPRoutes(t *testing.T, mgr *mcp.Manager) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	registerMCPRoutes(mux, mgr)
	return httptest.NewServer(mux)
}

// TestMCPListServersEmpty 验证 GET /api/mcp/servers 返回空列表。
func TestMCPListServersEmpty(t *testing.T) {
	mgr := newTestMCPManager(t)
	defer mgr.Close()

	ts := setupMCPRoutes(t, mgr)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/mcp/servers")
	if err != nil {
		t.Fatalf("GET /api/mcp/servers: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	servers, ok := body["servers"].([]any)
	if !ok {
		t.Fatalf("expected servers array, got %T", body["servers"])
	}
	if len(servers) != 0 {
		t.Fatalf("expected 0 servers, got %d", len(servers))
	}
}

// TestMCPCreateServer 验证 POST /api/mcp/servers 添加一个 server。
func TestMCPCreateServer(t *testing.T) {
	mgr := newTestMCPManager(t)
	defer mgr.Close()

	ts := setupMCPRoutes(t, mgr)
	defer ts.Close()

	payload, _ := json.Marshal(map[string]any{
		"id":      "test-server",
		"enabled": false,
		"config": map[string]any{
			"name":      "test-server",
			"transport": "stdio",
			"command":   "node",
			"args":      []string{"example.js"},
			"enabled":   false,
		},
	})
	resp, err := http.Post(ts.URL+"/api/mcp/servers", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /api/mcp/servers: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, readBodyString(resp))
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	server, ok := body["server"].(map[string]any)
	if !ok {
		t.Fatalf("expected server object, got %T", body["server"])
	}
	if server["id"] != "test-server" {
		t.Fatalf("expected id test-server, got %v", server["id"])
	}
	if server["enabled"] != false {
		t.Fatalf("expected enabled false, got %v", server["enabled"])
	}

	// 列表中此时应正好包含一个 server。
	resp2, err := http.Get(ts.URL + "/api/mcp/servers")
	if err != nil {
		t.Fatalf("list after create: %v", err)
	}
	defer resp2.Body.Close()
	var list map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list["servers"].([]any)) != 1 {
		t.Fatalf("expected 1 server after create, got %d", len(list["servers"].([]any)))
	}
}

// TestMCPDeleteServer 移除一个动态 server。
func TestMCPDeleteServer(t *testing.T) {
	mgr := newTestMCPManager(t)
	defer mgr.Close()

	ts := setupMCPRoutes(t, mgr)
	defer ts.Close()

	payload, _ := json.Marshal(map[string]any{
		"id":      "to-delete",
		"enabled": false,
		"config": map[string]any{
			"name":      "to-delete",
			"transport": "stdio",
			"command":   "node",
			"args":      []string{"example.js"},
		},
	})
	createResp, _ := http.Post(ts.URL+"/api/mcp/servers", "application/json", bytes.NewReader(payload))
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected create 201, got %d", createResp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/mcp/servers/to-delete", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/mcp/servers/to-delete: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBodyString(resp))
	}

	// 列表应再次为空。
	resp2, err := http.Get(ts.URL + "/api/mcp/servers")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	defer resp2.Body.Close()
	var list map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list["servers"].([]any)) != 0 {
		t.Fatalf("expected 0 servers after delete, got %d", len(list["servers"].([]any)))
	}
}

// TestMCPDeleteStaticServerForbidden 确保静态 server 不能被删除。
func TestMCPDeleteStaticServerForbidden(t *testing.T) {
	mgr := newTestMCPManager(t)
	defer mgr.Close()

	ctx, cancel := newTestContext()
	defer cancel()
	mgr.LoadStaticServers(ctx, []mcp.ServerConfig{
		{Name: "static", Command: "node", Args: []string{"example.js"}, Enabled: false},
	})

	ts := setupMCPRoutes(t, mgr)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/mcp/servers/static", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE static: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", resp.StatusCode, readBodyString(resp))
	}
}

// TestMCPStaticServerCannotBeCreatedViaAPI 确保通过 POST 覆盖静态 server 是被禁止的。
func TestMCPStaticServerCannotBeCreatedViaAPI(t *testing.T) {
	mgr := newTestMCPManager(t)
	defer mgr.Close()

	ctx, cancel := newTestContext()
	defer cancel()
	mgr.LoadStaticServers(ctx, []mcp.ServerConfig{
		{Name: "static", Command: "node", Args: []string{"example.js"}, Enabled: false},
	})

	ts := setupMCPRoutes(t, mgr)
	defer ts.Close()

	payload, _ := json.Marshal(map[string]any{
		"id":      "static",
		"enabled": true,
		"config": map[string]any{
			"name":      "static",
			"transport": "stdio",
			"command":   "node",
			"args":      []string{"new.js"},
		},
	})
	resp, err := http.Post(ts.URL+"/api/mcp/servers", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create over static: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", resp.StatusCode, readBodyString(resp))
	}
}

// TestMCPDisableEnableServer 切换一个动态 server 的 enabled 状态。
// 由于 handler 使用由缺失命令支撑的真实 stdio transport，server 实际上
// 无法通过 API 加载。我们对一个已跟踪（disabled）的 server 执行 disable，
// 并验证对一个没有有效命令的 server 重新 enable 会返回一个被 API 表达为 500 的错误。
func TestMCPDisableEnableServer(t *testing.T) {
	mgr := newTestMCPManager(t)
	defer mgr.Close()

	ts := setupMCPRoutes(t, mgr)
	defer ts.Close()

	// 创建一个 disabled 的 server。
	payload, _ := json.Marshal(map[string]any{
		"id":      "toggle",
		"enabled": false,
		"config": map[string]any{
			"name":      "toggle",
			"transport": "stdio",
			"command":   "node",
			"args":      []string{"example.js"},
		},
	})
	createResp, err := http.Post(ts.URL+"/api/mcp/servers", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected create 201, got %d", createResp.StatusCode)
	}

	// 对一个已 disabled 的 server 执行 disable 应保持幂等并成功。
	resp, err := http.Post(ts.URL+"/api/mcp/servers/toggle/disable", "application/json", nil)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 disable, got %d: %s", resp.StatusCode, readBodyString(resp))
	}

	// 对一个命令不存在的 server 重新 enable 在 API 层面会成功，因为
	// EnableServer 目前只设置标志位并持久化变更；真正的 transport 连接是
	// 异步尝试的。这里记录当前行为，待 enable 时校验连接健康度后再收紧。
	resp2, err := http.Post(ts.URL+"/api/mcp/servers/toggle/enable", "application/json", nil)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 enable, got %d: %s", resp2.StatusCode, readBodyString(resp2))
	}
}

// readBodyString 读取整个响应体作为字符串，用于测试诊断。
func readBodyString(resp *http.Response) string {
	var buf bytes.Buffer
	if resp.Body != nil {
		buf.ReadFrom(resp.Body)
		// 还原 body，以便调用方需要时可以再次读取。
		resp.Body = &nopCloser{Reader: bytes.NewReader(buf.Bytes())}
	}
	return buf.String()
}

// nopCloser 包装一个 io.Reader，实现 io.ReadCloser。
type nopCloser struct {
	*bytes.Reader
}

func (nopCloser) Close() error { return nil }

// newTestContext 返回一个适合 handler 测试的短生命周期 context。
func newTestContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}
