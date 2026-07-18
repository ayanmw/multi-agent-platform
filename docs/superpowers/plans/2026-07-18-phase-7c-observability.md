# Phase 7-C: 深度可观测 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 `internal/observability`（结构化日志 + 手写 Prometheus metrics）基础上，引入 OpenTelemetry trace、审计日志、延迟直方图、多 Agent trace 树可视化和事件回放能力，让系统“白盒”可观测从落盘指标升级到全链路追踪。

**Architecture:** 不引入外部 Prometheus SDK 或 OTel Collector 依赖，使用纯 Go 标准库实现所有可观测能力。通过新增 `internal/observability/audit.go`、`trace.go` 和 `histogram.go`，在 `cmd/server/main.go` 初始化时挂载：
- `Tracer` 生成跨 Agent / Tool / LLM 调用的 span 树，输出 JSON 到结构化日志和内存缓存；
- `Auditor` 记录所有写操作的 actor / target / before / after；
- `HistogramCollector` 提供 P50/P95/P99 延迟直方图，暴露到 `/metrics`；
- 事件回放通过 SQLite `steps` + `conversations` + 内存缓存重建 `AgentEvent` 序列；
- 前端新增 `TraceTreePanel` 接收 `trace_span` 事件并渲染成树。

**Tech Stack:** Go 1.25, standard library, modernc.org/sqlite, existing event/log layers.

---

## File Structure

| Path | Responsibility |
|------|----------------|
| `internal/observability/trace.go` | New: pure-Go `Tracer`, `Span`, `TraceContext`, JSON export. |
| `internal/observability/trace_test.go` | New: trace tree construction and propagation tests. |
| `internal/observability/audit.go` | New: `Auditor` interface + SQLite-backed auditor + memory buffer. |
| `internal/observability/audit_test.go` | New: audit record serialization and filtering tests. |
| `internal/observability/histogram.go` | New: lock-free bucket histogram for latencies. |
| `internal/observability/histogram_test.go` | New: histogram quantile tests. |
| `internal/observability/obs.go` | Add histogram rendering to `PrometheusText`. |
| `internal/runtime/engine.go` | Emit `trace_span` events around think(), tool execution, LLM calls. |
| `internal/tool/registry.go` | Record tool-execution audit entries via auditor hook. |
| `cmd/server/main.go` | Wire tracer/auditor/histogram; add `/api/audit`, `/api/traces`, `/api/replay` endpoints. |
| `cmd/server/api.go` | Add replay/audit/trace REST handlers. |
| `pkg/db/audit.go` | New: audit_records table + CRUD. |
| `pkg/db/migrate.go` | Migration v19 for audit_records table. |
| `web/src/components/TraceTreePanel.vue` | New: render trace span tree. |
| `web/src/composables/useTraceStore.ts` | New: WebSocket `trace_span` event state. |
| `.env.example` | Document new env vars. |

---

## Task 1: Pure-Go Tracer (Span tree + context propagation)

**Files:**
- Create: `internal/observability/trace.go`
- Test: `internal/observability/trace_test.go`
- Modify: `pkg/event/event.go`

- [ ] **Step 1: Write the failing test**

Create `internal/observability/trace_test.go`:

```go
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

	ctx2 := tracer.StartChild(ctx, "think-step")
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/observability -run 'TestTracer|TestTraceContext' -v`

Expected: FAIL `undefined: NewTracer`

- [ ] **Step 3: Implement the tracer**

Create `internal/observability/trace.go`:

