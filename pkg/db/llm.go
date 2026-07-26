// Package db —— LLM Provider 与 Model 持久化 CRUD。
//
// 本文件提供 llm_providers 与 llm_models 表的读写接口。
// Provider 记录仅保存配置快照与同步健康状态，不保存 api_key。
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// LLMProviderRecord 描述 llm_providers 表中的一行。
// API key 不存入数据库，避免 secret 落入 SQLite。
type LLMProviderRecord struct {
	Name          string
	Type          string
	Endpoint      string
	Healthy       bool
	LastSyncAt    *time.Time
	LastSyncError string
}

// LLMModelRecord 描述 llm_models 表中的一行模型画像。
type LLMModelRecord struct {
	ProviderName     string
	ModelID          string
	DisplayName      string
	Tier             string
	Capabilities     []string
	InputPrice       float64
	OutputPrice      float64
	MaxContextWindow int
	MaxOutputTokens  int
	FallbackModel    string
	RateLimitRPM     int
	AvgLatencyMs     int
	Missing          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// InsertOrReplaceProvider 写入或覆盖一个 Provider 快照。
func InsertOrReplaceProvider(rec LLMProviderRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`
		INSERT INTO llm_providers (name, type, endpoint, healthy, last_sync_at, last_sync_error)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			type = excluded.type,
			endpoint = excluded.endpoint,
			healthy = excluded.healthy,
			last_sync_at = excluded.last_sync_at,
			last_sync_error = excluded.last_sync_error`,
		rec.Name, rec.Type, rec.Endpoint, rec.Healthy, rec.LastSyncAt, rec.LastSyncError)
	if err != nil {
		return fmt.Errorf("insert or replace provider %q: %w", rec.Name, err)
	}
	return nil
}

