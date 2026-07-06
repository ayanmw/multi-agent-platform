// Package llm — ModelProfile and ModelRegistry for multi-model management.
//
// # Design Rationale
//
// ModelProfile describes a model's complete profile — capabilities, cost, limits,
// and fallback path. The ModelRegistry is the central catalog that the Router
// queries to select the best model for a given task.
//
// # Model Tiers
//
// Models are organized into 6 tiers based on cost and capability:
//
//	TierFree       — free/local models for development and cold backup
//	TierEfficient  — low-cost, high-throughput models for bulk tasks
//	TierLightweight — fast, cheap models for classification and routing
//	TierStandard   — primary workhorse models for general agent execution
//	TierPremium    — top-tier models for complex reasoning and planning
//
// # Usage
//
//	registry := llm.NewModelRegistry()
//	registry.Register(llm.ModelProfile{...})
//	models := registry.FilterByCapability(llm.CapToolCalling)
//
// See doc/chapters/10-multi-model-layered-design.html for the full design.
package llm

import (
	"slices"
	"sort"
	"sync"
)

// ModelTier represents the capability/cost tier of a model.
// Lower values = cheaper/faster; higher values = more capable/expensive.
type ModelTier int

const (
	// TierFree represents free or locally-hosted models.
	// Used for development, testing, and cold backup.
	TierFree ModelTier = iota

	// TierEfficient represents low-cost, high-throughput models.
	// Used for bulk analysis, data cleaning, result validation.
	TierEfficient

	// TierLightweight represents fast, cheap models for routing/classification.
	// Used for intent classification, simple Q&A, format conversion.
	TierLightweight

	// TierStandard represents primary workhorse models.
	// Used for general agent execution, code generation, tool calling.
	TierStandard

	// TierPremium represents top-tier reasoning models.
	// Used for complex multi-step reasoning, architecture design, planning.
	TierPremium
)

// String returns the human-readable tier name.
func (t ModelTier) String() string {
	switch t {
	case TierFree:
		return "free"
	case TierEfficient:
		return "efficient"
	case TierLightweight:
		return "lightweight"
	case TierStandard:
		return "standard"
	case TierPremium:
		return "premium"
	default:
		return "unknown"
	}
}

// ModelCapability describes a specific capability a model may or may not support.
// The Router uses capabilities to filter models that can handle a given task.
type ModelCapability string

const (
	// CapToolCalling indicates the model supports function/tool calling.
	CapToolCalling ModelCapability = "tool_calling"

	// CapStreaming indicates the model supports SSE streaming responses.
	CapStreaming ModelCapability = "streaming"

	// CapVision indicates the model supports image/video input.
	CapVision ModelCapability = "vision"

	// CapReasoning indicates the model supports deep reasoning / chain-of-thought.
	CapReasoning ModelCapability = "reasoning"

	// CapJSONMode indicates the model supports structured JSON output mode.
	CapJSONMode ModelCapability = "json_mode"
)

// ModelProfile describes a model's complete profile for routing decisions.
//
// Each profile captures the model's identity, capabilities, cost structure,
// technical limits, and fallback path. The Router uses this information to
// select the best model for a given task.
type ModelProfile struct {
	// Name is the model identifier (e.g., "deepseek-v4-flash", "claude-sonnet-4-6").
	Name string

	// Provider identifies the API provider (e.g., "openai", "anthropic", "deepseek").
	Provider string

	// Tier is the model's capability/cost tier.
	Tier ModelTier

	// Capabilities lists the model's supported features.
	Capabilities []ModelCapability

	// InputPrice is the cost per 1M input tokens (USD).
	InputPrice float64

	// OutputPrice is the cost per 1M output tokens (USD).
	OutputPrice float64

	// MaxContextWindow is the maximum context length in tokens.
	MaxContextWindow int

	// MaxOutputTokens is the maximum output length in tokens.
	MaxOutputTokens int

	// RateLimitRPM is the maximum requests per minute.
	RateLimitRPM int

	// FallbackModel is the model to use when this one is unavailable.
	// Empty string means no fallback is configured.
	FallbackModel string

	// AvgLatencyMs is the average response latency in milliseconds.
	AvgLatencyMs int
}

