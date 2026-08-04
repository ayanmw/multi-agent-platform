package ws

import (
	"context"
	"testing"
	"time"

	"github.com/ayanmw/multi-agent-platform/pkg/event"
)

// TestHubBroadcastSessionScope 验证 E3 隔离增强（N3-02）：已订阅 session 的
// client 只能收到命中其订阅集的事件，未订阅的 legacy client 仍收到全部，
// 从而服务端按会话隔离事件广播，杜绝跨 session 实时数据泄漏。
func TestHubBroadcastSessionScope(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
	}()

	// scoped 仅订阅 sess-A；legacy 不过滤（接收全部）。
	scoped := h.RegisterTestClientWithSessions("scoped-A", []string{"sess-A"})
	legacy := h.RegisterTestClient("legacy")

	// 等待 Run goroutine 处理 register。
	time.Sleep(20 * time.Millisecond)

	// e1：session_id=sess-A → scoped 收、legacy 收。
	h.SendEvent(event.Event{EventID: "e1", Type: "task_started", Timestamp: 1, Data: map[string]any{"session_id": "sess-A"}})
	// e2：session_id=sess-B → scoped 不收、legacy 收（跨 session 不应泄漏到 scoped）。
	h.SendEvent(event.Event{EventID: "e2", Type: "task_started", Timestamp: 2, Data: map[string]any{"session_id": "sess-B"}})
	// e3：无 session_id 的系统事件 → scoped 不收、legacy 收。
	h.SendEvent(event.Event{EventID: "e3", Type: "health", Timestamp: 3})

	gotScoped := drainClientEvents(t, scoped, 1, 500*time.Millisecond)
	gotLegacy := drainClientEvents(t, legacy, 3, 500*time.Millisecond)

	// scoped 必须收到 e1（命中订阅）。
	if !hasEvent(gotScoped, "e1") {
		t.Errorf("scoped client missed session-scoped event e1")
	}
	// scoped 绝不能收到 e2（其它 session）或 e3（无 session）。
	if hasEvent(gotScoped, "e2") || hasEvent(gotScoped, "e3") {
		t.Errorf("cross-session/system event leaked to scoped client: %v", gotScoped)
	}
	// legacy 收到全部 3 条（向后兼容）。
	for _, id := range []string{"e1", "e2", "e3"} {
		if !hasEvent(gotLegacy, id) {
			t.Errorf("legacy client missed event %s", id)
		}
	}
}

// drainClientEvents 在超时内从 client.Send 非阻塞收集至多 want 条事件。
func drainClientEvents(t *testing.T, c *Client, want int, timeout time.Duration) []event.Event {
	t.Helper()
	var out []event.Event
	deadline := time.Now().Add(timeout)
	for len(out) < want {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		select {
		case evt := <-c.Send:
			out = append(out, evt)
		case <-time.After(remaining):
			return out
		}
	}
	// 抽干剩余（避免后续断言受缓冲影响）。
	for {
		select {
		case evt := <-c.Send:
			out = append(out, evt)
		default:
			return out
		}
	}
}

// hasEvent 判断事件列表中是否含指定 EventID。
func hasEvent(events []event.Event, id string) bool {
	for _, e := range events {
		if e.EventID == id {
			return true
		}
	}
	return false
}
