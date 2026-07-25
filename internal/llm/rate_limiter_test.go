package llm

import (
	"testing"
	"time"
)

// TestRateLimiter_IsLimitExceeded 验证按模型 RPM 限流的基本行为。
func TestRateLimiter_IsLimitExceeded(t *testing.T) {
	lim := NewRateLimiter()
	lim.SetLimit("model-a", 2)
	lim.RecordCall("model-a")
	lim.RecordCall("model-a")

	if lim.IsLimitExceeded("model-a") {
		t.Fatal("model-a 不应在 2 次调用后超限（RPM=2）")
	}

	lim.RecordCall("model-a")
	if !lim.IsLimitExceeded("model-a") {
		t.Fatal("model-a 应在 3 次调用后超限（RPM=2）")
	}

	// 模型 B 无调用，不限流。
	if lim.IsLimitExceeded("model-b") {
		t.Fatal("未调用的 model-b 不应被限流")
	}
}

// TestRateLimiter_SlidingWindowExpires 验证 1 分钟后旧调用过期。
func TestRateLimiter_SlidingWindowExpires(t *testing.T) {
	lim := NewRateLimiter()
	now := time.Now()
	lim.RecordCallAt("model-a", now.Add(-61*time.Second))
	lim.RecordCallAt("model-a", now.Add(-61*time.Second))
	lim.RecordCallAt("model-a", now)

	if lim.IsLimitExceeded("model-a") {
		t.Fatal("旧调用应已过期，model-a 不应超限")
	}
}

// TestRateLimiter_ZeroRPMAllowed 验证注册 RPM=0 的模型无限制。
func TestRateLimiter_ZeroRPMAllowed(t *testing.T) {
	lim := NewRateLimiter()
	lim.RecordCall("deepseek-v4-flash-local")
	if lim.IsLimitExceeded("deepseek-v4-flash-local") {
		t.Fatal("RPM=0 的已注册模型应无限制")
	}
}

// TestRateLimiter_ForgetOldCalls 验证 prune 清理过期条目。
func TestRateLimiter_ForgetOldCalls(t *testing.T) {
	lim := NewRateLimiter()
	now := time.Now()
	lim.RecordCallAt("model-a", now.Add(-2*time.Minute))
	lim.ForgetOldCalls(now.Add(-1*time.Minute))

	// 清理后，内部 timestamps map 中 model-a 的切片应为空（被移除），
	// 继续记录新调用应正常计数。
	lim.RecordCallAt("model-a", now)
	if lim.IsLimitExceeded("model-a") {
		t.Fatal("清理旧调用后不应超限")
	}
}
