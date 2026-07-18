package mcp

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
)

// Manager owns the lifecycle of all configured and dynamically added MCP servers.
//
// It is the single integration point between static configuration, runtime API
// additions, and future marketplace installs. Each managed server is uniquely
// identified by ID; the manager binds its ServerConfig to a Transport, negotiates
// capabilities via a Client, and registers discovered tools as ProxyTool instances
// into the shared tool.Registry.
//
// Concurrency: Manager methods are safe for concurrent use. Loaded servers share
// the registry by namespacing every tool as mcp__<server>__<tool>.
type Manager struct {
	registry *tool.Registry
	loader   *Loader
	repo     Repository

	mu      sync.RWMutex
	servers map[string]*managedServer

	// StaticIDs records which server IDs originated from static config so that
	// they are not accidentally removed by dynamic API deletes. Static entries
	// can still be enabled/disabled at runtime, but their config is reloaded
	// on the next process restart.
	staticIDs map[string]struct{}
}

// managedServer is the in-memory runtime state of one loaded MCP server.
type managedServer struct {
	ms      ManagedServer
	loaded  bool
	loadErr error
}

// Repository persists ManagedServer records. The nil repository is valid and
// simply means "no persistence"; all dynamic changes will be lost on restart.
type Repository interface {
	// Save stores or updates a managed server. The implementation decides the
	// primary key (typically ms.ID).
	Save(ctx context.Context, ms ManagedServer) error

	// Delete removes a managed server by ID.
	Delete(ctx context.Context, id string) error

	// ListEnabled returns all servers that should be loaded at startup.
	ListEnabled(ctx context.Context) ([]ManagedServer, error)

	// ListAll returns every persisted server, including disabled ones.
	ListAll(ctx context.Context) ([]ManagedServer, error)
}

// NewManager creates a Manager bound to registry. repo may be nil.
func NewManager(registry *tool.Registry, repo Repository) *Manager {
	return &Manager{
		registry:  registry,
		loader:    NewLoader(registry),
		repo:      repo,
		servers:   make(map[string]*managedServer),
		staticIDs: make(map[string]struct{}),
	}
}

// LoadStaticServers loads servers from static configuration at startup.
//
// If a server is enabled and can be connected, its tools are registered into
// the shared registry. Disabled servers are remembered but not connected.
// Static servers are marked so they cannot be deleted by dynamic RemoveServer.
func (m *Manager) LoadStaticServers(ctx context.Context, configs []ServerConfig) error {
	for _, cfg := range configs {
		if cfg.Name == "" {
			continue
		}
		id := cfg.Name
		ms := ManagedServer{
			ID:      id,
			Source:  SourceStatic,
			Config:  cfg,
			Enabled: cfg.Enabled,
		}
		m.staticIDs[id] = struct{}{}
		m.setServer(ms)
		if !ms.Enabled {
			continue
		}
		if err := m.connect(ctx, ms); err != nil {
			// Log but do not fail startup; individual MCP servers may be unavailable.
			// The server stays in the map with loadErr so callers can inspect it.
			m.markLoadError(id, err)
		}
	}
	return nil
}

// LoadDBServers loads enabled servers from the persistent repository.
//
// This is typically called once at startup after LoadStaticServers. DB servers
// take no precedence over static ones: if the same ID exists, the static
// configuration wins because it was loaded first.
func (m *Manager) LoadDBServers(ctx context.Context) error {
	if m.repo == nil {
		return nil
	}
	servers, err := m.repo.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("list enabled mcp servers: %w", err)
	}
	for _, ms := range servers {
		if ms.ID == "" {
			continue
		}
		if _, exists := m.servers[ms.ID]; exists {
			continue
		}
		m.setServer(ms)
		if err := m.connect(ctx, ms); err != nil {
			m.markLoadError(ms.ID, err)
		}
	}
	return nil
}

