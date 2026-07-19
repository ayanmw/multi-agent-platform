// Package llm — AnthropicProvider：Anthropic Claude Messages API 实现。
//
// Anthropic 的协议与 OpenAI-compatible API 差异显著：
//   - Endpoint：POST /v1/messages（不是 /v1/chat/completions）
//   - System prompt：顶层 "system" 字段（不是 "system" role 消息）
//   - Tool schema："input_schema"（不是 "parameters"）
//   - Tool choice：对象 {"type": "..."}（不是字符串 "auto"/"none"）
//   - max_tokens：必填（不是可选）
//   - Auth：x-api-key header（不是 Bearer token）
//   - Streaming：双层 SSE，带 "event:" 类型指示
//   - Tool use streaming：content_block_start / content_block_delta 配合 partial_json
//   - Token 计数：input_tokens 在 message_start，output_tokens 在 message_delta
//
// # 线程安全
//
// AnthropicProvider 可安全并发使用 —— 每次调用都会创建各自的
// HTTP request 与 response。底层 http.Client 是 goroutine 安全的。
package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Anthropic 专用的请求/响应类型（未导出 —— 仅用于内部转换）
// ---------------------------------------------------------------------------

// anthropicMessage 表示 Anthropic Messages API 中的单条消息。
// Role 取值："user"、"assistant"。"system" 被提取到顶层字段。
type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

// anthropicContent 是消息内的多态 content block。
// 可以是纯文本，也可以是 tool use block。
type anthropicContent struct {
	Type string `json:"type"` // "text" 或 "tool_use"

	// text block 字段：
	Text string `json:"text,omitempty"`

	// tool_use block 字段：
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	InputJSON map[string]interface{} `json:"input,omitempty"`

	// tool_use 结果 block 字段（发往 Anthropic 的 tool-result）：
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

// anthropicChatRequest 是 POST /v1/messages 的请求体。
type anthropicChatRequest struct {
	Model       string               `json:"model"`
	MaxTokens   int                  `json:"max_tokens"` // Anthropic 必填
	System      string               `json:"system,omitempty"`
	Messages    []anthropicMessage   `json:"messages"`
	Tools       []anthropicToolDef   `json:"tools,omitempty"`
	ToolChoice  *anthropicToolChoice `json:"tool_choice,omitempty"`
	Temperature float32              `json:"temperature,omitempty"`
	Stream      bool                 `json:"stream"`
}

// anthropicToolDef 以 Anthropic 格式定义一个 tool。
// 关键差异：使用 "input_schema" 而非 "parameters"。
type anthropicToolDef struct {
	Type        string                 `json:"type"` // 始终为 "function"
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"` // JSON Schema（不是 "parameters"）
}

// anthropicToolChoice 以 Anthropic 的对象格式指定 tool 选择行为。
type anthropicToolChoice struct {
	Type string `json:"type"`           // "auto"、"any"、"tool"、"none"
	Name string `json:"name,omitempty"` // 仅在 Type == "tool" 时使用
}

// ---------------------------------------------------------------------------
// Anthropic streaming 响应类型
// ---------------------------------------------------------------------------

// anthropicStreamEvent 是 Anthropic 的顶层 SSE 信封。
// 事件类型位于 "event:" 行，不在 JSON data 中。
type anthropicStreamEvent struct {
	Type string `json:"type"` // message_start、content_block_start、content_block_delta 等
}

// messageStartEvent 携带初始消息元数据，包括 usage。
type messageStartEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Usage struct {
		InputTokens int `json:"input_tokens"`
	} `json:"usage"`
}

// contentBlockStartEvent 标记一个 content block 的开始。
type contentBlockStartEvent struct {
	Type         string                      `json:"type"`
	Index        int                         `json:"index"`
	ContentBlock anthropicStreamContentBlock `json:"content_block"`
}

