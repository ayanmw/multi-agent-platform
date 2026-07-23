// Package tool — DynamicTool：agent 可调用的运行时注册工具。
//
// DynamicTool 为通过 REST API (/api/tools) 在运行时创建的工具实现 Tool 接口。
// 它支持三种执行类型：
//
//   - shell：带输入替换的 shell 命令执行
//   - http：带输入替换的 HTTP 请求
//   - inline：为未来代码执行预留（仅存储代码）
//
// # 输入替换
//
// shell 与 http 工具均支持在 command/url 模板中使用 {param_name} 占位符。
// 调用 Execute 时，占位符会被输入 map 中的对应值替换。例如，命令模板
// "echo {name}" 配合输入 {"name": "world"} 将变为 "echo world"。
//
// # 安全
//
// 所有 dynamic tool 执行均由 30 秒的 context 超时守护，
// 防止进程失控或 HTTP 请求挂起。
package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// DynamicToolType 表示 dynamic tool 的执行策略。
type DynamicToolType string

const (
	// DynamicToolShell 执行 shell 命令模板。
	DynamicToolShell DynamicToolType = "shell"
	// DynamicToolHTTP 向 URL 模板发起 HTTP 请求。
	DynamicToolHTTP DynamicToolType = "http"
	// DynamicToolInline 存储代码以供未来执行（预留）。
	DynamicToolInline DynamicToolType = "inline"
)

// DynamicTool 是运行时注册的工具，实现了 Tool 接口。
// 与使用预编译 executor 函数的 BuiltinTool 不同，DynamicTool 将其执行
// 配置（command、url、code）作为数据存储，并在执行时通过输入替换进行求值。
type DynamicTool struct {
	name        string
	description string
	parameters  map[string]any
	toolType    DynamicToolType
	// namespace/version 由 descriptor 透传，支持多版本并存与分组。
	// 旧 NewDynamicTool 路径不设置，保持空（namespace="" 仍属全局 namespace）。
	namespace string
	version   string
	// 对于 shell 类型：含 {param} 占位符的 shell 命令模板
	command string
	// 对于 http 类型：URL 模板与 HTTP method
	url    string
	method string
	// 对于 inline 类型：存储的代码（为未来预留）
	code string
}

// NewDynamicTool 以给定配置创建一个新的 DynamicTool。
// 调用方需根据工具类型通过 Set* 方法设置相应的执行字段（command、url、code）。
func NewDynamicTool(name, description string, parameters map[string]any, toolType DynamicToolType) *DynamicTool {
	return &DynamicTool{
		name:        name,
		description: description,
		parameters:  parameters,
		toolType:    toolType,
	}
}

// NewDynamicToolFromDescriptor 从 ToolDescriptor 构造 DynamicTool。
// 这是 DBToolLoader 还原持久化工具的入口：descriptor 中的 ExecutionConfig
// 携带 type/command/url/method/code 等字段，这里拆解到 DynamicTool 对应字段。
// namespace/version 透传到元数据方法（Namespace/Version），供 Registry 多版本键使用。
func NewDynamicToolFromDescriptor(desc ToolDescriptor) *DynamicTool {
	t := &DynamicTool{
		name:        desc.Name,
		description: desc.Description,
		parameters:  desc.Parameters,
		toolType:    DynamicToolType(getString(desc.ExecutionConfig, "type", "")),
		namespace:   desc.Namespace,
		version:     desc.Version,
	}
	t.command = getString(desc.ExecutionConfig, "command", "")
	t.url = getString(desc.ExecutionConfig, "url", "")
	t.method = getString(desc.ExecutionConfig, "method", "")
	t.code = getString(desc.ExecutionConfig, "code", "")
	return t
}

// SetCommand 设置 shell 类型工具的 shell 命令模板。
func (t *DynamicTool) SetCommand(cmd string) { t.command = cmd }

// SetHTTP 设置 http 类型工具的 URL 与 method。
func (t *DynamicTool) SetHTTP(url, method string) {
	t.url = url
	t.method = method
}

// SetCode 设置 inline 类型工具的 inline 代码（预留）。
func (t *DynamicTool) SetCode(code string) { t.code = code }

// Command 返回 shell 命令模板（仅 shell 类型）。
func (t *DynamicTool) Command() string { return t.command }

// URL 返回 HTTP URL 模板（仅 http 类型）。
func (t *DynamicTool) URL() string { return t.url }

