package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ayanmw/multi-agent-platform/pkg/event"

	"github.com/gorilla/websocket"
)

// ===========================================================================
// N3-05 (E10 API 契约文档校正)：/ws 握手与事件流专项测试
// ===========================================================================
//
// 背景：`scripts/smoke-test.sh` 用 curl 只能触发 HTTP 请求，无法完成
// WebSocket 的 Upgrade 握手（101 Switching Protocols），因此 /ws 在冒烟里
// 长期是 [SKIP]，构成 API_CHANGELOG §4.1 记录的第 4 处「契约未覆盖」缺口。
//
// 本文件用真实 gorilla/websocket 客户端 + httptest.Server 打通端到端链路，
// 把该缺口从「仅文档声明」升级为「可执行断言」：
//   - 握手契约：GET /ws 必须完成 101 升级；非 WS 请求必须被拒（不 panic）。
//   - 事件流契约：Hub.SendEvent 广播的事件必须以 JSON 文本帧原样抵达客户端，
//     且 event_id / type / task_id / data 等字段与 pkg/event.Event 的 JSON
//     tag 一致（前端 web/v2 types/events.ts 依赖该结构）。
//   - 会话隔离契约（N3-02）：`?session_id=` 订阅在真实连接上同样生效。
//
// 这些测试不依赖 LLM / DB，确定性且可在 CI（含 -race job）稳定运行。

// newWSTestServer 启动一个只挂载 ServeWS 的 httptest.Server，并返回其 ws:// 基址。
// 返回的 cleanup 会先关 Hub 再关 Server，避免 Run goroutine 泄漏。
func newWSTestServer(t *testing.T) (hub *Hub, wsURL string, cleanup func()) {
	t.Helper()

	hub = NewHub()
	go hub.Run()

	srv := httptest.NewServer(ServeWS(hub))
	wsURL = "ws" + strings.TrimPrefix(srv.URL, "http")

	cleanup = func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = hub.Shutdown(ctx)
		srv.Close()
	}
	return hub, wsURL, cleanup
}

// dialWS 建立一条真实 WebSocket 连接并断言握手返回 101。
// query 为可选的查询串（如 "session_id=sess-A"），空串表示不带参数。
func dialWS(t *testing.T, wsURL, query string) *websocket.Conn {
	t.Helper()

	target := wsURL
	if query != "" {
		target += "?" + query
	}
	conn, resp, err := websocket.DefaultDialer.Dial(target, nil)
	if err != nil {
		t.Fatalf("websocket dial %s failed: %v", target, err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected handshake status 101, got %d", resp.StatusCode)
	}
	return conn
}

// readEventJSON 在超时内读取一条文本帧并反序列化为 event.Event。
// ok=false 表示超时（调用方据此断言「不应收到事件」）。
func readEventJSON(t *testing.T, conn *websocket.Conn, timeout time.Duration) (evt event.Event, ok bool) {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		return event.Event{}, false
	}
	if msgType != websocket.TextMessage {
		t.Fatalf("expected text frame, got message type %d", msgType)
	}
	if err := json.Unmarshal(data, &evt); err != nil {
		t.Fatalf("event frame is not valid JSON: %v (raw=%s)", err, string(data))
	}
	return evt, true
}

// TestServeWSHandshakeAndEventStream 覆盖 /ws 的握手 + 事件流最小契约：
// 真实客户端完成 101 升级后，Hub 广播的事件必须以 JSON 文本帧抵达，
// 且字段名与 pkg/event.Event 的 JSON tag 完全一致（前端契约依据）。
func TestServeWSHandshakeAndEventStream(t *testing.T) {
	hub, wsURL, cleanup := newWSTestServer(t)
	defer cleanup()

	conn := dialWS(t, wsURL, "")
	defer conn.Close()

	// 等待 Hub.Run 完成 register（register 是异步 channel 投递）。
	waitForClientCount(t, hub, 1, time.Second)

	sent := event.Event{
		EventID:   "ws-handshake-1",
		TaskID:    "task-1",
		AgentID:   "leader",
		StepIndex: 3,
		Type:      "llm_delta",
		Timestamp: time.Now().UnixMilli(),
		Data:      map[string]any{"session_id": "sess-A", "content": "hello"},
	}
	hub.SendEvent(sent)

	got, ok := readEventJSON(t, conn, 2*time.Second)
	if !ok {
		t.Fatal("client did not receive broadcast event over the wire")
	}
	if got.EventID != sent.EventID {
		t.Errorf("event_id mismatch: want %s got %s", sent.EventID, got.EventID)
	}
	if got.Type != sent.Type {
		t.Errorf("type mismatch: want %s got %s", sent.Type, got.Type)
	}
	if got.TaskID != sent.TaskID || got.AgentID != sent.AgentID || got.StepIndex != sent.StepIndex {
		t.Errorf("identity fields mismatch: %+v", got)
	}
	if got.Data == nil || got.Data["content"] != "hello" {
		t.Errorf("data payload not preserved: %+v", got.Data)
	}
}

// TestServeWSSessionSubscriptionOverWire 验证 N3-02 的服务端会话订阅在真实
// 连接上同样生效：带 ?session_id= 的连接只收命中事件，跨 session 事件不泄漏。
// 与 hub_session_scope_test.go 的内存级断言互补（这里跨越真实 WS 编解码）。
func TestServeWSSessionSubscriptionOverWire(t *testing.T) {
	hub, wsURL, cleanup := newWSTestServer(t)
	defer cleanup()

	conn := dialWS(t, wsURL, "session_id=sess-A")
	defer conn.Close()

	waitForClientCount(t, hub, 1, time.Second)

	// 先发一条其它 session 的事件（不应抵达），再发一条命中事件（应抵达）。
	hub.SendEvent(event.Event{
		EventID:   "other-session",
		Type:      "task_started",
		Timestamp: time.Now().UnixMilli(),
		Data:      map[string]any{"session_id": "sess-B"},
	})
	hub.SendEvent(event.Event{
		EventID:   "own-session",
		Type:      "task_started",
		Timestamp: time.Now().UnixMilli(),
		Data:      map[string]any{"session_id": "sess-A"},
	})

	got, ok := readEventJSON(t, conn, 2*time.Second)
	if !ok {
		t.Fatal("subscribed client did not receive its own session event")
	}
	// 首条抵达的必须是命中订阅的那条——若是 other-session 则说明隔离失效。
	if got.EventID != "own-session" {
		t.Fatalf("cross-session event leaked over the wire: got %s", got.EventID)
	}
}

// TestServeWSRejectsPlainHTTPRequest 验证非 WebSocket 的普通 GET 不会 panic，
// 而是被 gorilla 的 Upgrader 以 4xx 拒绝（握手契约的负向用例）。
func TestServeWSRejectsPlainHTTPRequest(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = hub.Shutdown(ctx)
	}()

	rec := httptest.NewRecorder()
	ServeWS(hub)(rec, httptest.NewRequest(http.MethodGet, "/ws", nil))

	if rec.Code < 400 || rec.Code >= 500 {
		t.Errorf("plain HTTP GET on /ws should be rejected with 4xx, got %d", rec.Code)
	}
	if hub.clientCount() != 0 {
		t.Errorf("failed upgrade must not register a client, got %d", hub.clientCount())
	}
}

// waitForClientCount 轮询等待 Hub 注册的 client 数达到 want（替代固定 sleep，
// 保证测试确定性；超时即 Fatal）。
func waitForClientCount(t *testing.T, h *Hub, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if h.clientCount() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for clientCount=%d (got %d)", want, h.clientCount())
}
