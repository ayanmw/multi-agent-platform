package skill

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ayanmw/multi-agent-platform/internal/tool"
)

// SkillEventBroadcaster 是 skill 工具的事件广播抽象。
// 由 cmd/server 在注册工具时注入适配器，避免 internal/skill 直接依赖 ws / cmd/server 包。
// nil 值表示跳过广播，现有单测和旧调用方无需改动。
type SkillEventBroadcaster interface {
	BroadcastSkillEvent(eventType, skillID string, data map[string]any)
}

// SkillCreateLocalTool 实现 skill/create_local Tool（别名 skill_create_local）。
type SkillCreateLocalTool struct {
	store    *Store
	registry *Registry
	bus      SkillEventBroadcaster
}

// SkillDeleteLocalTool 实现 skill/delete_local Tool（别名 skill_delete_local）。
// 支持删除 local_db skill；对 built_in 的 shadow，删除后恢复内置版本。
type SkillDeleteLocalTool struct {
	store    *Store
	registry *Registry
	bus      SkillEventBroadcaster
}

// skillListTool 实现 skill/list Tool（别名 skill_list）。
type skillListTool struct {
	registry *Registry
}

// skillGetTool 实现 skill/get Tool（别名 skill_get）。
// 返回单个 skill 完整详情，供 LLM 在修改前查看模板与参数。
type skillGetTool struct {
	registry *Registry
}

// SkillUpdateLocalTool 实现 skill/update_local Tool（别名 skill_update_local）。
// 可更新 local_db skill；若目标是 built_in，自动 fork 为 local_db shadow 后修改。
type SkillUpdateLocalTool struct {
	store    *Store
	registry *Registry
	bus      SkillEventBroadcaster
}

// SkillEnableTool 实现 skill/enable Tool（别名 skill_enable）。
type SkillEnableTool struct {
	store    *Store
	registry *Registry
	bus      SkillEventBroadcaster
}

// SkillDisableTool 实现 skill/disable Tool（别名 skill_disable）。
type SkillDisableTool struct {
	store    *Store
	registry *Registry
	bus      SkillEventBroadcaster
}

// skillSearchTool 实现 skill/search Tool（别名 skill_search）。
// 返回摘要列表，避免把大 prompt 塞进每次 tool result。
type skillSearchTool struct {
	registry *Registry
}

// NewSkillCreateLocalTool 创建 skill/create_local 工具。
func NewSkillCreateLocalTool(store *Store, registry *Registry) tool.Tool {
	return &SkillCreateLocalTool{store: store, registry: registry}
}

// WithBus 为 SkillCreateLocalTool 注入事件广播器，不含则跳过广播。
// 返回值实现了 tool.Tool，调用方可在注册时链式调用。
func (t *SkillCreateLocalTool) WithBus(b SkillEventBroadcaster) tool.Tool {
	t.bus = b
	return t
}

// NewSkillDeleteLocalTool 创建 skill/delete_local 工具。
func NewSkillDeleteLocalTool(store *Store, registry *Registry) tool.Tool {
	return &SkillDeleteLocalTool{store: store, registry: registry}
}

// WithBus 为 SkillDeleteLocalTool 注入事件广播器，不含则跳过广播。
func (t *SkillDeleteLocalTool) WithBus(b SkillEventBroadcaster) tool.Tool {
	t.bus = b
	return t
}

// NewSkillListTool 创建 skill/list 工具。
func NewSkillListTool(registry *Registry) tool.Tool {
	return &skillListTool{registry: registry}
}

// NewSkillGetTool 创建 skill/get 工具。
func NewSkillGetTool(registry *Registry) tool.Tool {
	return &skillGetTool{registry: registry}
}

// NewSkillUpdateLocalTool 创建 skill/update_local 工具。
func NewSkillUpdateLocalTool(store *Store, registry *Registry) tool.Tool {
	return &SkillUpdateLocalTool{store: store, registry: registry}
}

// WithBus 为 SkillUpdateLocalTool 注入事件广播器，不含则跳过广播。
func (t *SkillUpdateLocalTool) WithBus(b SkillEventBroadcaster) tool.Tool {
	t.bus = b
	return t
}

// NewSkillEnableTool 创建 skill/enable 工具。
func NewSkillEnableTool(store *Store, registry *Registry) tool.Tool {
	return &SkillEnableTool{store: store, registry: registry}
}

