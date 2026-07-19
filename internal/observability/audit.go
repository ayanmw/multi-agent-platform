package observability

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// AuditRecord 记录一次写操作，用于合规与取证。
type AuditRecord struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Actor     string         `json:"actor"`            // user/api_key/agent id
	Action    string         `json:"action"`           // 例如 delete_session、write_file
	Target    string         `json:"target"`           // 资源 id / 路径
	Before    map[string]any `json:"before,omitempty"`
	After     map[string]any `json:"after,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	IP        string         `json:"ip,omitempty"`
}

// Auditor 是 audit 日志记录的接口。
type Auditor interface {
	Record(rec AuditRecord)
	List(limit int) []AuditRecord
}

// MemoryAuditor 将 audit 记录保存在有界的 ring buffer(环形缓冲)中。
type MemoryAuditor struct {
	mu      sync.RWMutex
	records []AuditRecord
	limit   int
}

// NewMemoryAuditor 创建一个内存 audit 记录器。
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

// JSON 返回最近 N 条记录的 JSON 形式。
func (a *MemoryAuditor) JSON(limit int) ([]byte, error) {
	return json.Marshal(a.List(limit))
}

func generateAuditID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "audit_" + hex.EncodeToString(b)
}
