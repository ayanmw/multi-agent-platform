package tool

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/todo"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// mockTodoService 是 *todo.Service 的测试替身。
// 由于 todo.Service 是具体结构体且工具构造函数直接使用指针类型，
// 这里通过子类化思路实现：内部持有一个真正的 *todo.Service，
// 但底层使用 mock DBStore 与事件总线，从而不依赖真实 SQLite。
type mockTodoService struct {
	svc           *todo.Service
	store         *mockTodoDBStore
	bus           *mockEventBus
	broadcastHits []event.Event
}

// newMockTodoService 创建一个基于内存 mock 的 todo Service。
func newMockTodoService() *mockTodoService {
	store := newMockTodoDBStore()
	bus := &mockEventBus{}
	svc := todo.NewService(store, bus)
	return &mockTodoService{
		svc:           svc,
		store:         store,
		bus:           bus,
		broadcastHits: []event.Event{},
	}
}

// Create 委托给底层 Service。
func (m *mockTodoService) Create(sessionID, taskID, title, description, parentTodoID string, priority int) (*todo.Todo, error) {
	return m.svc.Create(sessionID, taskID, title, description, parentTodoID, priority)
}

// Update 委托给底层 Service。
func (m *mockTodoService) Update(id string, updates todo.UpdateInput) (*todo.Todo, error) {
	return m.svc.Update(id, updates)
}

// UpdateStatus 委托给底层 Service。
func (m *mockTodoService) UpdateStatus(id string, status todo.TodoStatus) (*todo.Todo, error) {
	return m.svc.UpdateStatus(id, status)
}

// Delete 委托给底层 Service。
func (m *mockTodoService) Delete(id string) error {
	return m.svc.Delete(id)
}

// Get 委托给底层 Service。
func (m *mockTodoService) Get(id string) (*todo.Todo, error) {
	return m.svc.Get(id)
}

// List 委托给底层 Service。
func (m *mockTodoService) List(sessionID string, statusFilter []todo.TodoStatus, includeDone bool) ([]todo.Todo, error) {
	return m.svc.List(sessionID, statusFilter, includeDone)
}

// ClearAll 委托给底层 Service。
func (m *mockTodoService) ClearAll(sessionID string, onlyCompleted bool, taskID string) error {
	return m.svc.ClearAll(sessionID, onlyCompleted, taskID)
}

// mockTodoDBStore 是 todo.DBStore 的内存实现，支持可预测 ID 生成。
// 注意：Service.Create 现在在 Service 侧预先生成 UUID 作为 ID 再调 InsertTodo，
// 因此 mock 只需按 t.ID 落库即可，不再需要 lastCreated 兜底。
type mockTodoDBStore struct {
	todos    map[string]*todo.Todo
	sequence int64
}

// newMockTodoDBStore 创建空的内存 Todo 存储。
func newMockTodoDBStore() *mockTodoDBStore {
	return &mockTodoDBStore{
		todos:    make(map[string]*todo.Todo),
		sequence: 0,
	}
}

