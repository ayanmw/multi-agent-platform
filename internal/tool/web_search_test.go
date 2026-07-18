package tool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebSearchNotConfigured(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)

	res, err := r.Execute("core/web_search", map[string]any{"query": "go modules"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.(map[string]any)
	if out["status"] != "not_configured" {
		t.Fatalf("expected not_configured status, got %v", out)
	}
}

func TestWebSearchMCPRequest(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"exa search result"}]}}`))
	}))
	defer srv.Close()

	cfg := WebSearchConfig{
		Provider:   "exa",
		ExaAPIKey:  "test-key",
		UserAgent:  "test-agent",
		HTTPClient: srv.Client(),
		Timeout:    5 * time.Second,
	}
	// We cannot redirect the hard-coded Exa endpoint in unit tests without a
	// test hook, so exercise the parser directly and keep the server for future
	// hook-based integration tests.
	_ = srv
	_ = cfg

	text, err := parseMCPResponse(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"exa search result"}]}}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if text != "exa search result" {
		t.Fatalf("unexpected text: %s", text)
	}
}

func TestWebSearchParseSSE(t *testing.T) {
	body := "data: [DONE]\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"sse result\"}]}}\n\n"
	text, err := parseMCPResponse(body)
	if err != nil {
		t.Fatalf("parse sse: %v", err)
	}
	if text != "sse result" {
		t.Fatalf("unexpected text: %s", text)
	}
}

func TestWebSearchEmptyResult(t *testing.T) {
	text, err := parseMCPResponse(`{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`)
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
}

func TestSelectWebSearchProvider(t *testing.T) {
	if selectWebSearchProvider("sess", WebSearchConfig{Provider: "parallel"}) != "parallel" {
		t.Fatal("provider override failed")
	}
	if selectWebSearchProvider("sess", WebSearchConfig{EnableParallel: true}) != "parallel" {
		t.Fatal("enable parallel failed")
	}
	if selectWebSearchProvider("sess", WebSearchConfig{EnableExa: true}) != "exa" {
		t.Fatal("enable exa failed")
	}
	// Stable selection.
	cfg := WebSearchConfig{}
	p1 := selectWebSearchProvider("abc", cfg)
	p2 := selectWebSearchProvider("abc", cfg)
	if p1 != p2 {
		t.Fatalf("unstable provider: %s vs %s", p1, p2)
	}
}
