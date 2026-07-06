// Package llm — Provider interface for LLM protocol abstraction.
//
// # Design Rationale
//
// The Provider interface abstracts the LLM API protocol so the Engine can work
// with different providers (OpenAI, Anthropic, DeepSeek) without code changes.
//
// Each provider has its own:
//   - Request/response format (OpenAI uses Chat Completions API, Anthropic uses Messages API)
//   - Authentication scheme (Bearer token vs x-api-key header)
//   - Streaming protocol (SSE vs Server-Sent Events with different field names)
//   - Tool call representation (function calling vs tool_use blocks)
//   - Usage/token reporting format
//
// The Provider interface encapsulates these differences behind a single ChatStream
// method. The Engine only sees the unified ChatRequest/StreamChunk/Usage/ToolCall
// types — the provider handles conversion internally.
//
// # Current Status (Phase 5)
//
//   - OpenAIProvider: baseline implementation wrapping the existing Client.
//     Supports all OpenAI-compatible APIs (DeepSeek, Groq, Together, etc.).
//   - AnthropicProvider: Phase 6 — requires full request/response format conversion
//     because Anthropic's Messages API is structurally different from Chat Completions.
//   - DeepSeekProvider: Phase 6 — extends OpenAIProvider with reasoning_content support
//     for DeepSeek R1/V4's chain-of-thought output.
//
// # Usage
//
//	provider := llm.NewOpenAIProvider(endpoint, apiKey, model)
//	content, usage, toolCalls, err := provider.ChatStream(req, onChunk)
//
// See doc/chapters/09-llm-api-comparison.html for detailed differences between providers,
// and doc/chapters/10-multi-model-layered-design.html for the multi-model routing strategy.
package llm

// Provider abstracts the LLM API protocol, allowing the Engine to work with
// different LLM providers without code changes.
//
// Each provider implementation handles:
//   - Building the HTTP request (URL, headers, body format)
//   - Parsing the streaming response (SSE format, delta structure, tool call accumulation)
//   - Converting provider-specific types to the unified ChatRequest/StreamChunk/Usage/ToolCall types
//
// The Provider interface is intentionally minimal — it only exposes the two methods
// the Engine needs: Chat (non-streaming) and ChatStream (streaming). This keeps
// implementations simple and testable.
type Provider interface {
	// Name returns the provider identifier (e.g., "openai", "anthropic", "deepseek").
	// Used for logging, cost tracking, and model registry lookups.
	Name() string

	// Chat sends a non-streaming chat request and returns the full response.
	// Used for simple synchronous calls where streaming is not needed
	// (e.g., Router intent classification, simple validation).
	Chat(req ChatRequest) (*ChatResponse, error)

	// ChatStream sends a streaming chat request and calls onChunk for each SSE event.
	// Returns the accumulated content, usage, tool calls, and any error.
	//
	// The onChunk callback is called for each parsed SSE chunk. Each chunk contains
	// a text delta, tool call delta, and/or finish reason. The callback is the
	// mechanism that enables the "white-box" philosophy — every token the LLM
	// generates is forwarded to the frontend in real time.
	//
	// The returned Usage is from the final SSE chunk (the only chunk that carries
	// usage data in OpenAI-compatible APIs). The returned ToolCalls are the fully
	// assembled tool calls after all deltas have been accumulated.
	ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error)
}