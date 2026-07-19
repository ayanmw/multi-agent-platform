// Package llm 提供 token 估算工具，用于上下文窗口记账。
//
// # 设计理由
//
// 多数 OpenAI-compatible API 只为整次请求上报汇总 usage。
// 它们不会按消息拆分 prompt_tokens。为了让前端能看到
// 上下文窗口如何被填充的白盒视图，Engine 需要一个本地 tokenizer
// 来估算每条消息的 token 数。
//
// 当前实现使用简单但够用的启发式：
//   - 文本密集内容按约 4 个字符 1 个 token 估算。
//   - 加上少量每条消息开销，覆盖 role token 与分隔符。
//
// 刻意不采用更重的方案（tiktoken 移植、embedding API 调用），因为：
//  1. 无需外部依赖。
//  2. 确定性且开销小。
//  3. 对上下文窗口*占比*可视化而言，相对准确度已足够；
//     不需要精确的 API token 计数。
//
// 后续工作（Phase 7+）若需精确值可引入基于 tiktoken 的计数，
// 但本文件的公开 API 保持不变。
package llm

// messageOverheadTokens 是 OpenAI chat 格式中每条消息的估算格式开销：
// role、分隔符以及 name/tool_call_id 元数据。
const messageOverheadTokens = 5

// EstimateTokenCount 近似计算单条消息的 token 数。
//
// 估算采用约 4 个字符 1 个 token + 固定每条消息开销。
// 快速、零依赖，对基于占比的 UI 可视化足够准确。返回值始终非负。
func EstimateTokenCount(msg Message) int {
	// 合并所有计入上下文长度的文本内容。
	text := msg.Content + msg.Reasoning
	tokens := len(text)/4 + messageOverheadTokens
	if tokens < 0 {
		tokens = 0
	}
	return tokens
}

// SumEstimatedTokens 返回消息 slice 的估算总 token 数。
func SumEstimatedTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += EstimateTokenCount(m)
	}
	return total
}

// ContextSnapshotMessage 是 Engine 在每次 LLM 调用前发出的
// context_window_snapshot 事件中的单条消息负载。
type ContextSnapshotMessage struct {
	Role          string     `json:"role"`
	Content       string     `json:"content"`
	Reasoning     string     `json:"reasoning,omitempty"`
	EstimatedTokens int      `json:"estimated_tokens"`
	UsageRatio    float64    `json:"usage_ratio"`
	ToolCallID    string     `json:"tool_call_id,omitempty"`
	ToolCalls     []ToolCall `json:"tool_calls,omitempty"`
}

// ContextWindowSnapshot 描述 LLM 调用前一刻的上下文窗口状态。
// 它刻意保持轻量、人类可读，便于前端同时渲染进度条和消息级明细。
type ContextWindowSnapshot struct {
	Model                string                   `json:"model"`
	MaxContextTokens     int                      `json:"max_context_tokens"`
	EstimatedTotalTokens int                      `json:"estimated_total_tokens"`
	EstimatedUsageRatio  float64                  `json:"estimated_usage_ratio"`
	Messages             []ContextSnapshotMessage `json:"messages"`
}

// BuildContextWindowSnapshot 构造当前上下文窗口的快照。
//
// maxContextTokens 从所选 model 的 profile 读取。若为 0，
// 快照不计算 ratio（usage_ratio 保持为 0）。
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

// defaultContextWindow 是 model profile 不可用时的回退最大上下文长度。
// 200K 对应当前主流大上下文 model，给 UI 提供一个合理基线，
// 直到 Phase 7 引入 provider 感知的上下文窗口 registry。
const defaultContextWindow = 200_000

// EstimateModelContextWindow 从 registry 解析 model 的最大上下文窗口。
// 若 model 未知或 registry 为 nil，返回默认 200K 回退，
// 以免 UI 容量条被人为压低。
func EstimateModelContextWindow(registry *ModelRegistry, model string) int {
	if registry != nil && model != "" {
		if p := registry.Get(model); p != nil && p.MaxContextWindow > 0 {
			return p.MaxContextWindow
		}
	}
	return defaultContextWindow
}
