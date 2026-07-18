package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp"
	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp/marketplace"
)

func setupMarketTestManager() *mcp.Manager {
	reg := tool.NewRegistry()
	tool.RegisterBuiltins(reg)
	mgr := mcp.NewManager(reg, nil)
	static, _ := marketplace.NewStaticProvider([]byte(`{
		"version": "1.0.0",
		"markets": [{"name": "default", "display_name": "Default", "description": "test"}],
		"servers": [
			{
				"id": "test",
				"market": "default",
				"name": "Test",
				"description": "Test server",
				"transport": "stdio",
				"command": "node",
				"args": ["notfound.js"]
			}
		]
	}`))
	mgr.RegisterMarket(static)
	return mgr
}

func TestListMarkets(t *testing.T) {
	mgr := setupMarketTestManager()
	mux := http.NewServeMux()
	registerMCPMarketRoutes(mux, mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/mcp/markets", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	markets, ok := body["markets"].([]any)
	if !ok || len(markets) != 1 {
		t.Fatalf("markets = %v, want 1", body["markets"])
	}
}

func TestListMarketServers(t *testing.T) {
	mgr := setupMarketTestManager()
	mux := http.NewServeMux()
	registerMCPMarketRoutes(mux, mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/mcp/markets/default/servers", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	servers, ok := body["servers"].([]any)
	if !ok || len(servers) != 1 {
		t.Fatalf("servers = %v, want 1", body["servers"])
	}
}

func TestInstallMarketServer(t *testing.T) {
	mgr := setupMarketTestManager()
	mux := http.NewServeMux()
	registerMCPMarketRoutes(mux, mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/mcp/markets/default/servers/test/install", bytes.NewReader([]byte{}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// The install may fail to connect because the fake command is invalid, but
	// the server record should still be created as a disabled managed server.
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	// Verify the server is now managed.
	if _, err := mgr.GetServer("test"); err != nil {
		t.Fatalf("server not installed: %v", err)
	}
}

func TestInstallMissingMarketServer(t *testing.T) {
	mgr := setupMarketTestManager()
	mux := http.NewServeMux()
	registerMCPMarketRoutes(mux, mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/mcp/markets/default/servers/missing/install", bytes.NewReader([]byte{}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// Ensure context.Background is used as a placeholder to satisfy any unused import
// linter if future edits remove ctx usage.
var _ = context.Background
