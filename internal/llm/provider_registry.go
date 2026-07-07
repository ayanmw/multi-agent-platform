// Package llm — ProviderRegistry for managing a pool of Provider instances.
//
// # Design Rationale
//
// ProviderRegistry provides a centralized pool of Provider instances keyed by name.
// This avoids creating duplicate HTTP clients for the same endpoint/API key
// combination, and allows the Router and Engine to look up providers by name
// rather than holding direct references.
//
// The registry is populated at startup from Config.Models:
//   for _, m := range cfg.Models {
//       registry.Register(llm.ProviderConfig{Name: m.Name, ...})
//   }
//
// Then the Engine requests a provider by name:
//   provider := registry.Get("deepseek-v4-flash")
//
// # Thread Safety
//
// ProviderRegistry uses a sync.RWMutex for concurrent access. Get operations
// use read locks (fast, concurrent), while Register uses write locks (serialized).
package llm

import (
	"sort"
	"sync"
)

// ProviderRegistry manages a pool of Provider instances keyed by provider name.
// It supports registration, lookup, and lazy creation of providers.
type ProviderRegistry struct {
	mu       sync.RWMutex
	providers map[string]Provider      // name → provider instance
	configs   map[string]ProviderConfig // name → config used to create it
}

// NewProviderRegistry creates an empty provider registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		providers: make(map[string]Provider),
		configs:   make(map[string]ProviderConfig),
	}
}

// Register creates a Provider from cfg and stores it under cfg.Name.
// If a provider with the same name already exists, it is overwritten.
// Returns the registered provider or an error if creation fails.
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

// Get returns the Provider registered under the given name.
// Returns nil if no provider is registered with that name.
func (r *ProviderRegistry) Get(name string) Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.providers[name]
}

// List returns the names of all registered providers, sorted alphabetically.
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

// GetConfig returns the ProviderConfig for a registered provider by name.
// Returns the config and a boolean indicating whether it was found.
func (r *ProviderRegistry) GetConfig(name string) (ProviderConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cfg, ok := r.configs[name]
	return cfg, ok
}
