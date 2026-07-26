package main

// model_api.go —— LLM Provider 与 Model 管理 REST API。
//
// # Endpoints
//
//	GET  /api/providers                    —— 列出所有已配置 Provider（不含 api_key）
//	POST /api/providers/{name}/sync        —— 手动触发指定 Provider 的模型发现
//	GET  /api/models/prices                —— 列出所有持久化模型画像
//	PUT  /api/models/prices/{provider}/{model} —— 更新模型画像的可编辑字段
//
// # 设计理由
//
// 本文件替代旧的 model_price_api.go：模型画像现在持久化在 llm_models 表，
// 价格/能力/context/output/fallback 等字段都通过 PUT 保存到 DB 并同步生效。
// Provider 信息从 llm_providers 表读取，绝不返回 api_key。
//
// # Auth
//
// GET /api/providers 与 GET /api/models/prices 公开可读。
// POST /api/providers/{name}/sync 与 PUT /api/models/prices/{provider}/{model}
// 是写操作，已注册在 auth.DefaultProtectedRoutes 中，REQUIRE_AUTH 启用时
// 需要 Bearer token。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// ProviderItem 是 GET /api/providers 返回的 Provider 表示。
type ProviderItem struct {
	Name          string     `json:"name"`
	Type          string     `json:"type"`
	Endpoint      string     `json:"endpoint"`
	Healthy       bool       `json:"healthy"`
	LastSyncAt    *time.Time `json:"last_sync_at,omitempty"`
	LastSyncError string     `json:"last_sync_error,omitempty"`
}

// ModelProfileItem 是 GET /api/models/prices 返回的模型画像表示。
type ModelProfileItem struct {
	Provider         string   `json:"provider"`
	ModelID          string   `json:"model_id"`
	DisplayName      string   `json:"display_name"`
	Tier             string   `json:"tier"`
	Capabilities     []string `json:"capabilities"`
	InputPrice       float64  `json:"input_price"`
	OutputPrice      float64  `json:"output_price"`
	MaxContextWindow int      `json:"max_context_window"`
	MaxOutputTokens  int      `json:"max_output_tokens"`
	FallbackModel    string   `json:"fallback_model"`
	RateLimitRPM     int      `json:"rate_limit_rpm"`
	AvgLatencyMs     int      `json:"avg_latency_ms"`
	Missing          bool     `json:"missing"`
	Source           string   `json:"source"`
	UpdatedAt        int64    `json:"updated_at_ms"`
}

// RegisterModelAPIRoutes 注册 LLM Provider / Model 管理路由。
func RegisterModelAPIRoutes(mux *http.ServeMux, providerManager *llm.ProviderManager, modelRegistry *llm.ModelRegistry, hub *ws.Hub) {
	mux.HandleFunc("/api/providers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		handleListProviders(w, r)
	})

	mux.HandleFunc("/api/providers/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/providers/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] != "sync" {
			respondJSON(w, http.StatusNotFound, map[string]any{"error": "invalid provider resource"})
			return
		}
		name := parts[0]
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		handleSyncProvider(w, r, providerManager, name, hub)
	})

	mux.HandleFunc("/api/models/prices", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			handleListModelProfiles(w, r, modelRegistry)
			return
		}
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/api/models/prices/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/models/prices/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			respondJSON(w, http.StatusBadRequest, map[string]any{"error": "provider/model required"})
			return
		}
		if r.Method != http.MethodPut {
			http.Error(w, "PUT only", http.StatusMethodNotAllowed)
			return
		}
		handleUpdateModelProfile(w, r, modelRegistry, parts[0], parts[1])
	})
}

