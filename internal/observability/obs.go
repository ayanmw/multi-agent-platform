// Package observability 为平台提供结构化日志与 metric 指标。
//
// Phase 6-D：本 package 刻意避免引入 Prometheus client SDK 或 OpenTelemetry 等
// 外部依赖。metric 以简单的 counter 形式保存，并以 Prometheus 文本格式输出，
// 运维方无需新增二进制或库即可直接抓取。
package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// --- Logger -----------------------------------------------------------------

// LogLevel 表示一条日志的严重级别。
type LogLevel string

const (
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
	LevelFatal LogLevel = "fatal"
)

// StructuredLogger 提供带级别过滤的结构化（JSON）日志。
type StructuredLogger struct {
	mu     sync.Mutex
	output *log.Logger
	level  LogLevel
}

// NewStructuredLogger 创建一个向 os.Stdout 输出 JSON、级别为 Info 的 logger。
func NewStructuredLogger() *StructuredLogger {
	return &StructuredLogger{
		output: log.New(os.Stdout, "", 0),
		level:  LevelInfo,
	}
}

// SetLevel 调整最低日志级别。
func (l *StructuredLogger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// SetOutput 替换 structured logger 底层的 writer。
// 通常在启动阶段打开日志文件后调用，以便通过 io.MultiWriter 将日志
// 同时写入控制台和持久化文件。
func (l *StructuredLogger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.output = log.New(w, "", 0)
}

// Log 在级别通过过滤时输出一条结构化日志。
func (l *StructuredLogger) Log(level LogLevel, component, msg string, fields map[string]any) {
	l.mu.Lock()
	minLevel := l.level
	l.mu.Unlock()

	if !levelEnabled(level, minLevel) {
		return
	}

	entry := map[string]any{
		"ts":        time.Now().UTC().Format(time.RFC3339Nano),
		"level":     level,
		"component": component,
		"msg":       msg,
	}
	for k, v := range fields {
		entry[k] = v
	}

	data, _ := json.Marshal(entry)
	l.output.Println(string(data))
}

// Debug 输出 debug 级别的结构化日志。
func (l *StructuredLogger) Debug(component, msg string, fields map[string]any) {
	l.Log(LevelDebug, component, msg, fields)
}

// Info 输出 info 级别的结构化日志。
func (l *StructuredLogger) Info(component, msg string, fields map[string]any) {
	l.Log(LevelInfo, component, msg, fields)
}

// Warn 输出 warn 级别的结构化日志。
func (l *StructuredLogger) Warn(component, msg string, fields map[string]any) {
	l.Log(LevelWarn, component, msg, fields)
}

// Error 输出 error 级别的结构化日志。
func (l *StructuredLogger) Error(component, msg string, fields map[string]any) {
	l.Log(LevelError, component, msg, fields)
}

// Infof 以 fmt 风格格式化输出一条 info 日志。
func (l *StructuredLogger) Infof(component, format string, args ...any) {
	l.Log(LevelInfo, component, fmt.Sprintf(format, args...), nil)
}

// Warnf 以 fmt 风格格式化输出一条 warn 日志。
func (l *StructuredLogger) Warnf(component, format string, args ...any) {
	l.Log(LevelWarn, component, fmt.Sprintf(format, args...), nil)
}

// Errorf 以 fmt 风格格式化输出一条 error 日志。
func (l *StructuredLogger) Errorf(component, format string, args ...any) {
	l.Log(LevelError, component, fmt.Sprintf(format, args...), nil)
}

func levelEnabled(level, minLevel LogLevel) bool {
	order := map[LogLevel]int{
		LevelDebug: 0, LevelInfo: 1, LevelWarn: 2, LevelError: 3, LevelFatal: 4,
	}
	return order[level] >= order[minLevel]
}

// ParseLogLevel 将字符串转换为 LogLevel。无法识别的值会回退为 Info，
// 以避免配置中一个拼写错误就让所有日志静默。
func ParseLogLevel(s string) LogLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	case "fatal":
		return LevelFatal
	default:
		return LevelInfo
	}
}

// --- Metrics ----------------------------------------------------------------

// MetricsCollector 保存用于 observability 的简单线程安全计数器。
// 所有 counter 都是单调递增的 uint64 值，以 Prometheus exposition 格式输出。
type MetricsCollector struct {
	mu              sync.RWMutex
	tasksStarted    uint64
	tasksCompleted  uint64
	tasksFailed     uint64
	llmCalls        uint64
	llmInputTokens  uint64
	llmOutputTokens uint64
	llmTotalTokens  uint64
	costCents       int64
	llmLatencyHist  *HistogramCollector
	toolLatencyHist *HistogramCollector
}

