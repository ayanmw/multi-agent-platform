package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCohereEmbeddingProviderEmbed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embed" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		resp := map[string]any{
			"id":         "id",
			"texts":      []string{"hello"},
			"embeddings": []any{[]float32{0.4, 0.5, 0.6}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewCohereEmbeddingProvider(server.URL, "key", "embed-english-v3.0", 3)
	vec, err := p.Embed("hello")
	if err != nil {
		t.Fatalf("embed error: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("dim = %d, want 3", len(vec))
	}
}
