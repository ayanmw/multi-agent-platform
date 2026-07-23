// Package tool 实现 agent 工具系统，提供基于 registry 的机制供 agent 调用外部能力。
//
// # 工具系统概览
//
// 工具系统围绕 Tool 接口（定义于 registry.go）构建，该接口为工具执行提供
// 统一契约。每个工具暴露：
//   - Name：用于调用的唯一标识符
//   - Description：人类可读的工具用途说明
//   - Parameters：描述输入形状的 JSON Schema
//   - Execute：执行工具实际工作的运行时函数
//
// # 内置工具
//
// 本文件定义了三个始终可用的内置工具：
//   - run_shell：带超时支持的 shell 命令执行
//   - write_file：将内容写入文件系统，含 path traversal 保护
//   - read_file：读取文件内容，支持可配置字节上限与行 offset/limit
//
// # 安全
//
// 所有内置工具均含安全防护：
//   - run_shell：基于 context 的超时机制防止进程失控
//   - write_file：拒绝包含 ".." 的路径以防止 directory traversal
//   - read_file：强制最大字节上限（默认 1 MB）以防止内存耗尽
//
// # 注册
//
// 调用 RegisterBuiltins(registry) 可将所有内置工具注册到 tool.Registry 实例。
// 通过 NewToolFromJSON（位于 tool_json.go）创建的额外工具可单独注册。
package tool

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// BuiltinTool — Tool 接口的基础实现
// ---------------------------------------------------------------------------

// BuiltinTool 是 Tool 接口的具体实现，由一个简单的 executor 函数支撑。
// 它存储工具的元数据（name、namespace、description、JSON Schema 参数、
// aliases 与 tags），并将执行委托给所提供的 executor。
//
// BuiltinTool 供内置工具构造器（NewRunShellTool、NewWriteFileTool、
// NewReadFileTool）内部使用。外部调用方不应直接构造 BuiltinTool，
// 请改用 Registry 或 NewToolFromJSON。
type BuiltinTool struct {
	name        string
	namespace   string
	description string
	parameters  map[string]any
	tags        []string
	aliases     []string
	executor    func(ctx ExecuteContext, input map[string]any) (any, error)
}

// Namespace 返回工具的 namespace。空字符串表示工具位于全局 namespace；
// 非空 namespace 会生成 "namespace/name" 形式的 FullName。
func (t *BuiltinTool) Namespace() string { return t.namespace }

// Name 返回工具的唯一标识符，例如 "run_shell"。
func (t *BuiltinTool) Name() string { return t.name }

// FullName 返回工具的完全限定标识符。当 namespace 非空时返回
// "namespace/name"，否则返回 Name。
func (t *BuiltinTool) FullName() string {
	if t.namespace == "" {
		return t.name
	}
	return t.namespace + "/" + t.name
}

// Description 返回工具用途的人类可读说明，适合放入 LLM 的 system prompt。
func (t *BuiltinTool) Description() string { return t.description }

// Parameters 返回描述输入形状的 JSON Schema。
// 该 schema 遵循 JSON Schema (draft-07) 规范，含 "type"、"properties"
// 与 "required" 键。
func (t *BuiltinTool) Parameters() map[string]any { return t.parameters }

// Tags 返回工具的 tags。Tags 用于发现与过滤。
func (t *BuiltinTool) Tags() []string { return t.tags }

// Aliases 返回可解析到该工具的别名。
func (t *BuiltinTool) Aliases() []string { return t.aliases }

// WithAliases 为 BuiltinTool 附加 aliases，并返回自身以便链式调用。
func (t *BuiltinTool) WithAliases(aliases ...string) *BuiltinTool {
	t.aliases = append(t.aliases, aliases...)
	return t
}

// Execute 使用给定的输入 map 运行工具并返回结果。
// 输入 map 必须符合 Parameters() 返回的 schema。
func (t *BuiltinTool) Execute(input map[string]any) (any, error) {
	return t.executor(ExecuteContext{}, input)
}

// executeWithCtx 允许 Registry 显式注入 ExecuteContext（如工作目录）后执行工具。
func (t *BuiltinTool) executeWithCtx(ctx ExecuteContext, input map[string]any) (any, error) {
	return t.executor(ctx, input)
}

// Version 返回工具的版本标识符。BuiltinTool 默认无版本，返回空字符串。
func (t *BuiltinTool) Version() string { return "" }

// Source 返回工具来源。BuiltinTool 始终返回 "builtin"。
func (t *BuiltinTool) Source() string { return "builtin" }

