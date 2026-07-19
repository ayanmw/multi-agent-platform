// Package cost 提供 cost 记录的持久化接口与实现。
//
// CostRepository 是 HTTP 层与 Engine 集成用于查询和存储 cost 记录的抽象。
// 默认的生产实现写入由 migration v10 创建（并由 v11 扩展）的 SQLite cost_records 表。
package cost

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CostRepository 抽象 CostRecord 的持久化。
type CostRepository interface {
	// Insert 存储一条新的 cost 记录。
	Insert(record CostRecord) error
	// QueryByTask 返回某个 task 的所有记录，按创建时间排序。
	QueryByTask(taskID string) ([]CostRecord, error)
	// QueryBySession 返回某个 session 的所有记录。
	QueryBySession(sessionID string) ([]CostRecord, error)
	// QueryByProject 返回某个 project 的所有记录。
	QueryByProject(projectID string) ([]CostRecord, error)
	// QueryRecent 返回跨所有 task 的最近 N 条记录。
	QueryRecent(limit int) ([]CostRecord, error)
}

// InMemoryCostRepository 是一个线程安全的内存存储，用于测试，以及在没有
// SQLite 数据库可用时作为 fallback。进程退出时记录会丢失。
type InMemoryCostRepository struct {
	mu      sync.RWMutex
	records []CostRecord
}

// NewInMemoryCostRepository 创建一个内存型 cost repository。
func NewInMemoryCostRepository() *InMemoryCostRepository {
	return &InMemoryCostRepository{
		records: make([]CostRecord, 0),
	}
}

// Insert 将一条记录追加到内存缓存。
func (r *InMemoryCostRepository) Insert(record CostRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record)
	return nil
}

// QueryByTask 返回匹配 taskID 的记录。
func (r *InMemoryCostRepository) QueryByTask(taskID string) ([]CostRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []CostRecord
	for _, rec := range r.records {
		if rec.TaskID == taskID {
			out = append(out, rec)
		}
	}
	return out, nil
}

// QueryBySession 返回匹配 sessionID 的记录。
func (r *InMemoryCostRepository) QueryBySession(sessionID string) ([]CostRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []CostRecord
	for _, rec := range r.records {
		if rec.SessionID == sessionID {
			out = append(out, rec)
		}
	}
	return out, nil
}

// QueryByProject 返回匹配 projectID 的记录。
func (r *InMemoryCostRepository) QueryByProject(projectID string) ([]CostRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []CostRecord
	for _, rec := range r.records {
		if rec.ProjectID == projectID {
			out = append(out, rec)
		}
	}
	return out, nil
}

// QueryRecent 返回最近的 limit 条记录。
func (r *InMemoryCostRepository) QueryRecent(limit int) ([]CostRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if limit <= 0 || limit > len(r.records) {
		limit = len(r.records)
	}
	start := len(r.records) - limit
	return append([]CostRecord(nil), r.records[start:]...), nil
}

// SqliteCostRepository 将 cost 记录持久化到 SQLite cost_records 表。
type SqliteCostRepository struct {
	db *sql.DB
}

// NewSqliteCostRepository 创建一个 SQLite 后端的 repository。
func NewSqliteCostRepository(db *sql.DB) (*SqliteCostRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	return &SqliteCostRepository{db: db}, nil
}

// Insert 将一条 CostRecord 写入 cost_records 表。
func (r *SqliteCostRepository) Insert(record CostRecord) error {
	if record.ID == "" {
		record.ID = "cost_" + uuid.New().String()
	}
	_, err := r.db.Exec(`
		INSERT INTO cost_records (
			id, task_id, session_id, project_id, agent_id, step_index,
			model, provider, tier, input_tokens, output_tokens, total_tokens,
			cost_usd, cost_cents, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.TaskID, record.SessionID, record.ProjectID, record.AgentID, record.StepIndex,
		record.Model, record.Provider, record.Tier, record.InputTokens, record.OutputTokens, record.TotalTokens,
		record.CostUSD, record.CostCents, record.CreatedAt.Format(time.RFC3339),
	)
	return err
}

// QueryByTask 返回所有匹配 taskID 的记录，按 created_at 排序。
func (r *SqliteCostRepository) QueryByTask(taskID string) ([]CostRecord, error) {
	return r.queryWhere("task_id = ? ORDER BY created_at", taskID)
}

// QueryBySession 返回所有匹配 sessionID 的记录，按 created_at 排序。
func (r *SqliteCostRepository) QueryBySession(sessionID string) ([]CostRecord, error) {
	return r.queryWhere("session_id = ? ORDER BY created_at", sessionID)
}

// QueryByProject 返回所有匹配 projectID 的记录，按 created_at 排序。
func (r *SqliteCostRepository) QueryByProject(projectID string) ([]CostRecord, error) {
	return r.queryWhere("project_id = ? ORDER BY created_at", projectID)
}

// QueryRecent 返回按 created_at 排序的最近 N 条记录。
func (r *SqliteCostRepository) QueryRecent(limit int) ([]CostRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	return r.queryWhere("1=1 ORDER BY created_at DESC LIMIT ?", limit)
}

func (r *SqliteCostRepository) queryWhere(where string, args ...any) ([]CostRecord, error) {
	rows, err := r.db.Query(fmt.Sprintf("SELECT id, task_id, session_id, project_id, agent_id, step_index, model, provider, tier, input_tokens, output_tokens, total_tokens, cost_usd, cost_cents, created_at FROM cost_records WHERE %s", where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []CostRecord
	for rows.Next() {
		var rec CostRecord
		var created string
		if err := rows.Scan(
			&rec.ID, &rec.TaskID, &rec.SessionID, &rec.ProjectID, &rec.AgentID, &rec.StepIndex,
			&rec.Model, &rec.Provider, &rec.Tier, &rec.InputTokens, &rec.OutputTokens, &rec.TotalTokens,
			&rec.CostUSD, &rec.CostCents, &created,
		); err != nil {
			return nil, err
		}
		rec.CreatedAt, _ = time.Parse(time.RFC3339, created)
		records = append(records, rec)
	}
	return records, rows.Err()
}
