package tool

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Tool represents a callable tool that agents can use. Every tool belongs to an
// optional namespace and carries a set of tags for discovery and filtering.
// The registry keys tools by their FullName (namespace/name or just name when
// namespace is empty).
type Tool interface {
	// Namespace returns the tool's namespace. Empty means the tool lives in the
	// global namespace and its FullName equals its Name.
	Namespace() string
	// Name returns the tool's short identifier, unique within its namespace.
	Name() string
	// FullName returns the fully-qualified identifier used by the Registry:
	// "namespace/name" when namespace is non-empty, otherwise "name".
	FullName() string
	// Aliases returns alternative names that should resolve to this tool. Aliases
	// share the same namespace as the primary FullName and are added to the
	// registry so searches can find the tool under common synonyms (e.g.
	// "web_fetch" for "core/fetch_url").
	Aliases() []string
	// Description returns a human-readable explanation of what the tool does.
	Description() string
	// Parameters returns a JSON Schema describing the expected input shape.
	Parameters() map[string]any
	// Tags returns a list of labels used for categorization and filtering.
	Tags() []string
	// Execute runs the tool with the given input map and returns the result.
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

// NewRegistry creates an empty Registry with no registered tools.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
		order: make([]string, 0),
	}
}

// Register adds a tool to the registry. If another tool with the same FullName
// is already present, it is silently overwritten. Any Aliases defined by the
// tool are also registered and point to the same Tool instance.
func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registerLocked(tool)
}

// registerLocked registers a tool and its aliases under the registry lock.
func (r *Registry) registerLocked(tool Tool) {
	name := tool.FullName()
	if _, exists := r.tools[name]; !exists {
		r.order = append(r.order, name)
	}
	r.tools[name] = tool
	for _, alias := range tool.Aliases() {
		if alias == "" || alias == name {
			continue
		}
		fullAlias := alias
		if tool.Namespace() != "" && !strings.Contains(alias, "/") {
			fullAlias = tool.Namespace() + "/" + alias
		}
		if _, exists := r.tools[fullAlias]; !exists {
			r.order = append(r.order, fullAlias)
		}
		r.tools[fullAlias] = tool
	}
}

// Execute runs the tool identified by its FullName with the provided input.
func (r *Registry) Execute(name string, input map[string]any) (any, error) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return tool.Execute(input)
}

// List returns a snapshot of all registered tools, optionally including aliases.
// The returned slice is a copy and is safe to iterate without holding the
// registry lock. When includeAliases is false (default for LLM tool definitions),
// aliases are omitted so duplicate function definitions are not sent to the
// model. When true, callers receive every registered entry including aliases.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]Tool, 0, len(r.tools))
	seen := make(map[Tool]struct{})
	// Iterate in registration order for deterministic tool definitions sent to
	// the LLM. Map iteration order is intentionally randomized in Go, so we
	// must use the order slice rather than ranging over r.tools.
	for _, name := range r.order {
		if tool, ok := r.tools[name]; ok {
			if _, exists := seen[tool]; !exists {
				list = append(list, tool)
				seen[tool] = struct{}{}
			}
		}
	}
	return list
}

// ListAll returns all registered tool entries including aliases. This is useful
// for discovery APIs where users may search by alias.
func (r *Registry) ListAll() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		if tool, ok := r.tools[name]; ok {
			list = append(list, tool)
		}
	}
	return list
}

// Unregister removes a tool from the registry by its FullName.
// Returns an error if the tool is not found, or if the tool is built-in
// (built-in tools cannot be removed via the Registry; use IsBuiltin to check).
// Note: unregistering a primary name also removes all aliases that point to it.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.IsBuiltin(name) {
		return fmt.Errorf("cannot unregister built-in tool: %s", name)
	}
	tool, ok := r.tools[name]
	if !ok {
		return fmt.Errorf("tool not found: %s", name)
	}
	// Remove primary name and all registered aliases pointing to this tool.
	delete(r.tools, name)
	for _, alias := range tool.Aliases() {
		fullAlias := alias
		if tool.Namespace() != "" && !strings.Contains(alias, "/") {
			fullAlias = tool.Namespace() + "/" + alias
		}
		delete(r.tools, fullAlias)
	}
	// Keep order slice as-is: stale names are ignored by List().
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

// ToJSON serializes every registered tool into a JSON array. Each entry contains
// the tool's namespace, name, full name, description, parameters, and tags.
func (r *Registry) ToJSON() ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	schema := make([]map[string]any, 0, len(r.tools))
	for _, name := range r.order {
		if tool, ok := r.tools[name]; ok {
			schema = append(schema, map[string]any{
				"namespace":   tool.Namespace(),
				"name":        tool.Name(),
				"full_name":   tool.FullName(),
				"description": tool.Description(),
				"parameters":  tool.Parameters(),
				"tags":        tool.Tags(),
			})
		}
	}
	return json.Marshal(schema)
}

// ToolTags returns the tags for the tool registered under the given name,
// or nil if no such tool exists. This is used by the Harness TagPolicyRule to
// enforce TaskContract permissions without importing the concrete BuiltinTool type.
func (r *Registry) ToolTags(name string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	if !ok {
		return nil
	}
	return tool.Tags()
}

// ToolMetadata returns the namespace, description, and tags for the tool
// registered under the given name. It is used by the Engine to emit
// authoritative tool metadata in tool_call_started and approval events.
func (r *Registry) ToolMetadata(name string) (namespace, description string, tags []string, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, exists := r.tools[name]
	if !exists {
		return "", "", nil, false
	}
	return tool.Namespace(), tool.Description(), tool.Tags(), true
}

// Names returns the short Name() values for the provided tools, preserving order.
func Names(tools []Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name())
	}
	return out
}

// FilterByTag returns the subset of tools whose Tags() contain the given tag.
func FilterByTag(tools []Tool, tag string) []Tool {
	out := make([]Tool, 0)
	for _, t := range tools {
		if slices.Contains(t.Tags(), tag) {
			out = append(out, t)
		}
	}
	return out
}
