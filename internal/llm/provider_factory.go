// Package llm — Provider factory for creating LLM provider instances.
//
// # Design Rationale
//
// The Provider factory centralizes provider instantiation, decoupling the Server
// and Engine from concrete provider types. Instead of calling NewOpenAIProvider
// directly, callers use NewProvider with a ProviderConfig, and the factory selects
// the correct implementation based on the provider name.
//
// This design enables:
//   - Clean addition of new providers without changing the Server or Engine code
//   - Automatic fallback: unknown provider names default to OpenAI-compatible
//   - Configuration-driven provider selection (loaded from Config.Models)
//
// # Provider Mapping
//
//   - "openai"    → OpenAIProvider  — direct implementation of OpenAI-compatible API
//   - "deepseek"  → OpenAIProvider  — DeepSeek's API is fully OpenAI-compatible
//        (same /chat/completions endpoint, same request/response format)
//   - "anthropic" → AnthropicProvider — Claude's Messages API (Phase 6)
//        Different endpoint (/v1/messages), auth (x-api-key), and streaming format.
//   - default     → OpenAIProvider  — any unrecognized name falls back to
//        OpenAI-compatible, since most providers (Groq, Together, Fireworks, etc.)
//        share this protocol.
//
// # Usage
//
//	provider, err := llm.NewProvider(llm.ProviderConfig{
//	    Name:     "deepseek",
//	    Endpoint: "https://aicoding.dobest.com/v1",
//	    APIKey:   "sk-xxx",
//	    Model:    "deepseek-v4-flash",
//	})
package llm

// ProviderConfig holds the configuration parameters for creating a Provider instance.
// It mirrors the fields needed by all provider constructors, providing a uniform
// interface regardless of the underlying provider type.
type ProviderConfig struct {
	// Name is the provider identifier ("openai", "deepseek", "anthropic", etc.)
	Name string

	// Endpoint is the API base URL (e.g., "https://api.openai.com/v1")
	Endpoint string

	// APIKey is the authentication token (Bearer token for OpenAI-compatible APIs)
	APIKey string

	// Model is the default model name for this provider (e.g., "deepseek-v4-flash")
	Model string
}

// NewProvider creates a Provider instance based on the provider name in cfg.
//
// Supported provider names:
//   - "openai"   → OpenAIProvider (OpenAI-compatible API)
//   - "deepseek" → OpenAIProvider (DeepSeek's API is OpenAI-compatible)
//   - "anthropic" → returns an error (not yet implemented; different API format)
//   - anything else → OpenAIProvider (safe fallback for OpenAI-compatible providers)
//
// Returns an error if the factory cannot create the provider (e.g., missing API key
// for anthropic, which requires different config semantics).
func NewProvider(cfg ProviderConfig) (Provider, error) {
	switch cfg.Name {
	case "openai":
		// OpenAIProvider directly implements OpenAI's Chat Completions API.
		return NewOpenAIProvider(cfg.Name, cfg.Endpoint, cfg.APIKey, cfg.Model), nil

	case "deepseek":
		// DeepSeek's API is fully OpenAI-compatible — it uses /chat/completions
		// with the same request/response format, Bearer token auth, and SSE streaming.
		// The only difference is reasoning_content in deltas for R1/V4, which will
		// be handled by a DeepSeekProvider extension in a later Phase.
		// For now, reuse OpenAIProvider which works correctly.
		return NewOpenAIProvider(cfg.Name, cfg.Endpoint, cfg.APIKey, cfg.Model), nil

	case "anthropic":
		// AnthropicProvider implements Claude's Messages API with proper format
		// conversion (system prompt, input_schema, x-api-key header, etc.).
		return NewAnthropicProvider(cfg.Name, cfg.Endpoint, cfg.APIKey, cfg.Model), nil

	default:
		// Safe fallback: most LLM providers (Groq, Together, Fireworks, etc.)
		// implement OpenAI's Chat Completions API, so default to OpenAIProvider.
		// This allows the system to work with new providers without code changes.
		return NewOpenAIProvider(cfg.Name, cfg.Endpoint, cfg.APIKey, cfg.Model), nil
	}
}
