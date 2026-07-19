// Package harness —— Summarizer：基于 LLM 的 memory 摘要，带关键词回退。
//
// ContextCompressor（session turn summary）与 Heartbeat（episode summary）此前都生成
// 基于关键词的 summary。Phase 6-F 将它们升级为通过 Provider 调用 LLM，并在 LLM 调用
// 失败（超时、网络错误、空响应）时保留现有关键词实现作为回退。
//
// # 架构
//
//   - KeywordSummarizer —— 旧关键词提取器的接口，保留以便 LLMSummarizer 优雅降级。
//   - LLMSummarizer     —— CompressIfNeeded / Beat 使用的公开接口。
//   - LLMSummarizerImpl —— 默认实现。用紧凑 prompt 调用 provider.Chat，通过 context 超时，
//     失败时回退。
//
// # 事件可见性
//
// 每次调用都会发射 memory_summarize_{started,completed,failed} 事件，使前端可观察
// summarization 延迟与回退使用情况。事件是 best-effort 的：emitter==nil 时完全不发射。
package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// KeywordSummarizer 是旧的关键词摘要接口。保留为 LLMSummarizerImpl 的回退目标 ——
// 当 LLM 调用失败时，关键词路径运行并返回其输出。
type KeywordSummarizer interface {
	SummarizeTurn(ctx context.Context, turnIndex int, messages []db.SessionMessageRecord) (string, error)
	SummarizeEpisode(ctx context.Context, taskID string, convs []db.ConversationRecord, steps []db.StepRecord) (string, error)
}

// LLMSummarizer 是 ContextCompressor 与 Heartbeat 使用的主要摘要接口。实现应尝试基于
// LLM 的摘要，失败时回退到关键词提取（此时返回 nil error，使调用者总能获得可用 summary）。
type LLMSummarizer interface {
	SummarizeTurn(ctx context.Context, turnIndex int, messages []db.SessionMessageRecord) (string, error)
	SummarizeEpisode(ctx context.Context, taskID string, convs []db.ConversationRecord, steps []db.StepRecord) (string, error)
}

// EventEmitter 是 LLMSummarizerImpl 发布摘要事件所需的最小接口。传 nil 会完全禁用事件
// 发射。harness package 仅依赖此接口，因此该 package 不会直接引入 ws.Hub 或
// runtime.EventBus。
type EventEmitter interface {
	Emit(eventType string, data map[string]any)
}

// Event type 常量用于摘要遥测。它们遵循 pkg/event 中其他 memory 事件使用的
// snake_case 命名。
const (
	EventMemorySummarizeStarted   = "memory_summarize_started"
	EventMemorySummarizeCompleted = "memory_summarize_completed"
	EventMemorySummarizeFailed    = "memory_summarize_failed"
)

// defaultSummaryTimeout 限制每次摘要调用。压缩必须保持同步（在 CompressIfNeeded /
// Beat 内调用）；8s 给 LLM 足够预算生成短 summary，又不会长时间阻塞长会话。
const defaultSummaryTimeout = 8 * time.Second

// turnSummaryPromptTemplate 是用于每轮摘要的 prompt。占位符会被该轮 message（user /
// assistant / tool 块）的紧凑渲染替换。要求模型回复一段简短的事实性段落（无前言），
// 以便结果可直接放入 session summary。
const turnSummaryPromptTemplate = `You are summarizing a single conversation turn for memory consolidation.

Produce a compact paragraph (max 80 words) capturing:
- The user's goal or question
- The final outcome or assistant answer
- Any tool calls and their results (only the key ones)

Be factual and concise. Do NOT include meta language like "This turn" or "The user asked". Reply with the summary only.

--- TURN ---
%s
--- END TURN ---`

// episodeSummaryPromptTemplate 是用于完整 episode 摘要的 prompt。它接收任务对话 +
// 关键 step 的扁平视图，使模型可生成适合 consolidated memory tier 的段落。
const episodeSummaryPromptTemplate = `You are summarizing a completed agent task for long-term memory storage.

Produce a structured summary (3-5 sentences) covering:
- Task goal and user input
- Tools called and their key results
- Errors or failures encountered
- Final outcome and key learnings

Be factual. No preamble, no "This task". Return only the summary text.

--- TASK %s ---
%s
--- KEY STEPS ---
%s
--- END TASK ---`

