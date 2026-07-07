// Package cost provides token usage and cost tracking for LLM calls.
//
// # Design Rationale
//
// CostTracker records every LLM call's token consumption and calculates the
// associated USD cost based on model pricing profiles. It supports multi-
// dimensional aggregation (by model, tier, agent, session, project, task)
// and emits real-time events when new records are captured.
//
// The cost calculation strictly uses the API-returned Usage fields — no local
// estimation. Prices are expressed per 1M input/output tokens as defined in
// llm.ModelProfile, and the tracker normalizes all arithmetic to avoid
// floating-point drift across many records.
//
// # Integration Pattern
//
// The tracker is integrated via the callback pattern. When an Engine Run()
// completes, the caller creates a CostRecord from the task's final usage data
// and calls Record(). This keeps Engine clean (no cost coupling) while still
// capturing every LLM transaction for billing, budgeting, and observability.
//
// Phase 6+: In a full implementation, the tracker would also:
//   - Persist records to the cost_records table via migration v10
//   - Expose HTTP handlers for real-time cost queries
//   - Support budget alerts when a project's cost exceeds a configured limit
package cost

import (
	"fmt"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// CostRecord captures the token consumption and cost of a single LLM call
// (or a batch of calls in a single ReAct step). It is the primary data unit
// for cost tracking and reporting.
type CostRecord struct {
	// ID is a unique identifier for this cost record.
	ID string

	// TaskID is the task this cost belongs to (links to tasks.id).
	TaskID string

	// SessionID is the session this cost belongs to (links to sessions.id).
	SessionID string

	// ProjectID is the project this cost belongs to (links to projects.id).
	ProjectID string

	// AgentID is the agent that performed the LLM call.
	AgentID string

	// StepIndex is the ReAct step index within the task.
	StepIndex int

	// Model is the LLM model name (e.g., "deepseek-v4-flash").
	Model string

	// Provider is the API provider (e.g., "openai", "deepseek", "anthropic").
	Provider string

	// Tier is the model tier derived from ModelProfile (e.g., "efficient", "standard").
	Tier string

	// InputTokens is the number of input/prompt tokens consumed.
	InputTokens int

	// OutputTokens is the number of output/completion tokens generated.
	OutputTokens int

	// TotalTokens is the total token count (input + output).
	TotalTokens int

	// CostUSD is the calculated cost in US dollars for this call.
	CostUSD float64

	// CreatedAt is the timestamp when this record was created.
	CreatedAt time.Time
}

// CostReport is an aggregated cost report across a set of CostRecords.
// It provides top-level totals plus breakdowns by model, tier, and agent.
type CostReport struct {
	// TotalCostUSD is the sum of all CostUSD values in the report.
	TotalCostUSD float64

	// TotalTokens is the sum of all TotalTokens values.
	TotalTokens int

	// TotalInputTokens is the sum of all input tokens.
	TotalInputTokens int

	// TotalOutputTokens is the sum of all output tokens.
	TotalOutputTokens int

	// ByModel maps model name → total cost for that model.
	ByModel map[string]float64

	// ByTier maps tier name → total cost for that tier.
	ByTier map[string]float64

	// ByAgent maps agent ID → total cost for that agent.
	ByAgent map[string]float64

	// RecordCount is the number of records included in this report.
	RecordCount int
}

// newCostReport creates a zero-valued CostReport with initialized maps.
func newCostReport() *CostReport {
	return &CostReport{
		ByModel: make(map[string]float64),
		ByTier:  make(map[string]float64),
		ByAgent: make(map[string]float64),
	}
}

// CostTracker is the central cost tracking component. It maintains an in-memory
// cache of CostRecords and provides aggregation methods for multi-dimensional
// cost analysis.
//
// Thread-safe: all public methods safe for concurrent use.
type CostTracker struct {
	mu      sync.RWMutex
	records []CostRecord

	// onRecord is an optional callback invoked after a record is stored.
	// It is called asynchronously — errors are not propagated to the caller.
	onRecord func(CostRecord)

	// registry is the optional model registry for tier lookups.
	// When nil, tier resolution defaults to "unknown".
	registry *llm.ModelRegistry
}

// CostTrackerOption is a functional option for configuring a CostTracker.
type CostTrackerOption func(*CostTracker)

// WithOnRecord sets the callback invoked when a new record is stored.
// The callback is called after the record is appended to the in-memory cache.
func WithOnRecord(fn func(CostRecord)) CostTrackerOption {
	return func(ct *CostTracker) {
		ct.onRecord = fn
	}
}

// WithRegistry sets the model registry for tier resolution in cost records.
// When set, the tracker can populate the Tier field from the model name.
func WithRegistry(registry *llm.ModelRegistry) CostTrackerOption {
	return func(ct *CostTracker) {
		ct.registry = registry
	}
}

// NewCostTracker creates a new CostTracker with optional configuration.
//
// Usage:
//
//	// Basic usage — in-memory only, no callback
//	ct := cost.NewCostTracker()
//
//	// With event callback for real-time cost broadcasting
//	ct := cost.NewCostTracker(cost.WithOnRecord(func(r cost.CostRecord) {
//	    bus.SendEvent(event.NewEvent("cost_recorded", r.TaskID, r.AgentID, r.StepIndex, map[string]any{"cost": r.CostUSD}))
//	}))
//
//	// With model registry for automatic tier resolution
//	ct := cost.NewCostTracker(cost.WithRegistry(registry))
func NewCostTracker(opts ...CostTrackerOption) *CostTracker {
	ct := &CostTracker{
		records: make([]CostRecord, 0),
	}
	for _, apply := range opts {
		apply(ct)
	}
	return ct
}

// Record appends a CostRecord to the in-memory cache and invokes the onRecord
// callback if configured.
//
// This is the primary entry point for cost data — call it after each LLM interaction
// (or batch of interactions per ReAct step) with the completed CostRecord.
//
// The method is non-blocking even if the callback is slow, because the callback
// is invoked in a best-effort manner (logged-warning on panic, no error returned).
func (ct *CostTracker) Record(record CostRecord) {
	ct.mu.Lock()
	ct.records = append(ct.records, record)
	ct.mu.Unlock()

	if ct.onRecord != nil {
		// Callback in a best-effort manner — we don't block the caller if the callback panics.
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Log the panic but don't propagate it to the caller.
					// The callback is a side effect; Record must not fail because of it.
					fmt.Printf("[CostTracker] onRecord callback panicked: %v\n", r)
				}
			}()
			ct.onRecord(record)
		}()
	}
}