// CanonicalName 返回 Registry 使用的唯一键。无版本时等于 FullName()。
func (t *BuiltinTool) CanonicalName() string {
	fn := t.FullName()
	if v := t.Version(); v != "" {
		return fmt.Sprintf("%s@%s", fn, v)
	}
	return fn
}

// NewBuiltinTool 使用给定的元数据创建一个新的 BuiltinTool。
// 当 namespace 非空时，工具的 FullName 为 "namespace/name"。
func NewBuiltinTool(name, namespace, description string, parameters map[string]any, executor func(ctx ExecuteContext, input map[string]any) (any, error)) *BuiltinTool {
	if executor == nil {
		panic("NewBuiltinTool: executor is nil")
	}
	return &BuiltinTool{
		name:        name,
		namespace:   namespace,
		description: description,
		parameters:  parameters,
		executor:    executor,
		tags:        []string{},
		aliases:     []string{},
	}
}

// NewBuiltinToolFromFunc 兼容旧闭包风格构造器。旧 executor 只接收 input map，
// 内部被包装为 ToolExecutor 接口形式，使历史调用点无需一次性重构。
func NewBuiltinToolFromFunc(name, namespace, description string, parameters map[string]any, fn func(input map[string]any) (any, error)) *BuiltinTool {
	return NewBuiltinTool(name, namespace, description, parameters, func(_ ExecuteContext, input map[string]any) (any, error) {
		return fn(input)
	})
}

// WithTags 为 BuiltinTool 附加 tags，并返回自身以便链式调用。
func (t *BuiltinTool) WithTags(tags ...string) *BuiltinTool {
	t.tags = append(t.tags, tags...)
	return t
}

// ---------------------------------------------------------------------------
// 从工具输入 map 中读取类型化值的通用辅助函数
// ---------------------------------------------------------------------------

// getString 从 m[key] 提取 string 值，当键缺失或值不是 string 时返回 def。
func getString(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return def
}

// getBool 从 m[key] 提取 bool 值，当键缺失或值不是 bool 时返回 def。
func getBool(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

// getInt 从 m[key] 提取整数值，当键缺失或值不是数值类型时返回 def。
// JSON 数字以 float64 反序列化，调用方也可能直接传入 int 或 int64。
func getInt(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return def
	}
}

// getMap 从 m[key] 提取嵌套 map 值，当键缺失或值不是 map[string]any 时返回 nil。
func getMap(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

// truncateObservation 把可能很长的字符串截断到 maxBytes 上限，供 observation 回喂
// 给 LLM 时控制上下文体积。截断时按字节计数（UTF-8 安全：只在 rune 边界截断），
// 并追加 "...[truncated]" 标记，让 LLM 明确知道内容被裁剪、需要时可主动追取全文。
// maxBytes <= 0 时不截断，原样返回。
func truncateObservation(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	// 回退到最后一个完整的 rune 边界，避免截断 UTF-8 多字节字符。
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...[truncated]"
}

// resolvePath 对工具输入路径做规范化。绝对路径会被 Clean 后原样返回。
// 相对路径会先尝试基于 input["workdir"] 解析，若不存在则回退到 ctx.Workdir，
// 最后再回到进程工作目录。
//
// 调用方应在调用 resolvePath 之前先通过 isPathTraversal 检查，以防止
// 通过 ".." 段进行 directory traversal。
func resolvePathWithCtx(path string, input map[string]any, ctx ExecuteContext) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	workdir, _ := input["workdir"].(string)
	if workdir == "" {
		workdir = ctx.Workdir
	}
	if workdir != "" {
		return filepath.Clean(filepath.Join(workdir, path))
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Clean(filepath.Join(wd, path))
	}
	return filepath.Clean(path)
}

// resolvePath 是 resolvePathWithCtx 的兼容封装，用于不携带 ExecuteContext 的调用点。
func resolvePath(path string, input map[string]any) string {
	return resolvePathWithCtx(path, input, ExecuteContext{})
}

// workdirFromInputOrCtx 返回 input["workdir"] 与 ctx.Workdir 的优先级合并结果，
// input 中显式指定的 workdir 优先。
func workdirFromInputOrCtx(input map[string]any, ctx ExecuteContext) string {
	workdir, _ := input["workdir"].(string)
	if workdir == "" {
		workdir = ctx.Workdir
	}
	return workdir
}

// ---------------------------------------------------------------------------
// run_shell — 带超时的 shell 命令执行
// ---------------------------------------------------------------------------

