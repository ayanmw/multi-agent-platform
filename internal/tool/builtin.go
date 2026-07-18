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
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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
	namespace   string
	description string
	parameters  map[string]any
	tags        []string
	executor    func(input map[string]any) (any, error)
}

// Namespace returns the tool's namespace. Empty string means the tool lives in
// the global namespace; non-empty namespaces produce a "namespace/name" FullName.
func (t *BuiltinTool) Namespace() string { return t.namespace }

// Name returns the tool's unique identifier, e.g. "run_shell".
func (t *BuiltinTool) Name() string { return t.name }

// FullName returns the tool's fully-qualified identifier. When namespace is
// non-empty, it returns "namespace/name"; otherwise it returns Name.
func (t *BuiltinTool) FullName() string {
	if t.namespace == "" {
		return t.name
	}
	return t.namespace + "/" + t.name
}

// Description returns a human-readable explanation of the tool's purpose,
// suitable for inclusion in LLM system prompts.
func (t *BuiltinTool) Description() string { return t.description }

// Parameters returns the JSON Schema describing the expected input shape.
// The schema follows the JSON Schema (draft-07) convention with "type",
// "properties", and "required" keys.
func (t *BuiltinTool) Parameters() map[string]any { return t.parameters }

// Tags returns the tool's tags. Tags are used for discovery and filtering.
func (t *BuiltinTool) Tags() []string { return t.tags }

// Execute runs the tool with the given input map and returns the result.
// The input map must conform to the schema returned by Parameters().
func (t *BuiltinTool) Execute(input map[string]any) (any, error) {
	return t.executor(input)
}

// NewBuiltinTool creates a new BuiltinTool with the given metadata.
// When namespace is non-empty, the tool's FullName becomes "namespace/name".
func NewBuiltinTool(name, namespace, description string, parameters map[string]any, executor func(input map[string]any) (any, error)) *BuiltinTool {
	return &BuiltinTool{
		name:        name,
		namespace:   namespace,
		description: description,
		parameters:  parameters,
		executor:    executor,
		tags:        []string{},
	}
}

// WithTags attaches tags to a BuiltinTool and returns it for chaining.
func (t *BuiltinTool) WithTags(tags ...string) *BuiltinTool {
	t.tags = append(t.tags, tags...)
	return t
}

// ---------------------------------------------------------------------------
// Generic helpers for reading typed values from tool input maps
// ---------------------------------------------------------------------------

// getString extracts a string value from m[key], returning def when missing
// or when the value is not a string.
func getString(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return def
}

// getBool extracts a bool value from m[key], returning def when missing
// or when the value is not a bool.
func getBool(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

// getInt extracts an integer value from m[key], returning def when missing
// or when the value is not a numeric type. JSON numbers unmarshal as float64,
// while callers may also pass int or int64 directly.
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

// getMap extracts a nested map value from m[key], returning nil when missing
// or when the value is not a map[string]any.
func getMap(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

// resolvePath normalizes a tool input path. Absolute paths are cleaned and
// returned as-is. Relative paths are resolved against the input["workdir"]
// value when present, falling back to the process working directory.
//
// Callers should check isPathTraversal before calling resolvePath to prevent
// directory traversal through ".." segments.
func resolvePath(path string, input map[string]any) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if workdir, ok := input["workdir"].(string); ok && workdir != "" {
		return filepath.Clean(filepath.Join(workdir, path))
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Clean(filepath.Join(wd, path))
	}
	return filepath.Clean(path)
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
		name: "write_file",
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

	// Reject paths that attempt directory traversal BEFORE resolving against
	// workdir. filepath.Join + filepath.Clean would normalize ".." segments and
	// silently re-root the path, defeating the traversal check.
	if isPathTraversal(path) {
		return nil, fmt.Errorf("path traversal detected: %s", path)
	}

	// Resolve relative path against workdir if provided. When workdir is empty
	// (workspace_dir not bound to the session), fall back to the current
	// working directory so relative paths still resolve predictably.
	if !filepath.IsAbs(path) {
		if workdir, hasWorkdir := input["workdir"].(string); hasWorkdir && workdir != "" {
			path = filepath.Join(workdir, path)
		} else {
			wd, _ := os.Getwd()
			if wd != "" {
				path = filepath.Join(wd, path)
			}
		}
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
		name: "read_file",
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

	// Resolve relative path against workdir if provided.
	if workdir, hasWorkdir := input["workdir"].(string); hasWorkdir && !filepath.IsAbs(path) {
		path = filepath.Join(workdir, path)
	}

	// Reject paths that attempt directory traversal after resolving against workdir.
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
	registry.Register(NewListDirTool())
}

// NewListDirTool creates a directory listing tool named "core/list_dir".
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
		listDirExecutor,
	).WithTags("filesystem", "readonly")
}

// listDirExecutor implements the list_dir tool logic.
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

// walkDir enumerates entries under root according to the supplied filters.
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
			// For non-recursive mode we still report the directory itself,
			// but we must not descend into it.
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