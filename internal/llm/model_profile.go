// Package llm —— ModelProfile 与 ModelRegistry，用于多 model 管理。
//
// # 设计理由
//
// ModelProfile 描述一个 model 的完整画像 —— 能力、成本、限制
// 以及 fallback 路径。ModelRegistry 是中央目录，Router 查询它来
// 为给定任务选择最合适的 model。
//
// # Model 分层
//
// Model 按成本与能力划分为 6 个层级：
//
//	TierFree       —— 免费/本地 model，用于开发与冷备
//	TierEfficient  —— 低成本高吞吐 model，用于批量任务
//	TierLightweight —— 快速便宜的 model，用于分类与路由
//	TierStandard   —— 主力 workhorse model，用于通用 agent 执行
//	TierPremium    —— 顶级 model，用于复杂推理与规划
//
// # 用法
//
//	registry := llm.NewModelRegistry()
//	registry.Register(llm.ModelProfile{...})
//	models := registry.FilterByCapability(llm.CapToolCalling)
//
// 完整设计参见 doc/chapters/10-multi-model-layered-design.html。
package llm

import (
	"slices"
	"sort"
	"strings"
	"sync"
)

// ModelTier 表示 model 的能力/成本层级。
// 值越小越便宜/越快；值越大越强/越贵。
type ModelTier int

const (
	// TierFree 表示免费或本地托管的 model。
	// 用于开发、测试与冷备。
	TierFree ModelTier = iota

	// TierEfficient 表示低成本高吞吐的 model。
	// 用于批量分析、数据清洗、结果校验。
	TierEfficient

	// TierLightweight 表示用于路由/分类的快速便宜 model。
	// 用于 intent 分类、简单问答、格式转换。
	TierLightweight

	// TierStandard 表示主力 workhorse model。
	// 用于通用 agent 执行、代码生成、tool calling。
	TierStandard

	// TierPremium 表示顶级推理 model。
	// 用于复杂多步推理、架构设计、规划。
	TierPremium
)

// String 返回人类可读的层级名。
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

// ParseTier 把字符串解析为 ModelTier。它忽略大小写和前后空白，
// 非法字符串返回 -1。调用方通常按空字符串即"未指定"处理。
func ParseTier(s string) ModelTier {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "free":
		return TierFree
	case "efficient":
		return TierEfficient
	case "lightweight":
		return TierLightweight
	case "standard":
		return TierStandard
	case "premium":
		return TierPremium
	default:
		return ModelTier(-1)
	}
}

// ModelCapability 描述一个 model 可能支持或不支持的具体能力。
// Router 用能力过滤能处理给定任务的 model。
type ModelCapability string

const (
	// CapToolCalling 表示 model 支持 function/tool calling。
	CapToolCalling ModelCapability = "tool_calling"

	// CapStreaming 表示 model 支持 SSE streaming 响应。
	CapStreaming ModelCapability = "streaming"

	// CapVision 表示 model 支持图像/视频输入。
	CapVision ModelCapability = "vision"

	// CapReasoning 表示 model 支持深度推理 / 思维链。
	CapReasoning ModelCapability = "reasoning"

	// CapJSONMode 表示 model 支持结构化 JSON 输出模式。
	CapJSONMode ModelCapability = "json_mode"
)

// Source 表示 model profile 的来源，用于 Router 区分实际可用模型与默认候选。
// 当前仅在 ModelRegistry 内部元数据使用，不暴露给 API。
type ModelSource string

const (
	// SourceConfiguredProvider 表示该模型来自实际已配置的 provider（DB 发现或 .env 静态声明）。
	SourceConfiguredProvider ModelSource = "configured_provider"
	// SourceDefaultProfile 表示该模型来自内置 DefaultProfiles()，仅作为默认值补充。
	SourceDefaultProfile ModelSource = "default_profile"
)

