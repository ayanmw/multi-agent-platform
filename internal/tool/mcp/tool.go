package mcp

import (
    "context"
    "fmt"
    "time"
)

// ProxyTool wraps a remote MCP tool so it can be registered in the shared
// tool.Registry. From the runtime perspective it behaves like any other tool:
// Name / Description / Parameters are advertised to the LLM, and Execute
// forwards the call to the MCP server.
//
// Tags and Aliases let the proxy participate in registry filtering and alias
// resolution just like built-in tools. The "mcp" tag is always present so
// callers can FilterByTag("mcp") to discover all MCP-provided tools.
type ProxyTool struct {
    serverName string
    def        ToolDefinition
    client     *Client
    timeout    time.Duration
}

// NewProxyTool creates a ProxyTool from a server-scoped tool definition.
//
// serverName is used to build the registry-safe unique name. The remote method
// name stays def.Name because that is what the MCP server expects in tools/call.
func NewProxyTool(serverName string, def ToolDefinition, client *Client) *ProxyTool {
    return &ProxyTool{
        serverName: serverName,
        def:        def,
        client:     client,
        timeout:    30 * time.Second,
    }
}

// Namespace returns the MCP server namespace that scopes this tool.
// The full namespace is "mcp__<server>" so proxy tools are grouped together
// and do not collide with other namespaces.
func (t *ProxyTool) Namespace() string {
    if t.serverName == "" {
        return "mcp"
    }
    return "mcp__" + t.serverName
}

// FullName returns the globally unique registry name used by the engine:
// mcp__<server>__<tool>.
func (t *ProxyTool) FullName() string {
    return ServerConfig{Name: t.serverName}.ToolName(t.def.Name)
}

// Aliases returns alternative names for this proxy tool.
// MCP proxy tools currently do not define aliases; future versions could expose
// per-tool aliases from the server's capabilities response.
func (t *ProxyTool) Aliases() []string {
    return nil
}

// Tags returns discovery tags for the proxy tool.
// The "mcp" tag is always present so callers can discover all MCP tools.
func (t *ProxyTool) Tags() []string {
    return []string{"mcp"}
}

// Name returns the globally unique tool name in the form mcp__<server>__<tool>.
// For registry compatibility Name equals FullName.
func (t *ProxyTool) Name() string {
    return t.FullName()
}

// Description returns the remote tool's description.
func (t *ProxyTool) Description() string {
    return t.def.Description
}

// Parameters returns the JSON Schema describing the tool's arguments.
func (t *ProxyTool) Parameters() map[string]any {
    return t.def.InputSchema
}

// Execute forwards the invocation to the MCP server. The input is sent as the
// tool arguments; the text content of the response is returned as a string.
func (t *ProxyTool) Execute(input map[string]any) (any, error) {
    ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
    defer cancel()

    result, err := t.client.CallTool(ctx, t.def.Name, input)
    if err != nil {
        return nil, fmt.Errorf("mcp tool %s call failed: %w", t.def.Name, err)
    }
    return result.Text, nil
}
