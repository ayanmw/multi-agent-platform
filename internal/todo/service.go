// service.go — Todo 业务服务。
//
// Service 层负责把 Todo CRUD 封装成带事件广播的业务操作。
// 它不直接依赖 pkg/db，而是通过 DBStore 接口注入持久化实现；
// 通过 EventBus 接口把状态变更广播出去，保持白盒 Agent 的可观测性。
package todo

import (
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// EventBus 是 Service 用来广播 todo_list_changed 事件的抽象。
// 与 runtime.EventBus 签名一致，但不直接依赖 runtime 包，避免循环依赖。
type EventBus interface {
	SendEvent(evt event.Event)
}

// UpdateInput 定义 Update 可选更新的字段，指针类型表示"本次是否修改"。
type UpdateInput struct {
	Title        *string
	Description  *string
	Priority     *int
	SortOrder    *int
	ParentTodoID *string
	ActiveTaskID *string
	CascadeDone  *bool
}

// Service 是 Todo 领域的业务逻辑入口。
type Service struct {
	store    *Store
	eventBus EventBus
}

// NewService 创建 Service。
// db 必须实现 DBStore；eventBus 可为 nil，此时不广播事件。
func NewService(db DBStore, bus EventBus) *Service {
	return &Service{store: NewStore(db), eventBus: bus}
}

// Create 创建一个新的 Todo。
//
// 默认状态为 pending；CreatedByTaskID 与 ActiveTaskID 都记录为当前 taskID。
func (s *Service) Create(sessionID, taskID, title, description, parentTodoID string, priority int) (*Todo, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("title is required")
	}
	t := Todo{
		SessionID:       sessionID,
		CreatedByTaskID: taskID,
		ActiveTaskID:    taskID,
		ParentTodoID:    parentTodoID,
		Title:           strings.TrimSpace(title),
		Description:     description,
		Status:          StatusPending,
		Priority:        priority,
		SortOrder:       0,
	}
	if err := s.store.Create(t); err != nil {
		return nil, fmt.Errorf("create todo: %w", err)
	}
	// InsertTodo 可能给 t 生成 ID，重新读取一次以拿到完整字段。
	created, err := s.store.Get(t.ID)
	if err != nil {
		return nil, fmt.Errorf("read created todo: %w", err)
	}
	s.broadcast(created.SessionID, taskID, taskID)
	return &created, nil
}

// Update 更新 Todo 的非状态字段。
//
// 只有 UpdateInput 中非 nil 的字段才会被更新，status 由 UpdateStatus 独立处理。
// 提供 CascadeDone 选项：当把父任务设为 done/cancelled 时，同时完成其所有子任务。
func (s *Service) Update(todoID string, updates UpdateInput) (*Todo, error) {
	t, err := s.store.Get(todoID)
	if err != nil {
		return nil, fmt.Errorf("get todo: %w", err)
	}
	if updates.Title != nil {
		trimmed := strings.TrimSpace(*updates.Title)
		if trimmed == "" {
			return nil, fmt.Errorf("title cannot be empty")
		}
		t.Title = trimmed
	}
	if updates.Description != nil {
		t.Description = *updates.Description
	}
	if updates.Priority != nil {
		t.Priority = *updates.Priority
	}
	if updates.SortOrder != nil {
		t.SortOrder = *updates.SortOrder
	}
	if updates.ParentTodoID != nil {
		t.ParentTodoID = *updates.ParentTodoID
	}
	if updates.ActiveTaskID != nil {
		t.ActiveTaskID = *updates.ActiveTaskID
	}
	t.UpdatedAt = time.Now().Unix()
	if err := s.store.Update(t); err != nil {
		return nil, fmt.Errorf("update todo: %w", err)
	}

	// 级联完成子任务：当 todo 进入终态且 CascadeDone=true 时，递归完成所有子任务。
	if updates.CascadeDone != nil && *updates.CascadeDone && t.Status.IsTerminal() {
		if err := s.cascadeStatus(t.SessionID, t.ID, t.Status); err != nil {
			return nil, fmt.Errorf("cascade done: %w", err)
		}
	}

	updated, err := s.store.Get(t.ID)
	if err != nil {
		return nil, fmt.Errorf("read updated todo: %w", err)
	}
	s.broadcast(updated.SessionID, updated.CreatedByTaskID, updated.ActiveTaskID)
	return &updated, nil
}

