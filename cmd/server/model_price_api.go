package main

// model_price_api.go —— 用于查看和编辑模型价格 profile 的 HTTP handler。
//
// # Endpoints
//
//	GET  /api/models/prices        —— 列出所有已注册 model profile 的价格
//	PUT  /api/models/prices/{model} —— 更新某模型的 InputPrice/OutputPrice（USD/1M tokens）
//
// # 设计理由
//
// 成本追踪 (internal/cost) 从 ModelProfile 的 InputPrice/OutputPrice 计算
// CostCents。profile registry 在启动时由 llm.DefaultProfiles() 加上
// cfg.LLMModel 的克隆构建（见 main.go）。如果没有办法查看或调整这些价格，
// 运维就无法在不重新构建 binary 的情况下纠正错误的官方价格或适配自定义
// rate-card。
//
// 这些 endpoint 直接暴露内存中的 registry。PUT 路径使用
// ModelRegistry.Register（覆盖语义，model_profile.go:174），因此新价格
// 对后续所有 cost record 立即生效。改动**仅运行时有效**——重启后会丢失
// 并回退到 DefaultProfiles()。这是 MVP 阶段有意为之：价格仅供参考
//（"仅供参考，但必须非 0"），持久化它们会引入新 schema 却没有明显收益。
// GET 响应里会标注这一点，便于前端提示用户。
//
// # Auth
//
// GET 公开可读（与 /api/costs 等读 endpoint 一致）。
// PUT 是写操作，已注册在 auth.DefaultProtectedRoutes 中，因此
// REQUIRE_AUTH 启用时需要 Bearer token。

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// ModelPriceItem 是 GET /api/models/prices 返回的 model profile 的 JSON 表示。
// 仅暴露与价格相关的字段，即使 ModelProfile 以后新增字段，API 契约仍保持精简。
type ModelPriceItem struct {
	// Name 是 model 标识（例如 "deepseek-v4-flash-local"）。
	Name string `json:"name"`

	// Provider 是 API provider 名（例如 "deepseek"）。
	Provider string `json:"provider"`

	// Tier 是人类可读的能力/成本 tier（例如 "efficient"）。
	Tier string `json:"tier"`

	// InputPrice 是每 1M input tokens 的成本（USD）。
	InputPrice float64 `json:"input_price"`

	// OutputPrice 是每 1M output tokens 的成本（USD）。
	OutputPrice float64 `json:"output_price"`

	// MaxContextWindow 是最大 context 长度（tokens）。
	MaxContextWindow int `json:"max_context_window"`

	// MaxOutputTokens 是最大输出长度（tokens）。
	MaxOutputTokens int `json:"max_output_tokens"`

	// FallbackModel 是兜底 model 名（空 = 无兜底）。
	FallbackModel string `json:"fallback_model"`

	// Capabilities 列出 model 支持的能力（例如 ["tool_calling","streaming"]）。
	Capabilities []string `json:"capabilities"`
}

