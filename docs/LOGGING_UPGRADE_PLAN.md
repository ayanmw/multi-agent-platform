# 日志系统替换升级 + 可观测性改进方案

> 状态：**已实施完成**（S1–S25 全部落地，V1–V16 全部达标）
> 版本：v0.14.0 规划
> 基于当前代码库实际审计结果编写
>
> 实施记录：Phase 8-1（S1–S14）、Phase 8-2（S15 试点 + S24）、
> Phase 8-3（S16–S23 全量迁移 + S25 Metrics 回填）。
> V14 实测：非测试代码中标准库 `log.Print*` 残留 **0 处**（目标 ≤5）。

---

## 目录

- [一、现状审计与问题清单](#一现状审计与问题清单)
- [二、日志库替换方案](#二日志库替换方案)
- [三、多日志级别体系](#三多日志级别体系)
- [四、日志埋点补充方案](#四日志埋点补充方案)
- [五、可观测性配套修复](#五可观测性配套修复)
- [六、配置体系完善](#六配置体系完善)
- [七、迁移与兼容策略](#七迁移与兼容策略)
- [八、实施步骤与验收标准](#八实施步骤与验收标准)

---

## 一、现状审计与问题清单

### 1.1 日志库现状

| 维度 | 当前实现 | 问题 |
|------|---------|------|
| 底层库 | Go stdlib `log` 包封装为 `*log.Logger` | 无原生多 sink、无采样、无轮转、无 caller 信息 |
| 多输出 | `initDualLogging` 用 `io.MultiWriter(stdout, file)` | **文件和控制台拿到完全相同的 JSON 流**，无法分别配置级别/格式；控制台本应可读但被迫吃 JSON |
| 控制台 | stdlib `log.Printf` 散布 ~200 处，与 StructuredLogger 双轨 | 结构化日志和纯文本日志两套并行，无法统一过滤/检索 |
| 级别数 | 5 级：debug/info/warn/error/fatal | 缺 **TRACE** 级（ReAct 逐步骤细节无处安放）；fatal 定义了但无方法实现（`Fatal()` 未编写，`log.Fatalf` 绕过 StructuredLogger） |
| 级别分布 | Debug 1 次、Info 14 次、Warn 46 次、Error 1 次 | 极度不均衡：几乎全靠 Warn，Error 仅 1 处，Debug 形同虚设 |
| caller | 无 | 日志不含 `file:line` / `func`，无法定位问题来源 |
| 采样 | 无 | 高频循环日志（heartbeat、hub、orchestrator）无采样，刷屏 |
| 轮转 | 无 | `O_APPEND` 无限增长，无 lumberjack 或外部 logrotate |
| 字段 | 固定 `ts/level/component/msg` + 任意 fields | 无 `trace_id`/`span_id`/`task_id` 自动注入，日志与 trace 无法关联 |

### 1.2 可观测性问题（上一轮审计确认）

| # | 问题 | 证据 |
|---|------|------|
| P1 | `/metrics`、`/api/audit`、`/api/traces`、`/healthz` 无认证 | `server.go:222-224,338,367` 注册时无 `RequireRoleFunc`，而 `/api/agents` 有 |
| P2 | trace span 仅覆盖 think，tool_call 无 span | `engine.go:1621` 唯一一次 `StartChild("think")`；`executeTool()` (L1986) 无 span |
| P3 | `AUDIT_BUFFER_LIMIT` / `TRACE_BUFFER_LIMIT` 未从环境变量读取 | `main.go:63` 硬编码 `NewTracer(2000)`，`main.go:389` 硬编码 `NewMemoryAuditor(10000)` |
| P4 | 审计 SQLite 写入错误被 `_ =` 丢弃 | `audit_sqlite.go:18` |
| P5 | `onSpan` 回调每个 span `go` 一个 goroutine，无背压 | `trace.go:166` `go t.onSpan(rec)` |
| P6 | 无日志轮转 | `main.go:1420` `os.OpenFile(..., O_APPEND, ...)` 无轮转 |
| P7 | Metrics 纯内存，重启归零 | `obs.go` MetricsCollector 无持久化/回填 |
| P8 | Histogram bucket 最大 10s，LLM 长尾全溢出 | `obs.go:166` `buckets := []float64{10,...,10000}` |
| P9 | `PrometheusText()` 持锁期间格式化 | `obs.go:230` `RLock` 覆盖整个 `fmt.Sprintf` |

---

## 二、日志库替换方案

### 2.1 选型决策

```
方案 A：zap (uber-go/zap)
方案 B：slog (Go 1.21+ stdlib, 本项目 go 1.25.4 已支持)
方案 C：继续手写增强
```

**推荐：方案 B — `log/slog`（标准库）**

理由：
1. **零新增依赖**：项目设计哲学是"刻意避免引入外部依赖"（`obs.go` 注释明确声明），slog 是 Go 1.21+ 标准库，不违背此原则
2. **原生多 Handler**：slog 的 `slog.Handler` 接口天然支持多 sink，每个 sink 可独立配置级别/格式
3. **结构化优先**：slog 原生 `slog.Attr` + `slog.LogValuer`，比 zap 的 `zap.Field` 更符合 Go 惯例
4. **caller 信息内置**：`slog.HandlerOptions{AddSource: true}` 一行启用
5. **Go 1.25.4 已满足**：无需考虑版本兼容
6. **可扩展**：需要采样时用 `slog.Handler` 中间件包装即可，不需要换库

> 如果团队更偏好 zap 的极致性能（零分配），可用方案 A，API 映射关系不变。
> 以下方案以 slog 为基准设计。

### 2.2 新 Logger 架构

```
                         ┌─────────────────────────────────────┐
                         │         Logger (slog.Logger)         │
                         │   统一入口 Debug/Info/Warn/Error/     │
                         │   Trace / Fatal                      │
                         └──────────────┬──────────────────────┘
                                        │ MultiHandler
                    ┌───────────────────┼───────────────────┐
                    ▼                   ▼                   ▼
          ┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐
          │ ConsoleHandler   │ │  FileHandler     │ │  AuditHandler   │
          │ (TextFormat)     │ │ (JSONFormat)     │ │ (可选, 写DB)     │
          │ Level: 可配置     │ │ Level: 可配置     │ │ Level: warn+    │
          │ 含 caller        │ │ 含 caller        │ │                 │
          │ lumberjack 轮转  │ │ lumberjack 轮转  │ │                 │
          └─────────────────┘ └─────────────────┘ └─────────────────┘
```

**核心设计点：**
- **双 sink 独立级别**：控制台可设 `info`（可读），文件可设 `debug`（详细），各自过滤
- **双格式**：控制台用 `slog.NewTextHandler`（人类可读），文件用 `slog.NewJSONHandler`（机器采集）
- **统一接口**：全平台只通过 `observability.DefaultLogger` 调用，消灭散布的 `log.Printf`

### 2.3 新 Logger 接口设计

替换 `internal/observability/obs.go` 中的 `StructuredLogger`：

```go
package observability

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// --- 日志级别 -----------------------------------------------------------

// LogLevel 扩展为 7 级，新增 TRACE 和 PANIC。
type LogLevel string

const (
	LevelTrace  LogLevel = "trace"   // 新增：ReAct 逐步骤、MCP 逐消息
	LevelDebug  LogLevel = "debug"
	LevelInfo   LogLevel = "info"
	LevelWarn   LogLevel = "warn"
	LevelError  LogLevel = "error"
	LevelFatal  LogLevel = "fatal"   //Fatal 调用后 os.Exit(1)
	LevelPanic  LogLevel = "panic"   // 新增：panic 前记录
)

// slogLevelMap 将自定义级别映射到 slog.Level。
// TRACE 映射到 slog.LevelDebug - 4（比 Debug 更低），
// FATAL / PANIC 映射到 slog.LevelError + 4 / +8（比 Error 更高）。
var slogLevelMap = map[LogLevel]slog.Level{
	LevelTrace:  slog.LevelDebug - 4,
	LevelDebug:  slog.LevelDebug,
	LevelInfo:   slog.LevelInfo,
	LevelWarn:   slog.LevelWarn,
	LevelError:  slog.LevelError,
	LevelFatal:  slog.LevelError + 4,
	LevelPanic:  slog.LevelError + 8,
}

// --- Logger -------------------------------------------------------------

// Logger 是全平台统一的日志器，封装 slog 并提供：
//   - 7 级日志（Trace/Debug/Info/Warn/Error/Fatal/Panic）
//   - 多 sink 独立级别与格式
//   - 结构化 fields（slog.Attr）与 fmt 风格（Xxxf）双 API
//   - caller 信息自动注入
//   - lumberjack 文件轮转
type Logger struct {
	mu      sync.RWMutex
	inner   *slog.Logger
	level   LogLevel
	handers []io.Closer // 用于关闭 lumberjack 等资源
}

// LogConfig 配置多 sink 日志输出。
type LogConfig struct {
	ConsoleLevel LogLevel // 控制台最低级别
	FileLevel    LogLevel // 文件最低级别
	FilePath     string   // 日志文件路径，空则不写文件
	// lumberjack 轮转参数
	MaxSizeMB    int  // 单文件最大 MB，默认 100
	MaxBackups   int  // 保留旧文件数，默认 7
	MaxAgeDays   int  // 保留天数，默认 30
	Compress     bool // 是否 gzip 压缩旧文件
	AddSource    bool // 是否注入 caller file:line
}

// NewLogger 根据 LogConfig 构建多 sink Logger。
func NewLogger(cfg LogConfig) *Logger {
	var handlers []slog.Handler

	// --- Console sink: Text 格式，人类可读 ---
	consoleOpts := &slog.HandlerOptions{
		Level:     slogLevelMap[cfg.ConsoleLevel],
		AddSource: cfg.AddSource,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// 控制台简化 source 为 file:line
			if a.Key == slog.SourceKey {
				src, _ := a.Value.Any().(*slog.Source)
				if src != nil {
					return slog.String("caller",
						fmt.Sprintf("%s:%d", filepath.Base(src.File), src.Line))
				}
			}
			return a
		},
	}
	handlers = append(handlers, slog.NewTextHandler(os.Stdout, consoleOpts))

	// --- File sink: JSON 格式，机器采集 ---
	var closers []io.Closer
	if cfg.FilePath != "" {
		var fileWriter io.Writer = lumberjack.Logger{
			Filename:   cfg.FilePath,
			MaxSize:    defaultIf(cfg.MaxSizeMB, 100),
			MaxBackups: defaultIf(cfg.MaxBackups, 7),
			MaxAge:     defaultIf(cfg.MaxAgeDays, 30),
			Compress:   cfg.Compress,
			LocalTime:  true,
		}
		fileOpts := &slog.HandlerOptions{
			Level:     slogLevelMap[cfg.FileLevel],
			AddSource: cfg.AddSource,
		}
		handlers = append(handlers, slog.NewJSONHandler(fileWriter, fileOpts))
	}

	multi := slog.MultiHandler(handlers...)
	return &Logger{
		inner:   slog.New(multi),
		level:   cfg.ConsoleLevel, // 全局最低级别取控制台级别
		closers: closers,
	}
}

// --- 7 级 API ---

func (l *Logger) Trace(msg string, attrs ...slog.Attr) {
	l.logAttrs(LevelTrace, msg, attrs...)
}
func (l *Logger) Debug(msg string, attrs ...slog.Attr) {
	l.logAttrs(LevelDebug, msg, attrs...)
}
func (l *Logger) Info(msg string, attrs ...slog.Attr) {
	l.logAttrs(LevelInfo, msg, attrs...)
}
func (l *Logger) Warn(msg string, attrs ...slog.Attr) {
	l.logAttrs(LevelWarn, msg, attrs...)
}
func (l *Logger) Error(msg string, attrs ...slog.Attr) {
	l.logAttrs(LevelError, msg, attrs...)
}
func (l *Logger) Fatal(msg string, attrs ...slog.Attr) {
	l.logAttrs(LevelFatal, msg, attrs...)
	os.Exit(1)
}
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
// ... Infof / Warnf / Errorf / Fatalf / Panicf 同理

// --- 带 context 的方法（用于 trace 关联）---

func (l *Logger) InfoCtx(ctx context.Context, msg string, attrs ...slog.Attr) {
	// 从 ctx 提取 trace_id / span_id / task_id 并注入 attrs
	attrs = injectTraceFromCtx(ctx, attrs)
	l.inner.LogAttrs(ctx, slog.LevelInfo, msg, attrs...)
}
// ... DebugCtx / WarnCtx / ErrorCtx 同理

func (l *Logger) logAttrs(level LogLevel, msg string, attrs ...slog.Attr) {
	slevel := slogLevelMap[level]
	l.inner.LogAttrs(context.Background(), slevel, msg, attrs...)
}
```

### 2.4 与旧 API 的兼容层

旧代码使用 `DefaultLogger.Info("component", "msg", map[string]any{...})` 签名。
为避免 200+ 处调用一次性修改，提供 **适配函数** 兼容旧签名：

```go
// Deprecated: 使用 Info(msg, attrs...) 替代。
// 适配层把旧签名 (component, msg, fields map[string]any) 转为新签名。
func (l *Logger) InfoCompat(component, msg string, fields map[string]any) {
	attrs := mapToAttrs(fields)
	attrs = append(attrs, slog.String("component", component))
	l.Info(msg, attrs...)
}
// WarnCompat / ErrorCompat / DebugCompat 同理

func mapToAttrs(m map[string]any) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(m))
	for k, v := range m {
		attrs = append(attrs, slog.Any(k, v))
	}
	return attrs
}
```

迁移策略：旧 `DefaultLogger` 保持为 `*Logger` 类型，旧的 `Info/Warn/Error` 方法签名**保留为 deprecated 别名**，新增 `InfoMsg/WarnMsg/ErrorMsg` 方法使用新签名。逐步迁移后删除兼容层。

### 2.5 日志输出样例对比

**当前**（控制台和文件相同 JSON）：
```json
{"component":"server","level":"info","msg":"initializing subsystems","port":"32704","ts":"2026-07-27T10:33:46.618Z"}
```

**升级后 — 控制台**（Text，人类可读，含 caller）：
```
time=2026-07-29T15:30:00.618+08:00 level=INFO caller=main.go:267 msg="initializing subsystems" component=server port=32704 db_path=data/app.db
```

**升级后 — 文件**（JSON，机器采集，含 caller + trace_id）：
```json
{"time":"2026-07-29T15:30:00.618+08:00","level":"INFO","source":{"function":"main.main","file":"/cmd/server/main.go","line":267},"msg":"initializing subsystems","component":"server","port":"32704","db_path":"data/app.db","trace_id":"a1b2c3d4","task_id":"task_abc123"}
```

---

## 三、多日志级别体系

### 3.1 7 级日志定义与使用边界

| 级别 | slog 映射 | 使用场景 | 示例 |
|------|-----------|---------|------|
| **TRACE** | Debug-4 | ReAct 每一步骤的原始数据、MCP JSON-RPC 逐消息、WebSocket 帧级调试 | `Trace("react step", "step_type", "think", "model", "qwen-3.5", "raw_response_len", 4096)` |
| **DEBUG** | Debug | 子系统内部决策路径、配置加载详情、tool 参数解析 | `Debug("tool args parsed", "tool", "run_shell", "args", args)` |
| **INFO** | Info | 正常运行里程碑：启动/关闭、任务开始/完成、MCP server 连接 | `Info("task started", "task_id", tid, "agent_id", agentID)` |
| **WARN** | Warn | 可恢复异常、降级、重试、回退 | `Warn("llm call failed, using fallback", "model", model, "error", err)` |
| **ERROR** | Error | 不可恢复错误、需要人工介入的异常 | `Error("audit record persist failed", "record_id", rec.ID, "error", err)` |
| **FATAL** | Error+4 | 启动失败等不可继续的情况，记录后 `os.Exit(1)` | `Fatal("failed to load config", "error", err)` |
| **PANIC** | Error+8 | 不该发生的编程错误，记录后 `panic` | `Panic("nil tracer in finish", "task_id", taskID)` |

### 3.2 级别配置策略

```bash
# .env 新增
LOG_CONSOLE_LEVEL=info      # 控制台：info 及以上（日常运维可读）
LOG_FILE_LEVEL=trace        # 文件：trace 及以上（全量留底，事后排查）
LOG_ADD_SOURCE=true         # 注入 caller file:line
# 轮转
LOG_FILE_MAX_SIZE_MB=100
LOG_FILE_MAX_BACKUPS=7
LOG_FILE_MAX_AGE_DAYS=30
LOG_FILE_COMPRESS=true
```

控制台和文件**独立配置级别**是核心改进——运维只看 info，排查时翻文件 trace。

### 3.3 级别迁移对照表

| 旧调用 | 新调用 | 说明 |
|--------|--------|------|
| `DefaultLogger.Warn("comp","msg",fields)` | `DefaultLogger.Warn("msg", append(attrs, slog.String("component","comp"))...)` | Warn → Warn（语义不变，签名变化） |
| `log.Printf("[API] ...")` | `DefaultLogger.Debug("...", ...)` 或 `.Info(...)` | 200 处 stdlib log 迁移到结构化 |
| `log.Fatalf("...")` | `DefaultLogger.Fatal("...", ...)` | Fatal 经由统一日志器 |
| `fmt.Println(colorfulBanner)` | 保留为启动横幅，或 `DefaultLogger.Info("server started", ...)` | 启动横幅可保留纯文本 |
| `// DEBUG log.Printf(...)` 注释 | `DefaultLogger.Trace(...)` 或 `.Debug(...)` | 取消注释，改为可控级别 |

---

## 四、日志埋点补充方案

### 4.1 埋点位置总览

当前 62 处 `DefaultLogger` 调用 + ~200 处 `log.Printf`。
按子系统列出需新增/升级的埋点：

### 4.2 ReAct Engine 埋点（`internal/runtime/engine.go`）

| 位置 | 级别 | 内容 | 当前状态 |
|------|------|------|---------|
| `Run()` 入口 | INFO | `task_id, agent_id, model, max_steps` | 缺失 |
| `think()` 入口 | TRACE | `step_idx, model, provider, system_prompt_len` | 仅 Debug 注释 |
| `think()` LLM 调用前 | TRACE | `messages_count, total_tokens_estimate` | 缺失 |
| `think()` LLM 响应后 | DEBUG | `model, usage{input,output,total}, latency_ms, tool_calls_count` | 缺失 |
| `think()` 成功 | DEBUG | `content_len, tool_calls` | 缺失 |
| `think()` 失败 | ERROR | `model, error, latency_ms, retry_count` | 缺失 |
| `executeTool()` 入口 | TRACE | `tool_name, args, step_idx` | 缺失 |
| `executeTool()` 审批触发 | WARN | `tool, approval_id, reason` | 有事件无日志 |
| `executeTool()` 策略拦截 | WARN | `tool, rule, reason` | 有事件无日志 |
| `executeTool()` 成功 | DEBUG | `tool, duration_ms, result_len` | 缺失 |
| `executeTool()` 失败 | ERROR | `tool, error, duration_ms` | 缺失 |
| `Run()` 超过 max_steps | WARN | `task_id, max_steps` | 有事件无日志 |
| `Run()` 任务完成 | INFO | `task_id, agent_id, total_steps, total_duration_ms` | 缺失 |

### 4.3 LLM Provider 埋点（`internal/llm/`）

| 位置 | 级别 | 内容 | 当前状态 |
|------|------|------|---------|
| API 请求前 | TRACE | `provider, model, endpoint, messages_count` | 缺失 |
| API 请求后 | DEBUG | `provider, model, latency_ms, usage` | 缺失 |
| API 超时/重试 | WARN | `provider, model, attempt, error` | 仅 log.Printf |
| API 失败 | ERROR | `provider, model, error, status_code` | 缺失 |
| Fallback 触发 | WARN | `from_model, to_model, reason` | 缺失 |
| 流式首 token | DEBUG | `provider, model, time_to_first_token_ms` | 缺失 |

### 4.4 Orchestrator 埋点（`internal/orchestrator/`）

| 位置 | 级别 | 内容 | 当前状态 |
|------|------|------|---------|
| 编排启动 | INFO | `task_id, pattern, sub_agent_count` | 缺失 |
| sub-agent 派发 | DEBUG | `parent_agent, child_agent, sub_task_id` | 缺失 |
| sub-agent 完成 | DEBUG | `sub_task_id, status, duration_ms` | 缺失 |
| 编排完成 | INFO | `task_id, total_duration_ms, sub_agent_results` | 缺失 |
| 编排失败 | ERROR | `task_id, failed_agent, error` | 缺失 |

### 4.5 MCP 子系统埋点（`internal/tool/mcp/`）

| 位置 | 级别 | 内容 | 当前状态 |
|------|------|------|---------|
| Server 连接 | INFO | `server_name, transport, endpoint/command` | 缺失 |
| Server 断开 | WARN | `server_name, reason` | 缺失 |
| JSON-RPC 请求 | TRACE | `server, method, params` | 缺失 |
| JSON-RPC 响应 | TRACE | `server, method, latency_ms, result_len` | 缺失 |
| 工具调用 | DEBUG | `tool_name(=mcp__server__tool), duration_ms` | 缺失 |
| Server 启动失败 | ERROR | `server_name, command, error` | 缺失 |

### 4.6 WebSocket Hub 埋点（`internal/ws/`）

| 位置 | 级别 | 内容 | 当前状态 |
|------|------|------|---------|
| 客户端连接 | INFO | `client_count, session_id` | log.Printf |
| 客户端断开 | DEBUG | `client_count, reason` | log.Printf |
| 事件发送失败 | WARN | `event_type, client_count, error` | log.Printf |
| 控制消息 | DEBUG | `action, task_id, agent_id` | 有 DefaultLogger.Debug（唯一一处） |

### 4.7 数据库埋点（`pkg/db/`）

| 位置 | 级别 | 内容 | 当前状态 |
|------|------|------|---------|
| 迁移执行 | INFO | `version, description` | log.Printf |
| 迁移失败 | ERROR | `version, error` | log.Printf |
| DB 初始化 | INFO | `path` | 有 DefaultLogger.Info |

### 4.8 审计日志埋点补充

| 位置 | 级别 | 内容 | 当前状态 |
|------|------|------|---------|
| 审计记录创建 | INFO | `actor, action, target` | 缺失（只写 audit table） |
| 审计持久化失败 | ERROR | `record_id, error` | **P4：当前被 `_ =` 吞没** |
| 审计查询 | DEBUG | `limit, returned_count` | 缺失 |

---

## 五、可观测性配套修复

### 5.1 P1：可观测性端点认证

**文件**：`cmd/server/server.go`

```go
// 修复前（L222-224）
http.HandleFunc("/api/audit", s.handleAudit)
http.HandleFunc("/api/traces", s.handleTraces)

// 修复后
http.HandleFunc("/api/audit", func(w http.ResponseWriter, r *http.Request) {
    if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
        return
    }
    s.handleAudit(w, r)
})
http.HandleFunc("/api/traces", func(w http.ResponseWriter, r *http.Request) {
    if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
        return
    }
    s.handleTraces(w, r)
})

// /metrics 使用独立 token 或 IP 白名单（不强制 admin，供 Prometheus 抓取）
http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
    if metricsToken := config.Getenv("METRICS_TOKEN"); metricsToken != "" {
        if r.Header.Get("Authorization") != "Bearer "+metricsToken {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
    }
    w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
    fmt.Fprint(w, observability.DefaultMetrics.PrometheusText())
})

// /healthz 保持无认证（健康检查不应需要鉴权）
```

### 5.2 P2：tool_call trace span 补全

**文件**：`internal/runtime/engine.go`，`executeTool()` 函数内

```go
func (e *Engine) executeTool(tc llm.ToolCall) (string, error) {
    e.stepIdx++

    // --- 新增：tool trace span ---
    var toolTraceCtx *observability.TraceContext
    if e.cfg.Tracer != nil && e.rootTraceCtx != nil {
        toolTraceCtx = e.cfg.Tracer.StartChild(
            e.rootTraceCtx, e.cfg.AgentID, "tool:"+tc.Function.Name)
    }
    // ... 参数解析 ...

    start := time.Now()
    // ... tool 执行 ...
    duration := time.Since(start).Milliseconds()

    // --- 新增：完成 tool span ---
    if e.cfg.Tracer != nil && toolTraceCtx != nil {
        attrs := map[string]any{
            "tool":        tc.Function.Name,
            "duration_ms": duration,
        }
        if execErr != nil {
            attrs["error"] = execErr.Error()
            e.cfg.Tracer.FinishWithAttributes(toolTraceCtx, execErr, attrs)
        } else {
            e.cfg.Tracer.FinishWithAttributes(toolTraceCtx, nil, attrs)
        }
    }
    // ... 其余逻辑不变 ...
}
```

### 5.3 P4：审计写入错误不再吞没

**文件**：`internal/observability/audit_sqlite.go`

```go
func (a *SQLiteAuditor) Record(rec AuditRecord) {
    a.inner.Record(rec)
    if err := db.InsertAuditRecord(db.AuditRecord{
        ID: rec.ID, Timestamp: rec.Timestamp, Actor: rec.Actor,
        Action: rec.Action, Target: rec.Target,
        Before: rec.Before, After: rec.After, Reason: rec.Reason, IP: rec.IP,
    }); err != nil {
        // 不用 DefaultLogger 避免循环依赖；用 stdlib log.Printf
        log.Printf("[AUDIT] CRITICAL: failed to persist audit record id=%s action=%s error=%v",
            rec.ID, rec.Action, err)
    }
}
```

> 注意：此处不能用 `DefaultLogger` 因为 `obs.go` 的 `DefaultLogger` 初始化可能晚于 `DefaultAuditor`。
> 迁移到 slog 后可安全使用 `slog.Error(...)`。

### 5.4 P5：onSpan 背压控制

**文件**：`internal/observability/trace.go`

```go
type Tracer struct {
    mu       sync.Mutex
    spans    []SpanRecord
    limit    int
    onSpanCh chan SpanRecord   // 新增：有界 channel
    onSpan   func(SpanRecord)
    wg       sync.WaitGroup
}

func NewTracer(limit int) *Tracer {
    if limit <= 0 {
        limit = 1000
    }
    t := &Tracer{limit: limit}
    return t
}

func (t *Tracer) SetOnSpan(fn func(SpanRecord)) {
    t.mu.Lock()
    defer t.mu.Unlock()
    if fn == nil {
        if t.onSpanCh != nil {
            close(t.onSpanCh)
            t.wg.Wait()
            t.onSpanCh = nil
        }
        t.onSpan = nil
        return
    }
    t.onSpan = fn
    // 有界 channel + 单消费者 goroutine，背压上限 256
    t.onSpanCh = make(chan SpanRecord, 256)
    t.wg.Add(1)
    go func() {
        defer t.wg.Done()
        for rec := range t.onSpanCh {
            fn(rec)
        }
    }()
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
            // 可选：递增 droppedSpanCount metric
        }
    }
}
```

### 5.5 P8：Histogram bucket 扩展

**文件**：`internal/observability/obs.go`

```go
func NewMetricsCollector() *MetricsCollector {
    // LLM 延迟可能有 30-60s 长尾
    llmBuckets := []float64{50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000, 120000}
    // Tool 延迟通常较短
    toolBuckets := []float64{10, 50, 100, 250, 500, 1000, 5000, 10000}
    return &MetricsCollector{
        llmLatencyHist:  NewHistogramCollector(llmBuckets),
        toolLatencyHist: NewHistogramCollector(toolBuckets),
    }
}
```

### 5.6 P9：PrometheusText 锁外格式化

**文件**：`internal/observability/obs.go`

```go
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
    llmHistSnap := m.llmLatencyHist.snapshot()
    toolHistSnap := m.toolLatencyHist.snapshot()
    m.mu.RUnlock()

    // 2. 锁外格式化
    ts := uint64(time.Now().UnixMilli())
    out := fmt.Sprintf(`# HELP ...`, snap.tasksStarted, ts, ...)
    out += formatHistogram("llm_latency_ms", "LLM call latency in milliseconds.", llmHistSnap)
    out += formatHistogram("tool_latency_ms", "Tool execution latency in milliseconds.", toolHistSnap)
    return out
}
```

需要给 `HistogramCollector` 增加 `snapshot()` 方法返回值拷贝。

### 5.7 P7：Metrics 启动回填（已实施）

**动机**：`MetricsCollector` 是纯内存实现，进程重启即归零。Prometheus 把 counter
突降解释为 counter reset，导致 `rate()` / `increase()` 在重启点断档。

**实现**（3 部分）：

1. **`pkg/db/metrics.go`（新增）**——聚合查询，返回 `TaskCounts` / `LLMUsage`：
   - `AggregateTaskCounts()`：单条 SQL 统计 tasks 表，`Started` = 全部行、
     `Completed` = `status='completed'`、`Failed` = `status IN ('failed','cancelled')`
     （与 `IncrTasksFailed` 覆盖"失败或被取消"的语义对齐）。
   - `AggregateLLMUsage()`：聚合 cost_records 的 `COUNT(*)` 与 token/`cost_cents`
     整数列求和，避免浮点累加漂移。
   - 该文件**刻意不引入 `internal/observability`**：observability 的
     `audit_sqlite.go` 依赖 `pkg/db`，pkg/db 必须保持叶子包，否则形成 import 环。

2. **`internal/observability/obs.go`**——`SeedTaskCounts` / `SeedLLMUsage`（已有）。

3. **`cmd/server/main.go`**——新增 `seedMetricsFromDB()`，在 DB 初始化分支内调用。
   **顺序约束**：必须排在 `repairStaleRunningTasks()` 之后，因为后者会把遗留
   running task 改判为 failed；先回填会漏计这批任务，导致 `/metrics` 与 DB 口径不一致。
   任一聚合失败只告警不阻断启动（全新 DB / 无持久化模式下需正常降级）。

---

## 六、配置体系完善

### 6.1 新增配置项

**`.env.example` 更新**：

```bash
# ---------------------------------------------------------------------------
# Logging & Observability (Phase 8)
# ---------------------------------------------------------------------------

# --- 日志级别（控制台与文件独立配置）---
LOG_CONSOLE_LEVEL=info       # trace/debug/info/warn/error/fatal/panic
LOG_FILE_LEVEL=trace         # 文件留全量，事后排查
LOG_ADD_SOURCE=true          # 注入 caller file:line

# --- 日志文件轮转 ---
LOG_FILE=logs/server.log
LOG_FILE_MAX_SIZE_MB=100
LOG_FILE_MAX_BACKUPS=7
LOG_FILE_MAX_AGE_DAYS=30
LOG_FILE_COMPRESS=true

# --- Trace / Audit 缓冲 ---
TRACE_BUFFER_LIMIT=2000      # 实际从环境变量读取（修复 P3）
AUDIT_BUFFER_LIMIT=10000     # 实际从环境变量读取（修复 P3）

# --- Metrics 端点保护 ---
METRICS_TOKEN=               # 为空则不鉴权；设值后 /metrics 需 Bearer token
```

### 6.2 配置读取代码

**`cmd/server/main.go`** 替换硬编码：

```go
// 修复前 (main.go:63)
tracer = observability.NewTracer(2000)

// 修复后
traceLimit := parseIntEnv("TRACE_BUFFER_LIMIT", 2000)
tracer = observability.NewTracer(traceLimit)

// 修复前 (main.go:389)
observability.DefaultAuditor = observability.NewSQLiteAuditor(
    observability.NewMemoryAuditor(10000))

// 修复后
auditLimit := parseIntEnv("AUDIT_BUFFER_LIMIT", 10000)
observability.DefaultAuditor = observability.NewSQLiteAuditor(
    observability.NewMemoryAuditor(auditLimit))

// 日志初始化替换 initDualLogging
observability.DefaultLogger = observability.NewLogger(observability.LogConfig{
    ConsoleLevel: observability.ParseLogLevel(config.Getenv("LOG_CONSOLE_LEVEL")),
    FileLevel:    observability.ParseLogLevel(config.Getenv("LOG_FILE_LEVEL")),
    FilePath:     config.Getenv("LOG_FILE"),
    MaxSizeMB:    parseIntEnv("LOG_FILE_MAX_SIZE_MB", 100),
    MaxBackups:   parseIntEnv("LOG_FILE_MAX_BACKUPS", 7),
    MaxAgeDays:   parseIntEnv("LOG_FILE_MAX_AGE_DAYS", 30),
    Compress:     parseBoolEnv("LOG_FILE_COMPRESS", true),
    AddSource:    parseBoolEnv("LOG_ADD_SOURCE", true),
})

func parseIntEnv(key string, def int) int {
    if v := config.Getenv(key); v != "" {
        if n, err := strconv.Atoi(v); err == nil {
            return n
        }
    }
    return def
}

func parseBoolEnv(key string, def bool) bool {
    if v := config.Getenv(key); v != "" {
        if b, err := strconv.ParseBool(v); err == nil {
            return b
        }
    }
    return def
}
```

---

## 七、迁移与兼容策略

### 7.1 新增依赖

```
go get gopkg.in/natefinch/lumberjack.v2
```

这是唯一新增依赖，用于文件轮转。slog 是标准库，无新增。
lumberjack 是 Go 生态最成熟的日志轮转库，单文件、零依赖、~500 行。

### 7.2 迁移分三阶段

**Phase 8-1：日志库替换（不改变调用点）**
1. 在 `obs.go` 中实现新 `Logger`（slog 封装）
2. 保留旧 `StructuredLogger` 类型名，内部委托给新 `Logger`
3. 旧 `DefaultLogger.Info(component, msg, fields)` 签名通过适配层继续工作
4. 替换 `initDualLogging` 为 `NewLogger`
5. 所有现有代码**无需修改**即可获得多 sink + 轮转 + caller

**Phase 8-2：log.Printf 迁移**
1. 全局搜索 `log.Printf` / `log.Fatalf`，逐文件迁移到 `DefaultLogger`
2. 按严重程度分配级别（当前全是"无级别"的 Printf）
3. 优先迁移 `api.go`(46处)、`engine.go`(29处)、`runner.go`(16处)
4. `main.go` 的启动横幅可保留 `fmt.Printf`（非错误信息）

**Phase 8-3：新级别与埋点补充**
1. 启用 TRACE 级别，为 ReAct 步骤、MCP 消息添加 TRACE 埋点
2. 将 `// DEBUG log.Printf` 注释改为 `DefaultLogger.Debug(...)`
3. 补充第四章列出的所有缺失埋点
4. 平衡级别分布（目标：Debug 40%、Info 30%、Warn 20%、Error 10%）

### 7.3 测试策略

1. **现有测试不破坏**：`trace_test.go`、`audit_test.go`、`histogram_test.go` 无需改动
2. **新增 logger 测试**：`logger_test.go` 验证多 sink 级别过滤、caller 注入、轮转触发
3. **新增端到端验证**：
   - 启动 server，确认控制台输出 Text 格式、文件输出 JSON 格式
   - 设置 `LOG_CONSOLE_LEVEL=warn`，确认控制台不再显示 info
   - 设置 `LOG_FILE_LEVEL=trace`，确认文件包含 trace 级日志
   - 触发 tool 执行，确认 `/api/traces` 返回含 `tool:*` span
   - curl `/api/audit` 无 token，确认 401

---

## 八、实施步骤与验收标准

### 步骤清单

| 步骤 | 文件 | 内容 | 对应问题 |
|------|------|------|---------|
| S1 | `go.mod` | 添加 lumberjack 依赖 | P6 |
| S2 | `internal/observability/obs.go` | 新增 `Logger`（slog 封装）、7 级 API、`LogConfig` | 日志库替换 |
| S3 | `internal/observability/obs.go` | 保留 `StructuredLogger` 为兼容别名，委托给 `Logger` | 兼容 |
| S4 | `internal/observability/obs.go` | 扩展 `LogLevel` 增加 TRACE/PANIC，更新 `ParseLogLevel` | 多级别 |
| S5 | `internal/observability/obs.go` | 扩展 histogram bucket（LLM 120s / Tool 10s） | P8 |
| S6 | `internal/observability/obs.go` | `PrometheusText` 改为锁内快照 + 锁外格式化 | P9 |
| S7 | `internal/observability/trace.go` | `onSpan` 改为有界 channel + 单消费者 | P5 |
| S8 | `internal/observability/audit_sqlite.go` | 审计写入错误记录到日志 | P4 |
| S9 | `internal/observability/logger_test.go` | 新增 Logger 测试 | 测试 |
| S10 | `cmd/server/main.go` | 替换 `initDualLogging` 为 `NewLogger`，从 env 读配置 | P3/P6 |
| S11 | `cmd/server/main.go` | 从 env 读取 `TRACE_BUFFER_LIMIT` / `AUDIT_BUFFER_LIMIT` | P3 |
| S12 | `cmd/server/server.go` | `/api/audit` `/api/traces` 加 admin 鉴权 | P1 |
| S13 | `cmd/server/server.go` | `/metrics` 加可选 Bearer token | P1 |
| S14 | `internal/runtime/engine.go` | `executeTool` 添加 trace span | P2 |
| S15 | `internal/runtime/engine.go` | ReAct 步骤日志埋点（TRACE/DEBUG/INFO/ERROR） | 埋点补充 |
| S16 | `internal/llm/*.go` | LLM Provider 日志埋点 | 埋点补充 |
| S17 | `internal/orchestrator/*.go` | 编排日志埋点 | 埋点补充 |
| S18 | `internal/tool/mcp/*.go` | MCP 日志埋点 | 埋点补充 |
| S19 | `internal/ws/*.go` | WebSocket 日志迁移 | 埋点补充 |
| S20 | `cmd/server/api.go` | 46 处 `log.Printf` 迁移到 `DefaultLogger` | 迁移 |
| S21 | `cmd/server/main.go` | 54 处 `log.Printf` 迁移到 `DefaultLogger` | 迁移 |
| S22 | `cmd/server/runner.go` | 16 处 `log.Printf` 迁移到 `DefaultLogger` | 迁移 |
| S23 | 其余文件 | 散布的 `log.Printf` 迁移 | 迁移 |
| S24 | `.env.example` | 更新所有新增配置项 | 配置 |
| S25 | `cmd/server/main.go` + `pkg/db/metrics.go` | Metrics 启动回填 ✅ | P7 |

### 验收标准

| 编号 | 标准 | 验证方法 |
|------|------|---------|
| V1 | 控制台输出 Text 格式，文件输出 JSON 格式 | 启动 server，对比 stdout 与 `logs/server.log` |
| V2 | 控制台和文件可独立配置不同级别 | 设 `LOG_CONSOLE_LEVEL=warn`，确认 stdout 无 info |
| V3 | 日志含 `caller` (file:line) | grep 日志文件确认 caller 字段存在 |
| V4 | 日志文件自动轮转 | 写入超过 `LOG_FILE_MAX_SIZE_MB` 后确认产生 `.1.gz` |
| V5 | TRACE 级别可用且可过滤 | 设 `LOG_FILE_LEVEL=trace`，确认文件含 ReAct 步骤 trace |
| V6 | FATAL 级别调用后进程退出 | 模拟配置加载失败，确认进程退出且日志有 fatal 记录 |
| V7 | `executeTool` 产生 `tool:*` trace span | curl `/api/traces` 确认含 tool span |
| V8 | `/api/audit` 无认证返回 401 | curl 不带 token 访问 |
| V9 | `/metrics` 有 token 时需 Bearer | 设 `METRICS_TOKEN=xxx`，curl 验证 |
| V10 | `TRACE_BUFFER_LIMIT` 从 env 生效 | 设为 10，产生 20 个 span 后 `/api/traces` 返回 ≤10 |
| V11 | `AUDIT_BUFFER_LIMIT` 从 env 生效 | 设为 5，产生 10 条审计后 `/api/audit` 返回 ≤5 |
| V12 | 审计写入失败有日志 | 模拟 DB 错误，确认日志含 `[AUDIT] CRITICAL` |
| V13 | LLM latency P99 不再固定 10000ms | 产生 >10s 延迟后 `curl /metrics` 确认 bucket 分布 |
| V14 | `log.Printf` 残留 ≤5 处（仅启动横幅） | `grep -r "log\.Printf" cmd/ internal/ pkg/ \| wc -l` |
| V15 | 现有测试全部通过 | `go test ./internal/observability/...` |
| V16 | 新增 logger 测试通过 | `go test ./internal/observability/... -run TestLogger` |
| V17 | Metrics 启动回填生效（P7/S25） | `go test ./cmd/server/ -run TestSeedMetrics`；重启后 `/metrics` 的 `agent_tasks_total` 不归零 |

---

## 附录 A：slog Attr 便捷构造

为减少迁移工作量，提供常用 Attr 的便捷函数：

```go
// 在 obs.go 中提供
func F(key string, val any) slog.Attr    { return slog.Any(key, val) }
func S(key, val string) slog.Attr        { return slog.String(key, val) }
func I(key string, val int) slog.Attr    { return slog.Int(key, val) }
func I64(key string, val int64) slog.Attr{ return slog.Int64(key, val) }
func D(key string, val time.Duration) slog.Attr {
    return slog.String(key, val.String())
}
func Err(err error) slog.Attr { return slog.String("error", err.Error()) }
```

## 附录 B：trace_id 自动注入

利用 slog 的 context 传播，在 ReAct loop 中通过 `context.WithValue` 携带 trace_id：

```go
// observability/trace_ctx.go
type traceCtxKey struct{}

func ContextWithTrace(ctx context.Context, traceID, taskID string) context.Context {
    return context.WithValue(ctx, traceCtxKey{}, struct{ TraceID, TaskID string }{traceID, taskID})
}

// 在 Logger 的 handler middleware 中自动提取
func injectTraceFromCtx(ctx context.Context, attrs []slog.Attr) []slog.Attr {
    if v, ok := ctx.Value(traceCtxKey{}).(struct{ TraceID, TaskID string }); ok {
        attrs = append(attrs, slog.String("trace_id", v.TraceID), slog.String("task_id", v.TaskID))
    }
    return attrs
}
```

这样所有在 ReAct loop 内发出的日志都自动携带 `trace_id` / `task_id`，
日志与 trace 可直接关联。