// AddServer adds a server dynamically, connects if enabled, and persists it.
//
// If a server with the same ID already exists and originated from static config,
// AddServer returns an error; static config must be edited and the process
// restarted to change those servers.
func (m *Manager) AddServer(ctx context.Context, ms ManagedServer) error {
	if ms.ID == "" {
		return fmt.Errorf("server id is required")
	}
	m.mu.Lock()
	_, isStatic := m.staticIDs[ms.ID]
	m.mu.Unlock()
	if isStatic {
		return fmt.Errorf("server %q is static and cannot be modified via AddServer", ms.ID)
	}

	if ms.Source == "" {
		ms.Source = SourceDB
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if ms.CreatedAt == "" {
		ms.CreatedAt = now
	}
	ms.UpdatedAt = now

	if m.repo != nil {
		if err := m.repo.Save(ctx, ms); err != nil {
			return fmt.Errorf("persist server %s: %w", ms.ID, err)
		}
	}

	// Disconnect any previous incarnation.
	_ = m.DisconnectServer(ctx, ms.ID)

	m.setServer(ms)
	if ms.Enabled {
		if err := m.connect(ctx, ms); err != nil {
			m.markLoadError(ms.ID, err)
			return fmt.Errorf("connect server %s: %w", ms.ID, err)
		}
	}
	return nil
}

// RemoveServer disconnects and deletes a dynamic server.
//
// Static servers cannot be removed at runtime; callers should disable them
// instead.
func (m *Manager) RemoveServer(ctx context.Context, id string) error {
	m.mu.Lock()
	_, isStatic := m.staticIDs[id]
	m.mu.Unlock()
	if isStatic {
		return fmt.Errorf("server %q is static and cannot be removed via RemoveServer", id)
	}

	_ = m.DisconnectServer(ctx, id)

	if m.repo != nil {
		if err := m.repo.Delete(ctx, id); err != nil {
			return fmt.Errorf("delete server %s: %w", id, err)
		}
	}

	m.mu.Lock()
	delete(m.servers, id)
	m.mu.Unlock()
	return nil
}

// EnableServer enables and connects a previously disabled server.
func (m *Manager) EnableServer(ctx context.Context, id string) error {
	m.mu.Lock()
	ms, ok := m.servers[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("server not found: %s", id)
	}
	ms.ms.Enabled = true
	ms.ms.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	server := ms.ms
	m.mu.Unlock()

	if m.repo != nil {
		if err := m.repo.Save(ctx, server); err != nil {
			return fmt.Errorf("persist enable %s: %w", id, err)
		}
	}

	return m.connect(ctx, server)
}

// DisableServer disconnects a server and marks it disabled.
func (m *Manager) DisableServer(ctx context.Context, id string) error {
	m.mu.Lock()
	ms, ok := m.servers[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("server not found: %s", id)
	}
	ms.ms.Enabled = false
	ms.ms.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	server := ms.ms
	wasLoaded := ms.loaded
	m.mu.Unlock()

	// Disconnect only if it was actually loaded; otherwise this is a no-op disable
	// and unloading would return an error for a server that was never connected.
	if wasLoaded {
		if err := m.DisconnectServer(ctx, id); err != nil {
			return err
		}
	}

	if m.repo != nil {
		if err := m.repo.Save(ctx, server); err != nil {
			return fmt.Errorf("persist disable %s: %w", id, err)
		}
	}
	return nil
}

// DisconnectServer closes the transport and unregisters tools for a server
// without changing its enabled/disabled state.
func (m *Manager) DisconnectServer(ctx context.Context, id string) error {
	_ = ctx
	return m.loader.UnloadServer(id)
}

// ListServers returns the current state of all known servers.
func (m *Manager) ListServers() []ManagedServer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]ManagedServer, 0, len(m.servers))
	for _, ms := range m.servers {
		out = append(out, ms.ms)
	}
	return out
}

// GetServer returns a single server by ID, or an error if not found.
func (m *Manager) GetServer(id string) (ManagedServer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ms, ok := m.servers[id]
	if !ok {
		return ManagedServer{}, fmt.Errorf("server not found: %s", id)
	}
	return ms.ms, nil
}

// Close disconnects all loaded servers. It is safe to call multiple times.
func (m *Manager) Close() error {
	m.mu.Lock()
	ids := make([]string, 0, len(m.servers))
	for id := range m.servers {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	var firstErr error
	for _, id := range ids {
		if err := m.loader.UnloadServer(id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// connect asks the loader to establish a connection for ms.
func (m *Manager) connect(ctx context.Context, ms ManagedServer) error {
	err := m.loader.LoadServer(ctx, ms.Config)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if s, ok := m.servers[ms.ID]; ok {
		s.loaded = true
		s.loadErr = nil
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) markLoadError(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.servers[id]; ok {
		s.loaded = false
		s.loadErr = err
	}
}

func (m *Manager) setServer(ms ManagedServer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers[ms.ID] = &managedServer{ms: ms}
}

// ServerStatus is a JSON-friendly snapshot returned by API endpoints.
type ServerStatus struct {
	ManagedServer
	Loaded  bool   `json:"loaded"`
	LoadErr string `json:"load_err,omitempty"`
}

// ListServerStatuses returns snapshot states suitable for REST responses.
func (m *Manager) ListServerStatuses() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]ServerStatus, 0, len(m.servers))
	for _, s := range m.servers {
		st := ServerStatus{
			ManagedServer: s.ms,
			Loaded:        s.loaded,
		}
		if s.loadErr != nil {
			st.LoadErr = s.loadErr.Error()
		}
		out = append(out, st)
	}
	return out
}

// CloneManagedServer returns a deep-ish copy of ms for repository storage.
// It is exported so repository tests can reuse the same helpers.
func CloneManagedServer(ms ManagedServer) ManagedServer {
	cp := ms
	if ms.Config.Args != nil {
		cp.Config.Args = make([]string, len(ms.Config.Args))
		copy(cp.Config.Args, ms.Config.Args)
	}
	if ms.Config.Environment != nil {
		cp.Config.Environment = make(map[string]string, len(ms.Config.Environment))
		maps.Copy(cp.Config.Environment, ms.Config.Environment)
	}
	return cp
}

