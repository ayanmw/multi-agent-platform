package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/google/uuid"
)

// RegisterMockRoutes 把 mock 脚本管理 API endpoint 注册到 mux。
// 它暴露对 mock 脚本的 CRUD 操作，以及一个 reset endpoint 用于重新加载
// 内置脚本。这些 endpoint 面向开发与测试，便于运维无需重启 server 即可
// 检查并修改 mock LLM 行为。
func RegisterMockRoutes(mux *http.ServeMux, store llm.MockScriptStore, builtinScripts []llm.MockScript) {
	// GET /api/mock/scripts —— 列出所有脚本
	// POST /api/mock/scripts —— 创建或更新一个脚本
	mux.HandleFunc("/api/mock/scripts", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			listScripts(w, r, store)
		case http.MethodPost:
			saveScript(w, r, store)
		default:
			http.Error(w, "GET, POST only", http.StatusMethodNotAllowed)
		}
	})

	// GET /api/mock/scripts/{id} —— 获取一个脚本
	// DELETE /api/mock/scripts/{id} —— 删除一个脚本
	mux.HandleFunc("/api/mock/scripts/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			http.Error(w, "GET, DELETE only", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/mock/scripts/")
		if id == "" {
			http.Error(w, "script id required", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodGet:
			getScript(w, r, store, id)
		case http.MethodDelete:
			deleteScript(w, r, store, id)
		}
	})

	// POST /api/mock/reset —— 清空 store 并重新加载内置脚本
	mux.HandleFunc("/api/mock/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		resetScripts(w, r, store, builtinScripts)
	})
}

// listScripts 返回 store 中的所有 mock 脚本。
// GET /api/mock/scripts
func listScripts(w http.ResponseWriter, _ *http.Request, store llm.MockScriptStore) {
	scripts, err := store.List()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if scripts == nil {
		scripts = []llm.MockScript{}
	}
	respondJSON(w, http.StatusOK, map[string]any{"scripts": scripts})
}

// getScript 按 ID 返回单个 mock 脚本。
// GET /api/mock/scripts/{id}
func getScript(w http.ResponseWriter, _ *http.Request, store llm.MockScriptStore, id string) {
	script, err := store.Get(id)
	if err != nil {
		respondJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"script": script})
}

// saveScript 创建或更新一个 mock 脚本。若请求体未提供 ID，
// 则生成一个新的 UUID 并在保存的脚本中返回。
// POST /api/mock/scripts
func saveScript(w http.ResponseWriter, r *http.Request, store llm.MockScriptStore) {
	var script llm.MockScript
	if err := json.NewDecoder(r.Body).Decode(&script); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if script.ID == "" {
		script.ID = uuid.New().String()
	}
	saved, err := store.Save(script)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"script": saved})
}

// deleteScript 从 store 中移除一个 mock 脚本。
// DELETE /api/mock/scripts/{id}
func deleteScript(w http.ResponseWriter, _ *http.Request, store llm.MockScriptStore, id string) {
	if err := store.Delete(id); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// resetScripts 清空 store 并重新加载内置脚本。
// POST /api/mock/reset
func resetScripts(w http.ResponseWriter, _ *http.Request, store llm.MockScriptStore, builtinScripts []llm.MockScript) {
	if err := store.LoadBuiltin(builtinScripts); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"reset": true})
}

// respondJSON 把 v 编码为 JSON 并按给定 status code 写入 w。
func respondJSON(w http.ResponseWriter, status int, v map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
