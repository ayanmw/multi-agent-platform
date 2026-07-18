package observability

import (
	"testing"
)

func TestMemoryAuditor(t *testing.T) {
	auditor := NewMemoryAuditor(10)
	auditor.Record(AuditRecord{
		Actor:  "user-1",
		Action: "delete_session",
		Target: "session-a",
	})
	recs := auditor.List(0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Actor != "user-1" {
		t.Fatalf("actor mismatch")
	}
}

func TestMemoryAuditorBounded(t *testing.T) {
	auditor := NewMemoryAuditor(2)
	for i := 0; i < 5; i++ {
		auditor.Record(AuditRecord{Action: "action", Target: "target"})
	}
	recs := auditor.List(0)
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
}
