package tool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestWebSearchFallbackWhenNoProvider(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)

	// Without any provider/API key config, web_search should fall back to
	// DuckDuckGo. We simulate DuckDuckGo HTML by intercepting HTTP with a custom
	// client injected through the tool's HTTPClient field.
	var requestedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`
<!DOCTYPE html>
<html><body>
<a class="result__a" href="https://go.dev/ref/mod">Go Modules</a>
<a class="result__snippet">Modules are how Go manages dependencies.</a>
<a class="result__a" href="https://pkg.go.dev/">Package Docs</a>
<a class="result__snippet">Find, add, and publish Go packages.</a>
</body></html>`))
	}))
	defer srv.Close()

	// Replace the globally registered placeholder with a configured version that
	// points to the test server. DuckDuckGo URLs are hard-coded, but the injected
	// HTTPClient redirects all HTTP(S) calls to srv via transport rewrite below.
	client := srv.Client()
	client.Transport = &rewriteHostTransport{base: client.Transport, host: srv.URL}
	cfg := WebSearchConfig{HTTPClient: client, Timeout: 5 * time.Second}
	r.Unregister("core/web_search")
	r.Register(NewWebSearchTool(cfg))

	res, err := r.Execute("core/web_search", map[string]any{"query": "go modules", "num_results": 8})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.(map[string]any)
	if out["provider"] != "duckduckgo" {
		t.Fatalf("expected duckduckgo provider, got %v", out["provider"])
	}
	text := out["text"].(string)
	if !strings.Contains(text, "Go Modules") || !strings.Contains(text, "Modules are how Go manages dependencies") {
		t.Fatalf("expected parsed results in text, got:\n%s", text)
	}
	_ = requestedPath
}

// rewriteHostTransport replaces every request URL host with the test server host
// so that hard-coded DuckDuckGo endpoints hit our httptest server.
type rewriteHostTransport struct {
	base http.RoundTripper
	host string
}

func (t *rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	u, err := url.Parse(t.host)
	if err != nil {
		return nil, err
	}
	clone.URL.Host = u.Host
	return t.base.RoundTrip(clone)
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
	if selectWebSearchProvider(WebSearchConfig{Provider: "parallel"}) != "parallel" {
		t.Fatal("provider override failed")
	}
	if selectWebSearchProvider(WebSearchConfig{EnableBrave: true}) != "brave" {
		t.Fatal("enable brave failed")
	}
	if selectWebSearchProvider(WebSearchConfig{BingAPIKey: "k"}) != "bing" {
		t.Fatal("bing api key failed")
	}
	if selectWebSearchProvider(WebSearchConfig{GoogleAPIKey: "k", GoogleCX: "cx"}) != "google" {
		t.Fatal("google config failed")
	}
	if selectWebSearchProvider(WebSearchConfig{EnableTavily: true}) != "tavily" {
		t.Fatal("enable tavily failed")
	}
	if selectWebSearchProvider(WebSearchConfig{EnableParallel: true}) != "parallel" {
		t.Fatal("enable parallel failed")
	}
	if selectWebSearchProvider(WebSearchConfig{EnableExa: true}) != "exa" {
		t.Fatal("enable exa failed")
	}
	if selectWebSearchProvider(WebSearchConfig{}) != "" {
		t.Fatal("expected empty provider when nothing configured")
	}
}

