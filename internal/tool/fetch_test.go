package tool

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	r := NewRegistry()
	RegisterBuiltins(r)
	res, err := r.Execute("core/fetch_url", map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)
	if out["status_code"].(int) != 200 {
		t.Fatalf("status = %v", out["status_code"])
	}
	if !strings.Contains(out["body"].(string), "hello") {
		t.Fatal("missing body")
	}
}

func TestFetchURLTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
	}))
	defer srv.Close()

	r := NewRegistry()
	RegisterBuiltins(r)
	res, err := r.Execute("core/fetch_url", map[string]any{"url": srv.URL, "max_bytes": 1024})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)
	if !out["truncated"].(bool) {
		t.Fatal("expected truncated")
	}
	if len(out["body"].(string)) != 1024 {
		t.Fatalf("expected body length 1024, got %d", len(out["body"].(string)))
	}
}

func TestFetchURLTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	r := NewRegistry()
	RegisterBuiltins(r)
	_, err := r.Execute("core/fetch_url", map[string]any{"url": srv.URL, "timeout_ms": 50})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
