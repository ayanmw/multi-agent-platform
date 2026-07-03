// Package tool implements the agent tool system, providing a registry-based
// mechanism for agents to invoke external capabilities.
//
// # Tool System Overview
//
// The tool system is built around the Tool interface (defined in registry.go),
// which provides a uniform contract for tool execution. Every tool exposes:
//   - Name: a unique identifier used for invocation
//   - Description: a human-readable explanation of what the tool does
//   - Parameters: a JSON Schema describing the expected input shape
//   - Execute: the runtime function that performs the tool's work
//
// # Built-in Tools
//
// This file defines three built-in tools that are always available:
//   - run_shell: Execute shell commands with timeout support
//   - write_file: Write content to the filesystem with path traversal protection
//   - read_file: Read file contents with configurable byte limit and line offset/limit
//
// # Security
//
// All built-in tools include safety guards:
//   - run_shell: context-based timeout prevents runaway processes
//   - write_file: rejects paths containing ".." to prevent directory traversal
//   - read_file: enforces a maximum byte limit (default 1 MB) to prevent memory exhaustion
//
// # Registration
//
// Call RegisterBuiltins(registry) to register all built-in tools into a
// tool.Registry instance. Additional tools created via NewToolFromJSON (in
// tool_json.go) can be registered individually.
package tool

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// BuiltinTool — base implementation of the Tool interface
// ---------------------------------------------------------------------------

// BuiltinTool is a concrete implementation of the Tool interface backed by
// a simple executor function. It stores the tool's metadata (name, description,
// JSON Schema parameters) and delegates execution to the provided executor.
//
// BuiltinTool is used internally by the built-in tool constructors
// (NewRunShellTool, NewWriteFileTool, NewReadFileTool). External callers
// should not construct BuiltinTool directly; use the Registry or
// NewToolFromJSON instead.
type BuiltinTool struct {
	name        string
	description string
	parameters  map[string]any
	executor    func(input map[string]any) (any, error)
}

// Name returns the tool's unique identifier, e.g. "run_shell".
func (t *BuiltinTool) Name() string { return t.name }

// Description returns a human-readable explanation of the tool's purpose,
// suitable for inclusion in LLM system prompts.
func (t *BuiltinTool) Description() string { return t.description }

// Parameters returns the JSON Schema describing the expected input shape.
// The schema follows the JSON Schema (draft-07) convention with "type",
// "properties", and "required" keys.
func (t *BuiltinTool) Parameters() map[string]any { return t.parameters }

// Execute runs the tool with the given input map and returns the result.
// The input map must conform to the schema returned by Parameters().
func (t *BuiltinTool) Execute(input map[string]any) (any, error) {
	return t.executor(input)
}

// ---------------------------------------------------------------------------
// run_shell — shell command execution with timeout
// ---------------------------------------------------------------------------

// NewRunShellTool creates a shell execution tool named "run_shell".
//
// Parameters:
//   - command  (string, required):  The shell command to execute.
//   - workdir  (string, optional):  Working directory for the command.
//   - timeout_ms (integer, optional): Timeout in milliseconds (default 30000).
//
// The tool selects the appropriate shell based on the runtime OS:
//   - Windows: tries "bash" (Git Bash) first, falls back to "cmd /c".
//   - Linux/macOS: uses "sh -c".
//
// Execution is guarded by context.WithTimeout; if the command does not
// complete within the timeout, it is killed and an error is returned.
func NewRunShellTool() *BuiltinTool {
	return &BuiltinTool{
		name:        "run_shell",
		description: "Execute a shell command and return its output. Use this to run system commands, scripts, or development tools.",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
				"workdir": map[string]any{
					"type":        "string",
					"description": "Working directory for the command (optional)",
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Timeout in milliseconds (optional, default 30000)",
				},
			},
			"required": []string{"command"},
		},
		executor: executeShell,
	}
}

