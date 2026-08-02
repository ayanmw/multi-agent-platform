// Package observability 为平台提供结构化日志与 metric 指标。
//
// Phase 8：日志库从 stdlib log 替换为 log/slog（标准库），支持多 sink
// 独立级别与格式、lumberjack 文件轮转、caller 注入、7 级日志。
// metric 仍以简单 counter + histogram 形式保存，以 Prometheus 文本格式输出。
package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// ===========================================================================
// 日志级别
// ===========================================================================

// LogLevel 表示一条日志的严重级别。Phase 8 扩展为 7 级。
type LogLevel string

const (
	LevelTrace LogLevel = "trace" // 新增：ReAct 逐步骤、MCP 逐消息
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
	LevelFatal LogLevel = "fatal" // 记录后 os.Exit(1)
	LevelPanic LogLevel = "panic" // 新增：记录后 panic
)

// slog 自定义级别常量。
// slog 内置只有 Debug(-4)/Info(0)/Warn(4)/Error(8)，
// TRACE 需要比 Debug 更低，FATAL / PANIC 需要比 Error 更高，
// 因此在 slog.Level 数轴上按 4 为步长向两端扩展。
const (
	slogLevelTrace = slog.LevelDebug - 4 // -8
	slogLevelFatal = slog.LevelError + 4 // 12
	slogLevelPanic = slog.LevelError + 8 // 16
)

// slogLevelMap 将自定义级别映射到 slog.Level。
var slogLevelMap = map[LogLevel]slog.Level{
	LevelTrace: slogLevelTrace,
	LevelDebug: slog.LevelDebug,
	LevelInfo:  slog.LevelInfo,
	LevelWarn:  slog.LevelWarn,
	LevelError: slog.LevelError,
	LevelFatal: slogLevelFatal,
	LevelPanic: slogLevelPanic,
}

// levelOrder 用于级别比较（数值越大越严重）。
var levelOrder = map[LogLevel]int{
	LevelTrace: 0, LevelDebug: 1, LevelInfo: 2, LevelWarn: 3,
	LevelError: 4, LevelFatal: 5, LevelPanic: 6,
}

// levelEnabled 判断 level 是否达到 minLevel 的输出门槛。
func levelEnabled(level, minLevel LogLevel) bool {
	return levelOrder[level] >= levelOrder[minLevel]
}

// levelNames 用于把自定义 slog.Level 渲染成人类可读的名字。
// slog 默认会把 -8 渲染成 "DEBUG-4"、12 渲染成 "ERROR+4"，
// 这里通过 ReplaceAttr 统一替换为 TRACE / FATAL / PANIC。
var levelNames = map[slog.Level]string{
	slogLevelTrace: "TRACE",
	slogLevelFatal: "FATAL",
	slogLevelPanic: "PANIC",
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
	// inner 用原子指针保存，使热路径（每条日志）无需加锁即可读取；
	// 只有 SetOutput 这类重建 handler 的冷路径才会写入。
	inner atomic.Pointer[slog.Logger]

	// consoleLevel / fileLevel 是 slog.LevelVar，可在运行时热更新级别，
	// 且被 handler 直接持有引用 —— 这是 SetLevel 真正生效的关键。
	consoleLevel *slog.LevelVar
	fileLevel    *slog.LevelVar

	mu         sync.Mutex // 仅保护 cfg / consoleOut / closers 等冷路径字段
	cfg        LogConfig
	consoleOut io.Writer
	fileSink   slog.Handler // 文件 sink（可能为 nil），SetOutput 时需要保留
	closers    []io.Closer
}

// callerSkipDirect 是 emit 需要跳过的栈帧数：
//
//	0 = runtime.Callers 自身
//	1 = (*Logger).emit
//	2 = 调用 emit 的公开日志方法（Trace / InfoMsg / StructuredLogger.Info ...）
//	3 = 真正的业务调用点 ← 我们要记录的就是它
//
// 约定：所有公开日志方法都必须 **直接** 调用 emit，不得再套一层内部封装，
// 否则 caller 会指向 obs.go 而不是业务代码。
const callerSkipDirect = 3

