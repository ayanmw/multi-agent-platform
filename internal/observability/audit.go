package observability

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// AuditRecord captures a write operation for compliance and forensics.
type AuditRecord struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Actor     string         `json:"actor"`            // user/api_key/agent id
	Action    string         `json:"action"`           // e.g. delete_session, write_file
	Target    string         `json:"target"`           // resource id / path
	Before    map[string]any `json:"before,omitempty"`
	After     map[string]any `json:"after,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	IP        string         `json:"ip,omitempty"`
}

// Auditor is the interface for audit logging.
type Auditor interface {
	Record(rec AuditRecord)
	List(limit int) []AuditRecord
}

// MemoryAuditor keeps audit records in a bounded ring buffer.
type MemoryAuditor struct {
	mu      sync.RWMutex
	records []AuditRecord
	limit   int
}

// NewMemoryAuditor creates an in-memory auditor.
func NewMemoryAuditor(limit int) *MemoryAuditor {
	if limit <= 0 {
		limit = 10000
	}
	return &MemoryAuditor{limit: limit}
}

func (a *MemoryAuditor) Record(rec AuditRecord) {
	if rec.ID == "" {
		rec.ID = generateAuditID()
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, rec)
	if len(a.records) > a.limit {
		a.records = a.records[len(a.records)-a.limit:]
	}
}

func (a *MemoryAuditor) List(limit int) []AuditRecord {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if limit <= 0 || limit > len(a.records) {
		limit = len(a.records)
	}
	out := make([]AuditRecord, limit)
	copy(out, a.records[len(a.records)-limit:])
	return out
}

// JSON returns the latest N records as JSON.
func (a *MemoryAuditor) JSON(limit int) ([]byte, error) {
	return json.Marshal(a.List(limit))
}

func generateAuditID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "audit_" + hex.EncodeToString(b)
}
