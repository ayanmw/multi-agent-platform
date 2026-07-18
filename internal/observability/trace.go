package observability

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// TraceContext carries trace identifiers across goroutines / HTTP boundaries.
// It is intentionally lightweight and dependency-free (no OpenTelemetry libs).
type TraceContext struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	TaskID       string
	AgentID      string
	Operation    string
	StartTime    time.Time
}

// HTTPHeaders returns W3C-style headers for propagation over HTTP.
func (tc *TraceContext) HTTPHeaders() map[string]string {
	return map[string]string{
		"X-Trace-ID": tc.TraceID,
		"X-Span-ID":  tc.SpanID,
		"X-Task-ID":  tc.TaskID,
		"X-Agent-ID": tc.AgentID,
	}
}

// SpanRecord is the completed, exportable representation of a span.
type SpanRecord struct {
	TraceID      string         `json:"trace_id"`
	SpanID       string         `json:"span_id"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	TaskID       string         `json:"task_id"`
	AgentID      string         `json:"agent_id"`
	Operation    string         `json:"operation"`
	StartTime    time.Time      `json:"start_time"`
	DurationMS   int64          `json:"duration_ms"`
	Status       string         `json:"status"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

// Tracer is a simple in-memory span producer. It keeps a bounded ring buffer of
// completed spans so operators can query recent traces without an external collector.
type Tracer struct {
	mu    sync.Mutex
	spans []SpanRecord
	limit int
}

// NewTracer creates a tracer with a bounded span buffer.
func NewTracer(limit int) *Tracer {
	if limit <= 0 {
		limit = 1000
	}
	return &Tracer{limit: limit}
}

func generateTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func generateSpanID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// StartRoot creates a root span for a top-level operation (e.g., a task).
func (t *Tracer) StartRoot(taskID, operation string) *TraceContext {
	return &TraceContext{
		TraceID:   generateTraceID(),
		SpanID:    generateSpanID(),
		TaskID:    taskID,
		Operation: operation,
		StartTime: time.Now().UTC(),
	}
}

// StartChild creates a child span. Call Finish to complete it.
func (t *Tracer) StartChild(parent *TraceContext, operation string) *TraceContext {
	return &TraceContext{
		TraceID:      parent.TraceID,
		SpanID:       generateSpanID(),
		ParentSpanID: parent.SpanID,
		TaskID:       parent.TaskID,
		AgentID:      parent.AgentID,
		Operation:    operation,
		StartTime:    time.Now().UTC(),
	}
}

// Finish completes a span and pushes it to the bounded buffer.
func (t *Tracer) Finish(ctx *TraceContext, err error) {
	if ctx == nil {
		return
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	rec := SpanRecord{
		TraceID:      ctx.TraceID,
		SpanID:       ctx.SpanID,
		ParentSpanID: ctx.ParentSpanID,
		TaskID:       ctx.TaskID,
		AgentID:      ctx.AgentID,
		Operation:    ctx.Operation,
		StartTime:    ctx.StartTime,
		DurationMS:   time.Since(ctx.StartTime).Milliseconds(),
		Status:       status,
	}
	t.push(rec)
}

// FinishWithAttributes completes a span with extra attributes.
func (t *Tracer) FinishWithAttributes(ctx *TraceContext, err error, attrs map[string]any) {
	if ctx == nil {
		return
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	rec := SpanRecord{
		TraceID:      ctx.TraceID,
		SpanID:       ctx.SpanID,
		ParentSpanID: ctx.ParentSpanID,
		TaskID:       ctx.TaskID,
		AgentID:      ctx.AgentID,
		Operation:    ctx.Operation,
		StartTime:    ctx.StartTime,
		DurationMS:   time.Since(ctx.StartTime).Milliseconds(),
		Status:       status,
		Attributes:   attrs,
	}
	t.push(rec)
}

func (t *Tracer) push(rec SpanRecord) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.spans = append(t.spans, rec)
	if len(t.spans) > t.limit {
		t.spans = t.spans[len(t.spans)-t.limit:]
	}
}

// Flush returns a copy of all buffered spans and clears the buffer.
func (t *Tracer) Flush() []SpanRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]SpanRecord, len(t.spans))
	copy(out, t.spans)
	t.spans = t.spans[:0]
	return out
}

// Peek returns a copy without clearing.
func (t *Tracer) Peek() []SpanRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]SpanRecord, len(t.spans))
	copy(out, t.spans)
	return out
}

// Extract reconstructs a TraceContext from propagated headers.
func (t *Tracer) Extract(headers map[string]string) (*TraceContext, error) {
	traceID := headers["X-Trace-ID"]
	spanID := headers["X-Span-ID"]
	if traceID == "" || spanID == "" {
		return nil, fmt.Errorf("missing trace_id or span_id")
	}
	return &TraceContext{
		TraceID: traceID,
		SpanID:  spanID,
		TaskID:  headers["X-Task-ID"],
		AgentID: headers["X-Agent-ID"],
	}, nil
}

// JSON returns all buffered spans as JSON.
func (t *Tracer) JSON() ([]byte, error) {
	return json.Marshal(t.Peek())
}
