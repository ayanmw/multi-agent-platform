package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// === 颜色输出 ===
const (
	cReset   = "\033[0m"
	cRed     = "\033[31m"
	cGreen   = "\033[32m"
	cYellow  = "\033[33m"
	cBlue    = "\033[34m"
	cMagenta = "\033[35m"
	cCyan    = "\033[36m"
	cBold    = "\033[1m"
	cDim     = "\033[2m"
)

// 事件类型 → 颜色映射
var eventColor = map[string]string{
	"task_started":         cGreen,
	"task_completed":       cGreen,
	"task_failed":          cRed,
	"step_started":         cCyan,
	"step_complete":        cCyan,
	"llm_thinking":         cYellow,
	"llm_delta":            cDim,
	"llm_message_complete": cYellow,
	"tool_call_started":    cMagenta,
	"tool_call_output":     cMagenta,
	"tool_call_complete":   cMagenta,
	"tool_call_failed":     cRed,
	"observation":          cBlue,
	"agent_ready":          cGreen,
}

func main() {
	serverURL := flag.String("server", "http://localhost:8080", "Server URL")
	wsURL := flag.String("ws", "ws://localhost:8080/ws", "WebSocket URL")
	scenario := flag.String("scenario", "all", "Test scenario: simple, tool, all")
	flag.Parse()

	fmt.Println(cBold + "╔══════════════════════════════════════════════════════════════╗" + cReset)
	fmt.Println(cBold + "║       Multi-Agent Platform — Phase 1 端到端测试工具          ║" + cReset)
	fmt.Println(cBold + "╚══════════════════════════════════════════════════════════════╝" + cReset)
	fmt.Printf("\n%sServer:%s %s\n", cDim, cReset, *serverURL)
	fmt.Printf("%sWebSocket:%s %s\n", cDim, cReset, *wsURL)
	fmt.Printf("%sScenario:%s %s\n\n", cDim, cReset, *scenario)

	// 检查服务器健康状态
	if !healthCheck(*serverURL) {
		log.Fatal(cRed + "❌ Server is not running! Please start it first: go run ./cmd/server/" + cReset)
	}
	fmt.Println(cGreen + "✅ Server is healthy" + cReset)

	// 运行测试场景
	tests := []TestCase{}
	switch *scenario {
	case "simple":
		tests = append(tests, simpleChatTest())
	case "tool":
		tests = append(tests, toolCallTest())
	case "all":
		tests = append(tests, simpleChatTest(), toolCallTest())
	}

	for i, test := range tests {
		fmt.Printf("\n%s━━━ Test %d/%d: %s ━━━%s\n", cBold, i+1, len(tests), test.Name, cReset)
		fmt.Printf("%s%s%s\n", cDim, test.Description, cReset)
		runTest(*serverURL, *wsURL, test)
		time.Sleep(1 * time.Second) // 测试间隔
	}
}

// === 数据结构 ===

type TestCase struct {
	Name        string
	Description string
	Request     map[string]any
}

type WSEvent struct {
	EventID   string         `json:"event_id"`
	TaskID    string         `json:"task_id"`
	AgentID   string         `json:"agent_id"`
	StepIndex int            `json:"step_index"`
	Type      string         `json:"type"`
	Timestamp int64          `json:"timestamp"`
	Data      map[string]any `json:"data"`
}

// === 测试用例定义 ===

func simpleChatTest() TestCase {
	return TestCase{
		Name:        "简单对话 (无工具调用)",
		Description: "发送: 1+1=? → 期望 LLM 直接返回答案，无 tool call",
		Request: map[string]any{
			"action": "chat",
			"input":  "请用中文回答：1+1等于几？直接回答数字即可。",
		},
	}
}

func toolCallTest() TestCase {
	return TestCase{
		Name:        "工具调用 (run_shell)",
		Description: "发送: 用 run_shell 执行 echo hello_from_agent → 期望 2 步 Loop：tool_call → 分析结果",
		Request: map[string]any{
			"action": "chat",
			"input":  "请用 run_shell 工具执行命令 echo hello_from_agent，然后告诉我命令的输出结果是什么",
		},
	}
}