// LLMSummarizerImpl 是默认的 LLMSummarizer。它用简短、聚焦的 prompt 调用 provider.Chat，
// 失败时回退到关键词 summarizer。事件发射是 best-effort 的；nil emitter 会禁用。
type LLMSummarizerImpl struct {
	provider llm.Provider
	model    string
	fallback KeywordSummarizer
	emitter  EventEmitter
	timeout  time.Duration
}

// NewLLMSummarizerImpl 装配 LLM summarizer。fallback 在只测试 LLM 路径的测试中可为
// nil；此时失败会作为 error 传播。emitter 可为 nil 以禁用事件发布。
func NewLLMSummarizerImpl(provider llm.Provider, model string, fallback KeywordSummarizer, emitter EventEmitter) *LLMSummarizerImpl {
	return &LLMSummarizerImpl{
		provider: provider,
		model:    model,
		fallback: fallback,
		emitter:  emitter,
		timeout:  defaultSummaryTimeout,
	}
}

// SummarizeTurn 生成单轮 summary，优先使用 LLM。任何失败（超时、网络、解析、空）时，
// 若有关键词实现则回退；否则返回底层 error。
//
// 事件：
//   - memory_summarize_started   (kind=turn, model, turn_index)
//   - memory_summarize_completed (kind=turn, fallback_used, duration_ms)
//   - memory_summarize_failed    (仅当回退也失败时)
func (s *LLMSummarizerImpl) SummarizeTurn(ctx context.Context, turnIndex int, messages []db.SessionMessageRecord) (string, error) {
	start := time.Now()
	s.emit(EventMemorySummarizeStarted, map[string]any{
		"kind":       "turn",
		"model":      s.model,
		"turn_index": turnIndex,
	})
	prompt := buildTurnPrompt(turnIndex, messages)
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	out, err := s.callLLM(callCtx, prompt)
	if err == nil && out != "" {
		s.emit(EventMemorySummarizeCompleted, map[string]any{
			"kind":          "turn",
			"turn_index":    turnIndex,
			"duration_ms":   time.Since(start).Milliseconds(),
			"fallback_used": false,
		})
		return out, nil
	}
	// LLM 失败或返回空 —— 回退。
	if s.fallback == nil {
		s.emit(EventMemorySummarizeFailed, map[string]any{
			"kind":        "turn",
			"turn_index":  turnIndex,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       errString(err),
		})
		return "", fmt.Errorf("llm summarization failed and no fallback: %w", err)
	}
	out, fbErr := s.fallback.SummarizeTurn(ctx, turnIndex, messages)
	if fbErr != nil {
		s.emit(EventMemorySummarizeFailed, map[string]any{
			"kind":        "turn",
			"turn_index":  turnIndex,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       errString(fbErr),
			"llm_error":   errString(err),
		})
		return "", fmt.Errorf("llm failed (%v) and fallback failed: %w", err, fbErr)
	}
	s.emit(EventMemorySummarizeCompleted, map[string]any{
		"kind":          "turn",
		"turn_index":    turnIndex,
		"duration_ms":   time.Since(start).Milliseconds(),
		"fallback_used": true,
		"llm_error":     errString(err),
	})
	return out, nil
}

// SummarizeEpisode 生成任务级 summary，优先使用 LLM。回退语义与 SummarizeTurn 相同。
// taskID 既用于 prompt，也用于遥测事件。
func (s *LLMSummarizerImpl) SummarizeEpisode(ctx context.Context, taskID string, convs []db.ConversationRecord, steps []db.StepRecord) (string, error) {
	start := time.Now()
	s.emit(EventMemorySummarizeStarted, map[string]any{
		"kind":    "episode",
		"model":   s.model,
		"task_id": taskID,
	})
	prompt := buildEpisodePrompt(taskID, convs, steps)
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	out, err := s.callLLM(callCtx, prompt)
	if err == nil && out != "" {
		s.emit(EventMemorySummarizeCompleted, map[string]any{
			"kind":          "episode",
			"task_id":       taskID,
			"duration_ms":   time.Since(start).Milliseconds(),
			"fallback_used": false,
		})
		return out, nil
	}
	if s.fallback == nil {
		s.emit(EventMemorySummarizeFailed, map[string]any{
			"kind":        "episode",
			"task_id":     taskID,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       errString(err),
		})
		return "", fmt.Errorf("llm episode summarization failed and no fallback: %w", err)
	}
	out, fbErr := s.fallback.SummarizeEpisode(ctx, taskID, convs, steps)
	if fbErr != nil {
		s.emit(EventMemorySummarizeFailed, map[string]any{
			"kind":        "episode",
			"task_id":     taskID,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       errString(fbErr),
			"llm_error":   errString(err),
		})
		return "", fmt.Errorf("llm failed (%v) and fallback failed: %w", err, fbErr)
	}
	s.emit(EventMemorySummarizeCompleted, map[string]any{
		"kind":          "episode",
		"task_id":       taskID,
		"duration_ms":   time.Since(start).Milliseconds(),
		"fallback_used": true,
		"llm_error":     errString(err),
	})
	return out, nil
}

