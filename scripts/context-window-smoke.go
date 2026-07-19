//go:build cwsmoke

// context-window-smoke.go —— 用于 context_window_snapshot smoke 的 WebSocket 客户端。
//
// 运行方式：go run -tags cwsmoke scripts/context-window-smoke.go <wsURL> <output.json> [deadlineSeconds]
//
// 设计要点（稳定性）：
//   - 每收到一个 context_window_snapshot 事件就立即把当前结果 flush 到 outFile，
//     而不是等循环结束才写。这样即使被外层脚本 SIGTERM 强杀，已收集的事件也已落盘。
//   - 注册 SIGTERM/SIGINT 信号处理，收到信号时把已收集事件 flush 后再退出，
//     保证 deferred 写入不被跳过（Go 默认收到 SIGTERM 会立即终止，不跑 defer）。
//   - deadline 通过第三个可选参数注入（默认 20s），便于 real LLM 慢链路下放宽。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

type Event struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: go run -tags cwsmoke scripts/context-window-smoke.go <wsURL> <output.json> [deadlineSeconds]")
		os.Exit(2)
	}
	wsURL := os.Args[1]
	outFile := os.Args[2]

	// Gorilla 要求使用 ws:// 或 wss:// scheme
	if strings.HasPrefix(wsURL, "http://") {
		wsURL = "ws://" + strings.TrimPrefix(wsURL, "http://")
	}
	if !strings.HasPrefix(wsURL, "ws://") && !strings.HasPrefix(wsURL, "wss://") {
		wsURL = "ws://" + wsURL
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ws dial failed: %v\n", err)
		os.Exit(9)
	}
	defer conn.Close()

	// deadline 可通过第三个参数覆盖，默认 20s。real LLM 链路慢时调大。
	deadlineSeconds := 20
	if len(os.Args) >= 4 {
		if v, err := strconv.Atoi(os.Args[3]); err == nil && v > 0 {
			deadlineSeconds = v
		}
	}
	deadline := time.Now().Add(time.Duration(deadlineSeconds) * time.Second)

	var events []Event
	// flush 把当前已收集事件写入 outFile。每次新增事件后调用一次，
	// 保证被强杀时磁盘上仍有最新结果。写失败只记录，不中断收集。
	flush := func() {
		f, err := os.Create(outFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create output: %v\n", err)
			return
		}
		_ = json.NewEncoder(f).Encode(events)
		_ = f.Close()
	}

	// 收到 SIGTERM/SIGINT 时优雅 flush 后退出。外层脚本用 kill 默认发 SIGTERM，
	// 若不处理，Go 进程会立即终止，defer 和循环后的写入都被跳过 → 事件文件丢失。
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stop
		flush()
		fmt.Printf("Collected %d context_window_snapshot events (signaled)\n", len(events))
		os.Exit(0)
	}()

	conn.SetReadDeadline(deadline)
	for time.Now().Before(deadline) {
		var ev Event
		if err := conn.ReadJSON(&ev); err != nil {
			break
		}
		if ev.Type == "context_window_snapshot" {
			events = append(events, ev)
			flush() // 增量落盘，避免被强杀时丢失
		}
	}

	flush()
	fmt.Printf("Collected %d context_window_snapshot events\n", len(events))
}