// ListProviders 返回所有 Provider 快照（不含 api_key）。
func ListProviders() ([]LLMProviderRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(`
		SELECT name, type, endpoint, healthy, last_sync_at, last_sync_error
		FROM llm_providers
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query providers: %w", err)
	}
	defer rows.Close()

	var list []LLMProviderRecord
	for rows.Next() {
		var rec LLMProviderRecord
		var lastSync sql.NullTime
		if err := rows.Scan(&rec.Name, &rec.Type, &rec.Endpoint, &rec.Healthy, &lastSync, &rec.LastSyncError); err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		if lastSync.Valid {
			rec.LastSyncAt = &lastSync.Time
		}
		list = append(list, rec)
	}
	return list, rows.Err()
}

// UpdateProviderSyncStatus 更新 Provider 的同步状态。
func UpdateProviderSyncStatus(name string, healthy bool, syncErr string, at time.Time) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`
		UPDATE llm_providers
		SET healthy = ?, last_sync_at = ?, last_sync_error = ?
		WHERE name = ?`,
		healthy, at, syncErr, name)
	if err != nil {
		return fmt.Errorf("update provider sync status %q: %w", name, err)
	}
	return nil
}

// InsertOrReplaceModel 写入或覆盖一个模型画像。
// 重新发现时覆盖整个行，用于首次写入或来自 .env 种子的全量同步。
func InsertOrReplaceModel(rec LLMModelRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	capsJSON, err := json.Marshal(rec.Capabilities)
	if err != nil {
		return fmt.Errorf("marshal capabilities: %w", err)
	}
	now := time.Now().UTC()
	_, err = DB.Exec(`
		INSERT INTO llm_models (
			provider_name, model_id, display_name, tier, capabilities,
			input_price, output_price, max_context_window, max_output_tokens,
			fallback_model, rate_limit_rpm, avg_latency_ms, missing,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider_name, model_id) DO UPDATE SET
			display_name = excluded.display_name,
			tier = excluded.tier,
			capabilities = excluded.capabilities,
			input_price = excluded.input_price,
			output_price = excluded.output_price,
			max_context_window = excluded.max_context_window,
			max_output_tokens = excluded.max_output_tokens,
			fallback_model = excluded.fallback_model,
			rate_limit_rpm = excluded.rate_limit_rpm,
			avg_latency_ms = excluded.avg_latency_ms,
			missing = excluded.missing,
			updated_at = ?`,
		rec.ProviderName, rec.ModelID, rec.DisplayName, rec.Tier, string(capsJSON),
		rec.InputPrice, rec.OutputPrice, rec.MaxContextWindow, rec.MaxOutputTokens,
		rec.FallbackModel, rec.RateLimitRPM, rec.AvgLatencyMs, rec.Missing,
		rec.CreatedAt, rec.UpdatedAt, now)
	if err != nil {
		return fmt.Errorf("insert or replace model %s/%s: %w", rec.ProviderName, rec.ModelID, err)
	}
	return nil
}

// GetModel 按复合主键查询单个模型。
// 返回记录、是否存在以及可能的错误。
func GetModel(providerName, modelID string) (*LLMModelRecord, bool, error) {
	if DB == nil {
		return nil, false, fmt.Errorf("db not initialized")
	}
	row := DB.QueryRow(`
		SELECT provider_name, model_id, display_name, tier, capabilities,
		       input_price, output_price, max_context_window, max_output_tokens,
		       fallback_model, rate_limit_rpm, avg_latency_ms, missing,
		       created_at, updated_at
		FROM llm_models
		WHERE provider_name = ? AND model_id = ?`,
		providerName, modelID)
	rec, err := scanLLMModelRecord(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get model %s/%s: %w", providerName, modelID, err)
	}
	return rec, true, nil
}

// ListModels 返回所有模型画像。
func ListModels() ([]LLMModelRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(`
		SELECT provider_name, model_id, display_name, tier, capabilities,
		       input_price, output_price, max_context_window, max_output_tokens,
		       fallback_model, rate_limit_rpm, avg_latency_ms, missing,
		       created_at, updated_at
		FROM llm_models
		ORDER BY provider_name, model_id`)
	if err != nil {
		return nil, fmt.Errorf("query models: %w", err)
	}
	defer rows.Close()
	return scanLLMModelRows(rows)
}

// ListModelsByProvider 返回指定 Provider 下的所有模型画像。
func ListModelsByProvider(providerName string) ([]LLMModelRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(`
		SELECT provider_name, model_id, display_name, tier, capabilities,
		       input_price, output_price, max_context_window, max_output_tokens,
		       fallback_model, rate_limit_rpm, avg_latency_ms, missing,
		       created_at, updated_at
		FROM llm_models
		WHERE provider_name = ?
		ORDER BY model_id`, providerName)
	if err != nil {
		return nil, fmt.Errorf("query models by provider %q: %w", providerName, err)
	}
	defer rows.Close()
	return scanLLMModelRows(rows)
}

// UpdateModel 更新 llm_models 中除主键外的可编辑字段。
// 调用方应保证 ProviderName + ModelID 与目标行一致。
func UpdateModel(rec LLMModelRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	capsJSON, err := json.Marshal(rec.Capabilities)
	if err != nil {
		return fmt.Errorf("marshal capabilities: %w", err)
	}
	res, err := DB.Exec(`
		UPDATE llm_models
		SET display_name = ?, tier = ?, capabilities = ?,
		    input_price = ?, output_price = ?, max_context_window = ?, max_output_tokens = ?,
		    fallback_model = ?, rate_limit_rpm = ?, avg_latency_ms = ?, missing = ?, updated_at = ?
		WHERE provider_name = ? AND model_id = ?`,
		rec.DisplayName, rec.Tier, string(capsJSON),
		rec.InputPrice, rec.OutputPrice, rec.MaxContextWindow, rec.MaxOutputTokens,
		rec.FallbackModel, rec.RateLimitRPM, rec.AvgLatencyMs, rec.Missing, time.Now().UTC(),
		rec.ProviderName, rec.ModelID)
	if err != nil {
		return fmt.Errorf("update model %s/%s: %w", rec.ProviderName, rec.ModelID, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("model %s/%s not found", rec.ProviderName, rec.ModelID)
	}
	return nil
}

// MarkModelsMissingForProvider 标记指定 Provider 下、**不在** seenModelIDs 中的模型为 missing。
// seenModelIDs 中的模型视为仍可用，其他模型被设置 missing=true。
func MarkModelsMissingForProvider(providerName string, seenModelIDs []string, missing bool) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}

	now := time.Now().UTC()
	// 没有 seen IDs 时，将该 provider 下所有模型标记为 missing。
	if len(seenModelIDs) == 0 {
		_, err := DB.Exec(`
			UPDATE llm_models
			SET missing = ?, updated_at = ?
			WHERE provider_name = ?`,
			missing, now, providerName)
		if err != nil {
			return fmt.Errorf("mark all models missing for provider %q: %w", providerName, err)
		}
		return nil
	}

	placeholders := make([]string, len(seenModelIDs))
	args := make([]any, 0, len(seenModelIDs)+3)
	args = append(args, missing, now, providerName)
	for i, id := range seenModelIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := fmt.Sprintf(`
		UPDATE llm_models
		SET missing = ?, updated_at = ?
		WHERE provider_name = ? AND model_id NOT IN (%s)`,
		strings.Join(placeholders, ","))
	_, err := DB.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("mark models missing for provider %q: %w", providerName, err)
	}
	return nil
}

// scanLLMModelRecord 从单行扫描出 LLMModelRecord。
func scanLLMModelRecord(row *sql.Row) (*LLMModelRecord, error) {
	var rec LLMModelRecord
	var capabilities string
	err := row.Scan(&rec.ProviderName, &rec.ModelID, &rec.DisplayName, &rec.Tier, &capabilities,
		&rec.InputPrice, &rec.OutputPrice, &rec.MaxContextWindow, &rec.MaxOutputTokens,
		&rec.FallbackModel, &rec.RateLimitRPM, &rec.AvgLatencyMs, &rec.Missing,
		&rec.CreatedAt, &rec.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if capabilities != "" && capabilities != "null" {
		_ = json.Unmarshal([]byte(capabilities), &rec.Capabilities)
	}
	return &rec, nil
}

// scanLLMModelRows 扫描多行模型记录。
func scanLLMModelRows(rows *sql.Rows) ([]LLMModelRecord, error) {
	var list []LLMModelRecord
	for rows.Next() {
		var rec LLMModelRecord
		var capabilities string
		if err := rows.Scan(&rec.ProviderName, &rec.ModelID, &rec.DisplayName, &rec.Tier, &capabilities,
			&rec.InputPrice, &rec.OutputPrice, &rec.MaxContextWindow, &rec.MaxOutputTokens,
			&rec.FallbackModel, &rec.RateLimitRPM, &rec.AvgLatencyMs, &rec.Missing,
			&rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan model: %w", err)
		}
		if capabilities != "" && capabilities != "null" {
			_ = json.Unmarshal([]byte(capabilities), &rec.Capabilities)
		}
		list = append(list, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}
	return list, nil
}