// WithBus 为 SkillEnableTool 注入事件广播器，不含则跳过广播。
func (t *SkillEnableTool) WithBus(b SkillEventBroadcaster) tool.Tool {
	t.bus = b
	return t
}

// NewSkillDisableTool 创建 skill/disable 工具。
func NewSkillDisableTool(store *Store, registry *Registry) tool.Tool {
	return &SkillDisableTool{store: store, registry: registry}
}

// WithBus 为 SkillDisableTool 注入事件广播器，不含则跳过广播。
func (t *SkillDisableTool) WithBus(b SkillEventBroadcaster) tool.Tool {
	t.bus = b
	return t
}

// NewSkillSearchTool 创建 skill/search 工具。
func NewSkillSearchTool(registry *Registry) tool.Tool {
	return &skillSearchTool{registry: registry}
}

func (t *SkillCreateLocalTool) Namespace() string { return "skill" }
func (t *SkillCreateLocalTool) Name() string      { return "create_local" }
func (t *SkillCreateLocalTool) FullName() string  { return "skill/create_local" }
func (t *SkillCreateLocalTool) Aliases() []string { return []string{"skill_create_local"} }
func (t *SkillCreateLocalTool) Description() string {
	return "Create a new local editable skill with a system_prompt template. The skill is persisted to the database and registered in memory."
}
func (t *SkillCreateLocalTool) Tags() []string { return []string{"skill", "management"} }

// Version 返回 skill 工具的版本标识符。skill 工具默认无版本。
func (t *SkillCreateLocalTool) Version() string { return "" }

// Source 返回 skill 工具的来源。skill 工具由本地代码实现，返回 "builtin"。
func (t *SkillCreateLocalTool) Source() string { return "builtin" }

// CanonicalName 返回 Registry 使用的唯一键。无版本时等于 FullName()。
func (t *SkillCreateLocalTool) CanonicalName() string {
	if v := t.Version(); v != "" {
		return fmt.Sprintf("%s@%s", t.FullName(), v)
	}
	return t.FullName()
}
func (t *SkillCreateLocalTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "Unique skill identifier.",
			},
			"display_name": map[string]any{
				"type":        "string",
				"description": "Human-readable name shown in the UI.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Short description of what the skill does.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content of the system_prompt template. Supports {{variable}} placeholders.",
			},
			"parameters": map[string]any{
				"type":        "array",
				"description": "List of parameter definitions accepted by the skill.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":        map[string]any{"type": "string"},
						"type":        map[string]any{"type": "string"},
						"required":    map[string]any{"type": "boolean"},
						"default":     map[string]any{"type": "any"},
						"description": map[string]any{"type": "string"},
					},
				},
			},
		},
		"required": []string{"id", "display_name", "content"},
	}
}

func (t *SkillCreateLocalTool) Execute(input map[string]any) (any, error) {
	id := getString(input, "id", "")
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	if t.registry.Exists(id) {
		return nil, fmt.Errorf("skill %q already exists", id)
	}

	displayName := getString(input, "display_name", id)
	description := getString(input, "description", "")
	content := getString(input, "content", "")

	renderer := NewRenderer()
	variables := renderer.ExtractVariables(content)

	var params []SkillParameter
	if raw, ok := input["parameters"].([]any); ok {
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			param := SkillParameter{
				Name:        getString(m, "name", ""),
				Type:        getString(m, "type", "string"),
				Required:    getBool(m, "required", false),
				Default:     m["default"],
				Description: getString(m, "description", ""),
			}
			params = append(params, param)
		}
	}

	now := time.Now().Unix()
	s := Skill{
		ID:              id,
		Version:         "1.0.0",
		DisplayName:     displayName,
		Description:     description,
		Source:          SkillSourceLocalDB,
		IsLocalEditable: true,
		State:           SkillStateEnabled,
		Scope:           SkillScope(getString(input, "scope", string(SkillScopeGlobal))),
		ProjectID:       getString(input, "project_id", ""),
		Templates: []SkillTemplate{
			{
				Name:       "system_prompt",
				Content:    content,
				Variables:  variables,
				IsRequired: true,
			},
		},
		Parameters: params,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if t.store != nil {
		if err := t.store.Save(&s); err != nil {
			return nil, fmt.Errorf("save skill: %w", err)
		}
	}
	if t.registry != nil {
		t.registry.Register(s)
	}

	if t.bus != nil {
		t.bus.BroadcastSkillEvent(EventSkillCreated, s.ID, map[string]any{
			"id":     s.ID,
			"source": string(s.Source),
			"state":  string(s.State),
			"scope":  string(s.Scope),
		})
	}

	return map[string]any{
		"id":          s.ID,
		"created":     true,
		"forked_from": false,
	}, nil
}

