package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIEmbeddingProviderEmbed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("bad request body: %v", err)
		}
		resp := map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"object":    "embedding",
					"index":     0,
					"embedding": []float32{0.1, 0.2, 0.3},
				},
			},
			"usage": map[string]int{"prompt_tokens": 4, "total_tokens": 4},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIEmbeddingProvider(server.URL, "key", "text-embedding-3-small", 3)
	vec, err := p.Embed("hello")
	if err != nil {
		t.Fatalf("embed error: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("dim = %d, want 3", len(vec))
	}
	if p.Dimensions() != 3 {
		t.Fatalf("dimensions = %d, want 3", p.Dimensions())
	}
}