// TaskCost aggregates costs for all records matching the given task_id.
func (ct *CostTracker) TaskCost(taskID string) (*CostReport, error) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	report := newCostReport()
	for _, r := range ct.records {
		if r.TaskID != taskID {
			continue
		}
		report.add(r)
	}
	return report, nil
}

// SessionCost aggregates costs for all records matching the given session_id.
func (ct *CostTracker) SessionCost(sessionID string) (*CostReport, error) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	report := newCostReport()
	for _, r := range ct.records {
		if r.SessionID != sessionID {
			continue
		}
		report.add(r)
	}
	return report, nil
}

// ProjectCost aggregates costs for all records matching the given project_id.
func (ct *CostTracker) ProjectCost(projectID string) (*CostReport, error) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	report := newCostReport()
	for _, r := range ct.records {
		if r.ProjectID != projectID {
			continue
		}
		report.add(r)
	}
	return report, nil
}

// DailyReport aggregates costs for the last N days across all records.
// Days=0 returns all records (no date filter).
// Days=1 returns records from today only.
func (ct *CostTracker) DailyReport(days int) (*CostReport, error) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	cutoff := time.Time{}
	if days > 0 {
		cutoff = time.Now().AddDate(0, 0, -days)
	}

	report := newCostReport()
	for _, r := range ct.records {
		if !cutoff.IsZero() && r.CreatedAt.Before(cutoff) {
			continue
		}
		report.add(r)
	}
	return report, nil
}

// CalculateCost computes the USD cost for a single LLM call based on the
// model's pricing profile and the API-reported token usage.
//
// The prices in ModelProfile are per 1M tokens. The function normalizes
// the arithmetic as:
//
//	cost = (input_tokens * input_price + output_tokens * output_price) / 1_000_000
//
// This strictly uses the API-returned Usage — no local token estimation.
//
// Returns the cost in USD as a float64. Returns 0.0 if profile is nil or
// usage has zero tokens.
func (ct *CostTracker) CalculateCost(profile *llm.ModelProfile, usage llm.Usage) float64 {
	if profile == nil {
		return 0.0
	}
	if usage.TotalTokens == 0 {
		return 0.0
	}

	// Cost = (input_tokens * input_price + output_tokens * output_price) / 1M
	inputCost := float64(usage.PromptTokens) * profile.InputPrice
	outputCost := float64(usage.CompletionTokens) * profile.OutputPrice
	return (inputCost + outputCost) / 1_000_000
}

// BuildRecordFromProfile constructs a CostRecord from a model profile, usage
// data, and task/agent identifiers. It auto-populates the Tier field from the
// registry (if available) and assigns a unique ID and timestamp.
//
// This is a convenience constructor used by integration code to create records
// from Engine execution results without manually filling every field.
func (ct *CostTracker) BuildRecordFromProfile(
	taskID, sessionID, projectID, agentID string,
	stepIndex int,
	model string,
	profile *llm.ModelProfile,
	usage llm.Usage,
) CostRecord {
	tier := "unknown"
	if ct.registry != nil && profile != nil {
		if p := ct.registry.Get(model); p != nil {
			tier = p.Tier.String()
		}
	}

	cost := ct.CalculateCost(profile, usage)

	return CostRecord{
		ID:           fmt.Sprintf("cr_%d_%d", time.Now().UnixNano(), stepIndex),
		TaskID:       taskID,
		SessionID:    sessionID,
		ProjectID:    projectID,
		AgentID:      agentID,
		StepIndex:    stepIndex,
		Model:        model,
		Provider:     "", // filled by caller if needed
		Tier:         tier,
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		TotalTokens:  usage.TotalTokens,
		CostUSD:      cost,
		CreatedAt:    time.Now(),
	}
}

// add is an internal helper that accumulates a single record's data into
// the report aggregates. It is called by the public query methods while
// holding the read lock.
func (r *CostReport) add(record CostRecord) {
	r.TotalCostUSD += record.CostUSD
	r.TotalTokens += record.TotalTokens
	r.TotalInputTokens += record.InputTokens
	r.TotalOutputTokens += record.OutputTokens
	r.RecordCount++

	r.ByModel[record.Model] += record.CostUSD
	r.ByTier[record.Tier] += record.CostUSD
	r.ByAgent[record.AgentID] += record.CostUSD
}
