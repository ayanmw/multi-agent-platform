// Package llm —— ProfileResolver: 模型画像解析统一入口。
//
// 设计理由：
//   - 模型 tier 的通配符匹配逻辑原本分散在 ProviderManager / ModelService 中，
//     抽成独立结构后，保证启动期种子写入、Provider 发现同步、测试 helper 使用
//     同一套解析策略。
//   - 支持从 LLMProvider 配置与全局 cfg.Models 解析出 provider/type/endpoint/api_key，
//     供 provider_factory.go 同时支持全名 "provider/model" 与短名匹配。
package llm

import (
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/config"
)

// ProfileResolver 提供模型画像与 Provider 配置的解析能力。
// 它持有 Config 的只读视图，状态不可变，可在 goroutine 中安全使用。
type ProfileResolver struct {
	cfg            *config.Config
	providersByName map[string]config.LLMProviderConfig
}

// NewProfileResolver 从全局 Config 创建解析器。
func NewProfileResolver(cfg *config.Config) *ProfileResolver {
	pr := &ProfileResolver{
		cfg:             cfg,
		providersByName: make(map[string]config.LLMProviderConfig),
	}
	for _, pc := range cfg.LLMProviders {
		if pc.Name == "" {
			continue
		}
		pr.providersByName[pc.Name] = pc
	}
	return pr
}

// ResolveTier 用 ModelTierMapping 通配符解析 model ID 对应 tier。
// 未命中时返回 "standard" 作为保守默认值。
func (r *ProfileResolver) ResolveTier(modelID string) string {
	if r.cfg == nil {
		return "standard"
	}
	for pattern, tier := range r.cfg.ModelTierMapping {
		if matchWildcard(pattern, modelID) {
			return tier
		}
	}
	return "standard"
}

// ResolveProviderNameForModel 返回 model 在 .env 配置下应归属的 provider name。
// 未命中时返回 "default"，与 legacy 单模型路径一致。
func (r *ProfileResolver) ResolveProviderNameForModel(modelName string) string {
	if r.cfg == nil {
		return "default"
	}
	// 先按 cfg.Models 显式声明的 Provider 匹配。
	for _, mc := range r.cfg.Models {
		if mc.Name == modelName && mc.Provider != "" {
			return mc.Provider
		}
	}
	// 取任意第一个已声明的 Provider；单 Provider 配置时即为默认 Provider。
	for _, p := range r.cfg.LLMProviders {
		if p.Name != "" {
			return p.Name
		}
	}
	return "default"
}

// ResolveProviderForModel 解析 model 的完整 Provider 配置（type/endpoint/api_key）。
// modelName 支持全名 "provider/model" 或短名 "model"：
//   - 全名优先按 provider 查找 LLMProviders 条目；
//   - 短名则按 cfg.Models 中对应 model 的 Provider 字段查找；
//   - 都未命中时回退到 legacy LLM_ENDPOINT / LLM_API_KEY / LLM_MODEL。
func (r *ProfileResolver) ResolveProviderForModel(modelName string) ProviderConfig {
	providerName, shortName := splitScopedModelName(modelName)

	// 1. 全名 "provider/model": 直接按 providerName 查找 LLMProviders 条目。
	if providerName != "" {
		if pc, ok := r.providersByName[providerName]; ok {
			return r.providerConfigFromLLMProvider(pc, shortName)
		}
	}

	// 2. 短名：从 cfg.Models 中找出对应 model 的 Provider 字段。
	for _, mc := range r.cfg.Models {
		if mc.Name != shortName {
			continue
		}
		pc := ProviderConfig{
			Name:     mc.Provider,
			Endpoint: mc.Endpoint,
			APIKey:   mc.APIKey,
			Model:    mc.Name,
		}
		if pc.Name == "" {
			pc.Name = r.ResolveProviderNameForModel(mc.Name)
		}
		pc = r.fillDefaults(pc)
		return pc
	}

	// 3. 未命中：回退到 legacy 默认 Provider，锚定用户传入的 modelName。
	fallback := ProviderConfig{
		Name:     r.ResolveProviderNameForModel(shortName),
		Endpoint: r.cfg.LLMEndpoint,
		APIKey:   r.cfg.LLMAPIKey,
		Model:    shortName,
	}
	fallback = r.fillDefaults(fallback)
	return fallback
}

// providerConfigFromLLMProvider 把 LLMProviderConfig 转换为 ProviderConfig。
func (r *ProfileResolver) providerConfigFromLLMProvider(pc config.LLMProviderConfig, model string) ProviderConfig {
	cfg := ProviderConfig{
		Name:     pc.Type,
		Endpoint: pc.Endpoint,
		APIKey:   pc.APIKey,
		Model:    model,
	}
	return r.fillDefaults(cfg)
}

// fillDefaults 补齐未配置 endpoint / api_key 时的默认值。
func (r *ProfileResolver) fillDefaults(pc ProviderConfig) ProviderConfig {
	if r.cfg == nil {
		return pc
	}
	if pc.Name == "" {
		pc.Name = "openai"
	}
	// 未配置 endpoint/key 时，按 provider 类型回退到全局字段。
	switch pc.Name {
	case "anthropic":
		if pc.Endpoint == "" {
			pc.Endpoint = r.cfg.AnthropicEndpoint
		}
		if pc.APIKey == "" {
			pc.APIKey = r.cfg.AnthropicAPIKey
		}
	case "gemini":
		if pc.Endpoint == "" {
			pc.Endpoint = r.cfg.GeminiEndpoint
		}
		if pc.APIKey == "" {
			pc.APIKey = r.cfg.GeminiAPIKey
		}
	}
	if pc.Endpoint == "" {
		pc.Endpoint = r.cfg.LLMEndpoint
	}
	if pc.APIKey == "" {
		pc.APIKey = r.cfg.LLMAPIKey
	}
	return pc
}

// splitScopedModelName 把 "provider/model" 拆分为 provider 与 model 两部分。
// 不含斜杠时返回 ("", 原值)。
func splitScopedModelName(name string) (provider, model string) {
	parts := strings.SplitN(name, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", name
}