// NewMetricsCollector 返回一个零值的 metric 收集器。
func NewMetricsCollector() *MetricsCollector {
	buckets := []float64{10, 50, 100, 250, 500, 1000, 2500, 5000, 10000}
	return &MetricsCollector{
		llmLatencyHist:  NewHistogramCollector(buckets),
		toolLatencyHist: NewHistogramCollector(buckets),
	}
}

// IncrTasksStarted 递增已启动 agent task 的计数器。
func (m *MetricsCollector) IncrTasksStarted() {
	m.mu.Lock()
	m.tasksStarted++
	m.mu.Unlock()
}

// IncrTasksCompleted 递增成功完成 task 的计数器。
func (m *MetricsCollector) IncrTasksCompleted() {
	m.mu.Lock()
	m.tasksCompleted++
	m.mu.Unlock()
}

// IncrTasksFailed 递增失败或被取消 task 的计数器。
func (m *MetricsCollector) IncrTasksFailed() {
	m.mu.Lock()
	m.tasksFailed++
	m.mu.Unlock()
}

// RecordLLMCall 递增 LLM 调用计数器并累加 token 使用量。
func (m *MetricsCollector) RecordLLMCall(inputTokens, outputTokens, totalTokens uint64) {
	m.mu.Lock()
	m.llmCalls++
	m.llmInputTokens += inputTokens
	m.llmOutputTokens += outputTokens
	m.llmTotalTokens += totalTokens
	m.mu.Unlock()
}

// RecordCost 将以分为单位的成本累加到总成本计数器。
func (m *MetricsCollector) RecordCost(cents int64) {
	m.mu.Lock()
	m.costCents += cents
	m.mu.Unlock()
}

// RecordLLMLatency 记录一次 LLM API 调用的延迟。
func (m *MetricsCollector) RecordLLMLatency(d time.Duration) {
	m.mu.Lock()
	m.llmLatencyHist.Record(d)
	m.mu.Unlock()
}

// RecordToolLatency 记录一次 tool 执行的延迟。
func (m *MetricsCollector) RecordToolLatency(d time.Duration) {
	m.mu.Lock()
	m.toolLatencyHist.Record(d)
	m.mu.Unlock()
}

// PrometheusText 以 Prometheus exposition 格式返回当前 metric。
// 适合由 Prometheus、Grafana Agent 或 curl 抓取。
func (m *MetricsCollector) PrometheusText() string {
	m.mu.RLock()
	ts := uint64(time.Now().UnixMilli())
	out := fmt.Sprintf(`# HELP agent_tasks_total Total number of agent tasks by final state.
# TYPE agent_tasks_total counter
agent_tasks_total{state="started"} %d %d
agent_tasks_total{state="completed"} %d %d
agent_tasks_total{state="failed"} %d %d
# HELP llm_calls_total Total number of LLM API calls.
# TYPE llm_calls_total counter
llm_calls_total %d %d
# HELP llm_tokens_total Total number of LLM tokens consumed.
# TYPE llm_tokens_total counter
llm_tokens_total{direction="input"} %d %d
llm_tokens_total{direction="output"} %d %d
llm_tokens_total{direction="total"} %d %d
# HELP cost_cents_total Total LLM cost in integer cents.
# TYPE cost_cents_total counter
cost_cents_total %d %d
`,
		m.tasksStarted, ts,
		m.tasksCompleted, ts,
		m.tasksFailed, ts,
		m.llmCalls, ts,
		m.llmInputTokens, ts,
		m.llmOutputTokens, ts,
		m.llmTotalTokens, ts,
		m.costCents, ts,
	)
	out += m.llmLatencyHist.PrometheusHistogram("llm_latency_ms", "LLM call latency in milliseconds.")
	out += m.toolLatencyHist.PrometheusHistogram("tool_latency_ms", "Tool execution latency in milliseconds.")
	m.mu.RUnlock()
	return out
}

// DefaultMetrics 是 package 级别共享的 metric 收集器。
var DefaultMetrics = NewMetricsCollector()

// DefaultLogger 是 package 级别共享的 logger。
var DefaultLogger = NewStructuredLogger()

// DefaultAuditor 是 package 级别共享的 auditor。
var DefaultAuditor Auditor = NewMemoryAuditor(10000)
