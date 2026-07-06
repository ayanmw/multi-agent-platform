// Package harness — PromotionGate: promotes consolidated memories to semantic tier.
//
// The PromotionGate evaluates candidate memories in the consolidated tier and
// promotes them to the semantic tier when they meet promotion criteria. This
// implements the bridge between Consolidated Episodic and Semantic/Policy memory.
//
// # Three Promotion Channels
//
// Each candidate memory is checked against three promotion channels:
//
//  1. repeated_across_sessions — the memory's source_task_ids contain at least 2
//     distinct task IDs, meaning the same experience was observed in independent
//     tasks. This is the strongest signal of a reliable pattern.
//
//  2. tool_failure_evidence — the memory's source_event_ids reference an event
//     where a tool call failed (status=failed). Explicitly documented failures
//     are valuable lessons that should be promoted for future avoidance.
//
//  3. explicit_user_instruction — the memory's content contains a durable
//     instruction marker (e.g., "always", "以后都", "rule:", "preference:").
//     These are user-explicit directives that should be persisted as semantic
//     rules.
//
// # Promotion Lifecycle
//
//   - Consolidated (candidate) → Semantic (promoted) via PromotionGate.PromoteCandidates
//   - Promotion is idempotent: already-promoted memories are skipped
//   - Promotion reason is recorded in the memory record for auditability
//   - The gate returns a report with counts of promoted and skipped memories
package harness

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// PromotionGate evaluates consolidated memory candidates and promotes qualifying
// records to the semantic tier. It is typically called periodically (e.g., after
// the heartbeat runs) or on-demand via the /api/memories/promote endpoint.
type PromotionGate struct {
	db MemoryDB
}

// NewPromotionGate creates a new PromotionGate backed by the given MemoryDB.
func NewPromotionGate(database MemoryDB) *PromotionGate {
	return &PromotionGate{db: database}
}

// PromotionReport summarizes the results of a promotion cycle.
type PromotionReport struct {
	// TotalCandidates is the number of consolidated memories evaluated.
	TotalCandidates int `json:"total_candidates"`

	// PromotedCount is the number of memories promoted to semantic tier.
	PromotedCount int `json:"promoted_count"`

	// SkippedCount is the number of memories that did not meet any promotion criteria.
	SkippedCount int `json:"skipped_count"`

	// Details breaks down each promoted memory with its promotion reason.
	Details []PromotionDetail `json:"details"`

	// Duration is the time taken for this promotion cycle.
	Duration time.Duration `json:"duration_ms"`
}

// PromotionDetail records the promotion of a single memory, including the
// channel that triggered the promotion.
type PromotionDetail struct {
	MemoryID        string `json:"memory_id"`
	Type            string `json:"type"`
	ContentSnippet  string `json:"content_snippet"`
	PromotionReason string `json:"promotion_reason"`
	// Channel is the specific promotion channel that triggered: "repeated_across_sessions",
	// "tool_failure_evidence", or "explicit_user_instruction".
	Channel string `json:"channel"`
}

// PromoteCandidates evaluates all consolidated memories for the given project
// and promotes qualifying records to the semantic tier.
//
// Each candidate is checked against the three promotion channels; if any
// channel passes, the memory is promoted. The promotion reason is recorded
// and the memory's tier is updated to "semantic".
//
// Already-promoted (tier=semantic) memories are skipped.
func (pg *PromotionGate) PromoteCandidates(projectID string) (*PromotionReport, error) {
	start := time.Now()
	report := &PromotionReport{}

	// Fetch all consolidated memories for the project
	candidates, err := pg.db.QueryMemoriesByTier(projectID, "consolidated")
	if err != nil {
		return report, fmt.Errorf("query consolidated memories: %w", err)
	}
	report.TotalCandidates = len(candidates)

	if len(candidates) == 0 {
		report.Duration = time.Since(start)
		return report, nil
	}

	for _, mem := range candidates {
		// Check each promotion channel
		reason, channel := pg.evaluateCandidate(mem)
		if reason == "" {
			report.SkippedCount++
			continue
		}

		// Promote to semantic tier
		if err := pg.db.UpdateMemoryTier(mem.ID, "semantic", reason); err != nil {
			log.Printf("[PromotionGate] Failed to promote memory %s: %v", mem.ID, err)
			report.SkippedCount++
			continue
		}

		report.PromotedCount++
		report.Details = append(report.Details, PromotionDetail{
			MemoryID:        mem.ID,
			Type:            mem.Type,
			ContentSnippet:  truncateContent(mem.Content, 100),
			PromotionReason: reason,
			Channel:         channel,
		})

		log.Printf("[PromotionGate] Promoted %s to semantic: %s (channel=%s)", mem.ID, reason, channel)
	}

	report.Duration = time.Since(start)
	return report, nil
}

