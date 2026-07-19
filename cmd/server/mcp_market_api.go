package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp"
)

// registerMCPMarketRoutes 把 MCP marketplace endpoint 挂载到 mux。
//
// Endpoints：
//   GET    /api/mcp/markets                         — 列出已注册的 market
//   GET    /api/mcp/markets/:market/servers         — 列出某 market 中的 package
//   POST   /api/mcp/markets/:market/servers/:id/install — 将 package 安装为受管理的 server
func registerMCPMarketRoutes(mux *http.ServeMux, mgr *mcp.Manager) {
	mux.HandleFunc("/api/mcp/markets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		handleListMarkets(w, r, mgr)
	})
	mux.HandleFunc("/api/mcp/markets/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/mcp/markets/")
		if path == "" {
			writeJSONError(w, "market path required", http.StatusBadRequest)
			return
		}

		// 期望格式：:market/servers 或 :market/servers/:id/install
		parts := strings.SplitN(path, "/", 4)
		if len(parts) < 2 || parts[1] != "servers" {
			writeJSONError(w, "invalid market path", http.StatusBadRequest)
			return
		}
		marketName := parts[0]

		// GET /api/mcp/markets/:market/servers
		if len(parts) == 2 && r.Method == http.MethodGet {
			handleListMarketServers(w, r, mgr, marketName)
			return
		}

		// POST /api/mcp/markets/:market/servers/:id/install
		if len(parts) == 3 && r.Method == http.MethodPost && parts[2] == "install" {
			handleInstallMarketServer(w, r, mgr, marketName, parts[2])
			return
		}
		// 当路径有 4 段时，ID 本身可能包含斜杠（不太可能但安全起见做拼接）
		if len(parts) >= 3 && r.Method == http.MethodPost {
			id := strings.Join(parts[2:len(parts)-1], "/")
			if parts[len(parts)-1] == "install" {
				handleInstallMarketServer(w, r, mgr, marketName, id)
				return
			}
		}

		writeJSONError(w, "unsupported market operation", http.StatusMethodNotAllowed)
	})
}

// handleListMarkets 返回所有已注册的 marketplace provider。
func handleListMarkets(w http.ResponseWriter, _ *http.Request, mgr *mcp.Manager) {
	providers := mgr.Markets()
	markets := make([]map[string]any, 0, len(providers))
	for _, p := range providers {
		markets = append(markets, map[string]any{
			"name":         p.Name(),
			"display_name": p.DisplayName(),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"markets": markets})
}

// handleListMarketServers 返回某 market 中所有可用的 package。
func handleListMarketServers(w http.ResponseWriter, r *http.Request, mgr *mcp.Manager, marketName string) {
	provider, ok := mgr.GetMarket(marketName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("market not found: %s", marketName), http.StatusNotFound)
		return
	}
	pkgs, err := provider.ListServers(r.Context())
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"market":  marketName,
		"servers": pkgs,
	})
}

// handleInstallMarketServer 将一个 marketplace package 安装为受管理的 server。
func handleInstallMarketServer(w http.ResponseWriter, r *http.Request, mgr *mcp.Manager, marketName, id string) {
	if id == "" {
		writeJSONError(w, "server ID required", http.StatusBadRequest)
		return
	}
	ms, err := mgr.InstallFromMarket(r.Context(), marketName, id)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeJSONError(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"server": ms})
}