// Method 返回 HTTP method（仅 http 类型）。
func (t *DynamicTool) Method() string { return t.method }

// Code 返回 inline 代码（仅 inline 类型）。
func (t *DynamicTool) Code() string { return t.code }

// ToolType 返回该 dynamic tool 的执行类型。
func (t *DynamicTool) ToolType() DynamicToolType { return t.toolType }

// Name 返回工具的唯一标识符。
func (t *DynamicTool) Namespace() string { return t.namespace }
func (t *DynamicTool) Name() string      { return t.name }

// FullName 返回工具的完全限定标识符。Dynamic tool 位于全局 namespace，
// 因此 FullName 等于 Name。
func (t *DynamicTool) FullName() string { return t.name }

// Version 返回工具的版本标识符。DynamicTool 默认无版本；
// 由 descriptor 构造时透传 desc.Version。
func (t *DynamicTool) Version() string { return t.version }

// Source 返回工具来源。DynamicTool 始终返回 "local_db"。
func (t *DynamicTool) Source() string { return "local_db" }

// CanonicalName 返回 Registry 使用的唯一键。无版本时等于 FullName()。
func (t *DynamicTool) CanonicalName() string {
	if v := t.Version(); v != "" {
		return fmt.Sprintf("%s@%s", t.FullName(), v)
	}
	return t.FullName()
}

// Aliases 返回该 dynamic tool 的别名。默认情况下 dynamic tool 没有别名。
func (t *DynamicTool) Aliases() []string { return nil }

// Description 返回工具用途的人类可读说明。
func (t *DynamicTool) Description() string { return t.description }

// Parameters 返回描述输入形状的 JSON Schema。
func (t *DynamicTool) Parameters() map[string]any { return t.parameters }

// Tags 返回工具的 tags。默认情况下 dynamic tool 不带 tags。
func (t *DynamicTool) Tags() []string { return nil }

// Execute 使用给定输入 map 运行 dynamic tool。
// 它根据工具类型分派到相应的执行策略。
func (t *DynamicTool) Execute(input map[string]any) (any, error) {
	switch t.toolType {
	case DynamicToolShell:
		return t.executeShell(input)
	case DynamicToolHTTP:
		return t.executeHTTP(input)
	case DynamicToolInline:
		return t.executeInline(input)
	default:
		return nil, fmt.Errorf("unknown dynamic tool type: %s", t.toolType)
	}
}

// executeShell 运行带输入替换的 shell 命令。
// 命令模板中的 {param_name} 占位符会被输入 map 中的对应值替换。
// 执行由 30 秒的 context 超时守护。
func (t *DynamicTool) executeShell(input map[string]any) (any, error) {
	cmdStr := sanitizeInput(t.command, input)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// dynamic tool 在所有平台上都使用 sh -c（行为一致）。
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)

	output, err := cmd.CombinedOutput()
	result := map[string]any{
		"stdout":    string(output),
		"stderr":    "",
		"exit_code": 0,
	}

	if err != nil {
		if ctx.Err() != nil {
			result["exit_code"] = -1
			result["stderr"] = "command timed out after 30 seconds"
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

// executeHTTP 发起带输入替换的 HTTP 请求。
// URL 模板中的 {param_name} 占位符会被输入 map 中的对应值替换。
// 请求由 30 秒的超时守护。
func (t *DynamicTool) executeHTTP(input map[string]any) (any, error) {
	url := sanitizeInput(t.url, input)
	method := t.method
	if method == "" {
		method = "GET"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB 上限
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return map[string]any{
		"status_code": resp.StatusCode,
		"body":        string(body),
		"url":         url,
		"method":      method,
	}, nil
}

// executeInline 是 inline 代码执行的占位实现（预留）。
func (t *DynamicTool) executeInline(input map[string]any) (any, error) {
	return map[string]any{
		"message": "inline execution not yet implemented (Phase 5+)",
		"code":    t.code,
		"input":   input,
	}, nil
}

// sanitizeInput 将模板字符串中的 {param_name} 占位符替换为输入 map 中的
// 对应值。未在输入 map 中找到的键保持原样（占位符仍保留在输出中）。
func sanitizeInput(template string, input map[string]any) string {
	result := template
	for key, value := range input {
		placeholder := fmt.Sprintf("{%s}", key)
		result = strings.ReplaceAll(result, placeholder, fmt.Sprintf("%v", value))
	}
	return result
}
