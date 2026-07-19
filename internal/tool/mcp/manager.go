package mcp

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp/marketplace"
)

// ChangeNotifier 会在已加载 MCP server 集合（因而也是已注册 proxy tool 集合）
// 发生变化后，被传入动作类型和受影响的 server ID 调用。cmd/server 用它来
// 广播 mcp_tools_changed WebSocket 事件，让前端刷新可用 tool。
type ChangeNotifier func(action, serverID string)

// Manager 持有所有已配置及动态新增 MCP server 的生命周期。
//
// 它是静态配置、运行时 API 新增以及未来 marketplace 安装之间的唯一集成点。
// 每个被管理的 server 由 ID 唯一标识；Manager 将其 ServerConfig 绑定到一个
// Transport，通过 Client 协商 capabilities，并将发现的 tool 作为 ProxyTool
// 注册到共享的 tool.Registry。
//
// 并发：Manager 的方法可安全并发使用。已加载的 server 通过将每个 tool 命名为
// mcp__<server>__<tool> 来共享 registry 而不冲突。
type Manager struct {
	registry *tool.Registry
	loader   *Loader
	repo     Repository

	// markets 接入可选的 marketplace provider。Manager 可以从 market 安装
	// server，方式是将其 package 解析为 ServerConfig 并以 SourceDB server 的
	// 形式添加（Config 来自 market，但安装后生命周期由本地接管）。
	markets *marketplace.Registry

	mu      sync.RWMutex
	servers map[string]*managedServer

	// StaticIDs 记录来自静态配置的 server ID，以免被动态 API 误删。
	// 静态条目在运行时仍可被 enable/disable，但其配置会在下次进程重启时
	// 重新加载。
	staticIDs map[string]struct{}

	// onChange 在 server 连接或断开后被调用，以便平台向 WebSocket client
	// 广播 mcp_tools_changed 事件。
	onChange ChangeNotifier
}

// managedServer 是一个已加载 MCP server 的内存运行时状态。
type managedServer struct {
	ms      ManagedServer
	loaded  bool
	loadErr error
}

// Repository 持久化 ManagedServer 记录。nil repository 也是合法的，表示
// "不做持久化"；所有动态变更在重启后会丢失。
type Repository interface {
	// Save 存储或更新一个被管理的 server。实现自行决定主键
	// （通常是 ms.ID）。
	Save(ctx context.Context, ms ManagedServer) error

	// Delete 按 ID 删除一个被管理的 server。
	Delete(ctx context.Context, id string) error

	// ListEnabled 返回所有应在启动时加载的 server。
	ListEnabled(ctx context.Context) ([]ManagedServer, error)

	// ListAll 返回所有已持久化的 server，包括被禁用的。
	ListAll(ctx context.Context) ([]ManagedServer, error)
}

// NewManager 创建一个绑定到 registry 的 Manager。repo 可以为 nil。
func NewManager(registry *tool.Registry, repo Repository) *Manager {
	return &Manager{
		registry:  registry,
		loader:    NewLoader(registry),
		repo:      repo,
		markets:   marketplace.NewRegistry(),
		servers:   make(map[string]*managedServer),
		staticIDs: make(map[string]struct{}),
	}
}

// RegisterMarket 注册一个 marketplace provider。
//
// 静态 market provider 通常在启动时注册；后续可通过 Phase 7 适配器动态
// 发现新 market。
func (m *Manager) RegisterMarket(p marketplace.Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.markets.Register(p)
}

// Markets 返回已注册 market provider 的快照。
func (m *Manager) Markets() []marketplace.Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.markets.List()
}

// GetMarket 按名称返回已注册的 market provider。
func (m *Manager) GetMarket(name string) (marketplace.Provider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.markets.Get(name)
}

// InstallFromMarket 从指定名称的 market 解析一个 package，并作为被管理的
// server 添加。若 enabled，则立即连接该 server。
//
// 安装自 market 的 server 以 SourceDB 持久化，但通过 event data 中的 "market"
// key 记住其来源。原始 package ID 即作为 ManagedServer.ID。
func (m *Manager) InstallFromMarket(ctx context.Context, marketName, pkgID string) (ManagedServer, error) {
	m.mu.RLock()
	provider, ok := m.markets.Get(marketName)
	m.mu.RUnlock()
	if !ok {
		return ManagedServer{}, fmt.Errorf("market not found: %s", marketName)
	}

	pkg, err := provider.GetServer(ctx, pkgID)
	if err != nil {
		return ManagedServer{}, fmt.Errorf("lookup %s/%s: %w", marketName, pkgID, err)
	}

	resolver, ok := provider.(marketplace.ConfigResolver)
	if !ok {
		return ManagedServer{}, fmt.Errorf("market %s does not support installation", marketName)
	}

	cfg, err := resolver.ResolveConfig(ctx, pkgID)
	if err != nil {
		return ManagedServer{}, fmt.Errorf("resolve %s/%s: %w", marketName, pkgID, err)
	}

	ms := ManagedServer{
		ID:      pkgID,
		Source:  SourceMarket,
		Enabled: cfg.Enabled,
		Config: ServerConfig{
			Name:        pkg.Name,
			Transport:   cfg.Transport,
			Command:     cfg.Command,
			Args:        cfg.Args,
			Endpoint:    cfg.Endpoint,
			Environment: cfg.Environment,
			Enabled:     cfg.Enabled,
		},
	}

	if err := m.AddServer(ctx, ms); err != nil {
		// 如果连接失败但 server 记录已存在，则返回该被管理的 server，
		// 让调用方检查 load_err 并在后续重新 enable。
		if existing, getErr := m.GetServer(pkgID); getErr == nil {
			m.notifyChange("add", pkgID)
			return existing, nil
		}
		return ManagedServer{}, fmt.Errorf("add %s/%s: %w", marketName, pkgID, err)
	}

	m.notifyChange("add", pkgID)
	return m.GetServer(pkgID)
}

