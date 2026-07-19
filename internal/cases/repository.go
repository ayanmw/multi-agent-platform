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

// Repository 提供 cases 表的 SQLite CRUD 操作。
// Repository 负责 cases 表与 case_evaluations 表的持久化，仅管理自定义用例；
// 内置用例作为种子由 Service 合并，不写入此仓库。
type Repository struct {
	db *sql.DB
}

// NewRepository 创建一个包装给定 sql.DB 的 case repository。
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// CountAll 返回 case 行的总数（内置 + 自定义）。
// CountAll 返回 cases 表总行数，包括内置与自定义用例。
func (r *Repository) CountAll() (int, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM cases`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count cases: %w", err)
	}
	return count, nil
}

// generateCaseID 生成以 "case-" 为前缀的短随机 id。
// 使用 crypto/rand 产生 16 字节 hex，避免引入额外依赖。
func generateCaseID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate case id: %w", err)
	}
	return "case-" + hex.EncodeToString(b), nil
}

// toJSONString 将值序列化为 JSON。希望得到 `null`/`[]`/`{}` 的调用方
// 必须在调用此 helper 之前初始化 nil slice/map。
func toJSONString(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal json: %w", err)
	}
	return string(data), nil
}

// parseTags 反序列化 JSON 数组形式的 tags，空值或 null 时返回 nil。
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

// scanCases 将 rows 结果集扫描到 Case 值的 slice 中。
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

// scanCaseFromRows 将当前行扫描到 *Case。它被 scanCase（单行）和 scanCases
// （多行）共用，以避免重复代码。
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

// scanCase 将一行 cases 表数据扫描到 Case 值中。
func scanCase(row *sql.Row) (*Case, error) {
	return scanCaseFromRows(row)
}

// Create 插入一条新的自定义用例到 cases 表。
// Create 插入一条新的自定义用例；若 ID 为空则自动生成，并为时间戳填充默认值。
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

// GetByID 按 ID 返回自定义用例；未找到则返回 sql.ErrNoRows。
// GetByID 按 ID 查询自定义用例；未找到时返回 sql.ErrNoRows。
func (r *Repository) GetByID(id string) (*Case, error) {
	row := r.db.QueryRow(`
		SELECT id, name, description, icon, category, system_prompt, default_input, contract_json, tags_json, is_builtin, created_at, updated_at
		FROM cases WHERE id = ?`, id)
	return scanCase(row)
}

// List 返回数据库中所有自定义用例，可按 category 过滤。
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

// Update 更新自定义用例的所有可变字段。
// Update 更新自定义用例的所有可变字段；若未更新到任何行则返回 sql.ErrNoRows。
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

// Delete 按 id 删除自定义用例。
// Delete 删除指定 ID 的自定义用例；若不存在则返回 sql.ErrNoRows。
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

// boolToInt 将 bool 转换为 int，以便 SQLite 存储。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// CaseEvaluation 记录一个已完成 task 针对其 case 的评估结果。
type CaseEvaluation struct {
	ID          int64     `json:"id,omitempty"`
	TaskID      string    `json:"task_id"`
	CaseID      string    `json:"case_id"`
	Passed      bool      `json:"passed"`
	Score       float64   `json:"score"`
	Reason      string    `json:"reason"`
	EvaluatedAt time.Time `json:"evaluated_at"`
}

// SaveEvaluation 将一条 case evaluation 插入 case_evaluations 表。
// SaveEvaluation 保存任务针对用例的评估结果；若 EvaluatedAt 为空则设为当前时间。
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

// GetEvaluation 返回给定 task 与 case 的最新评估。
// GetEvaluation 返回指定任务与用例的最新评估记录；SQLite 的 DATETIME 直接扫描为 time.Time。
func (r *Repository) GetEvaluation(taskID, caseID string) (*CaseEvaluation, error) {
	row := r.db.QueryRow(`
		SELECT id, task_id, case_id, passed, score, reason, evaluated_at
		FROM case_evaluations
		WHERE task_id = ? AND case_id = ?
		ORDER BY evaluated_at DESC, id DESC
		LIMIT 1`, taskID, caseID)

	var e CaseEvaluation
	var passed int
	var evaluatedAt time.Time
	if err := row.Scan(&e.ID, &e.TaskID, &e.CaseID, &passed, &e.Score, &e.Reason, &evaluatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("select case_evaluation: %w", err)
	}
	e.Passed = passed != 0
	e.EvaluatedAt = evaluatedAt
	return &e, nil
}

// 保证接口兼容性：传给各 service 的新的 case 应携带有效的 contract。
var _ = harness.TaskContract{}
