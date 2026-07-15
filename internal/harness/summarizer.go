// Package harness — Summarizer: LLM-based memory summarization with keyword fallback.
//
// ContextCompressor (session turn summary) and Heartbeat (episode summary) both
// previously produced keyword-based summaries. Phase 6-F upgrades them to call
// the LLM via a Provider, while preserving the existing keyword implementation
// as a fallback when the LLM call fails (timeout, network error, empty response).
//
// # Architecture
//
//   - KeywordSummarizer — interface for the legacy keyword extractors, retained
//     so LLMSummarizer can degrade gracefully.
//   - LLMSummarizer     — public interface used by CompressIfNeeded / Beat.
//   - LLMSummarizerImpl — default implementation. Calls provider.Chat with a
//     compact prompt, times out via context, and falls back on failure.
//
// # Event Visibility
//
// Each call emits a memory_summarize_{started,completed,failed} event so the
// frontend can observe summarization latency and fallback usage. Events are
// best-effort: emitter==nil suppresses emission entirely.
package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// KeywordSummarizer is the legacy keyword-based summarization interface. It
// is preserved as a fallback target for LLMSummarizerImpl — when the LLM
// call fails, the keyword path runs and its output is returned.
type KeywordSummarizer interface {
	SummarizeTurn(ctx context.Context, turnIndex int, messages []db.SessionMessageRecord) (string, error)
	SummarizeEpisode(ctx context.Context, taskID string, convs []db.ConversationRecord, steps []db.StepRecord) (string, error)
}

// LLMSummarizer is the primary summarization interface used by
// ContextCompressor and Heartbeat. Implementations should attempt LLM-based
// summarization and fall back to keyword extraction on failure (returning
// nil error in that case so the caller always gets a usable summary).
type LLMSummarizer interface {
	SummarizeTurn(ctx context.Context, turnIndex int, messages []db.SessionMessageRecord) (string, error)
	SummarizeEpisode(ctx context.Context, taskID string, convs []db.ConversationRecord, steps []db.StepRecord) (string, error)
}

// EventEmitter is the minimal interface LLMSummarizerImpl uses to publish
// summarization events. Passing nil disables event emission entirely. The
// harness package depends only on this interface so the package does not
// pull in ws.Hub or runtime.EventBus directly.
type EventEmitter interface {
	Emit(eventType string, data map[string]any)
}

// Event type constants for summarization telemetry. They follow the
// snake_case naming used by other memory events in pkg/event.
const (
	EventMemorySummarizeStarted   = "memory_summarize_started"
	EventMemorySummarizeCompleted = "memory_summarize_completed"
	EventMemorySummarizeFailed    = "memory_summarize_failed"
)

// defaultSummaryTimeout caps each summarization call. Compression must remain
// synchronous (called inside CompressIfNeeded / Beat); 8s gives the LLM
// enough budget for short summaries without blocking long sessions.
const defaultSummaryTimeout = 8 * time.Second

// turnSummaryPromptTemplate is the prompt used for per-turn summarization.
// The placeholder is replaced with a compact rendering of the turn's
// messages (user / assistant / tool blocks). The model is asked to reply
// with a short factual paragraph (no preamble) so the result can be dropped
// straight into the session summary.
const turnSummaryPromptTemplate = `You are summarizing a single conversation turn for memory consolidation.

Produce a compact paragraph (max 80 words) capturing:
- The user's goal or question
- The final outcome or assistant answer
- Any tool calls and their results (only the key ones)

Be factual and concise. Do NOT include meta language like "This turn" or "The user asked". Reply with the summary only.

--- TURN ---
%s
--- END TURN ---`

// episodeSummaryPromptTemplate is the prompt used for full episode
// summarization. It receives a flattened view of the task's conversations +
// key steps so the model can produce a paragraph suitable for the
// consolidated memory tier.
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

// LLMSummarizerImpl is the default LLMSummarizer. It calls provider.Chat with
// a short, focused prompt and falls back to a keyword summarizer on failure.
// Event emission is best-effort; nil emitter disables it.
type LLMSummarizerImpl struct {
	provider llm.Provider
	model    string
	fallback KeywordSummarizer
	emitter  EventEmitter
	timeout  time.Duration
}

