package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// QueryMemoriesByScope returns all active memory records for a given project and scope.
// scope values: "session", "project", "global"
func QueryMemoriesByScope(projectID, scope string) ([]MemoryRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, project_id, scope, session_id, type, tier, content, COALESCE(embedding,''),
		 COALESCE(confidence,1.0), COALESCE(status,'active'),
		 COALESCE(source_task_ids,'[]'), COALESCE(source_event_ids,'[]'),
		 COALESCE(promotion_reason,''),
		 COALESCE(access_count,0), last_accessed, last_reviewed,
		 created_at, updated_at
		 FROM memories WHERE project_id=? AND scope=? ORDER BY updated_at DESC`,
		projectID, scope,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records, err := scanMemoryRecords(rows)
	if err != nil {
		return nil, err
	}
	return records, nil
}

// QueryMemoriesByScopeAndTier returns memory records filtered by project, scope, and tier.
func QueryMemoriesByScopeAndTier(projectID, scope, tier string) ([]MemoryRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, project_id, scope, session_id, type, tier, content, COALESCE(embedding,''),
		 COALESCE(confidence,1.0), COALESCE(status,'active'),
		 COALESCE(source_task_ids,'[]'), COALESCE(source_event_ids,'[]'),
		 COALESCE(promotion_reason,''),
		 COALESCE(access_count,0), last_accessed, last_reviewed,
		 created_at, updated_at
		 FROM memories WHERE project_id=? AND scope=? AND tier=? ORDER BY updated_at DESC`,
		projectID, scope, tier,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records, err := scanMemoryRecords(rows)
	if err != nil {
		return nil, err
	}
	return records, nil
}

// QueryMemoriesByScopeAndSession returns session-scoped memories filtered by session ID.
// Session-scoped memories have scope='session' and session_id matching the given session.
func QueryMemoriesByScopeAndSession(projectID, sessionID, scope string) ([]MemoryRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, project_id, scope, session_id, type, tier, content, COALESCE(embedding,''),
		 COALESCE(confidence,1.0), COALESCE(status,'active'),
		 COALESCE(source_task_ids,'[]'), COALESCE(source_event_ids,'[]'),
		 COALESCE(promotion_reason,''),
		 COALESCE(access_count,0), last_accessed, last_reviewed,
		 created_at, updated_at
		 FROM memories
		 WHERE project_id=? AND scope=? AND session_id=?
		 ORDER BY updated_at DESC`,
		projectID, scope, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records, err := scanMemoryRecords(rows)
	if err != nil {
		return nil, err
	}
	return records, nil
}

// scanMemoryRecords is a shared helper that scans sql.Rows into a slice of
// MemoryRecord. It is used by scope-aware query functions to avoid duplicating
// the column list and unmarshaling logic.
func scanMemoryRecords(rows *sql.Rows) ([]MemoryRecord, error) {
	var records []MemoryRecord
	for rows.Next() {
		var r MemoryRecord
		var embedding []byte
		var sourceTaskIDsJSON, sourceEventIDsJSON string
		var lastAccessed, lastReviewed sql.NullTime
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Scope, &r.SessionID, &r.Type, &r.Tier, &r.Content,
			&embedding, &r.Confidence, &r.Status,
			&sourceTaskIDsJSON, &sourceEventIDsJSON, &r.PromotionReason,
			&r.AccessCount, &lastAccessed, &lastReviewed,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Embedding = embedding
		json.Unmarshal([]byte(sourceTaskIDsJSON), &r.SourceTaskIDs)
		json.Unmarshal([]byte(sourceEventIDsJSON), &r.SourceEventIDs)
		if lastAccessed.Valid {
			r.LastAccessed = &lastAccessed.Time
		}
		if lastReviewed.Valid {
			r.LastReviewed = &lastReviewed.Time
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// UpdateMemoryScope changes the scope of a memory record (session | project | global).
func UpdateMemoryScope(id, scope, sessionID string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now()
	_, err := DB.Exec(
		`UPDATE memories SET scope = ?, session_id = ?, updated_at = ? WHERE id = ?`,
		scope, sessionID, now, id,
	)
	return err
}
