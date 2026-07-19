// Package llm —— Provider 工厂，用于创建 LLM provider 实例。
//
// # 设计理由
//
// Provider 工厂集中 provider 实例化，将 Server 与 Engine 与具体 provider 类型解耦。
// 调用方不再直接调用 NewOpenAIProvider，而是用 NewProvider 配合 ProviderConfig，
// 由工厂根据 provider 名选择正确的实现。
//
// 该设计支持：
//   - 无需改动 Server 或 Engine 代码即可新增 provider
//   - 自动 fallback：未识别的 provider 名默认走 OpenAI-compatible
//   - 基于配置的 provider 选择（从 Config.Models 加载）
//
// # Provider 映射
//
//   - "openai"    → OpenAIProvider  —— OpenAI-compatible API 的直接实现
//   - "deepseek"  → OpenAIProvider  —— DeepSeek 的 API 完全 OpenAI-compatible
//        （同样的 /chat/completions endpoint，同样的请求/响应格式）
//   - "anthropic" → AnthropicProvider —— Claude 的 Messages API（Phase 6）
//        不同的 endpoint（/v1/messages）、认证（x-api-key）与 streaming 格式。
//   - "mock"      → MockProvider —— 用于测试/演示的确定性脚本响应。
//   - default     → OpenAIProvider  —— 任何未识别的名称都回退到
//        OpenAI-compatible，因为多数 provider（Groq、Together、Fireworks 等）
//        都使用该协议。
//
// # 用法
//
//	provider, err := llm.NewProvider(llm.ProviderConfig{
//	    Name:     "deepseek",
//	    Endpoint: "https://aicoding.dobest.com/v1",
//	    APIKey:   "sk-xxx",
//	    Model:    "deepseek-v4-flash",
//	})
package llm

import "github.com/anmingwei/multi-agent-platform/internal/config"

// ProviderConfig 持有创建 Provider 实例所需的配置参数。
// 它对齐所有 provider 构造器所需字段，无论底层 provider 类型如何，
// 都提供统一接口。
type ProviderConfig struct {
	// Name 是 provider 标识（"openai"、"deepseek"、"anthropic"、"mock" 等）
	Name string

	// Endpoint 是 API 基础 URL（例如 "https://api.openai.com/v1"）
	Endpoint string

	// APIKey 是认证 token（OpenAI-compatible API 用 Bearer token）
	APIKey string

	// Model 是该 provider 的默认 model 名（例如 "deepseek-v4-flash"）
	Model string

	// CaseID 是可选 hint，供 MockProvider 选择 mock 脚本。
	CaseID string

	// MockStore 是创建 MockProvider 时使用的可选 mock 脚本 store。
	MockStore MockScriptStore
}

// NewProvider 根据 cfg 中的 provider 名创建 Provider 实例。
//
// 支持的 provider 名：
//   - "openai"   → OpenAIProvider（OpenAI-compatible API）
//   - "deepseek" → OpenAIProvider（DeepSeek 的 API 兼容 OpenAI）
//   - "anthropic" → AnthropicProvider（Claude Messages API，Phase 6）
//   - "mock"   → MockProvider（确定性脚本响应）
//   - 其他 → OpenAIProvider（对 OpenAI-compatible provider 的安全回退）
//
// 仅当 provider 名被识别但底层构造器失败（例如缺 API key）时返回 error。
func NewProvider(cfg ProviderConfig) (Provider, error) {
	if cfg.Name == "mock" {
		model := cfg.Model
		if model == "" {
			model = "mock/" + cfg.CaseID
		}
		return NewMockProvider(model, cfg.MockStore, BuiltinMockScripts()), nil
	}

	switch cfg.Name {
	case "openai":
		// OpenAIProvider 直接实现 OpenAI 的 Chat Completions API。
		return NewOpenAIProvider(cfg.Name, cfg.Endpoint, cfg.APIKey, cfg.Model), nil

	case "deepseek":
		// DeepSeek 的 API 完全 OpenAI-compatible —— 它使用 /chat/completions
		// 与相同的请求/响应格式、Bearer token 认证、SSE streaming。
		// 唯一差异是 R1/V4 delta 中的 reasoning_content，后续 Phase 将由
		// DeepSeekProvider 扩展处理。
		// 目前直接复用 OpenAIProvider 即可正常工作。
		return NewOpenAIProvider(cfg.Name, cfg.Endpoint, cfg.APIKey, cfg.Model), nil

	case "anthropic":
		// AnthropicProvider 实现 Claude 的 Messages API，含完整格式转换
		//（system prompt、input_schema、x-api-key header 等）。
		return NewAnthropicProvider(cfg.Name, cfg.Endpoint, cfg.APIKey, cfg.Model), nil

	default:
		// 安全回退：多数 LLM provider（Groq、Together、Fireworks 等）
		// 都实现 OpenAI 的 Chat Completions API，故默认走 OpenAIProvider。
		// 这样新 provider 无需改代码即可接入。
		return NewOpenAIProvider(cfg.Name, cfg.Endpoint, cfg.APIKey, cfg.Model), nil
	}
}

// CreateProviderFromConfig 根据全局配置创建 Provider，
// 依据 cfg.ShouldMock 选择 MockProvider 或真实 provider。
//
// 若选择 mock 模式，provider 名为 "mock"、Model 为
// "mock/<caseID>"，以便成本/metrics 流水线识别 mock 调用。
//
// 对于真实 provider，它会在 cfg.Models 中查找 modelName。若未找到匹配的
// model 配置，则回退到 cfg.LLMEndpoint 与 cfg.LLMModel，
// 作为 OpenAI-compatible provider 使用。
func CreateProviderFromConfig(cfg *config.Config, modelName string, caseID string) (Provider, error) {
	if cfg.ShouldMock(caseID, "") {
		return NewProvider(ProviderConfig{
			Name:      "mock",
			Model:     "mock/" + caseID,
			CaseID:    caseID,
			MockStore: DefaultMockStore,
		})
	}

	// 按名查找 model 配置；未找到则回退到默认字段。
	var mc config.ModelConfig
	found := false
	for _, m := range cfg.Models {
		if m.Name == modelName {
			mc = m
			found = true
			break
		}
	}
	if !found {
		mc = config.ModelConfig{
			Name:     cfg.LLMModel,
			Provider: "openai",
			Endpoint: cfg.LLMEndpoint,
			APIKey:   cfg.LLMAPIKey,
		}
	}
	if mc.Provider == "" {
		mc.Provider = "openai"
	}

	return NewProvider(ProviderConfig{
		Name:     mc.Provider,
		Endpoint: mc.Endpoint,
		APIKey:   mc.APIKey,
		Model:    mc.Name,
	})
}
