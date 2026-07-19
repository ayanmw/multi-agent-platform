package marketplace

import (
	"context"
	"testing"
)

const sampleCatalog = `{
  "version": "1.0.0",
  "markets": [
    {"name": "default", "display_name": "Test Market", "description": "test"}
  ],
  "servers": [
    {
      "id": "demo",
      "market": "default",
      "name": "Demo",
      "description": "A demo server",
      "transport": "stdio",
      "command": "node",
      "args": ["demo.js"]
    }
  ]
}`

func TestStaticProvider(t *testing.T) {
	p, err := NewStaticProvider([]byte(sampleCatalog))
	if err != nil {
		t.Fatalf("NewStaticProvider: %v", err)
	}

	if got, want := p.Name(), "default"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
	if got, want := p.DisplayName(), "Test Market"; got != want {
		t.Fatalf("DisplayName() = %q, want %q", got, want)
	}

	pkgs, err := p.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0].ID != "demo" {
		t.Fatalf("ListServers = %v, want [demo]", pkgs)
	}

	pkg, err := p.GetServer(context.Background(), "demo")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if pkg.Name != "Demo" {
		t.Fatalf("GetServer Name = %q, want Demo", pkg.Name)
	}

	cfg, err := p.ResolveConfig(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	expected := InstallConfig{
		Name:      "Demo",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"demo.js"},
		Enabled:   true,
	}
	if cfg.Name != expected.Name || cfg.Transport != expected.Transport || cfg.Command != expected.Command || len(cfg.Args) != 1 {
		t.Fatalf("ResolveConfig = %+v, want %+v", cfg, expected)
	}

	if _, err := p.GetServer(context.Background(), "missing"); err == nil {
		t.Fatalf("expected error for missing package")
	}
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	if len(reg.List()) != 0 {
		t.Fatalf("new registry should be empty")
	}

	p, _ := NewStaticProvider([]byte(sampleCatalog))
	reg.Register(p)

	if got := len(reg.List()); got != 1 {
		t.Fatalf("List() len = %d, want 1", got)
	}
	if _, ok := reg.Get("default"); !ok {
		t.Fatalf("Get(default) should return provider")
	}
	if names := reg.Names(); len(names) != 1 || names[0] != "default" {
		t.Fatalf("Names() = %v, want [default]", names)
	}
}

func TestParsePreinstallEntry(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    MCPPreinstallEntry
		wantErr bool
	}{
		{"full", "default/time-server", MCPPreinstallEntry{Market: "default", Package: "time-server"}, false},
		{"bare_package", "github", MCPPreinstallEntry{Market: "default", Package: "github"}, false},
		{"whitespace", "  opencode / github  ", MCPPreinstallEntry{Market: "opencode", Package: "github"}, false},
		{"empty", "", MCPPreinstallEntry{}, true},
		{"missing_package", "default/", MCPPreinstallEntry{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePreinstallEntry(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParsePreinstallEntry(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("ParsePreinstallEntry(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}
