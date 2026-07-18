package mcp

import "testing"

func TestServerConfigNamespace(t *testing.T) {
    cfg := ServerConfig{Name: "fs", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "."}}
    got := cfg.Namespace()
    if got != "fs" {
        t.Fatalf("expected namespace fs, got %s", got)
    }
}

func TestToolNamespaceName(t *testing.T) {
    s := ServerConfig{Name: "fs"}
    got := s.ToolName("read_file")
    want := "mcp__fs__read_file"
    if got != want {
        t.Fatalf("expected %s, got %s", want, got)
    }
}
