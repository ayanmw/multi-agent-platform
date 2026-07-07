// Package observability provides structured logging and metrics for the platform.
package observability

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
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

// DefaultLogger is the package-level shared logger.
var DefaultLogger = NewStructuredLogger()
