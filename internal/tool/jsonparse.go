package tool

import (
	"encoding/json"
	"fmt"
	"strings"
)

// NewParseJSONTool 创建名为 "core/parse_json" 的 JSON 查询工具。
//
// 参数：
//   - input     (string,  required)：要解析的 JSON 字符串。
//   - query     (string,  required)：以点分隔的路径（例如 "a.b"）。
//   - max_chars (integer, optional)：预览的最大字符数。
func NewParseJSONTool() *BuiltinTool {
	return NewBuiltinTool(
		"parse_json",
		"core",
		"Parse a JSON string and query a dotted path from it. Returns matches array and count.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{
					"type":        "string",
					"description": "JSON string to parse",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "Dot-separated path (e.g. \"a.b\")",
				},
				"max_chars": map[string]any{
					"type":        "integer",
					"description": "Maximum characters for the preview",
				},
			},
			"required": []string{"input", "query"},
		},
		parseJSONExecutor,
	).WithTags("data", "readonly")
}

// parseJSONExecutor 反序列化 JSON 并遍历以点分隔的查询路径。
func parseJSONExecutor(input map[string]any) (any, error) {
	raw := getString(input, "input", "")
	query := getString(input, "query", "")
	if raw == "" || query == "" {
		return nil, fmt.Errorf("input and query required")
	}
	maxChars := getInt(input, "max_chars", 10000)

	var data any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	parts := strings.Split(strings.TrimSpace(query), ".")
	cur := data
	for _, p := range parts {
		if p == "" {
			continue
		}
		switch v := cur.(type) {
		case map[string]any:
			if next, ok := v[p]; ok {
				cur = next
			} else {
				return map[string]any{"matches": []any{}, "count": 0, "preview": nil}, nil
			}
		default:
			// 键不存在于非对象值中：视为无匹配而非错误，
			// 以便调用方可以安全地查询嵌套路径。
			return map[string]any{"matches": []any{}, "count": 0, "preview": nil}, nil
		}
	}

	var matches []any
	switch v := cur.(type) {
	case []any:
		matches = v
	default:
		matches = []any{cur}
	}

	preview := cur
	if maxChars > 0 {
		s, _ := json.Marshal(cur)
		if len(s) > maxChars {
			preview = string(s[:maxChars]) + "..."
		}
	}

	return map[string]any{
		"matches": matches,
		"count":   len(matches),
		"preview": preview,
	}, nil
}