// InstallFromMarketIfMissing 仅当当前没有同 ID 的 enabled server（已加载或
// 已持久化）时，才安装 market package。它避免每次进程重启都重复安装和
// 重新连接。
//
// 检查先查询内存中的 server 列表，再查询 repository（若已配置）。已被禁用但
// 持久化的 server 视为 "已存在"，不会被重新安装；操作者可通过 API 来 enable。
func (m *Manager) InstallFromMarketIfMissing(ctx context.Context, marketName, pkgID string) (ManagedServer, bool, error) {
	// 快速路径：已在内存中加载。
	if existing, err := m.GetServer(pkgID); err == nil && existing.Enabled {
		return existing, false, nil
	}

	// 感知持久化的检查：把 disabled server 也算进来，避免重建操作者刻意
	// 禁用的记录。
	if m.repo != nil {
		all, err := m.repo.ListAll(ctx)
		if err != nil {
			return ManagedServer{}, false, fmt.Errorf("list existing servers: %w", err)
		}
		for _, ms := range all {
			if ms.ID == pkgID {
				return ms, false, nil
			}
		}
	}

	ms, err := m.InstallFromMarket(ctx, marketName, pkgID)
	if err != nil {
		return ManagedServer{}, false, err
	}
	return ms, true, nil
}

// SetChangeNotifier 注册一个在任何 server 加载/卸载后调用的回调。
// 通常由 cmd/server 在 WebSocket hub 就绪后调用一次。
func (m *Manager) SetChangeNotifier(fn ChangeNotifier) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = fn
}

// notifyChange 调用已注册的 change notifier（若有）。
func (m *Manager) notifyChange(action, serverID string) {
	m.mu.RLock()
	fn := m.onChange
	m.mu.RUnlock()
	if fn != nil {
		fn(action, serverID)
	}
}

// LoadStaticServers 在启动时从静态配置加载 server。
//
// 若某个 server 处于 enabled 且可连接，则其 tool 会被注册到共享 registry。
// disabled server 会被记录但不连接。静态 server 会被标记，因此不能被动态
// RemoveServer 删除。
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
			// 记录但不让启动失败；单个 MCP server 可能不可用。
			// server 仍保留在 map 中并带 loadErr，调用方可以检查。
			m.markLoadError(id, err)
		}
	}
	return nil
}

// LoadDBServers 从持久化 repository 中加载 enabled 的 server。
//
// 通常在启动时、LoadStaticServers 之后调用一次。DB server 不会优先于静态
// server：若同 ID 已存在，则静态配置胜出（因为它先被加载）。
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

// AddServer 动态添加一个 server，若 enabled 则建立连接，并持久化记录。
//
// 若同 ID server 已存在且来自静态配置，则 AddServer 返回错误；要修改静态
// server，必须编辑静态配置并重启进程。
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

	// 断开任何此前已存在的实例。
	_ = m.DisconnectServer(ctx, ms.ID)

	m.setServer(ms)
	if ms.Enabled {
		if err := m.connect(ctx, ms); err != nil {
			m.markLoadError(ms.ID, err)
			return fmt.Errorf("connect server %s: %w", ms.ID, err)
		}
	}
	m.notifyChange("add", ms.ID)
	return nil
}

// RemoveServer 断开并删除一个动态 server。
//
// 静态 server 无法在运行时删除；调用方应改为禁用它。
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
	m.notifyChange("delete", id)
	return nil
}

// EnableServer 启用并连接一个此前被禁用的 server。
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

	if err := m.connect(ctx, server); err != nil {
		return err
	}
	m.notifyChange("enable", id)
	return nil
}

// DisableServer 断开一个 server 并将其标记为 disabled。
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

	// 仅在确实已加载时才断开；否则这是一次空 disable，而对从未连接的
	// server 执行 unload 会返回错误。
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
	m.notifyChange("disable", id)
	return nil
}

// DisconnectServer 关闭 transport 并注销该 server 的 tool，
// 但不改变其 enabled/disabled 状态。
func (m *Manager) DisconnectServer(ctx context.Context, id string) error {
	_ = ctx
	err := m.loader.UnloadServer(id)
	if err == nil {
		m.mu.Lock()
		if s, ok := m.servers[id]; ok {
			s.loaded = false
		}
		m.mu.Unlock()
	}
	return err
}

// ListServers 返回所有已知 server 的当前状态。
func (m *Manager) ListServers() []ManagedServer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]ManagedServer, 0, len(m.servers))
	for _, ms := range m.servers {
		out = append(out, ms.ms)
	}
	return out
}

// GetServer 按 ID 返回单个 server，未找到时返回错误。
func (m *Manager) GetServer(id string) (ManagedServer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ms, ok := m.servers[id]
	if !ok {
		return ManagedServer{}, fmt.Errorf("server not found: %s", id)
	}
	return ms.ms, nil
}

// Close 断开所有已加载的 server。可多次安全调用。
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

// connect 让 loader 为 ms 建立连接。
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
	m.notifyChange("connect", ms.ID)
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

// ServerStatus 是 API 端点返回的 JSON 友好快照。
type ServerStatus struct {
	ManagedServer
	Loaded  bool   `json:"loaded"`
	LoadErr string `json:"load_err,omitempty"`
}

// ListServerStatuses 返回适合 REST 响应的快照状态。
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

// CloneManagedServer 返回 ms 的"半深拷贝"，用于 repository 存储。
// 它被导出以便 repository 测试可复用同一套辅助函数。
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

