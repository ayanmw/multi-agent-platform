package mcp

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
)

// TestManagerChangeNotifier verifies that connect/disconnect/enable/disable
// operations notify the registered change notifier with the correct action and
// server ID, and that AddServer/RemoveServer do so as well.
func TestManagerChangeNotifier(t *testing.T) {
	reg := tool.NewRegistry()
	mgr := NewManager(reg, EmptyRepository{})
	defer mgr.Close()

	ft := fakeMCPForManager(t)

	var mu sync.Mutex
	var events []struct {
		action   string
		serverID string
	}

	mgr.SetChangeNotifier(func(action, serverID string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, struct {
			action   string
			serverID string
		}{action: action, serverID: serverID})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Add disabled server — should emit add but not connect.
	if err := mgr.AddServer(ctx, ManagedServer{ID: "demo", Config: ServerConfig{Name: "demo", Transport: "stdio", Enabled: false}}); err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	// Manually load with fake transport so enable works.
	if err := mgr.loader.LoadServerWithTransport(ctx, ServerConfig{Name: "demo", Transport: "stdio", Enabled: true}, newRecordingTransport(ft)); err != nil {
		t.Fatalf("LoadServerWithTransport: %v", err)
	}

	// Enable emits its own connect via Manager.connect.
	if err := mgr.EnableServer(ctx, "demo"); err != nil {
		t.Fatalf("EnableServer: %v", err)
	}

	// Disable emits disable.
	if err := mgr.DisableServer(ctx, "demo"); err != nil {
		t.Fatalf("DisableServer: %v", err)
	}

	// Remove emits delete.
	if err := mgr.RemoveServer(ctx, "demo"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Normalize events for assertion.
	want := []struct{ action, serverID string }{
		{"add", "demo"},
		{"connect", "demo"},
		{"enable", "demo"},
		{"disable", "demo"},
		{"delete", "demo"},
	}
	if len(events) < len(want) {
		t.Fatalf("expected at least %d events, got %d: %+v", len(want), len(events), events)
	}
	for i, w := range want {
		if events[i].action != w.action || events[i].serverID != w.serverID {
			t.Fatalf("event %d = (%s, %s), want (%s, %s)", i, events[i].action, events[i].serverID, w.action, w.serverID)
		}
	}
}
