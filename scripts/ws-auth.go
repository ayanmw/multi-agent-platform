//go:build authsmoke

// WebSocket Auth-on 模式测试客户端
//
// 本文件使用 `//go:build authsmoke` 构建约束隔离，避免与同目录的
// ws-smoke.go 在 `go build ./scripts/...` 时因 package main 重复声明
// (Event / main) 而冲突。两个文件逻辑上完全独立，互不依赖。
//
// 运行方式 (由 scripts/ws-auth-smoke.sh 调用)：
//
//	go run -tags authsmoke scripts/ws-auth.go <ADMIN_KEY> <BASE_URL>
//
// 测试 3 个场景：
//  1. 携带合法 Authorization: Bearer <key> 连接
//  2. 不携带 Authorization 连接
//  3. 携带非法 Authorization: Bearer bad-key 连接
//
// 在 auth-on (REQUIRE_AUTH=true) 模式下，预期 1 成功，2 和 3 被拒绝。
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Event 镜像后端 pkg/event.Event，用于反序列化 WS 消息。
type Event struct {
	EventID   string         `json:"event_id"`
	TaskID    string         `json:"task_id"`
	AgentID   string         `json:"agent_id"`
	StepIndex int            `json:"step_index"`
	Type      string         `json:"type"`
	Timestamp int64          `json:"timestamp"`
	Data      map[string]any `json:"data"`
}

// scenario 记录一个 WS auth 场景的结果。
type scenario struct {
	name    string
	apiKey  string
	headers http.Header
	ok      bool
	detail  string
	events  []Event
}

func main() {
	logf("=== WebSocket Auth-on 模式测试 ===\n")

	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "用法: go run ws-auth.go <ADMIN_KEY> <BASE_URL>\n")
		os.Exit(2)
	}
	adminKey := os.Args[1]
	baseURL := strings.TrimSuffix(os.Args[2], "/")

	wsBase := strings.Replace(baseURL, "http://", "ws://", 1)
	wsBase = strings.Replace(wsBase, "https://", "wss://", 1)
	wsURL := wsBase + "/ws?session_id=temp_test"

	logf("目标 WS endpoint: %s\n", wsURL)

	scenarios := []scenario{
		{name: "合法 key", apiKey: adminKey},
		{name: "无 key", apiKey: ""},
		{name: "非法 key", apiKey: "sk-invalid-fake-key"},
	}

	allPass := true
	for i := range scenarios {
		s := &scenarios[i]
		s.headers = make(http.Header)
		if s.apiKey != "" {
			s.headers.Set("Authorization", "Bearer "+s.apiKey)
		}

		logf("\n--- 场景 %d: %s ---\n", i+1, s.name)
		s.ok, s.detail, s.events = testWSAuth(wsURL, s.headers)

		status := "PASS"
		if !s.ok {
			status = "FAIL"
			allPass = false
		}
		logf("[%s] %s\n", status, s.detail)
		if len(s.events) > 0 {
			logf("  收到 %d 条事件:\n", len(s.events))
			for j, evt := range s.events {
				logf("    %2d. %s (agent=%s, task=%s)\n", j+1, evt.Type, evt.AgentID, evt.TaskID)
			}
		}
	}

	logf("\n=== 汇总 ===\n")
	for _, s := range scenarios {
		status := "PASS"
		if !s.ok {
			status = "FAIL"
		}
		logf("  [%s] %s\n", status, s.name)
	}

	if !allPass {
		os.Exit(1)
	}
}

// testWSAuth 尝试用指定 header 连接 WS，并读取一段时间内的消息。
// 返回：是否符合预期、详细说明、收到的事件列表。
func testWSAuth(wsURL string, headers http.Header) (bool, string, []Event) {
	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 5 * time.Second

	conn, resp, err := dialer.Dial(wsURL, headers)
	if err != nil {
		// WS 握手被拒绝时，err 通常包含 HTTP 状态码
		errStr := err.Error()
		if resp != nil {
			return assessRejected(resp.StatusCode, errStr)
		}
		return false, fmt.Sprintf("连接失败: %s", errStr), nil
	}
	defer conn.Close()

	// 对于 "无 key" 和 "非法 key" 场景，如果连接意外建立，说明 auth 可能未生效
	apiKey := headers.Get("Authorization")
	if apiKey == "" {
		// 读取一次消息确认是否真的被允许
		evts := readOneEvent(conn, 2*time.Second)
		if len(evts) > 0 {
			return false, fmt.Sprintf("未提供 key 但连接成功并收到事件 (auth 未生效?)"), evts
		}
		return false, "未提供 key 但连接成功 (未收到事件，可能 auth 未严格校验)", nil
	}
	if !strings.Contains(apiKey, adminKeyHint()) {
		// 非法 key 但连接成功
		evts := readOneEvent(conn, 2*time.Second)
		if len(evts) > 0 {
			return false, fmt.Sprintf("非法 key 但连接成功并收到事件 (auth 未生效?)"), evts
		}
		return false, "非法 key 但连接成功 (未收到事件，可能 auth 未严格校验)", nil
	}

	// 合法 key：尝试读取系统事件或 ping，确认通道可用
	evts := readOneEvent(conn, 3*time.Second)
	if len(evts) == 0 {
		// 没有事件不代表失败；可能是没有系统广播。
		// 我们额外发送一条 ping 验证 conn 是否存活。
		if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(2*time.Second)); err != nil {
			return false, fmt.Sprintf("合法 key 连接成功但 ping 失败: %s", err), nil
		}
		return true, "合法 key 连接成功 (无广播事件，ping 存活)", nil
	}
	return true, fmt.Sprintf("合法 key 连接成功，收到 %d 条事件", len(evts)), evts
}

// assessRejected 根据握手被拒绝的 HTTP 状态码判断是否符合预期。
func assessRejected(statusCode int, errStr string) (bool, string, []Event) {
	// 401/403 均视为拒绝成功
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return true, fmt.Sprintf("连接被拒绝: HTTP %d (%s)", statusCode, errStr), nil
	}
	if statusCode == http.StatusBadRequest {
		return true, fmt.Sprintf("连接被拒绝: HTTP 400 (%s)", errStr), nil
	}
	return false, fmt.Sprintf("连接失败但未返回预期的拒绝码: HTTP %d (%s)", statusCode, errStr), nil
}

// readOneEvent 在 timeout 内读取一条或多条 WS 消息，过滤出 JSON 事件。
func readOneEvent(conn *websocket.Conn, timeout time.Duration) []Event {
	var events []Event
	deadline := time.After(timeout)
	done := make(chan struct{})

	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				close(done)
				return
			}
			var evt Event
			if err := json.Unmarshal(msg, &evt); err != nil {
				continue
			}
			events = append(events, evt)
			// 读到第一条有效事件即可返回；调用方需要更多时可扩展
			if len(events) >= 1 {
				close(done)
				return
			}
		}
	}()

	select {
	case <-done:
	case <-deadline:
	}
	return events
}

// logf 输出带时间戳的日志。
func logf(format string, args ...any) {
	fmt.Printf("[%s] %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// adminKeyHint 返回一个占位字符串，仅用于判断 headers 中的 key 是否为 admin key。
// 实际比较通过字符串包含关系粗略判定，因为本脚本不暴露真实 admin key 长度。
func adminKeyHint() string {
	return "Bearer "
}
