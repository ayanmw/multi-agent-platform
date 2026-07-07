// Package harness provides policy rules for the PolicyGate.
//
// CostBudgetRule is a PolicyRule that blocks tool calls when the cumulative
// cost exceeds the TaskContract's cost budget. It implements the PolicyRule
// interface and integrates with the PolicyChain.
package harness

import (
	"fmt"
	"sync"
)

// ErrBlockedByCostBudget is returned when a tool call would exceed the cost budget.
// (ErrBlockedByPolicy already exists in this package and is used for all policy blocks.)

// CostBudgetRule blocks tool calls when the cumulative cost exceeds the
// TaskContract's CostBudgetUSD threshold. It tracks cost via explicit SetCost
// calls from the Engine, which computes per-call cost from token usage and
// model pricing.
//
// Design rationale: Cost is a hard economic constraint. The Engine computes
// the actual cost (from API-returned usage × model pricing) after each LLM
// call and reports it via SetCost. The CostBudgetRule then blocks subsequent
// tool calls when the budget is exhausted, preventing the agent from consuming
// more resources than allocated.
//
// When CostBudgetUSD is 0 (default), the rule never blocks (unlimited budget).
type CostBudgetRule struct {
	currentCostUSD float64 // cumulative cost in USD
	mu             sync.Mutex
}

// NewCostBudgetRule creates a new CostBudgetRule with zero accumulated cost.
func NewCostBudgetRule() *CostBudgetRule {
	return &CostBudgetRule{}
}

// Name returns the rule name for logging and error messages.
func (r *CostBudgetRule) Name() string {
	return "CostBudgetRule"
}

// SetCost updates the cumulative cost. Called by the Engine after each LLM
// call with the computed cost for that call (usage × model pricing).
// The cost is additive — each call's cost is added to the running total.
func (r *CostBudgetRule) SetCost(cost float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.currentCostUSD += cost
}

// Check evaluates whether the tool call would exceed the cost budget.
// If CostBudgetUSD > 0 and currentCostUSD >= CostBudgetUSD, the call is blocked.
// Otherwise, the call is allowed.
func (r *CostBudgetRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	if contract.CostBudgetUSD <= 0 {
		return input, nil // unlimited budget
	}

	r.mu.Lock()
	cost := r.currentCostUSD
	r.mu.Unlock()

	if cost >= contract.CostBudgetUSD {
		return input, &ErrBlockedByPolicy{
			Rule:   r.Name(),
			Reason: fmt.Sprintf("cost budget exceeded: $%.4f/$%.2f USD used", cost, contract.CostBudgetUSD),
			Tool:   toolName,
		}
	}

	return input, nil
}

// Reset clears the accumulated cost, setting it back to zero.
// This is useful for test isolation or when reusing a rule across tasks.
func (r *CostBudgetRule) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.currentCostUSD = 0
}