// cascadeStatus 递归把指定父 todo 下的所有子任务（及子任务的子任务）同步为同一终态。
func (s *Service) cascadeStatus(sessionID, parentID string, status TodoStatus) error {
	children, err := s.store.ListBySession(sessionID, nil, true)
	if err != nil {
		return err
	}
	var direct []Todo
	for _, c := range children {
		if c.ParentTodoID == parentID {
			direct = append(direct, c)
		}
	}
	for _, c := range direct {
		if !c.Status.IsTerminal() {
			c.Status = status
			c.UpdatedAt = time.Now().Unix()
			if err := s.store.Update(c); err != nil {
				return err
			}
			if err := s.cascadeStatus(sessionID, c.ID, status); err != nil {
				return err
			}
		}
	}
	return nil
}

// UpdateStatus 独立更新 Todo 状态。
//
// status 支持 pending / in_progress / done / cancelled 任意转换；
// pkg/db.UpdateTodo 会自动维护 completed_at 字段。
func (s *Service) UpdateStatus(todoID string, status TodoStatus) (*Todo, error) {
	switch status {
	case StatusPending, StatusInProgress, StatusDone, StatusCancelled:
	default:
		return nil, fmt.Errorf("invalid status: %s", status)
	}
	t, err := s.store.Get(todoID)
	if err != nil {
		return nil, fmt.Errorf("get todo: %w", err)
	}
	t.Status = status
	t.UpdatedAt = time.Now().Unix()
	if err := s.store.Update(t); err != nil {
		return nil, fmt.Errorf("update todo status: %w", err)
	}
	updated, err := s.store.Get(t.ID)
	if err != nil {
		return nil, fmt.Errorf("read updated todo: %w", err)
	}
	s.broadcast(updated.SessionID, updated.CreatedByTaskID, updated.ActiveTaskID)
	return &updated, nil
}

// Delete 删除指定 Todo。
func (s *Service) Delete(todoID string) error {
	t, err := s.store.Get(todoID)
	if err != nil {
		return fmt.Errorf("get todo: %w", err)
	}
	if err := s.store.Delete(todoID); err != nil {
		return fmt.Errorf("delete todo: %w", err)
	}
	s.broadcast(t.SessionID, t.CreatedByTaskID, t.ActiveTaskID)
	return nil
}

// Reorder 批量调整一组 Todo 的层级关系与排序位置。
//
// moves 中的每个元素指定 todo 的新 parent_todo_id 与 sort_order。
// 写入前会校验：目标 parent 必须存在、属于同一 session，并且不能形成循环依赖。
// 操作完成后广播 todo_list_changed 事件。
func (s *Service) Reorder(sessionID string, moves []TodoMove) error {
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	if len(moves) == 0 {
		return nil
	}

	// 加载当前 session 下全部 active todo 作为校验上下文。
	current, err := s.store.ListBySession(sessionID, nil, false)
	if err != nil {
		return fmt.Errorf("load todos for validation: %w", err)
	}

	// 把 moves 的修改应用到校验用的对象上，同时校验每个 move 的合法性。
	future := simulateMoves(current, moves)
	for _, m := range moves {
		// 目标 parent 非空时必须属于同一 session。
		if m.ParentTodoID != "" {
			parent, ok := future[m.ParentTodoID]
			if !ok {
				return fmt.Errorf("parent todo %s not found", m.ParentTodoID)
			}
			if parent.SessionID != sessionID {
				return fmt.Errorf("parent todo %s does not belong to session %s", m.ParentTodoID, sessionID)
			}
		}
		// 被移动的 todo 必须存在，且 seller 已在 store.Reorder 中校验 session;
		// 这里只校验循环依赖。
		if moved, ok := future[m.ID]; ok && wouldCreateCycle(m.ID, m.ParentTodoID, future) {
			return fmt.Errorf("reorder would create cycle for todo %s", moved.ID)
		}
	}

	if err := s.store.Reorder(sessionID, moves); err != nil {
		return fmt.Errorf("reorder todos: %w", err)
	}
	s.broadcast(sessionID, "", "")
	return nil
}

