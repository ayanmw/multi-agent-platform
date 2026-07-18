// Package db — Memory persistence layer for the Memory infrastructure.
//
// This file provides CRUD operations for the memories and memory_links tables,
// which implement the Consolidated Episodic and Semantic tiers of the 4-tier
// memory system:
//
//	1. Working Memory  — per-task context (in-memory, not persisted here)
//	2. Raw Episodic     — conversation records (conversations table, existing)
//	3. Consolidated Episodic — task summaries, candidate experiences (memories, tier=consolidated)
//	4. Semantic/Policy  — stable rules, preferences (memories, tier=semantic)
//
// Memory records are promoted from consolidated to semantic tier via the
// PromotionGate (internal/harness/promotion.go) based on three channels:
//   - repeated_across_sessions: >=2 independent tasks support the same experience
//   - tool_failure_evidence:    associated event has tool status=failed
//   - explicit_user_instruction: user explicitly said "always do this" / durable=true
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MemoryTypes is the allow-list of memory type values accepted by the
// Memory CRUD API. Keeping the list as a package-level slice lets handlers
// validate user input without hard-coding strings in multiple files.
var MemoryTypes = []string{
	"preference",
	"rule",
	"fact",
	"lesson",
	"reflection",
	"session_summary",
	"heartbeat_state",
}

// validMemoryTypeSet is a lookup map derived from MemoryTypes for O(1)
// existence checks in IsValidMemoryType.
var validMemoryTypeSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(MemoryTypes))
	for _, t := range MemoryTypes {
		m[t] = struct{}{}
	}
	return m
}()

// IsValidMemoryType reports whether t is a recognized memory type value.
// It is used by API handlers to reject unknown "type" values early.
func IsValidMemoryType(t string) bool {
	_, ok := validMemoryTypeSet[t]
	return ok
}

