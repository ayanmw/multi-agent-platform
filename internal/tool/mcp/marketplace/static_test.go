package marketplace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// TestResolveConfigRelativeArgs 验证：当 projectRoot 注入后，ResolveConfig 会把
// 在 projectRoot 下真实存在的相对路径参数解析成绝对路径；不存在或非路径参数原样返回。
// 这是修复 "内置示例 MCP server 从非项目根目录启动时 EOF" 的核心逻辑。
func TestResolveConfigRelativeArgs(t *testing.T) {
	// 准备一个临时 projectRoot，并在其中放置 examples/mcp/demo.js，模拟真实脚本布局。
	root := t.TempDir()
	examplesMcp := filepath.Join(root, "examples", "mcp")
	if err := os.MkdirAll(examplesMcp, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	scriptPath := filepath.Join(examplesMcp, "demo.js")
	if err := os.WriteFile(scriptPath, []byte("// demo"), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	const catalog = `{
	  "version": "1.0.0",
	  "markets": [{"name": "default", "display_name": "T", "description": ""}],
	  "servers": [
	    {"id": "demo", "market": "default", "name": "Demo", "description": "", "transport": "stdio", "command": "node", "args": ["examples/mcp/demo.js"]},
	    {"id": "flags", "market": "default", "name": "Flags", "description": "", "transport": "stdio", "command": "node", "args": ["-y", "some-npm-pkg", "--flag"]}
	  ]
	}`

	prev := projectRoot
	SetProjectRoot(root)
	t.Cleanup(func() { projectRoot = prev })

	p, err := NewStaticProvider([]byte(catalog))
	if err != nil {
		t.Fatalf("NewStaticProvider: %v", err)
	}

	// 相对路径且文件存在 → 应解析为绝对路径。
	cfg, err := p.ResolveConfig(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ResolveConfig demo: %v", err)
	}
	if len(cfg.Args) != 1 {
		t.Fatalf("Args len = %d, want 1", len(cfg.Args))
	}
	if cfg.Args[0] != scriptPath {
		t.Fatalf("Args[0] = %q, want %q", cfg.Args[0], scriptPath)
	}

	// npm 包名 + flag 不应被解析（不含分隔符或文件不存在），原样返回。
	cfg2, err := p.ResolveConfig(context.Background(), "flags")
	if err != nil {
		t.Fatalf("ResolveConfig flags: %v", err)
	}
	want := []string{"-y", "some-npm-pkg", "--flag"}
	for i, a := range cfg2.Args {
		if a != want[i] {
			t.Fatalf("Args[%d] = %q, want %q", i, a, want[i])
		}
	}

	// 未注入 projectRoot 时应原样返回相对路径（保证向后兼容）。
	projectRoot = ""
	cfg3, err := p.ResolveConfig(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ResolveConfig demo (no root): %v", err)
	}
	if cfg3.Args[0] != "examples/mcp/demo.js" {
		t.Fatalf("Args[0] = %q, want relative path unchanged", cfg3.Args[0])
	}
}

// TestDetectProjectRoot 验证从子目录向上探测项目根目录的行为：
// 命中时返回的根目录下必须存在 examples/mcp；临时目录链下应返回空串而非 panic。
func TestDetectProjectRoot(t *testing.T) {
	if root := DetectProjectRoot(); root != "" {
		if _, err := os.Stat(filepath.Join(root, "examples", "mcp")); err != nil {
			t.Fatalf("detected root %q lacks examples/mcp: %v", root, err)
		}
	}
	tmp := t.TempDir()
	if got := findRootFrom(tmp); got != "" && !strings.Contains(filepath.Dir(got), "examples") {
		// 临时目录一般不会命中；命中则只要求返回的是有效根（含 examples/mcp）。
		if _, err := os.Stat(filepath.Join(got, "examples", "mcp")); err != nil {
			t.Fatalf("findRootFrom(%q) = %q, not a valid root", tmp, got)
		}
	}
}
