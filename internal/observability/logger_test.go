package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestLogger 构造一个「控制台写入 buf + 文件写入临时目录」的 Logger，
// 返回 logger、控制台缓冲区与日志文件路径。
func newTestLogger(t *testing.T, cfg LogConfig) (*Logger, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")
	cfg.FilePath = logPath

	lg := NewLogger(cfg)
	buf := &bytes.Buffer{}
	lg.SetConsoleOutput(buf)
	t.Cleanup(func() { _ = lg.Close() })
	return lg, buf, logPath
}

// readLogFile 关闭 logger 后读取文件 sink 的内容。
func readLogFile(t *testing.T, lg *Logger, path string) string {
	t.Helper()
	if err := lg.Close(); err != nil {
		t.Fatalf("close logger: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read log file: %v", err)
	}
	return string(b)
}

// TestMultiSinkIndependentLevels 验证 V2：控制台与文件可以有各自的最低级别。
// 控制台设为 warn、文件设为 debug 时，一条 debug 日志只应出现在文件里。
func TestMultiSinkIndependentLevels(t *testing.T) {
	lg, console, logPath := newTestLogger(t, LogConfig{
		ConsoleLevel: LevelWarn,
		FileLevel:    LevelDebug,
	})

	lg.Debug("only-in-file")
	lg.WarnMsg("in-both")

	consoleOut := console.String()
	if strings.Contains(consoleOut, "only-in-file") {
		t.Errorf("console 级别为 warn，不应输出 debug 日志:\n%s", consoleOut)
	}
	if !strings.Contains(consoleOut, "in-both") {
		t.Errorf("console 缺少 warn 日志:\n%s", consoleOut)
	}

	fileOut := readLogFile(t, lg, logPath)
	if !strings.Contains(fileOut, "only-in-file") {
		t.Errorf("文件级别为 debug，应包含 debug 日志:\n%s", fileOut)
	}
	if !strings.Contains(fileOut, "in-both") {
		t.Errorf("文件缺少 warn 日志:\n%s", fileOut)
	}
}

// TestConsoleTextFileJSON 验证 V1：控制台是 Text 格式，文件是 JSON 格式。
func TestConsoleTextFileJSON(t *testing.T) {
	lg, console, logPath := newTestLogger(t, LogConfig{
		ConsoleLevel: LevelInfo,
		FileLevel:    LevelInfo,
	})

	lg.InfoMsg("hello", AttrS("k", "v"))

	consoleOut := strings.TrimSpace(console.String())
	if strings.HasPrefix(consoleOut, "{") {
		t.Errorf("控制台应为 Text 格式，实际像 JSON:\n%s", consoleOut)
	}
	if !strings.Contains(consoleOut, "k=v") {
		t.Errorf("控制台 Text 输出缺少 k=v:\n%s", consoleOut)
	}

	fileOut := strings.TrimSpace(readLogFile(t, lg, logPath))
	var rec map[string]any
	if err := json.Unmarshal([]byte(fileOut), &rec); err != nil {
		t.Fatalf("文件应为 JSON 一行一条，解析失败: %v\n%s", err, fileOut)
	}
	if rec["msg"] != "hello" || rec["k"] != "v" {
		t.Errorf("JSON 字段不符合预期: %#v", rec)
	}
}

// TestCallerPointsToCallSite 验证 V4：AddSource=true 时 caller 指向业务调用点，
// 而不是 obs.go 这一层封装。这是 emit 手工取 PC 的核心目的。
func TestCallerPointsToCallSite(t *testing.T) {
	lg, console, _ := newTestLogger(t, LogConfig{
		ConsoleLevel: LevelTrace,
		FileLevel:    LevelTrace,
		AddSource:    true,
	})

	cases := []struct {
		name string
		emit func()
	}{
		{"Logger.InfoMsg", func() { lg.InfoMsg("m") }},
		{"Logger.Debugf", func() { lg.Debugf("m") }},
		{"Logger.Trace", func() { lg.Trace("m") }},
	}
	for _, tc := range cases {
		console.Reset()
		tc.emit()
		out := console.String()
		if !strings.Contains(out, "caller=logger_test.go:") {
			t.Errorf("%s: caller 应指向 logger_test.go，实际:\n%s", tc.name, out)
		}
		if strings.Contains(out, "caller=obs.go:") {
			t.Errorf("%s: caller 错误地指向了封装层 obs.go:\n%s", tc.name, out)
		}
	}
}

