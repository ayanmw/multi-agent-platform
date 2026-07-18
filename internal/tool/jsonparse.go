package tool

import (
	"encoding/json"
	"fmt"
	"strings"
)

// NewParseJSONTool creates a JSON query tool named "core/parse_json".
//
// Parameters:
//   - input     (string,  required): JSON string to parse.
//   - query     (string,  required): Dot-separated path (e.g. "a.b").
//   - max_chars (integer, optional): Maximum characters for the preview.
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

// parseJSONExecutor unmarshals JSON and traverses a dotted query path.
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
			// Key not present in a non-object value: treat as no match rather
			// than an error so callers can query nested paths safely.
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
