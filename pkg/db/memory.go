// Package db —— Memory 基础设施的持久化层。
//
// 本文件提供 memories 和 memory_links 表的 CRUD 操作，
// 用于实现 4 层 memory 系统中的 Consolidated Episodic 与 Semantic 层：
//
//	1. Working Memory  —— 单任务上下文（内存中，不在此持久化）
//	2. Raw Episodic     —— 对话记录（conversations 表，已存在）
//	3. Consolidated Episodic —— 任务摘要、候选经验（memories，tier=consolidated）
//	4. Semantic/Policy  —— 稳定规则、偏好（memories，tier=semantic）
//
// Memory 记录通过 PromotionGate（internal/harness/promotion.go）从
// consolidated 层晋升到 semantic 层，基于以下三个通道：
//   - repeated_across_sessions: 有 ≥2 个独立任务支持同一经验
//   - tool_failure_evidence:    关联事件的 tool status=failed
//   - explicit_user_instruction: 用户明确表示"始终这样做" / durable=true
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MemoryTypes 是 Memory CRUD API 接受的 memory type 取值白名单。
// 以包级 slice 形式维护，便于 handler 校验用户输入时无需在多个文件里硬编码字符串。
var MemoryTypes = []string{
	"preference",
	"rule",
	"fact",
	"lesson",
	"reflection",
	"session_summary",
	"heartbeat_state",
}

// validMemoryTypeSet 是由 MemoryTypes 派生的查找 map，供 IsValidMemoryType 做 O(1) 存在性校验。
var validMemoryTypeSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(MemoryTypes))
	for _, t := range MemoryTypes {
		m[t] = struct{}{}
	}
	return m
}()

// IsValidMemoryType 返回 t 是否为已识别的 memory type 值。
// API handler 用它在早期阶段拒绝未知的 "type" 值。
func IsValidMemoryType(t string) bool {
	_, ok := validMemoryTypeSet[t]
	return ok
}

