package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// QueryMemoriesByScope 返回某个 project 中、指定 scope 下的全部 active memory 记录。
// scope 取值："session"、"project"、"global"
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

// QueryMemoriesByScopeAndTier 返回按 project、scope、tier 过滤的 memory 记录。
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

// QueryMemoriesByScopeAndSession 返回按 session ID 过滤的 session 作用域 memory。
// session 作用域的 memory 满足 scope='session' 且 session_id 匹配给定 session。
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

// scanMemoryRecords 是一个共享辅助函数，用于把 sql.Rows 扫描为 MemoryRecord 切片。
// scope 感知的查询函数通过它避免重复列清单和反序列化逻辑。
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

// UpdateMemoryScope 修改 memory 记录的 scope（session | project | global）。
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
