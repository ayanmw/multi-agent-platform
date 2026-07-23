package tool

import (
	"fmt"
	"os"
)

// NewDeleteFileTool 创建名为 "core/delete_file" 的文件删除工具。
//
// 参数：
//   - path      (string,  required)：要删除的文件或目录路径。
//   - recursive (boolean, optional)：为 true 时删除目录及其内容。
//     对包含文件的目录进行递归删除仍需 filesystem:destructive 权限；
//     此 flag 仅控制工具行为。
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
		func(ctx ExecuteContext, input map[string]any) (any, error) { return deleteFileExecutor(ctx, input) },
	).WithTags("filesystem", "filesystem:destructive")
}

// deleteFileExecutor 在路径 traversal 检查之后删除文件或目录。
// 相对路径按 ctx.Workdir → input["workdir"] 优先级解析。
func deleteFileExecutor(ctx ExecuteContext, input map[string]any) (any, error) {
	path := getString(input, "path", "")
	if path == "" {
		return nil, fmt.Errorf("path required")
	}
	recursive := getBool(input, "recursive", false)

	if isPathTraversal(path) {
		return nil, fmt.Errorf("path traversal not allowed: %s", path)
	}
	path = resolvePathWithCtx(path, ctx, input)

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