// === 核心逻辑 ===

func healthCheck(serverURL string) bool {
	resp, err := http.Get(serverURL + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func runTest(serverURL, wsURL string, test TestCase) {
	// 连接 WebSocket
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		log.Printf("%s❌ WebSocket 连接失败: %v%s\n", cRed, err, cReset)
		return
	}
	defer conn.Close()

	fmt.Printf("%s🔌 WebSocket 已连接%s\n", cGreen, cReset)

	// 用 channel 接收中断信号
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	done := make(chan struct{})
	eventCount := 0
	var totalTokens int
	var toolCallsDetected int
	var finalResult string

	// 启动 WebSocket 读取 goroutine
	go func() {
		defer close(done)
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					log.Printf("%s⚠ WebSocket 读取错误: %v%s\n", cYellow, err, cReset)
				}
				return
			}

			var evt WSEvent
			if err := json.Unmarshal(message, &evt); err != nil {
				fmt.Printf("%s⚠ 无法解析事件: %s%s\n", cYellow, string(message), cReset)
				continue
			}

			eventCount++
			printEvent(evt, &totalTokens, &toolCallsDetected, &finalResult)

			// 任务完成或失败时退出
			if evt.Type == "task_completed" || evt.Type == "task_failed" {
				return
			}
		}
	}()

	// 发送 HTTP 请求触发任务
	body, _ := json.Marshal(test.Request)
	resp, err := http.Post(serverURL+"/api/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Printf("%s❌ HTTP 请求失败: %v%s\n", cRed, err, cReset)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var taskResp map[string]any
	err = json.Unmarshal(respBody, &taskResp)
	if err != nil {
		fmt.Printf("%s❌ HTTP 请求失败: %v-%v %s\n", cRed, string(respBody), err, cReset)
		return
	}

	fmt.Printf("%s📤 任务已提交 → task_id: %+v%s\n", cGreen, taskResp, cReset)
	fmt.Println(strings.Repeat("─", 60))

	// 等待任务完成或超时
	select {
	case <-done:
		fmt.Println(strings.Repeat("─", 60))
		fmt.Printf("\n%s📊 测试摘要:%s\n", cBold, cReset)
		fmt.Printf("  事件总数:    %s%d%s\n", cCyan, eventCount, cReset)
		fmt.Printf("  Tool 调用:   %s%d 次%s\n", cMagenta, toolCallsDetected, cReset)
		if totalTokens > 0 {
			fmt.Printf("  Token 消耗:  %s%d%s\n", cYellow, totalTokens, cReset)
		}
		if finalResult != "" {
			fmt.Printf("  最终结果:    %s%s%s\n", cGreen, truncate(finalResult, 200), cReset)
		}
		fmt.Println(cGreen + "✅ 测试完成" + cReset)

	case <-time.After(90 * time.Second):
		fmt.Printf("%s⏰ 测试超时 (90s)%s\n", cRed, cReset)
		fmt.Printf("  已收到事件: %d\n", eventCount)

	case <-interrupt:
		fmt.Printf("%s⚠ 用户中断%s\n", cYellow, cReset)
	}
}

// === 事件打印 ===