// NewRunShellTool 创建名为 "run_shell" 的 shell 执行工具。
//
// 参数：
//   - command  (string, required)：要执行的 shell 命令。
//   - workdir  (string, optional)：命令的工作目录。
//   - timeout_ms (integer, optional)：超时时间，单位毫秒（默认 30000）。
//
// 工具根据运行时 OS 选择合适的 shell：
//   - Windows：优先尝试 "bash"（Git Bash），失败时回退到 "cmd /c"。
//   - Linux/macOS：使用 "sh -c"。
//
// 执行由 context.WithTimeout 守护；若命令在超时内未完成，将被终止并返回错误。
func NewRunShellTool() *BuiltinTool {
	return &BuiltinTool{
		name:        "run_shell",
		description: "Execute a shell command and return its output. The command runs in the session's working directory (see system prompt for current directory). Use relative paths for file references (e.g. 'python script.py' not 'cd workspace && python script.py').",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
				"workdir": map[string]any{
					"type":        "string",
					"description": "Working directory for the command (optional — defaults to the session's working directory set automatically by the system)",
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Timeout in milliseconds (optional, default 30000)",
				},
			},
			"required": []string{"command"},
		},
		executor: func(ctx ExecuteContext, input map[string]any) (any, error) { return executeShell(ctx, input) },
	}
}

// executeShell 是 run_shell 工具的 executor 函数。
// 它解析 shell 二进制文件、创建带超时的 context，并通过 exec.CommandContext
// 运行命令。结果包含 stdout、stderr 与 exit_code。
func executeShell(ctx ExecuteContext, input map[string]any) (any, error) {
	cmdStr, ok := input["command"].(string)
	if !ok {
		return nil, fmt.Errorf("command must be a string")
	}

	// 根据当前 OS 确定 shell 二进制文件与对应 flag。
	var shell string
	var shellFlag string
	if runtime.GOOS == "windows" {
		// 优先尝试 bash（Git Bash），失败则回退到 cmd。
		shell = "bash"
		shellFlag = "-c"
		if _, err := exec.LookPath("bash"); err != nil {
			shell = "cmd"
			shellFlag = "/c"
		}
	} else {
		shell = "sh"
		shellFlag = "-c"
	}

	// 解析超时，默认 30 秒。
	timeoutMs := 30000
	if t, ok := input["timeout_ms"].(float64); ok && t > 0 {
		timeoutMs = int(t)
	}

	execCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(execCtx, shell, shellFlag, cmdStr)

	// 设置工作目录顺序：input["workdir"] > ctx.Workdir > 进程 CWD。
	workdir, _ := input["workdir"].(string)
	if workdir == "" {
		workdir = ctx.Workdir
	}
	if workdir != "" {
		cmd.Dir = workdir
	}

	output, err := cmd.CombinedOutput()
	result := map[string]any{
		"stdout":    string(output),
		"stderr":    "",
		"exit_code": 0,
	}

	if err != nil {
		// 显式检查 context 超时/取消。
		if execCtx.Err() != nil {
			result["exit_code"] = -1
			result["stderr"] = fmt.Sprintf("command timed out after %d ms", timeoutMs)
			return result, nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			result["exit_code"] = exitErr.ExitCode()
			result["stderr"] = err.Error()
		} else {
			result["exit_code"] = -1
			result["stderr"] = err.Error()
		}
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// write_file — 含 path traversal 保护的文件写入
// ---------------------------------------------------------------------------

// NewWriteFileTool 创建名为 "write_file" 的文件写入工具。
//
// 参数：
//   - path    (string, required)：要写入的文件路径。
//   - content (string, required)：要写入文件的内容。
//
// 工具会自动创建尚不存在的父目录。出于安全考虑，包含 ".." 的路径会被
// 拒绝，以防止 directory traversal 攻击。
func NewWriteFileTool() *BuiltinTool {
	return &BuiltinTool{
		name:        "write_file",
		description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Parent directories are created automatically. Use RELATIVE paths only — the working directory is set automatically by the system (see system prompt for the current working directory). Do NOT prepend directory segments like 'workspace/session-xxx/'. Example: {\"path\": \"snake_game.html\", \"content\": \"...\"}",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The RELATIVE file path to write to (e.g. \"output.txt\", \"src/main.go\"). The system resolves this against the current working directory. Do NOT use absolute paths.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "The text content to write to the file. This field is REQUIRED. Always provide the complete file content as a string.",
				},
			},
			"required": []string{"path", "content"},
		},
		executor: func(ctx ExecuteContext, input map[string]any) (any, error) { return executeWriteFile(ctx, input) },
	}
}

// isPathTraversal 当给定路径试图通过 ".." 段逃逸出其预期目录时返回 true。
func isPathTraversal(path string) bool {
	cleanPath := filepath.Clean(path)
	// 经过 Clean 之后，存在 traversal 的路径要么恰好是 ".."，
	// 要么以 ".." 加 OS 路径分隔符开头。
	if cleanPath == ".." {
		return true
	}
	if strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return true
	}
	return false
}