func (t *skillGetTool) Namespace() string { return "skill" }
func (t *skillGetTool) Name() string      { return "get" }
func (t *skillGetTool) FullName() string  { return "skill/get" }
func (t *skillGetTool) Aliases() []string { return []string{"skill_get"} }
func (t *skillGetTool) Description() string {
	return "Get the full details (templates, parameters, scope, state) of a skill by id. Use this before updating."
}
func (t *skillGetTool) Tags() []string     { return []string{"skill", "management"} }
func (t *skillGetTool) Version() string    { return "" }
func (t *skillGetTool) Source() string     { return "builtin" }
func (t *skillGetTool) CanonicalName() string {
	if v := t.Version(); v != "" {
		return fmt.Sprintf("%s@%s", t.FullName(), v)
	}
	return t.FullName()
}
func (t *skillGetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "The unique id of the skill to retrieve.",
			},
		},
		"required": []string{"id"},
	}
}
func (t *skillGetTool) Execute(input map[string]any) (any, error) {
	id := getString(input, "id", "")
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	s, ok := t.registry.Get(id)
	if !ok {
		return nil, fmt.Errorf("skill %q not found", id)
	}
	data, err := skillToSummaryGO(&s)
	if err != nil {
		return nil, fmt.Errorf("serialize skill: %w", err)
	}
	return data, nil
}

func (t *SkillUpdateLocalTool) Namespace() string { return "skill" }
func (t *SkillUpdateLocalTool) Name() string      { return "update_local" }
func (t *SkillUpdateLocalTool) FullName() string  { return "skill/update_local" }
func (t *SkillUpdateLocalTool) Aliases() []string { return []string{"skill_update_local"} }
func (t *SkillUpdateLocalTool) Description() string {
	return "Update a local editable skill. If the target is a built-in skill, it is automatically forked into a local_db shadow with the same id before applying changes."
}
func (t *SkillUpdateLocalTool) Tags() []string  { return []string{"skill", "management"} }
func (t *SkillUpdateLocalTool) Version() string { return "" }
func (t *SkillUpdateLocalTool) Source() string  { return "builtin" }
func (t *SkillUpdateLocalTool) CanonicalName() string {
	if v := t.Version(); v != "" {
		return fmt.Sprintf("%s@%s", t.FullName(), v)
	}
	return t.FullName()
}
func (t *SkillUpdateLocalTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "The unique id of the skill to update.",
			},
			"updates": map[string]any{
				"type":        "object",
				"description": "Partial update fields: display_name, description, tags, templates, parameters, scope, project_id, workspace_dir.",
			},
		},
		"required": []string{"id", "updates"},
	}
}
func (t *SkillUpdateLocalTool) Execute(input map[string]any) (any, error) {
	id := getString(input, "id", "")
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	rawUpdates, ok := input["updates"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("updates is required and must be an object")
	}

	existing, ok := t.registry.Get(id)
	if !ok {
		return nil, fmt.Errorf("skill %q not found", id)
	}

	// local_file 不可通过 Tool 编辑，必须修改文件系统本身。
	if existing.Source == SkillSourceLocalFile {
		return nil, fmt.Errorf("skill %q is loaded from local file and can only be modified by editing the file", id)
	}

	// 构造副本：built_in 自动 fork 为 local_db shadow；local_db 直接修改。
	// 二次编辑已有的 local_db shadow 时，仍需保留 shadow 语义（forked_from=true）。
	updated := existing
	if existing.Source == SkillSourceBuiltIn || IsShadowOfBuiltIn(existing) {
		updated.Source = SkillSourceLocalDB
		updated.IsLocalEditable = true
		updated.SourceURL = ""
		// 保留原始 created_at 语义：fork 时继承原 built_in 的创建时间，updated_at 随后刷新。
	}

	// scope / project 权限：读取执行期变量 project_id 用于后续校验。
	// 这里先把 updates 中的值解析出来；project scope 越权在下面统一检查。
	updates := normalizeSkillUpdates(rawUpdates, &updated)

	// 作用域与 project_id 权限检查。
	callerProjectID := ""
	if v, ok := input["_project_id"].(string); ok {
		callerProjectID = v
	}
	if updates.Scope == SkillScopeProject {
		if updates.ProjectID != "" && callerProjectID != "" && updates.ProjectID != callerProjectID {
			return nil, fmt.Errorf("skill %q belongs to project %q, cannot modify from project %q", id, updates.ProjectID, callerProjectID)
		}
	}

	updated = updates
	updated.UpdatedAt = time.Now().Unix()

	if t.store != nil {
		if err := t.store.Save(&updated); err != nil {
			return nil, fmt.Errorf("save skill: %w", err)
		}
	}
	if t.registry != nil {
		t.registry.Register(updated)
	}

	if t.bus != nil {
		t.bus.BroadcastSkillEvent(EventSkillChanged, id, map[string]any{
			"id":          id,
			"source":      string(updated.Source),
			"scope":       string(updated.Scope),
			"forked_from": existing.Source == SkillSourceBuiltIn || IsShadowOfBuiltIn(existing),
		})
	}

	return map[string]any{
		"id":          id,
		"updated":     true,
		"forked_from": existing.Source == SkillSourceBuiltIn || IsShadowOfBuiltIn(existing),
		"skill":       skillToSummary(&updated),
	}, nil
}

