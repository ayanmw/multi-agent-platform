package ws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ayanmw/multi-agent-platform/internal/observability"
)

// TestWSBroadcastBufferBackpressure 验证 N3-04 摄入背压：当 hub.broadcast 缓冲满时，
// SendEvent 非阻塞丢弃并累加 ws_broadcast_drops_total，绝不阻塞调用方（引擎关键路径）。
// 本测试不启动 Run（broadcast 无人消费），从而精确触发缓冲满丢弃这一条机制。
func TestWSBroadcastBufferBackpressure(t *testing.T) {
	const bufSize = 128
	h := NewHubWithConfig(HubConfig{
		BroadcastBufferSize:     bufSize,
		ClientSendBuffer:       256,
		RateLimitPerSec:        1e12, // 关闭限流，隔离验证缓冲背压这一个机制
		RateLimitBurst:         1e12,
		SlowClientDropThreshold: 256,
	})
	const total = 400
	start := time.Now()
	for i := 0; i < total; i++ {
		h.SendEvent(newTestEvent(fmt.Sprintf("bp-%d", i), "llm_delta"))
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("SendEvent blocked too long under backpressure: %v", elapsed)
	}
	got := metricValue(t, observability.DefaultMetrics.PrometheusText(), "ws_broadcast_drops_total")
	// 前 bufSize 条进入缓冲，其余 (total-bufSize) 条被丢弃。
	if got < total-bufSize {
		t.Fatalf("expected >= %d broadcast drops, got %d", total-bufSize, got)
	}
}

// TestWSRateLimitDrops 验证 N3-04 摄入限流：当全局令牌桶速率被超过时，
// SendEvent 丢弃并累加 ws_broadcast_rate_limited_total，而非阻塞。
// 本测试把缓冲设得极大、速率设得极低，从而隔离验证限流这一条机制。
func TestWSRateLimitDrops(t *testing.T) {
	h := NewHubWithConfig(HubConfig{
		BroadcastBufferSize:    1 << 20, // 巨大缓冲，隔离限流机制
		ClientSendBuffer:       256,
		RateLimitPerSec:        1,   // 1 事件/秒
		RateLimitBurst:         5,   // 突发 5
		SlowClientDropThreshold: 256,
	})
	const total = 100
	for i := 0; i < total; i++ {
		h.SendEvent(newTestEvent(fmt.Sprintf("rl-%d", i), "llm_delta"))
	}
	got := metricValue(t, observability.DefaultMetrics.PrometheusText(), "ws_broadcast_rate_limited_total")
	// 突发 5 个被放行，其余约 (total-5) 个被限流丢弃。
	if got < total-10 {
		t.Fatalf("expected >= %d rate-limited drops, got %d", total-10, got)
	}
}

// TestWSSlowClientEviction 验证 N3-04 慢客户端保护：一个从不消费其 Send 缓冲的
// 客户端在缓冲持续满、连续丢弃累计达阈值后被主动注销，clientCount 归零，
// 从而保护广播循环不被该病态客户端无限拖累。
func TestWSSlowClientEviction(t *testing.T) {
	h := NewHubWithConfig(DefaultHubConfig())
	go h.Run()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
	}()

	// legacy 客户端（接收全部），注册后从不读取其 Send 通道。
	h.RegisterTestClient("slow")
	// 等待 Run 处理 register（避免早期事件在客户端注册前被广播而错过）。
	time.Sleep(20 * time.Millisecond)

	const total = 2000
	for i := 0; i < total; i++ {
		h.SendEvent(newTestEvent(fmt.Sprintf("sc-%d", i), "llm_delta"))
	}

	// 轮询等待客户端被注销（clientCount 归零），最长 3s。
	deadline := time.Now().Add(3 * time.Second)
	for {
		if h.clientCount() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("slow client was not evicted, clientCount=%d", h.clientCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHubConfigDefaults 验证 DefaultHubConfig 的安全边界（非零、正值），
// 避免误把零值当作有效配置注入 Hub。
func TestHubConfigDefaults(t *testing.T) {
	c := DefaultHubConfig()
	if c.BroadcastBufferSize <= 0 || c.ClientSendBuffer <= 0 ||
		c.RateLimitPerSec <= 0 || c.RateLimitBurst <= 0 ||
		c.SlowClientDropThreshold <= 0 {
		t.Fatalf("DefaultHubConfig has non-positive field: %+v", c)
	}
}
