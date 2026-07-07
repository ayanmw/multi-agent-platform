// Package harness — Heartbeat: periodic background task for Memory consolidation.
//
// The Heartbeat runs on a configurable interval (default 5 minutes). On each
// beat it scans for newly completed tasks, generates episode summaries from
// their conversation history, and writes candidate memories into the
// consolidated episodic tier of the memories table.
//
// # Architecture
//
// The Heartbeat is the bridge between Raw Episodic memory (conversations table)
// and Consolidated Episodic memory (memories table, tier=consolidated). It
// implements the third tier of the 4-tier memory system:
//
//  1. Working Memory      — per-task context (in-memory)
//  2. Raw Episodic        — conversation records (conversations table)
//  3. Consolidated Episodic — task summaries (memories table, tier=consolidated)  <-- THIS
//  4. Semantic/Policy     — stable rules (memories table, tier=semantic)
//
// # Episode Summarization
//
// The current summarization uses keyword extraction from conversation content.
// In Phase 6+, this will be replaced with an LLM-based summarization using a
// dedicated summarizer model. The generated summaries include:
//   - Tools used and their results
//   - Errors encountered
//   - Final task outcome
//   - Key observations
//
// # Adaptive Interval
//
// The heartbeat interval adapts based on activity: more completed tasks means
// shorter intervals (minimum 30 seconds), fewer tasks means longer intervals
// (maximum 10 minutes). This prevents excessive polling when idle and keeps
// memory fresh when busy.
package harness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// Heartbeat periodically scans for completed tasks, generates episode summaries,
// and writes consolidated memory records.
//
// Lifecycle:
//
//	hb := NewHeartbeat(db.DB)
//	go hb.Start(context.Background())
//	defer hb.Stop()
type Heartbeat struct {
	db       MemoryDB
	interval time.Duration
	state    *HeartbeatState
	mu       sync.Mutex
	cancel   context.CancelFunc
}

// HeartbeatState tracks the heartbeat's progress so it can resume from the
// last checkpoint after a restart. It is persisted as a memory record of
// type "heartbeat_state" with tier "consolidated".
type HeartbeatState struct {
	// LastProcessedEventID is the ID of the last event that was processed.
	// Currently unused — reserved for event-driven heartbeat in Phase 6+.
	LastProcessedEventID string `json:"last_processed_event_id"`

	// LastRunAt is the timestamp of the last successful heartbeat cycle.
	LastRunAt time.Time `json:"last_run_at"`

	// ProcessedTaskCount is the cumulative number of tasks processed since
	// the heartbeat was started.
	ProcessedTaskCount int `json:"processed_task_count"`

	// NextIntervalSeconds is the interval for the next heartbeat cycle,
	// adjusted by adaptiveInterval.
	NextIntervalSeconds int `json:"next_interval_seconds"`
}

// HeartbeatReport summarizes the results of a single heartbeat cycle.
type HeartbeatReport struct {
	// NewTasksFound is the number of completed tasks found since the last beat.
	NewTasksFound int `json:"new_tasks_found"`

	// SummariesGenerated is the number of episode summaries successfully generated.
	SummariesGenerated int `json:"summaries_generated"`

	// MemoriesWritten is the number of memory records written to the DB.
	MemoriesWritten int `json:"memories_written"`

	// Errors is the number of errors encountered during this beat.
	Errors int `json:"errors"`

	// Duration is the time taken for this heartbeat cycle.
	Duration time.Duration `json:"duration_ms"`

	// NextInterval is the interval for the next heartbeat cycle.
	NextInterval time.Duration `json:"next_interval_ms"`
}

// MemoryDB is a minimal interface over the DB operations needed by the Heartbeat
// and PromotionGate. This avoids direct coupling to the db package and makes
// testing easier.
type MemoryDB interface {
	QueryCompletedTaskIDs(since time.Time) ([]string, error)
	QueryConversationsByTask(taskID string) ([]db.ConversationRecord, error)
	QueryStepsByTaskForMemory(taskID string) ([]db.StepRecord, error)
	InsertMemory(record db.MemoryRecord) error
	QueryMemoriesByTier(projectID, tier string) ([]db.MemoryRecord, error)
	UpdateMemoryTier(id, tier, promotionReason string) error
}

