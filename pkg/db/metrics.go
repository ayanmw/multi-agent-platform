// metrics.go —— 供 observability Metrics 启动回填（P7）使用的聚合查询。
//
// 进程内的 MetricsCollector 是纯内存计数器，重启后归零。对 Prometheus 而言
// counter 突降会被解释为 counter reset，导致 rate()/increase() 在重启点出现
// 尖刺或断档。这里从已持久化的业务数据反推历史累计值，供 cmd/server 在启动
// 时 Seed 回内存，使 counter 跨重启保持单调。
//
// 注意：本文件刻意不引入 internal/observability。observability 的
// audit_sqlite.go 依赖 pkg/db，pkg/db 必须保持为叶子包，否则会形成 import 环。
package db

import "fmt"

// TaskCounts 是 tasks 表按终态聚合出的任务计数。
//
// 口径与 cmd/server/runner.go 中的 IncrTasks* 埋点对齐：
//   - Started：所有已落库的 task 行；每个 task 在启动时都会先 SaveTask 一次，
//     与 IncrTasksStarted 一一对应。
//   - Completed：status = 'completed'。
//   - Failed：status ∈ ('failed', 'cancelled')，与 IncrTasksFailed 覆盖
//     "失败或被取消" 的语义保持一致。
type TaskCounts struct {
	Started   uint64
	Completed uint64
	Failed    uint64
}

// LLMUsage 是 cost_records 表聚合出的 LLM 调用次数、token 与成本累计值。
//
// 每次 LLM 调用都会写入一条 cost_record，因此 COUNT(*) 等价于内存侧
// llmCalls 的累计值。
type LLMUsage struct {
	Calls        uint64
	InputTokens  uint64
	OutputTokens uint64
	TotalTokens  uint64
	CostCents    int64
}

// AggregateTaskCounts 汇总 tasks 表的任务总数与终态分布。
//
// 用单条 SQL 完成三个计数，避免在启动路径上多次往返数据库。
// 空表返回全零且不报错；DB 未初始化时返回错误，由调用方决定是否忽略。
func AggregateTaskCounts() (TaskCounts, error) {
	var tc TaskCounts
	if DB == nil {
		return tc, fmt.Errorf("db not initialized")
	}
	var started, completed, failed int64
	err := DB.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status IN ('failed', 'cancelled') THEN 1 ELSE 0 END), 0)
		FROM tasks`).Scan(&started, &completed, &failed)
	if err != nil {
		return tc, err
	}
	tc.Started = toUint64(started)
	tc.Completed = toUint64(completed)
	tc.Failed = toUint64(failed)
	return tc, nil
}

// AggregateLLMUsage 汇总 cost_records 表的 LLM 调用次数、token 用量与成本。
//
// cost_cents 由 migration v11 引入，并从旧行的 cost_usd 回填，因此这里只读
// 整数列，避免浮点累加带来的精度漂移。空表返回全零且不报错。
func AggregateLLMUsage() (LLMUsage, error) {
	var u LLMUsage
	if DB == nil {
		return u, fmt.Errorf("db not initialized")
	}
	var calls, input, output, total, cents int64
	err := DB.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(cost_cents), 0)
		FROM cost_records`).Scan(&calls, &input, &output, &total, &cents)
	if err != nil {
		return u, err
	}
	u.Calls = toUint64(calls)
	u.InputTokens = toUint64(input)
	u.OutputTokens = toUint64(output)
	u.TotalTokens = toUint64(total)
	u.CostCents = cents
	return u, nil
}

// toUint64 把 SQLite 返回的有符号计数安全转换为 uint64。
//
// COUNT/SUM 理论上不会为负，这里做防御性截断：若脏数据导致负值，
// 直接转换会回绕成天文数字并污染 Prometheus 上的 counter。
func toUint64(v int64) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}
