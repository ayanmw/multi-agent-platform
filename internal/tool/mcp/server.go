// Package mcp implements the Model Context Protocol client for the tool system.
//
// It connects to external MCP Servers (stdio, HTTP+SSE in the future), negotiates
// capabilities, lists tools, and registers each tool as a proxy into the shared
// tool.Registry. From the runtime's perspective, a proxy tool is identical to a
// built-in or dynamic tool.
package mcp

// ServerConfig describes how to start and connect to one MCP Server.
//
// Transport may be "stdio" (default) or "sse". For stdio, Command + Args start
// the child process; for sse, Endpoint is the HTTP URL.
type ServerConfig struct {
    Name        string            `json:"name"`
    Transport   string            `json:"transport"`
    Command     string            `json:"command,omitempty"`
    Args        []string          `json:"args,omitempty"`
    Endpoint    string            `json:"endpoint,omitempty"`
    Environment map[string]string `json:"environment,omitempty"`
    Enabled     bool              `json:"enabled"`
}

// Namespace returns the human-readable server namespace used to prefix tools.
func (c ServerConfig) Namespace() string {
    if c.Name == "" {
        return "default"
    }
    return c.Name
}

// ToolName produces the registry-safe name for a tool declared by this server.
func (c ServerConfig) ToolName(toolName string) string {
    return "mcp__" + c.Namespace() + "__" + toolName
}

// ServerCapability mirrors the initialize response from an MCP Server.
// Only fields that the client needs for routing are modeled here.
type ServerCapability struct {
    ProtocolVersion string    `json:"protocolVersion"`
    ServerInfo      ServerInfo `json:"serverInfo"`
}

// ServerInfo identifies the MCP Server.
type ServerInfo struct {
    Name    string `json:"name"`
    Version string `json:"version"`
}

// ToolDefinition represents one tool exposed by the MCP Server.
type ToolDefinition struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    InputSchema map[string]any `json:"inputSchema"`
}

// ResourceDefinition represents one resource exposed by the MCP Server.
type ResourceDefinition struct {
    URI         string `json:"uri"`
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
    MIMEType    string `json:"mimeType,omitempty"`
}

// Source describes where an MCP server config comes from.
//
// It is stored with each persisted server so the manager can load both static
// configuration and dynamically added servers from the same source of truth.
type Source string

const (
    // SourceStatic comes from environment configuration (MCP_SERVERS).
    SourceStatic Source = "static"

    // SourceDB was added dynamically at runtime and persisted in the database.
    SourceDB Source = "db"

    // SourceMarket was installed from an external MCP marketplace.
    // TODO: Phase 6 — wire marketplace adapter into Manager.Install.
    SourceMarket Source = "market"
)

// ManagedServer is the persisted/loaded view of one MCP server.
type ManagedServer struct {
    ID        string       `json:"id"`
    Source    Source       `json:"source"`
    Config    ServerConfig `json:"config"`
    Enabled   bool         `json:"enabled"`
    CreatedAt string       `json:"created_at"`
    UpdatedAt string       `json:"updated_at"`
}