// InsertTodo 将 Todo 写入内存。Service 侧已生成 ID，此处保留该 ID；
// 若调用方未提供 ID（非 Service.Create 路径），按顺序生成 "todo-N"。
func (m *mockTodoDBStore) InsertTodo(t todo.Todo) error {
	m.sequence++
	if t.ID == "" {
		t.ID = fmt.Sprintf("todo-%d", m.sequence)
	}
	now := time.Now().Unix()
	if t.CreatedAt == 0 {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	if t.Status == todo.StatusDone {
		t.CompletedAt = &now
	}
	m.todos[t.ID] = &t
	return nil
}

// UpdateTodo 覆盖内存中现有 Todo 并维护时间戳与 completed_at。
func (m *mockTodoDBStore) UpdateTodo(t todo.Todo) error {
	if _, ok := m.todos[t.ID]; !ok {
		return fmt.Errorf("todo not found: %s", t.ID)
	}
	now := time.Now().Unix()
	t.UpdatedAt = now
	if t.Status == todo.StatusDone {
		t.CompletedAt = &now
	} else {
		t.CompletedAt = nil
	}
	m.todos[t.ID] = &t
	return nil
}

// DeleteTodo 从内存中删除指定 Todo。
func (m *mockTodoDBStore) DeleteTodo(id string) error {
	if _, ok := m.todos[id]; !ok {
		return fmt.Errorf("todo not found: %s", id)
	}
	delete(m.todos, id)
	return nil
}

// GetTodo 按 ID 读取单个 Todo；找不到时返回错误。
// Service.Create 在 Service 侧已生成 ID，因此此处按 id 精确查找即可。
func (m *mockTodoDBStore) GetTodo(id string) (todo.Todo, error) {
	t, ok := m.todos[id]
	if !ok {
		return todo.Todo{}, fmt.Errorf("todo not found: %s", id)
	}
	return *t, nil
}

// ListTodosBySession 列出某 session 下的 Todo，支持状态过滤与 includeDone 开关。
func (m *mockTodoDBStore) ListTodosBySession(sessionID string, statusFilter []todo.TodoStatus, includeDone bool) ([]todo.Todo, error) {
	var items []todo.Todo
	statusMap := make(map[todo.TodoStatus]struct{})
	for _, s := range statusFilter {
		statusMap[s] = struct{}{}
	}

	for _, t := range m.todos {
		if t.SessionID != sessionID {
			continue
		}
		if len(statusFilter) > 0 {
			if _, ok := statusMap[t.Status]; !ok {
				continue
			}
		} else if !includeDone && (t.Status == todo.StatusDone || t.Status == todo.StatusCancelled) {
			continue
		}
		items = append(items, *t)
	}

	// 与数据库保持一致：按 priority DESC, sort_order ASC, created_at ASC 排序。
	sort.Slice(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority > items[j].Priority
		}
		if items[i].SortOrder != items[j].SortOrder {
			return items[i].SortOrder < items[j].SortOrder
		}
		return items[i].CreatedAt < items[j].CreatedAt
	})
	return items, nil
}

// ListTodosByTask 按创建任务 ID 列出 Todo，测试中未直接使用但需实现接口。
func (m *mockTodoDBStore) ListTodosByTask(taskID string) ([]todo.Todo, error) {
	var items []todo.Todo
	for _, t := range m.todos {
		if t.CreatedByTaskID == taskID {
			items = append(items, *t)
		}
	}
	return items, nil
}

// DeleteCompletedTodosBySession 删除某 session 下所有 done 的 Todo。
func (m *mockTodoDBStore) DeleteCompletedTodosBySession(sessionID string) error {
	for id, t := range m.todos {
		if t.SessionID == sessionID && t.Status == todo.StatusDone {
			delete(m.todos, id)
		}
	}
	return nil
}

// DeleteAllTodosBySession 删除某 session 下全部 Todo。
func (m *mockTodoDBStore) DeleteAllTodosBySession(sessionID string) error {
	for id, t := range m.todos {
		if t.SessionID == sessionID {
			delete(m.todos, id)
		}
	}
	return nil
}

// Reorder 批量更新 parent_todo_id 与 sort_order（内存实现）。
func (m *mockTodoDBStore) Reorder(sessionID string, moves []todo.TodoMove) error {
	for _, mv := range moves {
		t, ok := m.todos[mv.ID]
		if !ok {
			return fmt.Errorf("todo %s not found", mv.ID)
		}
		if t.SessionID != sessionID {
			return fmt.Errorf("todo %s does not belong to session %s", mv.ID, sessionID)
		}
		t.ParentTodoID = mv.ParentTodoID
		t.SortOrder = mv.SortOrder
		t.UpdatedAt = time.Now().Unix()
		m.todos[mv.ID] = t
	}
	return nil
}

// mockEventBus 是 todo.EventBus 的最小实现，只记录发送过的事件。
type mockEventBus struct {
	events []event.Event
}

// SendEvent 记录广播事件。
func (m *mockEventBus) SendEvent(evt event.Event) {
	m.events = append(m.events, evt)
}

