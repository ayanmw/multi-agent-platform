package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
)

// findNode returns the absolute path to the node executable, or skips the test
// if Node is not installed.
func findNode(t *testing.T) string {
	t.Helper()
	name := "node"
	if runtime.GOOS == "windows" {
		name = "node.exe"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	return path
}

// TestManagerWithRealStdioTimeServer starts the Node.js time example and
// verifies the Manager can load it and execute its tool.
func TestManagerWithRealStdioTimeServer(t *testing.T) {
	node := findNode(t)

	script, err := filepath.Abs("../../../examples/mcp/time/mcp-time-server.js")
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("time example script not found: %v", err)
	}

	reg := tool.NewRegistry()
	mgr := NewManager(reg, EmptyRepository{})
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := ServerConfig{
		Name:      "time",
		Transport: "stdio",
		Command:   node,
		Args:      []string{script},
		Enabled:   true,
	}

	if err := mgr.LoadStaticServers(ctx, []ServerConfig{cfg}); err != nil {
		t.Fatalf("LoadStaticServers: %v", err)
	}

	servers := mgr.ListServers()
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	if !servers[0].Enabled {
		t.Fatalf("expected server to be enabled")
	}

	out, err := reg.Execute("mcp__time__get_current_time", map[string]any{"timezone": "UTC"})
	if err != nil {
		t.Fatalf("execute time tool: %v", err)
	}
	text, ok := out.(string)
	if !ok || text == "" {
		t.Fatalf("expected non-empty string result, got %v", out)
	}
}

// TestManagerWithRealStdioCalcServer starts the Node.js calc example and runs
// all four arithmetic tools.
func TestManagerWithRealStdioCalcServer(t *testing.T) {
	node := findNode(t)

	script, err := filepath.Abs("../../../examples/mcp/calc/mcp-calc-server.js")
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("calc example script not found: %v", err)
	}

	reg := tool.NewRegistry()
	mgr := NewManager(reg, EmptyRepository{})
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := ServerConfig{
		Name:      "calc",
		Transport: "stdio",
		Command:   node,
		Args:      []string{script},
		Enabled:   true,
	}

	if err := mgr.LoadStaticServers(ctx, []ServerConfig{cfg}); err != nil {
		t.Fatalf("LoadStaticServers: %v", err)
	}

	tests := []struct {
		tool   string
		a, b   float64
		want   string
	}{
		{"mcp__calc__add", 1, 2, "3"},
		{"mcp__calc__subtract", 5, 3, "2"},
		{"mcp__calc__multiply", 4, 6, "24"},
		{"mcp__calc__divide", 10, 2, "5"},
	}
	for _, tc := range tests {
		out, err := reg.Execute(tc.tool, map[string]any{"a": tc.a, "b": tc.b})
		if err != nil {
			t.Fatalf("execute %s: %v", tc.tool, err)
		}
		if out != tc.want {
			t.Fatalf("%s: got %v, want %s", tc.tool, out, tc.want)
		}
	}
}

// TestManagerReloadsDynamicServerFromDB verifies that a server persisted to
// the SqliteRepository is reloaded by a fresh Manager instance.
func TestManagerReloadsDynamicServerFromDB(t *testing.T) {
	node := findNode(t)
	script, err := filepath.Abs("../../../examples/mcp/calc/mcp-calc-server.js")
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("calc example script not found: %v", err)
	}

	db := newTestDB(t)
	defer db.Close()

	repo := NewSqliteRepository(db)
	ctx := context.Background()

	ms := ManagedServer{
		ID:      "calc-dynamic",
		Source:  SourceDB,
		Enabled: true,
		Config: ServerConfig{
			Name:      "calc-dynamic",
			Transport: "stdio",
			Command:   node,
			Args:      []string{script},
			Enabled:   true,
		},
	}
	if err := repo.Save(ctx, ms); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// First manager loads from DB.
	reg1 := tool.NewRegistry()
	mgr1 := NewManager(reg1, repo)
	if err := mgr1.LoadDBServers(ctx); err != nil {
		t.Fatalf("LoadDBServers: %v", err)
	}
	out, err := reg1.Execute("mcp__calc-dynamic__add", map[string]any{"a": 7, "b": 8})
	if err != nil {
		t.Fatalf("execute before restart: %v", err)
	}
	if out != "15" {
		t.Fatalf("expected 15, got %v", out)
	}
	mgr1.Close()

	// Second manager loads the same server from DB, proving persistence.
	reg2 := tool.NewRegistry()
	mgr2 := NewManager(reg2, repo)
	defer mgr2.Close()
	if err := mgr2.LoadDBServers(ctx); err != nil {
		t.Fatalf("LoadDBServers second manager: %v", err)
	}
	out, err = reg2.Execute("mcp__calc-dynamic__multiply", map[string]any{"a": 3, "b": 4})
	if err != nil {
		t.Fatalf("execute after restart: %v", err)
	}
	if out != "12" {
		t.Fatalf("expected 12, got %v", out)
	}
}
