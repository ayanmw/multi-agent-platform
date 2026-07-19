// Package llm —— Provider 接口，用于 LLM 协议抽象。
//
// # 设计理由
//
// Provider 接口对 LLM API 协议做抽象，让 Engine 能在不动代码的前提下
// 与不同 provider（OpenAI、Anthropic、DeepSeek）协同。
//
// 各 provider 都有自己的：
//   - 请求/响应格式（OpenAI 用 Chat Completions API，Anthropic 用 Messages API）
//   - 认证方式（Bearer token vs x-api-key header）
//   - streaming 协议（SSE vs 字段名不同的 Server-Sent Events）
//   - tool call 表示（function calling vs tool_use block）
//   - usage/token 上报格式
//
// Provider 接口把这些差异封装在单个 ChatStream 方法之后。Engine 只看到
// 统一的 ChatRequest/StreamChunk/Usage/ToolCall 类型 ——
// provider 在内部处理转换。
//
// # 当前状态（Phase 5）
//
//   - OpenAIProvider：包装现有 Client 的基线实现。
//     支持所有 OpenAI-compatible API（DeepSeek、Groq、Together 等）。
//   - AnthropicProvider：Phase 6 —— 需要完整的请求/响应格式转换，
//     因为 Anthropic 的 Messages API 与 Chat Completions 结构上不同。
//   - DeepSeekProvider：Phase 6 —— 扩展 OpenAIProvider 以支持
//     DeepSeek R1/V4 思维链输出的 reasoning_content。
//
// # 用法
//
//	provider := llm.NewOpenAIProvider(endpoint, apiKey, model)
//	content, usage, toolCalls, err := provider.ChatStream(req, onChunk)
//
// provider 之间的详细差异参见 doc/chapters/09-llm-api-comparison.html，
// 多 model 路由策略参见 doc/chapters/10-multi-model-layered-design.html。
package llm

// Provider 对 LLM API 协议做抽象，让 Engine 能在不动代码的前提下
// 与不同 LLM provider 协同。
//
// 各 provider 实现负责：
//   - 构建 HTTP 请求（URL、header、body 格式）
//   - 解析 streaming 响应（SSE 格式、delta 结构、tool call 累积）
//   - 将 provider 专有类型转换为统一的 ChatRequest/StreamChunk/Usage/ToolCall
//
// Provider 接口刻意保持最小 —— 只暴露 Engine 需要的两个方法：
// Chat（非流式）与 ChatStream（流式）。这让实现简单且可测试。
type Provider interface {
	// Name 返回 provider 标识（例如 "openai"、"anthropic"、"deepseek"）。
	// 用于日志、成本追踪与 model registry 查找。
	Name() string

	// Chat 发送非流式 chat 请求并返回完整响应。
	// 用于不需要 streaming 的简单同步调用
	//（例如 Router intent 分类、简单校验）。
	Chat(req ChatRequest) (*ChatResponse, error)

	// ChatStream 发送 streaming chat 请求，对每个 SSE 事件调用 onChunk。
	// 返回累积的 content、usage、tool calls 以及任意 error。
	//
	// onChunk 回调对每个解析出的 SSE chunk 调用一次。每个 chunk 含
	// text delta、tool call delta 和/或 finish reason。该回调是
	// "白盒"哲学的落地机制 —— LLM 生成的每个 token 都实时转发给前端。
	//
	// 返回的 Usage 来自最后一个 SSE chunk（OpenAI-compatible API 中
	// 唯一携带 usage 数据的 chunk）。返回的 ToolCalls 是所有 delta
	// 累积完成后完整组装的 tool call。
	ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error)
}