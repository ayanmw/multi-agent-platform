package llm

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SqliteMockScriptStore 将 mock 脚本持久化到 SQLite。
type SqliteMockScriptStore struct {
	db *sql.DB
}

// NewSqliteMockScriptStore 创建一个 SQLite 后端的 mock 脚本 store。
func NewSqliteMockScriptStore(db *sql.DB) *SqliteMockScriptStore {
	return &SqliteMockScriptStore{db: db}
}

// List 返回所有已存储的 mock 脚本。
func (s *SqliteMockScriptStore) List() ([]MockScript, error) {
	rows, err := s.db.Query(`SELECT id, case_id, priority, match_input, responses, created_at, updated_at FROM mock_scripts ORDER BY priority DESC, updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list mock scripts: %w", err)
	}
	defer rows.Close()

	var list []MockScript
	for rows.Next() {
		var script MockScript
		var matchInputJSON, responsesJSON string
		if err := rows.Scan(&script.ID, &script.CaseID, &script.Priority, &matchInputJSON, &responsesJSON, &script.CreatedAt, &script.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan mock script: %w", err)
		}
		if matchInputJSON != "" {
			_ = json.Unmarshal([]byte(matchInputJSON), &script.MatchInput)
		}
		if responsesJSON != "" {
			_ = json.Unmarshal([]byte(responsesJSON), &script.Responses)
		}
		list = append(list, script)
	}
	return list, rows.Err()
}

// Get 按 ID 返回单个脚本。
func (s *SqliteMockScriptStore) Get(id string) (MockScript, error) {
	row := s.db.QueryRow(`SELECT id, case_id, priority, match_input, responses, created_at, updated_at FROM mock_scripts WHERE id = ?`, id)
	var script MockScript
	var matchInputJSON, responsesJSON string
	if err := row.Scan(&script.ID, &script.CaseID, &script.Priority, &matchInputJSON, &responsesJSON, &script.CreatedAt, &script.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return MockScript{}, fmt.Errorf("mock script %q not found", id)
		}
		return MockScript{}, fmt.Errorf("get mock script: %w", err)
	}
	if matchInputJSON != "" {
		_ = json.Unmarshal([]byte(matchInputJSON), &script.MatchInput)
	}
	if responsesJSON != "" {
		_ = json.Unmarshal([]byte(responsesJSON), &script.Responses)
	}
	return script, nil
}

// Save 持久化一个脚本，ID 为空时分配随机 ID。
func (s *SqliteMockScriptStore) Save(script MockScript) (MockScript, error) {
	if script.ID == "" {
		script.ID = fmt.Sprintf("mock_%d", time.Now().UnixNano())
	}
	script.UpdatedAt = time.Now()
	if script.CreatedAt.IsZero() {
		script.CreatedAt = script.UpdatedAt
	}

	matchInputJSON, _ := json.Marshal(script.MatchInput)
	responsesJSON, _ := json.Marshal(script.Responses)

	_, err := s.db.Exec(`
		INSERT INTO mock_scripts (id, case_id, priority, match_input, responses, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			case_id = excluded.case_id,
			priority = excluded.priority,
			match_input = excluded.match_input,
			responses = excluded.responses,
			updated_at = excluded.updated_at`,
		script.ID, script.CaseID, script.Priority, string(matchInputJSON), string(responsesJSON), script.CreatedAt, script.UpdatedAt,
	)
	if err != nil {
		return MockScript{}, fmt.Errorf("save mock script: %w", err)
	}
	return script, nil
}

// Delete 按 ID 删除脚本。
func (s *SqliteMockScriptStore) Delete(id string) error {
	res, err := s.db.Exec(`DELETE FROM mock_scripts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete mock script: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("mock script %q not found", id)
	}
	return nil
}

// LoadBuiltin 用内置脚本初始化 store。
func (s *SqliteMockScriptStore) LoadBuiltin(scripts []MockScript) error {
	for _, script := range scripts {
		if _, err := s.Save(script); err != nil {
			return err
		}
	}
	return nil
}
