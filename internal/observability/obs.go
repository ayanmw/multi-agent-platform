// Package observability 为平台提供结构化日志与 metric 指标。
//
// Phase 8：日志库从 stdlib log 替换为 log/slog（标准库），支持多 sink
// 独立级别与格式、lumberjack 文件轮转、caller 注入、7 级日志。
// metric 仍以简单 counter + histogram 形式保存，以 Prometheus 文本格式输出。
package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// ===========================================================================
// 日志级别
// ===========================================================================

// LogLevel 表示一条日志的严重级别。Phase 8 扩展为 7 级。
type LogLevel string

const (
	LevelTrace  LogLevel = "trace"  // 新增：ReAct 逐步骤、MCP 逐消息
	LevelDebug  LogLevel = "debug"
	LevelInfo   LogLevel = "info"
	LevelWarn   LogLevel = "warn"
	LevelError  LogLevel = "error"
	LevelFatal  LogLevel = "fatal"  // 记录后 os.Exit(1)
	LevelPanic  LogLevel = "panic"  // 新增：记录后 panic
)

// slogLevelMap 将自定义级别映射到 slog.Level。
// TRACE 映射到比 Debug 更低的级别，FATAL / PANIC 映射到比 Error 更高的级别。
var slogLevelMap = map[LogLevel]slog.Level{
	LevelTrace:  slog.LevelDebug - 4,
	LevelDebug:  slog.LevelDebug,
	LevelInfo:   slog.LevelInfo,
	LevelWarn:   slog.LevelWarn,
	LevelError:  slog.LevelError,
	LevelFatal:  slog.LevelError + 4,
	LevelPanic:  slog.LevelError + 8,
}

// levelOrder 用于级别比较。
var levelOrder = map[LogLevel]int{
	LevelTrace: 0, LevelDebug: 1, LevelInfo: 2, LevelWarn: 3,
	LevelError: 4, LevelFatal: 5, LevelPanic: 6,
}

func levelEnabled(level, minLevel LogLevel) bool {
	return levelOrder[level] >= levelOrder[minLevel]
}

// ParseLogLevel 将字符串转换为 LogLevel。无法识别的值会回退为 Info，
// 以避免配置中一个拼写错误就让所有日志静默。
func ParseLogLevel(s string) LogLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return LevelTrace
	case "debug":
		return LevelDebug
	case "info", "":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	case "fatal":
		return LevelFatal
	case "panic":
		return LevelPanic
	default:
		return LevelInfo
	}
}

// ===========================================================================
// Logger — slog 封装，多 sink 独立级别与格式
// ===========================================================================

// LogConfig 配置多 sink 日志输出。
type LogConfig struct {
	ConsoleLevel LogLevel // 控制台最低级别
	FileLevel    LogLevel // 文件最低级别
	FilePath     string   // 日志文件路径，空则不写文件
	// lumberjack 轮转参数
	MaxSizeMB  int  // 单文件最大 MB，默认 100
	MaxBackups int  // 保留旧文件数，默认 7
	MaxAgeDays int  // 保留天数，默认 30
	Compress   bool // 是否 gzip 压缩旧文件
	AddSource  bool // 是否注入 caller file:line
}

// Logger 是全平台统一的日志器，封装 slog 并提供：
//   - 7 级日志（Trace/Debug/Info/Warn/Error/Fatal/Panic）
//   - 多 sink 独立级别与格式（控制台 Text + 文件 JSON）
//   - 结构化 fields（slog.Attr）与旧签名（component, msg, map）双 API
//   - caller 信息自动注入
//   - lumberjack 文件轮转
type Logger struct {
	mu      sync.RWMutex
	inner   *slog.Logger
	level   LogLevel
	closers []io.Closer
}

// NewLogger 根据 LogConfig 构建多 sink Logger。
func NewLogger(cfg LogConfig) *Logger {
	var handlers []slog.Handler

	// --- Console sink: Text 格式，人类可读 ---
	consoleOpts := &slog.HandlerOptions{
		Level:     slogLevelMap[cfg.ConsoleLevel],
		AddSource: cfg.AddSource,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.SourceKey {
				if src, ok := a.Value.Any().(*slog.Source); ok && src != nil {
					return slog.String("caller",
						fmt.Sprintf("%s:%d", filepath.Base(src.File), src.Line))
				}
			}
			return a
		},
	}
	handlers = append(handlers, slog.NewTextHandler(os.Stdout, consoleOpts))

	// --- File sink: JSON 格式，机器采集 + lumberjack 轮转 ---
	var closers []io.Closer
	if cfg.FilePath != "" {
		lj := &lumberjack.Logger{
			Filename:   cfg.FilePath,
			MaxSize:    defaultInt(cfg.MaxSizeMB, 100),
			MaxBackups: defaultInt(cfg.MaxBackups, 7),
			MaxAge:     defaultInt(cfg.MaxAgeDays, 30),
			Compress:   cfg.Compress,
			LocalTime:  true,
		}
		closers = append(closers, lj)
		fileOpts := &slog.HandlerOptions{
			Level:     slogLevelMap[cfg.FileLevel],
			AddSource: cfg.AddSource,
		}
		handlers = append(handlers, slog.NewJSONHandler(lj, fileOpts))
	}

	multi := &multiHandler{handlers: handlers}
	return &Logger{
		inner:   slog.New(multi),
		level:   cfg.ConsoleLevel,
		closers: closers,
	}
}

