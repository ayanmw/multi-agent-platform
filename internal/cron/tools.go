// tools.go — Cron 子系统的 Agent Tools。
//
// 4 个工具，namespace="cron"，让 LLM/Agent 在运行时创建和管理定时器：
//   - cron/create   创建并启用一个定时器
//   - cron/list     列出当前定时器（可按 status 过滤）
//   - cron/delete   删除一个定时器
//   - cron/trigger  手动触发一次执行
//
// 所有工具通过 *cron.Service 完成业务操作。不提供 cron/update（YAGNI）：
// LLM 要改定时器可在 UI 删后重建。用户侧的更新走 REST API。
package cron

import (
	"encoding/json"
	"fmt"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
)

const cronNamespace = "cron"

// RegisterCronTools 把 4 个 cron 工具注册到 registry。
func RegisterCronTools(reg *tool.Registry, svc *Service) {
	if svc == nil {
		return
	}
	reg.Register(NewCronCreateTool(svc))
	reg.Register(NewCronListTool(svc))
	reg.Register(NewCronDeleteTool(svc))
	reg.Register(NewCronTriggerTool(svc))
}

// NewCronCreateTool 创建 cron/create 工具。
func NewCronCreateTool(svc *Service) tool.Tool {
	return tool.NewBuiltinTool(
		"create", cronNamespace,
		"Create a scheduled timer (cron) that triggers a new agent task, runs whitelist tools, calls a webhook, or notifies a session on a schedule. Returns the created cron with its ID.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":             map[string]any{"type": "string", "description": "Human-readable name of the timer."},
				"description":      map[string]any{"type": "string", "description": "Optional description."},
				"schedule_type":    map[string]any{"type": "string", "enum": []string{"cron", "interval", "once"}, "description": "Scheduling type."},
				"cron_expr":        map[string]any{"type": "string", "description": "6-field second-level cron expression (for schedule_type=cron), e.g. '*/30 * * * * *'; or interval like '30s'/'5m' (for interval)."},
				"once_at":          map[string]any{"type": "string", "description": "RFC3339 timestamp for schedule_type=once."},
				"timezone":         map[string]any{"type": "string", "description": "Timezone, default UTC."},
				"display_type":     map[string]any{"type": "string", "description": "Display hint: preset|interval|cron|once. Defaults to schedule_type."},
				"action_type":      map[string]any{"type": "string", "enum": []string{"start_task", "script", "webhook", "notify_session"}, "description": "What to do when the timer fires."},
				"action_payload":   map[string]any{"type": "object", "description": "Action-specific payload. start_task: {agent_id, session_id?, input, ...}. script: {tool_calls:[{tool,input}]}. webhook: {method,url,headers?,body?,timeout_seconds?}. notify_session: {session_id,message}. String fields support template vars {{.Now}} {{.Count}} {{.PrevResult}}."},
				"allow_concurrent": map[string]any{"type": "boolean", "description": "Allow overlapping executions. Default false (skip if previous still running)."},
			},
			"required": []string{"name", "schedule_type", "action_type", "action_payload"},
		},
		func(_ tool.ExecuteContext, input map[string]any) (any, error) {
			in, err := parseCreateInput(input)
			if err != nil {
				return nil, err
			}
			c, err := svc.Create(in)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	).WithTags("cron", "scheduling").WithAliases("cron_create")
}

// parseCreateInput 从 tool input map 解析 CreateInput。
func parseCreateInput(input map[string]any) (CreateInput, error) {
	in := CreateInput{
		Name:         getStringAny(input, "name"),
		Description:  getStringAny(input, "description"),
		ScheduleType: ScheduleType(getStringAny(input, "schedule_type")),
		CronExpr:     getStringAny(input, "cron_expr"),
		OnceAt:       getStringAny(input, "once_at"),
		Timezone:     getStringAny(input, "timezone"),
		DisplayType:  getStringAny(input, "display_type"),
		ActionType:   ActionType(getStringAny(input, "action_type")),
		Source:       "agent",
	}
	if b, ok := input["allow_concurrent"].(bool); ok {
		in.AllowConcurrent = b
	}
	if p, ok := input["action_payload"].(map[string]any); ok {
		in.ActionPayload = p
	}
	return in, nil
}

// getStringAny 从 map 取 string，缺失返回空串。
func getStringAny(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// NewCronListTool 创建 cron/list 工具。
func NewCronListTool(svc *Service) tool.Tool {
	return tool.NewBuiltinTool(
		"list", cronNamespace,
		"List scheduled timers. Optional status filter: enabled|disabled|paused.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "enum": []string{"enabled", "disabled", "paused"}, "description": "Optional status filter."},
			},
		},
		func(_ tool.ExecuteContext, input map[string]any) (any, error) {
			f := ListFilter{}
			if s, ok := input["status"].(string); ok {
				f.Status = s
			}
			list, err := svc.List(f)
			if err != nil {
				return nil, err
			}
			return list, nil
		},
	).WithTags("cron", "scheduling").WithAliases("cron_list")
}

// NewCronDeleteTool 创建 cron/delete 工具。
func NewCronDeleteTool(svc *Service) tool.Tool {
	return tool.NewBuiltinTool(
		"delete", cronNamespace,
		"Delete a scheduled timer by ID.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "The cron ID to delete."},
			},
			"required": []string{"id"},
		},
		func(_ tool.ExecuteContext, input map[string]any) (any, error) {
			id := getStringAny(input, "id")
			if id == "" {
				return nil, fmt.Errorf("id is required")
			}
			if err := svc.Delete(id); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": id}, nil
		},
	).WithTags("cron", "scheduling").WithAliases("cron_delete")
}

// NewCronTriggerTool 创建 cron/trigger 工具。
func NewCronTriggerTool(svc *Service) tool.Tool {
	return tool.NewBuiltinTool(
		"trigger", cronNamespace,
		"Manually trigger one execution of a scheduled timer immediately, ignoring its schedule. Optional override_input replaces start_task input for this run.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":             map[string]any{"type": "string", "description": "The cron ID to trigger."},
				"override_input": map[string]any{"type": "string", "description": "Optional. Overrides start_task input for this single run."},
			},
			"required": []string{"id"},
		},
		func(_ tool.ExecuteContext, input map[string]any) (any, error) {
			id := getStringAny(input, "id")
			if id == "" {
				return nil, fmt.Errorf("id is required")
			}
			override := getStringAny(input, "override_input")
			exec, err := svc.Trigger(id, override)
			if err != nil {
				return nil, err
			}
			return exec, nil
		},
	).WithTags("cron", "scheduling").WithAliases("cron_trigger")
}

// MarshalCron 供 tools 内部需要时序列化（保留以备将来 API 层复用）。
func MarshalCron(c *Cron) ([]byte, error) {
	return json.Marshal(c)
}
