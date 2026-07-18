// orchestrator_test.go — unit tests for the AgentBus persistence hook.
//
// These tests are intentionally lightweight: they exercise the AgentBus
// plumbing (RegisterHandler / SendMessage / SetPersistFn) without spinning
// up the full Engine or SQLite. Persistence behaviour with the real db
// package is covered in pkg/db/persistence_test.go and via the migration tests.

package orchestrator

import (
	"sync"
	"testing"
	"time"
)

// TestAgentBus_DeliversToRegisteredHandler verifies SendMessage invokes the
// handler registered for the target agent synchronously.
func TestAgentBus_DeliversToRegisteredHandler(t *testing.T) {
	bus := NewAgentBus()

	var got AgentMessage
	var mu sync.Mutex
	ready := make(chan struct{})
	bus.RegisterHandler("agent_b", func(msg AgentMessage) {
		mu.Lock()
		got = msg
		mu.Unlock()
		close(ready)
	})

	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_a",
		ToAgentID:   "agent_b",
		Type:        "request",
		Content:     "hello",
	})

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("handler not invoked within 2s")
	}

	mu.Lock()
	defer mu.Unlock()
	if got.FromAgentID != "agent_a" || got.ToAgentID != "agent_b" || got.Content != "hello" {
		t.Errorf("delivered message mismatch: %+v", got)
	}
	if got.Timestamp.IsZero() {
		t.Error("Timestamp not set by SendMessage")
	}
}

// TestAgentBus_PersistFnHookFired confirms SetPersistFn installs a hook that
// fires for every SendMessage (delivered or queued).
func TestAgentBus_PersistFnHookFired(t *testing.T) {
	bus := NewAgentBus()

	var seen []AgentMessage
	var mu sync.Mutex
	bus.SetPersistFn(func(msg AgentMessage) error {
		mu.Lock()
		seen = append(seen, msg)
		mu.Unlock()
		return nil
	})

	// Register a handler so the second send is delivered immediately, and
	// the first send to a non-registered target is queued. Both should
	// still fire the persistence hook.
	bus.RegisterHandler("agent_b", func(AgentMessage) {})

	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_a",
		ToAgentID:   "ghost_agent",
		Type:        "observation",
		Content:     "queued",
	})
	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_a",
		ToAgentID:   "agent_b",
		Type:        "response",
		Content:     "delivered",
	})

	// The persist hook runs in a goroutine; poll briefly until both
	// messages have been recorded. Order is non-deterministic, so we
	// check set membership rather than position.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("persist hook fired %d times, want 2", len(seen))
	}
	got := map[string]bool{}
	for _, m := range seen {
		got[m.Content] = true
		if m.Timestamp.IsZero() {
			t.Errorf("persisted message missing Timestamp: %+v", m)
		}
	}
	if !got["queued"] || !got["delivered"] {
		t.Errorf("missing persisted messages: %v", got)
	}
}

// TestAgentBus_NoPersistWhenHookNil documents that the default (nil) hook
// is a no-op and SendMessage continues to work.
func TestAgentBus_NoPersistWhenHookNil(t *testing.T) {
	bus := NewAgentBus()
	bus.SendMessage(AgentMessage{
		FromAgentID: "a",
		ToAgentID:   "b",
		Content:     "x",
	})
	// Just assert no panic — passing means nil hook is handled correctly.
}

// TestAgentBus_MetadataForwarded ensures the persist hook receives the
// Metadata map intact, including task_id set by Engine.sendAgentMessage.
func TestAgentBus_MetadataForwarded(t *testing.T) {
	bus := NewAgentBus()
	got := make(chan AgentMessage, 1)
	bus.SetPersistFn(func(msg AgentMessage) error {
		got <- msg
		return nil
	})
	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_a",
		ToAgentID:   "agent_b",
		Type:        "request",
		Content:     "ping",
		Metadata: map[string]string{
			"task_id":       "task_123",
			"from_agent_id": "agent_a",
		},
	})
	select {
	case msg := <-got:
		if msg.Metadata["task_id"] != "task_123" {
			t.Errorf("task_id metadata = %q, want task_123", msg.Metadata["task_id"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("persist hook did not fire within 2s")
	}
}