// RegisterModelPriceRoutes 把模型价格管理 endpoint 注册到 mux。
// registry 是启动时构建的共享 ModelRegistry；此处的修改会影响
// 同一进程中后续所有 cost 计算。
func RegisterModelPriceRoutes(mux *http.ServeMux, registry *llm.ModelRegistry) {
	mux.HandleFunc("/api/models/prices", func(w http.ResponseWriter, r *http.Request) {
		// GET /api/models/prices —— 列出所有 profile。
		if r.Method == http.MethodGet {
			handleListModelPrices(w, r, registry)
			return
		}
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/api/models/prices/", func(w http.ResponseWriter, r *http.Request) {
		// /api/models/prices/{model} —— 从路径中提取 model 名。
		// model 名可包含连字符但不能含斜杠，因此一次 TrimPrefix 加上
		// 斜杠存在性检查就足够。
		model := strings.TrimPrefix(r.URL.Path, "/api/models/prices/")
		if model == "" || strings.Contains(model, "/") {
			http.Error(w, "model name required in path", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPut {
			http.Error(w, "PUT only", http.StatusMethodNotAllowed)
			return
		}
		handleUpdateModelPrice(w, r, registry, model)
	})
}

// handleListModelPrices 返回所有已注册 model profile，按 tier 排序。
// GET /api/models/prices
func handleListModelPrices(w http.ResponseWriter, _ *http.Request, registry *llm.ModelRegistry) {
	profiles := registry.List()
	items := make([]ModelPriceItem, 0, len(profiles))
	for _, p := range profiles {
		items = append(items, profileToPriceItem(p))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":      items,
		"count":      len(items),
		"persistent": false, // 价格改动仅在内存生效，重启后重置为 DefaultProfiles
		"note":       "Prices are advisory and runtime-only. Edits take effect immediately for new cost records but reset on restart.",
	})
}

// handleUpdateModelPrice 更新单个 model 的 InputPrice 和/或 OutputPrice。
// PUT /api/models/prices/{model}
// Body: {"input_price": 0.14, "output_price": 0.28}
// 省略或为负的字段会被忽略（保持不变）。
//
// 实现：ModelRegistry.Register 按 name 覆盖整个 profile，因此我们先克隆
// 现有 profile，应用价格覆盖，再重新注册。这保证其它字段（tier、
// capabilities、context window、fallback）不变。
func handleUpdateModelPrice(w http.ResponseWriter, r *http.Request, registry *llm.ModelRegistry, model string) {
	existing := registry.Get(model)
	if existing == nil {
		respondJSON(w, http.StatusNotFound, map[string]any{"error": "model not found: " + model})
		return
	}

	var req struct {
		InputPrice  *float64 `json:"input_price"`
		OutputPrice *float64 `json:"output_price"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body: " + err.Error()})
		return
	}

	// 校验：价格必须非负。我们接受 0（免费 model），但在响应中给出 warning，
	// 因为 0 价格会产生 0 成本 —— 这正是本 endpoint 要修复的 bug。
	updated := *existing // 浅拷贝 —— Capabilities slice 共享，没问题（只读）
	warnings := []string{}
	if req.InputPrice != nil {
		if *req.InputPrice < 0 {
			respondJSON(w, http.StatusBadRequest, map[string]any{"error": "input_price must be >= 0"})
			return
		}
		if *req.InputPrice == 0 {
			warnings = append(warnings, "input_price=0 will produce zero input-token cost")
		}
		updated.InputPrice = *req.InputPrice
	}
	if req.OutputPrice != nil {
		if *req.OutputPrice < 0 {
			respondJSON(w, http.StatusBadRequest, map[string]any{"error": "output_price must be >= 0"})
			return
		}
		if *req.OutputPrice == 0 {
			warnings = append(warnings, "output_price=0 will produce zero output-token cost")
		}
		updated.OutputPrice = *req.OutputPrice
	}

	// Register 按 name 覆盖，因此重新注册这个克隆（并保持 name 不变）的
	// profile 会替换原条目。我们有意保持 Name 不变。
	updated.Name = existing.Name
	registry.Register(&updated)

	respondJSON(w, http.StatusOK, map[string]any{
		"model":    profileToPriceItem(&updated),
		"warnings": warnings,
		"persistent": false,
		"note":     "Price updated in memory only. Reset on server restart.",
	})
}

// profileToPriceItem 把 llm.ModelProfile 转换为面向 API 的 ModelPriceItem，
// 并将 capability 枚举 slice 映射为纯字符串以便 JSON 友好输出。
func profileToPriceItem(p *llm.ModelProfile) ModelPriceItem {
	caps := make([]string, 0, len(p.Capabilities))
	for _, c := range p.Capabilities {
		caps = append(caps, string(c))
	}
	return ModelPriceItem{
		Name:             p.Name,
		Provider:         p.Provider,
		Tier:             p.Tier.String(),
		InputPrice:       p.InputPrice,
		OutputPrice:      p.OutputPrice,
		MaxContextWindow: p.MaxContextWindow,
		MaxOutputTokens:  p.MaxOutputTokens,
		FallbackModel:    p.FallbackModel,
		Capabilities:     caps,
	}
}