// TestStructuredLoggerCallerAndComponent 验证兼容层同样不会把 caller 记成 obs.go，
// 并且 component 字段仍然存在。
func TestStructuredLoggerCallerAndComponent(t *testing.T) {
	inner, console, _ := newTestLogger(t, LogConfig{
		ConsoleLevel: LevelDebug,
		FileLevel:    LevelDebug,
		AddSource:    true,
	})
	sl := &StructuredLogger{inner: inner}

	cases := []struct {
		name string
		emit func()
	}{
		{"Info", func() { sl.Info("comp", "m", map[string]any{"a": 1}) }},
		{"Log", func() { sl.Log(LevelWarn, "comp", "m", nil) }},
		{"Errorf", func() { sl.Errorf("comp", "m-%d", 1) }},
	}
	for _, tc := range cases {
		console.Reset()
		tc.emit()
		out := console.String()
		if !strings.Contains(out, "caller=logger_test.go:") {
			t.Errorf("%s: caller 应指向 logger_test.go，实际:\n%s", tc.name, out)
		}
		if !strings.Contains(out, "component=comp") {
			t.Errorf("%s: 缺少 component 字段:\n%s", tc.name, out)
		}
	}
}

// TestCustomLevelNames 验证 V3：TRACE / FATAL / PANIC 渲染为可读名字，
// 而不是 slog 默认的 DEBUG-4 / ERROR+4 / ERROR+8。
func TestCustomLevelNames(t *testing.T) {
	lg, console, _ := newTestLogger(t, LogConfig{
		ConsoleLevel: LevelTrace,
		FileLevel:    LevelTrace,
	})

	lg.Trace("t")
	if out := console.String(); !strings.Contains(out, "level=TRACE") {
		t.Errorf("trace 级别名渲染错误:\n%s", out)
	}

	// FATAL / PANIC 会终止进程，这里只验证映射表本身。
	if got := slogLevelMap[LevelFatal]; levelNames[got] != "FATAL" {
		t.Errorf("FATAL 级别名映射错误: %v", got)
	}
	if got := slogLevelMap[LevelPanic]; levelNames[got] != "PANIC" {
		t.Errorf("PANIC 级别名映射错误: %v", got)
	}
}

// TestSetLevelTakesEffectAtRuntime 验证 SetLevel 不再是 no-op：
// 通过 slog.LevelVar，已创建的 handler 也能立即感知级别变化。
func TestSetLevelTakesEffectAtRuntime(t *testing.T) {
	lg, console, _ := newTestLogger(t, LogConfig{
		ConsoleLevel: LevelInfo,
		FileLevel:    LevelInfo,
	})

	lg.Debug("before")
	if strings.Contains(console.String(), "before") {
		t.Fatalf("info 级别不应输出 debug 日志:\n%s", console.String())
	}

	lg.SetLevel(LevelDebug)
	console.Reset()
	lg.Debug("after")
	if !strings.Contains(console.String(), "after") {
		t.Errorf("SetLevel(debug) 后应输出 debug 日志:\n%s", console.String())
	}

	// 单 sink 调整
	lg.SetConsoleLevel(LevelError)
	console.Reset()
	lg.WarnMsg("warn-suppressed")
	if strings.Contains(console.String(), "warn-suppressed") {
		t.Errorf("SetConsoleLevel(error) 后不应输出 warn:\n%s", console.String())
	}
}

