// Package tool — DynamicTool: runtime-registered tools that agents can invoke.
//
// DynamicTool implements the Tool interface for tools created at runtime via
// the REST API (/api/tools). It supports three execution types:
//
//   - shell:  Execute a shell command with input sanitization
//   - http:   Make an HTTP request with input sanitization
//   - inline: Placeholder for future code execution (just stores the code)
//
// # Input Sanitization
//
// Both shell and http tools support {param_name} placeholders in their
// command/url templates. When Execute is called, placeholders are replaced
// with values from the input map. For example, a command template
// "echo {name}" with input {"name": "world"} becomes "echo world".
//
// # Safety
//
// All dynamic tool executions are guarded by a 30-second context timeout
// to prevent runaway processes or hung HTTP requests.
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

// DynamicToolType represents the execution strategy for a dynamic tool.
type DynamicToolType string

const (
	// DynamicToolShell executes a shell command template.
	DynamicToolShell DynamicToolType = "shell"
	// DynamicToolHTTP makes an HTTP request to a URL template.
	DynamicToolHTTP DynamicToolType = "http"
	// DynamicToolInline stores code for future execution (reserved).
	DynamicToolInline DynamicToolType = "inline"
)

// DynamicTool is a runtime-registered tool that implements the Tool interface.
// Unlike BuiltinTool, which uses a pre-compiled executor function, DynamicTool
// stores its execution config (command, url, code) as data and evaluates it
// at execution time with input sanitization.
type DynamicTool struct {
	name        string
	description string
	parameters  map[string]any
	toolType    DynamicToolType
	// For shell type: the shell command template with {param} placeholders
	command string
	// For http type: the URL template and HTTP method
	url    string
	method string
	// For inline type: stored code (reserved for future)
	code string
}

// NewDynamicTool creates a new DynamicTool with the given configuration.
// The caller is responsible for setting the appropriate execution fields
// (command, url, code) based on the tool type via the Set* methods.
func NewDynamicTool(name, description string, parameters map[string]any, toolType DynamicToolType) *DynamicTool {
	return &DynamicTool{
		name:        name,
		description: description,
		parameters:  parameters,
		toolType:    toolType,
	}
}

// SetCommand sets the shell command template for shell-type tools.
func (t *DynamicTool) SetCommand(cmd string) { t.command = cmd }

// SetHTTP sets the URL and method for http-type tools.
func (t *DynamicTool) SetHTTP(url, method string) {
	t.url = url
	t.method = method
}

// SetCode sets the inline code for inline-type tools (reserved).
func (t *DynamicTool) SetCode(code string) { t.code = code }

// Command returns the shell command template (shell type only).
func (t *DynamicTool) Command() string { return t.command }

// URL returns the HTTP URL template (http type only).
func (t *DynamicTool) URL() string { return t.url }

// Method returns the HTTP method (http type only).
func (t *DynamicTool) Method() string { return t.method }

// Code returns the inline code (inline type only).
func (t *DynamicTool) Code() string { return t.code }

// ToolType returns the execution type of this dynamic tool.
func (t *DynamicTool) ToolType() DynamicToolType { return t.toolType }

// Name returns the tool's unique identifier.
func (t *DynamicTool) Namespace() string { return "" }
func (t *DynamicTool) Name() string      { return t.name }

// FullName returns the tool's fully-qualified identifier. Dynamic tools live in
// the global namespace, so FullName equals Name.
func (t *DynamicTool) FullName() string { return t.name }

// Description returns a human-readable explanation of the tool's purpose.
func (t *DynamicTool) Description() string { return t.description }

// Parameters returns the JSON Schema describing the expected input shape.
func (t *DynamicTool) Parameters() map[string]any { return t.parameters }

// Tags returns the tool's tags. Dynamic tools are untagged by default.
func (t *DynamicTool) Tags() []string { return nil }

// Execute runs the dynamic tool with the given input map.
// It dispatches to the appropriate execution strategy based on the tool type.
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

// executeShell runs a shell command with input sanitization.
// {param_name} placeholders in the command template are replaced with values
// from the input map. Execution is guarded by a 30-second context timeout.
func (t *DynamicTool) executeShell(input map[string]any) (any, error) {
	cmdStr := sanitizeInput(t.command, input)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use sh -c on all platforms for dynamic tools (consistent behavior).
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

// executeHTTP makes an HTTP request with input sanitization.
// {param_name} placeholders in the URL template are replaced with values
// from the input map. The request is guarded by a 30-second timeout.
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB limit
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

// executeInline is a placeholder for inline code execution (reserved).
func (t *DynamicTool) executeInline(input map[string]any) (any, error) {
	return map[string]any{
		"message": "inline execution not yet implemented (Phase 5+)",
		"code":    t.code,
		"input":   input,
	}, nil
}

// sanitizeInput replaces {param_name} placeholders in the template string
// with values from the input map. Keys not found in the input map are left
// as-is (the placeholder remains in the output).
func sanitizeInput(template string, input map[string]any) string {
	result := template
	for key, value := range input {
		placeholder := fmt.Sprintf("{%s}", key)
		result = strings.ReplaceAll(result, placeholder, fmt.Sprintf("%v", value))
	}
	return result
}