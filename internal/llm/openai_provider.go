// Package llm —— OpenAIProvider：Provider 接口的基线实现。
//
// OpenAIProvider 包装现有 Client 并实现 Provider 接口。
// 它支持所有 OpenAI-compatible API，包括 DeepSeek、Groq、Together 等。
//
// 这是基线实现 —— 其他 provider（Anthropic、带 reasoning_content 的 DeepSeek）
// 将在 Phase 6 中加入。
//
// # 协议说明
//
// OpenAI-compatible API 共享同一协议：
//   - 流式与非流式都通过 POST /chat/completions
//   - Authorization: Bearer <api_key> header
//   - SSE streaming 使用 "data: {...}" 格式，以 "data: [DONE]" 终止
//   - Tool call 作为 function_call/tool_calls 嵌入 assistant 消息
//   - Usage 在最后一个 SSE chunk 中上报（非流式则在响应体中）
//
// DeepSeek 的 API 完全 OpenAI-compatible，因此可直接用本 provider。
// 唯一扩展是 delta 中的 reasoning_content（R1/V4 推理用），
// Phase 6 将由扩展本实现的 DeepSeekProvider 处理。
package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider 为 OpenAI-compatible API 实现 Provider 接口。
//
// 它包装现有 Client 的 HTTP 逻辑，但通过 Provider 接口暴露。
// Engine 现在使用 Provider 而非直接使用 *Client，为后续
// 多 provider 支持铺路。
//
// # 线程安全
//
// OpenAIProvider 可安全并发使用 —— 每次调用 ChatStream 都会创建
// 自己的 HTTP request 与 response。底层 http.Client 也是
// goroutine 安全的。
type OpenAIProvider struct {
	name     string       // provider 名（例如 "openai"、"deepseek"）
	endpoint string       // 基础 URL（例如 "https://api.openai.com/v1"）
	apiKey   string       // Bearer token
	model    string       // model 名（例如 "gpt-4o"、"deepseek-v4-flash"）
	http     *http.Client // 已配置 120s timeout
}

// NewOpenAIProvider 创建新的 OpenAI-compatible provider。
//
// endpoint 会去除尾部斜杠以保证 URL 拼接一致。
// model 是该 provider 的默认 model —— 可通过 ChatRequest.Model 按请求覆盖。
func NewOpenAIProvider(name, endpoint, apiKey, model string) *OpenAIProvider {
	return &OpenAIProvider{
		name:     name,
		endpoint: strings.TrimRight(endpoint, "/"),
		apiKey:   apiKey,
		model:    model,
		http:     &http.Client{Timeout: 120 * time.Second},
	}
}

// Name 返回 provider 标识。
func (p *OpenAIProvider) Name() string {
	return p.name
}

// Chat 发送非流式 chat 请求并返回完整响应。
func (p *OpenAIProvider) Chat(req ChatRequest) (*ChatResponse, error) {
	// 当调用方未填 Model 时回退到 provider 的默认 model。
	// Router 的 intent classifier 特意把 Model 置空，以便使用分类器 provider
	// 配置的默认 model；不回退则 API 会收到 "model":"" 而拒绝请求。
	if req.Model == "" {
		req.Model = p.model
	}
	req.Stream = false
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(req.Context, "POST", p.endpoint+"/chat/completions", bytes.NewReader(body))
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

// ChatStream 发送 streaming chat 请求，并对每个 SSE 事件调用 onChunk。
//
// # SSE 解析策略
//
//  1. 用 bufio.Scanner 逐行读取（支持最大 1MB 的行）。
//  2. 跳过空行、注释（:）以及非 data 行。
//  3. 将每行 "data: {...}" 解析为 JSON chunk。
//  4. 用 strings.Builder 累积 content 形成完整文本。
//  5. 用 map[int]*ToolCall 累积 ToolCall delta（delta 可能乱序到达 ——
//     index/ID 先到，然后是 name，最后是 arguments）。
//  6. 从最后一个 chunk 提取 Usage（它携带完整 usage 对象）。
//
// 每解析出一个 chunk 都会调用 onChunk 回调，让 Engine
// 实时把 text delta 转发给前端。
func (p *OpenAIProvider) ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error) {
	// 当调用方未填 Model 时回退到 provider 的默认 model（与 Chat 对齐）。
	// Engine 通常会把 ChatRequest.Model 设为路由后的 model，但依赖
	// provider 默认值的旧调用方通过此回退仍能正常工作。
	if req.Model == "" {
		req.Model = p.model
	}
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(req.Context, "POST", p.endpoint+"/chat/completions", bytes.NewReader(body))
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
			log.Printf("[OpenAIProvider] ChatStream finished due to length limit (model=%s)", req.Model)
		case "content_filter":
			log.Printf("[OpenAIProvider] ChatStream finished due to content filter (model=%s)", req.Model)
		default:
			// 未知 finish_reason —— 高亮记录，便于发现 provider 新引入的
			// 枚举值（例如旧版的 "function_call"）。
			log.Printf("[OpenAIProvider] ChatStream finished with unexpected reason %q (model=%s)",
				choice.FinishReason, req.Model)
		}

		if choice.Delta.Content != "" {
			contentBuilder.WriteString(choice.Delta.Content)
		}

		// 累积来自 DeepSeek R1/V4 model 与 Step-3.x / vLLM 部署的
		// reasoning content（思维链）。DeepSeek 协议用 "reasoning_content"；
		// Step-3.x 把同一条流以 "reasoning" 暴露。
		// 两者都并入完整文本累积，让 Engine 的 think 阶段能看到 model 的
		// 实际输出，而非空 content（否则会提前以"final answer"结束 ReAct loop）。
		if choice.Delta.ReasoningContent != "" {
			contentBuilder.WriteString(choice.Delta.ReasoningContent)
		}
		if choice.Delta.Reasoning != "" {
			contentBuilder.WriteString(choice.Delta.Reasoning)
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