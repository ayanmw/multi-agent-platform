package observability

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestMetricsDimensionRecording 验证 N2-01 维度化指标（agent / session / step）
// 能被正确累加并输出到 Prometheus 文本。
func TestMetricsDimensionRecording(t *testing.T) {
	m := NewMetricsCollector()
	m.RecordAgentTask("leader", "started")
	m.RecordAgentTask("leader", "completed")
	m.RecordAgentStep("leader", "think")
	m.RecordAgentStep("leader", "tool_call")
	m.RecordSessionTask("sess-1", "started")
	m.RecordSessionTask("sess-1", "completed")
	m.RecordLLMLatencyForAgent("leader", 120*time.Millisecond)
	m.RecordToolLatencyForAgent("leader", 5*time.Millisecond)
	m.IncrEventsTotal()
	m.IncrMalformedEvents()

	text := m.PrometheusText()

	checks := []string{
		`agent_tasks_total{agent="leader",state="started"}`,
		`agent_tasks_total{agent="leader",state="completed"}`,
		`agent_steps_total{agent="leader",step_type="think"}`,
		`agent_steps_total{agent="leader",step_type="tool_call"}`,
		`session_tasks_total{session="sess-1",state="started"}`,
		`session_tasks_total{session="sess-1",state="completed"}`,
		`llm_latency_ms_bucket{agent="leader",le=`,
		`tool_latency_ms_bucket{agent="leader",le=`,
		`events_total `,
		`malformed_events_total `,
	}
	for _, c := range checks {
		if !strings.Contains(text, c) {
			t.Fatalf("expected metrics text to contain %q\n got:\n%s", c, text)
		}
	}

	// 空维度值应归一到 "unknown"，避免 Prometheus 空 label。
	m.RecordAgentTask("", "started")
	text2 := m.PrometheusText()
	if !strings.Contains(text2, `agent_tasks_total{agent="unknown",state="started"}`) {
		t.Fatalf("expected empty agent normalized to unknown, got:\n%s", text2)
	}
}

// TestMetricsDimensionCounterStable 验证同维度累加是单调递增的，且输出按 label 排序稳定。
// 注意：每行末尾的 scrape 时间戳（UnixMilli，13 位数字）在两次 PrometheusText() 调用间
// 可能跨毫秒边界而不同——这是 Prometheus 抓取语义的正常表现，不属于「指标值/label 排序」
// 的非确定性。比较前须剥离该时间戳，只断言指标内容与 label 排序的确定性。
var scrapeTsRE = regexp.MustCompile(` \d{13,}\n`)

func stripScrapeTimestamp(s string) string {
	return scrapeTsRE.ReplaceAllString(s, " <ts>\n")
}

func TestMetricsDimensionCounterStable(t *testing.T) {
	m := NewMetricsCollector()
	m.RecordAgentStep("a", "think")
	m.RecordAgentStep("a", "think")
	m.RecordAgentStep("b", "tool_call")
	first := stripScrapeTimestamp(m.PrometheusText())
	second := stripScrapeTimestamp(m.PrometheusText())
	if first != second {
		t.Fatalf("expected deterministic output, got diff:\n%s\n---\n%s", first, second)
	}
	if !strings.Contains(first, `agent_steps_total{agent="a",step_type="think"} 2 `) {
		t.Fatalf("expected agent=a think count 2, got:\n%s", first)
	}
}
