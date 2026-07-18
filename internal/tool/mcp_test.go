package tool

import "testing"

func TestWebSearchPlaceholder(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	res, err := r.Execute("mcp/web_search", map[string]any{"query": "go"})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)
	if out["status"].(string) != "not_implemented" {
		t.Fatalf("unexpected status: %v", out)
	}
}
