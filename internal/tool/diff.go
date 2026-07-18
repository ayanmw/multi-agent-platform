package tool

import (
	"fmt"
	"os"
	"strings"
)

// NewApplyDiffTool creates a text replacement tool named "core/apply_diff".
//
// Parameters:
//   - path              (string,  required): File path to modify.
//   - diffs             (array,   required): List of diff objects.
//   - create_if_missing (boolean, optional): If true, create the file when it
//     does not exist.
//
// Each diff object may contain:
//   - old_string (string,  optional): Text to replace.
//   - new_string (string,  optional): Replacement text.
//   - line_start (integer, optional): 1-based start line for range replacement.
//   - line_end   (integer, optional): 1-based end line for range replacement.
//   - count      (integer, optional): Maximum number of replacements for
//     old_string. Use -1 for "replace all". Default is 1.
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
		applyDiffExecutor,
	).WithTags("filesystem", "filesystem:write")
}

// applyDiffExecutor applies a sequence of edits to a file.
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

// lineIndex returns the byte offset of the first character of the given
// 1-based line number. If line is greater than the number of lines, it returns
// len(s).
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
