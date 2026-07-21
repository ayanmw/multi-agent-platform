// Package tool 为 LLM agent 提供操作 Todo 列表的工具。
//
// 这些工具位于 namespace "todo"，共 6 个：
//   - todo/create
//   - todo/update
//   - todo/update_status
//   - todo/delete
//   - todo/list
//   - todo/clear_all
//
// 所有工具通过 *todo.Service 完成业务操作，并在写入成功后自动触发
// todo_list_changed 事件广播。
package tool

import (
	"fmt"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/todo"
)

// RegisterTodoTools 将 6 个 todo 工具注册到指定 Registry。
func RegisterTodoTools(registry *Registry, svc *todo.Service) {
	if svc == nil {
		return
	}
	registry.Register(NewTodoCreateTool(svc))
	registry.Register(NewTodoUpdateTool(svc))
	registry.Register(NewTodoUpdateStatusTool(svc))
	registry.Register(NewTodoDeleteTool(svc))
	registry.Register(NewTodoListTool(svc))
	registry.Register(NewTodoClearAllTool(svc))
}

// todoNamespace 是全部 todo 工具的命名空间。
const todoNamespace = "todo"

// todoStatusSet 是 todo 状态字段允许取值的集合，用于工具输入校验。
var todoStatusSet = map[todo.TodoStatus]struct{}{
	todo.StatusPending:    {},
	todo.StatusInProgress: {},
	todo.StatusDone:       {},
	todo.StatusCancelled:  {},
}

// stringValue 从 map 中读取字符串，返回 nil 表示该键不存在或值为空字符串。
// 用于 update 工具判断哪些字段需要更新：空字符串视为“未提供”。
func stringValue(input map[string]any, key string) *string {
	v, ok := input[key].(string)
	if !ok {
		return nil
	}
	return &v
}

// intValue 从 map 中读取整数；当键不存在或类型不符时返回 nil。
// 注意：工具输入中显式的 0 会被保留。
func intValue(input map[string]any, key string) *int {
	switch v := input[key].(type) {
	case float64:
		i := int(v)
		return &i
	case int:
		return &v
	case int64:
		i := int(v)
		return &i
	default:
		return nil
	}
}

// NewTodoCreateTool 创建 "todo/create" 工具。
//
// 参数：
//   - session_id (string, required)：todo 所属的 session ID。
//   - title (string, required)：todo 标题。
//   - description (string, optional)：详细描述。
//   - parent_todo_id (string, optional)：父 todo ID，用于嵌套。
//   - priority (integer, optional)：优先级，越大越重要，默认 0。
//   - task_id (string, optional)：创建该 todo 的当前任务 ID。
//
// 返回新创建的 todo JSON。
func NewTodoCreateTool(svc *todo.Service) *BuiltinTool {
	return NewBuiltinTool(
		"create",
		todoNamespace,
		"Create a new todo item in the specified session. Returns the created todo with its generated ID and pending status.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID this todo belongs to",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Short title of the todo",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Optional detailed description",
				},
				"parent_todo_id": map[string]any{
					"type":        "string",
					"description": "Parent todo ID for nested todos",
				},
				"priority": map[string]any{
					"type":        "integer",
					"description": "Priority, higher number means more important",
				},
				"task_id": map[string]any{
					"type":        "string",
					"description": "Current task ID creating this todo",
				},
			},
			"required": []string{"session_id", "title"},
		},
		func(input map[string]any) (any, error) {
			sessionID, ok := input["session_id"].(string)
			if !ok || sessionID == "" {
				return nil, fmt.Errorf("session_id is required")
			}
			title, ok := input["title"].(string)
			if !ok || title == "" {
				return nil, fmt.Errorf("title is required")
			}
			description := getString(input, "description", "")
			parentTodoID := getString(input, "parent_todo_id", "")
			priority := getInt(input, "priority", 0)
			taskID := getString(input, "task_id", "")

			return svc.Create(sessionID, taskID, title, description, parentTodoID, priority)
		},
	).WithTags("todo")
}

// NewTodoUpdateTool 创建 "todo/update" 工具。
//
// 仅更新传入字段，不会修改 todo 的 status（应使用 todo/update_status）。
// 返回更新后的 todo JSON。
func NewTodoUpdateTool(svc *todo.Service) *BuiltinTool {
	return NewBuiltinTool(
		"update",
		todoNamespace,
		"Update an existing todo. Only provided fields are modified; status cannot be changed via this tool (use todo/update_status instead). Returns the updated todo.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Todo ID to update",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "New title",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "New description",
				},
				"priority": map[string]any{
					"type":        "integer",
					"description": "New priority",
				},
				"sort_order": map[string]any{
					"type":        "integer",
					"description": "New sort order",
				},
				"parent_todo_id": map[string]any{
					"type":        "string",
					"description": "New parent todo ID",
				},
			},
			"required": []string{"id"},
		},
		func(input map[string]any) (any, error) {
			id, ok := input["id"].(string)
			if !ok || id == "" {
				return nil, fmt.Errorf("id is required")
			}
			updates := todo.UpdateInput{
				Title:        stringValue(input, "title"),
				Description:  stringValue(input, "description"),
				Priority:     intValue(input, "priority"),
				SortOrder:    intValue(input, "sort_order"),
				ParentTodoID: stringValue(input, "parent_todo_id"),
			}
			return svc.Update(id, updates)
		},
	).WithTags("todo")
}

