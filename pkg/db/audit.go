package db

import (
	"encoding/json"
	"fmt"
	"time"
)

// AuditRecord 对应 observability.AuditRecord。
type AuditRecord struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Actor     string         `json:"actor"`
	Action    string         `json:"action"`
	Target    string         `json:"target"`
	Before    map[string]any `json:"before"`
	After     map[string]any `json:"after"`
	Reason    string         `json:"reason"`
	IP        string         `json:"ip"`
}

// InsertAuditRecord 持久化一条 audit 记录。
func InsertAuditRecord(rec AuditRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	beforeJSON, _ := json.Marshal(rec.Before)
	afterJSON, _ := json.Marshal(rec.After)
	_, err := DB.Exec(
		`INSERT INTO audit_records (id, timestamp, actor, action, target, before_json, after_json, reason, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.Timestamp, rec.Actor, rec.Action, rec.Target, string(beforeJSON), string(afterJSON), rec.Reason, rec.IP,
	)
	return err
}

// ListAuditRecords 返回最近的 audit 记录，按 timestamp 倒序排列。
func ListAuditRecords(limit int) ([]AuditRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := DB.Query(`SELECT id, timestamp, actor, action, target, before_json, after_json, reason, ip
						   FROM audit_records ORDER BY timestamp DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRecord
	for rows.Next() {
		var r AuditRecord
		var beforeJSON, afterJSON string
		if err := rows.Scan(&r.ID, &r.Timestamp, &r.Actor, &r.Action, &r.Target, &beforeJSON, &afterJSON, &r.Reason, &r.IP); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(beforeJSON), &r.Before)
		json.Unmarshal([]byte(afterJSON), &r.After)
		out = append(out, r)
	}
	return out, rows.Err()
}