// newConsoleHandlerOptions 构造控制台 sink 的 HandlerOptions。
// ReplaceAttr 做两件事：把 source 压缩成 `caller=file.go:123`，
// 以及把自定义级别渲染成 TRACE / FATAL / PANIC。
func newHandlerOptions(lv *slog.LevelVar, addSource, shortCaller bool) *slog.HandlerOptions {
	return &slog.HandlerOptions{
		Level:     lv,
		AddSource: addSource,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.SourceKey:
				if !shortCaller {
					return a
				}
				if src, ok := a.Value.Any().(*slog.Source); ok && src != nil {
					return slog.String("caller",
						fmt.Sprintf("%s:%d", filepath.Base(src.File), src.Line))
				}
			case slog.LevelKey:
				if lvl, ok := a.Value.Any().(slog.Level); ok {
					if name, ok := levelNames[lvl]; ok {
						return slog.String(slog.LevelKey, name)
					}
				}
			}
			return a
		},
	}
}

// NewLogger 根据 LogConfig 构建多 sink Logger。
// 控制台走 Text 格式（人类可读），文件走 JSON 格式（机器采集）+ lumberjack 轮转，
// 两个 sink 的最低级别互相独立。
func NewLogger(cfg LogConfig) *Logger {
	l := &Logger{
		consoleLevel: &slog.LevelVar{},
		fileLevel:    &slog.LevelVar{},
		cfg:          cfg,
		consoleOut:   os.Stdout,
	}
	l.consoleLevel.Set(slogLevelMap[normalizeLevel(cfg.ConsoleLevel)])
	l.fileLevel.Set(slogLevelMap[normalizeLevel(cfg.FileLevel)])

	// --- File sink: JSON + lumberjack 轮转 ---
	if cfg.FilePath != "" {
		lj := &lumberjack.Logger{
			Filename:   cfg.FilePath,
			MaxSize:    defaultInt(cfg.MaxSizeMB, 100),
			MaxBackups: defaultInt(cfg.MaxBackups, 7),
			MaxAge:     defaultInt(cfg.MaxAgeDays, 30),
			Compress:   cfg.Compress,
			LocalTime:  true,
		}
		l.closers = append(l.closers, lj)
		// 文件 sink 保留完整路径（shortCaller=false），便于日志采集侧定位。
		l.fileSink = slog.NewJSONHandler(lj, newHandlerOptions(l.fileLevel, cfg.AddSource, false))
	}

	l.rebuild()
	return l
}

// rebuild 依据当前 consoleOut / fileSink 重新组装 multiHandler。
// 调用方必须已持有 l.mu，或处于构造阶段（尚无并发）。
func (l *Logger) rebuild() {
	handlers := []slog.Handler{
		slog.NewTextHandler(l.consoleOut, newHandlerOptions(l.consoleLevel, l.cfg.AddSource, true)),
	}
	if l.fileSink != nil {
		handlers = append(handlers, l.fileSink)
	}
	l.inner.Store(slog.New(&multiHandler{handlers: handlers}))
}

// Close 关闭所有底层资源（如 lumberjack 文件句柄）。
// 进程退出前应调用，否则最后一批缓冲日志可能丢失。
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var firstErr error
	for _, c := range l.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	l.closers = nil
	return firstErr
}

// SetLevel 同时调整控制台与文件 sink 的最低级别。
// 通过 slog.LevelVar 实现，对已创建的 handler 立即生效。
func (l *Logger) SetLevel(level LogLevel) {
	sl := slogLevelMap[normalizeLevel(level)]
	l.consoleLevel.Set(sl)
	l.fileLevel.Set(sl)
}

// SetConsoleLevel 单独调整控制台 sink 的最低级别。
func (l *Logger) SetConsoleLevel(level LogLevel) {
	l.consoleLevel.Set(slogLevelMap[normalizeLevel(level)])
}