func (t *SkillEnableTool) Namespace() string { return "skill" }
func (t *SkillEnableTool) Name() string      { return "enable" }
func (t *SkillEnableTool) FullName() string  { return "skill/enable" }
func (t *SkillEnableTool) Aliases() []string { return []string{"skill_enable"} }
func (t *SkillEnableTool) Description() string {
	return "Enable a skill by id. Built-in and local_db skills can be enabled; local_file skills cannot be changed through this tool."
}
func (t *SkillEnableTool) Tags() []string     { return []string{"skill", "management"} }
func (t *SkillEnableTool) Version() string    { return "" }
func (t *SkillEnableTool) Source() string     { return "builtin" }
func (t *SkillEnableTool) CanonicalName() string {
	if v := t.Version(); v != "" {
		return fmt.Sprintf("%s@%s", t.FullName(), v)
	}
	return t.FullName()
}
func (t *SkillEnableTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "The unique id of the skill to enable.",
			},
		},
		"required": []string{"id"},
	}
}
func (t *SkillEnableTool) Execute(input map[string]any) (any, error) {
	return toggleSkill(input, "enable", t.store, t.registry, t.bus)
}

func (t *SkillDisableTool) Namespace() string { return "skill" }
func (t *SkillDisableTool) Name() string      { return "disable" }
func (t *SkillDisableTool) FullName() string  { return "skill/disable" }
func (t *SkillDisableTool) Aliases() []string { return []string{"skill_disable"} }
func (t *SkillDisableTool) Description() string {
	return "Disable a skill by id. Built-in and local_db skills can be disabled; local_file skills cannot be changed through this tool."
}
func (t *SkillDisableTool) Tags() []string     { return []string{"skill", "management"} }
func (t *SkillDisableTool) Version() string    { return "" }
func (t *SkillDisableTool) Source() string     { return "builtin" }
func (t *SkillDisableTool) CanonicalName() string {
	if v := t.Version(); v != "" {
		return fmt.Sprintf("%s@%s", t.FullName(), v)
	}
	return t.FullName()
}
func (t *SkillDisableTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "The unique id of the skill to disable.",
			},
		},
		"required": []string{"id"},
	}
}
func (t *SkillDisableTool) Execute(input map[string]any) (any, error) {
	return toggleSkill(input, "disable", t.store, t.registry, t.bus)
}

