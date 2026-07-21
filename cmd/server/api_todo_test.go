// api_todo_test.go — Todo REST API 路由测试。
//
// 本文件使用 httptest 直接调用 registerTodoRoutes 挂载的 handler，
// 不依赖真实 HTTP Server；通过 mock DBStore 注入 todo.Service，
// 保证用例间隔离且无需启动 SQLite。
package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/todo"
)

// mockTodoDBStore 是一个内存实现的 todo.DBStore，用于单元测试。
// 它模拟了 CRUD 行为，支持按 session/status 过滤以及 completed 清理。
type mockTodoDBStore struct {
	byID        map[string]todo.Todo
	lastCreated todo.Todo
}

// newMockTodoDBStore 创建空的 mock DBStore。
func newMockTodoDBStore() *mockTodoDBStore {
	return &mockTodoDBStore{byID: make(map[string]todo.Todo)}
}

// InsertTodo 将 todo 存入内存；若 t.ID 为空则生成 ID。
// 注意：DBStore 接口按值接收，Service.Create 在调用 s.store.Create(t) 后仍用
// 原始 t.ID（可能为空）重新读取。真实 pkg/db.InsertTodo 通过写库让 t.ID 生效；
// mock 中无法在接口内回写，因此额外在 m.lastCreated 保存最后插入记录，
// GetTodo 在空 id 时返回该记录，模拟按生成 ID 读取的效果。
func (m *mockTodoDBStore) InsertTodo(t todo.Todo) error {
	if t.ID == "" {
		t.ID = "todo-" + nextTodoID()
	}
	m.byID[t.ID] = t
	m.lastCreated = t
	return nil
}

// UpdateTodo 覆盖内存中的 todo；不存在时即创建（与 SQLite UPSERT 语义不同，但测试中只更新存在的记录）。
func (m *mockTodoDBStore) UpdateTodo(t todo.Todo) error {
	m.byID[t.ID] = t
	return nil
}

// DeleteTodo 按 id 删除 todo；不存在时返回哨兵错误。
func (m *mockTodoDBStore) DeleteTodo(id string) error {
	if _, ok := m.byID[id]; !ok {
		return errors.New("todo not found")
	}
	delete(m.byID, id)
	return nil
}

// GetTodo 按 id 读取单个 todo；不存在时返回 sql.ErrNoRows 风格错误，
// 让 handler 的 isTodoNotFound 能够识别。
// 当 id 为空时返回最近一次创建的 todo，以兼容 Service.Create 的重新读取。
func (m *mockTodoDBStore) GetTodo(id string) (todo.Todo, error) {
	if id == "" && m.lastCreated.ID != "" {
		return m.lastCreated, nil
	}
	if t, ok := m.byID[id]; ok {
		return t, nil
	}
	return todo.Todo{}, errors.New("get todo: sql: no rows in result set")
}

