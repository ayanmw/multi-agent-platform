// Package llm provides an OpenAI-compatible HTTP client for chat completions.
//
// It supports both non-streaming (Chat) and SSE streaming (ChatStream) modes,
// and handles tool_call delta accumulation for ReAct Agent loops.
//
// Design notes:
//   - The client is intentionally a thin HTTP wrapper — all ReAct logic lives in runtime/engine.go.
//   - SSE streaming is parsed line-by-line with bufio.Scanner to avoid buffering the entire response.
//   - ToolCall index tracking uses a map[int]*ToolCall because SSE deltas arrive out of order.
//   - Usage is always read from the final SSE chunk, per OpenAI-compatible API convention.
//
// TODO: Phase 5-6 — Provider 接口抽象
//   当前 Client 硬编码 OpenAI-compatible 协议。为支持 Anthropic（完全不兼容）和
//   DeepSeek（reasoning_content 扩展），需要抽取 Provider 接口：
//     type Provider interface {
//         Name() string
//         Endpoint() string
//         BuildRequest(*ChatRequest) ([]byte, error)
//         ParseStreamChunk([]byte) (*StreamDelta, error)
//         BuildHeaders() map[string]string
//     }
//   参见 doc/chapters/09-llm-api-comparison.html 各厂商差异分析。
//   实现分步走：
//     Phase 3-4: Delta 结构体增加 ReasoningContent 字段（DeepSeek R1 思维链）
//     Phase 5:   抽取 Provider 接口 + OpenAIProvider 基线实现
//     Phase 6:   AnthropicProvider + DeepSeekProvider + Embeddings API
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

// Message represents a chat message in OpenAI-compatible format.
// It supports text content, tool calls (assistant role), and tool results (tool role).
type Message struct {
	Role       string     `json:"role"`                   // "system", "user", "assistant", "tool"
	Content    string     `json:"content,omitempty"`      // text content (empty for tool calls)
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // LLM-requested function calls
	ToolCallID string     `json:"tool_call_id,omitempty"` // correlates tool result to call
	Name       string     `json:"name,omitempty"`         // optional agent name
}

// ToolCall represents a function call request from the LLM.
// During SSE streaming, ToolCall deltas arrive incrementally — the ID arrives first,
// the function name next, and arguments last (often across multiple chunks).
type ToolCall struct {
	Idx      int          `json:"index"`    // 0-based index for ordering multiple tool calls
	ID       string       `json:"id"`       // unique call ID for tool result correlation
	Type     string       `json:"type"`     // always "function"
	Function FunctionCall `json:"function"` // the function name + arguments
}

// FunctionCall holds the name and JSON-encoded arguments of a tool call.
// Arguments is a JSON string (not an object) because the LLM streams it incrementally.
type FunctionCall struct {
	Name      string `json:"name"`      // tool name (e.g., "run_shell")
	Arguments string `json:"arguments"` // JSON-encoded arguments string
}

// ToolDef is a tool definition sent to the LLM as part of the chat request.
// It tells the LLM what tools are available and how to call them.
type ToolDef struct {
	Type     string             `json:"type"`     // always "function"
	Function FunctionDefinition `json:"function"` // the function's name + schema
}

// FunctionDefinition describes a tool's interface to the LLM.
// Parameters is a JSON Schema object describing the input format.
type FunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

// ChatRequest is the request body POSTed to /chat/completions.
// When Stream=true, the response is SSE; otherwise it's a single JSON object.
//
// TODO: Phase 5-6 — 扩展为完整 OpenAI 参数集
//   当前仅包含核心参数，后续 Provider 抽象时需支持：
//   - max_completion_tokens (新版替代 max_tokens)
//   - top_p, frequency_penalty, presence_penalty, seed
//   - response_format (JSON Schema 结构化输出)
//   - parallel_tool_calls (并行工具调用开关)
//   - stream_options (include_usage)
//   参见 doc/chapters/09-llm-api-comparison.html §2.1 完整参数列表。
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []ToolDef `json:"tools,omitempty"`
	ToolChoice  string    `json:"tool_choice,omitempty"` // "auto", "none", or specific tool
	Temperature float32   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream"`
}

// ChatResponse is the non-streaming response body from /chat/completions.
type ChatResponse struct {
	ID      string   `json:"id"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice represents a single completion choice. For streaming, Delta is populated;
// for non-streaming, Message is populated.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"` // "stop", "tool_calls", "length"
	Delta        Delta   `json:"delta"`
}

// Delta is a single SSE delta chunk. Content accumulates text tokens;
// ToolCalls accumulate function call fragments.
//
// TODO: Phase 3-4 — 增加 ReasoningContent 字段
//   DeepSeek R1 / Qwen3 等推理模型在 delta 中返回 reasoning_content（思维链内容），
//   与 content 并列。当前 Delta 未包含此字段，后续扩展时需向后兼容。
//   参见 doc/chapters/09-llm-api-comparison.html §4.2。
type Delta struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls"`
}

// Usage tracks token consumption as reported by the API.
// Token statistics are strictly read from the API response — never estimated locally.
//
// Phase 4+ — added cache token breakdown. OpenAI-compatible APIs return
// prompt_tokens = prompt_cache_hit_tokens + prompt_cache_miss_tokens.
// Anthropic and DeepSeek also provide these fields. We store them for display
// and cost tracking.
type Usage struct {
	PromptTokens           int `json:"prompt_tokens"`
	CompletionTokens       int `json:"completion_tokens"`
	TotalTokens            int `json:"total_tokens"`
	PromptCacheHitTokens   int `json:"prompt_cache_hit_tokens"`   // tokens read from cache
	PromptCacheMissTokens  int `json:"prompt_cache_miss_tokens"`  // tokens not in cache
}

