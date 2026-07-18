package tool

import "fmt"

// MCPAdapter is the future interface for proxying calls to an MCP server.
// It is currently unused by the placeholder web_search tool but is retained
// so callers can inject a real adapter once MCP support lands.
type MCPAdapter interface {
	Search(query string, opts map[string]any) (any, error)
}

type noopMCPAdapter struct{}

func (noopMCPAdapter) Search(query string, opts map[string]any) (any, error) {
	return nil, fmt.Errorf("MCP provider not configured")
}

// NewNoopMCPAdapter returns an MCPAdapter that always reports "not configured".
func NewNoopMCPAdapter() MCPAdapter { return noopMCPAdapter{} }

// NewWebSearchTool creates an MCP placeholder tool named "mcp/web_search".
//
// Parameters:
//   - query (string, required): Search query.
//   - opts  (object,  optional): Future search options.
func NewWebSearchTool(adapter MCPAdapter) *BuiltinTool {
	return NewBuiltinTool(
		"web_search",
		"mcp",
		"Search the web via an MCP search provider. This is a placeholder: real search is not yet configured.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
				"opts": map[string]any{
					"type":        "object",
					"description": "Future search options",
				},
			},
			"required": []string{"query"},
		},
		func(input map[string]any) (any, error) { return webSearchExecutor(adapter, input) },
	).WithTags("network", "mcp")
}

// webSearchExecutor returns a not_implemented status. The adapter argument is
// intentionally unused until MCP integration is wired up.
func webSearchExecutor(adapter MCPAdapter, input map[string]any) (any, error) {
	query := getString(input, "query", "")
	if query == "" {
		return nil, fmt.Errorf("query required")
	}
	return map[string]any{
		"status":  "not_implemented",
		"message": "web_search requires an MCP search provider (not yet configured)",
		"query":   query,
	}, nil
}
