package mcp

import (
    "context"
    "fmt"
    "sync"

    "github.com/anmingwei/multi-agent-platform/internal/tool"
)

// Loader 将外部 MCP Server 与共享的 tool.Registry 桥接起来。
//
// 对于每个 ServerConfig，它会建立 transport、完成 MCP 握手、发现 tool，
// 并以 mcp__<server>__<tool> 命名空间注册 proxy tool。Loader 持有这些连接的
// 生命周期，以便在不泄漏子进程的前提下优雅关闭。
type Loader struct {
    registry *tool.Registry
    mu       sync.RWMutex
    // connections 按配置名称跟踪处于活跃状态的 server。
    connections map[string]*serverConnection
}

// serverConnection 保存一个已加载 MCP server 的运行时状态。
type serverConnection struct {
    config    ServerConfig
    transport Transport
    client    *Client
    tools     []ToolDefinition
}

// NewLoader 创建一个绑定到指定 registry 的 loader。
func NewLoader(registry *tool.Registry) *Loader {
    return &Loader{
        registry:    registry,
        connections: make(map[string]*serverConnection),
    }
}

// LoadServer 连接单个 MCP server，协商 capabilities、列出 tool，并将每个
// 发现的 tool 作为 ProxyTool 注册到 registry。
//
// 如果同名 server 之前已加载，则会关闭旧连接并由新注册的 tool 覆盖旧的。
// 处于 disabled 状态的 server 会被静默跳过。
func (l *Loader) LoadServer(ctx context.Context, cfg ServerConfig) error {
    if !cfg.Enabled {
        return nil
    }

    tr, err := newTransport(cfg)
    if err != nil {
        return err
    }
    return l.load(ctx, cfg, tr)
}

// LoadServerWithTransport 是一个测试钩子，用于注入预构建的 Transport。
func (l *Loader) LoadServerWithTransport(ctx context.Context, cfg ServerConfig, tr Transport) error {
    return l.load(ctx, cfg, tr)
}

func (l *Loader) load(ctx context.Context, cfg ServerConfig, tr Transport) error {
    // 关闭该 server 名称下任何已有的连接。
    _ = l.UnloadServer(cfg.Name)

    if err := tr.Start(ctx); err != nil {
        _ = tr.Close()
        return fmt.Errorf("start transport for %s: %w", cfg.Name, err)
    }

    client := NewClient(tr)
    if _, err := client.Initialize(ctx); err != nil {
        _ = tr.Close()
        return fmt.Errorf("initialize %s: %w", cfg.Name, err)
    }

    defs, err := client.ListTools(ctx)
    if err != nil {
        _ = tr.Close()
        return fmt.Errorf("list tools for %s: %w", cfg.Name, err)
    }

    conn := &serverConnection{
        config:    cfg,
        transport: tr,
        client:    client,
        tools:     defs,
    }

    l.mu.Lock()
    l.connections[cfg.Name] = conn
    l.mu.Unlock()

    for _, def := range defs {
        proxy := NewProxyTool(cfg.Namespace(), def, client)
        l.registry.Register(proxy)
    }

    return nil
}

// LoadedServerNames 返回当前已加载 MCP server 的名称列表。
func (l *Loader) LoadedServerNames() []string {
    l.mu.RLock()
    defer l.mu.RUnlock()
    names := make([]string, 0, len(l.connections))
    for name := range l.connections {
        names = append(names, name)
    }
    return names
}

// UnloadServer 关闭指定名称 server 的 transport，并从 registry 中移除其
// proxy tool。
func (l *Loader) UnloadServer(name string) error {
    l.mu.Lock()
    conn, ok := l.connections[name]
    if ok {
        delete(l.connections, name)
    }
    l.mu.Unlock()

    if !ok {
        return fmt.Errorf("server not loaded: %s", name)
    }

    // 注销该 server 公告的所有 tool。
    cfg := conn.config
    for _, def := range conn.tools {
        toolName := cfg.ToolName(def.Name)
        // 忽略注销时可能出现的错误：这些 tool 可能已被动态 API 提前移除。
        _ = l.registry.Unregister(toolName)
    }

    if conn.transport != nil {
        _ = conn.transport.Close()
    }
    return nil
}

// LoadedNames 返回当前已加载 MCP server 的名称列表。
func (l *Loader) LoadedNames() []string {
    l.mu.RLock()
    defer l.mu.RUnlock()
    names := make([]string, 0, len(l.connections))
    for name := range l.connections {
        names = append(names, name)
    }
    return names
}

// newTransport 根据配置创建对应的 Transport 实现。
func newTransport(cfg ServerConfig) (Transport, error) {
    switch cfg.Transport {
    case "", "stdio":
        if cfg.Command == "" {
            return nil, fmt.Errorf("stdio transport requires command")
        }
        return newStdioTransport(cfg), nil
    case "sse":
        if cfg.Endpoint == "" {
            return nil, fmt.Errorf("sse transport requires endpoint")
        }
        return newSSETransport(cfg), nil
    default:
        return nil, fmt.Errorf("unsupported transport %q", cfg.Transport)
    }
}
