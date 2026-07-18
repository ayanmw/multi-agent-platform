package marketplace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// URLProvider fetches an MCP marketplace catalog from a remote HTTP URL.
//
// The catalog format is identical to the static JSON catalog consumed by
// StaticProvider, which lets teams publish curated server lists to any
// static file host, npm registry, GitHub releases, or OpenCode endpoint.
//
// URLProvider implements Provider and ConfigResolver. The provider name is
// derived from the catalog's markets metadata or falls back to a configured
// alias; this keeps Manager integration identical to StaticProvider.
type URLProvider struct {
	name   string
	url    string
	client *http.Client

	// Cached catalog and resolved display name populated by load.
	catalog     catalog
	displayName string
}

// URLProviderOption configures a URLProvider.
type URLProviderOption func(*URLProvider)

// WithURLProviderHTTPClient sets a custom HTTP client for fetching catalogs.
func WithURLProviderHTTPClient(client *http.Client) URLProviderOption {
	return func(p *URLProvider) { p.client = client }
}

// NewURLProvider creates a provider that loads its catalog from catalogURL.
//
// name is the machine-readable market identifier used in REST paths such as
// /api/mcp/markets/:name. If empty, the provider attempts to read the first
// market's name from the downloaded catalog.
func NewURLProvider(catalogURL string, name string, opts ...URLProviderOption) (*URLProvider, error) {
	if catalogURL == "" {
		return nil, fmt.Errorf("url provider requires a catalog URL")
	}
	if _, err := url.Parse(catalogURL); err != nil {
		return nil, fmt.Errorf("invalid catalog URL: %w", err)
	}

	p := &URLProvider{
		url:    catalogURL,
		name:   name,
		client: &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(p)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.load(ctx); err != nil {
		return nil, err
	}

	return p, nil
}

// NewURLProviderFromConfig builds a URLProvider from InstallConfig-style fields.
//
// This helper is useful when the market source itself is configured via an
// environment variable or a static config block.
func NewURLProviderFromConfig(name string, catalogURL string, opts ...URLProviderOption) (*URLProvider, error) {
	return NewURLProvider(catalogURL, name, opts...)
}

// load fetches and parses the remote catalog.
func (p *URLProvider) load(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return fmt.Errorf("build catalog request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch catalog from %s: %w", p.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("catalog endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return fmt.Errorf("read catalog body: %w", err)
	}

	var c catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("parse catalog JSON: %w", err)
	}
	p.catalog = c
	p.resolveMetadata()
	return nil
}

// resolveMetadata picks the display name and, if no explicit name was given,
// derives the provider name from the catalog's first market entry.
func (p *URLProvider) resolveMetadata() {
	if p.name == "" && len(p.catalog.Markets) > 0 {
		p.name = p.catalog.Markets[0].Name
	}
	if p.name == "" {
		p.name = "remote"
	}

	for _, m := range p.catalog.Markets {
		if m.Name == p.name && m.DisplayName != "" {
			p.displayName = m.DisplayName
			return
		}
	}
	p.displayName = "远程市场"
}

// Name returns the machine-readable market name.
func (p *URLProvider) Name() string { return p.name }

// DisplayName returns the human-readable market label from the catalog metadata.
func (p *URLProvider) DisplayName() string { return p.displayName }

// ListServers returns the packages declared for this provider's market.
func (p *URLProvider) ListServers(ctx context.Context) ([]Package, error) {
	_ = ctx
	out := make([]Package, 0, len(p.catalog.Servers))
	for _, e := range p.catalog.Servers {
		if e.Market == p.name {
			out = append(out, e.Package)
		}
	}
	return out, nil
}

// GetServer returns a single package by ID.
func (p *URLProvider) GetServer(ctx context.Context, id string) (Package, error) {
	_ = ctx
	for _, e := range p.catalog.Servers {
		if e.ID == id {
			return e.Package, nil
		}
	}
	return Package{}, fmt.Errorf("market package not found: %s/%s", p.name, id)
}

// ResolveConfig returns the concrete install configuration for a package.
func (p *URLProvider) ResolveConfig(ctx context.Context, id string) (InstallConfig, error) {
	_ = ctx
	for _, e := range p.catalog.Servers {
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
	return InstallConfig{}, fmt.Errorf("market package not found: %s/%s", p.name, id)
}

// URL returns the remote catalog URL used by this provider.
func (p *URLProvider) URL() string { return p.url }