// simulateMoves 返回把 moves 应用到当前列表后的 id→Todo 映射，用于校验。
func simulateMoves(current []Todo, moves []TodoMove) map[string]Todo {
	byID := make(map[string]Todo, len(current))
	for _, t := range current {
		byID[t.ID] = t
	}
	for _, m := range moves {
		if t, ok := byID[m.ID]; ok {
			t.ParentTodoID = m.ParentTodoID
			t.SortOrder = m.SortOrder
			byID[t.ID] = t
		}
	}
	return byID
}

// wouldCreateCycle 判断把 draggedId 设为 targetParentId 的子任务是否会形成循环。
func wouldCreateCycle(draggedID, targetParentID string, byID map[string]Todo) bool {
	if targetParentID == "" {
		return false
	}
	current := targetParentID
	seen := make(map[string]bool)
	for current != "" {
		if current == draggedID {
			return true
		}
		if seen[current] {
			return true
		}
		seen[current] = true
		t, ok := byID[current]
		if !ok {
			break
		}
		current = t.ParentTodoID
	}
	return false
}

// GetTree 返回某 session 下所有 active（非终态）Todo 的树形结构，
// 适合前端一次性展示完整层级。
func (s *Service) GetTree(sessionID string) ([]Todo, error) {
	flat, err := s.store.ListBySession(sessionID, nil, false)
	if err != nil {
		return nil, err
	}
	return BuildTree(flat), nil
}

// Get 读取单个 Todo。
func (s *Service) Get(id string) (*Todo, error) {
	t, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// List 按 session 列出 Todo。
//
// statusFilter 非空时按指定状态过滤；否则 includeDone 决定返回全部或仅未完成。
func (s *Service) List(sessionID string, statusFilter []TodoStatus, includeDone bool) ([]Todo, error) {
	return s.store.ListBySession(sessionID, statusFilter, includeDone)
}

// ListByTask 返回与某个任务相关的 Todo（按 created_by_task_id 匹配）。
func (s *Service) ListByTask(taskID string) ([]Todo, error) {
	return s.store.ListByTask(taskID)
}

// ListActiveBySession 返回某 session 下所有 active（pending + in_progress）的 Todo。
// 供 Engine 在 system prompt 中注入当前任务列表。
func (s *Service) ListActiveBySession(sessionID string) ([]Todo, error) {
	return s.store.ListBySession(sessionID, nil, false)
}

// ClearAll 清理某 session 下的 Todo。
//
// onlyCompleted=true 时只删除 done/cancelled；否则删除全部。
// taskID 只用于事件中的 triggered_by_task_id，不参与过滤。
func (s *Service) ClearAll(sessionID string, onlyCompleted bool, taskID string) error {
	var err error
	if onlyCompleted {
		err = s.store.DeleteCompletedBySession(sessionID)
	} else {
		err = s.store.DeleteAllBySession(sessionID)
	}
	if err != nil {
		return fmt.Errorf("clear todos: %w", err)
	}
	s.broadcast(sessionID, taskID, taskID)
	return nil
}

// broadcast 在每次写入操作后广播 todo_list_changed 事件。
//
// 事件包含当前 session 的 active todos，便于前端与同 session 的 agent 同步。
func (s *Service) broadcast(sessionID, taskID, triggeredByTaskID string) {
	if s.eventBus == nil || sessionID == "" {
		return
	}
	todos, _ := s.store.ListBySession(sessionID, nil, false)
	s.eventBus.SendEvent(event.NewEventWithSubTask(
		"todo_list_changed",
		"", // todo 属于 session，不绑定某个 root task
		"",
		"todo-service",
		0,
		map[string]any{
			"session_id":           sessionID,
			"task_id":              taskID,
			"triggered_by_task_id": triggeredByTaskID,
			"todos":                todos,
		},
	))
}

// FormatActiveTodos 把 active todos 渲染成 markdown 列表字符串，供 Engine 注入 system prompt。
//
// 输出示例：
//
//	## Active TODO List for This Session
//	1. [~] 编写 API (priority: 2)
//	2. [ ] 编写测试 (priority: 1)
//	3. [x] 初始化项目 (priority: 0)
func FormatActiveTodos(todos []Todo) string {
	if len(todos) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Active TODO List for This Session\n")
	for i, t := range todos {
		marker := " "
		switch t.Status {
		case StatusDone, StatusCancelled:
			marker = "x"
		case StatusInProgress:
			marker = "~"
		}
		fmt.Fprintf(&b, "%d. [%s] %s (priority: %d)\n", i+1, marker, t.Title, t.Priority)
	}
	return b.String()
}
