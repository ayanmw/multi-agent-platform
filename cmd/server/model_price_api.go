package main

// model_price_api.go — HTTP handlers for viewing and editing model pricing profiles.
//
// # Endpoints
//
//	GET  /api/models/prices        — list all registered model profiles with prices
//	PUT  /api/models/prices/{model} — update a model's InputPrice/OutputPrice (USD per 1M tokens)
//
// # Design rationale
//
// Cost tracking (internal/cost) computes CostCents from a ModelProfile's InputPrice/
// OutputPrice. The profile registry is built at startup from llm.DefaultProfiles() plus
// a cfg.LLMModel clone (see main.go). Without a way to inspect or tweak these prices,
// operators cannot correct a wrong official price or adjust for a custom rate-card
// without rebuilding the binary.
//
// These endpoints expose the in-memory registry directly. The PUT path uses
// ModelRegistry.Register (overwrite semantics, model_profile.go:174) so the new price
// takes effect immediately for all subsequent cost records. Changes are **runtime-only**
// — they are lost on restart and revert to DefaultProfiles(). This is intentional for
// the MVP: prices are advisory ("仅供参考，但必须非 0"), and persisting them would
// introduce a new schema without clear payoff. The GET response annotates this so the
// frontend can surface it to the user.
//
// # Auth
//
// GET is public-read (consistent with /api/costs and other read endpoints).
// PUT is a write operation and is registered in auth.DefaultProtectedRoutes so it
// requires a Bearer token when REQUIRE_AUTH is enabled.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// ModelPriceItem is the JSON representation of a model profile returned by
// GET /api/models/prices. Only the pricing-relevant fields are exposed so the
// API contract stays small even if ModelProfile grows more fields later.
type ModelPriceItem struct {
	// Name is the model identifier (e.g., "deepseek-v4-flash-local").
	Name string `json:"name"`

	// Provider is the API provider name (e.g., "deepseek").
	Provider string `json:"provider"`

	// Tier is the human-readable capability/cost tier (e.g., "efficient").
	Tier string `json:"tier"`

	// InputPrice is the cost per 1M input tokens in USD.
	InputPrice float64 `json:"input_price"`

	// OutputPrice is the cost per 1M output tokens in USD.
	OutputPrice float64 `json:"output_price"`

	// MaxContextWindow is the maximum context length in tokens.
	MaxContextWindow int `json:"max_context_window"`

	// MaxOutputTokens is the maximum output length in tokens.
	MaxOutputTokens int `json:"max_output_tokens"`

	// FallbackModel is the fallback model name (empty = no fallback).
	FallbackModel string `json:"fallback_model"`

	// Capabilities lists the model's supported capabilities (e.g., ["tool_calling","streaming"]).
	Capabilities []string `json:"capabilities"`
}

// RegisterModelPriceRoutes registers the model price management endpoints on mux.
// The registry is the shared ModelRegistry built at startup; mutations here affect
// all subsequent cost calculations in the same process.
func RegisterModelPriceRoutes(mux *http.ServeMux, registry *llm.ModelRegistry) {
	mux.HandleFunc("/api/models/prices", func(w http.ResponseWriter, r *http.Request) {
		// GET /api/models/prices — list all profiles.
		if r.Method == http.MethodGet {
			handleListModelPrices(w, r, registry)
			return
		}
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/api/models/prices/", func(w http.ResponseWriter, r *http.Request) {
		// /api/models/prices/{model} — extract the model name from the path.
		// Model names may contain hyphens but not slashes, so a single TrimPrefix
		// followed by a slash-presence guard is sufficient.
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

// handleListModelPrices returns all registered model profiles sorted by tier.
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

// handleUpdateModelPrice updates a single model's InputPrice and/or OutputPrice.
// PUT /api/models/prices/{model}
// Body: {"input_price": 0.14, "output_price": 0.28}
// Omitted or negative fields are ignored (left unchanged).
//
// Implementation: ModelRegistry.Register overwrites the whole profile by name, so we
// clone the existing profile, apply the price overrides, and re-register. This keeps
// all other fields (tier, capabilities, context window, fallback) intact.
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

	// Validate: prices must be non-negative. We accept 0 (free model) but warn in
	// the response because a 0 price produces 0 cost — the exact bug this endpoint
	// exists to fix.
	updated := *existing // shallow copy — Capabilities slice is shared, which is fine (read-only)
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

	// Register uses name-based overwrite, so re-registering the cloned (and renamed-kept)
	// profile replaces the previous entry. We intentionally keep Name unchanged.
	updated.Name = existing.Name
	registry.Register(&updated)

	respondJSON(w, http.StatusOK, map[string]any{
		"model":    profileToPriceItem(&updated),
		"warnings": warnings,
		"persistent": false,
		"note":     "Price updated in memory only. Reset on server restart.",
	})
}

// profileToPriceItem converts an llm.ModelProfile to the API-facing ModelPriceItem,
// mapping the capability enum slice to plain strings for JSON friendliness.
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
