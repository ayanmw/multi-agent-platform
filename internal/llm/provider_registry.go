// Package llm —— ProviderRegistry，用于管理 Provider 实例池。
//
// # 设计理由
//
// ProviderRegistry 提供按名索引的 Provider 实例池中心化存储。
// 这避免了对同一 endpoint/API key 组合创建重复的 HTTP client，
// 也让 Router 与 Engine 按名查找 provider，而不必持有直接引用。
//
// registry 在启动时从 Config.Models 填充：
//   for _, m := range cfg.Models {
//       registry.Register(llm.ProviderConfig{Name: m.Name, ...})
//   }
//
// 之后 Engine 按名请求 provider：
//   provider := registry.Get("deepseek-v4-flash")
//
// # 线程安全
//
// ProviderRegistry 用 sync.RWMutex 处理并发访问。Get 操作
// 用读锁（快、可并发），Register 用写锁（串行）。
package llm

import (
	"sort"
	"sync"
)

// ProviderRegistry 管理按 provider 名索引的 Provider 实例池。
// 它支持注册、查找以及 provider 的惰性创建。
type ProviderRegistry struct {
	mu       sync.RWMutex
	providers map[string]Provider      // name → provider 实例
	configs   map[string]ProviderConfig // name → 创建该 provider 所用的配置
}

// NewProviderRegistry 创建一个空的 provider registry。
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		providers: make(map[string]Provider),
		configs:   make(map[string]ProviderConfig),
	}
}

// Register 根据 cfg 创建 Provider 并以 cfg.Name 为 key 存储。
// 若同名 provider 已存在，则会被覆盖。
// 返回已注册的 provider；若创建失败则返回 error。
func (r *ProviderRegistry) Register(cfg ProviderConfig) (Provider, error) {
	provider, err := NewProvider(cfg)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers[cfg.Name] = provider
	r.configs[cfg.Name] = cfg

	return provider, nil
}

// Get 返回以给定名注册的 Provider。
// 若该名未注册 provider，则返回 nil。
func (r *ProviderRegistry) Get(name string) Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.providers[name]
}

// List 返回所有已注册 provider 的名字，按字母序排序。
func (r *ProviderRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetConfig 按名返回已注册 provider 的 ProviderConfig。
// 返回配置以及一个表示是否找到的布尔值。
func (r *ProviderRegistry) GetConfig(name string) (ProviderConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cfg, ok := r.configs[name]
	return cfg, ok
}