// StreamChunk is a parsed SSE chunk passed to the onChunk callback.
// It contains the delta content, finish reason, and optional usage (from the final chunk).
type StreamChunk struct {
	Delta        Delta  `json:"delta"`
	FinishReason string `json:"finish_reason"`
	Usage        Usage  `json:"usage"`
}

// Client is an OpenAI-compatible HTTP client for LLM chat completions.
// It wraps an http.Client with the endpoint, API key, and model pre-configured.
//
// Each Agent gets its own Client instance (or shares one with the same config),
// allowing multi-Agent setups with different endpoints/models.
type Client struct {
	Endpoint   string       // base URL (e.g., "https://api.openai.com/v1")
	APIKey     string       // Bearer token
	Model      string       // model name (e.g., "deepseek-v4-flash")
	HTTPClient *http.Client // configured with 120s timeout
}

// NewClient creates a new LLM client with the given endpoint, API key, and model.
// The endpoint is trimmed of trailing slashes for consistent URL construction.
func NewClient(endpoint, apiKey, model string) *Client {
	return &Client{
		Endpoint:   strings.TrimRight(endpoint, "/"),
		APIKey:     apiKey,
		Model:      model,
		HTTPClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// Chat sends a non-streaming chat request and returns the full response.
// Used for simple synchronous calls where streaming is not needed.
func (c *Client) Chat(req ChatRequest) (*ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// TODO: Phase 5-6 — Provider 抽象后 URL 由 Provider.Endpoint() 决定
	httpReq, err := http.NewRequest("POST", c.Endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(httpReq)
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
// SSE parsing strategy:
//   1. Read lines with bufio.Scanner (supports up to 1MB lines).
//   2. Skip empty lines, comments (:), and non-data lines.
//   3. Parse each "data: {...}" line as a JSON chunk.
//   4. Accumulate content in a strings.Builder for full text.
//   5. Accumulate ToolCall deltas in a map[int]*ToolCall (because deltas arrive
//      out of order — the index/ID arrives first, then name, then arguments).
//   6. Extract Usage from the final chunk (which contains the full usage object).
//
// Returns the accumulated content, usage, tool calls, and any error.
func (c *Client) ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error) {
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("marshal request: %w", err)
	}

	// TODO: Phase 5-6 — Provider 抽象后 URL 由 Provider.Endpoint() 决定，
	//   Anthropic 使用 /v1/messages 而非 /v1/chat/completions。
	httpReq, err := http.NewRequest("POST", c.Endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", Usage{}, nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var (
		contentBuilder strings.Builder      // accumulates all text content
		toolCalls      []ToolCall           // final assembled tool calls
		usage          Usage                // from the final chunk
		toolCallMap    = make(map[int]*ToolCall) // index → partially accumulated tool call
	)

	scanner := bufio.NewScanner(resp.Body)
	// Increase buffer for large lines (tool call arguments can be large JSON)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// SSE protocol: empty lines are heartbeat, ":" lines are comments
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break // SSE stream termination signal
		}

		// Parse the SSE data as a JSON chunk
		var chunk struct {
			Choices []struct {
				Delta        Delta `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // skip malformed chunks gracefully
		}

		// Extract usage from the final chunk (the only chunk that carries it)
		if chunk.Usage != nil {
			usage = *chunk.Usage
			// Some providers only return prompt_tokens/completion_tokens, while others
			// provide cache hit/miss breakdown. If TotalTokens is zero, compute it.
			if usage.TotalTokens == 0 {
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}
			// If only prompt_cache_hit_tokens is provided, derive miss tokens.
			if usage.PromptCacheMissTokens == 0 && usage.PromptTokens > 0 {
				usage.PromptCacheMissTokens = usage.PromptTokens - usage.PromptCacheHitTokens
			}
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		// Accumulate text content — each delta may contain 1+ tokens
		if choice.Delta.Content != "" {
			contentBuilder.WriteString(choice.Delta.Content)
		}

		// Accumulate tool call deltas — they arrive incrementally:
		//   chunk 1: {index: 0, id: "call_xxx", type: "function"}
		//   chunk 2-N: {index: 0, function: {name: "run_shell", arguments: "{\"cmd"}}
		//   chunk N+1: {index: 0, function: {arguments: "\":\"ls\"}"}}
		for _, tc := range choice.Delta.ToolCalls {
			idx := tc.Idx
			if existing, ok := toolCallMap[idx]; ok {
				// Merge into existing tool call
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Function.Name != "" {
					existing.Function.Name = tc.Function.Name
				}
				existing.Function.Arguments += tc.Function.Arguments
			} else {
				// New tool call
				toolCallMap[idx] = &ToolCall{
					ID:       tc.ID,
					Type:     tc.Type,
					Function: FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}

		// Notify the streaming callback — this is how the Engine streams tokens to the frontend
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

	// Assemble tool calls from the map in index order
	for i := 0; i < len(toolCallMap); i++ {
		if tc, ok := toolCallMap[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}

	return contentBuilder.String(), usage, toolCalls, nil
}

// Index returns the tool call index from the struct field.
// TODO: Phase 4+ — 多 Agent 并发时 ToolCall Index 用于追踪 tool_call 执行顺序
// 和分布式 tracing，届时需要增强为确定性 ID 生成。
func (tc ToolCall) Index() int {
	return tc.Idx
}