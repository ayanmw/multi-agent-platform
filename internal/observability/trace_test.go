package observability

import (
	"testing"
	"time"
)

func TestTracerStartFinish(t *testing.T) {
	tracer := NewTracer(100)
	ctx := tracer.StartRoot("task-1", "agent-loop")
	if ctx.TraceID == "" || ctx.SpanID == "" {
		t.Fatal("trace_id and span_id must be set")
	}

	ctx2 := tracer.StartChild(ctx, "agent_default", "think-step")
	tracer.Finish(ctx2, nil)
	tracer.Finish(ctx, nil)

	spans := tracer.Flush()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
	if spans[0].ParentSpanID != ctx.SpanID {
		t.Fatalf("child parent_span_id mismatch")
	}
}

func TestTraceContextPropagation(t *testing.T) {
	tracer := NewTracer(10)
	ctx := tracer.StartRoot("task-1", "agent-loop")
	h := ctx.HTTPHeaders()
	ctx2, err := tracer.Extract(h)
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	if ctx2.TraceID != ctx.TraceID || ctx2.SpanID != ctx.SpanID {
		t.Fatal("propagation mismatch")
	}
}

func TestTracerBoundedBuffer(t *testing.T) {
	tracer := NewTracer(2)
	for i := 0; i < 5; i++ {
		ctx := tracer.StartRoot("task", "op")
		tracer.Finish(ctx, nil)
		time.Sleep(time.Millisecond)
	}
	spans := tracer.Flush()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
}
