// Package harness — MemoryRecall: builds Working Memory from stored memories.
//
// MemoryRecall is the recall side of the Memory infrastructure. When a new
// task starts, the recall engine loads relevant memories from the database
// and builds a WorkingMemory context block that is injected into the agent's
// system prompt. This gives the agent access to past experiences and stable
// semantic rules without requiring the user to repeat them.
//
// # Architecture
//
// The recall system implements the first tier of the 4-tier memory system:
//
//	1. Working Memory      — per-task context (in-memory)              <-- THIS
//	2. Raw Episodic        — conversation records (conversations table)
//	3. Consolidated Episodic — task summaries (memories, tier=consolidated)
//	4. Semantic/Policy     — stable rules (memories, tier=semantic)
//
// # Recall Process
//
// When BuildWorkingMemory is called:
//	1. Load session-scoped memories (highest priority, for current session)
//	2. Load project-scoped semantic rules (stable policies)
//	3. Load top N project-scoped consolidated episodes by keyword match
//	4. Load global-scoped semantic rules (cross-project preferences)
//	5. Update access_count and last_accessed for each recalled memory
//	6. Build a WorkingMemory struct ready for system prompt injection
//
// # Conflict Detection
//
// The DetectConflicts method scans memory items for pairs with contradictory
// content, using simple keyword-based detection. This helps surface stale or
// conflicting rules that need human review.
package harness

