// skill.go — skills 表的迁移与 CRUD 操作。
//
// 本文件为 Skill 支持计划提供 SQLite 持久化层：
//   1. 通过 init() 向 migrations 切片注册 v24 迁移，创建 skills 表。
//   2. 提供 SaveSkill、GetSkill、ListSkills、DeleteSkill 四个 CRUD 函数。
//   3. 数组与复杂结构统一使用 JSON 文本存储，保持 schema 简洁。
//
// 所有时间戳以 Unix 秒整数存储，与 Skill 内部类型保持一致。
package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/skill"
)

// ErrSkillNotFound 表示指定的 Skill 不存在。
var ErrSkillNotFound = errors.New("skill not found")

// init 注册 skills 表的 schema 迁移（v24）。
// 使用 init() 注册可让 database.go 无需显式修改即可在 Init 时自动跑迁移。
func init() {
	migrations = append(migrations, Migration{
		Version:     24,
		Description: "Create skills table for skill support",
		SQL: `CREATE TABLE IF NOT EXISTS skills (
			id TEXT PRIMARY KEY,
			version TEXT NOT NULL DEFAULT '1.0.0',
			display_name TEXT NOT NULL,
			description TEXT DEFAULT '',
			authors_json TEXT DEFAULT '[]',
			tags_json TEXT DEFAULT '[]',
			source TEXT NOT NULL DEFAULT 'local_db',
			source_url TEXT DEFAULT '',
			is_local_editable BOOLEAN DEFAULT 1,
			templates_json TEXT DEFAULT '[]',
			parameters_json TEXT DEFAULT '[]',
			required_tools_json TEXT DEFAULT '[]',
			suggested_tools_json TEXT DEFAULT '[]',
			permissions_json TEXT DEFAULT '[]',
			triggers_json TEXT DEFAULT '{}',
			state TEXT NOT NULL DEFAULT 'discovered',
			invalid_reason TEXT DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_skills_source ON skills(source);
		CREATE INDEX IF NOT EXISTS idx_skills_state ON skills(state);
		CREATE INDEX IF NOT EXISTS idx_skills_updated_at ON skills(updated_at DESC);`,
	})
}

// SaveSkill 将 Skill 记录保存到数据库。
// 如果 id 已存在则执行 UPSERT（insert or replace）：完全覆盖旧记录并刷新 updated_at。
func SaveSkill(s skill.Skill) error {
	if DB == nil {
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

	_, err := DB.Exec(
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

// GetSkill 根据 id 读取单个 Skill。
// 返回 sql.ErrNoRows 表示未找到。
func GetSkill(id string) (skill.Skill, error) {
	if DB == nil {
		return skill.Skill{}, fmt.Errorf("db not initialized")
	}

	row := DB.QueryRow(`SELECT id, version, display_name, description,
			authors_json, tags_json, source, source_url, is_local_editable,
			templates_json, parameters_json,
			required_tools_json, suggested_tools_json, permissions_json,
			triggers_json, state, invalid_reason,
			created_at, updated_at
		 FROM skills WHERE id = ?`, id)
	s, err := scanSkill(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return skill.Skill{}, ErrSkillNotFound
		}
		return skill.Skill{}, err
	}
	return s, nil
}

// ListSkills 列出所有 Skill，按 updated_at 降序返回。
// 可传入可选过滤条件：source 或 state。空字符串表示不过滤。
func ListSkills(sourceFilter, stateFilter string) ([]skill.Skill, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}

	query := `SELECT id, version, display_name, description,
			authors_json, tags_json, source, source_url, is_local_editable,
			templates_json, parameters_json,
			required_tools_json, suggested_tools_json, permissions_json,
			triggers_json, state, invalid_reason,
			created_at, updated_at
		  FROM skills`

	var conditions []string
	var args []any
	if sourceFilter != "" {
		conditions = append(conditions, "source = ?")
		args = append(args, sourceFilter)
	}
	if stateFilter != "" {
		conditions = append(conditions, "state = ?")
		args = append(args, stateFilter)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY updated_at DESC"

	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var skills []skill.Skill
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		skills = append(skills, s)
	}
	return skills, rows.Err()
}

// DeleteSkill 根据 id 删除 Skill。
func DeleteSkill(id string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`DELETE FROM skills WHERE id = ?`, id)
	return err
}

// skillScanner 约束 scanSkill 可接受的行类型（*sql.Row 或 *sql.Rows）。
type skillScanner interface {
	Scan(dest ...any) error
}

// scanSkill 将一行 skills 表数据解析为 skill.Skill。
// 负责将 JSON 文本还原为切片和 Triggers 结构。
func scanSkill(scanner skillScanner) (skill.Skill, error) {
	var s skill.Skill
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
		return skill.Skill{}, err
	}

	s.Source = skill.SkillSource(sourceStr)
	s.State = skill.SkillState(stateStr)

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
