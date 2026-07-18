package mcp

import (
    "context"
    "fmt"
    "sync"

    "github.com/anmingwei/multi-agent-platform/internal/tool"
)

// Loader bridges external MCP Servers with the shared tool.Registry.
//
// For each ServerConfig it establishes a transport, performs the MCP handshake,
// discovers tools, and registers proxy tools under the mcp__<server>__<tool>
// namespace. The loader owns the lifecycle of these connections so they can be
// closed gracefully without leaking child processes.
type Loader struct {
    registry *tool.Registry
    mu       sync.RWMutex
    // connections tracks active servers by their configured name.
    connections map[string]*serverConnection
}

// serverConnection holds the runtime state of one loaded MCP server.
type serverConnection struct {
    config    ServerConfig
    transport Transport
    client    *Client
    tools     []ToolDefinition
}

// NewLoader creates a loader bound to the given registry.
func NewLoader(registry *tool.Registry) *Loader {
    return &Loader{
        registry:    registry,
        connections: make(map[string]*serverConnection),
    }
}

// LoadServer connects to a single MCP server, negotiates capabilities, lists
// tools, and registers each discovered tool as a ProxyTool into the registry.
//
// If a server with the same name was already loaded, the previous connection
// is closed and the new tools replace the old registrations. Disabled servers
// are skipped silently.
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

// LoadServerWithTransport is a test hook that injects a pre-built Transport.
func (l *Loader) LoadServerWithTransport(ctx context.Context, cfg ServerConfig, tr Transport) error {
    return l.load(ctx, cfg, tr)
}

func (l *Loader) load(ctx context.Context, cfg ServerConfig, tr Transport) error {
    // Close any previous connection for this server name.
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

// LoadedServerNames returns the names of currently loaded MCP servers.
func (l *Loader) LoadedServerNames() []string {
    l.mu.RLock()
    defer l.mu.RUnlock()
    names := make([]string, 0, len(l.connections))
    for name := range l.connections {
        names = append(names, name)
    }
    return names
}

// UnloadServer closes the transport for the named server and removes its
// proxy tools from the registry.
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

    // Unregister all tools advertised by this server.
    cfg := conn.config
    for _, def := range conn.tools {
        toolName := cfg.ToolName(def.Name)
        // Ignore errors from unregistering tools that may have been removed
        // already by dynamic APIs.
        _ = l.registry.Unregister(toolName)
    }

    if conn.transport != nil {
        _ = conn.transport.Close()
    }
    return nil
}

// LoadedNames returns the names of currently loaded MCP servers.
func (l *Loader) LoadedNames() []string {
    l.mu.RLock()
    defer l.mu.RUnlock()
    names := make([]string, 0, len(l.connections))
    for name := range l.connections {
        names = append(names, name)
    }
    return names
}

// newTransport creates the appropriate Transport implementation for a config.
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
