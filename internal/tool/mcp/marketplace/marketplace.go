// Package marketplace defines a pluggable abstraction for MCP server marketplaces.
//
// A market provider exposes a curated list of MCP server packages. The Manager
// calls ListServers to show available packages, GetServer to inspect a package,
// and ResolveConfig to obtain the concrete ServerConfig that should be installed.
//
// Different marketplaces may have different APIs. This package intentionally only
// defines the common denominator: ID, name, description, version, transport hint,
// and the resolved install configuration. Future providers (OpenCode, OpenClaude,
// npm, GitHub releases, ...) can implement this interface without changing the
// Manager or the core mcp package.
//
// Installation details are represented by InstallConfig instead of the core
// ServerConfig type to avoid an import cycle: the manager imports marketplace,
// and marketplace must remain independent of the core mcp package.
package marketplace

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
)

//go:embed default.json
var defaultCatalog []byte

// DefaultStaticProvider returns the bundled static market provider.
//
// It embeds default.json from the same package so the server binary ships with
// a ready-to-use example market even when no external marketplace is configured.
func DefaultStaticProvider() (*StaticProvider, error) {
	return NewStaticProvider(defaultCatalog)
}

// Provider is the common interface implemented by every MCP marketplace adapter.
type Provider interface {
	// Name returns the short machine-readable market name, e.g. "default" or "opencode".
	Name() string

	// DisplayName returns the human-readable market label shown in the UI.
	DisplayName() string

	// ListServers returns all packages available in this market.
	ListServers(ctx context.Context) ([]Package, error)

	// GetServer returns a single package by ID.
	GetServer(ctx context.Context, id string) (Package, error)
}

// ConfigResolver is implemented by providers that can fully resolve a package
// into an InstallConfig suitable for local installation.
type ConfigResolver interface {
	ResolveConfig(ctx context.Context, pkgID string) (InstallConfig, error)
}

// Package describes one installable MCP server in a marketplace.
type Package struct {
	// ID is unique within the market. It becomes the ManagedServer.ID when installed.
	ID string `json:"id"`

	// Name is the human-readable server name.
	Name string `json:"name"`

	// Description explains what the server does.
	Description string `json:"description"`

	// Version is the package version string, if available.
	Version string `json:"version,omitempty"`

	// Transport is "stdio" or "sse".
	Transport string `json:"transport"`

	// SourceURL points to the package homepage, npm url, or git repository.
	SourceURL string `json:"source_url,omitempty"`
}

// InstallConfig is the resolved, ready-to-install representation of a package.
// It mirrors the fields required by the core mcp ServerConfig but lives in this
// package to avoid an import cycle.
type InstallConfig struct {
	Name        string            `json:"name"`
	Transport   string            `json:"transport"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Endpoint    string            `json:"endpoint,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Enabled     bool              `json:"enabled"`
}

// Registry keeps named market providers. It is used by cmd/server to expose
// a stable set of markets and by Manager to install from a named market.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds a provider. Registering the same name twice overwrites.
func (r *Registry) Register(p Provider) {
	r.providers[p.Name()] = p
}

// Get returns a provider by name, or nil if not registered.
func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// Names returns all registered market names.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.providers))
	for name := range r.providers {
		out = append(out, name)
	}
	return out
}

// List returns a snapshot of all registered providers.
func (r *Registry) List() []Provider {
	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	return out
}

// StaticProvider reads packages from an embedded JSON catalog.
type StaticProvider struct {
	catalog catalog
}

type catalog struct {
	Version string       `json:"version"`
	Markets []marketMeta `json:"markets"`
	Servers []entry      `json:"servers"`
}

type marketMeta struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type entry struct {
	Package
	Market      string            `json:"market"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Endpoint    string            `json:"endpoint,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
}

// NewStaticProviderFromFS creates a provider by reading the given JSON file.
func NewStaticProviderFromFS(fsys embed.FS, path string) (*StaticProvider, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("read static market catalog: %w", err)
	}
	return NewStaticProvider(data)
}

// NewStaticProvider parses an in-memory JSON catalog.
func NewStaticProvider(data []byte) (*StaticProvider, error) {
	var c catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse static market catalog: %w", err)
	}
	return &StaticProvider{catalog: c}, nil
}

// Name returns "default" for the bundled static market.
func (s *StaticProvider) Name() string {
	return "default"
}

// DisplayName returns the human-readable market label from the catalog.
func (s *StaticProvider) DisplayName() string {
	for _, m := range s.catalog.Markets {
		if m.Name == s.Name() {
			return m.DisplayName
		}
	}
	return "内置示例市场"
}

// ListServers returns every package belonging to this static market.
func (s *StaticProvider) ListServers(ctx context.Context) ([]Package, error) {
	_ = ctx
	out := make([]Package, 0, len(s.catalog.Servers))
	for _, e := range s.catalog.Servers {
		if e.Market == s.Name() {
			out = append(out, e.Package)
		}
	}
	return out, nil
}

// GetServer returns a single static package by ID.
func (s *StaticProvider) GetServer(ctx context.Context, id string) (Package, error) {
	_ = ctx
	for _, e := range s.catalog.Servers {
		if e.ID == id {
			return e.Package, nil
		}
	}
	return Package{}, fmt.Errorf("market package not found: %s/%s", s.Name(), id)
}

// ResolveConfig implements ConfigResolver for the static market.
// It returns the fully resolved InstallConfig, including command/args or endpoint.
func (s *StaticProvider) ResolveConfig(ctx context.Context, id string) (InstallConfig, error) {
	_ = ctx
	for _, e := range s.catalog.Servers {
		if e.ID == id {
			return InstallConfig{
				Name:        e.Name,
				Transport:   e.Transport,
				Command:     e.Command,
				Args:        e.Args,
				Endpoint:    e.Endpoint,
				Environment: e.Environment,
				Enabled:     true,
			}, nil
		}
	}
	return InstallConfig{}, fmt.Errorf("market package not found: %s/%s", s.Name(), id)
}
