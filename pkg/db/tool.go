// tool.go — tools 表 v27 schema 与 CRUD。
//
// Phase 8-A 把动态工具持久化从「单 name 主键 + schema 列」升级为支持
// 多版本与多来源的结构：复合主键 (namespace, name, version)，新增 source /
// execution_config_json / updated_at 列。由于 SQLite 无法 ALTER 复合主键，
// v27 采取 DROP 重建策略，并在 DROP 前把所有旧记录打印到日志，方便用户
// 手动重新添加历史动态工具。
//
// 旧 API（InsertTool / QueryTools / DeleteTool）保留为薄 wrapper，
// 维持 cmd/server/tool_api.go 现有调用点不破，后续可在 Task 5+ 统一切换。
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// init 注册 v27 tools 表迁移：DROP 重建前打印旧记录。
// 使用复合主键 (namespace, name, version)，因此无法用 ALTER TABLE 升级，
// 必须重建。Pre 钩子在 DROP 前把旧 tools 表内容打印到日志。
func init() {
	migrations = append(migrations, Migration{
		Version:     27,
		Description: "Back up and redefine tools table with namespace, version, source, execution_config",
		// DROP 前打印所有历史 tools 记录，方便用户手动重新添加。
		Pre: func(db *sql.DB) error {
			// 旧 tools 表可能不存在（全新库走 createTables 直接建新结构），
			// 此时 Query 报错属预期，静默返回。
			rows, err := db.Query(`SELECT name, COALESCE(description,''), COALESCE(schema,'{}'), COALESCE(enabled,1), created_at FROM tools`)
			if err != nil {
				return nil // 旧表不存在或 schema 不符，忽略
			}
			defer rows.Close()
			log.Printf("[migration v27] backing up old tools records before DROP:")
			count := 0
			for rows.Next() {
				var name, description, schema, createdAt string
				var enabled bool
				if err := rows.Scan(&name, &description, &schema, &enabled, &createdAt); err == nil {
					log.Printf("[migration v27] OLD_TOOL: name=%q description=%q schema=%q enabled=%v created_at=%s",
						name, description, schema, enabled, createdAt)
					count++
				}
			}
			log.Printf("[migration v27] backed up %d old tool record(s); they will be lost after DROP — re-add manually if needed", count)
			return rows.Err()
		},
		SQL: `DROP TABLE IF EXISTS tools;
CREATE TABLE tools (
    namespace TEXT DEFAULT '',
    name TEXT NOT NULL,
    version TEXT NOT NULL DEFAULT '1.0.0',
    source TEXT NOT NULL DEFAULT 'local_db',
    description TEXT DEFAULT '',
    parameters_json TEXT DEFAULT '{}',
    enabled BOOLEAN DEFAULT 1,
    execution_config_json TEXT DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (namespace, name, version)
);
CREATE INDEX idx_tools_source ON tools(source);
CREATE INDEX idx_tools_enabled ON tools(enabled);`,
	})
}

// ToolRecord 对应 v27 tools 表的一条记录。
// 承载动态工具的元数据与执行配置，供 DBToolLoader 还原为 tool.DynamicTool。
type ToolRecord struct {
	Namespace       string         // 命名空间，builtin 通常为空，动态工具可按来源分组
	Name            string         // 工具名（不含 namespace）
	Version         string         // 语义版本，空则默认 "1.0.0"
	Source          string         // "builtin" / "local_db" / "mcp" / "plugin"
	Description     string         // 人类可读描述，注入 LLM tool list
	Schema          map[string]any // JSON Schema 参数描述
	ExecutionConfig map[string]any // 执行配置（shell command / http url+method / inline code 等）
	Enabled         bool           // 是否启用
	CreatedAt       time.Time      // 创建时间
	UpdatedAt       time.Time      // 最近更新时间
}