// ModelProfile 描述一个 model 的完整画像，用于路由决策。
//
// 每个 profile 记录 model 的身份、能力、成本结构、技术限制
// 与 fallback 路径。Router 用这些信息为给定任务选择最合适的 model。
type ModelProfile struct {
	// Name 是 model 标识（例如 "deepseek-v4-flash"、"claude-sonnet-4-6"）。
	// 在 ModelRegistry 中，全名形式为 "provider/model_id"。
	Name string

	// Provider 标识 API provider（例如 "openai"、"anthropic"、"deepseek"）。
	Provider string

	// Tier 是 model 的能力/成本层级。
	Tier ModelTier

	// Capabilities 列出 model 支持的功能。
	Capabilities []ModelCapability

	// InputPrice 是每 1M 输入 token 的成本（USD）。
	InputPrice float64

	// OutputPrice 是每 1M 输出 token 的成本（USD）。
	OutputPrice float64

	// MaxContextWindow 是最大上下文长度（以 token 计）。
	MaxContextWindow int

	// MaxOutputTokens 是最大输出长度（以 token 计）。
	MaxOutputTokens int

	// RateLimitRPM 是每分钟最大请求数。
	RateLimitRPM int

	// FallbackModel 是该 model 不可用时的回退 model。
	// 空字符串表示未配置 fallback。
	FallbackModel string

	// AvgLatencyMs 是平均响应延迟（毫秒）。
	AvgLatencyMs int

	// Missing 表示 provider 发现时该 model 已不再返回。
	// Router 应将 missing=true 的模型排除在候选池之外。
	Missing bool

	// Source 记录 profile 来源，辅助 Router/调试识别模型是实际配置的还是默认补充。
	Source ModelSource
}

// HasCapability 检查 model 是否支持某个具体能力。
func (mp *ModelProfile) HasCapability(cap ModelCapability) bool {
	return slices.Contains(mp.Capabilities, cap)
}

// SupportsContextLen 检查 model 能否处理给定的上下文长度。
func (mp *ModelProfile) SupportsContextLen(tokens int) bool {
	return tokens <= mp.MaxContextWindow
}

// ModelRegistry 是可用 model profile 的中央目录。
//
// 它支持注册、按名查找、按层级/能力/上下文长度过滤，
// 以及 fallback 解析。registry 是 goroutine 安全的，可在运行时更新
//（例如新增 model 或 rate limit 变化时）。
//
// Phase 5 中，registry 在启动时从配置加载；Phase 6 中
// 将支持从数据库加载以实现动态更新。
type ModelRegistry struct {
	mu       sync.RWMutex
	profiles map[string]*ModelProfile // name → profile
	byTier   map[ModelTier][]string   // tier → model names
	// byProvider 按 provider 聚合 model name，用于 AvailableProfiles 过滤。
	byProvider map[string][]string
}

// NewModelRegistry 创建一个空的 model registry。
func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{
		profiles:   make(map[string]*ModelProfile),
		byTier:     make(map[ModelTier][]string),
		byProvider: make(map[string][]string),
	}
}

// Register 在 registry 中添加或更新一个 model profile。
// 若同名 profile 已存在，则会被覆盖。
func (r *ModelRegistry) Register(profile *ModelProfile) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.profiles[profile.Name] = profile
	r.byTier[profile.Tier] = append(r.byTier[profile.Tier], profile.Name)
	r.byProvider[profile.Provider] = append(r.byProvider[profile.Provider], profile.Name)
}

// Get 按名返回 model profile，未找到则返回 nil。
func (r *ModelRegistry) Get(name string) *ModelProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.profiles[name]
}

// GetByTier 返回指定层级内的所有 model profile。
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

// FilterByCapability 返回支持指定能力的所有 model，
// 按层级排序（最便宜的在前）。
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

// FilterByContextLen 返回能处理给定上下文长度的所有 model，
// 按层级排序（最便宜的在前）。
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

// GetFallback 返回给定 model 名对应的 fallback model。
// 若未配置 fallback 或该 model 不存在，则返回 nil。
func (r *ModelRegistry) GetFallback(name string) *ModelProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.profiles[name]
	if !ok || p.FallbackModel == "" {
		return nil
	}
	return r.profiles[p.FallbackModel]
}