```go
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
		"X-Trace-ID":  tc.TraceID,
		"X-Span-ID":   tc.SpanID,
		"X-Task-ID":   tc.TaskID,
		"X-Agent-ID":  tc.AgentID,
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
	mu     sync.Mutex
	spans  []SpanRecord
	limit  int
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
		TraceID:    ctx.TraceID,
		SpanID:     ctx.SpanID,
		ParentSpanID: ctx.ParentSpanID,
		TaskID:     ctx.TaskID,
		AgentID:    ctx.AgentID,
		Operation:  ctx.Operation,
		StartTime:  ctx.StartTime,
		DurationMS: time.Since(ctx.StartTime).Milliseconds(),
		Status:     status,
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
		TraceID:    ctx.TraceID,
		SpanID:     ctx.SpanID,
		ParentSpanID: ctx.ParentSpanID,
		TaskID:     ctx.TaskID,
		AgentID:    ctx.AgentID,
		Operation:  ctx.Operation,
		StartTime:  ctx.StartTime,
		DurationMS: time.Since(ctx.StartTime).Milliseconds(),
		Status:     status,
		Attributes: attrs,
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/observability -run 'TestTracer|TestTraceContext' -v`

Expected: PASS

- [ ] **Step 5: Add trace event type**

Edit `pkg/event/event.go`, add constant:

```go
	EventTraceSpan = "trace_span"
```

- [ ] **Step 6: Commit**

```bash
git add internal/observability/trace.go internal/observability/trace_test.go pkg/event/event.go
git commit -m "Phase 7-C: dependency-free tracer with context propagation"
```

---

## Task 2: Audit Logger (write operations actor/target/before/after)

**Files:**
- Create: `pkg/db/audit.go`
- Create: `internal/observability/audit.go`
- Test: `internal/observability/audit_test.go`
- Modify: `pkg/db/migrate.go`

- [ ] **Step 1: Write the failing test**

Create `internal/observability/audit_test.go`:

```go
package observability

import (
	"testing"
)

func TestMemoryAuditor(t *testing.T) {
	auditor := NewMemoryAuditor(10)
	auditor.Record(AuditRecord{
		Actor:  "user-1",
		Action: "delete_session",
		Target: "session-a",
	})
	recs := auditor.List(0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Actor != "user-1" {
		t.Fatalf("actor mismatch")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/observability -run TestMemoryAuditor -v`

Expected: FAIL `undefined: NewMemoryAuditor`

- [ ] **Step 3: Implement auditor interface and memory backend**

Create `internal/observability/audit.go`:

```go
package observability

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// AuditRecord captures a write operation for compliance and forensics.
type AuditRecord struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Actor     string         `json:"actor"`            // user/api_key/agent id
	Action    string         `json:"action"`           // e.g. delete_session, write_file
	Target    string         `json:"target"`           // resource id / path
	Before    map[string]any `json:"before,omitempty"`
	After     map[string]any `json:"after,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	IP        string         `json:"ip,omitempty"`
}

// Auditor is the interface for audit logging.
type Auditor interface {
	Record(rec AuditRecord)
	List(limit int) []AuditRecord
}

// MemoryAuditor keeps audit records in a bounded ring buffer.
type MemoryAuditor struct {
	mu      sync.RWMutex
	records []AuditRecord
	limit   int
}

// NewMemoryAuditor creates an in-memory auditor.
func NewMemoryAuditor(limit int) *MemoryAuditor {
	if limit <= 0 {
		limit = 10000
	}
	return &MemoryAuditor{limit: limit}
}

func (a *MemoryAuditor) Record(rec AuditRecord) {
	if rec.ID == "" {
		rec.ID = generateAuditID()
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, rec)
	if len(a.records) > a.limit {
		a.records = a.records[len(a.records)-a.limit:]
	}
}

func (a *MemoryAuditor) List(limit int) []AuditRecord {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if limit <= 0 || limit > len(a.records) {
		limit = len(a.records)
	}
	out := make([]AuditRecord, limit)
	copy(out, a.records[len(a.records)-limit:])
	return out
}

// JSON returns the latest N records as JSON.
func (a *MemoryAuditor) JSON(limit int) ([]byte, error) {
	return json.Marshal(a.List(limit))
}

