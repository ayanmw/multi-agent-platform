// metrics_test.go —— Metrics 启动回填聚合查询（P7）的白盒测试。
//
// 复用 database_test.go 的 freshDB helper，在临时 SQLite 文件上验证
// AggregateTaskCounts / AggregateLLMUsage 的口径与边界行为。
package db

import (
	"testing"
	"time"
)

// insertTaskWithStatus 落一条 task 并把它推进到指定状态。
// 先 InsertTask（写入 running）再 UpdateTask，与生产路径一致。
func insertTaskWithStatus(t *testing.T, id, status string) {
	t.Helper()
	if err := InsertTask(TaskRecord{
		ID:        id,
		UserInput: "input-" + id,
		Status:    "running",
		AgentIDs:  []string{"agent_1"},
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("InsertTask(%s): %v", id, err)
	}
	if status == "running" {
		return
	}
	if err := UpdateTask(id, status, "result-"+id, 0); err != nil {
		t.Fatalf("UpdateTask(%s, %s): %v", id, status, err)
	}
}

// insertCostRecord 直接写 cost_records，避免依赖 internal/cost（会形成
// 测试侧的额外耦合）。cost_cents 由 migration v11 引入。
func insertCostRecord(t *testing.T, id string, in, out, total int, cents int64) {
	t.Helper()
	_, err := DB.Exec(
		`INSERT INTO cost_records
			(id, task_id, agent_id, model, provider, input_tokens, output_tokens, total_tokens, cost_usd, cost_cents)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "task_"+id, "agent_1", "gpt-test", "openai", in, out, total, float64(cents)/100, cents,
	)
	if err != nil {
		t.Fatalf("insert cost_record(%s): %v", id, err)
	}
}

// TestAggregateTaskCountsEmpty 空库应返回全零且不报错——全新部署时
// 回填不能因为"没有数据"而告警。
func TestAggregateTaskCountsEmpty(t *testing.T) {
	freshDB(t)

	got, err := AggregateTaskCounts()
	if err != nil {
		t.Fatalf("AggregateTaskCounts on empty db: %v", err)
	}
	if got.Started != 0 || got.Completed != 0 || got.Failed != 0 {
		t.Errorf("empty db counts = %+v, want all zero", got)
	}
}

// TestAggregateTaskCounts 校验计数口径：Started 为全部行，
// Failed 同时覆盖 failed 与 cancelled。
func TestAggregateTaskCounts(t *testing.T) {
	freshDB(t)

	insertTaskWithStatus(t, "t1", "completed")
	insertTaskWithStatus(t, "t2", "completed")
	insertTaskWithStatus(t, "t3", "failed")
	insertTaskWithStatus(t, "t4", "cancelled")
	insertTaskWithStatus(t, "t5", "running")

	got, err := AggregateTaskCounts()
	if err != nil {
		t.Fatalf("AggregateTaskCounts: %v", err)
	}
	if got.Started != 5 {
		t.Errorf("Started = %d, want 5", got.Started)
	}
	if got.Completed != 2 {
		t.Errorf("Completed = %d, want 2", got.Completed)
	}
	// failed + cancelled 都计入 Failed，与 IncrTasksFailed 语义一致。
	if got.Failed != 2 {
		t.Errorf("Failed = %d, want 2 (failed + cancelled)", got.Failed)
	}
}

// TestAggregateLLMUsageEmpty 空 cost_records 应返回全零且不报错。
func TestAggregateLLMUsageEmpty(t *testing.T) {
	freshDB(t)

	got, err := AggregateLLMUsage()
	if err != nil {
		t.Fatalf("AggregateLLMUsage on empty db: %v", err)
	}
	if got.Calls != 0 || got.TotalTokens != 0 || got.CostCents != 0 {
		t.Errorf("empty db usage = %+v, want all zero", got)
	}
}

// TestAggregateLLMUsage 校验 token 与成本按整数列求和。
func TestAggregateLLMUsage(t *testing.T) {
	freshDB(t)

	insertCostRecord(t, "c1", 100, 50, 150, 12)
	insertCostRecord(t, "c2", 200, 80, 280, 34)
	insertCostRecord(t, "c3", 0, 0, 0, 0)

	got, err := AggregateLLMUsage()
	if err != nil {
		t.Fatalf("AggregateLLMUsage: %v", err)
	}
	if got.Calls != 3 {
		t.Errorf("Calls = %d, want 3", got.Calls)
	}
	if got.InputTokens != 300 {
		t.Errorf("InputTokens = %d, want 300", got.InputTokens)
	}
	if got.OutputTokens != 130 {
		t.Errorf("OutputTokens = %d, want 130", got.OutputTokens)
	}
	if got.TotalTokens != 430 {
		t.Errorf("TotalTokens = %d, want 430", got.TotalTokens)
	}
	if got.CostCents != 46 {
		t.Errorf("CostCents = %d, want 46", got.CostCents)
	}
}

// TestAggregateWithoutDB DB 未初始化时必须返回错误而非 panic：
// 启动路径依赖这个错误分支来降级为"跳过回填"。
func TestAggregateWithoutDB(t *testing.T) {
	prev := DB
	DB = nil
	t.Cleanup(func() { DB = prev })

	if _, err := AggregateTaskCounts(); err == nil {
		t.Error("AggregateTaskCounts with nil DB: want error, got nil")
	}
	if _, err := AggregateLLMUsage(); err == nil {
		t.Error("AggregateLLMUsage with nil DB: want error, got nil")
	}
}

// TestToUint64Clamp 负值必须被截断为 0，否则会回绕成天文数字并污染
// Prometheus counter。
func TestToUint64Clamp(t *testing.T) {
	if got := toUint64(-1); got != 0 {
		t.Errorf("toUint64(-1) = %d, want 0", got)
	}
	if got := toUint64(42); got != 42 {
		t.Errorf("toUint64(42) = %d, want 42", got)
	}
}
