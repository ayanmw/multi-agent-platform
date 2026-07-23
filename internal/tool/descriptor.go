package tool

import "fmt"

// ToolSource 表示工具的来源。来源决定工具如何被加载、是否可被反注册
// 以及执行时使用的 runner。
type ToolSource string

const (
	// ToolSourceBuiltin 表示由本仓库代码编译期内置的工具。
	ToolSourceBuiltin ToolSource = "builtin"
	// ToolSourceLocalDB 表示从本地 SQLite 加载的动态工具。
	ToolSourceLocalDB ToolSource = "local_db"
	// ToolSourceMCP 表示通过 MCP server 暴露的远端代理工具。
	ToolSourceMCP ToolSource = "mcp"
	// ToolSourcePlugin 表示未来由外部插件（WASM/.so/子进程）加载的工具。
	ToolSourcePlugin ToolSource = "plugin"
)

// ToolDescriptor 是纯数据、可 JSON 序列化的工具元数据。
// 它与具体的执行体解耦，使工具可以被持久化、网络传输并在不同 runtime
// 中重新构造执行。
//
// ExecutionConfig 的语义由 Source 决定：
//   - builtin：通常为空，执行体由代码注册。
//   - local_db：含 type / command / url / method / code 等字段，由 DynamicExecutor 解析。
//   - mcp：含 server_name，由 MCP ProxyTool 解析。
//   - plugin：未来扩展。
type ToolDescriptor struct {
	Namespace       string         `json:"namespace"`
	Name            string         `json:"name"`
	Version         string         `json:"version"`
	Source          ToolSource     `json:"source"`
	Description     string         `json:"description"`
	Parameters      map[string]any `json:"parameters"`
	Aliases         []string       `json:"aliases"`
	Tags            []string       `json:"tags"`
	ExecutionConfig map[string]any `json:"execution_config"`
}

// FullName 返回工具的完全限定标识符。namespace 为空时返回 Name。
func (d ToolDescriptor) FullName() string {
	if d.Namespace == "" {
		return d.Name
	}
	return d.Namespace + "/" + d.Name
}

// CanonicalName 返回 Registry 使用的唯一键：namespace/name@version。
// namespace 为空时为 name@version；version 为空时回退到 FullName。
func (d ToolDescriptor) CanonicalName() string {
	fn := d.FullName()
	if d.Version == "" {
		return fn
	}
	return fmt.Sprintf("%s@%s", fn, d.Version)
}