func (t *skillSearchTool) Namespace() string { return "skill" }
func (t *skillSearchTool) Name() string      { return "search" }
func (t *skillSearchTool) FullName() string  { return "skill/search" }
func (t *skillSearchTool) Aliases() []string { return []string{"skill_search"} }
func (t *skillSearchTool) Description() string {
	return "Search registered skills by keyword in id, display_name, description, or tags. Optionally filter by source or scope. Returns summaries only."
}
func (t *skillSearchTool) Tags() []string     { return []string{"skill", "management"} }
func (t *skillSearchTool) Version() string    { return "" }
func (t *skillSearchTool) Source() string     { return "builtin" }
func (t *skillSearchTool) CanonicalName() string {
	if v := t.Version(); v != "" {
		return fmt.Sprintf("%s@%s", t.FullName(), v)
	}
	return t.FullName()
}
func (t *skillSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"q": map[string]any{
				"type":        "string",
				"description": "Keyword to search in id, display_name, description, or tags.",
			},
			"source": map[string]any{
				"type":        "string",
				"description": "Optional source filter (e.g. built_in, local_db, local_file, market, mcp).",
			},
			"scope": map[string]any{
				"type":        "string",
				"description": "Optional scope filter (global, project, session).",
			},
		},
	}
}
func (t *skillSearchTool) Execute(input map[string]any) (any, error) {
	q := getString(input, "q", "")
	sourceFilter := getString(input, "source", "")
	scopeFilter := getString(input, "scope", "")

	qLower := lower(q)
	var result []map[string]any
	for _, s := range t.registry.List(nil) {
		if sourceFilter != "" && string(s.Source) != sourceFilter {
			continue
		}
		if scopeFilter != "" && string(s.Scope) != scopeFilter {
			continue
		}
		if q != "" && !matchSkillKeywordLower(s, qLower) {
			continue
		}
		result = append(result, skillToSummary(&s))
	}
	return result, nil
}

func (t *SkillDeleteLocalTool) Namespace() string { return "skill" }
func (t *SkillDeleteLocalTool) Name() string      { return "delete_local" }
func (t *SkillDeleteLocalTool) FullName() string  { return "skill/delete_local" }
func (t *SkillDeleteLocalTool) Aliases() []string { return []string{"skill_delete_local"} }
func (t *SkillDeleteLocalTool) Description() string {
	return "Delete a local editable skill by id. Built-in skills cannot be deleted; deleting a built-in shadow restores the built-in version."
}
func (t *SkillDeleteLocalTool) Tags() []string { return []string{"skill", "management"} }
func (t *SkillDeleteLocalTool) Version() string { return "" }
func (t *SkillDeleteLocalTool) Source() string  { return "builtin" }
func (t *SkillDeleteLocalTool) CanonicalName() string {
	if v := t.Version(); v != "" {
		return fmt.Sprintf("%s@%s", t.FullName(), v)
	}
	return t.FullName()
}
func (t *SkillDeleteLocalTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "The unique id of the skill to delete.",
			},
		},
		"required": []string{"id"},
	}
}

func (t *SkillDeleteLocalTool) Execute(input map[string]any) (any, error) {
	id := getString(input, "id", "")
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}

	s, ok := t.registry.Get(id)
	if !ok {
		return nil, fmt.Errorf("skill %q not found", id)
	}
	if s.Source == SkillSourceLocalFile {
		return nil, fmt.Errorf("skill %q is loaded from local file and can only be deleted by removing the file", id)
	}
	if s.Source == SkillSourceBuiltIn {
		return nil, fmt.Errorf("skill %q is built-in and cannot be deleted; create a shadow via update_local to override it", id)
	}

	// local_db shadow of built_in：从 registry 移除 shadow，若原 built_in 仍不存在则重新注册。
	if t.store != nil {
		if err := t.store.Delete(id); err != nil {
			return nil, fmt.Errorf("delete skill from store: %w", err)
		}
	}
	var restoredBuiltinID string
	if t.registry != nil {
		t.registry.Unregister(id)
		// 从 store 删除后，若该 ID 原本是 built_in 的 shadow，需要恢复 built_in。
		if IsShadowOfBuiltIn(s) {
			for _, builtin := range DefaultBuiltins() {
				if builtin.ID == id {
					t.registry.Register(*builtin)
					restoredBuiltinID = builtin.ID
					break
				}
			}
		}
	}

	if t.bus != nil {
		t.bus.BroadcastSkillEvent(EventSkillDeleted, id, map[string]any{
			"id": id,
		})
		if restoredBuiltinID != "" {
			t.bus.BroadcastSkillEvent(EventSkillLoaded, restoredBuiltinID, map[string]any{
				"id": restoredBuiltinID,
			})
		}
	}

	return map[string]any{
		"id":      id,
		"deleted": true,
	}, nil
}

