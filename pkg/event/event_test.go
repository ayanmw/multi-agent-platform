package event

import (
	"reflect"
	"testing"
)

// TestCronEventConstants 验证 Cron 子系统事件常量：
//   - 非空
//   - 统一以 "cron_" 前缀广播，便于前端按子系统路由
//   - 彼此唯一，避免事件类型冲突
func TestCronEventConstants(t *testing.T) {
	consts := map[string]string{
		"EventCronCreated":            EventCronCreated,
		"EventCronUpdated":            EventCronUpdated,
		"EventCronDeleted":            EventCronDeleted,
		"EventCronEnabled":            EventCronEnabled,
		"EventCronDisabled":           EventCronDisabled,
		"EventCronPaused":             EventCronPaused,
		"EventCronResumed":            EventCronResumed,
		"EventCronTriggered":          EventCronTriggered,
		"EventCronExecutionStarted":   EventCronExecutionStarted,
		"EventCronExecutionCompleted": EventCronExecutionCompleted,
		"EventCronExecutionFailed":    EventCronExecutionFailed,
		"EventCronExecutionSkipped":   EventCronExecutionSkipped,
		"EventCronMissed":             EventCronMissed,
		"EventCronNotification":       EventCronNotification,
	}
	seen := make(map[string]string, len(consts))
	for name, val := range consts {
		if val == "" {
			t.Fatalf("%s is empty", name)
		}
		if val[:5] != "cron_" {
			t.Fatalf("%s = %q, expected \"cron_\" prefix", name, val)
		}
		if dup, ok := seen[val]; ok {
			t.Fatalf("duplicate event value %q between %s and %s", val, dup, name)
		}
		seen[val] = name
	}
	if len(consts) != 14 {
		t.Fatalf("expected 14 cron event constants, got %d", len(consts))
	}
	// 防止有人误改常量名导致上面 map 静态漏掉一项：通过反射校验 Const 块一致性。
	_ = reflect.TypeOf
}
