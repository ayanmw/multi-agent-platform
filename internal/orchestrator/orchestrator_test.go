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

// TestAgentBus_RoutesBySubTask verifies that an exact (agentID, subTaskID)
// handler receives the message while an agentID-only handler does not when
// the message carries a different subTaskID. Phase 7-J.
func TestAgentBus_RoutesBySubTask(t *testing.T) {
	bus := NewAgentBus()

	fallbackCalled := make(chan struct{}, 1)
	exactCalled := make(chan AgentMessage, 1)

	bus.RegisterHandler("agent_a", func(msg AgentMessage) {
		close(fallbackCalled)
	})
	bus.RegisterHandlerBySubTask("agent_a", "sub-x", func(msg AgentMessage) {
		exactCalled <- msg
	})

	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_b",
		ToAgentID:   "agent_a",
		SubTaskID:   "sub-x",
		Type:        "request",
		Content:     "to sub-x",
	})

	select {
	case msg := <-exactCalled:
		if msg.Content != "to sub-x" {
			t.Errorf("exact handler content = %q, want to sub-x", msg.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exact (agentID, subTaskID) handler not called")
	}

	select {
	case <-fallbackCalled:
		t.Fatal("agentID-only fallback should not receive a sub-task specific message")
	case <-time.After(100 * time.Millisecond):
		// Expected: fallback not invoked.
	}
}

// TestAgentBus_FallsBackToAgentIDOnly verifies that a message with an empty
// SubTaskID is delivered to the agentID-only handler when an exact handler
// also exists for a different subTaskID.
func TestAgentBus_FallsBackToAgentIDOnly(t *testing.T) {
	bus := NewAgentBus()

	fallbackCalled := make(chan AgentMessage, 1)
	exactCalled := make(chan struct{}, 1)

	bus.RegisterHandler("agent_a", func(msg AgentMessage) {
		fallbackCalled <- msg
	})
	bus.RegisterHandlerBySubTask("agent_a", "sub-x", func(msg AgentMessage) {
		close(exactCalled)
	})

	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_b",
		ToAgentID:   "agent_a",
		Type:        "request",
		Content:     "broadcast",
	})

	select {
	case msg := <-fallbackCalled:
		if msg.Content != "broadcast" {
			t.Errorf("fallback content = %q, want broadcast", msg.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agentID-only fallback not called")
	}

	select {
	case <-exactCalled:
		t.Fatal("exact handler should not receive a message without SubTaskID")
	case <-time.After(100 * time.Millisecond):
		// Expected: exact handler not invoked.
	}
}
