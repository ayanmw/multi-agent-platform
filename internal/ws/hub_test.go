package ws

import (
	"testing"

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
