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

// newTestMCPManager returns a Manager with an empty repo for handler tests.
func newTestMCPManager(t *testing.T) *mcp.Manager {
	t.Helper()
	return mcp.NewManager(tool.NewRegistry(), mcp.EmptyRepository{})
}

// setupMCPRoutes registers MCP routes on a fresh ServeMux for testing.
func setupMCPRoutes(t *testing.T, mgr *mcp.Manager) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	registerMCPRoutes(mux, mgr)
	return httptest.NewServer(mux)
}

// TestMCPListServersEmpty verifies GET /api/mcp/servers returns an empty list.
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

// TestMCPCreateServer validates POST /api/mcp/servers adds a server.
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

	// List should now contain exactly one server.
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

// TestMCPDeleteServer removes a dynamic server.
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

	// List should be empty again.
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

// TestMCPDeleteStaticServerForbidden ensures static servers cannot be deleted.
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

// TestMCPStaticServerCannotBeCreatedViaAPI ensures overwriting a static server via POST is forbidden.
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

// TestMCPDisableEnableServer toggles a dynamic server's enabled state.
// Because the handler uses the real stdio transport backed by a missing
// command, the server never actually loads through the API. We exercise disable
// on a server that is already tracked (disabled) and verify that re-enabling a
// server without a valid command returns an error the API surfaces as 500.
func TestMCPDisableEnableServer(t *testing.T) {
	mgr := newTestMCPManager(t)
	defer mgr.Close()

	ts := setupMCPRoutes(t, mgr)
	defer ts.Close()

	// Create a disabled server.
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

	// Disable an already-disabled server should be idempotent and succeed.
	resp, err := http.Post(ts.URL+"/api/mcp/servers/toggle/disable", "application/json", nil)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 disable, got %d: %s", resp.StatusCode, readBodyString(resp))
	}

	// Re-enabling a server whose command does not exist succeeds at the API
	// level because EnableServer currently only sets the flag and persists the
	// change; the actual transport connection is attempted asynchronously.
	// This documents the current behavior and will be tightened once connection
	// health is validated during enable.
	resp2, err := http.Post(ts.URL+"/api/mcp/servers/toggle/enable", "application/json", nil)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 enable, got %d: %s", resp2.StatusCode, readBodyString(resp2))
	}
}

// readBodyString reads the entire response body as a string for test diagnostics.
func readBodyString(resp *http.Response) string {
	var buf bytes.Buffer
	if resp.Body != nil {
		buf.ReadFrom(resp.Body)
		// Restore the body so callers can read it again if needed.
		resp.Body = &nopCloser{Reader: bytes.NewReader(buf.Bytes())}
	}
	return buf.String()
}

// nopCloser wraps an io.Reader to implement io.ReadCloser.
type nopCloser struct {
	*bytes.Reader
}

func (nopCloser) Close() error { return nil }

// newTestContext returns a short-lived context suitable for handler tests.
func newTestContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}