func TestWebSearchBing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if got := r.Header.Get("Ocp-Apim-Subscription-Key"); got != "bing-key" {
			t.Fatalf("expected bing-key, got %s", got)
		}
		if q := r.URL.Query().Get("q"); q != "go modules" {
			t.Fatalf("expected q=go modules, got %s", q)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"webPages":{"value":[{"name":"Go Modules","url":"https://go.dev/ref/mod","snippet":"Using Go Modules."}]}}`))
	}))
	defer srv.Close()

	client := srv.Client()
	client.Transport = &rewriteHostTransport{base: client.Transport, host: srv.URL}
	cfg := WebSearchConfig{
		Provider:   "bing",
		BingAPIKey: "bing-key",
		BingEndpoint: srv.URL + "/v7.0/search",
		HTTPClient: client,
		Timeout:    5 * time.Second,
	}

	r := NewRegistry()
	r.Register(NewWebSearchTool(cfg))
	res, err := r.Execute("core/web_search", map[string]any{"query": "go modules"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.(map[string]any)
	if out["provider"] != "bing" {
		t.Fatalf("expected bing, got %v", out["provider"])
	}
	text := out["text"].(string)
	if !strings.Contains(text, "Go Modules") {
		t.Fatalf("expected Go Modules in text, got:\n%s", text)
	}
}

func TestWebSearchGoogle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("key"); got != "google-key" {
			t.Fatalf("expected google-key, got %s", got)
		}
		if got := r.URL.Query().Get("cx"); got != "cx123" {
			t.Fatalf("expected cx123, got %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"title":"Go Modules","link":"https://go.dev/ref/mod","snippet":"Using Go Modules."}]}`))
	}))
	defer srv.Close()

	client := srv.Client()
	client.Transport = &rewriteHostTransport{base: client.Transport, host: srv.URL}
	cfg := WebSearchConfig{
		Provider:     "google",
		GoogleAPIKey: "google-key",
		GoogleCX:     "cx123",
		GoogleEndpoint: srv.URL + "/customsearch/v1",
		HTTPClient:   client,
		Timeout:      5 * time.Second,
	}

	r := NewRegistry()
	r.Register(NewWebSearchTool(cfg))
	res, err := r.Execute("core/web_search", map[string]any{"query": "go modules"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.(map[string]any)
	if out["provider"] != "google" {
		t.Fatalf("expected google, got %v", out["provider"])
	}
}

func TestWebSearchTavily(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tavily-key" {
			t.Fatalf("expected Bearer tavily-key, got %s", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":"Go modules manage dependencies.","results":[{"title":"Go Modules","url":"https://go.dev/ref/mod","content":"Using Go Modules."}]}`))
	}))
	defer srv.Close()

	client := srv.Client()
	client.Transport = &rewriteHostTransport{base: client.Transport, host: srv.URL}
	cfg := WebSearchConfig{
		Provider:        "tavily",
		TavilyAPIKey:    "tavily-key",
		TavilyEndpoint:  srv.URL + "/search",
		TavilyIncludeAnswer: true,
		HTTPClient:      client,
		Timeout:         5 * time.Second,
	}

	r := NewRegistry()
	r.Register(NewWebSearchTool(cfg))
	res, err := r.Execute("core/web_search", map[string]any{"query": "go modules"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.(map[string]any)
	if out["provider"] != "tavily" {
		t.Fatalf("expected tavily, got %v", out["provider"])
	}
	text := out["text"].(string)
	if !strings.Contains(text, "Answer:") {
		t.Fatalf("expected Tavily answer prefix, got:\n%s", text)
	}
	_ = gotBody
}

func TestWebSearchBrave(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Subscription-Token"); got != "brave-key" {
			t.Fatalf("expected brave-key, got %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"web":{"results":[{"title":"Go Modules","url":"https://go.dev/ref/mod","description":"Using Go Modules."}]}}`))
	}))
	defer srv.Close()

	client := srv.Client()
	client.Transport = &rewriteHostTransport{base: client.Transport, host: srv.URL}
	cfg := WebSearchConfig{
		Provider:     "brave",
		BraveAPIKey:  "brave-key",
		BraveEndpoint: srv.URL + "/res/v1/web/search",
		HTTPClient:   client,
		Timeout:      5 * time.Second,
	}

	r := NewRegistry()
	r.Register(NewWebSearchTool(cfg))
	res, err := r.Execute("core/web_search", map[string]any{"query": "go modules"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.(map[string]any)
	if out["provider"] != "brave" {
		t.Fatalf("expected brave, got %v", out["provider"])
	}
}

func TestWebSearchPlaceholderProvider(t *testing.T) {
	r := NewRegistry()
	r.Register(NewWebSearchTool(WebSearchConfig{Provider: "kimi_search"}))
	_, err := r.Execute("core/web_search", map[string]any{"query": "go modules"})
	if err == nil {
		t.Fatal("expected error for placeholder provider")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Fatalf("expected not yet implemented error, got %v", err)
	}
}
