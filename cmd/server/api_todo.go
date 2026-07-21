package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/todo"
)

// registerTodoRoutes 把 Todo 管理 REST API 路由挂载到 mux。
//
// 路由总览：
//   GET    /api/todos              — 列出 session 下 todos（?session_id=&status=&include_done=）
//   POST   /api/todos              — 创建 todo
//   POST   /api/todos/clear        — 批量清理 session 下 todos
//   GET    /api/todos/:id          — 单个 todo 详情
//   PUT    /api/todos/:id          — 更新非 status 字段
//   PATCH  /api/todos/:id/status   — 独立更新 status
//   DELETE /api/todos/:id          — 删除 todo
//
// 所有 handler 直接操作传入的 todo.Service，避免与全局变量耦合。
func registerTodoRoutes(mux *http.ServeMux, todoSvc *todo.Service) {
	// /api/todos/clear 必须比 /api/todos/ 更精确先注册，
	// 否则 Go 的 ServeMux 会把 /clear 当成 :id 处理。
	mux.HandleFunc("/api/todos/clear", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		handleClearTodos(w, r, todoSvc)
	})

	mux.HandleFunc("/api/todos", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleListTodos(w, r, todoSvc)
		case http.MethodPost:
			handleCreateTodo(w, r, todoSvc)
		default:
			writeJSONError(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/todos/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/todos/")
		if path == "" {
			writeJSONError(w, "todo ID required", http.StatusBadRequest)
			return
		}

		// PATCH /api/todos/:id/status — 独立更新状态
		if r.Method == http.MethodPatch {
			if suffix, ok := strings.CutSuffix(path, "/status"); ok {
				handleUpdateTodoStatus(w, r, todoSvc, suffix)
				return
			}
		}

		switch r.Method {
		case http.MethodGet:
			handleGetTodo(w, r, todoSvc, path)
		case http.MethodPut:
			handleUpdateTodo(w, r, todoSvc, path)
		case http.MethodDelete:
			handleDeleteTodo(w, r, todoSvc, path)
		default:
			writeJSONError(w, "GET, PUT, PATCH or DELETE only", http.StatusMethodNotAllowed)
		}
	})
}

