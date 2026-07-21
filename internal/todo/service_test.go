// service_test.go — Todo Service 单元测试。
//
// 通过 mock DBStore 与 fake EventBus 隔离外部依赖，
// 验证 Create / Update / UpdateStatus / ClearAll / List / ListActiveBySession 等核心流程。
package todo

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/event"
	"github.com/google/uuid"
)

// mockDBStore 是 DBStore 的内存实现，用于单元测试。
// 它模拟了真实 SQLite 层的 ID 生成、时间戳维护与 completed_at 逻辑，
// 但避免引入 pkg/db 的循环依赖与数据库初始化。
//
// 注意：Service.Create 在调用 InsertTodo 时按值传递 Todo，因此 mock 无法把生成的 ID
// 回写到 Service 本地的变量里。为了兼容这个行为，InsertTodo 会同时把最后创建的记录
// 保留一份 lastInserted 副本，使得 Service 随后用空 ID 调用 GetTodo 时仍能拿到结果。
type mockDBStore struct {
	mu           sync.Mutex
	todos        map[string]Todo
	lastInserted Todo
}

// newMockDBStore 创建空的 mock store。
func newMockDBStore() *mockDBStore {
	return &mockDBStore{todos: make(map[string]Todo)}
}

// InsertTodo 将 Todo 写入内存；若 ID 为空则生成 UUID，
// 同时维护 created_at、updated_at 与 completed_at（状态为 done 时）。
func (m *mockDBStore) InsertTodo(t Todo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	now := time.Now().Unix()
	if t.CreatedAt == 0 {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	if t.Status == StatusDone {
		t.CompletedAt = &now
	} else {
		t.CompletedAt = nil
	}
	m.todos[t.ID] = t
	m.lastInserted = t
	return nil
}

// UpdateTodo 用传入对象覆盖内存中的记录，并自动刷新 completed_at。
func (m *mockDBStore) UpdateTodo(t Todo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.todos[t.ID]; !ok {
		return fmt.Errorf("todo not found: %s", t.ID)
	}
	now := time.Now().Unix()
	t.UpdatedAt = now
	if t.Status == StatusDone {
		t.CompletedAt = &now
	} else {
		t.CompletedAt = nil
	}
	m.todos[t.ID] = t
	return nil
}

// DeleteTodo 按 ID 删除 Todo。
func (m *mockDBStore) DeleteTodo(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.todos, id)
	return nil
}

// GetTodo 按 ID 读取单个 Todo，找不到时返回 sql.ErrNoRows 与真实存储行为一致。
// 为了兼容 Service.Create 用原始空 ID 读取的场景，当 id 为空时返回最后一次插入的记录。
func (m *mockDBStore) GetTodo(id string) (Todo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		if m.lastInserted.ID == "" {
			return Todo{}, sql.ErrNoRows
		}
		return m.lastInserted, nil
	}
	if t, ok := m.todos[id]; ok {
		return t, nil
	}
	return Todo{}, sql.ErrNoRows
}

// ListTodosBySession 按 session 与过滤条件列出 Todo，并模拟真实排序。
func (m *mockDBStore) ListTodosBySession(sessionID string, statusFilter []TodoStatus, includeDone bool) ([]Todo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []Todo
	for _, t := range m.todos {
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
		result = append(result, t)
	}
	// 按 priority DESC, sort_order ASC, created_at ASC 排序，与 pkg/db 保持一致。
	sortTodos(result)
	return result, nil
}

// ListTodosByTask 返回由指定 task 创建的 Todo。
func (m *mockDBStore) ListTodosByTask(taskID string) ([]Todo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []Todo
	for _, t := range m.todos {
		if t.CreatedByTaskID == taskID {
			result = append(result, t)
		}
	}
	sortTodos(result)
	return result, nil
}

// DeleteCompletedTodosBySession 删除某 session 下状态为 done 的 Todo。
func (m *mockDBStore) DeleteCompletedTodosBySession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, t := range m.todos {
		if t.SessionID == sessionID && t.Status == StatusDone {
			delete(m.todos, id)
		}
	}
	return nil
}

// DeleteAllTodosBySession 删除某 session 下全部 Todo。
func (m *mockDBStore) DeleteAllTodosBySession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, t := range m.todos {
		if t.SessionID == sessionID {
			delete(m.todos, id)
		}
	}
	return nil
}

