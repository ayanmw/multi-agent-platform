package tool

import (
	"fmt"
	"os"
)

// NewDeleteFileTool creates a file deletion tool named "core/delete_file".
//
// Parameters:
//   - path      (string,  required): File or directory path to delete.
//   - recursive (boolean, optional): If true, delete directories and contents.
func NewDeleteFileTool() *BuiltinTool {
	return NewBuiltinTool(
		"delete_file",
		"core",
		"Delete a file or empty directory. Use recursive=true to delete directories and their contents. Paths attempting directory traversal are rejected.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File or directory path to delete",
				},
				"recursive": map[string]any{
					"type":        "boolean",
					"description": "If true, delete directories and their contents",
				},
			},
			"required": []string{"path"},
		},
		deleteFileExecutor,
	).WithTags("filesystem", "destructive")
}

// deleteFileExecutor removes a file or directory after path traversal checks.
func deleteFileExecutor(input map[string]any) (any, error) {
	path := getString(input, "path", "")
	if path == "" {
		return nil, fmt.Errorf("path required")
	}
	recursive := getBool(input, "recursive", false)

	if isPathTraversal(path) {
		return nil, fmt.Errorf("path traversal not allowed: %s", path)
	}
	path = resolvePath(path, input)

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	switch {
	case info.Mode().IsRegular():
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	case info.IsDir():
		if recursive {
			if err := os.RemoveAll(path); err != nil {
				return nil, err
			}
		} else {
			if err := os.Remove(path); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("unsupported file type: %s", path)
	}

	return map[string]any{
		"success": true,
		"path":    path,
	}, nil
}
