// Package cost 提供 LLM 调用的 token 用量与 cost 跟踪。
//
// # 设计理由
//
// CostTracker 记录每一次 LLM 调用的 token 消耗，并基于模型 pricing profile 计算对应的
// USD cost。它支持多维聚合（按 model、tier、agent、session、project、task），
// 并在捕获新记录时发出实时事件。
//
// cost 计算严格使用 API 返回的 Usage 字段——不做任何本地估算。价格以每 1M
// input/output token 的形式表达，定义于 llm.ModelProfile。Cost 以 float64 USD 形式
// 按全精度存储；截断由展示层负责（前端 toFixed(3)）。CostCents int64 字段仅作为
// 派生值（= round(CostUSD*100)）保留，用于与 legacy 消费者（如 cost_cents_total
// Prometheus counter）保持向后兼容。
//
// 理由：单次 LLM 调用的 cost 处于 $0.0001 量级，因此整数-cent 算术会把小对话截断
// 为 $0，违背「cost 必须非零」的要求；float64 累加的漂移维持在 1e-15 量级，
// 在 3 位小数的展示精度下不可见。
//
// # 集成模式
//
// tracker 通过 callback 模式集成。当一次 Engine Run() 完成时，调用方从该 task 的
// 最终 usage 数据创建 CostRecord 并调用 Record()。这使 Engine 保持干净（无 cost 耦合），
// 同时仍能捕获每一笔 LLM 交易，用于计费、budget 与可观测性。
//
// Phase 6+：在完整实现中，tracker 还会：
//   - 通过 migration v10 将记录持久化到 cost_records 表
//   - 暴露 HTTP handler 用于实时 cost 查询
//   - 在某个 project 的 cost 超过配置阈值时支持 budget 告警
package cost

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// CostRecord 记录单次 LLM 调用（或单个 ReAct step 中的一批调用）的 token 消耗与 cost。
// 它是 cost 跟踪与报告的主数据单元。
type CostRecord struct {
	// ID 是此 cost 记录的唯一标识符。
	ID string

	// TaskID 是此 cost 所属的 task（对应 tasks.id）。
	TaskID string

	// SessionID 是此 cost 所属的 session（对应 sessions.id）。
	SessionID string

	// ProjectID 是此 cost 所属的 project（对应 projects.id）。
	ProjectID string

	// AgentID 是执行该 LLM 调用的 agent。
	AgentID string

	// StepIndex 是该 task 内的 ReAct step 索引。
	StepIndex int

	// Model 是 LLM 模型名（例如 "deepseek-v4-flash"）。
	Model string

	// Provider 是 API provider（例如 "openai"、"deepseek"、"anthropic"）。
	Provider string

	// Tier 是从 ModelProfile 派生的 model tier（例如 "efficient"、"standard"）。
	Tier string

	// InputTokens 是消耗的 input/prompt token 数量。
	InputTokens int

	// OutputTokens 是生成的 output/completion token 数量。
	OutputTokens int

	// TotalTokens 是总 token 数（input + output）。
	TotalTokens int

	// CostUSD 是以美元计算的 cost，按 float64 全精度存储。这是 cost 跟踪、聚合与
	// 持久化的主字段。截断由展示层负责（例如前端 toFixed(3)）。
	CostUSD float64

	// CostCents 是派生字段，等于 round(CostUSD * 100)，为与 legacy 消费者
	// （cost_cents_total Prometheus counter 和整数型 cost_cents 数据库列）保持向后
	// 兼容而保留。它并非 source of truth——单次调用的 cost 小于 1 cent，若用整数
	// 算术计算会被截断为 0。
	CostCents int64

	// CreatedAt 是此记录创建时的时间戳。
	CreatedAt time.Time
}

