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
	"fmt"
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
// 它把执行逻辑委托给 DynamicExecutor，自身保持为"Descriptor + Executor 薄壳"，
// 使持久化、加载与执行通过同一可序列化描述符对齐。
type DynamicTool struct {
	name, namespace, version, description string
	parameters                            map[string]any
	toolType                              DynamicToolType
	descriptor                            ToolDescriptor
	executor                              *DynamicExecutor
}

// NewDynamicTool 以给定配置创建一个新的 DynamicTool。
// 调用方需根据工具类型通过 Set* 方法设置相应的执行字段（command、url、code）。
func NewDynamicTool(name, description string, parameters map[string]any, toolType DynamicToolType) *DynamicTool {
	desc := ToolDescriptor{
		Name:            name,
		Description:     description,
		Parameters:      parameters,
		Source:          ToolSourceLocalDB,
		ExecutionConfig: map[string]any{"type": string(toolType)},
	}
	return &DynamicTool{
		name:        name,
		description: description,
		parameters:  parameters,
		toolType:    toolType,
		descriptor:  desc,
		executor:    NewDynamicExecutor(desc),
	}
}

// NewDynamicToolFromDescriptor 从 ToolDescriptor 构造 DynamicTool。
// 这是 DBToolLoader 还原持久化工具的入口：descriptor 中的 ExecutionConfig
// 携带 type/command/url/method/code 等字段，统一由 DynamicExecutor 解析执行。
func NewDynamicToolFromDescriptor(desc ToolDescriptor) *DynamicTool {
	if desc.Source == "" {
		desc.Source = ToolSourceLocalDB
	}
	t := &DynamicTool{
		name:        desc.Name,
		description: desc.Description,
		parameters:  desc.Parameters,
		toolType:    DynamicToolType(getString(desc.ExecutionConfig, "type", "")),
		namespace:   desc.Namespace,
		version:     desc.Version,
		descriptor:  desc,
		executor:    NewDynamicExecutor(desc),
	}
	return t
}

// SetCommand 设置 shell 类型工具的 shell 命令模板。
func (t *DynamicTool) SetCommand(cmd string) {
	t.descriptor.ExecutionConfig["command"] = cmd
	t.executor = NewDynamicExecutor(t.descriptor)
}

// SetHTTP 设置 http 类型工具的 URL 与 method。
func (t *DynamicTool) SetHTTP(url, method string) {
	t.descriptor.ExecutionConfig["url"] = url
	t.descriptor.ExecutionConfig["method"] = method
	t.executor = NewDynamicExecutor(t.descriptor)
}

// SetCode 设置 inline 类型工具的 inline 代码（预留）。
func (t *DynamicTool) SetCode(code string) {
	t.descriptor.ExecutionConfig["code"] = code
	t.executor = NewDynamicExecutor(t.descriptor)
}

// Command 返回 shell 命令模板（仅 shell 类型）。
func (t *DynamicTool) Command() string { return getString(t.descriptor.ExecutionConfig, "command", "") }

// URL 返回 HTTP URL 模板（仅 http 类型）。
func (t *DynamicTool) URL() string { return getString(t.descriptor.ExecutionConfig, "url", "") }

// Method 返回 HTTP method（仅 http 类型）。
func (t *DynamicTool) Method() string { return getString(t.descriptor.ExecutionConfig, "method", "") }

// Code 返回 inline 代码（仅 inline 类型）。
func (t *DynamicTool) Code() string { return getString(t.descriptor.ExecutionConfig, "code", "") }

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
// 它把执行委托给 DynamicExecutor，使执行路径与工具描述符保持一致。
func (t *DynamicTool) Execute(input map[string]any) (any, error) {
	return t.executor.Execute(ExecuteContext{}, input)
}

// executeWithCtx 供 Registry 注入 ExecuteContext 时调用。
func (t *DynamicTool) executeWithCtx(ctx ExecuteContext, input map[string]any) (any, error) {
	return t.executor.Execute(ctx, input)
}