func (t *skillListTool) Namespace() string { return "skill" }
func (t *skillListTool) Name() string      { return "list" }
func (t *skillListTool) FullName() string  { return "skill/list" }
func (t *skillListTool) Aliases() []string { return []string{"skill_list"} }
func (t *skillListTool) Description() string {
	return "List registered skills with id, display_name, description, source, tags, and state. Optionally filter by source."
}
func (t *skillListTool) Tags() []string { return []string{"skill", "management"} }

// Version 返回 skill 工具的版本标识符。
func (t *skillListTool) Version() string { return "" }

// Source 返回 skill 工具的来源。
func (t *skillListTool) Source() string { return "builtin" }

// CanonicalName 返回 Registry 使用的唯一键。无版本时等于 FullName()。
func (t *skillListTool) CanonicalName() string {
	if v := t.Version(); v != "" {
		return fmt.Sprintf("%s@%s", t.FullName(), v)
	}
	return t.FullName()
}
func (t *skillListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source": map[string]any{
				"type":        "string",
				"description": "Optional source filter (e.g. 'built_in', 'local_db', 'local_file', 'market', 'mcp').",
			},
		},
	}
}

func (t *skillListTool) Execute(input map[string]any) (any, error) {
	var source *SkillSource
	if s, ok := input["source"].(string); ok && s != "" {
		src := SkillSource(s)
		source = &src
	}

	var result []map[string]any
	for _, s := range t.registry.List(source) {
		result = append(result, skillToSummary(&s))
	}
	return result, nil
}

// toggleSkill 是 enable/disable 的公共实现。
// 对 built_in 仅改内存状态（不保存到 store）；对 local_db 同步 store。
func toggleSkill(input map[string]any, action string, store *Store, registry *Registry, bus SkillEventBroadcaster) (any, error) {
	id := getString(input, "id", "")
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}

	s, ok := registry.Get(id)
	if !ok {
		return nil, fmt.Errorf("skill %q not found", id)
	}
	if s.Source == SkillSourceLocalFile {
		return nil, fmt.Errorf("skill %q is loaded from local file and can only be changed by editing the file", id)
	}

	// L11：project scope 越权检查。scope=project 且已绑定 project_id 的 skill 只能被同
	// project 的会话操作；_project_id 由 Engine 在 executeToolCall 时从 SkillVariables 注入。
	if s.Scope == SkillScopeProject && s.ProjectID != "" {
		if callerProjectID, ok := input["_project_id"].(string); ok && callerProjectID != "" {
			if callerProjectID != s.ProjectID {
				return nil, fmt.Errorf("skill %q belongs to project %q, cannot enable/disable from project %q", id, s.ProjectID, callerProjectID)
			}
		}
	}

	var target SkillState
	eventType := EventSkillEnabled
	if action == "disable" {
		target = SkillStateDisabled
		eventType = EventSkillDisabled
	} else {
		target = SkillStateEnabled
	}
	if s.State == target {
		if bus != nil {
			bus.BroadcastSkillEvent(eventType, id, map[string]any{
				"id":    id,
				"state": string(target),
			})
		}
		return map[string]any{
			"id":      id,
			"action":  action,
			"enabled": target == SkillStateEnabled,
			"state":   string(target),
		}, nil
	}

	registry.UpdateState(id, target)
	s.State = target
	if s.Source == SkillSourceLocalDB {
		// local_db shadow 需要持久化状态。
		s.UpdatedAt = time.Now().Unix()
		if store != nil {
			if err := store.Save(&s); err != nil {
				return nil, fmt.Errorf("save skill: %w", err)
			}
		}
	}

	if bus != nil {
		bus.BroadcastSkillEvent(eventType, id, map[string]any{
			"id":    id,
			"state": string(target),
		})
	}

	return map[string]any{
		"id":      id,
		"action":  action,
		"enabled": target == SkillStateEnabled,
		"state":   string(target),
	}, nil
}

// skillToSummary 返回 skill 摘要 map，用于 list/search 等工具输出，避免塞入完整 prompt。
func skillToSummary(s *Skill) map[string]any {
	return map[string]any{
		"id":           s.ID,
		"display_name": s.DisplayName,
		"description":  s.Description,
		"source":       string(s.Source),
		"scope":        string(s.Scope),
		"project_id":   s.ProjectID,
		"tags":         s.Tags,
		"state":        string(s.State),
	}
}

