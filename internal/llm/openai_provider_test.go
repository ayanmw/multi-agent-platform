package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOpenAIProvider_ListModels 验证 OpenAIProvider 能正确解析 /models 响应。
func TestOpenAIProvider_ListModels(t *testing.T) {
	want := []string{"deepseek-v4-flash", "deepseek-v4-pro"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("expected /models, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		body := map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "deepseek-v4-flash", "object": "model"},
				{"id": "deepseek-v4-pro", "object": "model"},
			},
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer server.Close()

	p := NewOpenAIProvider("deepseek", server.URL, "sk-test", "")
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != len(want) {
		t.Fatalf("expected %d models, got %d", len(want), len(models))
	}
	for i, m := range models {
		if m.ID != want[i] {
			t.Errorf("model[%d].ID = %q, want %q", i, m.ID, want[i])
		}
		if m.Provider != "deepseek" {
			t.Errorf("model[%d].Provider = %q, want deepseek", i, m.Provider)
		}
	}
}

// TestOpenAIProvider_ListModels_EmptyData 验证空模型列表返回空 slice 而非 nil 错误。
func TestOpenAIProvider_ListModels_EmptyData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{}})
	}))
	defer server.Close()

	p := NewOpenAIProvider("openai", server.URL, "sk-test", "")
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("expected 0 models, got %d", len(models))
	}
}

// TestOpenAIProvider_ListModels_APIError 验证非 200 响应返回 error。
func TestOpenAIProvider_ListModels_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	p := NewOpenAIProvider("openai", server.URL, "sk-test", "")
	_, err := p.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}
