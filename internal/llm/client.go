// Package llm 提供 OpenAI-compatible HTTP client 用于 chat completions。
//
// 它同时支持非流式（Chat）与 SSE streaming（ChatStream）两种模式，
// 并为 ReAct Agent loop 处理 tool_call delta 的累积。
//
// 设计说明：
//   - Client 刻意做成一个轻量 HTTP 包装层 —— 所有 ReAct 逻辑都在 runtime/engine.go。
//   - SSE streaming 用 bufio.Scanner 逐行解析，避免缓冲整段响应。
//   - ToolCall 的 index 追踪使用 map[int]*ToolCall，因为 SSE delta 会乱序到达。
//   - Usage 始终从最后一个 SSE chunk 读取，遵循 OpenAI-compatible API 约定。
//
// Provider 抽象已完成 (Phase 5-6)，参见 internal/llm/provider.go Provider 接口
// 和 internal/llm/openai_provider.go / anthropic_provider.go / mock_provider.go。
// 当前 Client 仍作为 OpenAIProvider 的底层传输层保留使用。
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Message 表示 OpenAI-compatible 格式的 chat 消息。
// 它支持 text content、tool call（assistant role）和 tool 结果（tool role）。
type Message struct {
	Role       string     `json:"role"`                   // "system"、"user"、"assistant"、"tool"
	Content    string     `json:"content,omitempty"`      // 文本内容（tool call 时为空）
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // LLM 请求的 function call
	ToolCallID string     `json:"tool_call_id,omitempty"` // 将 tool 结果关联回对应 call
	Name       string     `json:"name,omitempty"`         // 可选 agent 名

	// Reasoning 是部分 provider（DeepSeek R1/V4、Step-3.x）在单独的 "reasoning" 字段
	// 而非 "content" 字段返回的思维链文本。
	// Router 的 intent classifier 先读 Content，Content 为空时回退到
	// Reasoning —— 这让 classifyIntent 在只于低 max_tokens 下填充 reasoning
	// 的推理模型上也能工作。
	// 不向外发送（我们从不把 reasoning 发给 LLM）；仅用于入站解析。
	Reasoning string `json:"reasoning,omitempty"`
}

// ToolCall 表示来自 LLM 的 function call 请求。
// SSE streaming 期间，ToolCall delta 增量到达 —— ID 先到，
// function name 次之，arguments 最后（常常跨多个 chunk）。
type ToolCall struct {
	Idx      int          `json:"index"`    // 多个 tool call 时基于 0 的 index，用于排序
	ID       string       `json:"id"`       // 唯一 call ID，用于 tool 结果关联
	Type     string       `json:"type"`     // 始终为 "function"
	Function FunctionCall `json:"function"` // function 名称 + 参数
}

// FunctionCall 持有 tool call 的名称与 JSON 编码的参数。
// Arguments 是 JSON 字符串（而非对象），因为 LLM 是增量 streaming 输出的。
type FunctionCall struct {
	Name      string `json:"name"`      // tool 名（例如 "run_shell"）
	Arguments string `json:"arguments"` // JSON 编码的参数字符串
}

// ToolDef 是发送给 LLM 的 tool 定义，作为 chat 请求的一部分。
// 它告诉 LLM 有哪些 tool 可用以及如何调用。
type ToolDef struct {
	Type     string             `json:"type"`     // 始终为 "function"
	Function FunctionDefinition `json:"function"` // function 名称 + schema
}

// FunctionDefinition 向 LLM 描述 tool 的接口。
// Parameters 是描述输入格式的 JSON Schema 对象。
type FunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

// ChatRequest 是 POST 到 /chat/completions 的请求体。
// 当 Stream=true 时响应为 SSE；否则为单个 JSON 对象。
//
// ChatRequest 当前仅包含核心参数。Provider 抽象已完成（Phase 5-6，参见
// internal/llm/provider.go），扩展参数（top_p、frequency_penalty 等）
// 可在后续迭代中按需追加到该结构体。
//   参见 doc/chapters/09-llm-api-comparison.html §2.1 完整参数列表。
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []ToolDef `json:"tools,omitempty"`
	ToolChoice  string    `json:"tool_choice,omitempty"` // "auto"、"none" 或具体 tool
	Temperature float32   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream"`
	// CaseID 是可选 hint，供 MockProvider 选择 mock 脚本。
	// 真实 LLM provider 会忽略它。
	CaseID string `json:"-"`
	// Context 携带调用方的 cancellation 与 timeout 信号。
	// Provider 用它来取消进行中的 HTTP 请求。若为 nil，
	// provider 会回退到默认 timeout。
	Context context.Context
}

// ChatResponse 是 /chat/completions 的非流式响应体。
type ChatResponse struct {
	ID      string   `json:"id"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice 表示单个 completion choice。流式模式下填充 Delta；
// 非流式模式下填充 Message。
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"` // "stop"、"tool_calls"、"length"
	Delta        Delta   `json:"delta"`
}

