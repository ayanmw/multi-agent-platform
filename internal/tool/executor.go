package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// ExecuteContext 携带一次工具执行的上下文信息。
// 当前包含工作目录以及运行时的 task/agent/step 身份、事件总线和 LLM provider。
// 这些字段使 tool 能够在保持白盒可观测的前提下发起内部 LLM 调用或广播状态事件。
type ExecuteContext struct {
	Workdir string

	// 当前 tool 调用所属的任务/Agent/Step 身份,用于事件与日志。
	TaskID    string
	AgentID   string
	StepIdx   int
	SessionID string

	// EventBus 允许 tool 向 WebSocket 前端发送事件。
	// 可能为 nil,调用方应使用 nil-safe 包装。
	EventBus EventBus

	// LLMProvider 允许 tool 在内部调用 LLM(如 web_research 的摘要)。
	// 可能为 nil,调用方应检查非空后再使用。
	LLMProvider LLMProvider
}

// EventBus 是 tool 包需要的事件总线最小接口。
type EventBus interface {
	SendEvent(event.Event)
}

// LLMProvider 是 tool 包对 llm.Provider 的最小使用子集。
// 通过自定义接口避免 tool 包直接依赖 llm 包,减少循环依赖风险。
type LLMProvider interface {
	Chat(req any) (any, error)
}

// ToolExecutor 是工具执行体的抽象。它接收执行上下文与输入,返回结构化结果。
// 通过把执行逻辑抽象为接口,BuiltinTool 与 DynamicTool 可共享同一执行模型,
// 未来 plugin/WASM 实现也能接入。
type ToolExecutor interface {
	// Execute 执行工具。ctx 携带这次调用的上下文信息;input 为 LLM/tool_call
	// 提供的参数 map,必须符合该工具的 JSON Schema。
	Execute(ctx ExecuteContext, input map[string]any) (any, error)
}

// BuiltinExecutor 用 Go 函数实现 ToolExecutor。
// 这是内置工具的主要执行体包装。
type BuiltinExecutor struct {
	Fn func(ctx ExecuteContext, input map[string]any) (any, error)
}

// Execute 调用内部 Fn 并返回结果。
func (e *BuiltinExecutor) Execute(ctx ExecuteContext, input map[string]any) (any, error) {
	return e.Fn(ctx, input)
}

// DynamicExecutor 根据 ToolDescriptor.ExecutionConfig 中的 type 字段,
// 在 shell / http / inline 三种策略间分派执行。
type DynamicExecutor struct {
	desc ToolDescriptor
}

// NewDynamicExecutor 用给定 descriptor 创建 DynamicExecutor。
func NewDynamicExecutor(desc ToolDescriptor) *DynamicExecutor {
	return &DynamicExecutor{desc: desc}
}

// Execute 解析 ExecutionConfig["type"] 并分派到具体执行策略。
func (e *DynamicExecutor) Execute(ctx ExecuteContext, input map[string]any) (any, error) {
	toolType, _ := e.desc.ExecutionConfig["type"].(string)
	switch DynamicToolType(toolType) {
	case DynamicToolShell:
		return e.executeShell(ctx, input)
	case DynamicToolHTTP:
		return e.executeHTTP(ctx, input)
	case DynamicToolInline:
		return e.executeInline(ctx, input)
	default:
		return nil, fmt.Errorf("unknown dynamic tool type: %s", toolType)
	}
}

// executeShell 运行 ExecutionConfig["command"] 模板,替换 {param} 占位符。
func (e *DynamicExecutor) executeShell(ctx ExecuteContext, input map[string]any) (any, error) {
	template, _ := e.desc.ExecutionConfig["command"].(string)
	cmdStr := sanitizeInput(template, input)

	execCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", cmdStr)
	if ctx.Workdir != "" {
		cmd.Dir = ctx.Workdir
	}

	output, err := cmd.CombinedOutput()
	result := map[string]any{
		"stdout":    string(output),
		"stderr":    "",
		"exit_code": 0,
	}
	if err != nil {
		if execCtx.Err() != nil {
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

// executeHTTP 发起 ExecutionConfig["url"] 模板请求,method 由
// ExecutionConfig["method"] 提供,默认 GET。
func (e *DynamicExecutor) executeHTTP(ctx ExecuteContext, input map[string]any) (any, error) {
	urlTemplate, _ := e.desc.ExecutionConfig["url"].(string)
	url := sanitizeInput(urlTemplate, input)
	method, _ := e.desc.ExecutionConfig["method"].(string)
	if method == "" {
		method = "GET"
	}

	execCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(execCtx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
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

// executeInline 是 inline 代码执行的占位实现,未来可接入受限执行环境。
func (e *DynamicExecutor) executeInline(ctx ExecuteContext, input map[string]any) (any, error) {
	code, _ := e.desc.ExecutionConfig["code"].(string)
	return map[string]any{
		"message": "inline execution not yet implemented (Phase 5+)",
		"code":    code,
		"input":   input,
	}, nil
}

// sanitizeInput 将模板字符串中的 {param_name} 占位符替换为输入 map 中的
// 对应值。未在输入 map 中找到的键保持原样(占位符仍保留在输出中)。
func sanitizeInput(template string, input map[string]any) string {
	result := template
	for key, value := range input {
		placeholder := fmt.Sprintf("{%s}", key)
		result = strings.ReplaceAll(result, placeholder, fmt.Sprintf("%v", value))
	}
	return result
}
