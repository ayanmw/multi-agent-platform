package marketplace

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const sampleRemoteCatalog = `{
  "version": "1.0.0",
  "markets": [
    {"name": "remote-demo", "display_name": "Remote Demo Market", "description": "test"}
  ],
  "servers": [
    {
      "id": "remote-time",
      "market": "remote-demo",
      "name": "Remote Time",
      "description": "Returns server time from a remote MCP server.",
      "transport": "sse",
      "endpoint": "http://example.com/time/sse"
    }
  ]
}`

func TestURLProviderFetchAndResolve(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "expected GET", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleRemoteCatalog))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p, err := NewURLProvider(server.URL+"/catalog.json", "", WithURLProviderHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	if err != nil {
		t.Fatalf("NewURLProvider: %v", err)
	}

	if got, want := p.Name(), "remote-demo"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
	if got, want := p.DisplayName(), "Remote Demo Market"; got != want {
		t.Fatalf("DisplayName() = %q, want %q", got, want)
	}

	pkgs, err := p.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0].ID != "remote-time" {
		t.Fatalf("ListServers = %v, want [remote-time]", pkgs)
	}

	pkg, err := p.GetServer(context.Background(), "remote-time")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if pkg.Transport != "sse" {
		t.Fatalf("transport = %q, want sse", pkg.Transport)
	}

	cfg, err := p.ResolveConfig(context.Background(), "remote-time")
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if cfg.Name != "Remote Time" || cfg.Transport != "sse" || cfg.Endpoint != "http://example.com/time/sse" || !cfg.Enabled {
		t.Fatalf("ResolveConfig = %+v, unexpected", cfg)
	}
}

func TestURLProviderExplicitName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleRemoteCatalog))
	}))
	defer server.Close()

	p, err := NewURLProvider(server.URL, "opencode", WithURLProviderHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	if err != nil {
		t.Fatalf("NewURLProvider: %v", err)
	}
	if got, want := p.Name(), "opencode"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
}

func TestURLProviderFetchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	_, err := NewURLProvider(server.URL+"/missing.json", "fail", WithURLProviderHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	if err == nil {
		t.Fatalf("expected error for 404")
	}
}

func TestURLProviderNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleRemoteCatalog))
	}))
	defer server.Close()

	p, err := NewURLProvider(server.URL, "remote-demo")
	if err != nil {
		t.Fatalf("NewURLProvider: %v", err)
	}
	if _, err := p.GetServer(context.Background(), "missing"); err == nil {
		t.Fatalf("expected error for missing package")
	}
}

func TestURLProviderFromConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleRemoteCatalog))
	}))
	defer server.Close()

	p, err := NewURLProviderFromConfig("from-cfg", server.URL)
	if err != nil {
		t.Fatalf("NewURLProviderFromConfig: %v", err)
	}
	if p.Name() != "from-cfg" {
		t.Fatalf("Name() = %q, want from-cfg", p.Name())
	}
	if p.URL() != server.URL {
		t.Fatalf("URL() = %q, want %q", p.URL(), server.URL)
	}
}

func TestURLProviderRegistry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleRemoteCatalog))
	}))
	defer server.Close()

	reg := NewRegistry()
	p, err := NewURLProvider(server.URL, "remote-demo")
	if err != nil {
		t.Fatalf("NewURLProvider: %v", err)
	}
	reg.Register(p)

	got, ok := reg.Get("remote-demo")
	if !ok {
		t.Fatalf("Get(remote-demo) not found")
	}
	if rp, ok := got.(*URLProvider); !ok {
		t.Fatalf("provider type = %T, want *URLProvider", rp)
	}
	if fmt.Sprintf("%T", got) != "*marketplace.URLProvider" {
		t.Fatalf("provider type mismatch: %T", got)
	}
}