// Delta 是单个 SSE delta chunk。Content 累积 text token；
// ReasoningContent 累积思维链 token（DeepSeek R1/V4 用）。
// ToolCalls 累积 function call 片段。
//
// ReasoningContent 字段已在 Phase 5-6 实现，DeepSeek R1/V4 推理模型
// 的思维链内容与 content 并列返回。空字符串时不影响旧逻辑。
// 参见 doc/chapters/09-llm-api-comparison.html §4.2。
//
// Reasoning（无 _content 后缀）是 Step-3.x / vLLM 风格的等价字段 —— 同一个
// 思维链在某些部署里叫 "reasoning"，在 DeepSeek 官方协议里叫 "reasoning_content"。
// 两个字段都解析，ReasoningContent 优先；任一非空都并入 content 累积，保证
// 推理型模型在 ChatStream 里也能拿到完整文本（否则 content 一直为空，think
// 阶段会把空 content 当作 final answer 提前结束 ReAct loop）。
type Delta struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ToolCalls        []ToolCall `json:"tool_calls"`
	ReasoningContent string     `json:"reasoning_content"` // DeepSeek R1/V4 思维链字段
	Reasoning        string     `json:"reasoning"`         // Step-3.x / vLLM 思维链字段（等价）
}

// Usage 跟踪由 API 返回的 token 消耗。
// Token 统计严格从 API 响应读取 —— 绝不本地估算。
//
// Phase 4+ —— 新增 cache token 拆分。OpenAI-compatible API 返回
// prompt_tokens = prompt_cache_hit_tokens + prompt_cache_miss_tokens。
// Anthropic 与 DeepSeek 也提供这些字段。我们存储它们用于展示
// 与成本追踪。
type Usage struct {
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`  // 命中缓存的 token
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"` // 未命中缓存的 token
}

// StreamChunk 是解析后传给 onChunk 回调的 SSE chunk。
// 它包含 delta 内容、finish reason 以及可选 usage（来自最后一个 chunk）。
type StreamChunk struct {
	Delta        Delta  `json:"delta"`
	FinishReason string `json:"finish_reason"`
	Usage        Usage  `json:"usage"`
}

// Client 是用于 LLM chat completions 的 OpenAI-compatible HTTP client。
// 它包装了一个 http.Client，并预配置 endpoint、API key 与 model。
//
// 每个 Agent 拥有自己的 Client 实例（或与相同配置的 Agent 共享一个），
// 允许多 Agent 配置使用不同 endpoint/model。
type Client struct {
	Endpoint   string       // 基础 URL（例如 "https://api.openai.com/v1"）
	APIKey     string       // Bearer token
	Model      string       // model 名（例如 "deepseek-v4-flash"）
	HTTPClient *http.Client // 已配置 120s timeout
}

