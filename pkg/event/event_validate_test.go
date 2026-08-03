package event

import "testing"

// TestValidate 验证事件完整性校验（N2-01 白盒闭合哨兵）能识别合法与非法事件。
func TestValidate(t *testing.T) {
	// 经 NewEvent 构造的事件满足全部必填字段，应为合法。
	valid := NewEvent("task_started", "t1", "leader", 0, nil)
	if !Valid(valid) {
		t.Fatalf("expected valid event, got issues: %v", Validate(valid))
	}

	cases := []struct {
		name string
		e    Event
	}{
		{"empty event_id", Event{Type: "x", Timestamp: 1, AgentID: "a"}},
		{"empty type", Event{EventID: "e", Timestamp: 1, AgentID: "a"}},
		{"zero timestamp", Event{EventID: "e", Type: "x", AgentID: "a"}},
		{"no routing key", Event{EventID: "e", Type: "x", Timestamp: 1}},
	}
	for _, c := range cases {
		if Valid(c.e) {
			t.Fatalf("%s: expected invalid", c.name)
		}
		if len(Validate(c.e)) == 0 {
			t.Fatalf("%s: expected non-empty issues", c.name)
		}
	}
}

// TestValidateMultipleIssues 验证一次事件可同时暴露多个完整性问题。
func TestValidateMultipleIssues(t *testing.T) {
	issues := Validate(Event{}) // 全空
	if len(issues) < 3 {
		t.Fatalf("expected at least 3 issues for empty event, got %v", issues)
	}
}