// executeWriteFile 是 write_file 工具的 executor 函数。
// 它校验路径、创建父目录并写入内容。
func executeWriteFile(ctx ExecuteContext, input map[string]any) (any, error) {
	path, ok := input["path"].(string)
	if !ok {
		return nil, fmt.Errorf("path must be a string")
	}
	content, ok := input["content"].(string)
	if !ok {
		return nil, fmt.Errorf("content must be a string")
	}

	// 在根据 workdir 解析之前先拒绝尝试 directory traversal 的路径。
	// filepath.Join + filepath.Clean 会规范化 ".." 段并悄无声息地重新
	// 改变路径根目录，从而绕过 traversal 检查。
	if isPathTraversal(path) {
		return nil, fmt.Errorf("path traversal detected: %s", path)
	}

	// 如提供 workdir，则将相对路径解析到 workdir 之下。优先取 input["workdir"]，
	// 未显式提供时回退到 ctx.Workdir，最后回到进程工作目录。
	if !filepath.IsAbs(path) {
		workdir := workdirFromInputOrCtx(input, ctx)
		if workdir != "" {
			path = filepath.Join(workdir, path)
		} else {
			wd, _ := os.Getwd()
			if wd != "" {
				path = filepath.Join(wd, path)
			}
		}
	}

	// 拒绝尝试 directory traversal 的路径。
	if isPathTraversal(path) {
		return nil, fmt.Errorf("path traversal detected: %s", path)
	}

	// 确保父目录存在。
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	return map[string]any{
		"success": true,
		"path":    path,
		"bytes":   len(content),
		"message": fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path),
	}, nil
}

// ---------------------------------------------------------------------------
// read_file — 带字节上限与行 offset/limit 的文件读取
// ---------------------------------------------------------------------------

// DefaultMaxBytes 是 read_file 从单个文件读取的默认最大字节数（1 MB）。
const DefaultMaxBytes = 1 << 20 // 1,048,576 bytes

// NewReadFileTool 创建名为 "read_file" 的文件读取工具。
//
// 参数：
//   - path      (string, required)：要读取的文件路径。
//   - offset    (integer, optional)：从第几行开始读取（1-based）。
//   - limit     (integer, optional)：最多读取的行数。
//   - max_bytes (integer, optional)：最多读取的字节数（默认 1048576 = 1 MB）。
//
// 工具读取文件内容，强制 max_bytes 上限，然后应用可选的行 offset/limit。
// 若文件超过 max_bytes，内容会被截断，并在结果中设置 "truncated" 标志。
func NewReadFileTool() *BuiltinTool {
	return &BuiltinTool{
		name:        "read_file",
		description: "Read the contents of a file. The working directory is set automatically by the system — use RELATIVE paths only (e.g. \"README.md\", \"src/main.go\"). Do NOT use absolute paths or prepend directory segments.",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The RELATIVE file path to read (e.g. \"README.md\", \"src/main.go\"). The system resolves this against the current working directory. Do NOT use absolute paths.",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Line number to start reading from (optional, 1-based)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of lines to read (optional)",
				},
				"max_bytes": map[string]any{
					"type":        "integer",
					"description": "Maximum bytes to read (optional, default 1048576 = 1 MB)",
				},
			},
			"required": []string{"path"},
		},
		executor: func(ctx ExecuteContext, input map[string]any) (any, error) { return executeReadFile(ctx, input) },
	}
}

