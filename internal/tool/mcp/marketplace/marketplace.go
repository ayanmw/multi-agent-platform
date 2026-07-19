// Package marketplace 为 MCP server marketplace 定义了一个可插拔抽象。
//
// market provider 暴露一份精选的 MCP server package 列表。Manager 调用
// ListServers 展示可用 package，调用 GetServer 检视某个 package，调用
// ResolveConfig 获取应被安装的具体 ServerConfig。
//
// 不同的 marketplace 可能拥有不同的 API。本包刻意只定义最小公约数：ID、
// 名称、描述、版本、transport 提示，以及解析出的安装配置。未来的 provider
// （OpenCode、OpenClaude、npm、GitHub releases 等）可在不改动 Manager 或
// 核心 mcp 包的前提下实现该接口。
//
// 安装细节由 InstallConfig 表达，而非核心 ServerConfig 类型，目的是避免
// import cycle：manager 导入 marketplace，而 marketplace 必须独立于核心
// mcp 包。
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

// DefaultStaticProvider 返回内置的静态 market provider。
//
// 它将同包下的 default.json 嵌入二进制，因此即使未配置任何外部 marketplace，
// server 二进制也自带一个开箱即用的示例 market。
func DefaultStaticProvider() (*StaticProvider, error) {
	return NewStaticProvider(defaultCatalog)
}

// Provider 是每个 MCP marketplace 适配器都实现的通用接口。
type Provider interface {
	// Name 返回机器可读的简短 market 名称，例如 "default" 或 "opencode"。
	Name() string

	// DisplayName 返回 UI 中展示的人类可读 market 标签。
	DisplayName() string

	// ListServers 返回该 market 中所有可用的 package。
	ListServers(ctx context.Context) ([]Package, error)

	// GetServer 按 ID 返回单个 package。
	GetServer(ctx context.Context, id string) (Package, error)
}

// ConfigResolver 由能将 package 完整解析为适合本地安装的 InstallConfig 的
// provider 实现。
type ConfigResolver interface {
	ResolveConfig(ctx context.Context, pkgID string) (InstallConfig, error)
}

// Package 描述 marketplace 中一个可安装的 MCP server。
type Package struct {
	// ID 在 market 内唯一。安装后它会成为 ManagedServer.ID。
	ID string `json:"id"`

	// Name 是人类可读的 server 名称。
	Name string `json:"name"`

	// Description 解释 server 的用途。
	Description string `json:"description"`

	// Version 是 package 的版本字符串（若可用）。
	Version string `json:"version,omitempty"`

	// Transport 取值为 "stdio" 或 "sse"。
	Transport string `json:"transport"`

	// SourceURL 指向 package 主页、npm URL 或 git 仓库。
	SourceURL string `json:"source_url,omitempty"`
}

// InstallConfig 是 package 解析后、可立即安装的表示。
// 它与核心 mcp ServerConfig 的必需字段相对应，但放在本包中以避免
// import cycle。
type InstallConfig struct {
	Name        string            `json:"name"`
	Transport   string            `json:"transport"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Endpoint    string            `json:"endpoint,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Enabled     bool              `json:"enabled"`
}

// Registry 维护命名的 market provider。cmd/server 用它暴露一组稳定的
// market，Manager 用它按名称安装 market。
type Registry struct {
	providers map[string]Provider
}

// NewRegistry 创建一个空的 registry。
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register 添加一个 provider。同名注册会覆盖原值。
func (r *Registry) Register(p Provider) {
	r.providers[p.Name()] = p
}

// Get 按名称返回 provider，未注册则返回 nil。
func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// Names 返回所有已注册的 market 名称。
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.providers))
	for name := range r.providers {
		out = append(out, name)
	}
	return out
}

// List 返回所有已注册 provider 的快照。
func (r *Registry) List() []Provider {
	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	return out
}

// StaticProvider 从内置 JSON catalog 读取 package。
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

// NewStaticProviderFromFS 通过读取给定 JSON 文件创建一个 provider。
func NewStaticProviderFromFS(fsys embed.FS, path string) (*StaticProvider, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("read static market catalog: %w", err)
	}
	return NewStaticProvider(data)
}

// NewStaticProvider 解析内存中的 JSON catalog。
func NewStaticProvider(data []byte) (*StaticProvider, error) {
	var c catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse static market catalog: %w", err)
	}
	return &StaticProvider{catalog: c}, nil
}

// Name 对内置静态 market 返回 "default"。
func (s *StaticProvider) Name() string {
	return "default"
}

// DisplayName 返回 catalog 中的人类可读 market 标签。
func (s *StaticProvider) DisplayName() string {
	for _, m := range s.catalog.Markets {
		if m.Name == s.Name() {
			return m.DisplayName
		}
	}
	return "内置示例市场"
}

// ListServers 返回属于该静态 market 的所有 package。
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

// GetServer 按 ID 返回单个静态 package。
func (s *StaticProvider) GetServer(ctx context.Context, id string) (Package, error) {
	_ = ctx
	for _, e := range s.catalog.Servers {
		if e.ID == id {
			return e.Package, nil
		}
	}
	return Package{}, fmt.Errorf("market package not found: %s/%s", s.Name(), id)
}

// ResolveConfig 为静态 market 实现 ConfigResolver。
// 它返回完全解析好的 InstallConfig，包括 command/args 或 endpoint。
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

// MCPPreinstallEntry 描述一对预安装的 market/package。
type MCPPreinstallEntry struct {
	Market  string `json:"market"`
	Package string `json:"package"`
}

// String 返回 "market/package" 形式的简写表示。
func (e MCPPreinstallEntry) String() string { return e.Market + "/" + e.Package }

// ParsePreinstallEntry 将简写字符串解析为 MCPPreinstallEntry。
//
// 支持的格式：
//   - "market/package"（例如 "default/time-server"）
//   - "package"（market 默认为 "default"）
//
// 空输入或缺少 package 名称的条目将返回错误。
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