// MemoryRecord 对应 memories 表。
// 它要么保存一条 consolidated episodic 摘要（tier=consolidated），
// 要么保存一条稳定的 semantic 规则/偏好（tier=semantic）。
type MemoryRecord struct {
	ID              string     `json:"id"`
	ProjectID       string     `json:"project_id"`
	Scope           string     `json:"scope"`           // session | project | global（Phase 5-B）
	SessionID       string     `json:"session_id"`
	Type            string     `json:"type"`            // preference | rule | fact | lesson | reflection
	Tier            string     `json:"tier"`            // consolidated | semantic
	Content         string     `json:"content"`
	Embedding       []byte     `json:"embedding,omitempty"` // Phase 6+ 向量
	Confidence      float64    `json:"confidence"`
	Status          string     `json:"status"`          // active | obsolete | cold | invalid
	SourceTaskIDs   []string   `json:"source_task_ids"`
	SourceEventIDs  []string   `json:"source_event_ids"`
	PromotionReason string     `json:"promotion_reason"`
	AccessCount     int        `json:"access_count"`
	LastAccessed    *time.Time `json:"last_accessed"`
	LastReviewed    *time.Time `json:"last_reviewed"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// MemoryLinkRecord 对应 memory_links 表。
// Link 表示 memory 记录之间的关系：supplements、contradicts、relates_to 或 replaces。
type MemoryLinkRecord struct {
	SourceID  string    `json:"source_id"`
	TargetID  string    `json:"target_id"`
	Relation  string    `json:"relation"` // supplements | contradicts | relates_to | replaces
	CreatedAt time.Time `json:"created_at"`
}

// PostInsertMemoryHook 在 InsertMemory 成功插入后被调用。
// 由 cmd/server/main.go 在外部注入，用于串联 MemoryIndexer。
var PostInsertMemoryHook func(memoryID, content string)

// InsertMemory 创建一条新的 memory 记录。
// 所有字段直接写入；调用方需在调用前自行设置默认值（tier、status、confidence）。
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

// QueryMemoriesByProject 返回某个 project 的全部 memory 记录，
// 按 updated_at 倒序排列（最近更新的在前）。
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

// QueryMemoriesByTier 返回某个 project 中指定 tier（如 "consolidated" 或 "semantic"）
// 的全部 memory 记录，按 updated_at 倒序排列。
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

// QueryMemoriesByTaskID 返回 source_task_ids JSON 数组中包含指定 taskID 的全部
// memory 记录。使用 SQLite 的 JSON 函数进行匹配。
func QueryMemoriesByTaskID(taskID string) ([]MemoryRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	// 使用 json_each 检查 taskID 是否出现在 source_task_ids 数组中
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

// QueryMemoryByID 按 ID 返回单条 memory 记录。
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

// UpdateMemoryAccess 递增 access_count 并将 last_accessed 设为当前时间。
// 在某条 memory 被取出用于某个 task 的上下文时调用。
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

// UpdateMemoryStatus 修改某条 memory 记录的 status（例如改为 "obsolete" 或 "cold"）。
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

// UpdateMemoryTier 将 memory 记录晋升或降级到其它 tier。
// 由 PromotionGate 在把 consolidated memory 晋升为 semantic 时使用。
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

// DeleteMemory 按 ID 删除一条 memory 记录。
func DeleteMemory(id string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`DELETE FROM memories WHERE id = ?`, id)
	return err
}

// UpdateMemoryContent 替换某条 memory 的内容并更新 updated_at。
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

// UpdateMemoryConfidence 替换 confidence 分数并更新 updated_at。
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

// CountMemoriesByFilter 返回匹配给定过滤条件的 memory 记录总数。
// 空字符串被视为对该字段"不加过滤"。
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

// ListMemoriesPaged 返回分页的 memory 记录切片及匹配总数。
// 支持 scope、tier、status、type 等可组合的过滤条件；空字符串表示"不过滤"。
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

// CountMemoriesGrouped 返回按 tier、scope、status 分组的聚合计数。
// map 的 key 遵循 "tier_<value>"、"scope_<value>"、"status_<value>" 的命名约定，
// 以便调用方在新增维度时不会发生 key 冲突。
// 注意：projectID 过滤的是基础集合；各维度都是对该集合做的计数。
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

// TopAccessedMemories 返回某个 project 中访问次数最多的 N 条 memory，
// 按 access_count 倒序排列。并列时按 updated_at 倒序，使较新的 memory 排名更高。
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

// scanMemoryRows 将 sql.Rows 扫描为 MemoryRecord 切片。它是 memory.go 内部的
// 私有辅助函数，供本文件内的调用方避免重复列清单与 JSON 反序列化逻辑。
// memory_scope.go 出于同样目的使用自己的 scanMemoryRecords。
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

// InsertMemoryLink 在两条 memory 记录之间建立关系。
// relation 描述 link 的类型：supplements、contradicts、relates_to 或 replaces。
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

// QueryMemoryLinks 返回从给定 source memory ID 出发的全部 link。
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

// QueryMemoryLinksByTarget 返回指向给定 target memory ID 的全部 link。
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

// DeleteMemoryLink 删除两条 memory 记录之间的一条 link。
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

// QueryCompletedTaskIDs 返回自给定 checkpoint 时间之后、状态为 completed（status=completed）
// 的 task ID 列表，按 completed_at 升序排列。
// Heartbeat 用它发现可用于 episode 摘要的新任务。
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

// QueryConversationsByTask 返回某个 task 的全部 conversation 记录，
// 按 created_at 升序排列。Heartbeat 用它来为 task episode 生成摘要。
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

// ConversationRecord 对应 conversations 表的一行。
// Heartbeat 在读取 raw episodic 数据以生成摘要时使用。
type ConversationRecord struct {
	ID        string
	TaskID    string
	Role      string
	Content   string
	CreatedAt time.Time
}

// QueryStepsByTaskForMemory 返回与 memory 摘要相关的某个 task 的 steps。
// 过滤为 tool_call 和 observation 类型，用于提取任务的关键事件。
func QueryStepsByTaskForMemory(taskID string) ([]StepRecord, error) {
	return QueryStepsByTask(taskID)
}