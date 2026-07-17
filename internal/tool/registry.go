package tool

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Tool represents a callable tool that agents can use
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(input map[string]any) (any, error)
}

// Registry manages available tools. It is safe for concurrent use by multiple
// goroutines. Built-in tools cannot be unregistered at the Registry level;
// callers can use IsBuiltin to check before attempting Unregister.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	// order preserves registration order so List() returns a deterministic
	// sequence. The slice is append-only; re-registration of an existing tool
	// keeps its original position to keep tool indices stable across multiple
	// registration calls.
	order []string
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
		order: make([]string, 0),
	}
}

func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := tool.Name()
	if _, exists := r.tools[name]; !exists {
		r.order = append(r.order, name)
	}
	r.tools[name] = tool
}

func (r *Registry) Execute(name string, input map[string]any) (any, error) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return tool.Execute(input)
}

func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]Tool, 0, len(r.tools))
	// Iterate in registration order for deterministic tool definitions sent to
	// the LLM. Map iteration order is intentionally randomized in Go, so we
	// must use the order slice rather than ranging over r.tools.
	for _, name := range r.order {
		if tool, ok := r.tools[name]; ok {
			list = append(list, tool)
		}
	}
	return list
}

// Unregister removes a tool from the registry by name.
// Returns an error if the tool is not found, or if the tool is built-in
// (built-in tools cannot be removed via the Registry; use IsBuiltin to check).
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.IsBuiltin(name) {
		return fmt.Errorf("cannot unregister built-in tool: %s", name)
	}
	if _, ok := r.tools[name]; !ok {
		return fmt.Errorf("tool not found: %s", name)
	}
	delete(r.tools, name)
	return nil
}

// IsBuiltin returns true if the given tool name is one of the built-in tools
// (run_shell, write_file, read_file). Built-in tools cannot be deleted via the
// dynamic tool registration API.
func (r *Registry) IsBuiltin(name string) bool {
	switch name {
	case "run_shell", "write_file", "read_file":
		return true
	}
	return false
}

func (r *Registry) ToJSON() ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	schema := make([]map[string]any, 0, len(r.tools))
	for _, name := range r.order {
		if tool, ok := r.tools[name]; ok {
			schema = append(schema, map[string]any{
				"name":        tool.Name(),
				"description": tool.Description(),
				"parameters":  tool.Parameters(),
			})
		}
	}
	return json.Marshal(schema)
}