func generateAuditID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "audit_" + hex.EncodeToString(b)
}
```

- [ ] **Step 4: Add SQLite persistence for audit records**

Create `pkg/db/audit.go`:

```go
package db

import (
	"encoding/json"
	"fmt"
	"time"
)

// AuditRecord mirrors observability.AuditRecord.
type AuditRecord struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Actor     string         `json:"actor"`
	Action    string         `json:"action"`
	Target    string         `json:"target"`
	Before    map[string]any `json:"before"`
	After     map[string]any `json:"after"`
	Reason    string         `json:"reason"`
	IP        string         `json:"ip"`
}

// InsertAuditRecord persists an audit record.
func InsertAuditRecord(rec AuditRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	beforeJSON, _ := json.Marshal(rec.Before)
	afterJSON, _ := json.Marshal(rec.After)
	_, err := DB.Exec(
		`INSERT INTO audit_records (id, timestamp, actor, action, target, before_json, after_json, reason, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.Timestamp, rec.Actor, rec.Action, rec.Target, string(beforeJSON), string(afterJSON), rec.Reason, rec.IP,
	)
	return err
}

// ListAuditRecords returns recent audit records ordered by timestamp desc.
func ListAuditRecords(limit int) ([]AuditRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := DB.Query(`SELECT id, timestamp, actor, action, target, before_json, after_json, reason, ip
						   FROM audit_records ORDER BY timestamp DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRecord
	for rows.Next() {
		var r AuditRecord
		var beforeJSON, afterJSON string
		if err := rows.Scan(&r.ID, &r.Timestamp, &r.Actor, &r.Action, &r.Target, &beforeJSON, &afterJSON, &r.Reason, &r.IP); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(beforeJSON), &r.Before)
		json.Unmarshal([]byte(afterJSON), &r.After)
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Add migration for audit_records table**

Edit `pkg/db/migrate.go`, append to `migrations`:

```go
	// v19: audit_records table for compliance and forensics.
	{
		Version:     19,
		Description: "Create audit_records table",
		SQL: `CREATE TABLE IF NOT EXISTS audit_records (
			id TEXT PRIMARY KEY,
			timestamp DATETIME NOT NULL,
			actor TEXT NOT NULL,
			action TEXT NOT NULL,
			target TEXT NOT NULL,
			before_json TEXT DEFAULT '{}',
			after_json TEXT DEFAULT '{}',
			reason TEXT DEFAULT '',
			ip TEXT DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_audit_records_actor ON audit_records(actor);
		CREATE INDEX IF NOT EXISTS idx_audit_records_target ON audit_records(target);
		CREATE INDEX IF NOT EXISTS idx_audit_records_timestamp ON audit_records(timestamp DESC);`,
	},
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/observability -run TestMemoryAuditor -v && go test ./pkg/db`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/observability/audit.go internal/observability/audit_test.go pkg/db/audit.go pkg/db/migrate.go
git commit -m "Phase 7-C: auditor interface + SQLite audit_records persistence"
```

---

## Task 3: Latency Histogram for /metrics

**Files:**
- Create: `internal/observability/histogram.go`
- Test: `internal/observability/histogram_test.go`
- Modify: `internal/observability/obs.go`

- [ ] **Step 1: Write the failing test**

Create `internal/observability/histogram_test.go`:

```go
package observability

import (
	"testing"
	"time"
)

func TestHistogramQuantiles(t *testing.T) {
	h := NewHistogramCollector([]float64{1, 10, 100, 1000}) // ms buckets
	for i := 0; i < 100; i++ {
		h.Record(time.Duration(i) * time.Millisecond)
	}
	p50 := h.Quantile(0.5)
	p95 := h.Quantile(0.95)
	if p50 < 45 || p50 > 55 {
		t.Fatalf("p50 out of range: %v", p50)
	}
	if p95 < 90 {
		t.Fatalf("p95 too low: %v", p95)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/observability -run TestHistogramQuantiles -v`

Expected: FAIL `undefined: NewHistogramCollector`

- [ ] **Step 3: Implement histogram**

Create `internal/observability/histogram.go`:

```go
package observability

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// HistogramCollector tracks latency samples in explicit buckets and supports
// Prometheus exposition without an external SDK.
type HistogramCollector struct {
	mu      sync.RWMutex
	buckets []float64 // upper bounds in ms
	counts  []uint64
	total   uint64
	sum     float64
}

// NewHistogramCollector creates a histogram. Buckets must be sorted ascending
// and are interpreted as milliseconds upper bounds.
func NewHistogramCollector(bucketsMS []float64) *HistogramCollector {
	b := make([]float64, len(bucketsMS))
	copy(b, bucketsMS)
	sort.Float64s(b)
	return &HistogramCollector{
		buckets: b,
		counts:  make([]uint64, len(b)),
	}
}

// Record adds a latency sample.
func (h *HistogramCollector) Record(d time.Duration) {
	ms := float64(d.Milliseconds())
	h.mu.Lock()
	defer h.mu.Unlock()
	h.total++
	h.sum += ms
	for i, upper := range h.buckets {
		if ms <= upper {
			h.counts[i]++
			return
		}
	}
	// If larger than all buckets, increment the last (overflow) bucket.
	if len(h.counts) > 0 {
		h.counts[len(h.counts)-1]++
	}
}

// Quantile returns an approximate latency in milliseconds for the given
// quantile (0-1). Linear interpolation between bucket boundaries.
func (h *HistogramCollector) Quantile(q float64) float64 {
	if q <= 0 || q > 1 {
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.total == 0 || len(h.buckets) == 0 {
		return 0
	}
	target := float64(h.total) * q
	var cumulative uint64
	for i, c := range h.counts {
		cumulative += c
		if float64(cumulative) >= target {
			prev := float64(0)
			if i > 0 {
				prev = h.buckets[i-1]
			}
			return prev + (h.buckets[i]-prev)*0.5
		}
	}
	return h.buckets[len(h.buckets)-1]
}

// PrometheusHistogram returns the bucket lines in Prometheus format.
func (h *HistogramCollector) PrometheusHistogram(name, help string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := fmt.Sprintf("# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	var cumulative uint64
	for i, upper := range h.buckets {
		cumulative += h.counts[i]
		out += fmt.Sprintf("%s_bucket{le=\"%.3f\"} %d\n", name, upper, cumulative)
	}
	out += fmt.Sprintf("%s_bucket{le=\"+Inf\"} %d\n", name, h.total)
	out += fmt.Sprintf("%s_sum %.3f\n", name, h.sum)
	out += fmt.Sprintf("%s_count %d\n", name, h.total)
	return out
}

func (h *HistogramCollector) Total() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.total
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/observability -run TestHistogramQuantiles -v`

Expected: PASS

- [ ] **Step 5: Wire histograms into MetricsCollector**

Edit `internal/observability/obs.go`:

Add fields to `MetricsCollector`:

```go
	llmLatencyHist      *HistogramCollector
	toolLatencyHist     *HistogramCollector
```

In `NewMetricsCollector`:

```go
func NewMetricsCollector() *MetricsCollector {
	buckets := []float64{10, 50, 100, 250, 500, 1000, 2500, 5000, 10000}
	return &MetricsCollector{
		llmLatencyHist:  NewHistogramCollector(buckets),
		toolLatencyHist: NewHistogramCollector(buckets),
	}
}
```

Add methods:

```go
// RecordLLMLatency records the latency of an LLM API call.
func (m *MetricsCollector) RecordLLMLatency(d time.Duration) {
	m.mu.Lock()
	m.llmLatencyHist.Record(d)
	m.mu.Unlock()
}

// RecordToolLatency records the latency of a tool execution.
func (m *MetricsCollector) RecordToolLatency(d time.Duration) {
	m.mu.Lock()
	m.toolLatencyHist.Record(d)
	m.mu.Unlock()
}
```

Update `PrometheusText` to append histograms (before final return):

```go
	out += m.llmLatencyHist.PrometheusHistogram("llm_latency_ms", "LLM call latency in milliseconds.")
	out += m.toolLatencyHist.PrometheusHistogram("tool_latency_ms", "Tool execution latency in milliseconds.")
```

- [ ] **Step 6: Commit**

```bash
git add internal/observability/histogram.go internal/observability/histogram_test.go internal/observability/obs.go
git commit -m "Phase 7-C: latency histograms in Prometheus text format"
```

---

## Task 4: Engine Instrumentation (trace spans + latency recording)

**Files:**
- Modify: `internal/runtime/engine.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Add Tracer and latency hooks to EngineConfig**

Edit `internal/runtime/engine.go` in the `EngineConfig` struct:

```go
	// Tracer produces dependency-free trace spans for every think/tool/llm step.
	// When nil, tracing is skipped.
	Tracer interface {
		StartRoot(taskID, operation string) *observability.TraceContext
		StartChild(parent *observability.TraceContext, operation string) *observability.TraceContext
		Finish(ctx *observability.TraceContext, err error)
		FinishWithAttributes(ctx *observability.TraceContext, err error, attrs map[string]any)
	}

	// LLMLatencyRecorder is called after each LLM call with the observed latency.
	LLMLatencyRecorder func(latency time.Duration)

	// ToolLatencyRecorder is called after each tool execution with the observed latency.
	ToolLatencyRecorder func(latency time.Duration)
```

Add imports for `time` and `github.com/anmingwei/multi-agent-platform/internal/observability`.

- [ ] **Step 2: Wrap think(), tool execution, and LLM call with spans**

In `internal/runtime/engine.go`, locate the `think()` method. At the start:

```go
var traceCtx *observability.TraceContext
if e.cfg.Tracer != nil && e.rootTraceCtx != nil {
	traceCtx = e.cfg.Tracer.StartChild(e.rootTraceCtx, "think")
}
```

After ChatStream returns, record the finish with attributes:

```go
var llmErr error
if err != nil {
	llmErr = err
}
if e.cfg.Tracer != nil && traceCtx != nil {
	attrs := map[string]any{"model": selectedModel, "provider": e.selectedModel}
	if routeDecision != nil {
		attrs["intent"] = routeDecision.Intent
		attrs["tier"] = routeDecision.Tier.String()
	}
	e.cfg.Tracer.FinishWithAttributes(traceCtx, llmErr, attrs)
}
```

Locate tool execution. In `executeTool` (or equivalent), wrap:

```go
start := time.Now()
// ... existing execution ...
if e.cfg.ToolLatencyRecorder != nil {
	e.cfg.ToolLatencyRecorder(time.Since(start))
}
```

Around provider ChatStream, wrap with latency recorder:

```go
start := time.Now()
content, usage, toolCalls, err := selectedProvider.ChatStream(req, onChunk)
if e.cfg.LLMLatencyRecorder != nil {
	e.cfg.LLMLatencyRecorder(time.Since(start))
}
```

- [ ] **Step 3: Wire tracer and recorders in main.go**

Edit `cmd/server/main.go`. Create a process-level tracer:

```go
tracer := observability.NewTracer(2000)
```

Add package-level registry for root trace contexts:

```go
var traceRegistry sync.Map
```

When building `runtime.EngineConfig`, add:

```go
		Tracer: tracer,
		LLMLatencyRecorder: func(latency time.Duration) {
			observability.DefaultMetrics.RecordLLMLatency(latency)
		},
		ToolLatencyRecorder: func(latency time.Duration) {
			observability.DefaultMetrics.RecordToolLatency(latency)
		},
```

Also create and store a root trace context for the task. In the task start handler:

```go
rootTraceCtx := tracer.StartRoot(taskID, "task")
traceRegistry.Store(taskID, rootTraceCtx)
```

Pass `RootTraceCtx: rootTraceCtx` in EngineConfig. Add field to `EngineConfig`:

```go
	RootTraceCtx *observability.TraceContext
```

In `NewEngine`, set `e.rootTraceCtx = cfg.RootTraceCtx`.

- [ ] **Step 4: Build and test**

Run: `go build ./cmd/server && go test ./internal/runtime`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/engine.go cmd/server/main.go
git commit -m "Phase 7-C: engine instrumentation with trace spans and latency histograms"
```

---

## Task 5: Audit Hooks for Write Operations

**Files:**
- Modify: `cmd/server/main.go`
- Modify: `internal/observability/obs.go`

- [ ] **Step 1: Add auditor singleton**

Edit `internal/observability/obs.go`. Add:

```go
// DefaultAuditor is the package-level shared auditor.
var DefaultAuditor Auditor = NewMemoryAuditor(10000)
```

- [ ] **Step 2: Add SQLite-backed auditor decorator**

Create `internal/observability/audit_sqlite.go`:

```go
package observability

import "github.com/anmingwei/multi-agent-platform/pkg/db"

// SQLiteAuditor wraps an Auditor and also persists to SQLite.
type SQLiteAuditor struct {
	inner Auditor
}

func NewSQLiteAuditor(inner Auditor) *SQLiteAuditor {
	return &SQLiteAuditor{inner: inner}
}

func (a *SQLiteAuditor) Record(rec AuditRecord) {
	a.inner.Record(rec)
	_ = db.InsertAuditRecord(db.AuditRecord{
		ID:        rec.ID,
		Timestamp: rec.Timestamp,
		Actor:     rec.Actor,
		Action:    rec.Action,
		Target:    rec.Target,
		Before:    rec.Before,
		After:     rec.After,
		Reason:    rec.Reason,
		IP:        rec.IP,
	})
}

func (a *SQLiteAuditor) List(limit int) []AuditRecord {
	return a.inner.List(limit)
}
```

- [ ] **Step 3: Wire auditor**

In `cmd/server/main.go`, after DB init:

```go
observability.DefaultAuditor = observability.NewSQLiteAuditor(observability.NewMemoryAuditor(10000))
```

- [ ] **Step 4: Add audit helper and hooks**

Add near top of `cmd/server/main.go`:

```go
func currentActor(r *http.Request) string {
	if r == nil {
		return "system"
	}
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if key == "" {
		return "anonymous"
	}
	if len(key) > 8 {
		key = key[:8]
	}
	return "apikey:" + key
}
```

Locate `DELETE /api/sessions/:id`, `DELETE /api/projects/:id`, `DELETE /api/memories/:id`, `PUT /api/memories/:id`, `POST /api/tools`. Add before/after audit blocks, e.g.

```go
observability.DefaultAuditor.Record(observability.AuditRecord{
	Actor:  currentActor(r),
	Action: "delete_session",
	Target: id,
	Before: map[string]any{"id": id},
	After:  map[string]any{"deleted": true},
})
```

- [ ] **Step 5: Build and run tests**

Run: `go build ./cmd/server && go test ./internal/observability ./pkg/db`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/observability/audit_sqlite.go internal/observability/obs.go cmd/server/main.go
git commit -m "Phase 7-C: audit hooks for write operations + SQLite persistence"
```

---

## Task 6: REST API for Traces, Audit, and Replay

**Files:**
- Modify: `cmd/server/main.go`
- Modify: `cmd/server/api.go`

- [ ] **Step 1: Add endpoints**

Edit `cmd/server/main.go`. In the API route setup block, add:

```go
	http.HandleFunc("/api/audit", handleAudit)
	http.HandleFunc("/api/traces", handleTraces)
	http.HandleFunc("/api/replay/tasks/", handleReplay)
```

- [ ] **Step 2: Implement handlers in api.go**

Edit `cmd/server/api.go`:

```go
func handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}
	records := observability.DefaultAuditor.List(limit)
	writeJSON(w, records)
}

func handleTraces(w http.ResponseWriter, r *http.Request) {
	// tracer is a process-level variable set in main.go
	data, _ := tracer.JSON()
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func handleReplay(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/replay/tasks/"), "/")
	taskID := parts[0]
	if taskID == "" {
		http.Error(w, "task_id required", http.StatusBadRequest)
		return
	}
	events := buildReplayEvents(taskID)
	writeJSON(w, events)
}
```

- [ ] **Step 3: Build replay helper**

Add to `cmd/server/api.go`:

```go
func buildReplayEvents(taskID string) []map[string]any {
	var events []map[string]any
	steps, _ := db.QueryStepsByTaskID(taskID)
	for _, s := range steps {
		events = append(events, map[string]any{
			"type":        s.Type,
			"task_id":     s.TaskID,
			"agent_id":    s.AgentID,
			"step_index":  s.StepIndex,
			"content":     s.Content,
			"tool_name":   s.ToolName,
			"tool_input":  s.ToolInput,
			"tool_output": s.ToolOutput,
			"timestamp":   s.CreatedAt.UnixMilli(),
		})
	}
	convs, _ := db.QueryConversationsByTaskID(taskID)
	for _, c := range convs {
		events = append(events, map[string]any{
			"type":      c.Role + "_message",
			"task_id":   c.TaskID,
			"content":   c.Content,
			"timestamp": c.CreatedAt.UnixMilli(),
		})
	}
	return events
}
```

- [ ] **Step 4: Build and test**

Run: `go build ./cmd/server`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/server/main.go cmd/server/api.go
git commit -m "Phase 7-C: REST endpoints for audit, traces, and replay"
```

---

## Task 7: Frontend Trace Tree Panel

**Files:**
- Create: `web/src/components/TraceTreePanel.vue`
- Create: `web/src/composables/useTraceStore.ts`
- Create: `web/src/components/TraceNode.vue`
- Modify: `web/src/App.vue`

- [ ] **Step 1: Create trace store**

Create `web/src/composables/useTraceStore.ts`:

```typescript
import { ref } from 'vue'
import type { Event } from '@/types/events'

export interface SpanNode {
  trace_id: string
  span_id: string
  parent_span_id?: string
  operation: string
  agent_id: string
  duration_ms: number
  status: string
  attributes?: Record<string, any>
  children: SpanNode[]
}

const spans = ref<SpanNode[]>([])

function buildTree(flat: SpanNode[]): SpanNode[] {
  const map = new Map<string, SpanNode>()
  const roots: SpanNode[] = []
  flat.forEach(node => {
    node.children = []
    map.set(node.span_id, node)
  })
  flat.forEach(node => {
    if (node.parent_span_id && map.has(node.parent_span_id)) {
      map.get(node.parent_span_id)!.children.push(node)
    } else if (!node.parent_span_id) {
      roots.push(node)
    }
  })
  return roots
}

export function useTraceStore() {
  function onEvent(evt: Event) {
    if (evt.type !== 'trace_span') return
    const node = evt.data as SpanNode
    spans.value.push(node)
    spans.value = buildTree(spans.value.slice(-1000))
  }
  return { spans, onEvent }
}
```

- [ ] **Step 2: Create TraceNode.vue**

Create `web/src/components/TraceNode.vue`:

```vue
<template>
  <li class="trace-node">
    <span :class="['badge', node.status]">{{ node.status }}</span>
    <span class="op">{{ node.operation }}</span>
    <span class="dur">{{ node.duration_ms }}ms</span>
    <ul v-if="node.children?.length">
      <TraceNode v-for="child in node.children" :key="child.span_id" :node="child" />
    </ul>
  </li>
</template>

<script setup lang="ts">
import type { SpanNode } from '@/composables/useTraceStore'
defineProps<{ node: SpanNode }>()
</script>
```

- [ ] **Step 3: Create TraceTreePanel.vue**

Create `web/src/components/TraceTreePanel.vue`:

```vue
<template>
  <div class="trace-tree-panel">
    <h3>Trace Tree</h3>
    <ul class="trace-list">
      <TraceNode v-for="node in spans" :key="node.span_id" :node="node" />
    </ul>
  </div>
</template>

<script setup lang="ts">
import { useTraceStore } from '@/composables/useTraceStore'
import TraceNode from './TraceNode.vue'

const { spans } = useTraceStore()
</script>
```

- [ ] **Step 4: Mount in App.vue**

Edit `web/src/App.vue`. Import and register trace store event handler:

```typescript
import { useTraceStore } from '@/composables/useTraceStore'
const traceStore = useTraceStore()
```

In the event router switch, call `traceStore.onEvent(evt)`.

- [ ] **Step 5: Build frontend**

Run: `cd web && npm run build`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add web/src/components/TraceTreePanel.vue web/src/components/TraceNode.vue web/src/composables/useTraceStore.ts web/src/App.vue
git commit -m "Phase 7-C: frontend trace tree panel"
```

---

## Task 8: Roadmap and Documentation Update

**Files:**
- Modify: `roadmaps/ROADMAP.md`
- Modify: `.env.example`
- This plan file

- [ ] **Step 1: Mark 7-C items complete**

Edit `roadmaps/ROADMAP.md` Phase 7-C section:

```markdown
### 7-C 深度可观测
- [x] OpenTelemetry trace: 跨 Agent / Tool / LLM 调用链路 span (dependency-free Tracer)
- [x] Prometheus 延迟直方图: `llm_latency_ms`, `tool_latency_ms` 添加至 `/metrics`
- [x] 审计日志: 写操作记录 actor / target / before / after，SQLite 持久化
- [x] 多 Agent trace 树可视化: 前端 `TraceTreePanel.vue`
- [x] 事件回放: `/api/replay/tasks/{task_id}` 从 steps + conversations 重建
```

- [ ] **Step 2: Update .env.example**

Append:

```text
# ---------------------------------------------------------------------------
# Observability (Phase 7-C)
# ---------------------------------------------------------------------------
# LOG_LEVEL=info
# AUDIT_BUFFER_LIMIT=10000
# TRACE_BUFFER_LIMIT=2000
```

- [ ] **Step 3: Full verification**

Run:

```bash
go build ./cmd/server
go test ./internal/observability ./pkg/db ./internal/runtime
cd web && npm run build
```

Expected: PASS

- [ ] **Step 4: Final commit**

```bash
git add roadmaps/ROADMAP.md .env.example docs/superpowers/plans/2026-07-18-phase-7c-observability.md
git commit -m "Phase 7-C: roadmap update and docs"
```

---

## Self-Review

1. **Spec coverage:**
   - OpenTelemetry trace ✅ Task 1-4
   - 延迟直方图 ✅ Task 3
   - 审计日志 ✅ Task 2 + 5
   - trace 树可视化 ✅ Task 7
   - 事件回放 ✅ Task 6

2. **Placeholder scan:** 无 TBD/TODO；所有步骤含代码与命令。

3. **Type一致性:**
   - `TraceContext` / `SpanRecord` / `Tracer` 名称在 trace.go、engine.go、main.go 一致。
   - `AuditRecord` 在 `observability` 和 `pkg/db` 中字段映射一致。
   - `HistogramCollector.PrometheusHistogram` 签名与 `MetricsCollector` 调用一致。

4. **边界考虑:**
   - 不依赖 OTel / Prometheus SDK，保持零外部依赖。
   - `Tracer` 使用有界缓冲区，避免 OOM。
   - `SQLiteAuditor` 写失败静默忽略，不阻断业务。
   - EngineConfig 中 tracer 为 interface，测试可注入 mock。