// skillToSummaryGO 返回完整 skill 的 JSON map，供 get / update 使用。
// 模板非常大时由 LLM 上下文长度自然限制，这里不额外截断（保持可预测）。
func skillToSummaryGO(s *Skill) (map[string]any, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	delete(m, "created_at")
	delete(m, "updated_at")
	return m, nil
}

// normalizeSkillUpdates 把 LLM 传入的 updates map 应用到 skill 副本上。
// 返回更新后的 skill，不保存。unknown 字段会被忽略。
func normalizeSkillUpdates(updates map[string]any, base *Skill) Skill {
	s := *base

	if v, ok := updates["display_name"].(string); ok {
		s.DisplayName = v
	}
	if v, ok := updates["description"].(string); ok {
		s.Description = v
	}
	if v, ok := updates["tags"].([]any); ok {
		s.Tags = toStringSlice(v)
	}
	if v, ok := updates["templates"].([]any); ok {
		s.Templates = parseTemplates(v)
	}
	if v, ok := updates["parameters"].([]any); ok {
		s.Parameters = parseParameters(v)
	}
	if v, ok := updates["scope"].(string); ok && v != "" {
		s.Scope = SkillScope(v)
	}
	if v, ok := updates["project_id"].(string); ok {
		s.ProjectID = v
	}
	if v, ok := updates["workspace_dir"].(string); ok {
		s.WorkspaceDir = v
	}
	if v, ok := updates["required_tools"].([]any); ok {
		s.RequiredTools = toStringSlice(v)
	}
	if v, ok := updates["suggested_tools"].([]any); ok {
		s.SuggestedTools = toStringSlice(v)
	}
	if v, ok := updates["permissions"].([]any); ok {
		s.Permissions = toStringSlice(v)
	}
	if v, ok := updates["content"].(string); ok && v != "" {
		// 便捷字段：直接覆盖 system_prompt 模板。
		renderer := NewRenderer()
		variables := renderer.ExtractVariables(v)
		var templates []SkillTemplate
		replaced := false
		for _, tmpl := range s.Templates {
			if tmpl.Name == "system_prompt" {
				templates = append(templates, SkillTemplate{
					Name:       "system_prompt",
					Content:    v,
					Variables:  variables,
					IsRequired: true,
				})
				replaced = true
				continue
			}
			templates = append(templates, tmpl)
		}
		if !replaced {
			templates = append([]SkillTemplate{{
				Name:       "system_prompt",
				Content:    v,
				Variables:  variables,
				IsRequired: true,
			}}, templates...)
		}
		s.Templates = templates
	}

	return s
}

func parseTemplates(raw []any) []SkillTemplate {
	var out []SkillTemplate
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, SkillTemplate{
			Name:       getString(m, "name", ""),
			Content:    getString(m, "content", ""),
			Variables:  toStringSlice(m["variables"]),
			IsRequired: getBool(m, "is_required", false),
		})
	}
	return out
}

func parseParameters(raw []any) []SkillParameter {
	var params []SkillParameter
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		param := SkillParameter{
			Name:        getString(m, "name", ""),
			Type:        getString(m, "type", "string"),
			Required:    getBool(m, "required", false),
			Default:     m["default"],
			Description: getString(m, "description", ""),
		}
		params = append(params, param)
	}
	return params
}

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// IsShadowOfBuiltIn 判断 s 是否为 built_in 的 local_db shadow：
// source 为 local_db 且 ID 命中默认内置列表。
func IsShadowOfBuiltIn(s Skill) bool {
	// local_db 且其 ID 命中默认内置列表，即视为 built_in shadow。
	if s.Source != SkillSourceLocalDB {
		return false
	}
	for _, builtin := range DefaultBuiltins() {
		if builtin.ID == s.ID {
			return true
		}
	}
	return false
}

func matchSkillKeywordLower(s Skill, q string) bool {
	if q == "" {
		return true
	}
	if containsLower(s.ID, q) || containsLower(s.DisplayName, q) || containsLower(s.Description, q) {
		return true
	}
	for _, tag := range s.Tags {
		if containsLower(tag, q) {
			return true
		}
	}
	return false
}

func containsLower(s, q string) bool {
	return q == "" || strings.Contains(strings.ToLower(s), q)
}

func lower(s string) string { return strings.ToLower(s) }

func getString(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return def
}

func getBool(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}