// handleListProviders 返回 llm_providers 表快照，不含 api_key。
func handleListProviders(w http.ResponseWriter, _ *http.Request) {
	records, err := db.ListProviders()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	items := make([]ProviderItem, 0, len(records))
	for _, rec := range records {
		items = append(items, ProviderItem{
			Name:          rec.Name,
			Type:          rec.Type,
			Endpoint:      rec.Endpoint,
			Healthy:       rec.Healthy,
			LastSyncAt:    rec.LastSyncAt,
			LastSyncError: rec.LastSyncError,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"providers": items,
		"count":     len(items),
	})
}

// handleSyncProvider 手动触发指定 Provider 的模型发现。
func handleSyncProvider(w http.ResponseWriter, r *http.Request, providerManager *llm.ProviderManager, name string, hub *ws.Hub) {
	if providerManager == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "provider manager not available"})
		return
	}

	// 广播 provider_sync_started 事件，让前端 Inspector 看到手动同步动作。
	emitProviderSyncEvent(hub, event.EventProviderSyncStarted, name, nil)

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	if err := providerManager.SyncProvider(ctx, name); err != nil {
		// 失败时广播并返回错误详情。
		emitProviderSyncEvent(hub, event.EventProviderSyncFailed, name, map[string]any{"error": err.Error()})
		respondJSON(w, http.StatusInternalServerError, map[string]any{
			"error":  err.Error(),
			"status": "failed",
		})
		return
	}

	// 成功后广播 completed 事件。
	emitProviderSyncEvent(hub, event.EventProviderSyncCompleted, name, nil)

	models, err := db.ListModelsByProvider(name)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{
			"status":     "synced",
			"provider":   name,
			"model_ids":  []string{},
			"model_count": 0,
			"warning":    err.Error(),
		})
		return
	}

	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ModelID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":      "synced",
		"provider":    name,
		"model_ids":   ids,
		"model_count": len(ids),
	})
}

// handleListModelProfiles 返回实际可用的模型画像。
// 只包含已配置 provider 或显式静态声明的模型；missing=true 与纯 DefaultProfiles
// 默认模型（SourceDefaultProfile 且 provider 未配置）被过滤掉，避免前端看到
// 无法调用的模型。
func handleListModelProfiles(w http.ResponseWriter, _ *http.Request, registry *llm.ModelRegistry) {
	profiles := registry.AvailableProfiles(nil, true)
	items := make([]ModelProfileItem, 0, len(profiles))
	for _, p := range profiles {
		items = append(items, profileToItem(p))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":      items,
		"count":      len(items),
		"persistent": true,
	})
}