// callLLM 执行 provider.Chat 调用。返回去除首尾空白的 assistant content。空响应视为
// 软失败，以便走回退路径。
func (s *LLMSummarizerImpl) callLLM(ctx context.Context, prompt string) (string, error) {
	if s.provider == nil {
		return "", fmt.Errorf("llm provider is nil")
	}
	req := llm.ChatRequest{
		Model:    s.model,
		Messages: []llm.Message{{Role: "user", Content: prompt}},
		Context:  ctx,
	}
	resp, err := s.provider.Chat(req)
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty chat response")
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("empty assistant content")
	}
	return content, nil
}

// emit 是安全的事件发布辅助；nil emitter 为 no-op。
func (s *LLMSummarizerImpl) emit(eventType string, data map[string]any) {
	if s.emitter == nil {
		return
	}
	s.emitter.Emit(eventType, data)
}

// errString 安全提取 error 信息用于遥测。
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// buildTurnPrompt 将该轮 message 渲染为 LLM prompt 的紧凑文本块。每条 message 前加
// 其 role，使模型可区分 user 输入、assistant 输出与 tool 结果。
func buildTurnPrompt(turnIndex int, messages []db.SessionMessageRecord) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Turn %d (%d messages):\n", turnIndex+1, len(messages)))
	for _, m := range messages {
		switch m.Role {
		case "user":
			sb.WriteString(fmt.Sprintf("[user] %s\n", truncateSummary(m.Content, 300)))
		case "assistant":
			sb.WriteString(fmt.Sprintf("[assistant] %s\n", truncateSummary(m.Content, 300)))
		case "tool":
			sb.WriteString(fmt.Sprintf("[tool:%s] %s\n", m.ToolCallID, truncateSummary(m.Content, 200)))
		default:
			sb.WriteString(fmt.Sprintf("[%s] %s\n", m.Role, truncateSummary(m.Content, 200)))
		}
	}
	return fmt.Sprintf(turnSummaryPromptTemplate, sb.String())
}

// buildEpisodePrompt 将任务对话历史 + 关键 step 扁平化为单个文本块。tool_call 与
// observation step 会被呈现，因为它们通常携带最重要的结果信息。
func buildEpisodePrompt(taskID string, convs []db.ConversationRecord, steps []db.StepRecord) string {
	var sbConv strings.Builder
	for _, c := range convs {
		sbConv.WriteString(fmt.Sprintf("[%s] %s\n", c.Role, truncateSummary(c.Content, 400)))
	}
	var sbSteps strings.Builder
	for _, st := range steps {
		if st.Type != "tool_call" && st.Type != "observation" {
			continue
		}
		if st.ToolName != "" {
			sbSteps.WriteString(fmt.Sprintf("- tool=%s status=%s output=%s\n", st.ToolName, st.Status, truncateSummary(st.ToolOutput, 200)))
		} else if st.Content != "" {
			sbSteps.WriteString(fmt.Sprintf("- %s: %s\n", st.Type, truncateSummary(st.Content, 200)))
		}
	}
	return fmt.Sprintf(episodeSummaryPromptTemplate, taskID, sbConv.String(), sbSteps.String())
}

// HubSender 是 HubEmitter 所需的最小 hub 表面。
type HubSender interface {
	SendEvent(evt interface{})
}

// HubEmitter 通过 WebSocket hub 的广播 channel 转发 memory 事件。我们使用 interface{}
// 以便 summarizer.go 无需直接 import pkg/event —— 调用者按需包装事件。
type HubEmitter struct {
	Hub HubSender
}