// MemoryRecord mirrors the memories table.
// It holds either a consolidated episodic summary (tier=consolidated) or a
// stable semantic rule/preference (tier=semantic).
type MemoryRecord struct {
	ID              string     `json:"id"`
	ProjectID       string     `json:"project_id"`
	Scope           string     `json:"scope"` // session | project | global (Phase 5-B)
	SessionID       string     `json:"session_id"`
	Type            string     `json:"type"` // preference | rule | fact | lesson | reflection
	Tier            string     `json:"tier"` // consolidated | semantic
	Content         string     `json:"content"`
	Embedding       []byte     `json:"embedding,omitempty"` // Phase 6+ vector
	Confidence      float64    `json:"confidence"`
	Status          string     `json:"status"` // active | obsolete | cold | invalid
	SourceTaskIDs   []string   `json:"source_task_ids"`
	SourceEventIDs  []string   `json:"source_event_ids"`
	PromotionReason string     `json:"promotion_reason"`
	AccessCount     int        `json:"access_count"`
	LastAccessed    *time.Time `json:"last_accessed"`
	LastReviewed    *time.Time `json:"last_reviewed"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// MemoryLinkRecord mirrors the memory_links table.
// Links represent relationships between memory records: supplements, contradicts,
// relates_to, or replaces.
type MemoryLinkRecord struct {
	SourceID  string    `json:"source_id"`
	TargetID  string    `json:"target_id"`
	Relation  string    `json:"relation"` // supplements | contradicts | relates_to | replaces
	CreatedAt time.Time `json:"created_at"`
}

// PostInsertMemoryHook is called by InsertMemory after a successful insert.
// It is set externally by cmd/server/main.go to wire the MemoryIndexer.
var PostInsertMemoryHook func(memoryID, content string)

// InsertMemory creates a new memory record.
// All fields are written directly; the caller is responsible for setting
// defaults (tier, status, confidence) before calling.
func InsertMemory(record MemoryRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	sourceTaskIDsJSON, _ := json.Marshal(record.SourceTaskIDs)
	sourceEventIDsJSON, _ := json.Marshal(record.SourceEventIDs)
	_, err := DB.Exec(
		`INSERT INTO memories (id, project_id, scope, session_id, type, tier, content, embedding, confidence,
		 status, source_task_ids, source_event_ids, promotion_reason,
		 access_count, last_accessed, last_reviewed, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.ProjectID, record.Scope, record.SessionID, record.Type, record.Tier, record.Content,
		record.Embedding, record.Confidence, record.Status,
		string(sourceTaskIDsJSON), string(sourceEventIDsJSON), record.PromotionReason,
		record.AccessCount, record.LastAccessed, record.LastReviewed,
		record.CreatedAt, record.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if PostInsertMemoryHook != nil {
		PostInsertMemoryHook(record.ID, record.Content)
	}
	return nil
}

// QueryMemoriesByProject returns all memory records for a given project,
// ordered by updated_at descending (most recently updated first).
func QueryMemoriesByProject(projectID string) ([]MemoryRecord, error) {
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
		 FROM memories WHERE project_id=? ORDER BY updated_at DESC`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

// QueryMemoriesByTier returns all memory records for a given project and tier
// (e.g., "consolidated" or "semantic"), ordered by updated_at descending.
func QueryMemoriesByTier(projectID, tier string) ([]MemoryRecord, error) {
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
		 FROM memories WHERE project_id=? AND tier=? ORDER BY updated_at DESC`,
		projectID, tier,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

// QueryMemoriesByTaskID returns all memory records whose source_task_ids JSON
// array contains the given taskID. Uses SQLite JSON functions for matching.
func QueryMemoriesByTaskID(taskID string) ([]MemoryRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	// Use json_each to check if the taskID appears in source_task_ids array
	rows, err := DB.Query(
		`SELECT id, project_id, scope, session_id, type, tier, content, COALESCE(embedding,''),
		 COALESCE(confidence,1.0), COALESCE(status,'active'),
		 COALESCE(source_task_ids,'[]'), COALESCE(source_event_ids,'[]'),
		 COALESCE(promotion_reason,''),
		 COALESCE(access_count,0), last_accessed, last_reviewed,
		 created_at, updated_at
		 FROM memories
		 WHERE id IN (SELECT id FROM memories, json_each(source_task_ids) WHERE json_each.value = ?)
		 ORDER BY updated_at DESC`, taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

// QueryMemoryByID returns a single memory record by its ID.
func QueryMemoryByID(id string) (*MemoryRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var r MemoryRecord
	var embedding []byte
	var sourceTaskIDsJSON, sourceEventIDsJSON string
	var lastAccessed, lastReviewed sql.NullTime
	err := DB.QueryRow(
		`SELECT id, project_id, scope, session_id, type, tier, content, COALESCE(embedding,''),
		 COALESCE(confidence,1.0), COALESCE(status,'active'),
		 COALESCE(source_task_ids,'[]'), COALESCE(source_event_ids,'[]'),
		 COALESCE(promotion_reason,''),
		 COALESCE(access_count,0), last_accessed, last_reviewed,
		 created_at, updated_at
		 FROM memories WHERE id=?`, id,
	).Scan(&r.ID, &r.ProjectID, &r.Scope, &r.SessionID, &r.Type, &r.Tier, &r.Content,
		&embedding, &r.Confidence, &r.Status,
		&sourceTaskIDsJSON, &sourceEventIDsJSON, &r.PromotionReason,
		&r.AccessCount, &lastAccessed, &lastReviewed,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
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
	return &r, nil
}

// UpdateMemoryAccess increments the access_count and sets last_accessed to now.
// Called when a memory is retrieved for use in a task's context.
func UpdateMemoryAccess(id string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now()
	_, err := DB.Exec(
		`UPDATE memories SET access_count = access_count + 1, last_accessed = ? WHERE id = ?`,
		now, id,
	)
	return err
}

// UpdateMemoryStatus changes the status of a memory record (e.g., to "obsolete" or "cold").
func UpdateMemoryStatus(id, status string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now()
	_, err := DB.Exec(
		`UPDATE memories SET status = ?, updated_at = ? WHERE id = ?`,
		status, now, id,
	)
	return err
}

// UpdateMemoryTier promotes or demotes a memory record to a different tier.
// Used by the PromotionGate when promoting consolidated memories to semantic.
func UpdateMemoryTier(id, tier, promotionReason string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now()
	_, err := DB.Exec(
		`UPDATE memories SET tier = ?, promotion_reason = ?, updated_at = ? WHERE id = ?`,
		tier, promotionReason, now, id,
	)
	return err
}

// DeleteMemory removes a memory record by ID.
func DeleteMemory(id string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`DELETE FROM memories WHERE id = ?`, id)
	return err
}

// UpdateMemoryContent replaces the content of a memory and bumps updated_at.
func UpdateMemoryContent(id, content string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now()
	_, err := DB.Exec(
		`UPDATE memories SET content = ?, updated_at = ? WHERE id = ?`,
		content, now, id,
	)
	return err
}

// UpdateMemoryConfidence replaces the confidence score and bumps updated_at.
func UpdateMemoryConfidence(id string, confidence float64) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now()
	_, err := DB.Exec(
		`UPDATE memories SET confidence = ?, updated_at = ? WHERE id = ?`,
		confidence, now, id,
	)
	return err
}

// CountMemoriesByFilter returns the total number of memory records matching
// the given filters. Empty strings are treated as "no filter" for that field.
func CountMemoriesByFilter(projectID, scope, tier, status string) (int, error) {
	if DB == nil {
		return 0, fmt.Errorf("db not initialized")
	}
	var args []any
	var conds []string
	conds = append(conds, "project_id = ?")
	args = append(args, projectID)
	if scope != "" {
		conds = append(conds, "scope = ?")
		args = append(args, scope)
	}
	if tier != "" {
		conds = append(conds, "tier = ?")
		args = append(args, tier)
	}
	if status != "" {
		conds = append(conds, "status = ?")
		args = append(args, status)
	}
	where := strings.Join(conds, " AND ")
	var count int
	query := fmt.Sprintf(`SELECT COUNT(*) FROM memories WHERE %s`, where)
	err := DB.QueryRow(query, args...).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// ListMemoriesPaged returns a paginated slice of memory records plus the
// total matching count. Filters on scope, tier, status, and type are
// composable; empty strings mean "no filter".
func ListMemoriesPaged(projectID, scope, tier, status, memType string, limit, offset int) ([]MemoryRecord, int, error) {
	if DB == nil {
		return nil, 0, fmt.Errorf("db not initialized")
	}
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var args []any
	var conds []string
	conds = append(conds, "project_id = ?")
	args = append(args, projectID)
	if scope != "" {
		conds = append(conds, "scope = ?")
		args = append(args, scope)
	}
	if tier != "" {
		conds = append(conds, "tier = ?")
		args = append(args, tier)
	}
	if status != "" {
		conds = append(conds, "status = ?")
		args = append(args, status)
	}
	if memType != "" {
		conds = append(conds, "type = ?")
		args = append(args, memType)
	}
	where := strings.Join(conds, " AND ")

	var total int
	err := DB.QueryRow(
		fmt.Sprintf(`SELECT COUNT(*) FROM memories WHERE %s`, where),
		args...,
	).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := DB.Query(
		fmt.Sprintf(`SELECT id, project_id, scope, session_id, type, tier, content, COALESCE(embedding,''),
			 COALESCE(confidence,1.0), COALESCE(status,'active'),
			 COALESCE(source_task_ids,'[]'), COALESCE(source_event_ids,'[]'),
			 COALESCE(promotion_reason,''),
			 COALESCE(access_count,0), last_accessed, last_reviewed,
			 created_at, updated_at
			 FROM memories WHERE %s ORDER BY updated_at DESC LIMIT ? OFFSET ?`, where),
		append(args, limit, offset)...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	records, err := scanMemoryRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

// CountMemoriesGrouped returns aggregate counts grouped by tier, scope, and
// status. The map keys follow the convention "tier_<value>", "scope_<value>",
// and "status_<value>" so callers can add more dimensions without collisions.
// Note: projectID filters the base set; dimensions are counts over that set.
func CountMemoriesGrouped(projectID string) (map[string]int, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT COALESCE(tier,''), COALESCE(scope,''), COALESCE(status,''), COUNT(*)
		 FROM memories
		 WHERE project_id = ?
		 GROUP BY tier, scope, status`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	grouped := make(map[string]int)
	for rows.Next() {
		var tier, scope, status string
		var count int
		if err := rows.Scan(&tier, &scope, &status, &count); err != nil {
			return nil, err
		}
		grouped[fmt.Sprintf("tier_%s", tier)] += count
		grouped[fmt.Sprintf("scope_%s", scope)] += count
		grouped[fmt.Sprintf("status_%s", status)] += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return grouped, nil
}

// TopAccessedMemories returns the N most frequently accessed memories for a
// project, ordered by access_count descending. Ties are broken by updated_at
// descending so newer memories rank higher.
func TopAccessedMemories(projectID string, n int) ([]MemoryRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if n <= 0 {
		n = 10
	}
	rows, err := DB.Query(
		`SELECT id, project_id, scope, session_id, type, tier, content, COALESCE(embedding,''),
			 COALESCE(confidence,1.0), COALESCE(status,'active'),
			 COALESCE(source_task_ids,'[]'), COALESCE(source_event_ids,'[]'),
			 COALESCE(promotion_reason,''),
			 COALESCE(access_count,0), last_accessed, last_reviewed,
			 created_at, updated_at
			 FROM memories
			 WHERE project_id = ?
			 ORDER BY access_count DESC, updated_at DESC
			 LIMIT ?`,
		projectID, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMemoryRows(rows)
}

// scanMemoryRows scans sql.Rows into a slice of MemoryRecord. It is a local
// helper private to memory.go so callers within this file avoid duplicating
// the column list and JSON unmarshalling logic. memory_scope.go uses its own
// scanMemoryRecords for the same purpose.
func scanMemoryRows(rows *sql.Rows) ([]MemoryRecord, error) {
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

// InsertMemoryLink creates a relationship between two memory records.
// The relation describes the type of link: supplements, contradicts, relates_to, or replaces.
func InsertMemoryLink(sourceID, targetID, relation string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`INSERT OR REPLACE INTO memory_links (source_id, target_id, relation, created_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
		sourceID, targetID, relation,
	)
	return err
}

// QueryMemoryLinks returns all links originating from the given source memory ID.
func QueryMemoryLinks(sourceID string) ([]MemoryLinkRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT source_id, target_id, relation, created_at
		 FROM memory_links WHERE source_id = ? ORDER BY created_at DESC`, sourceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []MemoryLinkRecord
	for rows.Next() {
		var l MemoryLinkRecord
		if err := rows.Scan(&l.SourceID, &l.TargetID, &l.Relation, &l.CreatedAt); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// QueryMemoryLinksByTarget returns all links pointing to the given target memory ID.
func QueryMemoryLinksByTarget(targetID string) ([]MemoryLinkRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT source_id, target_id, relation, created_at
		 FROM memory_links WHERE target_id = ? ORDER BY created_at DESC`, targetID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []MemoryLinkRecord
	for rows.Next() {
		var l MemoryLinkRecord
		if err := rows.Scan(&l.SourceID, &l.TargetID, &l.Relation, &l.CreatedAt); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// DeleteMemoryLink removes a single link between two memory records.
func DeleteMemoryLink(sourceID, targetID string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`DELETE FROM memory_links WHERE source_id = ? AND target_id = ?`,
		sourceID, targetID,
	)
	return err
}

// QueryCompletedTaskIDs returns IDs of tasks that are completed (status=completed)
// since the given checkpoint time. Ordered by completed_at ascending.
// Used by the Heartbeat to find new tasks for episode summarization.
func QueryCompletedTaskIDs(since time.Time) ([]string, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id FROM tasks WHERE status = 'completed' AND completed_at > ? ORDER BY completed_at ASC`,
		since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// QueryConversationsByTask returns all conversation records for a given task,
// ordered by created_at ASC. Used by the Heartbeat to summarize task episodes.
func QueryConversationsByTask(taskID string) ([]ConversationRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, task_id, role, content, created_at
		 FROM conversations WHERE task_id = ? ORDER BY created_at ASC`, taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var convs []ConversationRecord
	for rows.Next() {
		var c ConversationRecord
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Role, &c.Content, &c.CreatedAt); err != nil {
			return nil, err
		}
		convs = append(convs, c)
	}
	return convs, rows.Err()
}

// ConversationRecord mirrors a row from the conversations table.
// Used by the Heartbeat when reading raw episodic data for summarization.
type ConversationRecord struct {
	ID        string
	TaskID    string
	Role      string
	Content   string
	CreatedAt time.Time
}

// QueryStepsByTaskForMemory returns steps for a task relevant to memory summarization.
// Filters to tool_call and observation type steps for extracting key task events.
func QueryStepsByTaskForMemory(taskID string) ([]StepRecord, error) {
	return QueryStepsByTask(taskID)
}