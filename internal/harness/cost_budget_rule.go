// Package harness 提供 PolicyGate 的策略规则。
//
// CostBudgetRule 是一个 PolicyRule，当累计成本超过 TaskContract 的成本预算时拦截
// tool call。它实现 PolicyRule 接口，并与 PolicyChain 集成。
package harness

import (
	"fmt"
	"sync"
)

// ErrBlockedByCostBudget 在 tool call 会超出成本预算时返回。
// （本 package 中已存在 ErrBlockedByPolicy，用于所有策略拦截。）

// CostBudgetRule 在累计成本超过 TaskContract 的 CostBudgetUSD 阈值时拦截 tool call。
// 它通过 Engine 显式调用 SetCost 跟踪成本 —— Engine 根据 token 使用量与模型定价计算每次
// 调用成本。
//
// 设计理由：成本是硬性经济约束。Engine 在每次 LLM 调用后计算实际成本（API 返回 usage ×
// 模型定价）并通过 SetCost 上报。CostBudgetRule 在预算耗尽时拦截后续 tool call，防止
// agent 消耗超出分配的资源。
//
// 当 CostBudgetUSD 为 0（默认）时，rule 永不拦截（无限制预算）。
type CostBudgetRule struct {
	currentCostUSD float64 // 累计 USD 成本
	mu             sync.Mutex
}

// NewCostBudgetRule 创建一个累计成本为 0 的 CostBudgetRule。
func NewCostBudgetRule() *CostBudgetRule {
	return &CostBudgetRule{}
}

// Name 返回用于日志与错误信息的 rule 名称。
func (r *CostBudgetRule) Name() string {
	return "CostBudgetRule"
}

// SetCost 更新累计成本。由 Engine 在每次 LLM 调用后以该次调用计算的成本（usage × 模型定价）
// 调用。成本是累加的 —— 每次调用成本加到运行总额上。
func (r *CostBudgetRule) SetCost(cost float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.currentCostUSD += cost
}

// Check 评估 tool call 是否会超出成本预算。若 CostBudgetUSD > 0 且
// currentCostUSD >= CostBudgetUSD，则拦截此次调用。否则允许。
func (r *CostBudgetRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	if contract.CostBudgetUSD <= 0 {
		return input, nil // 无限制预算
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentCostUSD >= contract.CostBudgetUSD {
		return input, &ErrBlockedByPolicy{
			Rule:   r.Name(),
			Reason: fmt.Sprintf("cost budget exceeded: $%.4f/$%.2f USD used", r.currentCostUSD, contract.CostBudgetUSD),
			Tool:   toolName,
		}
	}

	return input, nil
}

// Reset 清除累计成本，将其重置为 0。用于测试隔离或在多个任务间复用 rule。
func (r *CostBudgetRule) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.currentCostUSD = 0
}