// Close 关闭所有底层资源（如 lumberjack 文件句柄）。
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var firstErr error
	for _, c := range l.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SetLevel 调整最低日志级别（影响内部记录的全局级别判断）。
func (l *Logger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// --- 7 级新 API（slog.Attr 风格）---

func (l *Logger) logAttrs(level LogLevel, msg string, attrs ...slog.Attr) {
	l.mu.RLock()
	slevel := slogLevelMap[level]
	l.mu.RUnlock()
	l.inner.LogAttrs(context.Background(), slevel, msg, attrs...)
}

// Trace 输出 trace 级别日志。
func (l *Logger) Trace(msg string, attrs ...slog.Attr) {
	l.logAttrs(LevelTrace, msg, attrs...)
}

// Debug 输出 debug 级别日志。
func (l *Logger) Debug(msg string, attrs ...slog.Attr) {
	l.logAttrs(LevelDebug, msg, attrs...)
}

// InfoMsg 输出 info 级别日志（新签名）。
func (l *Logger) InfoMsg(msg string, attrs ...slog.Attr) {
	l.logAttrs(LevelInfo, msg, attrs...)
}

// WarnMsg 输出 warn 级别日志（新签名）。
func (l *Logger) WarnMsg(msg string, attrs ...slog.Attr) {
	l.logAttrs(LevelWarn, msg, attrs...)
}

// ErrorMsg 输出 error 级别日志（新签名）。
func (l *Logger) ErrorMsg(msg string, attrs ...slog.Attr) {
	l.logAttrs(LevelError, msg, attrs...)
}

// Fatal 输出 fatal 级别日志后调用 os.Exit(1)。
func (l *Logger) Fatal(msg string, attrs ...slog.Attr) {
	l.logAttrs(LevelFatal, msg, attrs...)
	os.Exit(1)
}

// Panic 输出 panic 级别日志后 panic。
func (l *Logger) Panic(msg string, attrs ...slog.Attr) {
	l.logAttrs(LevelPanic, msg, attrs...)
	panic(msg)
}

// --- fmt 风格便捷方法 ---

func (l *Logger) Tracef(format string, args ...any) {
	l.logAttrs(LevelTrace, fmt.Sprintf(format, args...))
}
func (l *Logger) Debugf(format string, args ...any) {
	l.logAttrs(LevelDebug, fmt.Sprintf(format, args...))
}
func (l *Logger) Infof2(format string, args ...any) {
	l.logAttrs(LevelInfo, fmt.Sprintf(format, args...))
}
func (l *Logger) Warnf2(format string, args ...any) {
	l.logAttrs(LevelWarn, fmt.Sprintf(format, args...))
}
func (l *Logger) Errorf2(format string, args ...any) {
	l.logAttrs(LevelError, fmt.Sprintf(format, args...))
}
func (l *Logger) Fatalf(format string, args ...any) {
	l.logAttrs(LevelFatal, fmt.Sprintf(format, args...))
	os.Exit(1)
}

// --- 便捷 Attr 构造函数 ---

// AttrS 构造一个 string Attr。
func AttrS(key, val string) slog.Attr { return slog.String(key, val) }

// AttrI 构造一个 int Attr。
func AttrI(key string, val int) slog.Attr { return slog.Int(key, val) }

// AttrI64 构造一个 int64 Attr。
func AttrI64(key string, val int64) slog.Attr { return slog.Int64(key, val) }

// AttrAny 构造一个任意类型 Attr。
func AttrAny(key string, val any) slog.Attr { return slog.Any(key, val) }

// AttrErr 构造一个 error Attr。
func AttrErr(err error) slog.Attr { return slog.String("error", err.Error()) }

// AttrDur 构造一个 duration Attr（字符串形式）。
func AttrDur(key string, val time.Duration) slog.Attr { return slog.String(key, val.String()) }

// ===========================================================================
// StructuredLogger — 旧 API 兼容层，委托给 Logger
// ===========================================================================

// StructuredLogger 保留旧签名 (component, msg, fields map[string]any) 的方法，
// 内部委托给 slog-based Logger。现有调用点无需修改即可获得多 sink + 轮转 + caller。
//
// Deprecated: 新代码应直接使用 Logger 的 Trace/Debug/InfoMsg/WarnMsg/ErrorMsg 方法。
type StructuredLogger struct {
	inner *Logger
}

// NewStructuredLogger 创建一个仅控制台输出的兼容 logger（级别 Info）。
func NewStructuredLogger() *StructuredLogger {
	return &StructuredLogger{
		inner: NewLogger(LogConfig{
			ConsoleLevel: LevelInfo,
			AddSource:    false,
		}),
	}
}

// Inner 返回底层的新 Logger，供需要新 API 的调用方使用。
func (l *StructuredLogger) Inner() *Logger {
	return l.inner
}

// SetLevel 调整最低日志级别。
//
// Deprecated: 使用 NewLogger(cfg) 在初始化时配置级别。
func (l *StructuredLogger) SetLevel(level LogLevel) {
	l.inner.SetLevel(level)
}

// SetOutput 替换底层 writer。
//
// Deprecated: 使用 NewLogger(cfg) 配置文件输出。此方法仅在未使用 NewLogger
// 初始化时有效，会重建一个单 sink logger。
func (l *StructuredLogger) SetOutput(w io.Writer) {
	l.inner.mu.Lock()
	defer l.inner.mu.Unlock()
	opts := &slog.HandlerOptions{
		Level: slogLevelMap[l.inner.level],
	}
	l.inner.inner = slog.New(slog.NewJSONHandler(w, opts))
}

// Log 在级别通过过滤时输出一条结构化日志（旧签名兼容）。
func (l *StructuredLogger) Log(level LogLevel, component, msg string, fields map[string]any) {
	attrs := mapToAttrs(fields)
	attrs = append(attrs, slog.String("component", component))
	l.inner.logAttrs(level, msg, attrs...)
}

// Debug 输出 debug 级别的结构化日志（旧签名）。
func (l *StructuredLogger) Debug(component, msg string, fields map[string]any) {
	l.Log(LevelDebug, component, msg, fields)
}

// Info 输出 info 级别的结构化日志（旧签名）。
func (l *StructuredLogger) Info(component, msg string, fields map[string]any) {
	l.Log(LevelInfo, component, msg, fields)
}

// Warn 输出 warn 级别的结构化日志（旧签名）。
func (l *StructuredLogger) Warn(component, msg string, fields map[string]any) {
	l.Log(LevelWarn, component, msg, fields)
}

// Error 输出 error 级别的结构化日志（旧签名）。
func (l *StructuredLogger) Error(component, msg string, fields map[string]any) {
	l.Log(LevelError, component, msg, fields)
}

// Infof 以 fmt 风格格式化输出一条 info 日志（旧签名）。
func (l *StructuredLogger) Infof(component, format string, args ...any) {
	l.Log(LevelInfo, component, fmt.Sprintf(format, args...), nil)
}

// Warnf 以 fmt 风格格式化输出一条 warn 日志（旧签名）。
func (l *StructuredLogger) Warnf(component, format string, args ...any) {
	l.Log(LevelWarn, component, fmt.Sprintf(format, args...), nil)
}

// Errorf 以 fmt 风格格式化输出一条 error 日志（旧签名）。
func (l *StructuredLogger) Errorf(component, format string, args ...any) {
	l.Log(LevelError, component, fmt.Sprintf(format, args...), nil)
}

// --- 新级别方法（透传到 inner Logger）---

// Trace 输出 trace 级别日志。
func (l *StructuredLogger) Trace(msg string, attrs ...slog.Attr) {
	l.inner.logAttrs(LevelTrace, msg, attrs...)
}

// Tracef 以 fmt 风格输出 trace 日志。
func (l *StructuredLogger) Tracef(format string, args ...any) {
	l.inner.logAttrs(LevelTrace, fmt.Sprintf(format, args...))
}

// Fatal 输出 fatal 级别日志后 os.Exit(1)。
func (l *StructuredLogger) Fatal(msg string, attrs ...slog.Attr) {
	l.inner.Fatal(msg, attrs...)
}

// Fatalf 以 fmt 风格输出 fatal 日志后 os.Exit(1)。
func (l *StructuredLogger) Fatalf(format string, args ...any) {
	l.inner.Fatalf(format, args...)
}

// Panic 输出 panic 级别日志后 panic。
func (l *StructuredLogger) Panic(msg string, attrs ...slog.Attr) {
	l.inner.Panic(msg, attrs...)
}

// mapToAttrs 将 map[string]any 转换为 slog.Attr 切片。
func mapToAttrs(m map[string]any) []slog.Attr {
	if len(m) == 0 {
		return nil
	}
	attrs := make([]slog.Attr, 0, len(m))
	for k, v := range m {
		attrs = append(attrs, slog.Any(k, v))
	}
	return attrs
}

func defaultInt(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// ===========================================================================
// Metrics
// ===========================================================================

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
// Phase 8 (P8)：LLM 和 Tool 使用不同的 bucket 上界，
// LLM bucket 扩展到 120s 以覆盖长尾延迟。
func NewMetricsCollector() *MetricsCollector {
	llmBuckets := []float64{50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000, 120000}
	toolBuckets := []float64{10, 50, 100, 250, 500, 1000, 5000, 10000}
	return &MetricsCollector{
		llmLatencyHist:  NewHistogramCollector(llmBuckets),
		toolLatencyHist: NewHistogramCollector(toolBuckets),
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

// SeedTaskCounts 设置 counter 初值（用于启动时从 DB 回填，P7）。
func (m *MetricsCollector) SeedTaskCounts(started, completed, failed uint64) {
	m.mu.Lock()
	m.tasksStarted = started
	m.tasksCompleted = completed
	m.tasksFailed = failed
	m.mu.Unlock()
}

// SeedLLMUsage 设置 LLM 使用量初值（用于启动时从 DB 回填，P7）。
func (m *MetricsCollector) SeedLLMUsage(calls, inputTokens, outputTokens, totalTokens uint64, costCents int64) {
	m.mu.Lock()
	m.llmCalls = calls
	m.llmInputTokens = inputTokens
	m.llmOutputTokens = outputTokens
	m.llmTotalTokens = totalTokens
	m.costCents = costCents
	m.mu.Unlock()
}

// metricSnapshot 是 PrometheusText 锁内快照的结构（P9）。
type metricSnapshot struct {
	tasksStarted    uint64
	tasksCompleted  uint64
	tasksFailed     uint64
	llmCalls        uint64
	llmInputTokens  uint64
	llmOutputTokens uint64
	llmTotalTokens  uint64
	costCents       int64
}

// PrometheusText 以 Prometheus exposition 格式返回当前 metric。
// Phase 8 (P9)：先在锁内快照所有数值，再在锁外格式化，减少锁持有时间。
func (m *MetricsCollector) PrometheusText() string {
	// 1. 锁内快照
	m.mu.RLock()
	snap := metricSnapshot{
		tasksStarted:    m.tasksStarted,
		tasksCompleted:  m.tasksCompleted,
		tasksFailed:     m.tasksFailed,
		llmCalls:        m.llmCalls,
		llmInputTokens:  m.llmInputTokens,
		llmOutputTokens: m.llmOutputTokens,
		llmTotalTokens:  m.llmTotalTokens,
		costCents:       m.costCents,
	}
	llmHistBuckets, llmHistCounts, llmHistTotal, llmHistSum := m.llmLatencyHist.snapshot()
	toolHistBuckets, toolHistCounts, toolHistTotal, toolHistSum := m.toolLatencyHist.snapshot()
	m.mu.RUnlock()

	// 2. 锁外格式化
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
		snap.tasksStarted, ts,
		snap.tasksCompleted, ts,
		snap.tasksFailed, ts,
		snap.llmCalls, ts,
		snap.llmInputTokens, ts,
		snap.llmOutputTokens, ts,
		snap.llmTotalTokens, ts,
		snap.costCents, ts,
	)
	out += formatHistogram("llm_latency_ms", "LLM call latency in milliseconds.",
		llmHistBuckets, llmHistCounts, llmHistTotal, llmHistSum)
	out += formatHistogram("tool_latency_ms", "Tool execution latency in milliseconds.",
		toolHistBuckets, toolHistCounts, toolHistTotal, toolHistSum)
	return out
}

// formatHistogram 从快照数据生成 Prometheus histogram 文本（锁外调用）。
func formatHistogram(name, help string, buckets []float64, counts []uint64, total uint64, sum float64) string {
	out := fmt.Sprintf("# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	var cumulative uint64
	for i, upper := range buckets {
		cumulative += counts[i]
		out += fmt.Sprintf("%s_bucket{le=\"%.3f\"} %d\n", name, upper, cumulative)
	}
	out += fmt.Sprintf("%s_bucket{le=\"+Inf\"} %d\n", name, total)
	out += fmt.Sprintf("%s_sum %.3f\n", name, sum)
	out += fmt.Sprintf("%s_count %d\n", name, total)
	return out
}

// DefaultMetrics 是 package 级别共享的 metric 收集器。
var DefaultMetrics = NewMetricsCollector()

// DefaultLogger 是 package 级别共享的 logger。
var DefaultLogger = NewStructuredLogger()

// DefaultAuditor 是 package 级别共享的 auditor。
var DefaultAuditor Auditor = NewMemoryAuditor(10000)

// 保留 encoding/json 导入，供 audit.go 等同包文件使用。
var _ = json.Marshal