// InsertToolV2 向 v27 tools 表写入一条工具记录。
// 缺省字段会填充默认值：namespace=""、version="1.0.0"、source="local_db"。
// 复合主键冲突时返回 SQLite 错误（调用方按需处理）。
func InsertToolV2(tr ToolRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	if tr.Version == "" {
		tr.Version = "1.0.0"
	}
	if tr.Source == "" {
		tr.Source = "local_db"
	}
	now := time.Now().Unix()
	schemaJSON, _ := json.Marshal(tr.Schema)
	execJSON, _ := json.Marshal(tr.ExecutionConfig)
	_, err := DB.Exec(
		`INSERT INTO tools (namespace, name, version, source, description, parameters_json, enabled, execution_config_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tr.Namespace, tr.Name, tr.Version, tr.Source, tr.Description,
		string(schemaJSON), tr.Enabled, string(execJSON), now, now,
	)
	return err
}

// UpdateToolV2 按 (namespace, name, version) 更新工具的可变字段。
// 主键三元组必须已存在，否则返回 no rows 错误。
func UpdateToolV2(tr ToolRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	if tr.Version == "" {
		tr.Version = "1.0.0"
	}
	now := time.Now().Unix()
	schemaJSON, _ := json.Marshal(tr.Schema)
	execJSON, _ := json.Marshal(tr.ExecutionConfig)
	res, err := DB.Exec(
		`UPDATE tools SET description=?, parameters_json=?, enabled=?, execution_config_json=?, updated_at=?
		 WHERE namespace=? AND name=? AND version=?`,
		tr.Description, string(schemaJSON), tr.Enabled, string(execJSON), now,
		tr.Namespace, tr.Name, tr.Version,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("tool not found: %s/%s@%s", tr.Namespace, tr.Name, tr.Version)
	}
	return nil
}

// DeleteToolV2 按 (namespace, name, version) 删除工具记录。
func DeleteToolV2(namespace, name, version string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	if version == "" {
		version = "1.0.0"
	}
	_, err := DB.Exec(
		`DELETE FROM tools WHERE namespace=? AND name=? AND version=?`,
		namespace, name, version,
	)
	return err
}

// QueryToolsV2 返回 tools 表中的全部工具记录，按 updated_at 倒序。
// namespace 为空字符串的记录会被原样返回（不再过滤）。
func QueryToolsV2() ([]ToolRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT namespace, name, version, source, COALESCE(description,''),
		        COALESCE(parameters_json,'{}'), COALESCE(enabled,1),
		        COALESCE(execution_config_json,'{}'), created_at, updated_at
		 FROM tools ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ToolRecord
	for rows.Next() {
		var tr ToolRecord
		var schemaJSON, execJSON string
		var createdAt, updatedAt int64
		if err := rows.Scan(
			&tr.Namespace, &tr.Name, &tr.Version, &tr.Source, &tr.Description,
			&schemaJSON, &tr.Enabled, &execJSON, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(schemaJSON), &tr.Schema)
		json.Unmarshal([]byte(execJSON), &tr.ExecutionConfig)
		tr.CreatedAt = time.Unix(createdAt, 0)
		tr.UpdatedAt = time.Unix(updatedAt, 0)
		out = append(out, tr)
	}
	return out, rows.Err()
}

// GetToolV2 按 (namespace, name, version) 精确查询单条工具记录。
// 未找到时返回 sql.ErrNoRows。
func GetToolV2(namespace, name, version string) (ToolRecord, error) {
	if DB == nil {
		return ToolRecord{}, fmt.Errorf("db not initialized")
	}
	if version == "" {
		version = "1.0.0"
	}
	var tr ToolRecord
	var schemaJSON, execJSON string
	var createdAt, updatedAt int64
	err := DB.QueryRow(
		`SELECT namespace, name, version, source, COALESCE(description,''),
		        COALESCE(parameters_json,'{}'), COALESCE(enabled,1),
		        COALESCE(execution_config_json,'{}'), created_at, updated_at
		 FROM tools WHERE namespace=? AND name=? AND version=?`,
		namespace, name, version,
	).Scan(
		&tr.Namespace, &tr.Name, &tr.Version, &tr.Source, &tr.Description,
		&schemaJSON, &tr.Enabled, &execJSON, &createdAt, &updatedAt,
	)
	if err != nil {
		return ToolRecord{}, err
	}
	json.Unmarshal([]byte(schemaJSON), &tr.Schema)
	json.Unmarshal([]byte(execJSON), &tr.ExecutionConfig)
	tr.CreatedAt = time.Unix(createdAt, 0)
	tr.UpdatedAt = time.Unix(updatedAt, 0)
	return tr, nil
}

// InsertTool 是兼容旧 API 的薄 wrapper：以 local_db 来源、空 namespace、
// 默认版本 1.0.0 写入一条工具记录。保持 cmd/server/tool_api.go 现有调用点不破。
func InsertTool(name, description string, schema map[string]any, enabled bool) error {
	return InsertToolV2(ToolRecord{
		Name:        name,
		Description: description,
		Schema:      schema,
		Enabled:     enabled,
		Source:      "local_db",
	})
}

// DeleteTool 是兼容旧 API 的薄 wrapper：按 name 删除（namespace 为空、
// version 为默认 1.0.0）的所有匹配记录。注意旧 API 只以 name 标识工具，
// 新表为复合主键，这里删除 name 匹配的全部版本，以最大兼容旧行为。
func DeleteTool(name string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`DELETE FROM tools WHERE name=?`, name)
	return err
}

// QueryTools 是兼容旧 API 的薄 wrapper：返回全部工具记录。
// 旧 ToolRecord 字段（Name/Description/Schema/Enabled/CreatedAt）由新结构兼容提供。
func QueryTools() ([]ToolRecord, error) {
	return QueryToolsV2()
}