// SqliteMemoryDB adapts db.DB (the package-level *sql.DB) to the MemoryDB interface.
// It is the default implementation used by the Heartbeat and PromotionGate.
type SqliteMemoryDB struct{}

// QueryCompletedTaskIDs delegates to db.QueryCompletedTaskIDs.
func (s *SqliteMemoryDB) QueryCompletedTaskIDs(since time.Time) ([]string, error) {
	return db.QueryCompletedTaskIDs(since)
}

// QueryConversationsByTask delegates to db.QueryConversationsByTask.
func (s *SqliteMemoryDB) QueryConversationsByTask(taskID string) ([]db.ConversationRecord, error) {
	return db.QueryConversationsByTask(taskID)
}

// QueryStepsByTaskForMemory delegates to db.QueryStepsByTaskForMemory.
func (s *SqliteMemoryDB) QueryStepsByTaskForMemory(taskID string) ([]db.StepRecord, error) {
	return db.QueryStepsByTaskForMemory(taskID)
}

// InsertMemory delegates to db.InsertMemory.
func (s *SqliteMemoryDB) InsertMemory(record db.MemoryRecord) error {
	return db.InsertMemory(record)
}

// QueryMemoriesByTier delegates to db.QueryMemoriesByTier.
func (s *SqliteMemoryDB) QueryMemoriesByTier(projectID, tier string) ([]db.MemoryRecord, error) {
	return db.QueryMemoriesByTier(projectID, tier)
}

// UpdateMemoryTier delegates to db.UpdateMemoryTier.
func (s *SqliteMemoryDB) UpdateMemoryTier(id, tier, promotionReason string) error {
	return db.UpdateMemoryTier(id, tier, promotionReason)
}

// QuerySessionMessages delegates to db.QuerySessionMessages.
func (s *SqliteMemoryDB) QuerySessionMessages(sessionID string) ([]db.SessionMessageRecord, error) {
	return db.QuerySessionMessages(sessionID)
}

// QuerySessionByID delegates to db.QuerySessionByID.
func (s *SqliteMemoryDB) QuerySessionByID(sessionID string) (*db.SessionRecord, error) {
	return db.QuerySessionByID(sessionID)
}

// DeleteSessionMessagesBeforeTurn delegates to db.DeleteSessionMessagesBeforeTurn.
func (s *SqliteMemoryDB) DeleteSessionMessagesBeforeTurn(sessionID string, turnIndex int) error {
	return db.DeleteSessionMessagesBeforeTurn(sessionID, turnIndex)
}

// UpdateSessionContextSize delegates to db.UpdateSessionContextSize.
func (s *SqliteMemoryDB) UpdateSessionContextSize(sessionID string, totalTokens int, contextSize int) error {
	return db.UpdateSessionContextSize(sessionID, totalTokens, contextSize)
}

// NewHeartbeat creates a new Heartbeat with the default 5-minute interval.
// The database parameter implements the MemoryDB interface for all DB operations.
func NewHeartbeat(database MemoryDB) *Heartbeat {
	interval := 5 * time.Minute
	return &Heartbeat{
		db:       database,
		interval: interval,
		state: &HeartbeatState{
			LastRunAt:           time.Now(),
			NextIntervalSeconds: int(interval.Seconds()),
		},
	}
}

