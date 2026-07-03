package tool

import (
	"encoding/json"
	"fmt"
)

// Tool represents a callable tool that agents can use
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(input map[string]any) (any, error)
}

// Registry manages available tools
type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

func (r *Registry) Execute(name string, input map[string]any) (any, error) {
	tool, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return tool.Execute(input)
}

func (r *Registry) List() []Tool {
	list := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		list = append(list, tool)
	}
	return list
}

func (r *Registry) ToJSON() ([]byte, error) {
	schema := make([]map[string]any, 0)
	for _, tool := range r.tools {
		schema = append(schema, map[string]any{
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters":  tool.Parameters(),
		})
	}
	return json.Marshal(schema)
}