// CostReport 是跨一组 CostRecord 聚合的 cost 报告。
// 它提供顶层总计，以及按 model、tier 和 agent 的分项明细。
type CostReport struct {
	// TotalCostUSD 是报告中所有 CostUSD 值的总和，以 float64 全精度聚合。这是
	// 主要的 total cost 字段。
	TotalCostUSD float64

	// TotalCostCents 是派生值，等于每条记录 CostCents 的总和（= round(CostUSD*100)）。
	// 为与 legacy 整数-cent 消费者保持向后兼容而保留。注意：将已四舍五入的值相加
	// 在边界处可能与 round(TotalCostUSD*100) 略有偏差。
	TotalCostCents int64

	// TotalTokens 是所有 TotalTokens 值的总和。
	TotalTokens int

	// TotalInputTokens 是所有 input token 的总和。
	TotalInputTokens int

	// TotalOutputTokens 是所有 output token 的总和。
	TotalOutputTokens int

	// ByModel 将 model 名称映射到以 USD 表示的 total cost（float64 全精度）。
	ByModel map[string]float64

	// ByTier 将 tier 名称映射到以 USD 表示的 total cost（float64 全精度）。
	ByTier map[string]float64

	// ByAgent 将 agent ID 映射到以 USD 表示的 total cost（float64 全精度）。
	ByAgent map[string]float64

	// RecordCount 是本报告包含的记录数。
	RecordCount int
}

// newCostReport 创建一个零值的 CostReport，并初始化其中的 map。
func newCostReport() *CostReport {
	return &CostReport{
		ByModel: make(map[string]float64),
		ByTier:  make(map[string]float64),
		ByAgent: make(map[string]float64),
	}
}

// CostTracker 是核心的 cost 跟踪组件。它维护 CostRecord 的内存缓存，并提供用于
// 多维 cost 分析的聚合方法。
//
// Thread-safe：所有公开方法均可安全用于并发使用。
type CostTracker struct {
	mu      sync.RWMutex
	records []CostRecord

	// onRecord 是可选的 callback，在记录被存储后调用。
	// 它被异步调用——错误不会传播给调用方。
	onRecord func(CostRecord)

	// registry 是可选的 model registry，用于 tier 查询。
	// 当为 nil 时，tier 解析默认为 "unknown"。
	registry *llm.ModelRegistry
}

// CostTrackerOption 是用于配置 CostTracker 的 functional option。
type CostTrackerOption func(*CostTracker)

// WithOnRecord 设置在新记录被存储时调用的 callback。
// 该 callback 在记录被追加到内存缓存之后调用。
func WithOnRecord(fn func(CostRecord)) CostTrackerOption {
	return func(ct *CostTracker) {
		ct.onRecord = fn
	}
}

// WithRegistry 设置用于 cost 记录中 tier 解析的 model registry。
// 设置后，tracker 可从模型名填充 Tier 字段。
func WithRegistry(registry *llm.ModelRegistry) CostTrackerOption {
	return func(ct *CostTracker) {
		ct.registry = registry
	}
}

// NewCostTracker 创建一个带可选配置的 CostTracker。
//
// 用法：
//
//	// 基本用法——仅内存，无 callback
//	ct := cost.NewCostTracker()
//
//	// 带事件 callback，用于实时 cost 广播
//	ct := cost.NewCostTracker(cost.WithOnRecord(func(r cost.CostRecord) {
//	    bus.SendEvent(event.NewEvent("cost_recorded", r.TaskID, r.AgentID, r.StepIndex, map[string]any{"cost": r.CostUSD}))
//	}))
//
//	// 带 model registry，自动解析 tier
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

// Record 将一条 CostRecord 追加到内存缓存，并在已配置时调用 onRecord callback。
//
// 这是 cost 数据的主入口——在每次 LLM 交互（或每个 ReAct step 的一批交互）完成后，
// 用填写完整的 CostRecord 调用它。
//
// 即使 callback 较慢，本方法也不会阻塞，因为 callback 以 best-effort 方式调用
// （panic 时仅记录日志告警，不返回错误）。
func (ct *CostTracker) Record(record CostRecord) {
	ct.mu.Lock()
	ct.records = append(ct.records, record)
	ct.mu.Unlock()

	if ct.onRecord != nil {
		// 以 best-effort 方式调用 callback——如果 callback panic，我们不会阻塞调用方。
		func() {
			defer func() {
				if r := recover(); r != nil {
					// 记录 panic 但不向调用方传播。
					// callback 只是副作用；Record 不能因它而失败。
					fmt.Printf("[CostTracker] onRecord callback panicked: %v\n", r)
				}
			}()
			ct.onRecord(record)
		}()
	}
}

