// Package observability provides structured logging and metrics for the platform.
//
// Phase 6-D: This package intentionally avoids external dependencies such as
// Prometheus client SDK or OpenTelemetry. Metrics are kept as simple counters
// exposed in Prometheus text format so operators can scrape them without
// introducing new binaries or libraries.
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

// LogLevel represents the severity of a log entry.
type LogLevel string

const (
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
	LevelFatal LogLevel = "fatal"
)

// StructuredLogger provides structured (JSON) logging with level filtering.
type StructuredLogger struct {
	mu     sync.Mutex
	output *log.Logger
	level  LogLevel
}

// NewStructuredLogger creates a logger writing JSON to os.Stdout at Info level.
func NewStructuredLogger() *StructuredLogger {
	return &StructuredLogger{
		output: log.New(os.Stdout, "", 0),
		level:  LevelInfo,
	}
}

// SetLevel changes the minimum log level.
func (l *StructuredLogger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// SetOutput replaces the underlying writer of the structured logger.
// This is typically called at startup after opening a log file so that
// logs go to both the console and a persistent file via io.MultiWriter.
func (l *StructuredLogger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.output = log.New(w, "", 0)
}

// Log emits a structured log entry if the level passes the filter.
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

// Debug logs a debug-level structured message.
func (l *StructuredLogger) Debug(component, msg string, fields map[string]any) {
	l.Log(LevelDebug, component, msg, fields)
}

// Info logs an info-level structured message.
func (l *StructuredLogger) Info(component, msg string, fields map[string]any) {
	l.Log(LevelInfo, component, msg, fields)
}

// Warn logs a warning-level structured message.
func (l *StructuredLogger) Warn(component, msg string, fields map[string]any) {
	l.Log(LevelWarn, component, msg, fields)
}

// Error logs an error-level structured message.
func (l *StructuredLogger) Error(component, msg string, fields map[string]any) {
	l.Log(LevelError, component, msg, fields)
}

// Infof logs an info message with fmt-style formatting.
func (l *StructuredLogger) Infof(component, format string, args ...any) {
	l.Log(LevelInfo, component, fmt.Sprintf(format, args...), nil)
}

// Warnf logs a warning message with fmt-style formatting.
func (l *StructuredLogger) Warnf(component, format string, args ...any) {
	l.Log(LevelWarn, component, fmt.Sprintf(format, args...), nil)
}

// Errorf logs an error message with fmt-style formatting.
func (l *StructuredLogger) Errorf(component, format string, args ...any) {
	l.Log(LevelError, component, fmt.Sprintf(format, args...), nil)
}

func levelEnabled(level, minLevel LogLevel) bool {
	order := map[LogLevel]int{
		LevelDebug: 0, LevelInfo: 1, LevelWarn: 2, LevelError: 3, LevelFatal: 4,
	}
	return order[level] >= order[minLevel]
}

// ParseLogLevel converts a string to a LogLevel. Unrecognized values default
// to Info so a typo in configuration does not silence all logs.
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

// MetricsCollector holds simple thread-safe counters for observability.
// All counters are monotonically increasing uint64 values rendered in
// Prometheus exposition format.
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
}

// NewMetricsCollector returns a zero-valued metrics collector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{}
}

// IncrTasksStarted increments the counter for agent tasks that have started.
func (m *MetricsCollector) IncrTasksStarted() {
	m.mu.Lock()
	m.tasksStarted++
	m.mu.Unlock()
}

// IncrTasksCompleted increments the counter for tasks that finished successfully.
func (m *MetricsCollector) IncrTasksCompleted() {
	m.mu.Lock()
	m.tasksCompleted++
	m.mu.Unlock()
}

// IncrTasksFailed increments the counter for tasks that failed or were cancelled.
func (m *MetricsCollector) IncrTasksFailed() {
	m.mu.Lock()
	m.tasksFailed++
	m.mu.Unlock()
}

// RecordLLMCall increments the LLM call counter and adds the token usage.
func (m *MetricsCollector) RecordLLMCall(inputTokens, outputTokens, totalTokens uint64) {
	m.mu.Lock()
	m.llmCalls++
	m.llmInputTokens += inputTokens
	m.llmOutputTokens += outputTokens
	m.llmTotalTokens += totalTokens
	m.mu.Unlock()
}

// RecordCost adds the cost in cents to the total cost counter.
func (m *MetricsCollector) RecordCost(cents int64) {
	m.mu.Lock()
	m.costCents += cents
	m.mu.Unlock()
}

// PrometheusText returns the current metrics in Prometheus exposition format.
// This is suitable for scraping by Prometheus, Grafana Agent, or curl.
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
	m.mu.RUnlock()
	return out
}

// DefaultMetrics is the package-level shared metrics collector.
var DefaultMetrics = NewMetricsCollector()

// DefaultLogger is the package-level shared logger.
var DefaultLogger = NewStructuredLogger()