// TestTodoCreateTool 验证 todo/create 的参数校验与成功创建。
func TestTodoCreateTool(t *testing.T) {
	mock := newMockTodoService()
	tool := NewTodoCreateTool(mock.svc)

	// 缺少 session_id 应返回错误。
	_, err := tool.Execute(map[string]any{
		"session_id": "",
		"title":      "测试标题",
	})
	if err == nil {
		t.Fatalf("期望空 session_id 返回错误")
	}

	// 缺少 title 应返回错误。
	_, err = tool.Execute(map[string]any{
		"session_id": "session-1",
		"title":      "",
	})
	if err == nil {
		t.Fatalf("期望空 title 返回错误")
	}

	// 成功创建应返回包含生成 ID 与 pending 状态的 Todo。
	res, err := tool.Execute(map[string]any{
		"session_id":  "session-1",
		"title":       "第一个任务",
		"description": "这是描述",
		"priority":    2,
		"task_id":     "task-1",
	})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	created, ok := res.(*todo.Todo)
	if !ok {
		t.Fatalf("返回类型不是 *todo.Todo: %T", res)
	}
	if created.ID == "" {
		t.Fatalf("期望非空 ID，实际为空")
	}
	if created.Status != todo.StatusPending {
		t.Fatalf("期望状态为 pending，实际为 %s", created.Status)
	}
	if created.Title != "第一个任务" {
		t.Fatalf("期望标题为 第一个任务，实际为 %s", created.Title)
	}
	if created.SessionID != "session-1" {
		t.Fatalf("期望 session_id 为 session-1，实际为 %s", created.SessionID)
	}
	if created.Priority != 2 {
		t.Fatalf("期望 priority 为 2，实际为 %d", created.Priority)
	}
}

// TestTodoUpdateStatusTool 验证 todo/update_status 的状态校验与成功更新。
func TestTodoUpdateStatusTool(t *testing.T) {
	mock := newMockTodoService()
	svc := mock.svc

	// 预先创建一个 pending Todo。
	created, err := svc.Create("session-1", "task-1", "状态测试", "", "", 0)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	tool := NewTodoUpdateStatusTool(svc)

	// 无效状态应返回错误。
	_, err = tool.Execute(map[string]any{
		"id":     created.ID,
		"status": "invalid_status",
	})
	if err == nil {
		t.Fatalf("期望无效状态返回错误")
	}

	// 更新为 done 应成功，且 CompletedAt 非 nil。
	res, err := tool.Execute(map[string]any{
		"id":     created.ID,
		"status": "done",
	})
	if err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}
	updated, ok := res.(*todo.Todo)
	if !ok {
		t.Fatalf("返回类型不是 *todo.Todo: %T", res)
	}
	if updated.Status != todo.StatusDone {
		t.Fatalf("期望状态为 done，实际为 %s", updated.Status)
	}
	if updated.CompletedAt == nil {
		t.Fatalf("done 状态的 CompletedAt 不应为 nil")
	}

	// 重新打开回 pending 后 CompletedAt 应为 nil。
	res, err = tool.Execute(map[string]any{
		"id":     created.ID,
		"status": "pending",
	})
	if err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}
	updated = res.(*todo.Todo)
	if updated.Status != todo.StatusPending {
		t.Fatalf("期望状态回退为 pending，实际为 %s", updated.Status)
	}
	if updated.CompletedAt != nil {
		t.Fatalf("pending 状态的 CompletedAt 应为 nil")
	}
}