// handleUpdateModelProfile 更新 llm_models 中指定模型的可编辑字段。
// 可编辑字段：display_name, tier, capabilities, input_price, output_price,
// max_context_window, max_output_tokens, fallback_model, rate_limit_rpm, avg_latency_ms, missing。
// 不可编辑：provider, model_id。
func handleUpdateModelProfile(w http.ResponseWriter, r *http.Request, registry *llm.ModelRegistry, providerName, modelID string) {
	existing, found, err := db.GetModel(providerName, modelID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !found {
		respondJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("model not found: %s/%s", providerName, modelID)})
		return
	}

	var req struct {
		DisplayName      *string   `json:"display_name"`
		Tier             *string   `json:"tier"`
		Capabilities     []string  `json:"capabilities"`
		InputPrice       *float64  `json:"input_price"`
		OutputPrice      *float64  `json:"output_price"`
		MaxContextWindow *int      `json:"max_context_window"`
		MaxOutputTokens  *int      `json:"max_output_tokens"`
		FallbackModel    *string   `json:"fallback_model"`
		RateLimitRPM     *int      `json:"rate_limit_rpm"`
		AvgLatencyMs     *int      `json:"avg_latency_ms"`
		Missing          *bool     `json:"missing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body: " + err.Error()})
		return
	}

	// 拒绝任何试图修改主键的字段。
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err == nil {
		if _, ok := raw["provider"]; ok {
			respondJSON(w, http.StatusBadRequest, map[string]any{"error": "provider is read-only"})
			return
		}
		if _, ok := raw["model_id"]; ok {
			respondJSON(w, http.StatusBadRequest, map[string]any{"error": "model_id is read-only"})
			return
		}
	}

	if req.DisplayName != nil {
		existing.DisplayName = *req.DisplayName
	}
	if req.Tier != nil {
		existing.Tier = *req.Tier
	}
	if req.Capabilities != nil {
		existing.Capabilities = req.Capabilities
	}
	if req.InputPrice != nil {
		existing.InputPrice = *req.InputPrice
	}
	if req.OutputPrice != nil {
		existing.OutputPrice = *req.OutputPrice
	}
	if req.MaxContextWindow != nil {
		existing.MaxContextWindow = *req.MaxContextWindow
	}
	if req.MaxOutputTokens != nil {
		existing.MaxOutputTokens = *req.MaxOutputTokens
	}
	if req.FallbackModel != nil {
		existing.FallbackModel = *req.FallbackModel
	}
	if req.RateLimitRPM != nil {
		existing.RateLimitRPM = *req.RateLimitRPM
	}
	if req.AvgLatencyMs != nil {
		existing.AvgLatencyMs = *req.AvgLatencyMs
	}
	// 处理 missing 字段：前端只能将 missing 从 true 改回 false（重新标记为可用），
	// 不能通过 PUT 将 false 改为 true；missing 状态应由 ProviderManager 发现流程决定。
	if req.Missing != nil {
		if existing.Missing && !*req.Missing {
			existing.Missing = false
		} else if !existing.Missing && *req.Missing {
			respondJSON(w, http.StatusBadRequest, map[string]any{"error": "marking a model as missing is only allowed via provider sync"})
			return
		}
	}

	if err := db.UpdateModel(*existing); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	// 同步更新内存 ModelRegistry，使后续路由/成本计算立即生效。
	if registry != nil {
		registry.Register(profileFromRecord(*existing))
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"model":      profileToItem(profileFromRecord(*existing)),
		"persistent": true,
	})
}

// profileToItem 把 llm.ModelProfile 转为 API item。
func profileToItem(p *llm.ModelProfile) ModelProfileItem {
	caps := make([]string, 0, len(p.Capabilities))
	for _, c := range p.Capabilities {
		caps = append(caps, string(c))
	}
	// Name 在 registry 中可能是全名 "provider/model_id"；拆分。
	providerName, modelID := p.Provider, p.Name
	if idx := strings.LastIndex(p.Name, "/"); idx >= 0 {
		providerName = p.Name[:idx]
		modelID = p.Name[idx+1:]
	}
	return ModelProfileItem{
		Provider:         providerName,
		ModelID:          modelID,
		DisplayName:      p.DisplayName,
		Tier:             p.Tier.String(),
		Capabilities:     caps,
		InputPrice:       p.InputPrice,
		OutputPrice:      p.OutputPrice,
		MaxContextWindow: p.MaxContextWindow,
		MaxOutputTokens:  p.MaxOutputTokens,
		FallbackModel:    p.FallbackModel,
		RateLimitRPM:     p.RateLimitRPM,
		AvgLatencyMs:     p.AvgLatencyMs,
		Missing:          p.Missing,
		Source:           string(p.Source),
		UpdatedAt:        time.Now().UnixMilli(),
	}
}

// profileFromRecord 从 db record 构造 llm.ModelProfile，用于写库后的内存同步。
func profileFromRecord(rec db.LLMModelRecord) *llm.ModelProfile {
	caps := make([]llm.ModelCapability, len(rec.Capabilities))
	for i, c := range rec.Capabilities {
		caps[i] = llm.ModelCapability(c)
	}
	return &llm.ModelProfile{
		Name:             fmt.Sprintf("%s/%s", rec.ProviderName, rec.ModelID),
		Provider:         rec.ProviderName,
		Tier:             llm.ParseTier(rec.Tier),
		Capabilities:     caps,
		InputPrice:       rec.InputPrice,
		OutputPrice:      rec.OutputPrice,
		MaxContextWindow: rec.MaxContextWindow,
		MaxOutputTokens:  rec.MaxOutputTokens,
		FallbackModel:    rec.FallbackModel,
		RateLimitRPM:     rec.RateLimitRPM,
		AvgLatencyMs:     rec.AvgLatencyMs,
		Missing:          rec.Missing,
		Source:           llm.SourceConfiguredProvider,
	}
}

// emitProviderSyncEvent 辅助函数：在 hub 存在时广播 provider_sync_* 事件。
// task_id 固定为 provider 名，agent_id 固定为 "provider_manager"，便于前端过滤。
func emitProviderSyncEvent(hub *ws.Hub, eventType, providerName string, data map[string]any) {
	if hub == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["provider"] = providerName
	hub.SendEvent(event.NewEvent(eventType, providerName, "provider_manager", 0, data))
}