// anthropicStreamContentBlock 是 streaming 中已初始化的 content block。
type anthropicStreamContentBlock struct {
	Type  string   `json:"type"` // "text" 或 "tool_use"
	Text  string   `json:"text,omitempty"`
	ID    string   `json:"id,omitempty"`
	Name  string   `json:"name,omitempty"`
	Input struct{} `json:"input,omitempty"` // 空对象 —— input 通过 delta 到达
}

// contentBlockDeltaEvent 携带当前 content block 的增量 delta。
type contentBlockDeltaEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type string `json:"type"` // "text_delta" 或 "input_json_delta"
		Text string `json:"text,omitempty"`
		// PartialJSON 是 tool_use 参数 streaming 的增量 JSON 片段。
		PartialJSON string `json:"partial_json,omitempty"`
	} `json:"delta"`
}

// messageDeltaEvent 标记消息结束，携带最终 usage 和 stop reason。
type messageDeltaEvent struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason string `json:"stop_reason"` // "end_turn"、"tool_use"、"max_tokens"
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// messageStopEvent 标记 stream 结束。
type messageStopEvent struct {
	Type string `json:"type"`
}

// ---------------------------------------------------------------------------
// AnthropicProvider —— Provider 接口实现
// ---------------------------------------------------------------------------

// AnthropicProvider 为 Anthropic 的 Claude Messages API 实现 Provider 接口。
//
// 所有 Anthropic 专用的格式转换都隐藏在该 provider 之内 —— Engine
// 只看到统一的 ChatRequest/StreamChunk/Usage/ToolCall 类型。
type AnthropicProvider struct {
	name     string       // provider 标识，例如 "anthropic"
	endpoint string       // 基础 URL，例如 "https://api.anthropic.com"（不含 /v1）
	apiKey   string       // Anthropic API key（x-api-key）
	model    string       // 默认 model，例如 "claude-sonnet-4-20250514"
	http     *http.Client // goroutine 安全
}

// NewAnthropicProvider 创建一个新的 AnthropicProvider。
//
// endpoint 应为基础 URL（例如 "https://api.anthropic.com"）——
// provider 内部会自动追加 "/v1/messages"。
func NewAnthropicProvider(name, endpoint, apiKey, model string) *AnthropicProvider {
	endpoint = strings.TrimRight(endpoint, "/")
	// 若调用方传入了 /v1 则先剥离，以便后续统一追加。
	endpoint = strings.TrimSuffix(endpoint, "/v1")
	return &AnthropicProvider{
		name:     name,
		endpoint: endpoint,
		apiKey:   apiKey,
		model:    model,
		http:     &http.Client{Timeout: 120 * time.Second},
	}
}

// Name 返回 provider 标识。
func (p *AnthropicProvider) Name() string {
	return p.name
}

// ---------------------------------------------------------------------------
// Chat —— 非流式请求与响应
// ---------------------------------------------------------------------------

