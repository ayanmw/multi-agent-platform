package cases

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/harness"
)

// Repository provides SQLite CRUD operations for the cases table.
// 负责自定义用例的持久化；内置用例不进入此 Repository，仅作为种子由 Service 管理。
type Repository struct {
	db *sql.DB
}

// NewRepository creates a new case repository wrapping the given sql.DB.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// CountAll returns the total number of case rows (built-in + custom).
func (r *Repository) CountAll() (int, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM cases`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count cases: %w", err)
	}
	return count, nil
}

// generateCaseID generates a short random id prefixed with "case-".
// 使用 crypto/rand 产生 16 字节 hex，避免引入额外依赖。
func generateCaseID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate case id: %w", err)
	}
	return "case-" + hex.EncodeToString(b), nil
}

// toJSONString marshals the value to JSON. Callers that want `null`/`[]`/`{}`
// must initialize nil slices/maps before calling this helper.
func toJSONString(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal json: %w", err)
	}
	return string(data), nil
}

// parseTags unmarshals a JSON array of tags, returning nil on empty/null.
func parseTags(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return nil, nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(s), &tags); err != nil {
		return nil, fmt.Errorf("unmarshal tags: %w", err)
	}
	return tags, nil
}

// scanCases scans a rows result set into a slice of Case values.
func scanCases(rows *sql.Rows) ([]Case, error) {
	defer rows.Close()
	var cases []Case
	for rows.Next() {
		c, err := scanCaseFromRows(rows)
		if err != nil {
			return nil, err
		}
		cases = append(cases, *c)
	}
	return cases, rows.Err()
}

// scanCaseFromRows scans the current row into a *Case. It is used by both
// scanCase (single-row) and scanCases (multi-row) to avoid duplication.
func scanCaseFromRows(scanner interface {
	Scan(dest ...any) error
}) (*Case, error) {
	var c Case
	var contractJSON, tagsJSON string
	var isBuiltin int
	if err := scanner.Scan(
		&c.ID,
		&c.Name,
		&c.Description,
		&c.Icon,
		&c.Category,
		&c.SystemPrompt,
		&c.DefaultInput,
		&contractJSON,
		&tagsJSON,
		&isBuiltin,
		&c.CreatedAt,
		&c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(contractJSON), &c.Contract); err != nil {
		return nil, fmt.Errorf("unmarshal contract: %w", err)
	}
	tags, err := parseTags(tagsJSON)
	if err != nil {
		return nil, err
	}
	c.Tags = tags
	c.IsBuiltin = isBuiltin != 0
	return &c, nil
}

// scanCase scans a cases table row into a Case value.
func scanCase(row *sql.Row) (*Case, error) {
	return scanCaseFromRows(row)
}

// Create inserts a new custom case into the cases table.
func (r *Repository) Create(c Case) (*Case, error) {
	if c.ID == "" {
		id, err := generateCaseID()
		if err != nil {
			return nil, err
		}
		c.ID = id
	}
	now := time.Now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}

	contractJSON, err := toJSONString(c.Contract)
	if err != nil {
		return nil, err
	}
	tagsJSON, err := toJSONString(c.Tags)
	if err != nil {
		return nil, err
	}

	_, err = r.db.Exec(`
		INSERT INTO cases (id, name, description, icon, category, system_prompt, default_input, contract_json, tags_json, is_builtin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.Description, c.Icon, c.Category, c.SystemPrompt, c.DefaultInput,
		contractJSON, tagsJSON, boolToInt(c.IsBuiltin), c.CreatedAt, c.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert case: %w", err)
	}
	return &c, nil
}

// GetByID returns a custom case by id, or sql.ErrNoRows if not found.
func (r *Repository) GetByID(id string) (*Case, error) {
	row := r.db.QueryRow(`
		SELECT id, name, description, icon, category, system_prompt, default_input, contract_json, tags_json, is_builtin, created_at, updated_at
		FROM cases WHERE id = ?`, id)
	return scanCase(row)
}

// List returns all custom cases from the database, optionally filtered by category.
// 内置用例（is_builtin=1）由代码管理，不在这里返回，避免 Service 合并时重复。
func (r *Repository) List(category string) ([]Case, error) {
	query := `
		SELECT id, name, description, icon, category, system_prompt, default_input, contract_json, tags_json, is_builtin, created_at, updated_at
		FROM cases WHERE is_builtin = 0`
	var args []any
	if strings.TrimSpace(category) != "" {
		query += ` AND category = ?`
		args = append(args, category)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query cases: %w", err)
	}
	return scanCases(rows)
}

// Update updates all mutable fields of a custom case.
func (r *Repository) Update(c Case) (*Case, error) {
	c.UpdatedAt = time.Now()
	contractJSON, err := toJSONString(c.Contract)
	if err != nil {
		return nil, err
	}
	tagsJSON, err := toJSONString(c.Tags)
	if err != nil {
		return nil, err
	}

	res, err := r.db.Exec(`
		UPDATE cases SET name = ?, description = ?, icon = ?, category = ?, system_prompt = ?, default_input = ?, contract_json = ?, tags_json = ?, updated_at = ?
		WHERE id = ?`,
		c.Name, c.Description, c.Icon, c.Category, c.SystemPrompt, c.DefaultInput,
		contractJSON, tagsJSON, c.UpdatedAt, c.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("update case: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return nil, sql.ErrNoRows
	}
	return &c, nil
}

// Delete removes a custom case by id.
func (r *Repository) Delete(id string) error {
	res, err := r.db.Exec(`DELETE FROM cases WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete case: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// boolToInt converts a bool to an int for SQLite storage.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// CaseEvaluation records the result of evaluating a completed task against its case.
type CaseEvaluation struct {
	ID          int64     `json:"id,omitempty"`
	TaskID      string    `json:"task_id"`
	CaseID      string    `json:"case_id"`
	Passed      bool      `json:"passed"`
	Score       float64   `json:"score"`
	Reason      string    `json:"reason"`
	EvaluatedAt time.Time `json:"evaluated_at"`
}

// SaveEvaluation inserts a case evaluation into the case_evaluations table.
func (r *Repository) SaveEvaluation(eval CaseEvaluation) error {
	if eval.EvaluatedAt.IsZero() {
		eval.EvaluatedAt = time.Now()
	}
	_, err := r.db.Exec(`
		INSERT INTO case_evaluations (task_id, case_id, passed, score, reason, evaluated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		eval.TaskID, eval.CaseID, boolToInt(eval.Passed), eval.Score, eval.Reason, eval.EvaluatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert case_evaluation: %w", err)
	}
	return nil
}

// GetEvaluation returns the most recent evaluation for a given task and case.
func (r *Repository) GetEvaluation(taskID, caseID string) (*CaseEvaluation, error) {
	row := r.db.QueryRow(`
		SELECT id, task_id, case_id, passed, score, reason, evaluated_at
		FROM case_evaluations
		WHERE task_id = ? AND case_id = ?
		ORDER BY evaluated_at DESC, id DESC
		LIMIT 1`, taskID, caseID)

	var e CaseEvaluation
	var passed int
	var evaluatedAt string
	if err := row.Scan(&e.ID, &e.TaskID, &e.CaseID, &passed, &e.Score, &e.Reason, &evaluatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("select case_evaluation: %w", err)
	}
	e.Passed = passed != 0
	e.EvaluatedAt, _ = time.Parse("2006-01-02 15:04:05", evaluatedAt)
	return &e, nil
}

// Ensure interface compatibility: a new case should carry a valid contract when passed to services.
var _ = harness.TaskContract{}
