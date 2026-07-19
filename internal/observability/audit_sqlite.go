package observability

import "github.com/anmingwei/multi-agent-platform/pkg/db"

// SQLiteAuditor 包装一个 Auditor，并同时持久化到 SQLite。
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