// Emit 满足 EventEmitter。
func (h *HubEmitter) Emit(eventType string, data map[string]any) {
	if h == nil || h.Hub == nil {
		return
	}
	// 我们推送原始事件载荷；hub 负责序列化。
	// 调用者通常在调用 SendEvent 前用 pkg/event.NewEvent 包装 data。
	h.Hub.SendEvent(map[string]any{"type": eventType, "data": data})
}

// keywordAdapter 通过 KeywordSummarizer 接口暴露已有的 receiver 方法，使
// LLMSummarizerImpl 可回退到关键词提取，而不耦合 ContextCompressor/Heartbeat 内部。
type keywordAdapter struct {
	turnFn    func(ctx context.Context, turnIndex int, messages []db.SessionMessageRecord) (string, error)
	episodeFn func(ctx context.Context, taskID string, convs []db.ConversationRecord, steps []db.StepRecord) (string, error)
}

func (k *keywordAdapter) SummarizeTurn(ctx context.Context, turnIndex int, messages []db.SessionMessageRecord) (string, error) {
	if k.turnFn == nil {
		return "", fmt.Errorf("keyword turn adapter not wired")
	}
	return k.turnFn(ctx, turnIndex, messages)
}

func (k *keywordAdapter) SummarizeEpisode(ctx context.Context, taskID string, convs []db.ConversationRecord, steps []db.StepRecord) (string, error) {
	if k.episodeFn == nil {
		return "", fmt.Errorf("keyword episode adapter not wired")
	}
	return k.episodeFn(ctx, taskID, convs, steps)
}

// NewKeywordAdapter 用已有 receiver 方法构建 KeywordSummarizer。任一 fn 传 nil 可禁用
// 该路径。
func NewKeywordAdapter(
	turnFn func(ctx context.Context, turnIndex int, messages []db.SessionMessageRecord) (string, error),
	episodeFn func(ctx context.Context, taskID string, convs []db.ConversationRecord, steps []db.StepRecord) (string, error),
) KeywordSummarizer {
	return &keywordAdapter{turnFn: turnFn, episodeFn: episodeFn}
}

// BuildKeywordEpisodeSummary 是保留用于回退的关键词 episode summary 路径。它直接查询
// MemoryDB，从而与 Heartbeat 结构解耦（可从 main.go 用作 keyword adapter fn）。
func BuildKeywordEpisodeSummary(db MemoryDB, taskID string) (string, error) {
	convs, err := db.QueryConversationsByTask(taskID)
	if err != nil {
		return "", fmt.Errorf("query conversations: %w", err)
	}
	steps, _ := db.QueryStepsByTaskForMemory(taskID)
	return renderKeywordEpisode(taskID, convs, steps), nil
}

// renderKeywordEpisode 生成仅基于关键词的结构化 summary。
func renderKeywordEpisode(taskID string, convs []db.ConversationRecord, steps []db.StepRecord) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task: %s\n", taskID))
	for _, c := range convs {
		if c.Role == "user" {
			sb.WriteString(fmt.Sprintf("Input: %s\n", truncateAtKB(c.Content, 200)))
			break
		}
	}
	toolCount, errorCount := 0, 0
	for _, s := range steps {
		if s.Type == "tool_call" {
			toolCount++
			if s.ToolName != "" {
				sb.WriteString(fmt.Sprintf("Tool[%s]: %s", s.ToolName, truncateAtKB(s.ToolOutput, 100)))
				if s.Status == "failed" {
					sb.WriteString(" (FAILED)")
					errorCount++
				}
				sb.WriteString("\n")
			}
		}
	}
	var finalResult string
	for i := len(convs) - 1; i >= 0; i-- {
		if convs[i].Role == "assistant" {
			finalResult = convs[i].Content
			break
		}
	}
	sb.WriteString(fmt.Sprintf("Result: %s\n", truncateAtKB(finalResult, 300)))
	sb.WriteString(fmt.Sprintf("Stats: %d tools, %d errors, %d messages\n", toolCount, errorCount, len(convs)))
	return sb.String()
}

// truncateAtKB 是本地辅助函数，以避免与 compressor.go 的 truncateSummary 和
// heartbeat.go 的 truncateContent 重名冲突。
func truncateAtKB(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
