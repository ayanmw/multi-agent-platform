package main

// 本文件负责「进程启动期的日志与可观测性缓冲配置」，
// 是 Phase 8-1 把 log/slog 新日志器真正接进主流程的唯一入口。
//
// 拆成独立文件而不是塞进 main.go，是为了让「配置项 → 运行时行为」的映射
// 集中可读：新增一个 LOG_* 环境变量时只需要改这一处。

import (
	"strconv"
	"strings"

	"github.com/ayanmw/multi-agent-platform/internal/config"
	"github.com/ayanmw/multi-agent-platform/internal/observability"
)

// 日志与可观测性相关的默认值。
// 这些常量同时也是 .env.example 中注释掉的推荐值来源。
const (
	defaultLogFile           = "logs/server.log"
	defaultLogMaxSizeMB      = 100
	defaultLogMaxBackups     = 7
	defaultLogMaxAgeDays     = 30
	defaultTraceBufferLimit  = 2000
	defaultAuditBufferLimit  = 10000
	defaultLogCompressRotate = false
	defaultLogAddSource      = false
)

// envInt 读取一个整型环境变量。缺失、为空或无法解析时返回 def。
// 解析失败会打印告警而不是静默回退，避免配置写错却毫无提示。
func envInt(key string, def int) int {
	raw := strings.TrimSpace(config.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		log.Infof("server", "Config: %s=%q is not a valid integer, using default %d", key, raw, def)
		return def
	}
	return v
}

// envBool 读取一个布尔型环境变量，接受 1/t/true/yes/on 及其反义词。
func envBool(key string, def bool) bool {
	raw := strings.ToLower(strings.TrimSpace(config.Getenv(key)))
	switch raw {
	case "":
		return def
	case "1", "t", "true", "y", "yes", "on":
		return true
	case "0", "f", "false", "n", "no", "off":
		return false
	default:
		log.Infof("server", "Config: %s=%q is not a valid boolean, using default %v", key, raw, def)
		return def
	}
}

// envLogLevel 读取日志级别；为空时回退到 fallback 而不是硬编码 info，
// 这样 LOG_CONSOLE_LEVEL 缺省时可以自然继承 LOG_LEVEL。
func envLogLevel(key string, fallback observability.LogLevel) observability.LogLevel {
	raw := strings.TrimSpace(config.Getenv(key))
	if raw == "" {
		return fallback
	}
	return observability.ParseLogLevel(raw)
}

// buildLogConfig 依据环境变量组装 LogConfig。
//
// 级别解析规则（S11）：
//   - LOG_LEVEL 是总开关，缺省 info；
//   - LOG_CONSOLE_LEVEL / LOG_FILE_LEVEL 缺省时各自继承 LOG_LEVEL，
//     设置后可以做到「控制台只看 info+，文件留 debug 全量」；
//   - LOG_FILE 为空字符串表示显式关闭文件 sink（只写控制台）。
func buildLogConfig() observability.LogConfig {
	base := observability.ParseLogLevel(config.Getenv("LOG_LEVEL"))

	// LOG_FILE 未设置时用默认路径；显式设为空串则关闭文件日志。
	filePath := defaultLogFile
	if res := config.LookupEnv("LOG_FILE"); res.InOS || res.InDotEnv {
		filePath = strings.TrimSpace(config.Getenv("LOG_FILE"))
	}

	return observability.LogConfig{
		ConsoleLevel: envLogLevel("LOG_CONSOLE_LEVEL", base),
		FileLevel:    envLogLevel("LOG_FILE_LEVEL", base),
		FilePath:     filePath,
		MaxSizeMB:    envInt("LOG_MAX_SIZE_MB", defaultLogMaxSizeMB),
		MaxBackups:   envInt("LOG_MAX_BACKUPS", defaultLogMaxBackups),
		MaxAgeDays:   envInt("LOG_MAX_AGE_DAYS", defaultLogMaxAgeDays),
		Compress:     envBool("LOG_COMPRESS", defaultLogCompressRotate),
		AddSource:    envBool("LOG_ADD_SOURCE", defaultLogAddSource),
	}
}

// initLogging 构建新的多 sink Logger 并替换 observability.DefaultLogger 的底层实现。
//
// 之所以是「替换 inner」而不是「替换 DefaultLogger 变量」：全仓有大量
// `observability.DefaultLogger.Info(...)` 的直接引用，替换变量会在并发下产生
// 数据竞争，而 Replace 只动内部指针，调用点无需任何修改即可获得
// 多 sink + 轮转 + caller 能力。
//
// 返回的 *Logger 供调用方注册到 shutdownManager，进程退出时刷盘并关闭文件句柄。
func initLogging() *observability.Logger {
	cfg := buildLogConfig()
	logger := observability.NewLogger(cfg)
	observability.DefaultLogger.Replace(logger)

	log.Infof("server", "Logging: console=%s file=%s path=%q rotate=%dMB/%d/%dd compress=%v caller=%v",
		cfg.ConsoleLevel, cfg.FileLevel, cfg.FilePath,
		cfg.MaxSizeMB, cfg.MaxBackups, cfg.MaxAgeDays, cfg.Compress, cfg.AddSource)
	return logger
}