// Chat 向 Anthropic Messages API 发送非流式 chat 请求，
// 并返回完整解析后的响应。
//
// 格式转换：
//   - 将 "system" role 消息提取到顶层 "system" 字段
//   - 转换 tool schema：function.parameters → input_schema
//   - 转换 tool_choice：字符串 → {"type": "..."} 对象
//   - 若 max_tokens 未设置则置为 4096（Anthropic 必填）
func (p *AnthropicProvider) Chat(req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	anthropicReq, err := p.buildAnthropicReq(req)
	if err != nil {
		return nil, fmt.Errorf("build anthropic request: %w", err)
	}

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(req.Context, "POST", p.endpoint+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, bodyBytes)
	}

	// Parse Anthropic's non-streaming response into unified ChatResponse.
	var anthropicResp struct {
		Content []struct {
			Type  string                 `json:"type"` // "text" 或 "tool_use"
			Text  string                 `json:"text,omitempty"`
			ID    string                 `json:"id,omitempty"`
			Name  string                 `json:"name,omitempty"`
			Input map[string]interface{} `json:"input,omitempty"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// 将 Anthropic content blocks 转换为统一 Response。
	chatResp := &ChatResponse{
		ID: "anthropic-response",
		Choices: []Choice{{
			Index:        0,
			FinishReason: mapAnthropicStopReason(anthropicResp.StopReason),
			Message: Message{
				Role: "assistant",
			},
		}},
		Usage: Usage{
			PromptTokens:     anthropicResp.Usage.InputTokens,
			CompletionTokens: anthropicResp.Usage.OutputTokens,
			TotalTokens:      anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens,
		},
	}

	// 从 content blocks 重建 message 内容。
	var textContent strings.Builder
	var toolCalls []ToolCall
	for _, block := range anthropicResp.Content {
		switch block.Type {
		case "text":
			textContent.WriteString(block.Text)
		case "tool_use":
			// 将 input_map 序列化为 JSON，填入统一 ToolCall.Arguments 字段。
			argsJSON, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, ToolCall{
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	chatResp.Choices[0].Message.Content = textContent.String()
	chatResp.Choices[0].Message.ToolCalls = toolCalls
	return chatResp, nil
}

// ---------------------------------------------------------------------------
// ChatStream —— streaming 请求，解析双层 SSE
// ---------------------------------------------------------------------------

// ChatStream 向 Anthropic Messages API 发送 streaming chat 请求，
// 并对每个 SSE 事件调用 onChunk。
//
// Anthropic 的 SSE 格式使用两行独立的 header：
//
//	event: <event_type>
//	data: {"type": "<event_type>", ...}
//
// 事件流程：
//  1. message_start        —— 提取 input_tokens
//  2. content_block_start  —— 初始化 text 或 tool_use block
//  3. content_block_delta  —— 累积 text_delta 或 input_json_delta
//  4. message_delta        —— 提取 output_tokens 与 stop_reason
//  5. message_stop         —— stream 结束
//
// Tool use 参数以增量 JSON 片段（partial_json）形式到达，
// 而非完整 JSON 对象，因此我们按 content block index 用 strings.Builder
// 累积它们，仅在结束时解析。
func (p *AnthropicProvider) ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error) {
	anthropicReq, err := p.buildAnthropicReq(req)
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("build anthropic request: %w", err)
	}

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(req.Context, "POST", p.endpoint+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", Usage{}, nil, fmt.Errorf("API error %d: %s", resp.StatusCode, bodyBytes)
	}

	var (
		contentBuilder strings.Builder
		usage          Usage
		// 跟踪每个 tool call 所属的 content block index。
		// Anthropic 在 delta 中用 "index" 字段标识对应的 content block。
		toolCallMap = make(map[int]*ToolCall)
		// 为 tool_use block 累积 partial_json，直到 block 完成。
		toolArgBuilder = make(map[int]*strings.Builder)
		// 跟踪哪些 content block 是 tool_use（相对 text）。
		isToolBlock = make(map[int]bool)
		// 跟踪每个 tool_use block 的 tool 名称与 ID。
		toolBlockMeta = make(map[int]struct{ name, id string })
	)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Anthropic 的 SSE 使用两行事件："event: <type>\n data: {...}\n"
	// 需要在两行之间缓存 event type。
	var currentEventType string

	for scanner.Scan() {
		line := scanner.Text()

		// 空行分隔 SSE 事件 —— 重置 event type。
		if line == "" {
			currentEventType = ""
			continue
		}

		// 跳过注释。
		if strings.HasPrefix(line, ":") {
			continue
		}

		// "event: <type>" 行 —— 捕获 event type 以便下一行 data 使用。
		if strings.HasPrefix(line, "event: ") {
			currentEventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		// "data: {...}" 行 —— 根据 currentEventType 进行处理。
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		// 将 data 当作通用信封解析以获取 type 字段。
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			continue // 跳过格式错误 chunk
		}

		switch currentEventType {
		case "message_start":
			// 从 usage 提取 input_tokens。
			var ev messageStartEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				usage.PromptTokens = ev.Usage.InputTokens
			}

		case "content_block_start":
			// 一个新的 content block 开始 —— 记录其类型与元数据。
			var ev contentBlockStartEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				switch ev.ContentBlock.Type {
				case "text":
					// Text block —— 无需特殊初始化。
				case "tool_use":
					isToolBlock[ev.Index] = true
					toolBlockMeta[ev.Index] = struct{ name, id string }{
						name: ev.ContentBlock.Name,
						id:   ev.ContentBlock.ID,
					}
					toolArgBuilder[ev.Index] = &strings.Builder{}
					// 在 block 首次开始时初始化 tool call。
					if _, exists := toolCallMap[ev.Index]; !exists {
						toolCallMap[ev.Index] = &ToolCall{
							Type: "function",
							Function: FunctionCall{
								Name:      ev.ContentBlock.Name,
								Arguments: "",
							},
						}
					}
				}
			}

		case "content_block_delta":
			// 当前 content block 的增量 delta。
			var ev contentBlockDeltaEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				switch ev.Delta.Type {
				case "text_delta":
					contentBuilder.WriteString(ev.Delta.Text)
				case "input_json_delta":
					// 为 tool_use 参数累积 partial JSON。
					if idx, ok := toolArgBuilder[ev.Index]; ok {
						idx.WriteString(ev.Delta.PartialJSON)
					}
				}
			}

		case "message_delta":
			// 提取 output_tokens 与 stop reason。
			var ev messageDeltaEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				usage.CompletionTokens = ev.Usage.OutputTokens
				// 从累积的 partial_json 完成 tool call 参数组装。
				p.finalizeToolCalls(toolCallMap, toolArgBuilder, isToolBlock)
				// 发出最终 StreamChunk，让 Engine 看到 finish reason。
				if onChunk != nil {
					sc := StreamChunk{
						FinishReason: mapAnthropicStopReason(ev.Delta.StopReason),
					}
					if err := onChunk(sc); err != nil {
						return contentBuilder.String(), usage, nil, err
					}
				}
			}

		case "message_stop":
			// Stream 结束 —— 计算总 token 数。
			if usage.TotalTokens == 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}
			// 最后再做一次 tool call 收尾（安全兜底）。
			p.finalizeToolCalls(toolCallMap, toolArgBuilder, isToolBlock)

		case "error":
			// 处理 error 事件。
			var errEv struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(data), &errEv); err == nil {
				return contentBuilder.String(), usage, nil,
					fmt.Errorf("anthropic error (%s): %s", errEv.Error.Type, errEv.Error.Message)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return contentBuilder.String(), usage, nil, fmt.Errorf("scan stream: %w", err)
	}

	// 按 index 顺序从 map 组装 tool calls。
	//
	// 重要：Anthropic 的 content block index 在 text 与 tool_use block 之间共享。
	// 若 model 在 index 0 输出 text、在 index 1 输出 tool_use，
	// toolCallMap 只包含 key 1。若仅遍历 [0, len(toolCallMap)) 会漏掉 index 1。
	// 因此我们收集所有 key 排序后追加对应的 tool call。
	var toolCalls []ToolCall
	if len(toolCallMap) > 0 {
		indices := make([]int, 0, len(toolCallMap))
		for idx := range toolCallMap {
			indices = append(indices, idx)
		}
		// sort.Ints 对少量 tool call 而言是确定性且开销很小的。
		sort.Ints(indices)
		for _, idx := range indices {
			// 防御性：只输出实际为 tool_use 的 content block。
			if tc, ok := toolCallMap[idx]; ok && isToolBlock[idx] {
				toolCalls = append(toolCalls, *tc)
			}
		}
	}

	return contentBuilder.String(), usage, toolCalls, nil
}

// ---------------------------------------------------------------------------
// Helper：请求构建
// ---------------------------------------------------------------------------

// buildAnthropicReq 将统一 ChatRequest 转换为 Anthropic 的请求格式。
// 它负责：
//   - 将 system 消息提取到顶层 system 字段
//   - 转换 tool 定义（input_schema）
//   - 转换 tool_choice 为对象格式
//   - 设置默认 max_tokens
func (p *AnthropicProvider) buildAnthropicReq(req ChatRequest) (anthropicChatRequest, error) {
	// 若未提供 max_tokens 则设置默认值（Anthropic 必填）。
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	// 从 Role == "system" 的消息构建 system 字段。
	var systemPrompt strings.Builder
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			if systemPrompt.Len() > 0 {
				systemPrompt.WriteString("\n\n")
			}
			systemPrompt.WriteString(msg.Content)
		}
	}

	// 转换消息，过滤掉 system 消息（已提取）。
	messages, err := convertMessages(req.Messages)
	if err != nil {
		return anthropicChatRequest{}, fmt.Errorf("convert messages: %w", err)
	}

	// 转换 tool 定义。
	tools := convertTools(req.Tools)

	// 转换 tool_choice。
	var toolChoice *anthropicToolChoice
	if req.ToolChoice != "" {
		toolChoice = convertToolChoice(req.ToolChoice, req.Tools)
	}

	return anthropicChatRequest{
		Model:       firstNonEmpty(req.Model, p.model),
		MaxTokens:   maxTokens,
		System:      systemPrompt.String(),
		Messages:    messages,
		Tools:       tools,
		ToolChoice:  toolChoice,
		Temperature: req.Temperature,
		Stream:      req.Stream,
	}, nil
}

// ---------------------------------------------------------------------------
// Helper：tool 定义转换
// ---------------------------------------------------------------------------

// convertTools 将统一 ToolDef slice 转换为 Anthropic 的 tool 格式。
// 关键映射：function.parameters → input_schema
func convertTools(tools []ToolDef) []anthropicToolDef {
	result := make([]anthropicToolDef, len(tools))
	for i, tool := range tools {
		result[i] = anthropicToolDef{
			Type:        "function",
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Helper：消息转换
// ---------------------------------------------------------------------------

// convertMessages 将统一 Message slice 转换为 Anthropic 的消息格式。
//
// 关键转换：
//   - "system" role 消息：排除（由调用方提取到顶层 system 字段）
//   - "user" role 消息：content 变成单个 "text" block
//   - 带 tool_calls 的 "assistant" role 消息：转换为 type 为 "tool_use" 的 content_blocks
//   - "tool" role 消息：合并到前一条 "user" 消息中，作为
//     type 为 "tool_result" 的 content_blocks，并带 tool_use_id。
//
// Anthropic 的 Messages API 要求：一次 assistant tool_use turn 的所有
// tool_result block 必须放在同一条 "user" 消息中（多个 content block）。
// 因此连续的 "tool" role 消息会被合并到前一条 user 消息里
//（必要时创建一条），而不是各自独立成消息。
func convertMessages(messages []Message) ([]anthropicMessage, error) {
	result := make([]anthropicMessage, 0, len(messages))

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			// 跳过 —— 由顶层 system 字段处理。
			continue

		case "user":
			// 普通 user 消息 —— 单个 text content block。
			am := anthropicMessage{
				Role: "user",
				Content: []anthropicContent{
					{Type: "text", Text: msg.Content},
				},
			}
			// 若 Content 含结构化数据则处理多段 user 内容（文件上传、图像）。
			// 目前我们一律按 text 处理。
			result = append(result, am)

		case "assistant":
			am := anthropicMessage{Role: "assistant"}
			// 若消息含 tool_calls，则输出 tool_use block。
			if len(msg.ToolCalls) > 0 {
				am.Content = make([]anthropicContent, len(msg.ToolCalls))
				for i, tc := range msg.ToolCalls {
					// 将 JSON arguments 解析为 map，用于 Anthropic 的 input 字段。
					var argsMap map[string]interface{}
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &argsMap)
					am.Content[i] = anthropicContent{
						Type:      "tool_use",
						ID:        tc.ID,
						Name:      tc.Function.Name,
						InputJSON: argsMap,
					}
				}
			} else if msg.Content != "" {
				// 普通 text assistant 消息。
				am.Content = []anthropicContent{
					{Type: "text", Text: msg.Content},
				}
			}
			result = append(result, am)

		case "tool":
			// Tool 结果必须为 role "user" 且含 tool_result content block。
			// Anthropic 要求一次 assistant tool_use turn 的所有结果都在
			// 同一条 user 消息中（多个 content block）。若上一条结果
			// 已合并到最近一条 user 消息，则追加；否则新建一条 user 消息并折叠进去。
			block := anthropicContent{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msg.Content,
			}
			if len(result) > 0 && result[len(result)-1].Role == "user" {
				last := &result[len(result)-1]
				// 若上一条 user 消息是普通 text prompt，则通过追加 tool_result block
				// 将其转换为复合消息。在同一条 user turn 里混合 text 与 tool_result
				// 并不常见，但 Anthropic 允许这样做。
				last.Content = append(last.Content, block)
			} else {
				result = append(result, anthropicMessage{
					Role:    "user",
					Content: []anthropicContent{block},
				})
			}
		}
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Helper：tool_choice 转换
// ---------------------------------------------------------------------------

// convertToolChoice 将统一字符串 tool_choice 转换为 Anthropic 的
// 对象格式。OpenAI 使用字符串如 "auto"、"none" 或 tool 名。
// Anthropic 使用：{"type": "auto"} | {"type": "any"} | {"type": "tool", "name": "..."}
func convertToolChoice(choice string, availableTools []ToolDef) *anthropicToolChoice {
	switch choice {
	case "auto":
		return &anthropicToolChoice{Type: "auto"}
	case "none":
		return &anthropicToolChoice{Type: "none"}
	default:
		// 若与某个 tool 名匹配，则使用 {"type": "tool", "name": "..."}。
		for _, tool := range availableTools {
			if tool.Function.Name == choice {
				return &anthropicToolChoice{Type: "tool", Name: choice}
			}
		}
		// 回退到 auto。
		return &anthropicToolChoice{Type: "auto"}
	}
}

// ---------------------------------------------------------------------------
// Helper：stream 解析
// ---------------------------------------------------------------------------

// finalizeToolCalls 从累积的 partial_json delta 完成 tool call 参数组装。
// 在 message_delta 与 message_stop 时调用，确保所有 tool 参数在
// 返回 Engine 之前已完整解析。
func (p *AnthropicProvider) finalizeToolCalls(
	toolCallMap map[int]*ToolCall,
	toolArgBuilder map[int]*strings.Builder,
	isToolBlock map[int]bool,
) {
	for idx, builder := range toolArgBuilder {
		if builder == nil {
			continue
		}
		if tc, ok := toolCallMap[idx]; ok && isToolBlock[idx] {
			// 累积的 partial_json 应为合法 JSON。
			argsJSON := builder.String()
			// 尝试解析以校验 —— 失败则原样保存为字符串。
			var test map[string]interface{}
			if err := json.Unmarshal([]byte(argsJSON), &test); err == nil {
				tc.Function.Arguments = argsJSON
			} else if argsJSON != "" {
				// 若不是合法 JSON 对象，则包裹在 content 字段中。
				tc.Function.Arguments = fmt.Sprintf(`{"content": %s}`, argsJSON)
			}
		}
	}
}

// mapAnthropicStopReason 将 Anthropic 的 stop reason 字符串转换为
// 跨 provider 使用的统一 finish reason 格式。
func mapAnthropicStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "stop_sequence":
		return "stop"
	default:
		return reason
	}
}

// firstNonEmpty 返回传入值中第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
