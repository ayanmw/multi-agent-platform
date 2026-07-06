package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// toolCounter is used to generate unique names for dynamic tools that
// don't provide an explicit name. It increments atomically to avoid
// collisions across concurrent API requests.
var toolCounter uint64

// handleRegisterTool handles POST /api/tools — register a new dynamic tool.
// The tool is immediately available to agents after registration.
func handleRegisterTool(w http.ResponseWriter, r *http.Request, toolRegistry *tool.Registry) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
		Type        string         `json:"type"`
		// Shell-type fields
		Command string `json:"command"`
		// HTTP-type fields
		URL    string `json:"url"`
		Method string `json:"method"`
		// Inline-type fields
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Validate tool type
	if req.Type != "shell" && req.Type != "http" && req.Type != "inline" {
		http.Error(w, fmt.Sprintf("type must be 'shell', 'http', or 'inline', got: %s", req.Type), http.StatusBadRequest)
		return
	}

	// Generate unique name if not provided
	if req.Name == "" {
		counter := atomic.AddUint64(&toolCounter, 1)
		req.Name = fmt.Sprintf("dynamic_tool_%03d", counter)
	}

	// Check for name collision
	for _, t := range toolRegistry.List() {
		if t.Name() == req.Name {
			http.Error(w, fmt.Sprintf("tool with name '%s' already exists", req.Name), http.StatusConflict)
			return
		}
	}

	// Validate type-specific fields
	switch req.Type {
	case "shell":
		if req.Command == "" {
			http.Error(w, "command is required for shell-type tools", http.StatusBadRequest)
			return
		}
	case "http":
		if req.URL == "" {
			http.Error(w, "url is required for http-type tools", http.StatusBadRequest)
			return
		}
		if req.Method == "" {
			req.Method = "GET"
		}
	case "inline":
		if req.Code == "" {
			http.Error(w, "code is required for inline-type tools", http.StatusBadRequest)
			return
		}
	}

	// Default description if not provided
	if req.Description == "" {
		req.Description = fmt.Sprintf("Dynamic tool: %s (%s)", req.Name, req.Type)
	}

	// Default parameters schema if not provided
	if req.Parameters == nil {
		req.Parameters = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	// Create and configure the DynamicTool
	dt := tool.NewDynamicTool(req.Name, req.Description, req.Parameters, tool.DynamicToolType(req.Type))
	switch req.Type {
	case "shell":
		dt.SetCommand(req.Command)
	case "http":
		dt.SetHTTP(req.URL, req.Method)
	case "inline":
		dt.SetCode(req.Code)
	}

	// Register in the global tool registry (immediately available to agents)
	toolRegistry.Register(dt)

	// Persist to the tools table in SQLite
	if err := db.InsertTool(req.Name, req.Description, req.Parameters, true); err != nil {
		// Rollback registration on persistence failure
		toolRegistry.Unregister(req.Name)
		http.Error(w, fmt.Sprintf("failed to persist tool: %v", err), http.StatusInternalServerError)
		return
	}

	// Build response
	response := map[string]any{
		"name":        dt.Name(),
		"description": dt.Description(),
		"parameters":  dt.Parameters(),
		"type":        req.Type,
	}
	switch req.Type {
	case "shell":
		response["command"] = req.Command
	case "http":
		response["url"] = req.URL
		response["method"] = req.Method
	case "inline":
		response["code"] = req.Code
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// handleListTools handles GET /api/tools — list all registered tools
// (both built-in and dynamic). Each tool is returned with its metadata
// and a "builtin" flag indicating whether it is a protected built-in tool.
func handleListTools(w http.ResponseWriter, r *http.Request, toolRegistry *tool.Registry) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	tools := toolRegistry.List()
	result := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		entry := map[string]any{
			"name":        t.Name(),
			"description": t.Description(),
			"parameters":  t.Parameters(),
			"builtin":     toolRegistry.IsBuiltin(t.Name()),
		}
		// Include type-specific info for dynamic tools
		if dt, ok := t.(*tool.DynamicTool); ok {
			entry["type"] = string(dt.ToolType())
			switch dt.ToolType() {
			case tool.DynamicToolShell:
				entry["command"] = dt.Command()
			case tool.DynamicToolHTTP:
				entry["url"] = dt.URL()
				entry["method"] = dt.Method()
			}
		}
		result = append(result, entry)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleDeleteTool handles DELETE /api/tools?name=xxx — unregister a dynamic tool.
// Built-in tools (run_shell, write_file, read_file) are protected and cannot be deleted.
func handleDeleteTool(w http.ResponseWriter, r *http.Request, toolRegistry *tool.Registry) {
	if r.Method != http.MethodDelete {
		http.Error(w, "DELETE only", http.StatusMethodNotAllowed)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name query parameter is required", http.StatusBadRequest)
		return
	}

	// Protect built-in tools from deletion
	if toolRegistry.IsBuiltin(name) {
		http.Error(w, fmt.Sprintf("cannot delete built-in tool: %s", name), http.StatusForbidden)
		return
	}

	// Unregister from the global tool registry
	if err := toolRegistry.Unregister(name); err != nil {
		http.Error(w, fmt.Sprintf("tool not found: %s", name), http.StatusNotFound)
		return
	}

	// Remove from SQLite tools table
	if err := db.DeleteTool(name); err != nil {
		http.Error(w, fmt.Sprintf("failed to delete tool from database: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
	json.NewEncoder(w).Encode(map[string]any{
		"name":    name,
		"message": fmt.Sprintf("Tool '%s' unregistered successfully", name),
	})
}