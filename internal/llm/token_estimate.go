// Package llm provides token estimation utilities for context window accounting.
//
// # Design Rationale
//
// Most OpenAI-compatible APIs only report aggregate usage for an entire request.
// They do not break down prompt_tokens per message. To give the frontend a
// white-box view of how the context window is filled, the Engine needs a local
// tokenizer that estimates per-message token counts.
//
// The current implementation uses a simple but sufficient heuristic:
//   - Estimate ~4 characters per token for text-heavy content.
//   - Add a small per-message overhead to account for role tokens and delimiters.
//
// This is intentionally chosen over heavier alternatives (tiktoken port,
// embedding API calls) because:
//   1. It requires no external dependencies.
//   2. It is deterministic and cheap.
//   3. For context-window *proportion* visualization, relative accuracy is
//      sufficient; exact API token counts are not required.
//
// Future work (Phase 7+) may introduce tiktoken-based counting if exact values
// are needed, but the public API in this file stays the same.
package llm

// messageOverheadTokens is the estimated per-message formatting cost from the
// OpenAI chat format: role, separators, and name/tool_call_id metadata.
const messageOverheadTokens = 5

// EstimateTokenCount approximates the token count for a single message.
//
// The estimate uses ~4 characters per token plus a fixed per-message overhead.
// It is fast, dependency-free, and accurate enough for proportion-based UI
// visualizations. The returned value is always non-negative.
func EstimateTokenCount(msg Message) int {
	// Combine all textual content that contributes to context length.
	text := msg.Content + msg.Reasoning
	tokens := len(text)/4 + messageOverheadTokens
	if tokens < 0 {
		tokens = 0
	}
	return tokens
}

// SumEstimatedTokens returns the estimated total tokens for a message slice.
func SumEstimatedTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += EstimateTokenCount(m)
	}
	return total
}

// ContextSnapshotMessage is the per-message payload included in the
// context_window_snapshot event that the Engine emits before each LLM call.
type ContextSnapshotMessage struct {
	Role          string     `json:"role"`
	Content       string     `json:"content"`
	Reasoning     string     `json:"reasoning,omitempty"`
	EstimatedTokens int      `json:"estimated_tokens"`
	UsageRatio    float64    `json:"usage_ratio"`
	ToolCallID    string     `json:"tool_call_id,omitempty"`
	ToolCalls     []ToolCall `json:"tool_calls,omitempty"`
}

// ContextWindowSnapshot describes the state of the context window right before
// an LLM call. It is intentionally lightweight and human-readable so the
// frontend can render both a progress bar and a message-level breakdown.
type ContextWindowSnapshot struct {
	Model                string                   `json:"model"`
	MaxContextTokens     int                      `json:"max_context_tokens"`
	EstimatedTotalTokens int                      `json:"estimated_total_tokens"`
	EstimatedUsageRatio  float64                  `json:"estimated_usage_ratio"`
	Messages             []ContextSnapshotMessage `json:"messages"`
}

// BuildContextWindowSnapshot constructs a snapshot of the current context window.
//
// maxContextTokens is read from the selected model's profile. If it is zero,
// the snapshot is built without a ratio (usage_ratio remains 0).
func BuildContextWindowSnapshot(model string, maxContextTokens int, messages []Message) ContextWindowSnapshot {
	total := SumEstimatedTokens(messages)

	out := ContextWindowSnapshot{
		Model:                model,
		MaxContextTokens:     maxContextTokens,
		EstimatedTotalTokens: total,
		Messages:             make([]ContextSnapshotMessage, 0, len(messages)),
	}

	if maxContextTokens > 0 && total > 0 {
		out.EstimatedUsageRatio = float64(total) / float64(maxContextTokens)
		if out.EstimatedUsageRatio > 1.0 {
			out.EstimatedUsageRatio = 1.0
		}
	}

	for _, m := range messages {
		msgTokens := EstimateTokenCount(m)
		ratio := 0.0
		if total > 0 {
			ratio = float64(msgTokens) / float64(total)
		}
		out.Messages = append(out.Messages, ContextSnapshotMessage{
			Role:            m.Role,
			Content:         m.Content,
			Reasoning:       m.Reasoning,
			EstimatedTokens: msgTokens,
			UsageRatio:      ratio,
			ToolCallID:      m.ToolCallID,
			ToolCalls:       m.ToolCalls,
		})
	}

	return out
}

// defaultContextWindow is the fallback max context length used when a model's
// profile is unavailable. 200K aligns with current mainstream large-context
// models and gives the UI a reasonable baseline until Phase 7 introduces a
// provider-aware context-window registry.
const defaultContextWindow = 200_000

// EstimateModelContextWindow resolves a model's max context window from the
// registry. If the model is unknown or the registry is nil, it returns the
// default 200K fallback so the UI capacity gauge is not artificially capped.
func EstimateModelContextWindow(registry *ModelRegistry, model string) int {
	if registry != nil && model != "" {
		if p := registry.Get(model); p != nil && p.MaxContextWindow > 0 {
			return p.MaxContextWindow
		}
	}
	return defaultContextWindow
}
