// metrics_seed_test.go —— Metrics 启动回填（Phase 8-3 / P7）的端到端测试。
//
// 验证 seedMetricsFromDB 把 DB 中的历史累计值真正写进 DefaultMetrics，
// 并最终体现在 /metrics 暴露的 Prometheus 文本中。
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/ayanmw/multi-agent-platform/internal/observability"
	"github.com/ayanmw/multi-agent-platform/pkg/db"
)

func TestSeedMetricsFromDB(t *testing.T) {
	if err := db.Init(":memory:"); err != nil {
		t.Fatalf("db init: %v", err)
	}
	defer func() {
		if db.DB != nil {
			db.DB.Close()
			db.DB = nil
		}
	}()

	// 用独立 collector 替换全局单例，避免污染同包其他测试的计数。
	prev := observability.DefaultMetrics
	observability.DefaultMetrics = observability.NewMetricsCollector()
	t.Cleanup(func() { observability.DefaultMetrics = prev })

	now := time.Now()
	seed := []struct {
		id     string
		status string
	}{
		{"task_seed_1", "completed"},
		{"task_seed_2", "completed"},
		{"task_seed_3", "failed"},
		{"task_seed_4", "cancelled"},
		{"task_seed_5", "running"},
	}
	for _, s := range seed {
		if err := db.InsertTask(db.TaskRecord{
			ID:        s.id,
			UserInput: "seed",
			Status:    s.status,
			StartedAt: now,
		}); err != nil {
			t.Fatalf("insert task %s: %v", s.id, err)
		}
	}

	for _, c := range []struct {
		id             string
		in, out, total int
		cents          int64
	}{
		{"cost_seed_1", 100, 40, 140, 25},
		{"cost_seed_2", 300, 60, 360, 75},
	} {
		if _, err := db.DB.Exec(
			`INSERT INTO cost_records
				(id, task_id, agent_id, model, provider, input_tokens, output_tokens, total_tokens, cost_usd, cost_cents)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.id, "task_seed_1", "agent_1", "gpt-test", "openai", c.in, c.out, c.total, float64(c.cents)/100, c.cents,
		); err != nil {
			t.Fatalf("insert cost_record %s: %v", c.id, err)
		}
	}

	seedMetricsFromDB()

	text := observability.DefaultMetrics.PrometheusText()
	// 计数后带 timestamp，因此匹配到数值后加一个空格即可锁定字段边界。
	want := []string{
		`agent_tasks_total{state="started"} 5 `,
		`agent_tasks_total{state="completed"} 2 `,
		// failed 与 cancelled 合并计入 failed。
		`agent_tasks_total{state="failed"} 2 `,
		`llm_calls_total 2 `,
		`llm_tokens_total{direction="input"} 400 `,
		`llm_tokens_total{direction="output"} 100 `,
		`llm_tokens_total{direction="total"} 500 `,
		`cost_cents_total 100 `,
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("PrometheusText 缺少 %q\n--- got ---\n%s", w, text)
		}
	}
}

// TestSeedMetricsFromDBWithoutDB DB 未初始化时回填必须静默降级，
// 既不 panic 也不把 counter 写成脏值——否则服务会在无持久化模式下起不来。
func TestSeedMetricsFromDBWithoutDB(t *testing.T) {
	prevDB := db.DB
	db.DB = nil
	t.Cleanup(func() { db.DB = prevDB })

	prev := observability.DefaultMetrics
	observability.DefaultMetrics = observability.NewMetricsCollector()
	t.Cleanup(func() { observability.DefaultMetrics = prev })

	seedMetricsFromDB()

	text := observability.DefaultMetrics.PrometheusText()
	if !strings.Contains(text, `agent_tasks_total{state="started"} 0 `) {
		t.Errorf("nil DB 下 counter 应保持 0\n--- got ---\n%s", text)
	}
}
