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
	"os"
	"path/filepath"
	"strings"
)

// projectRoot 是内置示例市场的项目根目录绝对路径，由 cmd/server 在启动时
// 通过 SetProjectRoot 注入。ResolveConfig 会用它把 stdio 服务器 args 中的
// 相对路径（如 examples/mcp/time/mcp-time-server.js）解析成绝对路径，从而
// 让子进程不再依赖 server 进程的当前工作目录。
//
// Why: default.json 中内置示例用的是相对路径。当 server 从 bin/ 等子目录
// 启动时，子进程继承的 cwd 无法找到这些脚本，node 立即退出，stdout EOF，
// 表现为 "initialize request: transport receive: EOF"。解析为绝对路径后，
// 无论 server 从哪里启动都能正确定位脚本。
var projectRoot string

// SetProjectRoot 注入项目根目录绝对路径，供 ResolveConfig 解析相对路径使用。
// 通常在进程启动时调用一次。空字符串表示不启用相对路径解析。
func SetProjectRoot(root string) {
	if root == "" {
		return
	}
	if abs, err := filepath.Abs(root); err == nil {
		projectRoot = abs
	} else {
		projectRoot = root
	}
}

// ProjectRoot 返回当前注入的项目根目录（可能为空）。
func ProjectRoot() string {
	return projectRoot
}

// DetectProjectRoot 尽力推导项目根目录：
//  1. 从可执行文件所在目录向上查找包含 examples/mcp 的目录（覆盖从 bin/ 启动）；
//  2. 从进程 cwd 向上查找（覆盖 go run 等临时编译场景）。
//
// 找不到时返回空字符串，调用方应优雅降级。判定标志用 examples/mcp 是因为
// 内置市场的两个示例 server 脚本就放在该目录下，能稳定标识项目根。
func DetectProjectRoot() string {
	if exe, err := os.Executable(); err == nil {
		if root := findRootFrom(filepath.Dir(exe)); root != "" {
			return root
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if root := findRootFrom(cwd); root != "" {
			return root
		}
	}
	return ""
}

// findRootFrom 从 dir 开始向上最多 8 层，返回第一个包含 examples/mcp 的目录。
func findRootFrom(dir string) string {
	for range 8 {
		if _, err := os.Stat(filepath.Join(dir, "examples", "mcp")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

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
//
// 当 projectRoot 已注入时，args 中相对路径（不以 / 或盘符开头、且非绝对路径）
// 会被拼接成基于 projectRoot 的绝对路径。这样无论 server 进程从哪个目录启动，
// 内置示例的 stdio 子进程都能找到自己的脚本文件。
func (s *StaticProvider) ResolveConfig(ctx context.Context, id string) (InstallConfig, error) {
	_ = ctx
	for _, e := range s.catalog.Servers {
		if e.ID == id {
			return InstallConfig{
				Name:        e.Name,
				Transport:   e.Transport,
				Command:     e.Command,
				Args:        resolveArgs(e.Args),
				Endpoint:    e.Endpoint,
				Environment: e.Environment,
				Enabled:     true,
			}, nil
		}
	}
	return InstallConfig{}, fmt.Errorf("market package not found: %s/%s", s.Name(), id)
}

// resolveArgs 在 projectRoot 注入时把相对路径参数转成绝对路径。
// 仅作用于看起来像本地路径的参数：以 "examples/"、"./" 等开头或包含路径分隔符
// 且文件确实存在的参数；其余参数（如 npm 包名、命令 flag）原样返回，避免误伤。
func resolveArgs(args []string) []string {
	if projectRoot == "" || len(args) == 0 {
		return args
	}
	resolved := make([]string, len(args))
	for i, a := range args {
		resolved[i] = resolveArg(a)
	}
	return resolved
}

// resolveArg 处理单个参数：若 projectRoot 下存在该相对路径文件则转为绝对路径，
// 否则原样返回。
func resolveArg(a string) string {
	if a == "" || filepath.IsAbs(a) {
		return a
	}
	// 仅对疑似本地文件路径的参数做解析：必须包含路径分隔符且在 projectRoot 下存在。
	if !strings.ContainsAny(a, "/\\") {
		return a
	}
	candidate := filepath.Join(projectRoot, a)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return a
}

// MCPPreinstallEntry describes one preinstall market/package pair.
type MCPPreinstallEntry struct {
	Market  string `json:"market"`
	Package string `json:"package"`
}

// String returns the shorthand "market/package" representation.
func (e MCPPreinstallEntry) String() string { return e.Market + "/" + e.Package }

// ParsePreinstallEntry parses a shorthand string into an MCPPreinstallEntry.
//
// Supported formats:
//   - "market/package" (e.g. "default/time-server")
//   - "package" (defaults market to "default")
//
// Empty input or entries without a package name return an error.
func ParsePreinstallEntry(s string) (MCPPreinstallEntry, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return MCPPreinstallEntry{}, fmt.Errorf("empty preinstall entry")
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) == 1 {
		return MCPPreinstallEntry{Market: "default", Package: strings.TrimSpace(parts[0])}, nil
	}
	market := strings.TrimSpace(parts[0])
	pkg := strings.TrimSpace(parts[1])
	if pkg == "" {
		return MCPPreinstallEntry{}, fmt.Errorf("missing package in preinstall entry: %q", s)
	}
	if market == "" {
		market = "default"
	}
	return MCPPreinstallEntry{Market: market, Package: pkg}, nil
}