// TestSetConsoleOutputKeepsFileSink 验证旧 SetOutput 的「多 sink 退化成单 sink」
// 陷阱已经修掉：替换控制台 writer 后，文件 sink 仍然工作。
func TestSetConsoleOutputKeepsFileSink(t *testing.T) {
	lg, console, logPath := newTestLogger(t, LogConfig{
		ConsoleLevel: LevelInfo,
		FileLevel:    LevelInfo,
	})

	// newTestLogger 内部已经调用过一次 SetConsoleOutput，再换一次
	buf2 := &bytes.Buffer{}
	lg.SetConsoleOutput(buf2)
	lg.InfoMsg("still-logged")

	if console.Len() != 0 {
		t.Errorf("旧 writer 不应再收到日志: %s", console.String())
	}
	if !strings.Contains(buf2.String(), "still-logged") {
		t.Errorf("新 writer 未收到日志: %s", buf2.String())
	}
	if fileOut := readLogFile(t, lg, logPath); !strings.Contains(fileOut, "still-logged") {
		t.Errorf("替换 console writer 后文件 sink 丢失:\n%s", fileOut)
	}
}

// TestParseLogLevel 覆盖 7 级解析与非法值回退。
func TestParseLogLevel(t *testing.T) {
	cases := map[string]LogLevel{
		"trace": LevelTrace, "DEBUG": LevelDebug, " info ": LevelInfo,
		"warning": LevelWarn, "error": LevelError, "fatal": LevelFatal,
		"panic": LevelPanic, "": LevelInfo, "bogus": LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLogLevel(in); got != want {
			t.Errorf("ParseLogLevel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTraceAttrsFromContext 验证计划附录 B 的 trace 关联：
// CtxLog 会自动把 ctx 中的 trace_id / task_id 带进日志字段。
func TestTraceAttrsFromContext(t *testing.T) {
	lg, console, _ := newTestLogger(t, LogConfig{
		ConsoleLevel: LevelInfo,
		FileLevel:    LevelInfo,
	})

	ctx := WithTraceContext(context.Background(), &TraceContext{
		TraceID: "tid-1", SpanID: "sid-1", TaskID: "task-1", AgentID: "agent-1",
	})
	lg.CtxLog(ctx, LevelInfo, "with-trace")

	out := console.String()
	for _, want := range []string{"trace_id=tid-1", "span_id=sid-1", "task_id=task-1", "agent_id=agent-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %s:\n%s", want, out)
		}
	}

	// ctx 中没有 TraceContext 时不应 panic，也不应产生空字段
	console.Reset()
	lg.CtxLog(context.Background(), LevelInfo, "no-trace")
	if strings.Contains(console.String(), "trace_id=") {
		t.Errorf("无 trace 上下文时不应输出 trace_id:\n%s", console.String())
	}
}

// TestNoFileSinkWhenPathEmpty 验证 FilePath 为空时只有控制台 sink，不会创建文件。
func TestNoFileSinkWhenPathEmpty(t *testing.T) {
	lg := NewLogger(LogConfig{ConsoleLevel: LevelInfo, FileLevel: LevelInfo})
	buf := &bytes.Buffer{}
	lg.SetConsoleOutput(buf)
	defer func() { _ = lg.Close() }()

	lg.InfoMsg("console-only")
	if !strings.Contains(buf.String(), "console-only") {
		t.Errorf("控制台 sink 未生效: %s", buf.String())
	}
}

// TestStructuredLoggerReplace 验证 DefaultLogger 可以在 main 读完配置后被整体替换。
func TestStructuredLoggerReplace(t *testing.T) {
	sl := NewStructuredLogger()
	buf := &bytes.Buffer{}

	replacement := NewLogger(LogConfig{ConsoleLevel: LevelDebug, FileLevel: LevelDebug})
	replacement.SetConsoleOutput(buf)
	sl.Replace(replacement)
	defer func() { _ = sl.Close() }()

	sl.Debug("comp", "after-replace", nil)
	if !strings.Contains(buf.String(), "after-replace") {
		t.Errorf("Replace 后日志未写到新 sink: %s", buf.String())
	}
}