func printEvent(evt WSEvent, totalTokens *int, toolCallsDetected *int, finalResult *string) {
	color := eventColor[evt.Type]
	if color == "" {
		color = cReset
	}

	// 时间戳
	ts := time.UnixMilli(evt.Timestamp).Format("15:04:05.000")

	// 事件类型标签
	fmt.Printf("%s[%s]%s %s%-24s%s",
		cDim, ts, cReset,
		color, evt.Type, cReset)

	// 携带 step 和 agent 信息
	if evt.StepIndex > 0 || evt.AgentID != "" {
		fmt.Printf(" %sstep=%d agent=%s%s", cDim, evt.StepIndex, evt.AgentID, cReset)
	}
	fmt.Println()

	// 根据事件类型打印详细信息
	switch evt.Type {
	case "task_started":
		if input, ok := evt.Data["input"]; ok {
			fmt.Printf("  %s└─ 输入:%s %s\n", cDim, cReset, input)
		}

	case "llm_delta":
		if content, ok := evt.Data["content"]; ok {
			s := fmt.Sprintf("%v", content)
			// 流式 token 不换行打印
			fmt.Print(s)
		}

	case "llm_message_complete":
		fmt.Println() // delta 之后换行

	case "llm_thinking":
		if content, ok := evt.Data["content"]; ok {
			fmt.Printf("  %s└─ %s%s\n", cDim, content, cReset)
		}

	case "tool_call_started":
		*toolCallsDetected++
		if tool, ok := evt.Data["tool"]; ok {
			fmt.Printf("  %s├─ 工具:%s %s%s%s\n", cDim, cReset, cMagenta, tool, cReset)
		}
		if args, ok := evt.Data["args"]; ok {
			argsJSON, _ := json.MarshalIndent(args, "  │   ", "  ")
			fmt.Printf("  %s├─ 参数:%s\n", cDim, cReset)
			fmt.Printf("  %s│   %s%s\n", cDim, cDim, string(argsJSON))
		}

	case "tool_call_output":
		if result, ok := evt.Data["result"]; ok {
			val := fmt.Sprintf("%v", result)
			fmt.Printf("  %s├─ 输出:%s\n", cDim, cReset)
			// 尝试格式化 JSON 输出
			var prettyJSON bytes.Buffer
			if json.Indent(&prettyJSON, []byte(val), "  │   ", "  ") == nil {
				fmt.Printf("  %s│   %s%s\n", cDim, cDim, prettyJSON.String())
			} else {
				fmt.Printf("  %s│   %s%s\n", cDim, cReset, val)
			}
		}

	case "tool_call_complete":
		if tool, ok := evt.Data["tool"]; ok {
			duration := evt.Data["duration_ms"]
			fmt.Printf("  %s└─ 完成: %s (耗时 %vms)%s\n", cDim, tool, duration, cReset)
		}

	case "observation":
		if content, ok := evt.Data["content"]; ok {
			val := fmt.Sprintf("%v", content)
			fmt.Printf("  %s└─ 观察结果:%s %s%s%s\n", cDim, cReset, cBlue, truncate(val, 150), cReset)
		}
		if t, ok := evt.Data["total_tokens"]; ok {
			tokens := int(t.(float64))
			*totalTokens = tokens
			fmt.Printf("  %s    Token 统计: prompt=%v completion=%v total=%d%s\n",
				cDim, evt.Data["prompt_tokens"], evt.Data["completion_tokens"], tokens, cReset)
		}

	case "task_completed":
		if result, ok := evt.Data["result"]; ok {
			*finalResult = fmt.Sprintf("%v", result)
		}
		if t, ok := evt.Data["total_tokens"]; ok {
			*totalTokens = int(t.(float64))
		}
		fmt.Printf("  %s🎉 任务成功!%s\n", cGreen, cReset)
		if steps, ok := evt.Data["total_steps"]; ok {
			fmt.Printf("  %s    总步数: %v%s\n", cDim, steps, cReset)
		}

	case "task_failed":
		if reason, ok := evt.Data["reason"]; ok {
			fmt.Printf("  %s❌ 失败原因: %v%s\n", cRed, reason, cReset)
		}
		if err, ok := evt.Data["error"]; ok {
			fmt.Printf("  %s    错误详情: %v%s\n", cRed, err, cReset)
		}

	case "step_started":
		if stepType, ok := evt.Data["type"]; ok {
			icon := "🧠"
			if stepType == "tool_call" {
				icon = "🔧"
			}
			fmt.Printf("  %s%s Step %d → %s%s\n", cDim, icon, evt.StepIndex, stepType, cReset)
		}

	case "step_complete":
		// 太频繁，不打印额外信息
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
