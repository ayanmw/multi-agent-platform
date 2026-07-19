// orchestrator_test.go —— AgentBus 持久化 hook 的单元测试。
//
// 这些测试有意保持轻量：它们只验证 AgentBus 的管道行为
// （RegisterHandler / SendMessage / SetPersistFn），而不启动完整的
// Engine 或 SQLite。涉及真实 db 包的持久化行为由
// pkg/db/persistence_test.go 以及 migration 测试覆盖。

package orchestrator

import (
	"sync"
	"testing"
	"time"
)

// TestAgentBus_DeliversToRegisteredHandler 验证 SendMessage 会同步调用
// 为目标 agent 注册的 handler。
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

// TestAgentBus_PersistFnHookFired 确认 SetPersistFn 安装的 hook 会对
// 每次 SendMessage（无论已投递还是入队）都触发。
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

	// 注册一个 handler，使第二次发送能立即投递；第一次发送的目标未注册，
	// 会被入队。两次发送都应触发持久化 hook。
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

	// 持久化 hook 在 goroutine 中运行；短暂轮询直到两条消息都被记录。
	// 顺序不确定，因此按集合成员判断而不是按位置。
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

// TestAgentBus_NoPersistWhenHookNil 用于说明：默认（nil）hook 是 no-op，
// SendMessage 仍能正常工作。
func TestAgentBus_NoPersistWhenHookNil(t *testing.T) {
	bus := NewAgentBus()
	bus.SendMessage(AgentMessage{
		FromAgentID: "a",
		ToAgentID:   "b",
		Content:     "x",
	})
	// 只断言没有 panic —— 通过即说明 nil hook 被正确处理。
}

// TestAgentBus_RoutesBySubTask 验证当消息携带不同的 subTaskID 时，
// 精确 (agentID, subTaskID) handler 能收到消息，而 agentID-only
// handler 不会收到。Phase 7-J。
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
		// 预期：fallback 未被调用。
	}
}

// TestAgentBus_FallsBackToAgentIDOnly 验证当存在针对其它 subTaskID 的
// 精确 handler 时，携带空 SubTaskID 的消息会被投递给 agentID-only
// handler。
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
		// 预期：exact handler 未被调用。
	}
}
