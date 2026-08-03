package ws

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ayanmw/multi-agent-platform/internal/observability"
	"github.com/ayanmw/multi-agent-platform/pkg/event"
)

// metricValue 从 Prometheus 文本中解析某个精确匹配 series 的计数值。
func metricValue(t *testing.T, text, series string) int {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, series+" ") {
			var v int
			if _, err := fmt.Sscanf(line, series+" %d", &v); err == nil {
				return v
			}
		}
	}
	return 0
}

// TestSendEventRecordsMalformedAndDimensions 验证 Hub.SendEvent 单一漏斗
// （N2-01）在广播前做事件完整性校验，并按 agent / session / step 维度累加指标。
func TestSendEventRecordsMalformedAndDimensions(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
	}()
	client := h.RegisterTestClient("m1")
	time.Sleep(10 * time.Millisecond)

	before := observability.DefaultMetrics.PrometheusText()

	// 非法事件：缺 EventID / Type / Timestamp / 路由键。
	h.SendEvent(event.Event{Data: map[string]any{}})
	// 合法 task_started：带 agent + session。
	h.SendEvent(event.Event{EventID: "t1", Type: "task_started", Timestamp: 1, AgentID: "agent-x", Data: map[string]any{"session_id": "sess-x"}})
	// 合法 step_started：带 step type。
	h.SendEvent(event.Event{EventID: "t2", Type: "step_started", Timestamp: 2, AgentID: "agent-x", Data: map[string]any{"type": "think"}})

	// 同步屏障：确认 Run 已处理全部 3 个事件（指标在 SendEvent 内同步记录）。
	for i := 0; i < 3; i++ {
		select {
		case <-client.Send:
		case <-time.After(time.Second):
			t.Fatal("did not receive all broadcast events")
		}
	}

	after := observability.DefaultMetrics.PrometheusText()

	mb := metricValue(t, before, "malformed_events_total")
	ma := metricValue(t, after, "malformed_events_total")
	if ma <= mb {
		t.Fatalf("expected malformed_events_total to increase, before=%d after=%d", mb, ma)
	}

	if !strings.Contains(after, `agent_tasks_total{agent="agent-x",state="started"}`) {
		t.Fatalf("expected agent_tasks_total series for agent-x started in:\n%s", after)
	}
	if !strings.Contains(after, `agent_steps_total{agent="agent-x",step_type="think"}`) {
		t.Fatalf("expected agent_steps_total series for agent-x think in:\n%s", after)
	}
	if !strings.Contains(after, `session_tasks_total{session="sess-x",state="started"}`) {
		t.Fatalf("expected session_tasks_total series for sess-x started in:\n%s", after)
	}
}
