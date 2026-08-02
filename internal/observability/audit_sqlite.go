package observability

import (
	"log/slog"

	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// SQLiteAuditor 包装一个 Auditor，并同时持久化到 SQLite。
type SQLiteAuditor struct {
	inner Auditor
}

func NewSQLiteAuditor(inner Auditor) *SQLiteAuditor {
	return &SQLiteAuditor{inner: inner}
}

func (a *SQLiteAuditor) Record(rec AuditRecord) {
	a.inner.Record(rec)
	// Phase 8 (P4)：审计写入失败不再静默吞没，记录到 stdlib log。
	// 此处不用 DefaultLogger 避免循环依赖（obs.go 的 DefaultLogger
	// 初始化可能晚于 DefaultAuditor）。迁移到 slog 后可安全使用 slog.Error。
	if err := db.InsertAuditRecord(db.AuditRecord{
		ID:        rec.ID,
		Timestamp: rec.Timestamp,
		Actor:     rec.Actor,
		Action:    rec.Action,
		Target:    rec.Target,
		Before:    rec.Before,
		After:     rec.After,
		Reason:    rec.Reason,
		IP:        rec.IP,
	}); err != nil {
		// V12：审计持久化失败必须保留 [AUDIT] CRITICAL 标记，便于告警与复盘检索。
		slog.Error("[AUDIT] CRITICAL: failed to persist audit record",
			"id", rec.ID, "action", rec.Action, "target", rec.Target, "error", err)
	}
}

func (a *SQLiteAuditor) List(limit int) []AuditRecord {
	return a.inner.List(limit)
}