// executeReadFile 是 read_file 工具的 executor 函数。
// 它打开文件，读取最多 max_bytes 字节（默认 1 MB），然后对结果内容
// 应用可选的行 offset 与 limit 过滤。
func executeReadFile(ctx ExecuteContext, input map[string]any) (any, error) {
	path, ok := input["path"].(string)
	if !ok {
		return nil, fmt.Errorf("path must be a string")
	}

	// 拒绝尝试 directory traversal 的路径。
	if isPathTraversal(path) {
		return nil, fmt.Errorf("path traversal detected: %s", path)
	}

	// 如提供 workdir，则将相对路径解析到 workdir 之下。优先取 input["workdir"]，
	// 未显式提供时回退到 ctx.Workdir。
	workdir := workdirFromInputOrCtx(input, ctx)
	if workdir != "" && !filepath.IsAbs(path) {
		path = filepath.Join(workdir, path)
	}

	// 在根据 workdir 解析之后再次拒绝尝试 directory traversal 的路径。
	if isPathTraversal(path) {
		return nil, fmt.Errorf("path traversal detected: %s", path)
	}

	// 确定最大读取字节数（默认 1 MB）。
	maxBytes := int64(DefaultMaxBytes)
	if mb, ok := input["max_bytes"].(float64); ok && mb > 0 {
		maxBytes = int64(mb)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	defer f.Close()

	// 最多读取 maxBytes+1 字节；若读到了多余的那一字节，说明文件被截断。
	lr := io.LimitReader(f, maxBytes+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	truncated := len(data) > int(maxBytes)
	if truncated {
		data = data[:maxBytes]
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	// 应用行 offset（1-based 转为 0-based）。
	offset := 0
	if o, ok := input["offset"].(float64); ok {
		offset = int(o) - 1
		if offset < 0 {
			offset = 0
		}
	}

	if offset >= len(lines) {
		return map[string]any{
			"content":     "",
			"path":        path,
			"total_lines": len(lines),
			"lines_read":  0,
			"truncated":   truncated,
			"bytes_read":  len(data),
		}, nil
	}

	// 应用行 limit。
	limit := len(lines) - offset
	if l, ok := input["limit"].(float64); ok {
		limit = int(l)
		if limit > len(lines)-offset {
			limit = len(lines) - offset
		}
	}
	if limit < 0 {
		limit = 0
	}

	selectedLines := lines[offset : offset+limit]
	result := strings.Join(selectedLines, "\n")

	return map[string]any{
		"content":     result,
		"path":        path,
		"total_lines": len(lines),
		"lines_read":  len(selectedLines),
		"offset":      offset + 1,
		"truncated":   truncated,
		"bytes_read":  len(data),
	}, nil
}

// ---------------------------------------------------------------------------
// RegisterBuiltins — 批量注册所有内置工具
// ---------------------------------------------------------------------------

// RegisterBuiltins 将所有内置工具（run_shell、write_file、read_file）注册到
// 所提供的 Registry 中。这是引导工具系统的主要入口。
//
// 用法：
//
//	reg := tool.NewRegistry()
//	tool.RegisterBuiltins(reg)
//	// ... 如需可额外注册自定义工具
func RegisterBuiltins(registry *Registry) {
	registry.Register(NewRunShellTool())
	registry.Register(NewWriteFileTool())
	registry.Register(NewReadFileTool())
	registry.Register(NewListDirTool())
	registry.Register(NewApplyDiffTool())
	registry.Register(NewDeleteFileTool())
	registry.Register(NewFetchURLTool())
	registry.Register(NewParseJSONTool())
	registry.Register(NewExecuteProgramTool())
	registry.Register(NewWebSearchTool(WebSearchConfig{}))
}

// SubAgentDispatcher 是 leader agent 派发子 agent 的抽象。
// 工具层只依赖该接口，具体实现由 cmd/server 注入，避免 tool 包与
// orchestrator 包形成双向依赖。
type SubAgentDispatcher interface {
	Dispatch(ctx context.Context, leaderSubTaskID string, strategy string, agents []SubAgentSpec) ([]SubAgentResult, error)
}

// SubAgentSpec 定义了一个子 agent 的规格，是 orchestrator.AgentSpec 的最小子集。
// 工具包不直接引用 orchestrator 类型，以打破 import cycle。
type SubAgentSpec struct {
	AgentID      string   `json:"agent_id"`
	Name         string   `json:"name"`
	SystemPrompt string   `json:"system_prompt"`
	Input        string   `json:"input"`
	Model        string   `json:"model,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	OutputTo     []string `json:"output_to,omitempty"`
}

// SubAgentResult 是子 agent 执行结果的最小子集。
type SubAgentResult struct {
	AgentID     string `json:"agent_id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Result      string `json:"result"`
	TotalTokens int    `json:"total_tokens"`
	Error       string `json:"error,omitempty"`
	Duration    int64  `json:"duration_ms"`
}

// NewLeaderTools 返回一个包含 leader 专用工具的 slice：
// dispatch_sub_agent、approve_sub_agent_action、reject_sub_agent_action。
// 每个 leader 调度的 taskID 在创建工具时直接注入，避免全局状态或占位符。
// 这些工具不应注册到 worker/chat 共享的 base registry，确保 worker 不会
// 在 tool list 中看到 dispatch_sub_agent（防止其浪费 token 去尝试调用）。
func NewLeaderTools(
	dispatcher SubAgentDispatcher,
	leaderSubTaskID string,
	resolveApproval func(approvalID string, approved bool, reason string) error,
) []Tool {
	return []Tool{
		NewDispatchSubAgentTool(dispatcher, leaderSubTaskID),
		NewApproveSubAgentActionTool(resolveApproval),
		NewRejectSubAgentActionTool(resolveApproval),
	}
}

// NewDispatchSubAgentTool 创建 dispatch_sub_agent 工具实例。
// 工具位于全局命名空间，仅注册在 leader 的 registry 中，因此天然只有
// leader 可调。leaderSubTaskID 是本次调度对应的 root task ID，直接传入，
// 替代原先 "<leaderSubTaskID>" 占位符与全局 atomic.Bool 权限控制。
func NewDispatchSubAgentTool(dispatcher SubAgentDispatcher, leaderSubTaskID string) *BuiltinTool {
	return NewBuiltinTool(
		"dispatch_sub_agent",
		"",
		"Dispatch sub-agents to solve parts of the current task. Only the leader agent may use this tool. Provide the reason, execution strategy, and a list of agent specifications. Each sub-agent will run with its own Engine in the orchestrator and the results will be returned to you as observations.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reason": map[string]any{
					"type":        "string",
					"description": "Why you are delegating this work to sub-agents",
				},
				"strategy": map[string]any{
					"type":        "string",
					"enum":        []string{"parallel", "sequential", "pipeline"},
					"description": "How to coordinate the sub-agents: parallel, sequential, or pipeline",
				},
				"agents": map[string]any{
					"type":        "array",
					"description": "List of sub-agent specifications",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"agent_id": map[string]any{
								"type":        "string",
								"description": "Unique identifier for this sub-agent",
							},
							"system_prompt": map[string]any{
								"type":        "string",
								"description": "System prompt defining the sub-agent's role and constraints",
							},
							"input": map[string]any{
								"type":        "string",
								"description": "Specific task input for this sub-agent",
							},
							"allowed_tools": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Tool names this sub-agent is allowed to use",
							},
							"output_to": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Agent IDs that should receive this sub-agent's final result",
							},
							"model": map[string]any{
								"type":        "string",
								"description": "LLM model for this sub-agent (optional, defaults to leader model)",
							},
						},
						"required": []string{"agent_id", "system_prompt"},
					},
				},
			},
			"required": []string{"reason", "strategy", "agents"},
		},
		func(_ ExecuteContext, input map[string]any) (any, error) {
			strategy := getString(input, "strategy", "parallel")
			switch strategy {
			case "parallel", "sequential", "pipeline":
			default:
				return nil, fmt.Errorf("strategy must be one of parallel, sequential, pipeline")
			}

			rawAgents, ok := input["agents"].([]any)
			if !ok || len(rawAgents) == 0 {
				return nil, fmt.Errorf("agents must be a non-empty array")
			}

			agents := make([]SubAgentSpec, 0, len(rawAgents))
			for i, raw := range rawAgents {
				m, ok := raw.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("agents[%d] is not an object", i)
				}

				agentID, _ := m["agent_id"].(string)
				if agentID == "" {
					return nil, fmt.Errorf("agents[%d].agent_id is required", i)
				}
				systemPrompt, _ := m["system_prompt"].(string)
				if systemPrompt == "" {
					return nil, fmt.Errorf("agents[%d].system_prompt is required", i)
				}

				spec := SubAgentSpec{
					AgentID:      agentID,
					Name:         agentID,
					SystemPrompt: systemPrompt,
					Input:        getString(m, "input", ""),
					Model:        getString(m, "model", ""),
				}
				if v, ok := m["allowed_tools"].([]any); ok {
					for _, item := range v {
						if s, ok := item.(string); ok {
							spec.AllowedTools = append(spec.AllowedTools, s)
						}
					}
				}
				if v, ok := m["output_to"].([]any); ok {
					for _, item := range v {
						if s, ok := item.(string); ok {
							spec.OutputTo = append(spec.OutputTo, s)
						}
					}
				}
				agents = append(agents, spec)
			}

			// 在 Phase 7-H 中，leader 的 SubTaskID 与 root task ID 相同，
			// 因此这里把创建工具时注入的 leaderSubTaskID 直接传给 orchestrator。
			results, err := dispatcher.Dispatch(context.Background(), leaderSubTaskID, strategy, agents)
			if err != nil {
				return nil, fmt.Errorf("dispatch failed: %w", err)
			}

			// Phase 7-H2 阶段 5：标准化 observation，供 leader 多轮 dispatch 决策。
			// 设计要点（leader 可能在一次任务里多次调用本工具，每轮 observation 都会
			// 进入其对话历史，因此必须紧凑且自解释）：
			//   - summary：一句话顶层摘要，leader 可仅凭它判断本轮是否完成、是否需要
			//     再派发后续 agent，而不必逐条扫描 results。
			//   - all_completed / completed_count：快速成功标志，避免 leader 逐条比对
			//     status 字符串（也规避 LLM 把 "skipped" 误判为成功的风险）。
			//   - 每个 result 携带 succeeded bool 与截断后的 result 文本：worker 输出
			//     可能极长（如 read_file 大文件），不截断会撑爆 leader 上下文。完整
			//     长度通过 result_truncated 标记暴露，leader 知道何时需要主动追取全文。
			//   - total_tokens：本轮所有 worker 的 token 汇总，便于 leader 在多轮
			//     dispatch 中感知累计成本。
			const maxResultBytes = 4000
			resultItems := make([]map[string]any, 0, len(results))
			completedCount := 0
			totalTokens := 0
			for _, r := range results {
				if r.Status == "completed" {
					completedCount++
				}
				totalTokens += r.TotalTokens
				resultItems = append(resultItems, map[string]any{
					"agent_id":         r.AgentID,
					"name":             r.Name,
					"status":           r.Status,
					"succeeded":        r.Status == "completed",
					"result":           truncateObservation(r.Result, maxResultBytes),
					"result_truncated": len(r.Result) > maxResultBytes,
					"total_tokens":     r.TotalTokens,
					"error":            r.Error,
					"duration_ms":      r.Duration,
				})
			}

			allCompleted := len(results) > 0 && completedCount == len(results)
			nonCompleted := len(results) - completedCount
			summary := fmt.Sprintf("dispatched %d sub-agent(s) with strategy=%s; %d completed, %d failed/skipped",
				len(agents), strategy, completedCount, nonCompleted)
			if len(results) == 0 {
				summary = fmt.Sprintf("dispatched %d sub-agent(s) with strategy=%s; no results returned", len(agents), strategy)
			} else if allCompleted {
				summary = fmt.Sprintf("dispatched %d sub-agent(s) with strategy=%s; all completed", len(agents), strategy)
			}

			return map[string]any{
				"dispatched":      true,
				"agent_count":     len(agents),
				"strategy":        strategy,
				"all_completed":   allCompleted,
				"completed_count": completedCount,
				"total_tokens":    totalTokens,
				"summary":         summary,
				"results":         resultItems,
			}, nil
		},
	).WithTags("orchestration")
}

