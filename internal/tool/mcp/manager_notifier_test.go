package mcp

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
)

// TestManagerChangeNotifier 验证 connect/disconnect/enable/disable 操作会以
// 正确的 action 与 server ID 通知已注册的 change notifier，并且
// AddServer/RemoveServer 也会触发通知。
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

	// 添加 disabled server —— 应触发 add，但不应 connect。
	if err := mgr.AddServer(ctx, ManagedServer{ID: "demo", Config: ServerConfig{Name: "demo", Transport: "stdio", Enabled: false}}); err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	// 用 fake transport 手动加载，以便 enable 能成功。
	if err := mgr.loader.LoadServerWithTransport(ctx, ServerConfig{Name: "demo", Transport: "stdio", Enabled: true}, newRecordingTransport(ft)); err != nil {
		t.Fatalf("LoadServerWithTransport: %v", err)
	}

	// Enable 通过 Manager.connect 触发自身的 connect 事件。
	if err := mgr.EnableServer(ctx, "demo"); err != nil {
		t.Fatalf("EnableServer: %v", err)
	}

	// Disable 触发 disable。
	if err := mgr.DisableServer(ctx, "demo"); err != nil {
		t.Fatalf("DisableServer: %v", err)
	}

	// Remove 触发 delete。
	if err := mgr.RemoveServer(ctx, "demo"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// 规范化事件以便断言。
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
