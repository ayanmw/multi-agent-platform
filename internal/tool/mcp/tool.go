package mcp

import (
    "context"
    "fmt"
    "time"
)

// ProxyTool 包装一个远端 MCP tool，使其可被注册到共享 tool.Registry。
// 从 runtime 视角看它与其他 tool 行为一致：Name / Description / Parameters
// 会被公告给 LLM，Execute 则将调用转发给 MCP server。
//
// Tags 和 Aliases 让 proxy 能像内置 tool 一样参与 registry 的过滤与别名
// 解析。"mcp" tag 始终存在，调用方可通过 FilterByTag("mcp") 发现所有 MCP
// 提供的 tool。
type ProxyTool struct {
    serverName string
    def        ToolDefinition
    client     *Client
    timeout    time.Duration
}

// NewProxyTool 根据 server 作用域下的 tool 定义创建一个 ProxyTool。
//
// serverName 用于构造 registry 安全的唯一名称。远端方法名仍保持 def.Name，
// 因为那是 MCP server 在 tools/call 中期望的值。
func NewProxyTool(serverName string, def ToolDefinition, client *Client) *ProxyTool {
    return &ProxyTool{
        serverName: serverName,
        def:        def,
        client:     client,
        timeout:    30 * time.Second,
    }
}

// Namespace 返回作用域化该 tool 的 MCP server 命名空间。
// 完整命名空间为 "mcp__<server>"，使 proxy tool 被归到一起且不与其它命名空间冲突。
func (t *ProxyTool) Namespace() string {
    if t.serverName == "" {
        return "mcp"
    }
    return "mcp__" + t.serverName
}

// FullName 返回 engine 使用的全局唯一 registry 名称：
// mcp__<server>__<tool>。
func (t *ProxyTool) FullName() string {
    return ServerConfig{Name: t.serverName}.ToolName(t.def.Name)
}

// Aliases 返回该 proxy tool 的别名。
// MCP proxy tool 目前不定义别名；未来版本可从 server capabilities 响应中
// 暴露每个 tool 的别名。
func (t *ProxyTool) Aliases() []string {
    return nil
}

// Tags 返回该 proxy tool 的发现用 tag。
// "mcp" tag 始终存在，调用方可据此发现所有 MCP tool。
func (t *ProxyTool) Tags() []string {
    return []string{"mcp"}
}

// Name 返回形如 mcp__<server>__<tool> 的全局唯一 tool 名称。
// 为兼容 registry，Name 等同于 FullName。
func (t *ProxyTool) Name() string {
    return t.FullName()
}

// Description 返回远端 tool 的描述。
func (t *ProxyTool) Description() string {
    return t.def.Description
}

// Parameters 返回描述该 tool 参数的 JSON Schema。
func (t *ProxyTool) Parameters() map[string]any {
    return t.def.InputSchema
}

// Execute 把调用转发给 MCP server。input 作为 tool arguments 发送；
// 响应中的文本内容以字符串形式返回。
func (t *ProxyTool) Execute(input map[string]any) (any, error) {
    ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
    defer cancel()

    result, err := t.client.CallTool(ctx, t.def.Name, input)
    if err != nil {
        return nil, fmt.Errorf("mcp tool %s call failed: %w", t.def.Name, err)
    }
    return result.Text, nil
}