// NewListDirTool 创建名为 "core/list_dir" 的目录列举工具。
func NewListDirTool() *BuiltinTool {
	return NewBuiltinTool(
		"list_dir",
		"core",
		"List files and directories. Use relative paths only (resolved against working directory). Set recursive=true for nested listing; max_depth controls recursion depth (default 3).",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Directory path to list (default \".\")",
				},
				"recursive": map[string]any{
					"type":        "boolean",
					"description": "If true, list contents recursively",
				},
				"max_depth": map[string]any{
					"type":        "integer",
					"description": "Maximum recursion depth when recursive (default 3)",
				},
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern to filter entries by name",
				},
				"include_hidden": map[string]any{
					"type":        "boolean",
					"description": "If true, include hidden entries",
				},
			},
			"required": []string{},
		},
		func(_ ExecuteContext, input map[string]any) (any, error) { return listDirExecutor(input) },
	).WithTags("filesystem", "filesystem:readonly")
}

// listDirExecutor 实现 list_dir 工具的逻辑。
func listDirExecutor(input map[string]any) (any, error) {
	path := getString(input, "path", ".")
	recursive := getBool(input, "recursive", false)
	maxDepth := getInt(input, "max_depth", 3)
	pattern := getString(input, "pattern", "")
	includeHidden := getBool(input, "include_hidden", false)

	if isPathTraversal(path) {
		return nil, fmt.Errorf("path traversal not allowed: %s", path)
	}
	path = resolvePath(path, input)

	entries, err := walkDir(path, recursive, maxDepth, pattern, includeHidden)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"path":      path,
		"entries":   entries,
		"total":     len(entries),
		"truncated": false,
	}, nil
}

