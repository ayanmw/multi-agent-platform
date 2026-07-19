package skill

import (
	"fmt"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
)

// skillCreateLocalTool 实现 skill/create_local Tool（别名 skill_create_local）。
type skillCreateLocalTool struct {
	store    *Store
	registry *Registry
}

type skillDeleteLocalTool struct {
	store    *Store
	registry *Registry
}

type skillListTool struct {
	registry *Registry
}

func NewSkillCreateLocalTool(store *Store, registry *Registry) tool.Tool {
	return &skillCreateLocalTool{store: store, registry: registry}
}

func NewSkillDeleteLocalTool(store *Store, registry *Registry) tool.Tool {
	return &skillDeleteLocalTool{store: store, registry: registry}
}

func NewSkillListTool(registry *Registry) tool.Tool {
	return &skillListTool{registry: registry}
}

func (t *skillCreateLocalTool) Namespace() string { return "skill" }
func (t *skillCreateLocalTool) Name() string      { return "create_local" }
func (t *skillCreateLocalTool) FullName() string  { return "skill/create_local" }
func (t *skillCreateLocalTool) Aliases() []string { return []string{"skill_create_local"} }
func (t *skillCreateLocalTool) Description() string {
	return "Create a new local editable skill with a system_prompt template. The skill is persisted to the database and registered in memory."
}
func (t *skillCreateLocalTool) Tags() []string { return []string{"skill", "management"} }
func (t *skillCreateLocalTool) Parameters() map[string]any {
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
						"name": map[string]any{"type": "string"},
						"type": map[string]any{"type": "string"},
						"required": map[string]any{"type": "boolean"},
						"default": map[string]any{"type": "any"},
						"description": map[string]any{"type": "string"},
					},
				},
			},
		},
		"required": []string{"id", "display_name", "content"},
	}
}

func (t *skillCreateLocalTool) Execute(input map[string]any) (any, error) {
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

	return map[string]any{
		"id":      s.ID,
		"created": true,
	}, nil
}

func (t *skillDeleteLocalTool) Namespace() string { return "skill" }
func (t *skillDeleteLocalTool) Name() string      { return "delete_local" }
func (t *skillDeleteLocalTool) FullName() string  { return "skill/delete_local" }
func (t *skillDeleteLocalTool) Aliases() []string { return []string{"skill_delete_local"} }
func (t *skillDeleteLocalTool) Description() string {
	return "Delete a local editable skill by id. Built-in or non-editable skills cannot be deleted."
}
func (t *skillDeleteLocalTool) Tags() []string { return []string{"skill", "management"} }
func (t *skillDeleteLocalTool) Parameters() map[string]any {
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

func (t *skillDeleteLocalTool) Execute(input map[string]any) (any, error) {
	id := getString(input, "id", "")
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}

	s, ok := t.registry.Get(id)
	if !ok {
		return nil, fmt.Errorf("skill %q not found", id)
	}
	if !s.IsLocalEditable {
		return nil, fmt.Errorf("skill %q is not local editable", id)
	}

	if t.store != nil {
		if err := t.store.Delete(id); err != nil {
			return nil, fmt.Errorf("delete skill from store: %w", err)
		}
	}
	if t.registry != nil {
		t.registry.Unregister(id)
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
		result = append(result, map[string]any{
			"id":           s.ID,
			"display_name": s.DisplayName,
			"description":  s.Description,
			"source":       string(s.Source),
			"tags":         s.Tags,
			"state":        string(s.State),
		})
	}
	return result, nil
}

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
