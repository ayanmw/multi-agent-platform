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

// URLProvider 从远端 HTTP URL 拉取 MCP marketplace catalog。
//
// catalog 格式与 StaticProvider 使用的静态 JSON catalog 完全一致，这让团队
// 可以把精选 server 列表发布到任意静态文件托管、npm registry、GitHub
// releases 或 OpenCode endpoint 上。
//
// URLProvider 同时实现 Provider 与 ConfigResolver。provider 名称取自 catalog
// 的 markets 元数据，或回退到一个已配置的别名；这让 Manager 集成方式与
// StaticProvider 完全一致。
type URLProvider struct {
	name   string
	url    string
	client *http.Client

	// 缓存的 catalog 与解析出的展示名称，由 load 填充。
	catalog     catalog
	displayName string
}

// URLProviderOption 用于配置 URLProvider。
type URLProviderOption func(*URLProvider)

// WithURLProviderHTTPClient 设置拉取 catalog 时使用的自定义 HTTP client。
func WithURLProviderHTTPClient(client *http.Client) URLProviderOption {
	return func(p *URLProvider) { p.client = client }
}

// NewURLProvider 创建一个从 catalogURL 加载 catalog 的 provider。
//
// name 是机器可读的 market 标识，用于 REST 路径（如 /api/mcp/markets/:name）。
// 若为空，provider 会尝试从下载的 catalog 中读取第一个 market 的名称。
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

// NewURLProviderFromConfig 基于 InstallConfig 风格的字段构建一个 URLProvider。
//
// 当 market 来源本身通过环境变量或静态配置块配置时，该辅助函数很有用。
func NewURLProviderFromConfig(name string, catalogURL string, opts ...URLProviderOption) (*URLProvider, error) {
	return NewURLProvider(catalogURL, name, opts...)
}

// load 拉取并解析远端 catalog。
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

// resolveMetadata 选取展示名称；若未显式给定名称，则从 catalog 的第一个
// market 条目推导 provider 名称。
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

// Name 返回机器可读的 market 名称。
func (p *URLProvider) Name() string { return p.name }

// DisplayName 返回来自 catalog 元数据的人类可读 market 标签。
func (p *URLProvider) DisplayName() string { return p.displayName }

// ListServers 返回属于该 provider market 的 package。
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

// GetServer 按 ID 返回单个 package。
func (p *URLProvider) GetServer(ctx context.Context, id string) (Package, error) {
	_ = ctx
	for _, e := range p.catalog.Servers {
		if e.ID == id {
			return e.Package, nil
		}
	}
	return Package{}, fmt.Errorf("market package not found: %s/%s", p.name, id)
}

// ResolveConfig 返回某个 package 的具体安装配置。
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

// URL 返回该 provider 使用的远端 catalog URL。
func (p *URLProvider) URL() string { return p.url }