// todoCreateRequest 是 POST /api/todos 的请求体。
// session_id 与 title 必填；其余字段可选。
type todoCreateRequest struct {
	SessionID    string `json:"session_id"`
	TaskID       string `json:"task_id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	ParentTodoID string `json:"parent_todo_id"`
	Priority     int    `json:"priority"`
}

// handleListTodos 处理 GET /api/todos?session_id=xxx&status=&include_done=。
//
// session_id 必填；status 为单个状态过滤；include_done 默认 false，
// 表示过滤掉 done/cancelled 等终态。
func handleListTodos(w http.ResponseWriter, r *http.Request, todoSvc *todo.Service) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		writeJSONError(w, "session_id is required", http.StatusBadRequest)
		return
	}

	var statusFilter []todo.TodoStatus
	if s := strings.TrimSpace(r.URL.Query().Get("status")); s != "" {
		statusFilter = []todo.TodoStatus{todo.TodoStatus(s)}
	}

	includeDone := false
	if v := strings.TrimSpace(r.URL.Query().Get("include_done")); v != "" {
		includeDone = v == "true" || v == "1" || v == "yes"
	}

	todos, err := todoSvc.List(sessionID, statusFilter, includeDone)
	if err != nil {
		writeJSONError(w, "list todos: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if todos == nil {
		todos = []todo.Todo{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"todos": todos,
	})
}

// handleCreateTodo 处理 POST /api/todos，创建一条新 todo。
// 成功后返回 201 + 完整 todo 对象。
func handleCreateTodo(w http.ResponseWriter, r *http.Request, todoSvc *todo.Service) {
	var req todoCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.SessionID) == "" {
		writeJSONError(w, "session_id is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeJSONError(w, "title is required", http.StatusBadRequest)
		return
	}

	created, err := todoSvc.Create(req.SessionID, req.TaskID, req.Title, req.Description, req.ParentTodoID, req.Priority)
	if err != nil {
		writeJSONError(w, "create todo: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(created)
}

// handleGetTodo 处理 GET /api/todos/:id，返回单个 todo 详情。
func handleGetTodo(w http.ResponseWriter, r *http.Request, todoSvc *todo.Service, id string) {
	t, err := todoSvc.Get(id)
	if err != nil {
		if isTodoNotFound(err) {
			writeJSONError(w, "todo not found", http.StatusNotFound)
			return
		}
		writeJSONError(w, "get todo: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(t)
}

// todoUpdateRequest 是 PUT /api/todos/:id 的请求体。
// 所有字段可选；指针类型表示"是否由本次请求更新"。
type todoUpdateRequest struct {
	Title        *string `json:"title"`
	Description  *string `json:"description"`
	Priority     *int    `json:"priority"`
	SortOrder    *int    `json:"sort_order"`
	ParentTodoID *string `json:"parent_todo_id"`
}

// handleUpdateTodo 处理 PUT /api/todos/:id，只更新非 status 字段。
// 请求体中若包含 status 字段则返回 400，提示使用 PATCH /status。
func handleUpdateTodo(w http.ResponseWriter, r *http.Request, todoSvc *todo.Service, id string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// 禁止通过本接口修改 status，必须走 PATCH /status。
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err == nil {
		if _, ok := raw["status"]; ok {
			writeJSONError(w, "use PATCH /api/todos/:id/status to update status", http.StatusBadRequest)
			return
		}
	}

	var req todoUpdateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	updates := todo.UpdateInput{
		Title:        req.Title,
		Description:  req.Description,
		Priority:     req.Priority,
		SortOrder:    req.SortOrder,
		ParentTodoID: req.ParentTodoID,
	}

	updated, err := todoSvc.Update(id, updates)
	if err != nil {
		if isTodoNotFound(err) {
			writeJSONError(w, "todo not found", http.StatusNotFound)
			return
		}
		writeJSONError(w, "update todo: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

// todoStatusRequest 是 PATCH /api/todos/:id/status 的请求体。
// 只接受 status 字段。
type todoStatusRequest struct {
	Status todo.TodoStatus `json:"status"`
}

// handleUpdateTodoStatus 处理 PATCH /api/todos/:id/status。
// status 必须是 pending/in_progress/done/cancelled 之一。
func handleUpdateTodoStatus(w http.ResponseWriter, r *http.Request, todoSvc *todo.Service, id string) {
	var req todoStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	switch req.Status {
	case todo.StatusPending, todo.StatusInProgress, todo.StatusDone, todo.StatusCancelled:
	default:
		writeJSONError(w, "invalid status: must be pending, in_progress, done or cancelled", http.StatusBadRequest)
		return
	}

	updated, err := todoSvc.UpdateStatus(id, req.Status)
	if err != nil {
		if isTodoNotFound(err) {
			writeJSONError(w, "todo not found", http.StatusNotFound)
			return
		}
		writeJSONError(w, "update status: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

// handleDeleteTodo 处理 DELETE /api/todos/:id。
// 成功返回 { "deleted": true, "id": "..." }。
func handleDeleteTodo(w http.ResponseWriter, r *http.Request, todoSvc *todo.Service, id string) {
	if err := todoSvc.Delete(id); err != nil {
		if isTodoNotFound(err) {
			writeJSONError(w, "todo not found", http.StatusNotFound)
			return
		}
		writeJSONError(w, "delete todo: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"deleted": true,
		"id":      id,
	})
}

// todoClearRequest 是 POST /api/todos/clear 的请求体。
// only_completed 默认 true；task_id 仅用于事件 triggered_by_task_id。
type todoClearRequest struct {
	SessionID     string `json:"session_id"`
	OnlyCompleted *bool  `json:"only_completed"`
	TaskID        string `json:"task_id"`
}

// handleClearTodos 处理 POST /api/todos/clear，批量清理某 session 的 todos。
func handleClearTodos(w http.ResponseWriter, r *http.Request, todoSvc *todo.Service) {
	var req todoClearRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.SessionID) == "" {
		writeJSONError(w, "session_id is required", http.StatusBadRequest)
		return
	}

	onlyCompleted := true
	if req.OnlyCompleted != nil {
		onlyCompleted = *req.OnlyCompleted
	}

	if err := todoSvc.ClearAll(req.SessionID, onlyCompleted, req.TaskID); err != nil {
		writeJSONError(w, "clear todos: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"cleared":        true,
		"session_id":     req.SessionID,
		"only_completed": onlyCompleted,
	})
}

// isTodoNotFound 判断错误是否表示 todo 不存在。
// Service 层目前把 sql.ErrNoRows 包装成 "get todo: sql: no rows in result set"，
// 因此用字符串后缀匹配；如果后续 Service 层暴露明确错误常量，可替换为 errors.Is。
func isTodoNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.HasSuffix(msg, "sql: no rows in result set")
}