// ListTodosBySession 按 session 过滤，并支持 status 与 includeDone 过滤。
func (m *mockTodoDBStore) ListTodosBySession(sessionID string, statusFilter []todo.TodoStatus, includeDone bool) ([]todo.Todo, error) {
	var items []todo.Todo
	for _, t := range m.byID {
		if t.SessionID != sessionID {
			continue
		}
		if len(statusFilter) > 0 {
			matched := false
			for _, s := range statusFilter {
				if t.Status == s {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if !includeDone && t.Status.IsTerminal() {
			continue
		}
		items = append(items, t)
	}
	if items == nil {
		return []todo.Todo{}, nil
	}
	return items, nil
}

// ListTodosByTask 按 created_by_task_id 列出 todo（测试中无需使用，但接口要求实现）。
func (m *mockTodoDBStore) ListTodosByTask(taskID string) ([]todo.Todo, error) {
	var items []todo.Todo
	for _, t := range m.byID {
		if t.CreatedByTaskID == taskID {
			items = append(items, t)
		}
	}
	return items, nil
}

// DeleteCompletedTodosBySession 删除某 session 下状态为 done 的 todo。
func (m *mockTodoDBStore) DeleteCompletedTodosBySession(sessionID string) error {
	for id, t := range m.byID {
		if t.SessionID == sessionID && t.Status == todo.StatusDone {
			delete(m.byID, id)
		}
	}
	return nil
}

// DeleteAllTodosBySession 删除某 session 下全部 todo。
func (m *mockTodoDBStore) DeleteAllTodosBySession(sessionID string) error {
	for id, t := range m.byID {
		if t.SessionID == sessionID {
			delete(m.byID, id)
		}
	}
	return nil
}

// Reorder 批量更新 parent_todo_id 与 sort_order（内存实现）。
func (m *mockTodoDBStore) Reorder(sessionID string, moves []todo.TodoMove) error {
	for _, mv := range moves {
		t, ok := m.byID[mv.ID]
		if !ok {
			return errors.New("todo not found: " + mv.ID)
		}
		if t.SessionID != sessionID {
			return errors.New("todo does not belong to session")
		}
		t.ParentTodoID = mv.ParentTodoID
		t.SortOrder = mv.SortOrder
		m.byID[mv.ID] = t
	}
	return nil
}

// 确保 sql.ErrNoRows 在 mock 层也可被外部比较。
var _ = sql.ErrNoRows

// todoIDCounter 用于 mock 自动生成递增 ID，避免测试硬编码 UUID。
var todoIDCounter int

// nextTodoID 返回下一个递增 id 字符串。
func nextTodoID() string {
	todoIDCounter++
	return intToString(todoIDCounter)
}

// intToString 把 int 转成 string，辅助 nextTodoID。
func intToString(n int) string {
	// 简单实现，避免 strconv 导入；测试用 id 数量很少。
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}

// setupTodoService 创建基于 mock DBStore 的 todo.Service。
func setupTodoService(t *testing.T) *todo.Service {
	t.Helper()
	return todo.NewService(newMockTodoDBStore(), nil)
}

// TestTodoList 验证 GET /api/todos?session_id=s1 返回列表。
func TestTodoList(t *testing.T) {
	svc := setupTodoService(t)
	mux := http.NewServeMux()
	registerTodoRoutes(mux, svc)

	// 预设两条属于 s1 的 todo
	_, _ = svc.Create("s1", "task-1", "第一条", "", "", 1)
	_, _ = svc.Create("s1", "task-1", "第二条", "", "", 2)

	req := httptest.NewRequest(http.MethodGet, "/api/todos?session_id=s1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Todos []todo.Todo `json:"todos"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Todos) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(resp.Todos))
	}
}

// TestTodoCreate 验证 POST /api/todos 创建成功返回 201。
func TestTodoCreate(t *testing.T) {
	svc := setupTodoService(t)
	mux := http.NewServeMux()
	registerTodoRoutes(mux, svc)

	payload, _ := json.Marshal(map[string]any{
		"session_id": "s1",
		"task_id":    "task-1",
		"title":      "新待办",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/todos", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created todo.Todo
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created todo: %v", err)
	}
	if created.Title != "新待办" {
		t.Errorf("expected title 新待办, got %s", created.Title)
	}
	if created.Status != todo.StatusPending {
		t.Errorf("expected status pending, got %s", created.Status)
	}
}

// TestTodoGetNotFound 验证 GET /api/todos/:id 不存在时返回 404。
func TestTodoGetNotFound(t *testing.T) {
	svc := setupTodoService(t)
	mux := http.NewServeMux()
	registerTodoRoutes(mux, svc)

	req := httptest.NewRequest(http.MethodGet, "/api/todos/not-exist", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestTodoUpdateNonStatus 验证 PUT /api/todos/:id 更新非 status 字段成功。
func TestTodoUpdateNonStatus(t *testing.T) {
	svc := setupTodoService(t)
	mux := http.NewServeMux()
	registerTodoRoutes(mux, svc)

	created, _ := svc.Create("s1", "task-1", "原标题", "描述", "", 0)
	newTitle := "新标题"
	payload, _ := json.Marshal(map[string]any{
		"title": &newTitle,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/todos/"+created.ID, bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated todo.Todo
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated: %v", err)
	}
	if updated.Title != "新标题" {
		t.Errorf("expected title 新标题, got %s", updated.Title)
	}
	if updated.Status != todo.StatusPending {
		t.Errorf("expected status unchanged pending, got %s", updated.Status)
	}
}

// TestTodoUpdateWithStatusRejected 验证 PUT /api/todos/:id 携带 status 字段返回 400。
func TestTodoUpdateWithStatusRejected(t *testing.T) {
	svc := setupTodoService(t)
	mux := http.NewServeMux()
	registerTodoRoutes(mux, svc)

	created, _ := svc.Create("s1", "task-1", "标题", "", "", 0)
	payload, _ := json.Marshal(map[string]any{
		"status": "done",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/todos/"+created.ID, bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestTodoUpdateStatus 验证 PATCH /api/todos/:id/status 更新状态成功。
func TestTodoUpdateStatus(t *testing.T) {
	svc := setupTodoService(t)
	mux := http.NewServeMux()
	registerTodoRoutes(mux, svc)

	created, _ := svc.Create("s1", "task-1", "标题", "", "", 0)
	payload, _ := json.Marshal(map[string]any{
		"status": "done",
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/todos/"+created.ID+"/status", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated todo.Todo
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated: %v", err)
	}
	if updated.Status != todo.StatusDone {
		t.Errorf("expected status done, got %s", updated.Status)
	}
}

// TestTodoDelete 验证 DELETE /api/todos/:id 删除成功。
func TestTodoDelete(t *testing.T) {
	svc := setupTodoService(t)
	mux := http.NewServeMux()
	registerTodoRoutes(mux, svc)

	created, _ := svc.Create("s1", "task-1", "待删除", "", "", 0)
	req := httptest.NewRequest(http.MethodDelete, "/api/todos/"+created.ID, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Deleted bool   `json:"deleted"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Deleted || resp.ID != created.ID {
		t.Errorf("unexpected delete response: %+v", resp)
	}

	// 再次 GET 应 404
	getReq := httptest.NewRequest(http.MethodGet, "/api/todos/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", getRec.Code)
	}
}

// TestTodoClear 验证 POST /api/todos/clear 清理成功。
func TestTodoClear(t *testing.T) {
	svc := setupTodoService(t)
	mux := http.NewServeMux()
	registerTodoRoutes(mux, svc)

	// 创建一条已完成 todo
	done, _ := svc.Create("s1", "task-1", "已完成", "", "", 0)
	if _, err := svc.UpdateStatus(done.ID, todo.StatusDone); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	// 创建一条未完成 todo
	_, _ = svc.Create("s1", "task-1", "未完成", "", "", 0)

	payload, _ := json.Marshal(map[string]any{
		"session_id":     "s1",
		"only_completed": true,
		"task_id":        "task-clear",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/todos/clear", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Cleared        bool   `json:"cleared"`
		SessionID      string `json:"session_id"`
		OnlyCompleted  bool   `json:"only_completed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Cleared || resp.SessionID != "s1" || !resp.OnlyCompleted {
		t.Errorf("unexpected clear response: %+v", resp)
	}

	// 重新列出，应只剩 1 条未完成
	listReq := httptest.NewRequest(http.MethodGet, "/api/todos?session_id=s1", nil)
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var listResp struct {
		Todos []todo.Todo `json:"todos"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Todos) != 1 || listResp.Todos[0].Status == todo.StatusDone {
		t.Fatalf("expected 1 active todo, got %+v", listResp.Todos)
	}
}

// TestTodoReorder 验证 POST /api/todos/reorder 成功与循环依赖失败场景。
func TestTodoReorder(t *testing.T) {
	svc := setupTodoService(t)
	mux := http.NewServeMux()
	registerTodoRoutes(mux, svc)

	a, _ := svc.Create("s-reorder", "task-1", "A", "", "", 1)
	b, _ := svc.Create("s-reorder", "task-1", "B", "", "", 1)
	c, _ := svc.Create("s-reorder", "task-1", "C", "", "", 1)

	// 1. 合法 reorder：B 拖为 A 子任务，C 排到顶层第一。
	payload, _ := json.Marshal(map[string]any{
		"session_id": "s-reorder",
		"moves": []map[string]any{
			{"id": b.ID, "parent_todo_id": a.ID, "sort_order": 0},
			{"id": c.ID, "parent_todo_id": "", "sort_order": 0},
			{"id": a.ID, "parent_todo_id": "", "sort_order": 1},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/todos/reorder", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("合法 reorder 期望 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. 循环依赖应返回 400。
	cyclePayload, _ := json.Marshal(map[string]any{
		"session_id": "s-reorder",
		"moves": []map[string]any{
			{"id": a.ID, "parent_todo_id": b.ID, "sort_order": 0},
		},
	})
	cycleReq := httptest.NewRequest(http.MethodPost, "/api/todos/reorder", bytes.NewReader(cyclePayload))
	cycleRec := httptest.NewRecorder()
	mux.ServeHTTP(cycleRec, cycleReq)
	if cycleRec.Code != http.StatusBadRequest {
		t.Fatalf("循环依赖期望 400, got %d: %s", cycleRec.Code, cycleRec.Body.String())
	}
}

