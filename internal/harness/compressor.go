// Package harness —— ContextCompressor：用于长会话的同步 context 压缩。
//
// 当多轮会话超过 turn 或 token 阈值时，ContextCompressor 会将旧 turn 替换为结构化
// summary。这样可在保持 context window 较小的同时保留早期对话历史的关键事实。
//
// 压缩策略（Phase 5-B）：
//   - 触发：turn_count >= 20 或 total_tokens >= 100000
//   - 保留：最近 5 个 turn 不动
//   - 摘要：将更早的所有 turn 摘要为单条结构化 memory 记录
//   - 持久化：summary 作为 session 级 consolidated memory 存储
//   - 替换：旧的 session_messages 被删除；summary 作为 system message 注入
//
// Phase 6-F：每轮 summarization 委托给 LLMSummarizer（当 LLM 调用失败时回退到
//
//	旧的关键词实现）。
package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/google/uuid"
)

// ContextCompressor 在阈值超限时将旧对话 turn 压缩为结构化 summary。它保留最近 N 个
// turn 不动，将更早的 turn 摘要为 memory 记录。
type ContextCompressor struct {
	db         CompressorDB
	summarizer LLMSummarizer
}

// CompressorDB 是 compressor 所需的最小 DB 接口。它将 compressor 与完整的 db package
// 隔离，便于测试。
type CompressorDB interface {
	// QuerySessionMessages 返回某 session 的所有 message，按 turn_index ASC, created_at ASC 排序。
	QuerySessionMessages(sessionID string) ([]db.SessionMessageRecord, error)
	// QuerySessionByID 返回某 session 的 session 记录。
	QuerySessionByID(sessionID string) (*db.SessionRecord, error)
	// InsertMemory 持久化一条 memory 记录（用于存储生成的 summary）。
	InsertMemory(record db.MemoryRecord) error
	// DeleteSessionMessagesBeforeTurn 删除 turn_index 小于给定值的所有 session_messages。
	DeleteSessionMessagesBeforeTurn(sessionID string, turnIndex int) error
	// UpdateSessionContextSize 更新 session 的 total_tokens 与 context_size。
	UpdateSessionContextSize(sessionID string, totalTokens int, contextSize int) error
}

// CompressResult 报告一次压缩检查的结果。
type CompressResult struct {
	// Compressed 为 true 表示实际执行了压缩。
	Compressed bool `json:"compressed"`
	// TurnsCompressed 是被摘要的完整 turn 数。
	TurnsCompressed int `json:"turns_compressed"`
	// SummaryContent 是生成的 summary 文本（未压缩时为空）。
	SummaryContent string `json:"summary_content"`
	// MessagesKept 是压缩后保留的单条 message 数量。
	MessagesKept int `json:"messages_kept"`
}

// NewContextCompressor 用给定 DB 创建 ContextCompressor。summarizer 用于每轮 summary；
// 若为 nil，compressor 会回退到自身的关键词实现以保留 Phase 5-B 行为。
func NewContextCompressor(database CompressorDB, summarizer LLMSummarizer) *ContextCompressor {
	return &ContextCompressor{db: database, summarizer: summarizer}
}

const (
	// turnThreshold 当 session 至少有这么多 turn 时触发压缩。
	turnThreshold = 20
	// tokenThreshold 当 total_tokens 达到此值时触发压缩。
	tokenThreshold = 100000
	// keepTurns 是要保持不变的最近 turn 数量。
	keepTurns = 5
)