// NewClient 以给定的 endpoint、API key 与 model 创建新的 LLM client。
// endpoint 会被去除尾部斜杠，以保证 URL 拼接一致。
func NewClient(endpoint, apiKey, model string) *Client {
	return &Client{
		Endpoint:   strings.TrimRight(endpoint, "/"),
		APIKey:     apiKey,
		Model:      model,
		HTTPClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// Chat 发送非流式 chat 请求并返回完整响应。
// 用于不需要 streaming 的简单同步调用。
func (c *Client) Chat(req ChatRequest) (*ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Note: URL 路径硬编码为 /chat/completions（OpenAI 兼容协议）。
	// Provider 抽象已在 Phase 5-6 完成：OpenAIProvider 包装本 Client 直接调用，
	// AnthropicProvider 自行构建 /v1/messages 端点，无需修改 Client 的 URL 逻辑。
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

// ChatStream 发送 streaming chat 请求，并对每个 SSE 事件调用 onChunk。
//
// SSE 解析策略：
//  1. 用 bufio.Scanner 逐行读取（支持最大 1MB 的行）。
//  2. 跳过空行、注释（:）以及非 data 行。
//  3. 将每行 "data: {...}" 解析为 JSON chunk。
//  4. 用 strings.Builder 累积 content 形成完整文本。
//  5. 用 map[int]*ToolCall 累积 ToolCall delta（因为 delta 乱序到达 ——
//     index/ID 先到，然后是 name，最后是 arguments）。
//  6. 从最后一个 chunk 提取 Usage（它包含完整 usage 对象）。
//
// 返回累积的 content、usage、tool calls 以及任意 error。
func (c *Client) ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error) {
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("marshal request: %w", err)
	}

	// Note: URL 路径硬编码为 /chat/completions（OpenAI 兼容协议）。
	// Provider 抽象已在 Phase 5-6 完成：OpenAIProvider 包装本 Client 直接调用，
	// AnthropicProvider 自行构建 /v1/messages 端点，无需修改 Client 的 URL 逻辑。，
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

	// 从 request 派生 context，以便 SSE streaming 期间用于 cancellation 检查。
	// 回退到一个不可取消的 background context。
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", Usage{}, nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var (
		contentBuilder strings.Builder           // 累积所有 text content
		toolCalls      []ToolCall                // 最终组装好的 tool call
		usage          Usage                     // 来自最后一个 chunk
		toolCallMap    = make(map[int]*ToolCall) // index → 部分累积的 tool call
	)

	scanner := bufio.NewScanner(resp.Body)
	// 为长行增大缓冲（tool call arguments 可能是很长的 JSON）
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		// 在 SSE chunk 之间检查 context cancellation，让被取消的
		// task 能快速停下，而不是继续读完整个 stream。
		select {
		case <-ctx.Done():
			return "", usage, nil, ctx.Err()
		default:
		}

		line := scanner.Text()

		// SSE 协议：空行是心跳，":" 行是注释
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break // SSE stream 结束信号
		}

		// 将 SSE data 解析为 JSON chunk
		var chunk struct {
			Choices []struct {
				Delta        Delta  `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // 容错跳过格式错误的 chunk
		}

		// 从最后一个 chunk 提取 usage（唯一携带 usage 的 chunk）
		if chunk.Usage != nil {
			usage = *chunk.Usage
			// 部分 provider 只返回 prompt_tokens/completion_tokens，另一些
			// 提供 cache hit/miss 拆分。若 TotalTokens 为 0 则计算它。
			if usage.TotalTokens == 0 {
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}
			// 若只提供 prompt_cache_hit_tokens 则推导 miss token。
			if usage.PromptCacheMissTokens == 0 && usage.PromptTokens > 0 {
				usage.PromptCacheMissTokens = usage.PromptTokens - usage.PromptCacheHitTokens
			}
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		// 处理 finish_reason 语义。SSE 传输层用 "data: [DONE]" 标记
		// stream 结束，但语义层用 choices[0].finish_reason 告诉我们
		// 为什么停止生成。我们记录非预期 reason，便于运维发现协议漂移或
		// model 行为变化。
		switch choice.FinishReason {
		case "":
			// 仍在生成 —— 无需处理。
		case "stop":
			// 自然文本结束。
		case "tool_calls":
			// Tool call 生成结束；累积的 toolCalls 将被返回。
		case "length":
			// 触及 max_tokens 上限。响应被截断；记录日志，因为这可能产生
			// 不完整的 tool 参数，导致后续 json.Unmarshal 失败。
			log.Printf("[Client] ChatStream finished due to length limit (model=%s)", req.Model)
		case "content_filter":
			log.Printf("[Client] ChatStream finished due to content filter (model=%s)", req.Model)
		default:
			// 未知 finish_reason —— 高亮记录，便于发现 provider 新引入的
			// 枚举值（例如旧版的 "function_call"）。
			log.Printf("[Client] ChatStream finished with unexpected reason %q (model=%s)",
				choice.FinishReason, req.Model)
		}

		// 累积 text content —— 每个 delta 可能含 1 个或多个 token
		if choice.Delta.Content != "" {
			contentBuilder.WriteString(choice.Delta.Content)
		}

		// 累积来自 DeepSeek R1/V4 model 的 reasoning content（思维链）
		if choice.Delta.ReasoningContent != "" {
			contentBuilder.WriteString(choice.Delta.ReasoningContent)
		}

		// 累积 tool call delta —— 它们是增量到达的：
		//   chunk 1: {index: 0, id: "call_xxx", type: "function"}
		//   chunk 2-N: {index: 0, function: {name: "run_shell", arguments: "{\"cmd"}}
		//   chunk N+1: {index: 0, function: {arguments: "\":\"ls\"}"}}
		for _, tc := range choice.Delta.ToolCalls {
			idx := tc.Idx
			if existing, ok := toolCallMap[idx]; ok {
				// 合并进已有 tool call
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Function.Name != "" {
					existing.Function.Name = tc.Function.Name
				}
				existing.Function.Arguments += tc.Function.Arguments
			} else {
				// 新 tool call
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

		// 通知 streaming 回调 —— Engine 通过它将 token 流推送给前端
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

	// 按 index 顺序从 map 组装 tool call
	for i := 0; i < len(toolCallMap); i++ {
		if tc, ok := toolCallMap[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}

	return contentBuilder.String(), usage, toolCalls, nil
}

// Index 从结构体字段返回 tool call 的 index。
// Phase 4+ —— 多 Agent 并发时 ToolCall Index 用于追踪 tool_call 执行顺序
// 和分布式 tracing，届时需要增强为确定性 ID 生成。
func (tc ToolCall) Index() int {
	return tc.Idx
}
