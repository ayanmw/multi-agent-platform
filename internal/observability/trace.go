package observability

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// TraceContext 在 goroutine / HTTP 边界之间传递 trace 标识符。
// 它刻意保持轻量且无外部依赖（不引入 OpenTelemetry 库）。
type TraceContext struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	TaskID       string
	AgentID      string
	Operation    string
	StartTime    time.Time
}

// HTTPHeaders 返回用于通过 HTTP 传播的 W3C 风格 header。
func (tc *TraceContext) HTTPHeaders() map[string]string {
	return map[string]string{
		"X-Trace-ID": tc.TraceID,
		"X-Span-ID":  tc.SpanID,
		"X-Task-ID":  tc.TaskID,
		"X-Agent-ID": tc.AgentID,
	}
}

// SpanRecord 是一个已完成、可导出的 span 表示。
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

// Tracer 是一个简单的内存 span 生成器。它用有界 ring buffer 保存已完成
// 的 span，运维方无需外部 collector 即可查询最近的 trace。
type Tracer struct {
	mu    sync.Mutex
	spans []SpanRecord
	limit int
}

// NewTracer 创建一个带界 span 缓冲的 tracer。
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

// StartRoot 为一个顶层操作（例如一个 task）创建 root span。
func (t *Tracer) StartRoot(taskID, operation string) *TraceContext {
	return &TraceContext{
		TraceID:   generateTraceID(),
		SpanID:    generateSpanID(),
		TaskID:    taskID,
		Operation: operation,
		StartTime: time.Now().UTC(),
	}
}

// StartChild 创建一个 child span。调用 Finish 完成它。
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

// Finish 完成一个 span 并将其推入有界缓冲。
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

// FinishWithAttributes 完成一个 span，并附带额外的 attributes。
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

// Flush 返回所有已缓冲 span 的副本并清空缓冲。
func (t *Tracer) Flush() []SpanRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]SpanRecord, len(t.spans))
	copy(out, t.spans)
	t.spans = t.spans[:0]
	return out
}

// Peek 返回副本但不清空缓冲。
func (t *Tracer) Peek() []SpanRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]SpanRecord, len(t.spans))
	copy(out, t.spans)
	return out
}

// Extract 从传播过来的 header 中重建一个 TraceContext。
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

// JSON 以 JSON 形式返回所有已缓冲的 span。
func (t *Tracer) JSON() ([]byte, error) {
	return json.Marshal(t.Peek())
}