// CompressIfNeeded 检查阈值并在需要时压缩 session context。它在新任务开始前同步调用，
// 确保 agent loop 开始前 context 是干净的。
func (cc *ContextCompressor) CompressIfNeeded(sessionID string) (*CompressResult, error) {
	// 加载 session 记录以便检查阈值。
	session, err := cc.db.QuerySessionByID(sessionID)
	if err != nil {
		return nil, fmt.Errorf("query session %s: %w", sessionID, err)
	}

	// 两个阈值都未超过时无需压缩。
	if session.TurnCount < turnThreshold && session.TotalTokens < tokenThreshold {
		return &CompressResult{Compressed: false}, nil
	}

	// 加载所有 session message 并按 turn 分组。
	messages, err := cc.db.QuerySessionMessages(sessionID)
	if err != nil {
		return nil, fmt.Errorf("query session messages %s: %w", sessionID, err)
	}
	if len(messages) == 0 {
		return &CompressResult{Compressed: false}, nil
	}

	// 找到 session 中最大的 turn index。
	maxTurn := -1
	for _, m := range messages {
		if m.TurnIndex > maxTurn {
			maxTurn = m.TurnIndex
		}
	}
	if maxTurn < 0 {
		return &CompressResult{Compressed: false}, nil
	}

	// 确定截断 turn：严格早于此值的 turn 都会被压缩。
	cutoffTurn := maxTurn - keepTurns + 1
	if cutoffTurn <= 0 {
		// 旧 turn 不足以压缩；全部原样保留。
		return &CompressResult{Compressed: false}, nil
	}

	// 将 message 拆分为旧 turn（待摘要）与近期 turn（保留）。
	oldMessagesByTurn := make(map[int][]db.SessionMessageRecord)
	var keptMessages []db.SessionMessageRecord
	for _, m := range messages {
		// 绝不压缩合成的 summary message（turn_index == -1）。
		if m.TurnIndex < 0 {
			continue
		}
		if m.TurnIndex < cutoffTurn {
			oldMessagesByTurn[m.TurnIndex] = append(oldMessagesByTurn[m.TurnIndex], m)
		} else {
			keptMessages = append(keptMessages, m)
		}
	}
	if len(oldMessagesByTurn) == 0 {
		return &CompressResult{Compressed: false}, nil
	}

	// 为每个旧 turn 生成单轮 summary。
	// Phase 6-F：优先使用 LLMSummarizer（内部会回退到关键词）；若未配置 summarizer，
	// 则直接走旧的关键词路径。
	var turnSummaries []string
	ctx := context.Background()
	for turn := 0; turn < cutoffTurn; turn++ {
		if msgs, ok := oldMessagesByTurn[turn]; ok {
			var summary string
			if cc.summarizer != nil {
				summary, _ = cc.summarizer.SummarizeTurn(ctx, turn, msgs)
			}
			if summary == "" {
				summary = cc.generateTurnSummary(turn, msgs)
			}
			turnSummaries = append(turnSummaries, summary)
		}
	}

	// 构建组合的结构化 summary 文本。
	summaryText := cc.generateCompressedSummary(turnSummaries)

	// 将 summary 作为 session 级 consolidated memory 持久化。
	memoryID := "mem_summary_" + uuid.New().String()
	now := time.Now()
	memoryRecord := db.MemoryRecord{
		ID:             memoryID,
		ProjectID:      session.ProjectID,
		Scope:          "session",
		SessionID:      sessionID,
		Type:           "session_summary",
		Tier:           "consolidated",
		Content:        summaryText,
		Confidence:     0.85,
		Status:         "active",
		SourceTaskIDs:  cc.extractTaskIDs(oldMessagesByTurn),
		SourceEventIDs: []string{},
		AccessCount:    1,
		LastAccessed:   &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if memoryRecord.ProjectID == "" {
		memoryRecord.ProjectID = "default"
	}
	if err := cc.db.InsertMemory(memoryRecord); err != nil {
		return nil, fmt.Errorf("insert session summary memory: %w", err)
	}

	// 删除已被压缩的旧 session message。
	if err := cc.db.DeleteSessionMessagesBeforeTurn(sessionID, cutoffTurn); err != nil {
		return nil, fmt.Errorf("delete compressed messages before turn %d: %w", cutoffTurn, err)
	}

	// 将合成 summary 作为 system message 插入（turn_index = -1）。
	summaryMessage := db.SessionMessageRecord{
		ID:        "msg_summary_" + uuid.New().String(),
		SessionID: sessionID,
		TaskID:    "",
		TurnIndex: -1,
		Role:      "system",
		Content:   summaryText,
	}
	if err := db.InsertSessionMessage(summaryMessage); err != nil {
		return nil, fmt.Errorf("insert compressed summary message: %w", err)
	}

	// 重新估算总 token 数与 context size。
	totalTokens, contextSize := cc.estimateRemainingMetrics(summaryText, keptMessages)
	if err := cc.db.UpdateSessionContextSize(sessionID, totalTokens, contextSize); err != nil {
		return nil, fmt.Errorf("update session context size: %w", err)
	}

	return &CompressResult{
		Compressed:      true,
		TurnsCompressed: len(turnSummaries),
		SummaryContent:  summaryText,
		MessagesKept:    len(keptMessages),
	}, nil
}

// generateTurnSummary 为单个 turn 创建基于关键词的 summary。
// Phase 6-F：保留为旧版回退路径；关键词 summarization 接口的公开入口是下面的
// SummarizeTurn。
func (cc *ContextCompressor) generateTurnSummary(turnIndex int, messages []db.SessionMessageRecord) string {
	var userInput, finalAnswer string
	toolCount := 0
	toolSummary := make([]string, 0)

	for _, m := range messages {
		switch m.Role {
		case "user":
			if userInput == "" {
				userInput = truncateSummary(m.Content, 200)
			}
		case "assistant":
			finalAnswer = truncateSummary(m.Content, 300)
		case "tool":
			toolCount++
			toolSummary = append(toolSummary, truncateSummary(m.Content, 120))
		}
	}

	if userInput == "" {
		userInput = "(no explicit user input)"
	}
	if finalAnswer == "" {
		finalAnswer = "(no final answer recorded)"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Turn %d:\n", turnIndex+1))
	sb.WriteString(fmt.Sprintf("  User: %s\n", userInput))
	if toolCount > 0 {
		sb.WriteString(fmt.Sprintf("  Tools: %d call(s)\n", toolCount))
		for _, ts := range toolSummary {
			sb.WriteString(fmt.Sprintf("    - %s\n", ts))
		}
	}
	sb.WriteString(fmt.Sprintf("  Result: %s", finalAnswer))
	return sb.String()
}

// generateCompressedSummary 将多个单轮 summary 组合成一段结构化文本，适合用作压缩后的
// context message。
func (cc *ContextCompressor) generateCompressedSummary(turnSummaries []string) string {
	var sb strings.Builder
	sb.WriteString("[COMPRESSED CONVERSATION SUMMARY]\n")
	sb.WriteString(fmt.Sprintf("The following is a summary of turns %d through %d of the conversation.\n\n", 1, len(turnSummaries)))
	for i, ts := range turnSummaries {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(ts)
	}
	sb.WriteString("\n\n[END SUMMARY]")
	return sb.String()
}

// extractTaskIDs 返回从分组 message 中去重后的 task ID 列表。
func (cc *ContextCompressor) extractTaskIDs(msgsByTurn map[int][]db.SessionMessageRecord) []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, msgs := range msgsByTurn {
		for _, m := range msgs {
			if m.TaskID == "" {
				continue
			}
			if _, ok := seen[m.TaskID]; !ok {
				seen[m.TaskID] = struct{}{}
				ids = append(ids, m.TaskID)
			}
		}
	}
	return ids
}

// estimateRemainingMetrics 估算压缩后的总 token 数与 context size。我们将 token 近似为
// 字节数 / 4，对英文/ASCII 内容较合理。这样可在 Phase 5-B 避免引入 tokenizer 依赖。
func (cc *ContextCompressor) estimateRemainingMetrics(summaryText string, keptMessages []db.SessionMessageRecord) (totalTokens, contextSize int) {
	contextSize = len(summaryText)
	totalTokens = len(summaryText) / 4
	for _, m := range keptMessages {
		contextSize += len(m.Content) + len(m.Role) + len(m.ToolCallID) + len(m.ToolCalls)
		totalTokens += m.TokenCount
		if m.TokenCount == 0 {
			totalTokens += len(m.Content) / 4
		}
	}
	return totalTokens, contextSize
}

// truncateSummary 将内容截断以便纳入 summary。
func truncateSummary(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "..."
}
