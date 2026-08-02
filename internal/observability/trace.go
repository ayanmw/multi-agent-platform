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
//
// Phase 8 (P5)：onSpan 回调改为有界 channel + 单消费者 goroutine，
// 避免每个 span 无限制地 spawn goroutine 导致 OOM。
type Tracer struct {
	mu           sync.Mutex
	spans        []SpanRecord
	limit        int
	onSpanCh     chan SpanRecord // 有界 channel，背压上限 onSpanBufferSize
	wg           sync.WaitGroup
	droppedSpans uint64 // 因 channel 满而丢弃的 span 计数
}

// onSpanBufferSize 是 onSpan 回调 channel 的容量。
// 满了之后新 span 会被丢弃并计入 droppedSpans —— 宁可丢事件，
// 也不能阻塞业务线程或无限堆积 goroutine。
const onSpanBufferSize = 256

// NewTracer 创建一个带界 span 缓冲的 tracer。
func NewTracer(limit int) *Tracer {
	if limit <= 0 {
		limit = 1000
	}
	return &Tracer{limit: limit}
}

// SetOnSpan 注册一个回调，每次 span 完成时被调用。
// 用于把 trace span 转成 `trace_span` WebSocket 事件广播到前端。
// 传 nil 表示注销回调。
//
// Phase 8 (P5)：回调通过有界 channel 异步执行，避免无限制 goroutine。
// 注意 close(old) + wg.Wait() 必须在 **锁外** 执行：消费者回调里如果反过来
// 调用 Tracer 的任何方法（Peek / DroppedSpans / push），持锁等待就会死锁。
func (t *Tracer) SetOnSpan(fn func(SpanRecord)) {
	t.mu.Lock()
	old := t.onSpanCh
	t.onSpanCh = nil
	t.mu.Unlock()

	if old != nil {
		close(old)
		t.wg.Wait()
	}
	if fn == nil {
		return
	}

	ch := make(chan SpanRecord, onSpanBufferSize)
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		for rec := range ch {
			fn(rec)
		}
	}()

	t.mu.Lock()
	t.onSpanCh = ch
	t.mu.Unlock()
}

// SetLimit 在运行期调整 span ring buffer 的上限。
//
// 存在的原因：tracer 是包级变量，在 init 阶段就已创建，那时 .env 还没加载。
// main() 读完配置后调用本方法，把 TRACE_BUFFER_LIMIT 应用上去。
// 调小上限会立即裁掉最旧的 span。limit <= 0 视为无效，保持原值。
func (t *Tracer) SetLimit(limit int) {
	if limit <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.limit = limit
	if len(t.spans) > limit {
		t.spans = t.spans[len(t.spans)-limit:]
	}
}

// Limit 返回当前 span 缓冲上限。
func (t *Tracer) Limit() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.limit
}

// Close 注销回调并等待消费者 goroutine 退出，供进程优雅关闭时调用。
func (t *Tracer) Close() {
	t.SetOnSpan(nil)
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
// 创建时会继承 parent 的 TaskID，但 AgentID 由调用方显式传入，以便 worker
// sub-agent 的 span 能正确标识其实际 agent。
func (t *Tracer) StartChild(parent *TraceContext, agentID, operation string) *TraceContext {
	return &TraceContext{
		TraceID:      parent.TraceID,
		SpanID:       generateSpanID(),
		ParentSpanID: parent.SpanID,
		TaskID:       parent.TaskID,
		AgentID:      agentID,
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
	if t.onSpanCh != nil {
		select {
		case t.onSpanCh <- rec:
		default:
			// channel 满，丢弃事件（优于无限堆积 goroutine）
			t.droppedSpans++
		}
	}
}

// DroppedSpans 返回因 channel 满而被丢弃的 span 数量。
func (t *Tracer) DroppedSpans() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.droppedSpans
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
