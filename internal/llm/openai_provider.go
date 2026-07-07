// Package llm — OpenAIProvider: baseline implementation of the Provider interface.
//
// OpenAIProvider wraps the existing Client and implements the Provider interface.
// It supports all OpenAI-compatible APIs including DeepSeek, Groq, Together, etc.
//
// This is the baseline implementation — other providers (Anthropic, DeepSeek with
// reasoning_content) will be added in Phase 6.
//
// # Protocol Notes
//
// OpenAI-compatible APIs share a common protocol:
//   - POST /chat/completions for both streaming and non-streaming
//   - Authorization: Bearer <api_key> header
//   - SSE streaming with "data: {...}" format and "data: [DONE]" termination
//   - Tool calls embedded in the assistant message as function_call/tool_calls
//   - Usage reported in the final SSE chunk (or in the response body for non-streaming)
//
// DeepSeek's API is fully OpenAI-compatible, so it works with this provider.
// The only extension is reasoning_content in the delta (for R1/V4 reasoning),
// which will be handled by a DeepSeekProvider extending this one in Phase 6.
package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider implements the Provider interface for OpenAI-compatible APIs.
//
// It wraps the existing Client's HTTP logic but exposes it through the Provider
// interface. The Engine now uses a Provider instead of a *Client directly, enabling
// future multi-provider support.
//
// # Thread Safety
//
// OpenAIProvider is safe for concurrent use — each call to ChatStream creates
// its own HTTP request and response. The underlying http.Client is also
// goroutine-safe.
type OpenAIProvider struct {
	name     string       // provider name (e.g., "openai", "deepseek")
	endpoint string       // base URL (e.g., "https://api.openai.com/v1")
	apiKey   string       // Bearer token
	model    string       // model name (e.g., "gpt-4o", "deepseek-v4-flash")
	http     *http.Client // configured with 120s timeout
}

// NewOpenAIProvider creates a new OpenAI-compatible provider.
//
// The endpoint is trimmed of trailing slashes for consistent URL construction.
// The model is the default model for this provider — it can be overridden per-request
// by setting ChatRequest.Model.
func NewOpenAIProvider(name, endpoint, apiKey, model string) *OpenAIProvider {
	return &OpenAIProvider{
		name:     name,
		endpoint: strings.TrimRight(endpoint, "/"),
		apiKey:   apiKey,
		model:    model,
		http:     &http.Client{Timeout: 120 * time.Second},
	}
}

// Name returns the provider identifier.
func (p *OpenAIProvider) Name() string {
	return p.name
}

// Chat sends a non-streaming chat request and returns the full response.
func (p *OpenAIProvider) Chat(req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", p.endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &chatResp, nil
}

// ChatStream sends a streaming chat request and calls onChunk for each SSE event.
//
// # SSE Parsing Strategy
//
//  1. Read lines with bufio.Scanner (supports up to 1MB lines).
//  2. Skip empty lines, comments (:), and non-data lines.
//  3. Parse each "data: {...}" line as a JSON chunk.
//  4. Accumulate content in a strings.Builder for full text.
//  5. Accumulate ToolCall deltas in a map[int]*ToolCall (deltas may arrive
//     out of order — the index/ID arrives first, then name, then arguments).
//  6. Extract Usage from the final chunk (which carries the full usage object).
//
// The onChunk callback is called for each parsed chunk, enabling the Engine
// to forward text deltas to the frontend in real time.
func (p *OpenAIProvider) ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error) {
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", p.endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", Usage{}, nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var (
		contentBuilder strings.Builder
		toolCalls      []ToolCall
		usage          Usage
		toolCallMap    = make(map[int]*ToolCall)
	)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta        Delta `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.Usage != nil {
			usage = *chunk.Usage
			if usage.TotalTokens == 0 {
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}
			if usage.PromptCacheMissTokens == 0 && usage.PromptTokens > 0 {
				usage.PromptCacheMissTokens = usage.PromptTokens - usage.PromptCacheHitTokens
			}
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		if choice.Delta.Content != "" {
			contentBuilder.WriteString(choice.Delta.Content)
		}

		// Accumulate reasoning content (chain-of-thought) from DeepSeek R1/V4 models.
		// reasoning_content is emitted alongside content in the same delta by DeepSeek's
		// reasoning models; we merge it into the full text accumulation.
		if choice.Delta.ReasoningContent != "" {
			contentBuilder.WriteString(choice.Delta.ReasoningContent)
		}

		for _, tc := range choice.Delta.ToolCalls {
			idx := tc.Idx
			if existing, ok := toolCallMap[idx]; ok {
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Function.Name != "" {
					existing.Function.Name = tc.Function.Name
				}
				existing.Function.Arguments += tc.Function.Arguments
			} else {
				toolCallMap[idx] = &ToolCall{
					Idx:  idx,
					ID:   tc.ID,
					Type: tc.Type,
					Function: FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}

		if onChunk != nil {
			sc := StreamChunk{
				Delta:        choice.Delta,
				FinishReason: choice.FinishReason,
			}
			if chunk.Usage != nil {
				sc.Usage = *chunk.Usage
			}
			if err := onChunk(sc); err != nil {
				return "", usage, nil, err
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", usage, nil, fmt.Errorf("scan stream: %w", err)
	}

	for i := 0; i < len(toolCallMap); i++ {
		if tc, ok := toolCallMap[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}

	return contentBuilder.String(), usage, toolCalls, nil
}