// executeShell is the executor function for the run_shell tool.
// It resolves the shell binary, creates a context with timeout, and runs the
// command via exec.CommandContext. The result includes stdout, stderr, and
// exit_code.
func executeShell(input map[string]any) (any, error) {
	cmdStr, ok := input["command"].(string)
	if !ok {
		return nil, fmt.Errorf("command must be a string")
	}

	// Determine the shell binary and flag for the current OS.
	var shell string
	var shellFlag string
	if runtime.GOOS == "windows" {
		// Try bash first (Git Bash), fall back to cmd.
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

	// Parse timeout, default 30 seconds.
	timeoutMs := 30000
	if t, ok := input["timeout_ms"].(float64); ok && t > 0 {
		timeoutMs = int(t)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, shellFlag, cmdStr)

	// Set working directory if provided.
	if workdir, ok := input["workdir"].(string); ok && workdir != "" {
		cmd.Dir = workdir
	}

	output, err := cmd.CombinedOutput()
	result := map[string]any{
		"stdout":    string(output),
		"stderr":    "",
		"exit_code": 0,
	}

	if err != nil {
		// Check for context timeout / cancellation explicitly.
		if ctx.Err() != nil {
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
// write_file — file creation with path traversal protection
// ---------------------------------------------------------------------------

// NewWriteFileTool creates a file write tool named "write_file".
//
// Parameters:
//   - path    (string, required):  The file path to write to.
//   - content (string, required):  The content to write into the file.
//
// The tool automatically creates parent directories if they do not exist.
// For security, paths containing ".." are rejected to prevent directory
// traversal attacks.
func NewWriteFileTool() *BuiltinTool {
	return &BuiltinTool{
		name:        "write_file",
		description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Parent directories are created automatically.",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The file path to write to (relative to working directory)",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "The content to write to the file",
				},
			},
			"required": []string{"path", "content"},
		},
		executor: executeWriteFile,
	}
}

// isPathTraversal returns true if the given path attempts to escape its
// intended directory through ".." segments.
func isPathTraversal(path string) bool {
	cleanPath := filepath.Clean(path)
	// After cleaning, a traversing path will either be exactly ".." or
	// start with ".." followed by the OS path separator.
	if cleanPath == ".." {
		return true
	}
	if strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return true
	}
	return false
}

// executeWriteFile is the executor function for the write_file tool.
// It validates the path, creates parent directories, and writes the content.
func executeWriteFile(input map[string]any) (any, error) {
	path, ok := input["path"].(string)
	if !ok {
		return nil, fmt.Errorf("path must be a string")
	}
	content, ok := input["content"].(string)
	if !ok {
		return nil, fmt.Errorf("content must be a string")
	}

	// Reject paths that attempt directory traversal.
	if isPathTraversal(path) {
		return nil, fmt.Errorf("path traversal detected: %s", path)
	}

	// Ensure parent directory exists.
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
// read_file — file reading with byte limit and line offset/limit
// ---------------------------------------------------------------------------

// DefaultMaxBytes is the default maximum number of bytes read_file will read
// from a single file (1 MB).
const DefaultMaxBytes = 1 << 20 // 1,048,576 bytes

// NewReadFileTool creates a file read tool named "read_file".
//
// Parameters:
//   - path      (string, required):  The file path to read.
//   - offset    (integer, optional): 1-based line number to start reading from.
//   - limit     (integer, optional): Maximum number of lines to read.
//   - max_bytes (integer, optional): Maximum bytes to read (default 1048576 = 1 MB).
//
// The tool reads the file contents, enforces the max_bytes limit, and then
// applies the optional line offset/limit. If the file exceeds max_bytes the
// content is truncated and a "truncated" flag is set in the result.
func NewReadFileTool() *BuiltinTool {
	return &BuiltinTool{
		name:        "read_file",
		description: "Read the contents of a file. Use this to inspect files, source code, or configuration.",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The file path to read (relative to working directory)",
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
		executor: executeReadFile,
	}
}

// executeReadFile is the executor function for the read_file tool.
// It opens the file, reads up to max_bytes bytes (default 1 MB), then applies
// optional line offset and limit filters to the resulting content.
func executeReadFile(input map[string]any) (any, error) {
	path, ok := input["path"].(string)
	if !ok {
		return nil, fmt.Errorf("path must be a string")
	}

	// Reject paths that attempt directory traversal.
	if isPathTraversal(path) {
		return nil, fmt.Errorf("path traversal detected: %s", path)
	}

	// Determine the maximum bytes to read (default 1 MB).
	maxBytes := int64(DefaultMaxBytes)
	if mb, ok := input["max_bytes"].(float64); ok && mb > 0 {
		maxBytes = int64(mb)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	defer f.Close()

	// Read up to maxBytes+1; if we read that extra byte, the file was truncated.
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

	// Apply line offset (1-based, convert to 0-based).
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

	// Apply line limit.
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
// RegisterBuiltins — bulk registration of all built-in tools
// ---------------------------------------------------------------------------

// RegisterBuiltins registers all built-in tools (run_shell, write_file,
// read_file) into the provided Registry. This is the primary entry point
// for bootstrapping the tool system.
//
// Usage:
//
//	reg := tool.NewRegistry()
//	tool.RegisterBuiltins(reg)
//	// ... register additional custom tools if needed
func RegisterBuiltins(registry *Registry) {
	registry.Register(NewRunShellTool())
	registry.Register(NewWriteFileTool())
	registry.Register(NewReadFileTool())
}