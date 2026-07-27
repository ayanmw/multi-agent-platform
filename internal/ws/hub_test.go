package ws

import (
	"context"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// newTestEvent 用给定的 id 和 type 创建一个 Event。
func newTestEvent(id, typ string) event.Event {
	return event.Event{EventID: id, Type: typ}
}

func TestEventBufferAppendAndReplay(t *testing.T) {
	buf := newEventBuffer(5)
	e1 := newTestEvent("a", "task_started")
	e2 := newTestEvent("b", "step_started")
	e3 := newTestEvent("c", "llm_delta")
	buf.append(e1)
	buf.append(e2)
	buf.append(e3)

	evts, err := buf.eventsAfter("b", 10)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(evts) != 1 || evts[0].EventID != "c" {
		t.Fatalf("expected [c], got %+v", evts)
	}
}

func TestEventBufferReplayLimit(t *testing.T) {
	buf := newEventBuffer(100)
	for i := 0; i < 10; i++ {
		buf.append(newTestEvent(string(rune('a'+i)), "e"))
	}

	evts, err := buf.eventsAfter("b", 3)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// eventsAfter 返回 b 严格之后的事件，最多 limit 条。
	if len(evts) != 3 {
		t.Fatalf("expected 3 events, got %d", len(evts))
	}
	expected := []string{"c", "d", "e"}
	for i, exp := range expected {
		if evts[i].EventID != exp {
			t.Fatalf("expected event[%d]=%s, got %s", i, exp, evts[i].EventID)
		}
	}
}

func TestEventBufferUnknownEventID(t *testing.T) {
	buf := newEventBuffer(5)
	buf.append(newTestEvent("a", "task_started"))

	_, err := buf.eventsAfter("missing", 10)
	if err != ErrEventIDNotFound {
		t.Fatalf("expected ErrEventIDNotFound, got %v", err)
	}
}

func TestEventBufferEviction(t *testing.T) {
	buf := newEventBuffer(3)
	for i := 0; i < 5; i++ {
		buf.append(newTestEvent(string(rune('a'+i)), "e"))
	}

	// a 和 b 应已被驱逐。
	_, err := buf.eventsAfter("a", 10)
	if err != ErrEventIDNotFound {
		t.Fatalf("expected 'a' to be evicted, got %v", err)
	}
	_, err = buf.eventsAfter("b", 10)
	if err != ErrEventIDNotFound {
		t.Fatalf("expected 'b' to be evicted, got %v", err)
	}

	evts, err := buf.eventsAfter("c", 10)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(evts) != 2 || evts[0].EventID != "d" || evts[1].EventID != "e" {
		t.Fatalf("expected [d e], got %+v", evts)
	}
}

func TestEventBufferEmpty(t *testing.T) {
	buf := newEventBuffer(5)
	_, err := buf.eventsAfter("x", 10)
	if err != ErrEventIDNotFound {
		t.Fatalf("expected ErrEventIDNotFound on empty buffer, got %v", err)
	}
}

func TestEventBufferLastEvent(t *testing.T) {
	buf := newEventBuffer(5)
	buf.append(newTestEvent("a", "task_started"))

	evts, err := buf.eventsAfter("a", 10)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(evts) != 0 {
		t.Fatalf("expected 0 events after last event, got %d", len(evts))
	}
}

func TestEventBufferCriticalEventsNotEvicted(t *testing.T) {
	// 容量 10：先填 8 条普通事件，再填 1 条关键事件，最后继续填 5 条普通事件。
	// 关键事件应被保留，不会被普通事件挤掉。
	buf := newEventBuffer(10)
	for i := 0; i < 8; i++ {
		buf.append(newTestEvent(string(rune('a'+i)), "llm_delta"))
	}
	critical := newTestEvent("critical", "task_failed")
	buf.append(critical)
	for i := 8; i < 13; i++ {
		buf.append(newTestEvent(string(rune('a'+i)), "llm_delta"))
	}

	_, err := buf.eventsAfter("critical", 10)
	if err != nil {
		t.Fatalf("critical event was evicted: %v", err)
	}
}

func TestHubReplay(t *testing.T) {
	h := NewHub()
	h.eventBuf.append(newTestEvent("x", "task_started"))
	h.eventBuf.append(newTestEvent("y", "step_started"))
	h.eventBuf.append(newTestEvent("z", "llm_delta"))

	evts, err := h.ReplayEvents("y", 10)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(evts) != 1 || evts[0].EventID != "z" {
		t.Fatalf("expected [z], got %+v", evts)
	}
}

// TestHubShutdown 验证 Shutdown 可让 Run 的 goroutine 退出。
func TestHubShutdown(t *testing.T) {
	h := NewHub()
	go h.Run()

	client := h.RegisterTestClient("client-1")
	// 等待 client 注册完成
	time.Sleep(10 * time.Millisecond)

	// 在关闭前发一个事件验证广播仍在工作
	h.SendEvent(newTestEvent("e1", "task_started"))
	select {
	case evt := <-client.Send:
		if evt.EventID != "e1" {
			t.Fatalf("unexpected event: %v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive broadcast event before shutdown")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Shutdown 后向 broadcast 发事件不应再被处理（Run 已退出）。
	// 由于 broadcast 是无缓冲 channel，SendEvent 会阻塞；这里用 select 验证。
	deadline := time.After(50 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		h.SendEvent(newTestEvent("e2", "step_started"))
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("SendEvent should block after shutdown")
	case <-deadline:
		// expected: Run 已退出， broadcast channel 无人接收。
	}
}