// AvailableProfiles 返回可用于 Router 选择的实际模型集合。
// 过滤条件：
//   - configuredProviders 出现的 provider，或被强制声明为 available 的模型；
//   - profile.Missing == false；
//   - profile.Source 不为 default_profile，除非该 provider 在 configuredProviders 中。
//
// 若 configuredProviders 为空，只要模型源是 configured_provider 或 .env 静态模型
//（即非纯 DefaultProfiles）即视为可用。
func (r *ModelRegistry) AvailableProfiles(configuredProviders map[string]bool, allowDefault bool) []*ModelProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*ModelProfile
	seen := make(map[string]bool)
	for _, p := range r.profiles {
		if seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		if p.Missing {
			continue
		}
		providerConfigured := configuredProviders[p.Provider]
		isDefault := p.Source == SourceDefaultProfile
		if !providerConfigured && isDefault && !allowDefault {
			continue
		}
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Tier < result[j].Tier
	})
	return result
}

// Providers 返回 registry 中所有已知的 provider 名集合。
func (r *ModelRegistry) Providers() map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers := make(map[string]bool)
	for _, p := range r.profiles {
		providers[p.Provider] = true
	}
	return providers
}

// List 返回所有已注册的 model profile，按层级排序。
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

// DefaultProfiles 返回一组合理的默认 model profile。
// 在未提供配置时使用，保证系统对常见可用 model 开箱即用。
//
// 历史说明：DefaultProfiles 仅作为新部署的默认值补充源（ModelService seed），
// 不再是 Router 候选池。Router 只从 ModelRegistry 中实际已加载的模型里选择，
// 因此未配置对应 provider 的默认模型不会进入候选池。这解决了"配置了 1 个 provider
// 却因 DefaultProfiles 硬编码了数十个模型导致路由失败"的问题。
//
// 价格来源（USD per 1M tokens）：
//   - DeepSeek 官方定价
//   - Anthropic Claude 4 系列官方定价（2026-07）
//   - OpenAI GPT-5 系列官方定价（2026-07）
//   - Google Gemini 3 系列官方定价（2026-07）
//   - 本地/免费模型成本按 0 计算，仅作为上限参考
//
// 历史踩坑：早期版本 deepseek-v4-flash 的 OutputPrice 写成 0.29（笔误），
// deepseek-v4-pro 的 InputPrice/OutputPrice 写成 1.71/3.43（错误），
// 导致 /api/costs 的 CostCents 偏高或与官方价不符。已于 2026-07 修正为官方价。
// "deepseek-v4-flash-local" 等本地等效模型由 main.go 克隆本表第 0 项（flash）
// 改名注册，沿用 0.14/0.28 作为本地 API 的上界成本参考。
//
// 新增（2026-07-25）: 本次扩展补齐 TierFree/TierLightweight/TierStandard/TierPremium 全层，
// 引入 Claude / GPT / Gemini / Qwen / GLM / Kimi / MiniMax / Step 等模型，
// 为 multi-model 分层路由提供完整候选池。Fallback 链路优先指向同生态的低价模型或本地模型，
// 保证任一付费 API 失败时任务仍可继续。
func DefaultProfiles() []*ModelProfile {
	return []*ModelProfile{
		// ┌────────────────────────────────────────────┐
		// │ TierFree —— 本地/免费模型，成本为 0，用于冷备  │
		// └────────────────────────────────────────────┘
		{
			Name:             "deepseek-v4-flash-local",
			Provider:         "deepseek",
			Tier:             TierFree,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapJSONMode},
			InputPrice:       0.0,
			OutputPrice:      0.0,
			MaxContextWindow: 128000,
			MaxOutputTokens:  4096,
			RateLimitRPM:     0, // 本地无限制
			FallbackModel:    "",
			AvgLatencyMs:     1500,
		},
		{
			Name:             "qwen3.5-122b-local",
			Provider:         "openai",
			Tier:             TierFree,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapJSONMode},
			InputPrice:       0.0,
			OutputPrice:      0.0,
			MaxContextWindow: 128000,
			MaxOutputTokens:  4096,
			RateLimitRPM:     0,
			FallbackModel:    "",
			AvgLatencyMs:     2000,
		},
		{
			Name:             "glm-4.7-local",
			Provider:         "openai",
			Tier:             TierFree,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapJSONMode},
			InputPrice:       0.0,
			OutputPrice:      0.0,
			MaxContextWindow: 128000,
			MaxOutputTokens:  4096,
			RateLimitRPM:     0,
			FallbackModel:    "",
			AvgLatencyMs:     1800,
		},
		{
			Name:             "kimi-k2.7-code-local",
			Provider:         "openai",
			Tier:             TierFree,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapJSONMode},
			InputPrice:       0.0,
			OutputPrice:      0.0,
			MaxContextWindow: 128000,
			MaxOutputTokens:  4096,
			RateLimitRPM:     0,
			FallbackModel:    "",
			AvgLatencyMs:     1800,
		},
		{
			Name:             "minimax-m2.5",
			Provider:         "openai",
			Tier:             TierFree,
			Capabilities:     []ModelCapability{CapStreaming, CapJSONMode},
			InputPrice:       0.0,
			OutputPrice:      0.0,
			MaxContextWindow: 32000,
			MaxOutputTokens:  4096,
			RateLimitRPM:     100,
			FallbackModel:    "",
			AvgLatencyMs:     2000,
		},

		// ┌────────────────────────────────────────────┐
		// │ TierEfficient —— 低成本高吞吐               │
		// └────────────────────────────────────────────┘
		{
			Name:             "deepseek-v4-flash",
			Provider:         "deepseek",
			Tier:             TierEfficient,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapJSONMode},
			InputPrice:       0.14,
			OutputPrice:      0.28,
			MaxContextWindow: 128000,
			MaxOutputTokens:  4096,
			RateLimitRPM:     500,
			FallbackModel:    "deepseek-v4-flash-local",
			AvgLatencyMs:     800,
		},
		{
			Name:             "gpt-5.4-mini",
			Provider:         "openai",
			Tier:             TierEfficient,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapJSONMode},
			InputPrice:       0.75,
			OutputPrice:      4.5,
			MaxContextWindow: 128000,
			MaxOutputTokens:  4096,
			RateLimitRPM:     300,
			FallbackModel:    "deepseek-v4-flash",
			AvgLatencyMs:     700,
		},
		{
			Name:             "gemini-3-flash-preview",
			Provider:         "gemini",
			Tier:             TierEfficient,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapJSONMode},
			InputPrice:       0.5,
			OutputPrice:      3.0,
			MaxContextWindow: 1000000,
			MaxOutputTokens:  8192,
			RateLimitRPM:     300,
			FallbackModel:    "deepseek-v4-flash",
			AvgLatencyMs:     900,
		},
		{
			Name:             "gpt-5.4-nano",
			Provider:         "openai",
			Tier:             TierEfficient,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapJSONMode},
			InputPrice:       0.2,
			OutputPrice:      1.25,
			MaxContextWindow: 128000,
			MaxOutputTokens:  2048,
			RateLimitRPM:     600,
			FallbackModel:    "deepseek-v4-flash",
			AvgLatencyMs:     500,
		},
		{
			Name:             "step-3.7-flash",
			Provider:         "openai",
			Tier:             TierEfficient,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapReasoning, CapJSONMode},
			InputPrice:       0.0,
			OutputPrice:      0.0,
			MaxContextWindow: 128000,
			MaxOutputTokens:  4096,
			RateLimitRPM:     100,
			FallbackModel:    "deepseek-v4-flash",
			AvgLatencyMs:     1200,
		},

		// ┌────────────────────────────────────────────┐
		// │ TierLightweight —— 快速分类/路由             │
		// └────────────────────────────────────────────┘
		{
			Name:             "claude-haiku-4-5",
			Provider:         "anthropic",
			Tier:             TierLightweight,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapJSONMode},
			InputPrice:       1.0,
			OutputPrice:      5.0,
			MaxContextWindow: 200000,
			MaxOutputTokens:  4096,
			RateLimitRPM:     400,
			FallbackModel:    "deepseek-v4-flash",
			AvgLatencyMs:     600,
		},

		// ┌────────────────────────────────────────────┐
		// │ TierStandard —— 主力 Agent 执行             │
		// └────────────────────────────────────────────┘
		{
			Name:             "deepseek-v4-pro",
			Provider:         "deepseek",
			Tier:             TierStandard,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapReasoning, CapJSONMode},
			InputPrice:       0.435,
			OutputPrice:      0.87,
			MaxContextWindow: 128000,
			MaxOutputTokens:  8192,
			RateLimitRPM:     200,
			FallbackModel:    "deepseek-v4-flash",
			AvgLatencyMs:     1500,
		},
		{
			Name:             "claude-sonnet-4-5",
			Provider:         "anthropic",
			Tier:             TierStandard,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapReasoning, CapJSONMode},
			InputPrice:       3.0,
			OutputPrice:      15.0,
			MaxContextWindow: 200000,
			MaxOutputTokens:  8192,
			RateLimitRPM:     200,
			FallbackModel:    "deepseek-v4-pro",
			AvgLatencyMs:     1200,
		},
		{
			Name:             "claude-sonnet-4-6",
			Provider:         "anthropic",
			Tier:             TierStandard,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapReasoning, CapJSONMode},
			InputPrice:       3.0,
			OutputPrice:      15.0,
			MaxContextWindow: 200000,
			MaxOutputTokens:  8192,
			RateLimitRPM:     200,
			FallbackModel:    "deepseek-v4-pro",
			AvgLatencyMs:     1200,
		},
		{
			Name:             "gpt-5.4",
			Provider:         "openai",
			Tier:             TierStandard,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapReasoning, CapJSONMode},
			InputPrice:       2.5,
			OutputPrice:      15.0,
			MaxContextWindow: 256000,
			MaxOutputTokens:  16384,
			RateLimitRPM:     200,
			FallbackModel:    "deepseek-v4-pro",
			AvgLatencyMs:     1100,
		},
		{
			Name:             "gpt-5.3-codex",
			Provider:         "openai",
			Tier:             TierStandard,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapReasoning, CapJSONMode},
			InputPrice:       1.75,
			OutputPrice:      14.0,
			MaxContextWindow: 256000,
			MaxOutputTokens:  16384,
			RateLimitRPM:     200,
			FallbackModel:    "deepseek-v4-pro",
			AvgLatencyMs:     1100,
		},
		{
			Name:             "gemini-3.1-pro-preview",
			Provider:         "gemini",
			Tier:             TierStandard,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapReasoning, CapJSONMode},
			InputPrice:       2.0,
			OutputPrice:      12.0,
			MaxContextWindow: 2000000,
			MaxOutputTokens:  8192,
			RateLimitRPM:     200,
			FallbackModel:    "deepseek-v4-pro",
			AvgLatencyMs:     1300,
		},
		{
			Name:             "qwen3.5-397b",
			Provider:         "openai",
			Tier:             TierStandard,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapReasoning, CapJSONMode},
			InputPrice:       0.0,
			OutputPrice:      0.0,
			MaxContextWindow: 128000,
			MaxOutputTokens:  8192,
			RateLimitRPM:     100,
			FallbackModel:    "qwen3.5-122b-local",
			AvgLatencyMs:     2500,
		},

		// ┌────────────────────────────────────────────┐
		// │ TierPremium —— 顶级推理 / 编排决策            │
		// └────────────────────────────────────────────┘
		{
			Name:             "claude-opus-4-5",
			Provider:         "anthropic",
			Tier:             TierPremium,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapReasoning, CapJSONMode},
			InputPrice:       5.0,
			OutputPrice:      25.0,
			MaxContextWindow: 200000,
			MaxOutputTokens:  16384,
			RateLimitRPM:     120,
			FallbackModel:    "claude-sonnet-4-6",
			AvgLatencyMs:     2500,
		},
		{
			Name:             "claude-opus-4-6",
			Provider:         "anthropic",
			Tier:             TierPremium,
			Capabilities:     []ModelCapability{CapToolCalling, CapStreaming, CapReasoning, CapJSONMode},
			InputPrice:       5.0,
			OutputPrice:      25.0,
			MaxContextWindow: 200000,
			MaxOutputTokens:  16384,
			RateLimitRPM:     120,
			FallbackModel:    "claude-sonnet-4-6",
			AvgLatencyMs:     2500,
		},
	}
}