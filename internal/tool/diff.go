package tool

import (
	"fmt"
	"os"
	"strings"
)

// NewApplyDiffTool 创建名为 "core/apply_diff" 的文本替换工具。
//
// 参数：
//   - path              (string,  required)：要修改的文件路径。
//   - diffs             (array,   required)：diff 对象列表。
//   - create_if_missing (boolean, optional)：为 true 时若文件不存在则创建。
//
// 每个 diff 对象可包含：
//   - old_string (string,  optional)：要替换的文本。
//   - new_string (string,  optional)：替换后的文本。
//   - line_start (integer, optional)：按行范围替换的 1-based 起始行。
//   - line_end   (integer, optional)：按行范围替换的 1-based 结束行。
//   - count      (integer, optional)：old_string 的最大替换次数。
//     使用 -1 表示"全部替换"。默认为 1。
func NewApplyDiffTool() *BuiltinTool {
	return NewBuiltinTool(
		"apply_diff",
		"core",
		"Apply a set of text replacements (diffs) to a file. Supports old_string replacement or line_start/line_end deletion.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path to modify",
				},
				"diffs": map[string]any{
					"type":        "array",
					"description": "List of diff objects",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"old_string": map[string]any{
								"type":        "string",
								"description": "Text to replace",
							},
							"new_string": map[string]any{
								"type":        "string",
								"description": "Replacement text",
							},
							"line_start": map[string]any{
								"type":        "integer",
								"description": "1-based start line for range replacement",
							},
							"line_end": map[string]any{
								"type":        "integer",
								"description": "1-based end line for range replacement",
							},
							"count": map[string]any{
								"type":        "integer",
								"description": "Maximum replacements for old_string; -1 means replace all. Default 1.",
							},
						},
					},
				},
				"create_if_missing": map[string]any{
					"type":        "boolean",
					"description": "Create the file if it does not exist",
				},
			},
			"required": []string{"path", "diffs"},
		},
		func(_ ExecuteContext, input map[string]any) (any, error) { return applyDiffExecutor(input) },
	).WithTags("filesystem", "filesystem:write")
}

// applyDiffExecutor 对文件依次应用一组编辑。
func applyDiffExecutor(input map[string]any) (any, error) {
	path := getString(input, "path", "")
	if path == "" {
		return nil, fmt.Errorf("path required")
	}
	diffsRaw, ok := input["diffs"].([]any)
	if !ok || len(diffsRaw) == 0 {
		return nil, fmt.Errorf("diffs required")
	}
	createIfMissing := getBool(input, "create_if_missing", false)

	if isPathTraversal(path) {
		return nil, fmt.Errorf("path traversal not allowed: %s", path)
	}
	path = resolvePath(path, input)

	var content string
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) && createIfMissing {
		content = ""
	} else if err != nil {
		return nil, err
	} else {
		content = string(data)
	}

	totalReplacements := 0
	for i, d := range diffsRaw {
		diff, ok := d.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("diff[%d] invalid type", i)
		}
		old, hasOld := diff["old_string"].(string)
		newStr, _ := diff["new_string"].(string)
		start := getInt(diff, "line_start", 0)
		end := getInt(diff, "line_end", 0)

		switch {
		case hasOld:
			if !strings.Contains(content, old) {
				return nil, fmt.Errorf("diff[%d]: old_string not found", i)
			}
			count := -1
			if c, ok := diff["count"].(float64); ok {
				count = int(c)
			}
			if count == 0 {
				count = 1
			}
			content = strings.Replace(content, old, newStr, count)
		default:
			startIdx := 0
			if start > 0 {
				startIdx = lineIndex(content, start)
			}
			endIdx := len(content)
			if end >= start && end > 0 {
				endIdx = lineIndex(content, end+1)
			}
			if startIdx > len(content) {
				startIdx = len(content)
			}
			if endIdx > len(content) {
				endIdx = len(content)
			}
			if endIdx < startIdx {
				endIdx = startIdx
			}
			content = content[:startIdx] + newStr + content[endIdx:]
		}
		totalReplacements++
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil, err
	}
	return map[string]any{
		"success":      true,
		"path":         path,
		"replacements": totalReplacements,
	}, nil
}

// lineIndex 返回给定 1-based 行号首字符对应的字节偏移。
// 若 line 大于总行数，则返回 len(s)。
func lineIndex(s string, line int) int {
	if line <= 1 {
		return 0
	}
	count := 0
	for i, b := range s {
		if b == '\n' {
			count++
			if count == line-1 {
				return i + 1
			}
		}
	}
	return len(s)
}
