package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp"
)

// registerMCPRoutes 把 MCP 管理 API endpoint 挂载到 mux。
//
// Endpoints：
//   GET    /api/mcp/servers              — 列出受管理的 server 及加载状态
//   POST   /api/mcp/servers              — 添加一个动态 server
//   POST   /api/mcp/servers/:id/enable   — 启用并连接某个 server
//   POST   /api/mcp/servers/:id/disable  — 断开并禁用某个 server
//   DELETE /api/mcp/servers/:id          — 移除一个动态 server
func registerMCPRoutes(mux *http.ServeMux, mgr *mcp.Manager) {
	registerMCPServerRoutes(mux, mgr)
	registerMCPMarketRoutes(mux, mgr)
}

func registerMCPServerRoutes(mux *http.ServeMux, mgr *mcp.Manager) {
	mux.HandleFunc("/api/mcp/servers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleListMCPServers(w, r, mgr)
		case http.MethodPost:
			handleCreateMCPServer(w, r, mgr)
		default:
			writeJSONError(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/mcp/servers/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/mcp/servers/")
		if path == "" {
			writeJSONError(w, "server ID required", http.StatusBadRequest)
			return
		}
		id := path

		// POST /api/mcp/servers/:id/enable 或 /disable
		if r.Method == http.MethodPost {
			if suffix, ok := strings.CutSuffix(path, "/enable"); ok {
				handleEnableMCPServer(w, r, mgr, suffix)
				return
			}
			if suffix, ok := strings.CutSuffix(path, "/disable"); ok {
				handleDisableMCPServer(w, r, mgr, suffix)
				return
			}
		}

		// DELETE /api/mcp/servers/:id
		if r.Method == http.MethodDelete {
			handleDeleteMCPServer(w, r, mgr, id)
			return
		}

		writeJSONError(w, "unsupported method", http.StatusMethodNotAllowed)
	})
}

// handleListMCPServers 返回所有已配置 MCP server 及运行时加载状态。
func handleListMCPServers(w http.ResponseWriter, _ *http.Request, mgr *mcp.Manager) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"servers": mgr.ListServerStatuses(),
	})
}

// handleCreateMCPServer 添加一个动态 MCP server，持久化它，并在启用时建立连接。
func handleCreateMCPServer(w http.ResponseWriter, r *http.Request, mgr *mcp.Manager) {
	var req struct {
		ID      string           `json:"id"`
		Config  mcp.ServerConfig `json:"config"`
		Enabled bool             `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	if req.ID == "" {
		writeJSONError(w, "id is required", http.StatusBadRequest)
		return
	}
	if req.Config.Name == "" {
		req.Config.Name = req.ID
	}
	req.Config.Enabled = req.Enabled

	ms := mcp.ManagedServer{
		ID:      req.ID,
		Source:  mcp.SourceDB,
		Config:  req.Config,
		Enabled: req.Enabled,
	}

	if err := mgr.AddServer(r.Context(), ms); err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "static") {
			status = http.StatusForbidden
		} else if strings.Contains(err.Error(), "already") || strings.Contains(err.Error(), "conflict") {
			status = http.StatusConflict
		}
		writeJSONError(w, err.Error(), status)
		return
	}

	status, err := mgr.GetServer(req.ID)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"server": status,
	})
}

// handleEnableMCPServer 启用并连接一个受管理的 MCP server。
func handleEnableMCPServer(w http.ResponseWriter, r *http.Request, mgr *mcp.Manager, id string) {
	if err := mgr.EnableServer(r.Context(), id); err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeJSONError(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":      id,
		"enabled": true,
	})
}

// handleDisableMCPServer 断开并禁用一个受管理的 MCP server。
func handleDisableMCPServer(w http.ResponseWriter, r *http.Request, mgr *mcp.Manager, id string) {
	if err := mgr.DisableServer(r.Context(), id); err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeJSONError(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":       id,
		"enabled":  false,
	})
}

// handleDeleteMCPServer 移除一个受管理的 MCP server。
func handleDeleteMCPServer(w http.ResponseWriter, r *http.Request, mgr *mcp.Manager, id string) {
	if err := mgr.RemoveServer(r.Context(), id); err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "static") {
			status = http.StatusForbidden
		}
		writeJSONError(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":      id,
		"deleted": true,
	})
}

// writeJSONError 复制自 internal/auth/auth_http.go，避免 auth 包需要导出
// 该 helper。它用给定的 status code 写入一条 JSON 错误。
func writeJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