// SetFileLevel 单独调整文件 sink 的最低级别。
func (l *Logger) SetFileLevel(level LogLevel) {
	l.fileLevel.Set(slogLevelMap[normalizeLevel(level)])
}

// ConsoleLevel 返回控制台 sink 当前的 slog 级别（测试与 /healthz 自检用）。
func (l *Logger) ConsoleLevel() slog.Level { return l.consoleLevel.Level() }

// FileLevel 返回文件 sink 当前的 slog 级别。
func (l *Logger) FileLevel() slog.Level { return l.fileLevel.Level() }

// SetConsoleOutput 替换控制台 sink 的 writer（主要用于测试捕获输出）。
// 文件 sink 会被保留 —— 这与旧版 SetOutput 直接丢掉多 sink 的行为不同。
func (l *Logger) SetConsoleOutput(w io.Writer) {
	if w == nil {
		w = io.Discard
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.consoleOut = w
	l.rebuild()
}

// --- 7 级新 API（slog.Attr 风格）---

// emit 是所有日志输出的唯一出口。
//
// 它没有复用 slog.Logger.LogAttrs，因为后者会把 caller 记录成本封装层的位置
// （obs.go），而不是业务调用点。这里手工构造 slog.Record 并显式取 PC。
func (l *Logger) emit(level LogLevel, msg string, attrs ...slog.Attr) {
	lg := l.inner.Load()
	if lg == nil {
		return
	}
	slevel := slogLevelMap[level]
	ctx := context.Background()
	h := lg.Handler()
	if !h.Enabled(ctx, slevel) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(callerSkipDirect, pcs[:])
	rec := slog.NewRecord(time.Now(), slevel, msg, pcs[0])
	rec.AddAttrs(attrs...)
	_ = h.Handle(ctx, rec)
}

// Trace 输出 trace 级别日志。
func (l *Logger) Trace(msg string, attrs ...slog.Attr) {
	l.emit(LevelTrace, msg, attrs...)
}

// Debug 输出 debug 级别日志。
func (l *Logger) Debug(msg string, attrs ...slog.Attr) {
	l.emit(LevelDebug, msg, attrs...)
}

// InfoMsg 输出 info 级别日志（新签名）。
func (l *Logger) InfoMsg(msg string, attrs ...slog.Attr) {
	l.emit(LevelInfo, msg, attrs...)
}

// WarnMsg 输出 warn 级别日志（新签名）。
func (l *Logger) WarnMsg(msg string, attrs ...slog.Attr) {
	l.emit(LevelWarn, msg, attrs...)
}

// ErrorMsg 输出 error 级别日志（新签名）。
func (l *Logger) ErrorMsg(msg string, attrs ...slog.Attr) {
	l.emit(LevelError, msg, attrs...)
}

// Fatal 输出 fatal 级别日志后调用 os.Exit(1)。
func (l *Logger) Fatal(msg string, attrs ...slog.Attr) {
	l.emit(LevelFatal, msg, attrs...)
	_ = l.Close()
	os.Exit(1)
}

// Panic 输出 panic 级别日志后 panic。
func (l *Logger) Panic(msg string, attrs ...slog.Attr) {
	l.emit(LevelPanic, msg, attrs...)
	panic(msg)
}

// --- fmt 风格便捷方法 ---

// Tracef 以 fmt 风格输出 trace 日志。
func (l *Logger) Tracef(format string, args ...any) {
	l.emit(LevelTrace, fmt.Sprintf(format, args...))
}

// Debugf 以 fmt 风格输出 debug 日志。
func (l *Logger) Debugf(format string, args ...any) {
	l.emit(LevelDebug, fmt.Sprintf(format, args...))
}

// Infof 以 fmt 风格输出 info 日志。
func (l *Logger) Infof(format string, args ...any) {
	l.emit(LevelInfo, fmt.Sprintf(format, args...))
}

// Warnf 以 fmt 风格输出 warn 日志。
func (l *Logger) Warnf(format string, args ...any) {
	l.emit(LevelWarn, fmt.Sprintf(format, args...))
}

// Errorf 以 fmt 风格输出 error 日志。
func (l *Logger) Errorf(format string, args ...any) {
	l.emit(LevelError, fmt.Sprintf(format, args...))
}

// Fatalf 以 fmt 风格输出 fatal 日志后 os.Exit(1)。
func (l *Logger) Fatalf(format string, args ...any) {
	l.emit(LevelFatal, fmt.Sprintf(format, args...))
	_ = l.Close()
	os.Exit(1)
}

// --- trace 上下文关联（计划附录 B）---

// traceCtxKey 是把 TraceContext 放进 context.Context 的私有 key 类型。
type traceCtxKey struct{}

// WithTraceContext 把 TraceContext 挂到 ctx 上，供下游日志自动带出 trace_id。
func WithTraceContext(ctx context.Context, tc *TraceContext) context.Context {
	if tc == nil {
		return ctx
	}
	return context.WithValue(ctx, traceCtxKey{}, tc)
}

// TraceContextFrom 从 ctx 中取回 TraceContext，不存在时返回 nil。
func TraceContextFrom(ctx context.Context) *TraceContext {
	if ctx == nil {
		return nil
	}
	tc, _ := ctx.Value(traceCtxKey{}).(*TraceContext)
	return tc
}

// TraceAttrs 从 ctx 中提取 trace_id / span_id / task_id / agent_id 作为日志字段。
// ctx 中没有 TraceContext 时返回 nil，调用方可以无条件展开。
func TraceAttrs(ctx context.Context) []slog.Attr {
	tc := TraceContextFrom(ctx)
	if tc == nil {
		return nil
	}
	attrs := make([]slog.Attr, 0, 4)
	if tc.TraceID != "" {
		attrs = append(attrs, slog.String("trace_id", tc.TraceID))
	}
	if tc.SpanID != "" {
		attrs = append(attrs, slog.String("span_id", tc.SpanID))
	}
	if tc.TaskID != "" {
		attrs = append(attrs, slog.String("task_id", tc.TaskID))
	}
	if tc.AgentID != "" {
		attrs = append(attrs, slog.String("agent_id", tc.AgentID))
	}
	return attrs
}

// CtxLog 输出一条日志并自动附带 ctx 中的 trace 字段。
func (l *Logger) CtxLog(ctx context.Context, level LogLevel, msg string, attrs ...slog.Attr) {
	if t := TraceAttrs(ctx); len(t) > 0 {
		attrs = append(t, attrs...)
	}
	l.emit(level, msg, attrs...)
}

// normalizeLevel 把空值或非法值归一化为 Info，避免 slogLevelMap 取到零值
// （slog.Level(0) == Info 恰好安全，但显式归一更利于阅读与测试）。
func normalizeLevel(level LogLevel) LogLevel {
	if _, ok := levelOrder[level]; !ok {
		return LevelInfo
	}
	return level
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

// Replace 用一个新构建的 Logger 替换底层实现。
// main.go 在读完配置后调用它，让所有已经拿到 DefaultLogger 引用的调用点
// 无需改动即可切换到多 sink + 轮转 + caller 的新日志器。
func (l *StructuredLogger) Replace(newInner *Logger) {
	if newInner == nil {
		return
	}
	old := l.inner
	l.inner = newInner
	if old != nil && old != newInner {
		_ = old.Close()
	}
}

// Close 关闭底层 Logger 持有的资源。
func (l *StructuredLogger) Close() error {
	if l.inner == nil {
		return nil
	}
	return l.inner.Close()
}

// SetLevel 调整最低日志级别（控制台与文件同时生效）。
//
// Deprecated: 优先使用 NewLogger(cfg) 在初始化时配置各 sink 的独立级别。
func (l *StructuredLogger) SetLevel(level LogLevel) {
	l.inner.SetLevel(level)
}

// SetOutput 替换控制台 sink 的 writer，文件 sink 保持不变。
//
// Deprecated: 使用 NewLogger(cfg) 配置文件输出；本方法主要留给测试捕获输出。
func (l *StructuredLogger) SetOutput(w io.Writer) {
	l.inner.SetConsoleOutput(w)
}

// structAttrs 把旧签名的 (component, fields) 转成 slog.Attr 切片。
func structAttrs(component string, fields map[string]any) []slog.Attr {
	attrs := mapToAttrs(fields)
	if component != "" {
		attrs = append(attrs, slog.String("component", component))
	}
	return attrs
}

// Log 在级别通过过滤时输出一条结构化日志（旧签名兼容）。
func (l *StructuredLogger) Log(level LogLevel, component, msg string, fields map[string]any) {
	l.inner.emit(level, msg, structAttrs(component, fields)...)
}

// Debug 输出 debug 级别的结构化日志（旧签名）。
//
// 注意：下面这些方法都直接调用 inner.emit 而不是转发给 Log，
// 因为 emit 用固定栈深度定位 caller，多套一层会让 caller 指向 obs.go。
func (l *StructuredLogger) Debug(component, msg string, fields map[string]any) {
	l.inner.emit(LevelDebug, msg, structAttrs(component, fields)...)
}

// Info 输出 info 级别的结构化日志（旧签名）。
func (l *StructuredLogger) Info(component, msg string, fields map[string]any) {
	l.inner.emit(LevelInfo, msg, structAttrs(component, fields)...)
}

// Warn 输出 warn 级别的结构化日志（旧签名）。
func (l *StructuredLogger) Warn(component, msg string, fields map[string]any) {
	l.inner.emit(LevelWarn, msg, structAttrs(component, fields)...)
}

// Error 输出 error 级别的结构化日志（旧签名）。
func (l *StructuredLogger) Error(component, msg string, fields map[string]any) {
	l.inner.emit(LevelError, msg, structAttrs(component, fields)...)
}

// Infof 以 fmt 风格格式化输出一条 info 日志（旧签名）。
func (l *StructuredLogger) Infof(component, format string, args ...any) {
	l.inner.emit(LevelInfo, fmt.Sprintf(format, args...), structAttrs(component, nil)...)
}

// Warnf 以 fmt 风格格式化输出一条 warn 日志（旧签名）。
func (l *StructuredLogger) Warnf(component, format string, args ...any) {
	l.inner.emit(LevelWarn, fmt.Sprintf(format, args...), structAttrs(component, nil)...)
}

// Errorf 以 fmt 风格格式化输出一条 error 日志（旧签名）。
func (l *StructuredLogger) Errorf(component, format string, args ...any) {
	l.inner.emit(LevelError, fmt.Sprintf(format, args...), structAttrs(component, nil)...)
}

// --- 新级别方法 ---

// Trace 输出 trace 级别日志。
func (l *StructuredLogger) Trace(msg string, attrs ...slog.Attr) {
	l.inner.emit(LevelTrace, msg, attrs...)
}

// Tracef 以 fmt 风格输出 trace 日志。
func (l *StructuredLogger) Tracef(format string, args ...any) {
	l.inner.emit(LevelTrace, fmt.Sprintf(format, args...))
}

// Fatal 输出 fatal 级别日志后 os.Exit(1)。
func (l *StructuredLogger) Fatal(msg string, attrs ...slog.Attr) {
	l.inner.emit(LevelFatal, msg, attrs...)
	_ = l.inner.Close()
	os.Exit(1)
}

// Fatalf 以 fmt 风格输出 fatal 日志后 os.Exit(1)。
func (l *StructuredLogger) Fatalf(format string, args ...any) {
	l.inner.emit(LevelFatal, fmt.Sprintf(format, args...))
	_ = l.inner.Close()
	os.Exit(1)
}

// Panic 输出 panic 级别日志后 panic。
func (l *StructuredLogger) Panic(msg string, attrs ...slog.Attr) {
	l.inner.emit(LevelPanic, msg, attrs...)
	panic(msg)
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