// walkDir 按所提供的过滤条件枚举 root 之下的条目。
func walkDir(root string, recursive bool, maxDepth int, pattern string, includeHidden bool) ([]map[string]any, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", root)
	}

	root = filepath.Clean(root)
	rootDepth := len(strings.Split(root, string(filepath.Separator)))

	var out []map[string]any
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if !includeHidden && strings.HasPrefix(rel, ".") {
			if d.IsDir() && recursive {
				return fs.SkipDir
			}
			return nil
		}
		if pattern != "" {
			matched, _ := filepath.Match(pattern, d.Name())
			if !matched {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		if recursive {
			depth := len(strings.Split(p, string(filepath.Separator))) - rootDepth
			if depth > maxDepth {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}

		entry := map[string]any{
			"name": d.Name(),
			"type": "file",
			"path": p,
		}
		if d.IsDir() {
			entry["type"] = "dir"
		}
		if info, e := d.Info(); e == nil {
			entry["size"] = info.Size()
			entry["mod_time"] = info.ModTime().UTC().Format(time.RFC3339)
		}

		if !recursive && d.IsDir() {
			// 对于非递归模式，我们仍报告目录本身，但不应进入其中。
			entry["type"] = "dir"
			out = append(out, entry)
			return fs.SkipDir
		}

		out = append(out, entry)
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		return out[i]["path"].(string) < out[j]["path"].(string)
	})
	return out, err
}