// HasCapability checks whether the model supports a specific capability.
func (mp *ModelProfile) HasCapability(cap ModelCapability) bool {
	return slices.Contains(mp.Capabilities, cap)
}

// SupportsContextLen checks whether the model can handle a given context length.
func (mp *ModelProfile) SupportsContextLen(tokens int) bool {
	return tokens <= mp.MaxContextWindow
}

// ModelRegistry is the central catalog of available model profiles.
//
// It supports registration, lookup by name, filtering by tier/capability/context length,
// and fallback resolution. The registry is goroutine-safe and can be updated at runtime
// (e.g., when new models are added or rate limits change).
//
// In Phase 5, the registry is populated from configuration at startup. In Phase 6,
// it will be loadable from the database for dynamic updates.
type ModelRegistry struct {
	mu       sync.RWMutex
	profiles map[string]*ModelProfile // name → profile
	byTier   map[ModelTier][]string   // tier → model names
}

// NewModelRegistry creates an empty model registry.
func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{
		profiles: make(map[string]*ModelProfile),
		byTier:   make(map[ModelTier][]string),
	}
}

// Register adds or updates a model profile in the registry.
// If a profile with the same name already exists, it is overwritten.
func (r *ModelRegistry) Register(profile *ModelProfile) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.profiles[profile.Name] = profile
	r.byTier[profile.Tier] = append(r.byTier[profile.Tier], profile.Name)
}

// Get returns a model profile by name, or nil if not found.
func (r *ModelRegistry) Get(name string) *ModelProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.profiles[name]
}

// GetByTier returns all model profiles in a given tier.
func (r *ModelRegistry) GetByTier(tier ModelTier) []*ModelProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := r.byTier[tier]
	profiles := make([]*ModelProfile, 0, len(names))
	for _, name := range names {
		if p, ok := r.profiles[name]; ok {
			profiles = append(profiles, p)
		}
	}
	return profiles
}

// FilterByCapability returns all models that support a specific capability,
// sorted by tier (cheapest first).
func (r *ModelRegistry) FilterByCapability(cap ModelCapability) []*ModelProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*ModelProfile
	for _, p := range r.profiles {
		if p.HasCapability(cap) {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Tier < result[j].Tier
	})
	return result
}

// FilterByContextLen returns all models that can handle a given context length,
// sorted by tier (cheapest first).
func (r *ModelRegistry) FilterByContextLen(minTokens int) []*ModelProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*ModelProfile
	for _, p := range r.profiles {
		if p.SupportsContextLen(minTokens) {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Tier < result[j].Tier
	})
	return result
}

// GetFallback returns the fallback model for a given model name.
// Returns nil if no fallback is configured or the model is not found.
func (r *ModelRegistry) GetFallback(name string) *ModelProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.profiles[name]
	if !ok || p.FallbackModel == "" {
		return nil
	}
	return r.profiles[p.FallbackModel]
}

// List returns all registered model profiles, sorted by tier.
func (r *ModelRegistry) List() []*ModelProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*ModelProfile, 0, len(r.profiles))
	for _, p := range r.profiles {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Tier < result[j].Tier
	})
	return result
}

// DefaultProfiles returns a set of sensible default model profiles.
// These are used when no configuration is provided, ensuring the system
// works out of the box with commonly available models.
func DefaultProfiles() []*ModelProfile {
	return []*ModelProfile{
		{
			Name:             "deepseek-v4-flash",
			Provider:         "deepseek",
			Tier:             TierEfficient,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapJSONMode},
			InputPrice:       0.14,
			OutputPrice:      0.29,
			MaxContextWindow: 128000,
			MaxOutputTokens:  4096,
			RateLimitRPM:     500,
			FallbackModel:    "",
			AvgLatencyMs:     800,
		},
		{
			Name:             "deepseek-v4-pro",
			Provider:         "deepseek",
			Tier:             TierStandard,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapReasoning, CapJSONMode},
			InputPrice:       1.71,
			OutputPrice:      3.43,
			MaxContextWindow: 128000,
			MaxOutputTokens:  8192,
			RateLimitRPM:     200,
			FallbackModel:    "deepseek-v4-flash",
			AvgLatencyMs:     1500,
		},
	}
}