// evaluateCandidate checks a single memory against all three promotion channels.
// Returns the promotion reason and channel if any channel passes, or empty strings
// if the memory should not be promoted.
func (pg *PromotionGate) evaluateCandidate(mem db.MemoryRecord) (reason string, channel string) {
	// Channel 1: repeated_across_sessions
	// Count distinct source_task_ids; if >= 2, this experience has been observed
	// in multiple independent tasks — it is a reliable pattern.
	if len(mem.SourceTaskIDs) >= 2 {
		// Deduplicate task IDs (in case of duplicates in the array)
		unique := make(map[string]bool)
		for _, tid := range mem.SourceTaskIDs {
			unique[tid] = true
		}
		if len(unique) >= 2 {
			reason = fmt.Sprintf("repeated_across_sessions: observed in %d distinct tasks", len(unique))
			channel = "repeated_across_sessions"
			return
		}
	}

	// Channel 2: tool_failure_evidence
	// Check if any source_event_id references a tool call that failed.
	// This is a lightweight check based on the memory's content and metadata;
	// full event-based verification requires querying the events table (Phase 6+).
	if pg.hasFailureEvidence(mem) {
		reason = "tool_failure_evidence: associated tool call failed"
		channel = "tool_failure_evidence"
		return
	}

	// Channel 3: explicit_user_instruction
	// Check if the content contains durable instruction markers.
	if isExplicitInstruction(mem.Content) {
		reason = "explicit_user_instruction: content contains durable instruction marker"
		channel = "explicit_user_instruction"
		return
	}

	return "", ""
}

// hasFailureEvidence checks whether the memory's content or source data indicates
// a tool failure. This is a lightweight check based on content analysis; Phase 6+
// will add cross-referencing with the steps table for definitive failure detection.
func (pg *PromotionGate) hasFailureEvidence(mem db.MemoryRecord) bool {
	// Check content for failure indicators
	lower := strings.ToLower(mem.Content)
	failureMarkers := []string{
		"failed", "error", "exception", "timeout", "rejected",
		"blocked", "denied", "crashed", "aborted", "permission denied",
		"(FAILED)", "tool_call_failed",
	}
	for _, marker := range failureMarkers {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

// isExplicitInstruction checks whether the content contains markers that indicate
// a user explicitly stated a durable instruction, preference, or rule.
//
// Markers (English and Chinese):
//   - "always", "always do", "never", "never do"
//   - "rule:", "preference:", "policy:", "convention:"
//   - "以后都", "永远", "总是", "每次", "从不"
//   - "记住", "别忘了", "规定"
func isExplicitInstruction(content string) bool {
	lower := strings.ToLower(content)

	// English markers
	enMarkers := []string{
		"always", "never", "every time", "each time",
		"rule:", "preference:", "policy:", "convention:",
		"remember:", "don't forget", "do not forget",
		"from now on", "going forward", "henceforth",
	}
	for _, marker := range enMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	// Chinese markers
	zhMarkers := []string{
		"以后都", "永远", "总是", "每次", "从不",
		"记住", "别忘了", "规定", "规则", "偏好",
		"以后", "以后每个", "从此", "今后",
	}
	for _, marker := range zhMarkers {
		if strings.Contains(content, marker) {
			return true
		}
	}

	return false
}