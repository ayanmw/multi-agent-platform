package skill

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store 是对底层 skills 表的轻量级封装，直接复用全局 db.DB。
// 负责将 internal/skill.Skill 持久化到 SQLite，屏蔽 JSON 序列化细节。
//
// 为避免 internal/skill 与 pkg/db 之间的循环依赖，Store 在本包内直接执行 SQL。
type Store struct {
	db *sql.DB
}

// NewStore 创建一个新的 Store 实例。
// 调用方需先通过 db.Init 初始化全局 DB；本函数会引用 sql.DB 指针。
func NewStore(sqlDB *sql.DB) *Store {
	return &Store{db: sqlDB}
}

// Save 将 Skill 保存到数据库（存在则覆盖）。
func (st *Store) Save(s *Skill) error {
	if st.db == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now().Unix()
	if s.CreatedAt == 0 {
		s.CreatedAt = now
	}
	s.UpdatedAt = now

	authorsJSON, _ := json.Marshal(s.Authors)
	tagsJSON, _ := json.Marshal(s.Tags)
	templatesJSON, _ := json.Marshal(s.Templates)
	parametersJSON, _ := json.Marshal(s.Parameters)
	requiredToolsJSON, _ := json.Marshal(s.RequiredTools)
	suggestedToolsJSON, _ := json.Marshal(s.SuggestedTools)
	permissionsJSON, _ := json.Marshal(s.Permissions)
	triggersJSON, _ := json.Marshal(s.Triggers)

	_, err := st.db.Exec(
		`INSERT OR REPLACE INTO skills (
			id, version, display_name, description,
			authors_json, tags_json, source, source_url, is_local_editable,
			templates_json, parameters_json,
			required_tools_json, suggested_tools_json, permissions_json,
			triggers_json, state, invalid_reason,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Version, s.DisplayName, s.Description,
		string(authorsJSON), string(tagsJSON), string(s.Source), s.SourceURL, s.IsLocalEditable,
		string(templatesJSON), string(parametersJSON),
		string(requiredToolsJSON), string(suggestedToolsJSON), string(permissionsJSON),
		string(triggersJSON), string(s.State), s.InvalidReason,
		s.CreatedAt, s.UpdatedAt,
	)
	return err
}

// Get 根据 id 从数据库读取单个 Skill。
func (st *Store) Get(id string) (*Skill, error) {
	if st.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	row := st.db.QueryRow(`SELECT id, version, display_name, description,
			authors_json, tags_json, source, source_url, is_local_editable,
			templates_json, parameters_json,
			required_tools_json, suggested_tools_json, permissions_json,
			triggers_json, state, invalid_reason,
			created_at, updated_at
		 FROM skills WHERE id = ?`, id)
	s, err := scanSkill(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("skill not found")
		}
		return nil, err
	}
	return &s, nil
}

// Delete 根据 id 删除 Skill。
func (st *Store) Delete(id string) error {
	if st.db == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := st.db.Exec(`DELETE FROM skills WHERE id = ?`, id)
	return err
}

// ListBySource 按来源列出所有 Skill。传空则不过滤。
func (st *Store) ListBySource(source SkillSource) ([]Skill, error) {
	if st.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	query := `SELECT id, version, display_name, description,
			authors_json, tags_json, source, source_url, is_local_editable,
			templates_json, parameters_json,
			required_tools_json, suggested_tools_json, permissions_json,
			triggers_json, state, invalid_reason,
			created_at, updated_at
		  FROM skills`
	var args []any
	if source != "" {
		query += " WHERE source = ?"
		args = append(args, string(source))
	}
	query += " ORDER BY updated_at DESC"
	return st.querySkills(query, args...)
}

// ListAll 列出所有 Skill。
func (st *Store) ListAll() ([]Skill, error) {
	return st.ListBySource("")
}

func (st *Store) querySkills(query string, args ...any) ([]Skill, error) {
	rows, err := st.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Skill
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func scanSkill(scanner interface{ Scan(dest ...any) error }) (Skill, error) {
	var s Skill
	var sourceStr, stateStr string
	var authorsJSON, tagsJSON, templatesJSON, parametersJSON string
	var requiredToolsJSON, suggestedToolsJSON, permissionsJSON, triggersJSON string

	err := scanner.Scan(
		&s.ID, &s.Version, &s.DisplayName, &s.Description,
		&authorsJSON, &tagsJSON, &sourceStr, &s.SourceURL, &s.IsLocalEditable,
		&templatesJSON, &parametersJSON,
		&requiredToolsJSON, &suggestedToolsJSON, &permissionsJSON,
		&triggersJSON, &stateStr, &s.InvalidReason,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return Skill{}, err
	}
	s.Source = SkillSource(sourceStr)
	s.State = SkillState(stateStr)

	json.Unmarshal([]byte(authorsJSON), &s.Authors)
	json.Unmarshal([]byte(tagsJSON), &s.Tags)
	json.Unmarshal([]byte(templatesJSON), &s.Templates)
	json.Unmarshal([]byte(parametersJSON), &s.Parameters)
	json.Unmarshal([]byte(requiredToolsJSON), &s.RequiredTools)
	json.Unmarshal([]byte(suggestedToolsJSON), &s.SuggestedTools)
	json.Unmarshal([]byte(permissionsJSON), &s.Permissions)
	json.Unmarshal([]byte(triggersJSON), &s.Triggers)

	return s, nil
}

func joinConditions(conditions []string) string {
	return strings.Join(conditions, " AND ")
}
