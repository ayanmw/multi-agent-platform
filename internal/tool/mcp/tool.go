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

// Name returns the globally unique tool name in the form mcp__<server>__<tool>.
func (t *ProxyTool) Name() string {
    return ServerConfig{Name: t.serverName}.ToolName(t.def.Name)
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