// NewLLMSummarizerImpl wires the LLM summarizer. fallback may be nil for tests
// that only exercise the LLM path; in that case failures propagate as errors.
// emitter may be nil to disable event publishing.
func NewLLMSummarizerImpl(provider llm.Provider, model string, fallback KeywordSummarizer, emitter EventEmitter) *LLMSummarizerImpl {
	return &LLMSummarizerImpl{
		provider: provider,
		model:    model,
		fallback: fallback,
		emitter:  emitter,
		timeout:  defaultSummaryTimeout,
	}
}

// SummarizeTurn produces a per-turn summary, preferring the LLM. On any
// failure (timeout, network, parse, empty) it falls back to the keyword
// implementation if available; otherwise it returns the underlying error.
//
// Events:
//   - memory_summarize_started   (kind=turn, model, turn_index)
//   - memory_summarize_completed (kind=turn, fallback_used, duration_ms)
//   - memory_summarize_failed    (only if fallback also fails)
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
	// LLM failed or returned empty — fall back.
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

// SummarizeEpisode produces a task-level summary, preferring the LLM. Same
// fallback semantics as SummarizeTurn. taskID is used both for the prompt
// and for telemetry events.
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

// callLLM performs the provider.Chat call. It returns the assistant content
// trimmed of leading/trailing whitespace. Empty responses are treated as a
// soft failure so the fallback path runs.
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

// emit is a safe event publish helper; nil emitter is a no-op.
func (s *LLMSummarizerImpl) emit(eventType string, data map[string]any) {
	if s.emitter == nil {
		return
	}
	s.emitter.Emit(eventType, data)
}

// errString safely extracts an error message for telemetry.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// buildTurnPrompt renders the turn's messages into a compact text block for
// the LLM prompt. Each message is prefixed by its role so the model can
// distinguish user input from assistant output and tool results.
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

// buildEpisodePrompt flattens the task's conversation history + key steps
// into a single text block. Tool-call and observation steps are surfaced
// because they often carry the most important outcome information.
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

// HubSender is the minimal hub surface needed by HubEmitter.
type HubSender interface {
	SendEvent(evt interface{})
}

// HubEmitter forwards memory events through the WebSocket hub's broadcast
// channel. We use interface{} so summarizer.go does not need to import pkg/event
// directly — callers wrap events as needed.
type HubEmitter struct {
	Hub HubSender
}

// Emit satisfies EventEmitter.
func (h *HubEmitter) Emit(eventType string, data map[string]any) {
	if h == nil || h.Hub == nil {
		return
	}
	// We push a raw event payload; the hub is responsible for serialization.
	// Consumers typically wrap data in pkg/event.NewEvent before calling SendEvent.
	h.Hub.SendEvent(map[string]any{"type": eventType, "data": data})
}

// keywordAdapter exposes existing receiver methods through the KeywordSummarizer
// interface so LLMSummarizerImpl can fall back to keyword extraction without
// coupling to ContextCompressor/Heartbeat internals.
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

// NewKeywordAdapter builds a KeywordSummarizer from existing receiver methods.
// Pass nil for either fn to disable that path.
func NewKeywordAdapter(
	turnFn func(ctx context.Context, turnIndex int, messages []db.SessionMessageRecord) (string, error),
	episodeFn func(ctx context.Context, taskID string, convs []db.ConversationRecord, steps []db.StepRecord) (string, error),
) KeywordSummarizer {
	return &keywordAdapter{turnFn: turnFn, episodeFn: episodeFn}
}

// BuildKeywordEpisodeSummary is the keyword-based episode summary path retained
// for fallback. It queries the MemoryDB directly so it stays decoupled from the
// Heartbeat struct (and can be used as the keyword adapter fn from main.go).
func BuildKeywordEpisodeSummary(db MemoryDB, taskID string) (string, error) {
	convs, err := db.QueryConversationsByTask(taskID)
	if err != nil {
		return "", fmt.Errorf("query conversations: %w", err)
	}
	steps, _ := db.QueryStepsByTaskForMemory(taskID)
	return renderKeywordEpisode(taskID, convs, steps), nil
}

// renderKeywordEpisode produces a structured keyword-only summary.
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

// truncateAtKB is a local helper to avoid name collisions with compressor.go's
// truncateSummary and heartbeat.go's truncateContent.
func truncateAtKB(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