// NewTodoUpdateStatusTool 创建 "todo/update_status" 工具。
//
// 参数：
//   - id (string, required)：要更新的 todo ID。
//   - status (string, required)：新状态，必须为 pending、in_progress、done 或 cancelled。
//
// 返回更新后的 todo JSON。
func NewTodoUpdateStatusTool(svc *todo.Service) *BuiltinTool {
	return NewBuiltinTool(
		"update_status",
		todoNamespace,
		"Update the status of a todo. Valid statuses are pending, in_progress, done, and cancelled. Use this to mark sub-tasks as in-progress or complete.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Todo ID to update",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"pending", "in_progress", "done", "cancelled"},
					"description": "New status for the todo",
				},
			},
			"required": []string{"id", "status"},
		},
		func(input map[string]any) (any, error) {
			id, ok := input["id"].(string)
			if !ok || id == "" {
				return nil, fmt.Errorf("id is required")
			}
			statusStr, ok := input["status"].(string)
			if !ok || statusStr == "" {
				return nil, fmt.Errorf("status is required")
			}
			status := todo.TodoStatus(statusStr)
			if _, valid := todoStatusSet[status]; !valid {
				return nil, fmt.Errorf("invalid status: %s", statusStr)
			}
			return svc.UpdateStatus(id, status)
		},
	).WithTags("todo")
}

// NewTodoDeleteTool 创建 "todo/delete" 工具。
func NewTodoDeleteTool(svc *todo.Service) *BuiltinTool {
	return NewBuiltinTool(
		"delete",
		todoNamespace,
		"Delete a todo by its ID. Returns the deleted flag and ID.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Todo ID to delete",
				},
			},
			"required": []string{"id"},
		},
		func(input map[string]any) (any, error) {
			id := getString(input, "id", "")
			id = strings.TrimSpace(id)
			if id == "" {
				return nil, fmt.Errorf("id is required")
			}
			if err := svc.Delete(id); err != nil {
				return nil, err
			}
			return map[string]any{
				"deleted": true,
				"id":      id,
			}, nil
		},
	).WithTags("todo")
}

// NewTodoListTool 创建 "todo/list" 工具。
func NewTodoListTool(svc *todo.Service) *BuiltinTool {
	return NewBuiltinTool(
		"list",
		todoNamespace,
		"List todos for a session. Use include_done=true to also see completed/cancelled todos.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Required session ID",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "Optional single status filter: pending, in_progress, done, cancelled",
				},
				"include_done": map[string]any{
					"type":        "boolean",
					"description": "Include done/cancelled todos when no status filter",
				},
			},
			"required": []string{"session_id"},
		},
		func(input map[string]any) (any, error) {
			sessionID, ok := input["session_id"].(string)
			if !ok || sessionID == "" {
				return nil, fmt.Errorf("session_id is required")
			}
			statusStr, hasStatus := input["status"].(string)
			includeDone := getBool(input, "include_done", false)

			var filter []todo.TodoStatus
			if hasStatus && statusStr != "" {
				status := todo.TodoStatus(statusStr)
				if _, valid := todoStatusSet[status]; !valid {
					return nil, fmt.Errorf("invalid status filter: %s", statusStr)
				}
				filter = []todo.TodoStatus{status}
			}
			todos, err := svc.List(sessionID, filter, includeDone)
			if err != nil {
				return nil, err
			}
			return map[string]any{"todos": todos}, nil
		},
	).WithTags("todo")
}

// NewTodoClearAllTool 创建 "todo/clear_all" 工具。
func NewTodoClearAllTool(svc *todo.Service) *BuiltinTool {
	return NewBuiltinTool(
		"clear_all",
		todoNamespace,
		"Clear todos from a session. Set only_completed=true to remove only done/cancelled items; otherwise all todos in the session are removed.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID",
				},
				"only_completed": map[string]any{
					"type":        "boolean",
					"description": "If true, only remove done/cancelled todos",
				},
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task ID triggering the clear",
				},
			},
			"required": []string{"session_id"},
		},
		func(input map[string]any) (any, error) {
			sessionID, ok := input["session_id"].(string)
			if !ok || sessionID == "" {
				return nil, fmt.Errorf("session_id is required")
			}
			onlyCompleted := getBool(input, "only_completed", true)
			taskID := getString(input, "task_id", "")

			if err := svc.ClearAll(sessionID, onlyCompleted, taskID); err != nil {
				return nil, err
			}
			return map[string]any{
				"cleared":        true,
				"session_id":     sessionID,
				"only_completed": onlyCompleted,
			}, nil
		},
	).WithTags("todo")
}
