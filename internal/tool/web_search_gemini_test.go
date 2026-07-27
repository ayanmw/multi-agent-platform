package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWebSearchGeminiInteractions(t *testing.T) {
	var gotBody map[string]any
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"role": "model", "parts": [{"text": "Spain won Euro 2024, defeating England 2-1 in the final. This victory marks Spain's record fourth European Championship title."}]},
				"groundingMetadata": {
					"webSearchQueries": ["Euro 2024 winner"],
					"groundingChunks": [
						{"web": {"uri": "https://www.aljazeera.com/sports/euro-2024-final", "title": "aljazeera.com"}},
						{"web": {"uri": "https://www.uefa.com/euro2024/news/spain-wins-euro-2024", "title": "uefa.com"}}
					],
					"groundingSupports": [
						{"segment": {"text": "Spain won Euro 2024, defeating England 2-1 in the final."}, "groundingChunkIndices": [0]},
						{"segment": {"text": "This victory marks Spain's record fourth European Championship title."}, "groundingChunkIndices": [1]}
					]
				}
			}]
		}`))
	}))
	defer srv.Close()

	client := srv.Client()
	client.Transport = &rewriteHostTransport{base: client.Transport, host: srv.URL}
	cfg := WebSearchConfig{
		Provider:       "gemini",
		GeminiAPIKey:   "test-key",
		GeminiEndpoint: srv.URL,
		GeminiModel:    "gemini-2.0-flash",
		HTTPClient:     client,
		Timeout:        5 * time.Second,
	}

	r := NewRegistry()
	r.Register(NewWebSearchTool(cfg))
	res, err := r.Execute("core/web_search", map[string]any{"query": "Who won Euro 2024?", "num_results": 8})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.(map[string]any)
	if out["provider"] != "gemini" {
		t.Fatalf("expected gemini provider, got %v", out["provider"])
	}
	if !strings.HasPrefix(gotPath, "/models/") || !strings.Contains(gotPath, ":generateContent") {
		t.Fatalf("expected generateContent path, got %s", gotPath)
	}
	if gotBody["model"] != nil {
		t.Fatalf("request body must not contain top-level model, got %v", gotBody["model"])
	}
	if !strings.Contains(gotPath, "models/gemini-2.0-flash:generateContent") {
		t.Fatalf("expected model in path, got %s", gotPath)
	}
	tools, ok := gotBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one tool, got %v", gotBody["tools"])
	}
	toolMap, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("expected tool object, got %v", tools[0])
	}
	if _, ok := toolMap["google_search"]; !ok {
		t.Fatalf("expected google_search tool, got %v", tools[0])
	}

	text := out["text"].(string)
	if !strings.Contains(text, "Spain won Euro 2024") {
		t.Fatalf("expected answer text, got:\n%s", text)
	}
	if !strings.Contains(text, "aljazeera.com") || !strings.Contains(text, "uefa.com") {
		t.Fatalf("expected citations, got:\n%s", text)
	}
}

func TestWebSearchGeminiNoGroundingFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"role": "model", "parts": [{"text": "  AI is a broad field of computer science.  "}]}
			}]
		}`))
	}))
	defer srv.Close()

	client := srv.Client()
	client.Transport = &rewriteHostTransport{base: client.Transport, host: srv.URL}
	cfg := WebSearchConfig{
		Provider:       "gemini",
		GeminiAPIKey:   "test-key",
		GeminiEndpoint: srv.URL,
		HTTPClient:     client,
		Timeout:        5 * time.Second,
	}

	text, err := callGemini(context.Background(), cfg, "What is AI?", 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "AI is a broad field") {
		t.Fatalf("expected fallback text, got:\n%s", text)
	}
}

func TestSelectWebSearchProviderGeminiPriority(t *testing.T) {
	if p := selectWebSearchProvider(WebSearchConfig{EnableGemini: true, EnableBrave: true}); p != "gemini" {
		t.Fatalf("expected gemini priority, got %s", p)
	}
	if p := selectWebSearchProvider(WebSearchConfig{GeminiAPIKey: "key", EnableBrave: true}); p != "gemini" {
		t.Fatalf("expected gemini when key present, got %s", p)
	}
	if p := selectWebSearchProvider(WebSearchConfig{Provider: "gemini", EnableBrave: true}); p != "gemini" {
		t.Fatalf("expected explicit provider override, got %s", p)
	}
	if p := selectWebSearchProvider(WebSearchConfig{EnableBrave: true}); p != "brave" {
		t.Fatalf("expected brave when no gemini, got %s", p)
	}
}

// TestRealGeminiSearch 使用 .env 中的 GEMINI_API_KEY 对真实 Gemini interactions
// 端点做一次搜索冒烟测试。默认 Skip，手动运行时加 -run TestRealGeminiSearch。
func TestRealGeminiSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real network test")
	}
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	cfg := WebSearchConfig{
		GeminiAPIKey:   key,
		GeminiEndpoint: "https://generativelanguage.googleapis.com/v1beta",
		GeminiModel:    "gemini-2.0-flash",
		Timeout:        25 * time.Second,
		UserAgent:      "multi-agent-platform/0.1.0",
	}
	ctx := t.Context()
	text, err := callGemini(ctx, cfg, "Who won Euro 2024?", 3)
	if err != nil {
		t.Fatalf("gemini search failed: %v", err)
	}
	if text == "" || !strings.Contains(text, "http") || !strings.Contains(text, "Spain") {
		t.Fatalf("expected grounded search results, got:\n%s", text)
	}
	t.Logf("Gemini search result:\n%s", text)
}
