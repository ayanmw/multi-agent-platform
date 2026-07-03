package tool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// BuiltinTool provides a base implementation of the Tool interface
type BuiltinTool struct {
	name        string
	description string
	parameters  map[string]any
	executor    func(input map[string]any) (any, error)
}

func (t *BuiltinTool) Name() string           { return t.name }
func (t *BuiltinTool) Description() string     { return t.description }
func (t *BuiltinTool) Parameters() map[string]any { return t.parameters }
func (t *BuiltinTool) Execute(input map[string]any) (any, error) {
	return t.executor(input)
}

// NewRunShellTool creates a shell execution tool
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

func executeShell(input map[string]any) (any, error) {
	cmdStr, ok := input["command"].(string)
	if !ok {
		return nil, fmt.Errorf("command must be a string")
	}

	var shell string
	var shellFlag string
	if runtime.GOOS == "windows" {
		// Try bash first (Git Bash), fallback to cmd
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

	cmd := exec.Command(shell, shellFlag, cmdStr)

	// Set working directory if provided
	if workdir, ok := input["workdir"].(string); ok && workdir != "" {
		cmd.Dir = workdir
	}

	output, err := cmd.CombinedOutput()
	result := map[string]any{
		"stdout": string(output),
		"stderr": "",
		"exit_code": 0,
	}
	if err != nil {
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

// NewWriteFileTool creates a file write tool
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

func executeWriteFile(input map[string]any) (any, error) {
	path, ok := input["path"].(string)
	if !ok {
		return nil, fmt.Errorf("path must be a string")
	}
	content, ok := input["content"].(string)
	if !ok {
		return nil, fmt.Errorf("content must be a string")
	}

	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	return map[string]any{
		"success":  true,
		"path":     path,
		"bytes":    len(content),
		"message":  fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path),
	}, nil
}

// NewReadFileTool creates a file read tool
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
			},
			"required": []string{"path"},
		},
		executor: executeReadFile,
	}
}

func executeReadFile(input map[string]any) (any, error) {
	path, ok := input["path"].(string)
	if !ok {
		return nil, fmt.Errorf("path must be a string")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	offset := 0
	if o, ok := input["offset"].(float64); ok {
		offset = int(o) - 1 // convert to 0-based
		if offset < 0 {
			offset = 0
		}
	}

	if offset >= len(lines) {
		return map[string]any{
			"content":   "",
			"path":      path,
			"total_lines": len(lines),
			"lines_read": 0,
		}, nil
	}

	limit := len(lines) - offset
	if l, ok := input["limit"].(float64); ok {
		limit = int(l)
		if limit > len(lines)-offset {
			limit = len(lines) - offset
		}
	}

	selectedLines := lines[offset : offset+limit]
	result := strings.Join(selectedLines, "\n")

	return map[string]any{
		"content":    result,
		"path":       path,
		"total_lines": len(lines),
		"lines_read": len(selectedLines),
		"offset":     offset + 1,
	}, nil
}

// RegisterBuiltins registers all built-in tools to the registry
func RegisterBuiltins(registry *Registry) {
	registry.Register(NewRunShellTool())
	registry.Register(NewWriteFileTool())
	registry.Register(NewReadFileTool())
}