// Start begins the heartbeat loop in a background goroutine.
// It creates a cancellable context and runs Beat() on each tick.
// Call Stop() to halt the heartbeat gracefully.
func (hb *Heartbeat) Start(ctx context.Context) {
	ctx, hb.cancel = context.WithCancel(ctx)
	log.Printf("[Heartbeat] Started with interval %v", hb.interval)

	// Run immediately on start, then on each tick
	go func() {
		// Initial beat
		report, err := hb.Beat(ctx)
		if err != nil {
			log.Printf("[Heartbeat] Initial beat failed: %v", err)
		} else {
			log.Printf("[Heartbeat] Initial beat: %d tasks, %d memories, took %v",
				report.NewTasksFound, report.MemoriesWritten, report.Duration)
		}

		ticker := time.NewTicker(hb.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Println("[Heartbeat] Stopped")
				return
			case <-ticker.C:
				// Adjust ticker interval based on last report's NextInterval
				if report != nil && report.NextInterval != hb.interval {
					ticker.Reset(report.NextInterval)
					hb.interval = report.NextInterval
				}

				report, err = hb.Beat(ctx)
				if err != nil {
					log.Printf("[Heartbeat] Beat failed: %v", err)
					continue
				}
				log.Printf("[Heartbeat] Beat: %d tasks, %d memories, took %v",
					report.NewTasksFound, report.MemoriesWritten, report.Duration)
			}
		}
	}()
}

// Stop halts the heartbeat loop gracefully.
func (hb *Heartbeat) Stop() {
	if hb.cancel != nil {
		hb.cancel()
	}
}

// Beat executes a single heartbeat cycle:
//  1. Scan for new completed tasks since last checkpoint
//  2. For each new task: generate an episode summary from conversations
//  3. Write candidate memories to the memories table (tier=consolidated)
//  4. Update the heartbeat state checkpoint
//
// Returns a HeartbeatReport summarizing the cycle's results.
func (hb *Heartbeat) Beat(ctx context.Context) (*HeartbeatReport, error) {
	start := time.Now()
	report := &HeartbeatReport{}

	hb.mu.Lock()
	checkpoint := hb.state.LastRunAt
	hb.mu.Unlock()

	// 1. Scan for new completed tasks since last checkpoint
	taskIDs, err := hb.db.QueryCompletedTaskIDs(checkpoint)
	if err != nil {
		return report, fmt.Errorf("scan completed tasks: %w", err)
	}
	report.NewTasksFound = len(taskIDs)

	if len(taskIDs) == 0 {
		// No new tasks — update state and return
		hb.mu.Lock()
		hb.state.LastRunAt = time.Now()
		hb.state.NextIntervalSeconds = int(hb.adaptiveInterval(0).Seconds())
		hb.mu.Unlock()
		report.NextInterval = hb.adaptiveInterval(0)
		report.Duration = time.Since(start)
		return report, nil
	}

	// 2. For each new task: generate episode summary and write memory
	for _, taskID := range taskIDs {
		// Check context cancellation between tasks
		select {
		case <-ctx.Done():
			report.Duration = time.Since(start)
			return report, ctx.Err()
		default:
		}

		summary, err := hb.generateEpisodeSummary(taskID)
		if err != nil {
			log.Printf("[Heartbeat] Failed to summarize task %s: %v", taskID, err)
			report.Errors++
			continue
		}
		report.SummariesGenerated++

		// Write the summary as a consolidated memory record
		memoryID := "mem_" + generateHexID(8)
		now := time.Now()
		memRecord := db.MemoryRecord{
			ID:             memoryID,
			ProjectID:      "default",
			Type:           "lesson", // episode summary is a lesson learned
			Tier:           "consolidated",
			Content:        summary,
			Confidence:     0.7, // auto-generated summaries have moderate confidence
			Status:         "active",
			SourceTaskIDs:  []string{taskID},
			SourceEventIDs: []string{},
			AccessCount:    0,
			CreatedAt:      now,
			UpdatedAt:      now,
		}

		if err := hb.db.InsertMemory(memRecord); err != nil {
			log.Printf("[Heartbeat] Failed to write memory for task %s: %v", taskID, err)
			report.Errors++
			continue
		}
		report.MemoriesWritten++
	}

	// 3. Update heartbeat state checkpoint
	newInterval := hb.adaptiveInterval(report.NewTasksFound)
	hb.mu.Lock()
	hb.state.LastRunAt = time.Now()
	hb.state.ProcessedTaskCount += report.MemoriesWritten
	hb.state.NextIntervalSeconds = int(newInterval.Seconds())
	hb.mu.Unlock()

	report.NextInterval = newInterval
	report.Duration = time.Since(start)
	return report, nil
}