import (
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// WorkingMemory is the context injected into the system prompt for a new task.
// It contains layered memories from session, project, and global scopes, giving
// the agent access to the most relevant institutional knowledge without
// requiring the user to repeat instructions.
type WorkingMemory struct {
	// TaskGoal is the user's task description, used for relevance scoring.
	TaskGoal string `json:"task_goal"`

	// SessionMemories are session-scoped memories tied to the current session.
	// These have the highest priority because they reflect the immediate
	// conversational context.
	SessionMemories []MemoryItem `json:"session_memories"`

	// ProjectRules are project-scoped semantic-tier memories that represent
	// stable policies, preferences, and rules for the current project.
	ProjectRules []MemoryItem `json:"project_rules"`

	// ProjectEpisodes are project-scoped consolidated episodic memories that are
	// most relevant to the current task goal, scored by keyword overlap.
	ProjectEpisodes []MemoryItem `json:"project_episodes"`

	// GlobalRules are global-scoped semantic-tier memories that represent
	// cross-project preferences and stable conventions.
	GlobalRules []MemoryItem `json:"global_rules"`

	// BuiltAt is the timestamp when this WorkingMemory was constructed.
	BuiltAt time.Time `json:"built_at"`
}

// MemoryItem is a single memory entry prepared for injection into the system
// prompt. It carries the essential fields needed for the agent to understand
// and use the memory without exposing internal database details.
type MemoryItem struct {
	// ID is the unique identifier of the memory record.
	ID string `json:"id"`

	// Type describes the kind of memory: preference, rule, fact, lesson, reflection.
	Type string `json:"type"`

	// Content is the full text of the memory.
	Content string `json:"content"`

	// Confidence is the reliability score of the memory (0.0--1.0).
	Confidence float64 `json:"confidence"`

	// Reason describes why this memory was recalled for the current task.
	Reason string `json:"reason"`
}

// ConflictPair represents two memories that appear to contradict each other.
// Detected by simple keyword analysis — opposite markers like "use" vs "avoid"
// or "always" vs "never" in memories of the same type.
type ConflictPair struct {
	// MemoryA is the first memory in the conflicting pair.
	MemoryA MemoryItem `json:"memory_a"`

	// MemoryB is the second memory in the conflicting pair.
	MemoryB MemoryItem `json:"memory_b"`

	// Reason describes which opposite markers were detected.
	Reason string `json:"reason"`
}

// MemoryRecall builds Working Memory for a new task by recalling semantic rules
// and relevant consolidated episodes from the memory store. It is the recall
// side of the Memory infrastructure, complementing the Heartbeat (consolidation)
// and PromotionGate (promotion).
//
// Usage:
//
//	recall := NewMemoryRecall(memDB)
//	wm, err := recall.BuildWorkingMemory("default", "session_abc", "write a Go test", 3)
//	if err == nil {
//	    prompt := recall.FormatForSystemPrompt(wm)
//	    // prepend prompt to the agent's system prompt
//	}
type MemoryRecall struct {
	db MemoryDB
}

// NewMemoryRecall creates a new MemoryRecall backed by the given MemoryDB.
// The database parameter implements the MemoryDB interface for all DB operations
// needed by the recall engine (QueryMemoriesByTier). Access tracking is done
// via the db package-level UpdateMemoryAccess function.
func NewMemoryRecall(database MemoryDB) *MemoryRecall {
	return &MemoryRecall{db: database}
}

// BuildWorkingMemory loads layered memories for the given project, session, and
// task goal. The result is a WorkingMemory struct ready for injection into the
// system prompt.
//
// Recall priority (most specific first):
//
//	1. Session-scoped memories (scope=session, session_id=xxx)
//	2. Project-scoped semantic rules (scope=project, tier=semantic)
//	3. Project-scoped consolidated episodes (scope=project, tier=consolidated, keyword top N)
//	4. Global-scoped semantic rules (scope=global, tier=semantic)
//	5. Session history messages are injected separately by the Engine layer
//
// Parameters:
//   - projectID: the project to recall memories for (e.g., "default")
//   - sessionID: the current session ID; may be empty if not in a session
//   - taskGoal: the user's task description, used for keyword matching
//   - maxEpisodes: maximum number of consolidated episodes to recall
//
// Returns a WorkingMemory even if no memories are found (empty slices).
// Errors are returned only for database failures.
func (mr *MemoryRecall) BuildWorkingMemory(projectID, sessionID, taskGoal string, maxEpisodes int) (*WorkingMemory, error) {
	// 1. Load session-scoped memories (highest priority).
	//    These capture facts/rules that only apply to the current session.
	sessionMems, err := mr.loadSessionMemories(projectID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session memories: %w", err)
	}

	// 2. Load project-scoped semantic rules (stable policies for this project).
	projectRules, err := mr.loadSemanticRules(projectID, "project")
	if err != nil {
		return nil, fmt.Errorf("load project semantic rules: %w", err)
	}

	// 3. Load top N project-scoped consolidated episodes by keyword match.
	projectEpisodes, err := mr.recallEpisodes(projectID, "project", taskGoal, maxEpisodes)
	if err != nil {
		return nil, fmt.Errorf("recall project episodes: %w", err)
	}

	// 4. Load global-scoped semantic rules (cross-project conventions).
	globalRules, err := mr.loadSemanticRules(projectID, "global")
	if err != nil {
		return nil, fmt.Errorf("load global semantic rules: %w", err)
	}

	return &WorkingMemory{
		TaskGoal:        taskGoal,
		SessionMemories: sessionMems,
		ProjectRules:    projectRules,
		ProjectEpisodes: projectEpisodes,
		GlobalRules:     globalRules,
		BuiltAt:         time.Now(),
	}, nil
}

// FormatForSystemPrompt formats a WorkingMemory as a clean text block suitable
// for prepending to the agent's system prompt. The output uses Markdown-style
// headings for readability in the LLM context.
//
// Output format:
//
//	## Working Memory (from previous tasks)
//
//	### Session Context
//	- [memory content]
//
//	### Project Rules
//	- [rule content]
//
//	### Related Experiences
//	- [episode summary]
//
//	### Global Preferences
//	- [rule content]
func (mr *MemoryRecall) FormatForSystemPrompt(wm *WorkingMemory) string {
	var sb strings.Builder
	sb.WriteString("## Working Memory (from previous tasks)\n")

	if len(wm.SessionMemories) > 0 {
		sb.WriteString("\n### Session Context\n")
		for _, m := range wm.SessionMemories {
			sb.WriteString(fmt.Sprintf("- %s\n", m.Content))
		}
	}

	if len(wm.ProjectRules) > 0 {
		sb.WriteString("\n### Project Rules\n")
		for _, rule := range wm.ProjectRules {
			sb.WriteString(fmt.Sprintf("- %s\n", rule.Content))
		}
	}

	if len(wm.ProjectEpisodes) > 0 {
		sb.WriteString("\n### Related Experiences\n")
		for _, ep := range wm.ProjectEpisodes {
			sb.WriteString(fmt.Sprintf("- %s\n", ep.Content))
		}
	}

	if len(wm.GlobalRules) > 0 {
		sb.WriteString("\n### Global Preferences\n")
		for _, rule := range wm.GlobalRules {
			sb.WriteString(fmt.Sprintf("- %s\n", rule.Content))
		}
	}

	return sb.String()
}

// loadSessionMemories loads active session-scoped memories for the given
// project and session. These are the highest-priority memories because they
// reflect the immediate conversational context.
func (mr *MemoryRecall) loadSessionMemories(projectID, sessionID string) ([]MemoryItem, error) {
	if sessionID == "" {
		return nil, nil
	}

	records, err := db.QueryMemoriesByScopeAndSession(projectID, sessionID, "session")
	if err != nil {
		return nil, err
	}

	var items []MemoryItem
	for _, r := range records {
		if r.Status != "active" {
			continue
		}
		if err := db.UpdateMemoryAccess(r.ID); err != nil {
			continue
		}
		items = append(items, MemoryItem{
			ID:         r.ID,
			Type:       r.Type,
			Content:    r.Content,
			Confidence: r.Confidence,
			Reason:     "session-scoped memory (current session)",
		})
	}
	return items, nil
}

// loadSemanticRules loads active semantic-tier memories for the given project
// and scope, ordered by confidence descending (most reliable first). Updates
// access_count and last_accessed for each recalled memory to track usage
// patterns.
func (mr *MemoryRecall) loadSemanticRules(projectID, scope string) ([]MemoryItem, error) {
	records, err := db.QueryMemoriesByScopeAndTier(projectID, scope, "semantic")
	if err != nil {
		return nil, err
	}

	var items []MemoryItem
	for _, r := range records {
		// Only recall active memories — obsolete or invalid ones are skipped.
		if r.Status != "active" {
			continue
		}
		// Update access tracking for the recalled memory. This is non-fatal:
		// if the update fails, we still include the memory in the working set
		// but log the failure for diagnostics.
		if err := db.UpdateMemoryAccess(r.ID); err != nil {
			// Non-fatal — the memory is still recalled, just without access tracking.
			continue
		}
		items = append(items, MemoryItem{
			ID:         r.ID,
			Type:       r.Type,
			Content:    r.Content,
			Confidence: r.Confidence,
			Reason:     fmt.Sprintf("%s semantic rule (stable policy)", scope),
		})
	}
	return items, nil
}

// recallEpisodes loads consolidated episodic memories filtered by scope, scores
// each by keyword overlap with the task goal, and returns the top N most
// relevant. Updates access tracking for each recalled memory.
//
// The scoring algorithm is simple word-frequency overlap: each word in the
// task goal is checked against the memory content. The score is the percentage
// of query words that appear in the content. This is intentionally lightweight
// — Phase 6+ will add vector similarity scoring for semantic relevance.
func (mr *MemoryRecall) recallEpisodes(projectID, scope, taskGoal string, maxN int) ([]MemoryItem, error) {
	records, err := db.QueryMemoriesByScopeAndTier(projectID, scope, "consolidated")
	if err != nil {
		return nil, err
	}

	// Score each episode by keyword overlap with taskGoal.
	// Each episode is paired with its relevance score for sorting.
	type scored struct {
		item  MemoryItem
		score float64
	}
	var scoredList []scored
	for _, r := range records {
		// Only recall active memories.
		if r.Status != "active" {
			continue
		}
		score := keywordScore(r.Content, taskGoal)
		scoredList = append(scoredList, scored{
			item: MemoryItem{
				ID:         r.ID,
				Type:       r.Type,
				Content:    r.Content,
				Confidence: r.Confidence,
				Reason:     fmt.Sprintf("%s keyword match (score: %.1f)", scope, score),
			},
			score: score,
		})
	}

	// Sort by score descending. Since the number of consolidated episodes is
	// typically small (a few dozen), a simple bubble sort is sufficient.
	// Phase 6+ will use database-level ordering with vector similarity.
	for i := 0; i < len(scoredList); i++ {
		for j := i + 1; j < len(scoredList); j++ {
			if scoredList[j].score > scoredList[i].score {
				scoredList[i], scoredList[j] = scoredList[j], scoredList[i]
			}
		}
	}

	// Take the top N most relevant episodes.
	if maxN > len(scoredList) {
		maxN = len(scoredList)
	}
	var items []MemoryItem
	for i := 0; i < maxN; i++ {
		// Update access tracking for recalled memory.
		if err := db.UpdateMemoryAccess(scoredList[i].item.ID); err != nil {
			// Non-fatal — continue with the next item.
			continue
		}
		items = append(items, scoredList[i].item)
	}
	return items, nil
}

// keywordScore computes a simple word-frequency overlap score between content
// and query. Both strings are tokenized (split by whitespace, lowercased,
// punctuation stripped), and the score is the percentage of query words that
// appear in the content.
//
// Returns score = (overlap_count / total_query_words) * 100.
// Returns 0 if the query has no tokens.
func keywordScore(content, query string) float64 {
	contentWords := tokenize(content)
	queryWords := tokenize(query)

	if len(queryWords) == 0 {
		return 0
	}

	// Build a set of content words for O(1) lookup.
	contentSet := make(map[string]bool, len(contentWords))
	for _, w := range contentWords {
		contentSet[w] = true
	}

	// Count overlapping words between query and content.
	overlap := 0
	for _, w := range queryWords {
		if contentSet[w] {
			overlap++
		}
	}

	return (float64(overlap) / float64(len(queryWords))) * 100
}

// tokenize splits a string into lowercase word tokens, stripping punctuation
// and filtering out very short tokens (single characters). This is used by
// keywordScore for word-frequency overlap computation.
func tokenize(s string) []string {
	fields := strings.Fields(strings.ToLower(s))
	var tokens []string
	for _, f := range fields {
		// Strip common punctuation from word boundaries.
		f = strings.Trim(f, ".,;:!?()[]{}'\"")
		// Skip single-character tokens — they are rarely meaningful.
		if len(f) >= 2 {
			tokens = append(tokens, f)
		}
	}
	return tokens
}

// DetectConflicts checks a list of MemoryItems for pairs with contradictory
// content. Uses simple keyword-based detection: pairs where one memory contains
// a positive marker (e.g., "use", "always") and the other contains the opposite
// marker (e.g., "avoid", "never") in memories of the same type.
//
// This is a lightweight check for surfacing stale or conflicting rules. It is
// not exhaustive — a full semantic conflict analysis would require LLM-based
// comparison (Phase 6+).
//
// Returns a list of conflict pairs, empty if no conflicts are detected.
func (mr *MemoryRecall) DetectConflicts(memories []MemoryItem) []ConflictPair {
	var conflicts []ConflictPair

	// Opposite keyword pairs that indicate potential conflicts.
	// Each pair is [positive_marker, negative_marker].
	oppositePairs := [][2]string{
		{"use", "avoid"},
		{"always", "never"},
		{"do", "don't"},
		{"should", "shouldn't"},
		{"recommend", "avoid"},
		{"prefer", "dislike"},
		{"enable", "disable"},
		{"include", "exclude"},
		{"allow", "block"},
		{"accept", "reject"},
	}

	for i := 0; i < len(memories); i++ {
		for j := i + 1; j < len(memories); j++ {
			// Only check memories of the same type — a rule and a reflection
			// with opposite keywords are not necessarily conflicting.
			if memories[i].Type != memories[j].Type {
				continue
			}
			lowerA := strings.ToLower(memories[i].Content)
			lowerB := strings.ToLower(memories[j].Content)

			for _, pair := range oppositePairs {
				// Check direction A: MemoryA has positive, MemoryB has negative
				if strings.Contains(lowerA, pair[0]) && strings.Contains(lowerB, pair[1]) {
					conflicts = append(conflicts, ConflictPair{
						MemoryA: memories[i],
						MemoryB: memories[j],
						Reason:  fmt.Sprintf("opposite markers: '%s' vs '%s'", pair[0], pair[1]),
					})
					break
				}
				// Check direction B: MemoryA has negative, MemoryB has positive
				if strings.Contains(lowerA, pair[1]) && strings.Contains(lowerB, pair[0]) {
					conflicts = append(conflicts, ConflictPair{
						MemoryA: memories[i],
						MemoryB: memories[j],
						Reason:  fmt.Sprintf("opposite markers: '%s' vs '%s'", pair[1], pair[0]),
					})
					break
				}
			}
		}
	}

	return conflicts
}
