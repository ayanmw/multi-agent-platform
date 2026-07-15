// Package harness — ContextCompressor: synchronous context compression for long sessions.
//
// When a multi-turn session exceeds turn or token thresholds, the ContextCompressor
// replaces old turns with a structured summary. This keeps the context window small
// while preserving the key facts from earlier conversation history.
//
// Compression strategy (Phase 5-B):
//   - Trigger: turn_count >= 20 OR total_tokens >= 100000
//   - Keep: the most recent 5 turns intact
//   - Summarize: all earlier turns into a single structured memory record
//   - Persist: summary as a session-scoped consolidated memory
//   - Replace: old session_messages are deleted; summary is injected as a system message
//
// Phase 6-F: per-turn summarization delegates to a LLMSummarizer (which
//
//	falls back to the legacy keyword implementation when the LLM call
//	fails).
package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/google/uuid"
)

// ContextCompressor compresses old conversation turns into structured summaries
// when thresholds are exceeded. It keeps the most recent N turns intact and
// summarizes older turns into memory records.
type ContextCompressor struct {
	db         CompressorDB
	summarizer LLMSummarizer
}

// CompressorDB is the minimal DB interface needed by the compressor.
// It isolates the compressor from the full db package, making it easier to test.
type CompressorDB interface {
	// QuerySessionMessages returns all messages for a session, ordered by turn_index ASC, created_at ASC.
	QuerySessionMessages(sessionID string) ([]db.SessionMessageRecord, error)
	// QuerySessionByID returns the session record for a session.
	QuerySessionByID(sessionID string) (*db.SessionRecord, error)
	// InsertMemory persists a memory record (used to store the generated summary).
	InsertMemory(record db.MemoryRecord) error
	// DeleteSessionMessagesBeforeTurn deletes all session_messages with turn_index < the given value.
	DeleteSessionMessagesBeforeTurn(sessionID string, turnIndex int) error
	// UpdateSessionContextSize updates total_tokens and context_size for a session.
	UpdateSessionContextSize(sessionID string, totalTokens int, contextSize int) error
}

// CompressResult reports the outcome of a compression check.
type CompressResult struct {
	// Compressed is true if compression was actually performed.
	Compressed bool `json:"compressed"`
	// TurnsCompressed is the number of complete turns summarized.
	TurnsCompressed int `json:"turns_compressed"`
	// SummaryContent is the generated summary text (empty if no compression).
	SummaryContent string `json:"summary_content"`
	// MessagesKept is the number of individual messages retained after compression.
	MessagesKept int `json:"messages_kept"`
}

// NewContextCompressor creates a new ContextCompressor backed by the given DB.
// summarizer is consulted for every per-turn summary; if nil the compressor
// falls back to its own keyword implementation to preserve Phase 5-B behavior.
func NewContextCompressor(database CompressorDB, summarizer LLMSummarizer) *ContextCompressor {
	return &ContextCompressor{db: database, summarizer: summarizer}
}

const (
	// turnThreshold triggers compression when a session has at least this many turns.
	turnThreshold = 20
	// tokenThreshold triggers compression when total_tokens reaches this value.
	tokenThreshold = 100000
	// keepTurns is the number of most recent turns to preserve unchanged.
	keepTurns = 5
)

// CompressIfNeeded checks thresholds and compresses the session context if needed.
// It is called synchronously before starting a new task in a session, ensuring the
// context is clean before the agent loop begins.
func (cc *ContextCompressor) CompressIfNeeded(sessionID string) (*CompressResult, error) {
	// Load the session record so we can check thresholds.
	session, err := cc.db.QuerySessionByID(sessionID)
	if err != nil {
		return nil, fmt.Errorf("query session %s: %w", sessionID, err)
	}

	// No compression needed if neither threshold is exceeded.
	if session.TurnCount < turnThreshold && session.TotalTokens < tokenThreshold {
		return &CompressResult{Compressed: false}, nil
	}

	// Load all session messages and group them by turn.
	messages, err := cc.db.QuerySessionMessages(sessionID)
	if err != nil {
		return nil, fmt.Errorf("query session messages %s: %w", sessionID, err)
	}
	if len(messages) == 0 {
		return &CompressResult{Compressed: false}, nil
	}

	// Find the maximum turn index in the session.
	maxTurn := -1
	for _, m := range messages {
		if m.TurnIndex > maxTurn {
			maxTurn = m.TurnIndex
		}
	}
	if maxTurn < 0 {
		return &CompressResult{Compressed: false}, nil
	}

	// Determine the cutoff turn: anything strictly earlier than this will be compressed.
	cutoffTurn := maxTurn - keepTurns + 1
	if cutoffTurn <= 0 {
		// Not enough old turns to compress; keep everything as-is.
		return &CompressResult{Compressed: false}, nil
	}

	// Split messages into older turns (to summarize) and recent turns (to keep).
	oldMessagesByTurn := make(map[int][]db.SessionMessageRecord)
	var keptMessages []db.SessionMessageRecord
	for _, m := range messages {
		// Never compress the synthetic summary message (turn_index == -1).
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

	// Generate a per-turn summary for each older turn.
	// Phase 6-F: prefer LLMSummarizer (which falls back to keyword internally);
	// if no summarizer is configured we use the legacy keyword path directly.
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

	// Build the combined structured summary text.
	summaryText := cc.generateCompressedSummary(turnSummaries)

	// Persist the summary as a session-scoped consolidated memory.
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

	// Delete the old session messages that have been compressed.
	if err := cc.db.DeleteSessionMessagesBeforeTurn(sessionID, cutoffTurn); err != nil {
		return nil, fmt.Errorf("delete compressed messages before turn %d: %w", cutoffTurn, err)
	}

	// Insert the synthetic summary as a system message (turn_index = -1).
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

	// Recalculate total token and context size estimates.
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

// generateTurnSummary creates a keyword-based summary for a single turn.
// Phase 6-F: kept as the legacy fallback path; the public entry point for
// the keyword summarization interface is SummarizeTurn below.
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

// generateCompressedSummary combines multiple per-turn summaries into one structured text
// suitable for use as a compressed context message.
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

// extractTaskIDs returns a de-duplicated list of task IDs from the grouped messages.
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

// estimateRemainingMetrics estimates total token count and context size after compression.
// We approximate tokens as bytes / 4, which is reasonable for English/ASCII content.
// This avoids importing a tokenizer dependency in Phase 5-B.
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

// truncateSummary truncates content for summary inclusion.
func truncateSummary(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "..."
}