// TaskCost 聚合所有匹配给定 task_id 的记录的 cost。
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

// SessionCost 聚合所有匹配给定 session_id 的记录的 cost。
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

// ProjectCost 聚合所有匹配给定 project_id 的记录的 cost。
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

// DailyReport 聚合最近 N 天所有记录的 cost。
// Days=0 返回所有记录（不做日期过滤）。
// Days=1 仅返回今天的记录。
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

// CalculateCost 基于模型的 pricing profile 与 API 上报的 token 用量，计算单次 LLM 调用
// 的 USD cost（float64）。
//
// ModelProfile 中的价格为每 1M token 的 USD 价格。cost 以纯 float64 USD 计算并按
// 全精度存储——截断由展示层负责（例如前端 toFixed(3)）：
//
//	cost_usd = (input_tokens * input_price + output_tokens * output_price) / 1_000_000
//
// 本方法严格使用 API 返回的 Usage——不做本地 token 估算。
//
// 当 profile 为 nil 或 usage 的 token 为零时返回 0。需要向后兼容整数-cent 值的调用方
// 应通过 int64(math.Round(cost * 100)) 自行派生（正如 BuildRecordFromProfile 所做）。
func (ct *CostTracker) CalculateCost(profile *llm.ModelProfile, usage llm.Usage) float64 {
	if profile == nil {
		return 0
	}
	if usage.TotalTokens == 0 {
		return 0
	}

	// 价格为每 1M token 的 USD；除以 1_000_000 以换算到单 token。
	// float64 保留了 sub-cent 精度（例如 $1/1M 下 1000 token = $0.001），
	// 整数-cent 算术会将其截断为 $0。
	inputCost := float64(usage.PromptTokens) * profile.InputPrice / 1_000_000
	outputCost := float64(usage.CompletionTokens) * profile.OutputPrice / 1_000_000
	return inputCost + outputCost
}

// BuildRecordFromProfile 从 model profile、usage 数据和 task/agent 标识构造一条
// CostRecord。它会（在可用时）从 registry 自动填充 Tier 字段，并分配唯一 ID 与
// 时间戳。
//
// 这是一个便捷构造器，供集成代码从 Engine 执行结果创建记录使用，无需手动填写每个字段。
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

	costUSD := ct.CalculateCost(profile, usage)

	return CostRecord{
		ID:           fmt.Sprintf("cr_%d_%d", time.Now().UnixNano(), stepIndex),
		TaskID:       taskID,
		SessionID:    sessionID,
		ProjectID:    projectID,
		AgentID:      agentID,
		StepIndex:    stepIndex,
		Model:        model,
		Provider:     "", // 由调用方按需填充
		Tier:         tier,
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		TotalTokens:  usage.TotalTokens,
		CostUSD:      costUSD,                        // 主字段，全精度
		CostCents:    int64(math.Round(costUSD * 100)), // 派生字段，兼容 legacy
		CreatedAt:    time.Now(),
	}
}

// add 是内部辅助方法，将单条记录的数据累加到报告聚合中。
// 它由公开的查询方法在持有读锁时调用。
func (r *CostReport) add(record CostRecord) {
	r.TotalCostCents += record.CostCents
	r.TotalCostUSD += record.CostUSD
	r.TotalTokens += record.TotalTokens
	r.TotalInputTokens += record.InputTokens
	r.TotalOutputTokens += record.OutputTokens
	r.RecordCount++

	r.ByModel[record.Model] += record.CostUSD
	r.ByTier[record.Tier] += record.CostUSD
	r.ByAgent[record.AgentID] += record.CostUSD
}
