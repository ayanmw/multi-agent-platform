package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/anmingwei/multi-agent-platform/internal/observability"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// toolCounter 用于为未显式提供 name 的动态 tool 生成唯一 name。
// 它以原子方式自增，避免并发 API 请求间的冲突。
var toolCounter uint64

// handleRegisterTool 处理 POST /api/tools —— 注册一个新的动态 tool。
// 注册后该 tool 立即对 agent 可用。
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
		// Shell 类型字段
		Command string `json:"command"`
		// HTTP 类型字段
		URL    string `json:"url"`
		Method string `json:"method"`
		// Inline 类型字段
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// 校验 tool 类型
	if req.Type != "shell" && req.Type != "http" && req.Type != "inline" {
		http.Error(w, fmt.Sprintf("type must be 'shell', 'http', or 'inline', got: %s", req.Type), http.StatusBadRequest)
		return
	}

	// 未提供 name 时生成唯一 name
	if req.Name == "" {
		counter := atomic.AddUint64(&toolCounter, 1)
		req.Name = fmt.Sprintf("dynamic_tool_%03d", counter)
	}

	// 检查 name 冲突
	for _, t := range toolRegistry.List() {
		if t.Name() == req.Name {
			http.Error(w, fmt.Sprintf("tool with name '%s' already exists", req.Name), http.StatusConflict)
			return
		}
	}

	// 校验类型相关字段
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

	// 未提供 description 时使用默认值
	if req.Description == "" {
		req.Description = fmt.Sprintf("Dynamic tool: %s (%s)", req.Name, req.Type)
	}

	// 未提供 parameters schema 时使用默认值
	if req.Parameters == nil {
		req.Parameters = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	// 创建并配置 DynamicTool
	dt := tool.NewDynamicTool(req.Name, req.Description, req.Parameters, tool.DynamicToolType(req.Type))
	switch req.Type {
	case "shell":
		dt.SetCommand(req.Command)
	case "http":
		dt.SetHTTP(req.URL, req.Method)
	case "inline":
		dt.SetCode(req.Code)
	}

	// 注册到全局 tool registry（立即对 agent 可用）
	toolRegistry.Register(dt)

	// 持久化到 SQLite 的 tools 表
	if err := db.InsertTool(req.Name, req.Description, req.Parameters, true); err != nil {
		// 持久化失败时回滚注册
		toolRegistry.Unregister(req.Name)
		http.Error(w, fmt.Sprintf("failed to persist tool: %v", err), http.StatusInternalServerError)
		return
	}

	// Phase 7-C: 审计日志记录动态 tool 注册。
	observability.DefaultAuditor.Record(observability.AuditRecord{
		Actor:  currentActor(r),
		Action: "register_tool",
		Target: req.Name,
		Before: map[string]any{"exists": false},
		After: map[string]any{
			"name":        req.Name,
			"type":        req.Type,
			"command":     req.Command,
			"url":         req.URL,
			"method":      req.Method,
			"description": req.Description,
		},
	})

	// 构建响应
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

// currentActor 从请求的 Authorization 头中提取 actor 标识。
func currentActor(r *http.Request) string {
	if r == nil {
		return "system"
	}
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if key == "" {
		return "anonymous"
	}
	if len(key) > 8 {
		key = key[:8]
	}
	return "apikey:" + key
}
// （包括内置与动态 tool）。每个 tool 返回时附带其 metadata，
// 以及一个 "builtin" 标志，指示它是否为受保护的内置 tool。
func handleListTools(w http.ResponseWriter, r *http.Request, toolRegistry *tool.Registry) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	tools := toolRegistry.List()
	result := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		entry := map[string]any{
			// Phase 7-I：前端需要看到真实的 FullName（如 core/list_dir、skill/list）
			// 才能正确配置 agent.tools 白名单并与 Engine 的 AllowedTools 精确匹配。
			"name":        t.FullName(),
			"description": t.Description(),
			"parameters":  t.Parameters(),
			"builtin":     toolRegistry.IsBuiltin(t.Name()),
		}
		// 额外保留短名与 namespace，便于 UI 分组与展示
		entry["short_name"] = t.Name()
		entry["namespace"] = t.Namespace()
		// 对动态 tool 附带类型特定信息
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

// handleDeleteTool 处理 DELETE /api/tools?name=xxx —— 注销一个动态 tool。
// 内置 tool（run_shell、write_file、read_file）受保护，不能删除。
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

	// 保护内置 tool 不被删除
	if toolRegistry.IsBuiltin(name) {
		http.Error(w, fmt.Sprintf("cannot delete built-in tool: %s", name), http.StatusForbidden)
		return
	}

	// 从全局 tool registry 中注销
	if err := toolRegistry.Unregister(name); err != nil {
		http.Error(w, fmt.Sprintf("tool not found: %s", name), http.StatusNotFound)
		return
	}

	// 从 SQLite tools 表中删除
	if err := db.DeleteTool(name); err != nil {
		http.Error(w, fmt.Sprintf("failed to delete tool from database: %v", err), http.StatusInternalServerError)
		return
	}

	// Phase 7-C: 审计日志记录动态 tool 注销。
	observability.DefaultAuditor.Record(observability.AuditRecord{
		Actor:  currentActor(r),
		Action: "delete_tool",
		Target: name,
		Before: map[string]any{"registered": true},
		After:  map[string]any{"deleted": true},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
	json.NewEncoder(w).Encode(map[string]any{
		"name":    name,
		"message": fmt.Sprintf("Tool '%s' unregistered successfully", name),
	})
}