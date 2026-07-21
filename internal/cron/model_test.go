package cron

import "testing"

// TestScheduleTypeIsValid 验证调度类型的合法性判断。
func TestScheduleTypeIsValid(t *testing.T) {
	for _, s := range []ScheduleType{ScheduleCron, ScheduleInterval, ScheduleOnce} {
		if !s.IsValid() {
			t.Fatalf("%q should be valid", s)
		}
	}
	for _, s := range []ScheduleType{"", "unknown", "CRON"} {
		if s.IsValid() {
			t.Fatalf("%q should be invalid", s)
		}
	}
}

// TestActionTypeIsValid 验证动作类型的合法性判断。
func TestActionTypeIsValid(t *testing.T) {
	for _, a := range []ActionType{ActionStartTask, ActionScript, ActionWebhook, ActionNotifySession} {
		if !a.IsValid() {
			t.Fatalf("%q should be valid", a)
		}
	}
	if ActionType("nope").IsValid() {
		t.Fatalf("invalid action type accepted")
	}
}

// TestStatusIsValid 验证状态的合法性判断。
func TestStatusIsValid(t *testing.T) {
	for _, s := range []Status{StatusEnabled, StatusDisabled, StatusPaused} {
		if !s.IsValid() {
			t.Fatalf("%q should be valid", s)
		}
	}
	if Status("").IsValid() {
		t.Fatalf("empty status accepted")
	}
}
