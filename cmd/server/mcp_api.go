package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp"
)

// registerMCPRoutes wires the MCP management API endpoints into mux.
//
// Endpoints:
//   GET    /api/mcp/servers              — list managed servers and load status
//   POST   /api/mcp/servers              — add a dynamic server
//   POST   /api/mcp/servers/:id/enable   — enable and connect a server
//   POST   /api/mcp/servers/:id/disable  — disconnect and disable a server
//   DELETE /api/mcp/servers/:id          — remove a dynamic server
func registerMCPRoutes(mux *http.ServeMux, mgr *mcp.Manager) {
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

		// POST /api/mcp/servers/:id/enable or /disable
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

// handleListMCPServers returns all configured MCP servers with runtime load status.
func handleListMCPServers(w http.ResponseWriter, _ *http.Request, mgr *mcp.Manager) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"servers": mgr.ListServerStatuses(),
	})
}

// handleCreateMCPServer adds a dynamic MCP server, persists it, and connects if enabled.
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

// handleEnableMCPServer enables and connects a managed MCP server.
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

// handleDisableMCPServer disconnects and disables a managed MCP server.
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

// handleDeleteMCPServer removes a managed MCP server.
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

// writeJSONError is duplicated from internal/auth/auth_http.go so the auth package
// does not need to export helpers. It writes a JSON error with the given status code.
func writeJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