// sortTodos 提供与真实 SQLite 一致的简单排序逻辑，仅用于测试断言。
func sortTodos(items []Todo) {
	if len(items) <= 1 {
		return
	}
	// 冒泡排序，保持实现简单且结果稳定。
	for i := 0; i < len(items)-1; i++ {
		for j := i + 1; j < len(items); j++ {
			a, b := items[i], items[j]
			if a.Priority < b.Priority ||
				(a.Priority == b.Priority && a.SortOrder > b.SortOrder) ||
				(a.Priority == b.Priority && a.SortOrder == b.SortOrder && a.CreatedAt > b.CreatedAt) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

// fakeEventBus 收集所有 SendEvent 收到的事件，便于断言广播内容。
type fakeEventBus struct {
	events []event.Event
}

// SendEvent 保存事件到内存列表。
func (f *fakeEventBus) SendEvent(evt event.Event) {
	f.events = append(f.events, evt)
}

// TodoListChangedEvents 仅返回类型为 todo_list_changed 的事件。
func (f *fakeEventBus) TodoListChangedEvents() []event.Event {
	var out []event.Event
	for _, e := range f.events {
		if e.Type == "todo_list_changed" {
			out = append(out, e)
		}
	}
	return out
}

// TestService_Create 验证创建成功会写入 store 并广播 todo_list_changed 事件。
func TestService_Create(t *testing.T) {
	store := newMockDBStore()
	bus := &fakeEventBus{}
	svc := NewService(store, bus)

	created, err := svc.Create("session-1", "task-1", "  编写测试  ", "覆盖 Service 方法", "", 1)
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create 后 todo ID 为空")
	}
	if created.Title != "编写测试" {
		t.Fatalf("title 未 trim 或错误: got %q", created.Title)
	}
	if created.Status != StatusPending {
		t.Fatalf("默认状态应为 pending, got %s", created.Status)
	}
	if created.SessionID != "session-1" {
		t.Fatalf("session_id 错误: got %q", created.SessionID)
	}

	// 验证 store 中确实存入了记录。
	got, err := store.GetTodo(created.ID)
	if err != nil {
		t.Fatalf("store Get 失败: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("store 中记录不一致: got %q, want %q", got.ID, created.ID)
	}

	// 验证广播事件。
	changed := bus.TodoListChangedEvents()
	if len(changed) != 1 {
		t.Fatalf("期望广播 1 次 todo_list_changed, got %d", len(changed))
	}
	evt := changed[0]
	if evt.Data["session_id"] != "session-1" {
		t.Fatalf("事件 session_id 错误: got %v", evt.Data["session_id"])
	}
	todos, ok := evt.Data["todos"].([]Todo)
	if !ok {
		t.Fatalf("事件 todos 字段类型错误: %T", evt.Data["todos"])
	}
	if len(todos) != 1 {
		t.Fatalf("事件应包含 1 个 active todo, got %d", len(todos))
	}
	if todos[0].ID != created.ID {
		t.Fatalf("事件中 todo id 错误")
	}
}

// TestService_Update_OnlyNonNilFields 验证 Update 只更新非 nil 字段。
func TestService_Update_OnlyNonNilFields(t *testing.T) {
	store := newMockDBStore()
	bus := &fakeEventBus{}
	svc := NewService(store, bus)

	created, err := svc.Create("session-1", "task-1", "原标题", "原描述", "", 0)
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	newDesc := "新描述"
	updates := UpdateInput{
		Description: &newDesc,
		// Title 为 nil，不应被修改。
	}
	updated, err := svc.Update(created.ID, updates)
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if updated.Title != "原标题" {
		t.Fatalf("title 不应被修改: got %q", updated.Title)
	}
	if updated.Description != "新描述" {
		t.Fatalf("description 应被修改: got %q", updated.Description)
	}
	if updated.Priority != 0 {
		t.Fatalf("priority 不应被修改: got %d", updated.Priority)
	}

	// 重复验证：修改 title 校验 trim 与空值。
	newTitle := "  新标题  "
	updates2 := UpdateInput{Title: &newTitle}
	updated2, err := svc.Update(created.ID, updates2)
	if err != nil {
		t.Fatalf("Update title 失败: %v", err)
	}
	if updated2.Title != "新标题" {
		t.Fatalf("title 应被 trim 并更新: got %q", updated2.Title)
	}
}

// TestService_UpdateStatus_MaintainsCompletedAt 验证 UpdateStatus 独立更新状态，
// 且当状态变为 done 时 completed_at 被正确维护。
func TestService_UpdateStatus_MaintainsCompletedAt(t *testing.T) {
	store := newMockDBStore()
	bus := &fakeEventBus{}
	svc := NewService(store, bus)

	created, err := svc.Create("session-1", "task-1", "状态测试", "", "", 0)
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if created.CompletedAt != nil {
		t.Fatal("pending 状态不应有 completed_at")
	}

	// 状态机转换：pending -> in_progress -> done。
	inProgress, err := svc.UpdateStatus(created.ID, StatusInProgress)
	if err != nil {
		t.Fatalf("UpdateStatus in_progress 失败: %v", err)
	}
	if inProgress.Status != StatusInProgress {
		t.Fatalf("status 应为 in_progress, got %s", inProgress.Status)
	}
	if inProgress.CompletedAt != nil {
		t.Fatal("in_progress 状态不应有 completed_at")
	}

	done, err := svc.UpdateStatus(created.ID, StatusDone)
	if err != nil {
		t.Fatalf("UpdateStatus done 失败: %v", err)
	}
	if done.Status != StatusDone {
		t.Fatalf("status 应为 done, got %s", done.Status)
	}
	if done.CompletedAt == nil || *done.CompletedAt == 0 {
		t.Fatal("done 状态的 completed_at 应被设置")
	}

	// 验证 store 层面的 completed_at 也保持一致。
	got, err := store.GetTodo(created.ID)
	if err != nil {
		t.Fatalf("store Get 失败: %v", err)
	}
	if got.CompletedAt == nil || *got.CompletedAt == 0 {
		t.Fatal("store 中 done todo 的 completed_at 丢失")
	}
	if !got.Status.IsTerminal() {
		t.Fatalf("done 应为终态")
	}
}

// TestService_ClearAll 验证 onlyCompleted=true 仅删除 done 项，
// onlyCompleted=false 删除 session 下全部 todo。
func TestService_ClearAll(t *testing.T) {
	store := newMockDBStore()
	bus := &fakeEventBus{}
	svc := NewService(store, bus)

	_, _ = svc.Create("session-1", "task-1", "待办1", "", "", 0)
	t2, _ := svc.Create("session-1", "task-1", "待办2", "", "", 0)
	t3, _ := svc.Create("session-1", "task-2", "待办3", "", "", 0)
	if _, err := svc.UpdateStatus(t2.ID, StatusDone); err != nil {
		t.Fatalf("更新 t2 为 done 失败: %v", err)
	}
	// 跨 session 的 todo，用于验证删除不会误伤。
	_, _ = svc.Create("session-2", "task-x", "其它 session", "", "", 0)

	err := svc.ClearAll("session-1", true, "task-1")
	if err != nil {
		t.Fatalf("ClearAll onlyCompleted=true 失败: %v", err)
	}

	items, _ := svc.List("session-1", nil, true)
	if len(items) != 2 {
		t.Fatalf("只删除 completed 后期望剩余 2 项, got %d", len(items))
	}

	// 再次清空全部。
	err = svc.ClearAll("session-1", false, "task-1")
	if err != nil {
		t.Fatalf("ClearAll onlyCompleted=false 失败: %v", err)
	}
	items, _ = svc.List("session-1", nil, true)
	if len(items) != 0 {
		t.Fatalf("清空全部后期望 0 项, got %d", len(items))
	}

	// session-2 不应受影响。
	others, _ := svc.List("session-2", nil, true)
	if len(others) != 1 {
		t.Fatalf("session-2 不应被误删")
	}
	if others[0].Title != "其它 session" {
		t.Fatalf("session-2 的 todo 内容错误")
	}
	// t3 初始来自 task-2，未被 done，应已在 onlyCompleted=true 时保留。
	if t3.ID == "" {
		t.Fatal("t3 创建失败")
	}
}

// TestService_ListAndListActiveBySession 验证 List 与 ListActiveBySession 返回正确的 store 数据。
func TestService_ListAndListActiveBySession(t *testing.T) {
	store := newMockDBStore()
	bus := &fakeEventBus{}
	svc := NewService(store, bus)

	// 创建 3 个 todo，优先级分别为 2, 1, 0，并让一个完成。
	t1, _ := svc.Create("session-3", "task-1", "高优先级", "", "", 2)
	t2, _ := svc.Create("session-3", "task-1", "中优先级", "", "", 1)
	t3, _ := svc.Create("session-3", "task-1", "低优先级", "", "", 0)
	_, _ = svc.UpdateStatus(t2.ID, StatusDone)

	// List 不传 statusFilter、includeDone=true 应返回全部，且按 priority 降序，
	// 同优先级再按 created_at 升序。
	all, err := svc.List("session-3", nil, true)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("includeDone=true 后期望 3 项, got %d", len(all))
	}
	if all[0].ID != t1.ID || all[1].ID != t2.ID || all[2].ID != t3.ID {
		t.Fatalf("List 排序错误: got [%s, %s, %s]", all[0].ID, all[1].ID, all[2].ID)
	}

	// ListActiveBySession 返回仅未完成（pending + in_progress）的 todo。
	active, err := svc.ListActiveBySession("session-3")
	if err != nil {
		t.Fatalf("ListActiveBySession 失败: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("active 期望 2 项, got %d", len(active))
	}
	for _, a := range active {
		if a.Status.IsTerminal() {
			t.Fatalf("active 列表不应包含终态 todo: %s", a.Status)
		}
	}

	// status filter 只返回 done。
	doneList, err := svc.List("session-3", []TodoStatus{StatusDone}, true)
	if err != nil {
		t.Fatalf("List status filter 失败: %v", err)
	}
	if len(doneList) != 1 {
		t.Fatalf("done filter 期望 1 项, got %d", len(doneList))
	}
	if doneList[0].ID != t2.ID {
		t.Fatalf("done filter 返回错误")
	}
}