// adaptiveInterval adjusts the heartbeat interval based on activity level.
// More new tasks = shorter interval (minimum 30s).
// Fewer tasks = longer interval (maximum 10min).
// Zero tasks = use the default interval.
func (hb *Heartbeat) adaptiveInterval(newEventCount int) time.Duration {
	switch {
	case newEventCount >= 10:
		return 30 * time.Second
	case newEventCount >= 5:
		return 1 * time.Minute
	case newEventCount >= 3:
		return 2 * time.Minute
	case newEventCount >= 1:
		return 5 * time.Minute
	default:
		return 10 * time.Minute
	}
}

// generateEpisodeSummary reads the conversation history for a task and produces
// a structured summary string. This is a keyword-based extraction for now;
// Phase 6+ will use LLM-based summarization.
//
// The summary captures:
//   - User input / task goal
//   - Tools called and their results
//   - Errors encountered
//   - Final outcome
//   - Key observations
func (hb *Heartbeat) generateEpisodeSummary(taskID string) (string, error) {
	convs, err := hb.db.QueryConversationsByTask(taskID)
	if err != nil {
		return "", fmt.Errorf("query conversations for task %s: %w", taskID, err)
	}

	steps, err := hb.db.QueryStepsByTaskForMemory(taskID)
	if err != nil {
		// Steps are optional — log and continue without them
		log.Printf("[Heartbeat] Warning: failed to query steps for task %s: %v", taskID, err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task: %s\n", taskID))

	// Extract user input from the first user message
	for _, c := range convs {
		if c.Role == "user" {
			sb.WriteString(fmt.Sprintf("Input: %s\n", truncateContent(c.Content, 200)))
			break
		}
	}

	// Extract tool calls and their results from steps
	toolCount := 0
	errorCount := 0
	for _, s := range steps {
		if s.Type == "tool_call" {
			toolCount++
			if s.ToolName != "" {
				sb.WriteString(fmt.Sprintf("Tool[%s]: %s", s.ToolName, truncateContent(s.ToolOutput, 100)))
				if s.Status == "failed" {
					sb.WriteString(" (FAILED)")
					errorCount++
				}
				sb.WriteString("\n")
			}
		}
	}

	// Extract final result from the last assistant message
	var finalResult string
	for i := len(convs) - 1; i >= 0; i-- {
		if convs[i].Role == "assistant" {
			finalResult = convs[i].Content
			break
		}
	}
	sb.WriteString(fmt.Sprintf("Result: %s\n", truncateContent(finalResult, 300)))

	// Extract key observations from the conversation
	observations := extractObservations(convs)
	if len(observations) > 0 {
		sb.WriteString("Observations:\n")
		for _, obs := range observations {
			sb.WriteString(fmt.Sprintf("  - %s\n", truncateContent(obs, 150)))
		}
	}

	// Summary statistics
	sb.WriteString(fmt.Sprintf("Stats: %d tools, %d errors, %d messages\n",
		toolCount, errorCount, len(convs)))

	return sb.String(), nil
}

// extractObservations scans conversation content for lines that look like
// observations (containing tool results, error messages, or key findings).
// This is a simple keyword-based extraction for Phase 6; LLM-based extraction
// will be more accurate.
func extractObservations(convs []db.ConversationRecord) []string {
	var observations []string
	// Keywords that suggest an observation line
	obsKeywords := []string{
		"found", "result", "error", "failed", "success", "created",
		"modified", "deleted", "returned", "output", "generated",
		"completed", "analyzed", "detected", "discovered",
	}

	for _, c := range convs {
		if c.Role == "tool" || c.Role == "assistant" {
			lines := strings.Split(c.Content, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if len(line) < 10 || len(line) > 500 {
					continue
				}
				lower := strings.ToLower(line)
				for _, kw := range obsKeywords {
					if strings.Contains(lower, kw) {
						observations = append(observations, line)
						break
					}
				}
			}
		}
	}
	return observations
}

// truncateContent truncates a string to maxLen for display in summaries.
func truncateContent(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// generateHexID generates a random hex string of the given byte length.
// Used for memory IDs and other identifiers.
func generateHexID(byteLen int) string {
	bytes := make([]byte, byteLen)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}