// NewApproveSubAgentActionTool 创建 approve_sub_agent_action 工具。
// 该工具由 supervisor leader 调用，表示批准一个子 agent 的高风险动作。
//
// Parameters:
//   - approval_id (string, required): 需要批准的审批请求 ID。
//   - reason    (string, optional): 批准的原因说明。
func NewApproveSubAgentActionTool(resolve func(approvalID string, approved bool, reason string) error) *BuiltinTool {
	return NewBuiltinTool(
		"approve_sub_agent_action",
		"",
		"Approve a delegated high-risk action from a sub-agent. Only the supervisor leader should call this tool. Provide the approval_id returned in the approval_request message and an optional reason.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"approval_id": map[string]any{
					"type":        "string",
					"description": "The approval request ID to approve",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Reason for approving the action (optional)",
				},
			},
			"required": []string{"approval_id"},
		},
		func(_ ExecuteContext, input map[string]any) (any, error) {
			approvalID, ok := input["approval_id"].(string)
			if !ok || approvalID == "" {
				return nil, fmt.Errorf("approval_id is required")
			}
			reason := getString(input, "reason", "approved by leader")
			if err := resolve(approvalID, true, reason); err != nil {
				return nil, fmt.Errorf("failed to resolve approval: %w", err)
			}
			return map[string]any{
				"approved":    true,
				"approval_id": approvalID,
				"reason":      reason,
			}, nil
		},
	).WithTags("orchestration", "approval")
}

// NewRejectSubAgentActionTool 创建 reject_sub_agent_action 工具。
// 该工具由 supervisor leader 调用，表示拒绝一个子 agent 的高风险动作。
//
// Parameters:
//   - approval_id (string, required): 需要拒绝的审批请求 ID。
//   - reason    (string, optional): 拒绝的原因说明。
func NewRejectSubAgentActionTool(resolve func(approvalID string, approved bool, reason string) error) *BuiltinTool {
	return NewBuiltinTool(
		"reject_sub_agent_action",
		"",
		"Reject a delegated high-risk action from a sub-agent. Only the supervisor leader should call this tool. Provide the approval_id returned in the approval_request message and an optional reason.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"approval_id": map[string]any{
					"type":        "string",
					"description": "The approval request ID to reject",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Reason for rejecting the action (optional)",
				},
			},
			"required": []string{"approval_id"},
		},
		func(_ ExecuteContext, input map[string]any) (any, error) {
			approvalID, ok := input["approval_id"].(string)
			if !ok || approvalID == "" {
				return nil, fmt.Errorf("approval_id is required")
			}
			reason := getString(input, "reason", "rejected by leader")
			if err := resolve(approvalID, false, reason); err != nil {
				return nil, fmt.Errorf("failed to resolve approval: %w", err)
			}
			return map[string]any{
				"approved":    false,
				"approval_id": approvalID,
				"reason":      reason,
			}, nil
		},
	).WithTags("orchestration", "approval")
}
