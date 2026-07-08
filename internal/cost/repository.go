// Package cost provides persistence interfaces and implementations for cost records.
//
// CostRepository is the abstraction used by the HTTP layer and Engine integration
// to query and store cost records. The default production implementation writes
// to the SQLite cost_records table created by migration v10 (and extended by v11).
package cost

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CostRepository abstracts persistence for CostRecords.
type CostRepository interface {
	// Insert stores a new cost record.
	Insert(record CostRecord) error
	// QueryByTask returns all records for a task, ordered by creation time.
	QueryByTask(taskID string) ([]CostRecord, error)
	// QueryBySession returns all records for a session.
	QueryBySession(sessionID string) ([]CostRecord, error)
	// QueryByProject returns all records for a project.
	QueryByProject(projectID string) ([]CostRecord, error)
	// QueryRecent returns the most recent N records across all tasks.
	QueryRecent(limit int) ([]CostRecord, error)
}

// InMemoryCostRepository is a thread-safe in-memory store used for tests and as a
// fallback when no SQLite database is available. Records are lost on process exit.
type InMemoryCostRepository struct {
	mu      sync.RWMutex
	records []CostRecord
}

// NewInMemoryCostRepository creates an in-memory cost repository.
func NewInMemoryCostRepository() *InMemoryCostRepository {
	return &InMemoryCostRepository{
		records: make([]CostRecord, 0),
	}
}

// Insert appends a record to the in-memory cache.
func (r *InMemoryCostRepository) Insert(record CostRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record)
	return nil
}

// QueryByTask returns records matching taskID.
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

// QueryBySession returns records matching sessionID.
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

// QueryByProject returns records matching projectID.
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

// QueryRecent returns the most recent limit records.
func (r *InMemoryCostRepository) QueryRecent(limit int) ([]CostRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if limit <= 0 || limit > len(r.records) {
		limit = len(r.records)
	}
	start := len(r.records) - limit
	return append([]CostRecord(nil), r.records[start:]...), nil
}

// SqliteCostRepository persists cost records to the SQLite cost_records table.
type SqliteCostRepository struct {
	db *sql.DB
}

// NewSqliteCostRepository creates a SQLite-backed repository.
func NewSqliteCostRepository(db *sql.DB) (*SqliteCostRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	return &SqliteCostRepository{db: db}, nil
}

// Insert writes a CostRecord to the cost_records table.
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

// QueryByTask returns all records matching taskID ordered by created_at.
func (r *SqliteCostRepository) QueryByTask(taskID string) ([]CostRecord, error) {
	return r.queryWhere("task_id = ? ORDER BY created_at", taskID)
}

// QueryBySession returns all records matching sessionID ordered by created_at.
func (r *SqliteCostRepository) QueryBySession(sessionID string) ([]CostRecord, error) {
	return r.queryWhere("session_id = ? ORDER BY created_at", sessionID)
}

// QueryByProject returns all records matching projectID ordered by created_at.
func (r *SqliteCostRepository) QueryByProject(projectID string) ([]CostRecord, error) {
	return r.queryWhere("project_id = ? ORDER BY created_at", projectID)
}

// QueryRecent returns the most recent N records by created_at.
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