// TestTodoListTool 验证 todo/list 的 session_id 校验与 include_done 过滤行为。
func TestTodoListTool(t *testing.T) {
	mock := newMockTodoService()
	svc := mock.svc

	// 准备：session-1 下 1 个 done + 1 个 pending。
	doneTodo, err := svc.Create("session-1", "task-1", "已完成任务", "", "", 0)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	pendingTodo, err := svc.Create("session-1", "task-1", "未完成任务", "", "", 0)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if _, err := svc.UpdateStatus(doneTodo.ID, todo.StatusDone); err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}

	tool := NewTodoListTool(svc)

	// 缺少 session_id 应返回错误。
	_, err = tool.Execute(map[string]any{
		"session_id": "",
	})
	if err == nil {
		t.Fatalf("期望空 session_id 返回错误")
	}

	// 默认 include_done=false 应只返回 pending 的 Todo。
	res, err := tool.Execute(map[string]any{
		"session_id": "session-1",
	})
	if err != nil {
		t.Fatalf("列出失败: %v", err)
	}
	out := res.(map[string]any)
	items := out["todos"].([]todo.Todo)
	if len(items) != 1 {
		t.Fatalf("期望默认返回 1 条未完成，实际返回 %d 条", len(items))
	}
	if items[0].ID != pendingTodo.ID {
		t.Fatalf("期望返回 %s，实际返回 %s", pendingTodo.ID, items[0].ID)
	}

	// include_done=true 应返回全部 2 条。
	res, err = tool.Execute(map[string]any{
		"session_id":   "session-1",
		"include_done": true,
	})
	if err != nil {
		t.Fatalf("列出失败: %v", err)
	}
	out = res.(map[string]any)
	items = out["todos"].([]todo.Todo)
	if len(items) != 2 {
		t.Fatalf("期望 include_done 返回 2 条，实际返回 %d 条", len(items))
	}

	// 按 done 状态过滤应只返回已完成的 1 条。
	res, err = tool.Execute(map[string]any{
		"session_id": "session-1",
		"status":     "done",
	})
	if err != nil {
		t.Fatalf("列出失败: %v", err)
	}
	out = res.(map[string]any)
	items = out["todos"].([]todo.Todo)
	if len(items) != 1 {
		t.Fatalf("期望 status=done 返回 1 条，实际返回 %d 条", len(items))
	}
	if items[0].Status != todo.StatusDone {
		t.Fatalf("期望返回 done 状态，实际为 %s", items[0].Status)
	}
}

// TestTodoClearAllTool 验证 todo/clear_all 默认只删除已完成且记录调用参数。
func TestTodoClearAllTool(t *testing.T) {
	mock := newMockTodoService()
	svc := mock.svc

	// 准备：session-1 下 1 done + 1 pending；session-2 下 1 done 用于隔离验证。
	doneA, err := svc.Create("session-1", "task-1", "已完成 A", "", "", 0)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if _, err := svc.Create("session-1", "task-1", "未完成 B", "", "", 0); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	doneC, err := svc.Create("session-2", "task-1", "已完成 C", "", "", 0)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if _, err := svc.UpdateStatus(doneA.ID, todo.StatusDone); err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}
	if _, err := svc.UpdateStatus(doneC.ID, todo.StatusDone); err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}

	tool := NewTodoClearAllTool(svc)

	// 默认 only_completed=true：应只删除 session-1 的 done，保留 pending。
	res, err := tool.Execute(map[string]any{
		"session_id": "session-1",
	})
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	out := res.(map[string]any)
	if out["session_id"] != "session-1" {
		t.Fatalf("期望返回 session-1，实际为 %v", out["session_id"])
	}
	if out["only_completed"] != true {
		t.Fatalf("默认 only_completed 应为 true")
	}

	remaining, err := svc.List("session-1", nil, true)
	if err != nil {
		t.Fatalf("列出失败: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("期望 session-1 剩余 1 条，实际为 %d 条", len(remaining))
	}
	if remaining[0].Status != todo.StatusPending {
		t.Fatalf("剩余条目应为 pending，实际为 %s", remaining[0].Status)
	}

	// only_completed=false：应清空 session-1 全部。
	_, err = tool.Execute(map[string]any{
		"session_id":     "session-1",
		"only_completed": false,
	})
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	remaining, err = svc.List("session-1", nil, true)
	if err != nil {
		t.Fatalf("列出失败: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("期望 session-1 清空，实际剩余 %d 条", len(remaining))
	}

	// 确认 session-2 数据未被误删。
	other, err := svc.List("session-2", nil, true)
	if err != nil {
		t.Fatalf("列出失败: %v", err)
	}
	if len(other) != 1 {
		t.Fatalf("期望 session-2 仍有 1 条，实际为 %d 条", len(other))
	}
}
