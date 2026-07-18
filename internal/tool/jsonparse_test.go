package tool

import (
	"strings"
	"testing"
)

func TestParseJSON(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	res, err := r.Execute("core/parse_json", map[string]any{
		"input": `{"a":{"b":[1,2,3]}}`,
		"query": "a.b",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)
	if out["count"].(int) != 3 {
		t.Fatalf("expected count 3, got %v", out["count"])
	}
	matches := out["matches"].([]any)
	if len(matches) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(matches))
	}
}

func TestParseJSONMissingKey(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	res, err := r.Execute("core/parse_json", map[string]any{
		"input": `{"a":1}`,
		"query": "a.b",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)
	if out["count"].(int) != 0 {
		t.Fatalf("expected count 0, got %v", out["count"])
	}
}

func TestParseJSONMaxChars(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	res, err := r.Execute("core/parse_json", map[string]any{
		"input":     `{"a":"this is a long value"}`,
		"query":     "a",
		"max_chars": 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)
	preview := out["preview"].(string)
	if !strings.HasSuffix(preview, "...") {
		t.Fatalf("expected preview to end with ..., got %s", preview)
	}
}

func TestParseJSONInvalidJSON(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	_, err := r.Execute("core/parse_json", map[string]any{
		"input": `not json`,
		"query": "a",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
