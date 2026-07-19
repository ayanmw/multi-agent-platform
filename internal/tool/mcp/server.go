// Package mcp 为 tool 系统实现 Model Context Protocol 客户端。
//
// 它连接外部 MCP Server（stdio，未来支持 HTTP+SSE），协商 capabilities、
// 列出 tool，并将每个 tool 作为 proxy 注册到共享的 tool.Registry。
// 从 runtime 的角度看，proxy tool 与内置或动态 tool 完全等价。
package mcp

// ServerConfig 描述如何启动并连接一个 MCP Server。
//
// Transport 可取 "stdio"（默认）或 "sse"。stdio 用 Command + Args 启动子进程；
// sse 用 Endpoint 作为 HTTP URL。
type ServerConfig struct {
    Name        string            `json:"name"`
    Transport   string            `json:"transport"`
    Command     string            `json:"command,omitempty"`
    Args        []string          `json:"args,omitempty"`
    Endpoint    string            `json:"endpoint,omitempty"`
    Environment map[string]string `json:"environment,omitempty"`
    Enabled     bool              `json:"enabled"`
}

// Namespace 返回人类可读的 server 命名空间，用于给 tool 加前缀。
func (c ServerConfig) Namespace() string {
    if c.Name == "" {
        return "default"
    }
    return c.Name
}

// ToolName 为该 server 声明的 tool 生成 registry 安全的名称。
func (c ServerConfig) ToolName(toolName string) string {
    return "mcp__" + c.Namespace() + "__" + toolName
}

// ServerCapability 镜像 MCP Server 的 initialize 响应。
// 这里只建模 client 路由所需的那几个字段。
type ServerCapability struct {
    ProtocolVersion string    `json:"protocolVersion"`
    ServerInfo      ServerInfo `json:"serverInfo"`
}

// ServerInfo 标识一个 MCP Server。
type ServerInfo struct {
    Name    string `json:"name"`
    Version string `json:"version"`
}

// ToolDefinition 表示 MCP Server 暴露的一个 tool。
type ToolDefinition struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    InputSchema map[string]any `json:"inputSchema"`
}

// ResourceDefinition 表示 MCP Server 暴露的一个 resource。
type ResourceDefinition struct {
    URI         string `json:"uri"`
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
    MIMEType    string `json:"mimeType,omitempty"`
}

// Source 描述一个 MCP server 配置的来源。
//
// 它与每个已持久化的 server 一起保存，使 manager 能从同一来源加载静态配置
// 与动态新增的 server。
type Source string

const (
	// SourceStatic 来自环境配置（MCP_SERVERS）。
	SourceStatic Source = "static"

	// SourceDB 是运行时动态添加并持久化到数据库的来源。
	SourceDB Source = "db"

	// SourceMarket 是从外部 MCP marketplace 安装的来源。
	// TODO: Phase 6 — 将 marketplace 适配器接入 Manager.Install。
	SourceMarket Source = "market"
)

// ManagedServer 是单个 MCP server 的持久化/已加载视图。
type ManagedServer struct {
    ID        string       `json:"id"`
    Source    Source       `json:"source"`
    Config    ServerConfig `json:"config"`
    Enabled   bool         `json:"enabled"`
    CreatedAt string       `json:"created_at"`
    UpdatedAt string       `json:"updated_at"`
}
