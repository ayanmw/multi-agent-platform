// WebSocket 事件流专项评测脚本 (维度 A)
//
// 本脚本编译并启动 mock 模式的 server，连接 WebSocket，
// 通过 POST /api/tasks 触发任务，验证事件流序列、字段完整性、
// tool_call 三联事件，以及 cancel 控制消息是否生效。
//
// 运行: cd D:\Claude-Code-MultiAgent && go run scripts/ws-smoke.go
//
// 约束: 只读后端源码，不修改后端；仅本脚本可自由调整。
//go:build wssmoke || ignore

// WebSocket 事件流专项评测脚本 (维度 A)
//
// 本脚本编译并启动 mock 模式的 server，连接 WebSocket，
// 通过 POST /api/tasks 触发任务，验证事件流序列、字段完整性、
// tool_call 三联事件，以及 cancel 控制消息是否生效。
//
// 运行: cd D:\Claude-Code-MultiAgent && go run -tags wssmoke scripts/ws-smoke.go
//
// 约束: 只读后端源码，不修改后端；仅本脚本可自由调整。
//
// 本文件使用 `//go:build wssmoke || ignore` 构建约束隔离，避免与同目录其它
// package main 文件在默认 `go test ./...` 构建时因 main 重复声明而冲突。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Event 镜像 pkg/event.Event 结构，用于在本脚本中反序列化 WS 消息。
// 字段定义与后端 pkg/event/event.go 保持一致。
type Event struct {
	EventID   string         `json:"event_id"`
	TaskID    string         `json:"task_id"`
	AgentID   string         `json:"agent_id"`
	StepIndex int            `json:"step_index"`
	Type      string         `json:"type"`
	Timestamp int64          `json:"timestamp"`
	Data      map[string]any `json:"data"`
}

// scenarioResult 记录一次测试场景的完整结果。
type scenarioResult struct {
	name        string
	taskID      string
	events      []Event   // 按到达顺序记录的所有事件
	eventTypes  []string  // 按到达顺序的事件类型列表
	checks      []checkResult
	pass        bool
	failReasons []string
}

// checkResult 记录单项检查的结论。
type checkResult struct {
	name   string
	pass   bool
	detail string
}

const (
	port    = "18101"
	baseURL = "http://localhost:" + port
	wsURL   = "ws://localhost:" + port + "/ws?session_id=ws-smoke"
)

// 设计序列 (来自 CLAUDE.md / engine.go 注释)
// task_started → step_started → llm_thinking → llm_delta* → llm_message_complete
//   → tool_call_started → tool_call_output → tool_call_complete
//   → observation → step_complete → ... → task_completed / task_failed
var designedCoreSequence = []string{
	"task_started",
	"step_started",
	"llm_thinking",
	"llm_delta",
	"llm_message_complete",
	"tool_call_started",
	"tool_call_output",
	"tool_call_complete",
	"observation",
	"step_complete",
	"task_completed",
}

// knownExtras 是引擎额外发出、但不在设计序列中的事件类型。
// 这些事件出现不算"乱序"或"错误"，只是设计序列未列出。
var knownExtras = map[string]bool{
	"agent_ready":    true, // engine.Run 开始时发送
	"agent_status":   true, // 每次 think 后发送 token 用量
	"session_status": true, // 任务结束后发送 session 状态
	"model_routed":   true, // Router 选择模型时发送 (Phase 6)
	"system_info":    true, // 系统信息事件 (审批、agent 通信等)
}

func main() {
	log.SetFlags(log.Ltime)
	fmt.Println("=== WebSocket 事件流专项评测 (维度 A) ===")
	fmt.Println()

	// ================================================================
	// [1] 构建 server binary
	// ================================================================
	fmt.Println("=== [1] 构建 server binary ===")
	repoRoot, err := os.Getwd()
	if err != nil {
		log.Fatalf("无法获取 cwd: %v", err)
	}
	serverBin := filepath.Join(os.TempDir(), fmt.Sprintf("ws-smoke-server-%d.exe", time.Now().UnixNano()))
	build := exec.Command("go", "build", "-o", serverBin, "./cmd/server")
	build.Dir = repoRoot
	buildOut, err := build.CombinedOutput()
	if err != nil {
		log.Fatalf("go build 失败: %v\n%s", err, buildOut)
	}
	fmt.Printf("  构建成功: %s\n", serverBin)

	// ================================================================
	// [2] 启动 server (mock 模式)
	// ================================================================
	fmt.Println()
	fmt.Println("=== [2] 启动 server (mock 模式, 端口 18101) ===")
	dbPath := filepath.Join(os.TempDir(), fmt.Sprintf("ws-smoke-%d.db", time.Now().UnixNano()))
	srv := exec.Command(serverBin)
	srv.Dir = repoRoot
	srv.Env = append(os.Environ(),
		"DB_PATH="+dbPath,
		"LLM_USE_MOCK=true",
		"REQUIRE_AUTH=false",
		"SERVER_PORT="+port,
		"LOG_LEVEL=warn",
	)
	var srvLog bytes.Buffer
	srv.Stdout = &srvLog
	srv.Stderr = &srvLog
	if err := srv.Start(); err != nil {
		log.Fatalf("启动 server 失败: %v", err)
	}
	fmt.Printf("  server PID=%d, DB=%s\n", srv.Process.Pid, dbPath)

	// 清理: kill 进程 + 删除临时文件
	cleanup := func() {
		if srv.Process != nil {
			_ = srv.Process.Kill()
			_, _ = srv.Process.Wait()
		}
		os.Remove(dbPath)
		os.Remove(serverBin)
	}
	defer cleanup()

	// ================================================================
	// [3] 轮询 /healthz 直到 200
	// ================================================================
	fmt.Print("  等待 healthz...")
	if !waitForHealth(30 * time.Second) {
		fmt.Println(" FAIL")
		fmt.Println("  server 日志:")
		fmt.Println(indent(srvLog.String(), "    "))
		log.Fatalf("server 未在 30s 内就绪")
	}
	fmt.Println(" OK")

	// ================================================================
	// [4] 测试 1: dialogue case (纯对话，无 tool call)
	// ================================================================
	fmt.Println()
	fmt.Println("=== [4] 测试 1: dialogue case (纯对话，无 tool call) ===")
	r1 := runScenario("dialogue", map[string]any{
		"action":    "chat",
		"input":     "hello ws event flow",
		"agent_id":  "agent_ws",
		"max_steps": 3,
	}, 15*time.Second, false)
	printScenarioResult(r1)

	// ================================================================
	// [5] 测试 2: research case (带 tool_call)
	// ================================================================
	fmt.Println()
	fmt.Println("=== [5] 测试 2: research case (带 tool_call) ===")
	r2 := runScenario("research", map[string]any{
		"action":    "chat",
		"input":     "research AI agent frameworks 2026",
		"agent_id":  "agent_ws2",
		"max_steps": 3,
	}, 15*time.Second, false)
	printScenarioResult(r2)

	// ================================================================
	// [6] 测试 3: cancel 控制消息 (long-task case)
	// ================================================================
	fmt.Println()
	fmt.Println("=== [6] 测试 3: cancel 控制消息 (long-task case) ===")
	r3 := runCancelTest()
	printScenarioResult(r3)

	// ================================================================
	// [7] 汇总
	// ================================================================
	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("=== 评测汇总 ===")
	fmt.Println("========================================")
	fmt.Printf("  测试 1 (dialogue 事件序列):     %s\n", passFail(r1.pass))
	fmt.Printf("  测试 2 (research tool 三联事件): %s\n", passFail(r2.pass))
	fmt.Printf("  测试 3 (cancel 控制消息):        %s\n", passFail(r3.pass))
	fmt.Println()
	if !r1.pass || !r2.pass || !r3.pass {
		fmt.Println("  FAIL 原因汇总:")
		for _, r := range []scenarioResult{r1, r2, r3} {
			for _, reason := range r.failReasons {
				fmt.Printf("    [%s] %s\n", r.name, reason)
			}
		}
	}
	fmt.Println()
	fmt.Println("=== 评测完成 ===")
}

// waitForHealth 轮询 /healthz 直到返回 200 或超时。
func waitForHealth(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil {
			if resp.StatusCode == 200 {
				resp.Body.Close()
				return true
			}
			resp.Body.Close()
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// runScenario 执行一次完整的测试场景:
// 1. 连接 WS
// 2. POST /api/tasks?case=<caseID> 触发任务
// 3. 读取事件直到 task_completed/task_failed 或超时
// 4. 验证事件序列与字段
func runScenario(caseID string, body map[string]any, timeout time.Duration, isCancelTest bool) scenarioResult {
	result := scenarioResult{name: caseID}

	// 连接 WebSocket
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		result.failReasons = append(result.failReasons, "WS 连接失败: "+err.Error())
		result.pass = false
		return result
	}
	defer conn.Close()

	// POST 任务
	bodyJSON, _ := json.Marshal(body)
	url := baseURL + "/api/tasks?case=" + caseID
	resp, err := http.Post(url, "application/json", bytes.NewReader(bodyJSON))
	if err != nil {
		result.failReasons = append(result.failReasons, "POST /api/tasks 失败: "+err.Error())
		result.pass = false
		return result
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		result.failReasons = append(result.failReasons, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(respBody)))
		result.pass = false
		return result
	}
	var taskResp struct {
		TaskID    string `json:"task_id"`
		AgentID   string `json:"agent_id"`
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(respBody, &taskResp)
	result.taskID = taskResp.TaskID
	fmt.Printf("  POST %s → task_id=%s, agent_id=%s\n", url, taskResp.TaskID, taskResp.AgentID)

	// 读取事件
	result.events = readEvents(conn, timeout)
	for _, evt := range result.events {
		result.eventTypes = append(result.eventTypes, evt.Type)
	}

	// 打印事件序列
	fmt.Printf("  事件序列 (%d 条):\n", len(result.eventTypes))
	for i, t := range result.eventTypes {
		fmt.Printf("    %2d. %s\n", i+1, t)
	}

	// 验证
	result.checks = validateScenario(caseID, &result)
	result.pass = len(result.failReasons) == 0
	return result
}

// runCancelTest 测试 cancel 控制消息:
// 1. 连接 WS
// 2. POST long-task 任务
// 3. 等待收到首个 step_started 后再发送 cancel，确保 cancelRegistry 已注册
// 4. 读取事件，验证任务是否被取消
func runCancelTest() scenarioResult {
	result := scenarioResult{name: "cancel-test"}

	// 连接 WebSocket
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		result.failReasons = append(result.failReasons, "WS 连接失败: "+err.Error())
		result.pass = false
		return result
	}
	defer conn.Close()

	// POST long-task 任务
	body, _ := json.Marshal(map[string]any{
		"action":    "chat",
		"input":     "long task with multiple steps",
		"agent_id":  "agent_cancel",
		"max_steps": 12,
	})
	resp, err := http.Post(baseURL+"/api/tasks?case=long-task", "application/json", bytes.NewReader(body))
	if err != nil {
		result.failReasons = append(result.failReasons, "POST 失败: "+err.Error())
		result.pass = false
		return result
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var taskResp struct {
		TaskID  string `json:"task_id"`
		AgentID string `json:"agent_id"`
	}
	json.Unmarshal(respBody, &taskResp)
	result.taskID = taskResp.TaskID
	fmt.Printf("  POST /api/tasks?case=long-task → task_id=%s\n", taskResp.TaskID)

	// 等待收到首个 step_started 事件，确保 runAgentLoopWithTurn 已经把 cancel 函数
	// 注册到 cancelRegistry 中，然后再发送 cancel，避免在 goroutine 启动前 cancel。
	cancelMsg := map[string]any{
		"action":   "cancel",
		"task_id":  taskResp.TaskID,
		"agent_id": taskResp.AgentID,
	}
	cancelJSON, _ := json.Marshal(cancelMsg)

	sendCancelOnce := sync.OnceFunc(func() {
		fmt.Printf("  发送 cancel 控制消息: %s\n", string(cancelJSON))
		if err := conn.WriteMessage(websocket.TextMessage, cancelJSON); err != nil {
			result.failReasons = append(result.failReasons, "发送 cancel 失败: "+err.Error())
		}
	})

	// 再发一条 pause (测试多个控制消息)
	pauseMsg, _ := json.Marshal(map[string]any{
		"action":   "pause",
		"task_id":  taskResp.TaskID,
		"agent_id": taskResp.AgentID,
	})

	// 读取事件
	result.events = readEventsWithCancel(conn, 20*time.Second, func(evt Event) {
		if evt.Type == "step_started" {
			sendCancelOnce()
			fmt.Printf("  发送 pause 控制消息: %s\n", string(pauseMsg))
			conn.WriteMessage(websocket.TextMessage, pauseMsg)
		}
	})
	for _, evt := range result.events {
		result.eventTypes = append(result.eventTypes, evt.Type)
	}

	fmt.Printf("  事件序列 (%d 条):\n", len(result.eventTypes))
	for i, t := range result.eventTypes {
		fmt.Printf("    %2d. %s\n", i+1, t)
	}

	// 验证: 检查任务是否被取消
	// 预期: 如果 cancel 生效，应出现 task_failed (reason=cancelled)
	// 如果 cancel 未生效，任务正常完成 task_completed
	result.checks = validateCancelTest(&result)
	result.pass = len(result.failReasons) == 0
	return result
}

// readEvents 从 WS 读取事件，直到读到 task_completed/task_failed 或超时。
func readEvents(conn *websocket.Conn, timeout time.Duration) []Event {
	return readEventsWithCancel(conn, timeout, nil)
}

// readEventsWithCancel 从 WS 读取事件，每收到一个事件调用 onEvent，直到读到
// task_completed/task_failed 或超时。
func readEventsWithCancel(conn *websocket.Conn, timeout time.Duration, onEvent func(Event)) []Event {
	events := []Event{}
	evtCh := make(chan Event, 256)
	errCh := make(chan error, 1)

	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			var evt Event
			if err := json.Unmarshal(msg, &evt); err != nil {
				// 非 JSON 事件 (如 ping/pong) — 跳过
				continue
			}
			evtCh <- evt
		}
	}()

	deadline := time.After(timeout)
	for {
		select {
		case evt := <-evtCh:
			events = append(events, evt)
			if onEvent != nil {
				onEvent(evt)
			}
			if evt.Type == "task_completed" || evt.Type == "task_failed" {
				// 再读 500ms 捕获可能的 session_status 尾事件
				grace := time.After(500 * time.Millisecond)
			grabLoop:
				for {
					select {
					case e2 := <-evtCh:
						events = append(events, e2)
					case <-grace:
						break grabLoop
					case <-errCh:
						break grabLoop
					}
				}
				return events
			}
		case err := <-errCh:
			if len(events) == 0 {
				fmt.Printf("  [WARN] WS 读取错误 (无事件): %v\n", err)
			}
			return events
		case <-deadline:
			fmt.Printf("  [WARN] 读取超时 (%v)，已收 %d 条事件\n", timeout, len(events))
			return events
		}
	}
}

// validateScenario 验证事件序列与字段完整性。
func validateScenario(caseID string, result *scenarioResult) []checkResult {
	checks := []checkResult{}
	types := result.eventTypes

	// 检查 a: 事件序列非空
	checks = append(checks, checkResult{
		name:   "事件序列非空",
		pass:   len(types) > 0,
		detail: fmt.Sprintf("共 %d 条事件", len(types)),
	})
	if len(types) == 0 {
		result.failReasons = append(result.failReasons, "未收到任何事件")
		return checks
	}

	// 检查 b: 首事件是 task_started
	firstOK := types[0] == "task_started"
	checks = append(checks, checkResult{
		name:   "首事件 = task_started",
		pass:   firstOK,
		detail: fmt.Sprintf("实际首事件: %s", types[0]),
	})
	if !firstOK {
		result.failReasons = append(result.failReasons, fmt.Sprintf("首事件应为 task_started，实际为 %s", types[0]))
	}

	// 检查 c: 末事件是 task_completed 或 task_failed
	// session_status 可能在 task_completed 之后，往前找
	terminalIdx := len(types) - 1
	for terminalIdx >= 0 && types[terminalIdx] == "session_status" {
		terminalIdx--
	}
	terminal := ""
	if terminalIdx >= 0 {
		terminal = types[terminalIdx]
	}
	terminalOK := terminal == "task_completed" || terminal == "task_failed"
	checks = append(checks, checkResult{
		name:   "末事件 = task_completed/task_failed",
		pass:   terminalOK,
		detail: fmt.Sprintf("实际末事件: %s", terminal),
	})
	if !terminalOK {
		result.failReasons = append(result.failReasons, fmt.Sprintf("末事件应为 task_completed/task_failed，实际为 %s", terminal))
	}

	// 检查 d: task_started 字段完整性 (task_id/agent_id/session_id/input)
	var taskStarted *Event
	for i := range result.events {
		if result.events[i].Type == "task_started" {
			taskStarted = &result.events[i]
			break
		}
	}
	if taskStarted != nil {
		fields := map[string]bool{
			"task_id":    taskStarted.Data["task_id"] != nil && taskStarted.Data["task_id"] != "",
			"agent_id":   taskStarted.Data["agent_id"] != nil && taskStarted.Data["agent_id"] != "",
			"session_id": taskStarted.Data["session_id"] != nil,
			"input":      taskStarted.Data["input"] != nil && taskStarted.Data["input"] != "",
		}
		allPresent := true
		missing := []string{}
		for f, ok := range fields {
			if !ok {
				missing = append(missing, f)
				allPresent = false
			}
		}
		checks = append(checks, checkResult{
			name:   "task_started 含 task_id/agent_id/session_id/input",
			pass:   allPresent,
			detail: fmt.Sprintf("task_id=%v, agent_id=%v, session_id=%v, input=%v",
				taskStarted.Data["task_id"], taskStarted.Data["agent_id"],
				taskStarted.Data["session_id"], taskStarted.Data["input"]),
		})
		if !allPresent {
			result.failReasons = append(result.failReasons, "task_started 缺少字段: "+strings.Join(missing, ", "))
		}
	} else {
		checks = append(checks, checkResult{name: "task_started 含完整字段", pass: false, detail: "task_started 未出现"})
		result.failReasons = append(result.failReasons, "task_started 事件未出现")
	}

	// 检查 e: llm_* 类事件出现
	hasLLM := false
	for _, t := range types {
		if strings.HasPrefix(t, "llm_") {
			hasLLM = true
			break
		}
	}
	checks = append(checks, checkResult{
		name:   "出现 llm_* 类事件",
		pass:   hasLLM,
		detail: fmt.Sprintf("llm_thinking=%v, llm_delta=%v, llm_message_complete=%v",
			contains(types, "llm_thinking"), countPrefix(types, "llm_delta") > 0, contains(types, "llm_message_complete")),
	})
	if !hasLLM {
		result.failReasons = append(result.failReasons, "未出现任何 llm_* 事件")
	}

	// 检查 f: llm_thinking 在 llm_delta 之前
	if contains(types, "llm_thinking") && countPrefix(types, "llm_delta") > 0 {
		thinkIdx := indexOf(types, "llm_thinking")
		firstDeltaIdx := indexOfPrefix(types, "llm_delta")
		orderOK := thinkIdx < firstDeltaIdx
		checks = append(checks, checkResult{
			name:   "llm_thinking 在 llm_delta 之前",
			pass:   orderOK,
			detail: fmt.Sprintf("llm_thinking@%d, llm_delta@%d", thinkIdx, firstDeltaIdx),
		})
		if !orderOK {
			result.failReasons = append(result.failReasons, "llm_thinking 应在 llm_delta 之前")
		}
	}

	// 检查 g: task_completed 携带非空 output/final_result
	var taskCompleted *Event
	for i := range result.events {
		if result.events[i].Type == "task_completed" {
			taskCompleted = &result.events[i]
			break
		}
	}
	if taskCompleted != nil {
		resultStr := ""
		if v, ok := taskCompleted.Data["result"]; ok && v != nil {
			resultStr = fmt.Sprintf("%v", v)
		}
		nonEmpty := resultStr != ""
		checks = append(checks, checkResult{
			name:   "task_completed 携带非空 result",
			pass:   nonEmpty,
			detail: fmt.Sprintf("result=%q", truncate(resultStr, 80)),
		})
		if !nonEmpty {
			result.failReasons = append(result.failReasons, "task_completed 的 result 字段为空")
		}
	} else if terminal == "task_failed" {
		// 任务失败 — 检查 task_failed 的 reason
		var taskFailed *Event
		for i := range result.events {
			if result.events[i].Type == "task_failed" {
				taskFailed = &result.events[i]
				break
			}
		}
		if taskFailed != nil {
			reason := fmt.Sprintf("%v", taskFailed.Data["reason"])
			checks = append(checks, checkResult{
				name:   "task_failed 携带 reason",
				pass:   reason != "" && reason != "<nil>",
				detail: fmt.Sprintf("reason=%s", reason),
			})
		}
	} else {
		checks = append(checks, checkResult{name: "task_completed 出现", pass: false, detail: "未出现"})
		result.failReasons = append(result.failReasons, "task_completed 事件未出现")
	}

	// 检查 h: 与设计序列对比 — 列出缺失/多余/乱序
	checks = append(checks, compareWithDesign(types))

	// 检查 i (仅 research): tool_call 三联事件
	if caseID == "research" {
		hasTriple := contains(types, "tool_call_started") &&
			contains(types, "tool_call_output") &&
			contains(types, "tool_call_complete")
		checks = append(checks, checkResult{
			name:   "tool_call 三联事件 (started→output→complete)",
			pass:   hasTriple,
			detail: fmt.Sprintf("started=%v, output=%v, complete=%v",
				contains(types, "tool_call_started"), contains(types, "tool_call_output"), contains(types, "tool_call_complete")),
		})
		if !hasTriple {
			result.failReasons = append(result.failReasons, "research case 缺少 tool_call 三联事件")
		}

		// 检查三联事件顺序
		if hasTriple {
			sIdx := indexOf(types, "tool_call_started")
			oIdx := indexOf(types, "tool_call_output")
			cIdx := indexOf(types, "tool_call_complete")
			orderOK := sIdx < oIdx && oIdx < cIdx
			checks = append(checks, checkResult{
				name:   "tool_call 三联事件顺序正确",
				pass:   orderOK,
				detail: fmt.Sprintf("started@%d → output@%d → complete@%d", sIdx, oIdx, cIdx),
			})
			if !orderOK {
				result.failReasons = append(result.failReasons, "tool_call 三联事件顺序错误")
			}
		}
	}

	return checks
}

// validateCancelTest 验证 cancel 控制消息测试结果。
func validateCancelTest(result *scenarioResult) []checkResult {
	checks := []checkResult{}
	types := result.eventTypes

	// 检查: 任务是否被取消
	// 如果 cancel 生效: 应出现 task_failed (reason=cancelled)
	// 如果 cancel 未生效: 任务正常完成 task_completed
	hasTaskFailed := contains(types, "task_failed")
	hasTaskCompleted := contains(types, "task_completed")

	var failEvent *Event
	for i := range result.events {
		if result.events[i].Type == "task_failed" {
			failEvent = &result.events[i]
			break
		}
	}

	cancelled := false
	if failEvent != nil {
		reason := fmt.Sprintf("%v", failEvent.Data["reason"])
		cancelled = strings.Contains(strings.ToLower(reason), "cancel")
	}

	if cancelled {
		checks = append(checks, checkResult{
			name:   "cancel 生效 → task_failed (reason=cancelled)",
			pass:   true,
			detail: fmt.Sprintf("reason=%v", failEvent.Data["reason"]),
		})
	} else if hasTaskCompleted {
		checks = append(checks, checkResult{
			name:   "cancel 生效 → task_failed (reason=cancelled)",
			pass:   false,
			detail: fmt.Sprintf("任务正常 task_completed，cancel 未生效 (task_failed=%v)", hasTaskFailed),
		})
		result.failReasons = append(result.failReasons,
			"cancel 控制消息未生效: 任务正常完成 (task_completed)，未出现 task_failed(reason=cancelled)")
	} else if hasTaskFailed {
		checks = append(checks, checkResult{
			name:   "cancel 生效 → task_failed (reason=cancelled)",
			pass:   false,
			detail: fmt.Sprintf("task_failed 但 reason 不含 cancel: %v", failEvent.Data["reason"]),
		})
		result.failReasons = append(result.failReasons,
			fmt.Sprintf("task_failed 但 reason 不含 cancel: %v", failEvent.Data["reason"]))
	} else {
		checks = append(checks, checkResult{
			name:   "cancel 生效 → task_failed (reason=cancelled)",
			pass:   false,
			detail: "既无 task_completed 也无 task_failed",
		})
		result.failReasons = append(result.failReasons, "任务未终止 (无 task_completed/task_failed)")
	}

	return checks
}

// compareWithDesign 对比实际序列与设计序列，列出缺失/多余/乱序。
// 乱序判定策略: 不要求与设计序列一次性子序列匹配 (因为 ReAct Loop 中
// think→tool_call→observation 会循环多轮，核心事件会重复出现)。
// 我们改为检查"局部顺序约束": 每一对相邻的核心事件 (a, b) 必须满足
// designIndex(a) <= designIndex(b)，且如果 a != b 则 designIndex(a) < designIndex(b)。
// 这样允许多轮循环，但禁止逆序 (如 step_complete 在 step_started 之前)。
func compareWithDesign(actual []string) checkResult {
	// 提取实际序列中的"核心"事件 (设计序列中的类型)
	coreActual := []string{}
	extras := []string{}
	for _, t := range actual {
		if isCoreEvent(t) {
			coreActual = append(coreActual, t)
		} else if !knownExtras[t] {
			extras = append(extras, t)
		}
	}

	// 检查设计序列中的关键事件是否出现 (按相对顺序)
	missing := []string{}
	for _, expected := range designedCoreSequence {
		if expected == "task_completed" {
			if !contains(coreActual, "task_completed") && !contains(coreActual, "task_failed") {
				missing = append(missing, "task_completed/task_failed")
			}
			continue
		}
		if !contains(coreActual, expected) {
			missing = append(missing, expected)
		}
	}

	// 检查局部顺序约束: 每对相邻核心事件 (a, b) 满足 designIndex(a) <= designIndex(b)
	// 且若 a != b 则 designIndex(a) < designIndex(b)。
	// task_failed 映射为 task_completed 用于顺序比较。
	// 特例: step_complete → step_started 是 ReAct Loop 的正常迭代 (一步完成 → 下一步开始)，
	//       不算乱序。
	orderIssues := []string{}
	designIdx := func(t string) int {
		if t == "task_failed" {
			t = "task_completed"
		}
		for i, e := range designedCoreSequence {
			if e == t {
				return i
			}
		}
		return -1
	}
	for i := 0; i+1 < len(coreActual); i++ {
		a := coreActual[i]
		b := coreActual[i+1]
		ia, ib := designIdx(a), designIdx(b)
		if ia < 0 || ib < 0 {
			continue
		}
		if a == b {
			// 同事件重复 (多个 step_started, llm_delta 等) — 允许
			continue
		}
		// 特例: step_complete → step_started 是 Loop 正常迭代
		if a == "step_complete" && b == "step_started" {
			continue
		}
		// 特例: step_complete → observation 是"最终答案"路径的合法顺序。
		// 当 LLM 在某步返回纯文本 (无 tool_call) 时，引擎先发 step_complete
		// (结束 think 步)，再发 observation (最终答案)，最后 task_completed。
		// 见 engine.go think() 末尾 + Run() len(toolCalls)==0 分支。
		if a == "step_complete" && b == "observation" {
			continue
		}
		// 允许同一位置或前进; 禁止后退
		if ib < ia {
			orderIssues = append(orderIssues, fmt.Sprintf("%s@%d → %s@%d (后退)", a, i, b, i+1))
		}
	}

	detail := fmt.Sprintf("核心事件=%v, 缺失=%v, 多余(非已知)=%v, 乱序=%v",
		coreActual, missing, extras, orderIssues)

	pass := len(orderIssues) == 0
	return checkResult{
		name:   "与设计序列对比 (缺失/多余/乱序)",
		pass:   pass,
		detail: detail,
	}
}

// isCoreEvent 判断事件类型是否属于设计核心序列。
func isCoreEvent(t string) bool {
	for _, s := range designedCoreSequence {
		if t == s || (t == "task_failed" && s == "task_completed") {
			return true
		}
	}
	return false
}

// isSubsequence 检查 actual 是否是 expected 的子序列 (保持相对顺序)。
func isSubsequence(actual, expected []string) bool {
	// 去重 actual (连续重复的合并)
	deduped := []string{}
	for _, t := range actual {
		if len(deduped) == 0 || deduped[len(deduped)-1] != t {
			deduped = append(deduped, t)
		}
	}
	// 检查 deduped 中的每个事件在 expected 中的索引是否单调递增
	prevIdx := -1
	for _, t := range deduped {
		idx := -1
		for i, e := range expected {
			if e == t {
				idx = i
				break
			}
		}
		if idx == -1 {
			continue // 不在 expected 中 (如 task_failed 映射后)
		}
		if idx < prevIdx {
			return false
		}
		prevIdx = idx
	}
	return true
}

// printScenarioResult 打印一次测试场景的详细结果。
func printScenarioResult(r scenarioResult) {
	fmt.Println()
	fmt.Printf("  --- %s 详细检查 ---\n", r.name)
	for _, c := range r.checks {
		status := "PASS"
		if !c.pass {
			status = "FAIL"
		}
		fmt.Printf("    [%s] %s\n", status, c.name)
		fmt.Printf("           %s\n", c.detail)
	}
	fmt.Printf("  结论: %s\n", passFail(r.pass))
}

// --- 辅助函数 ---

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

func indexOf(list []string, item string) int {
	for i, v := range list {
		if v == item {
			return i
		}
	}
	return -1
}

func countPrefix(list []string, prefix string) int {
	n := 0
	for _, v := range list {
		if strings.HasPrefix(v, prefix) {
			n++
		}
	}
	return n
}

func indexOfPrefix(list []string, prefix string) int {
	for i, v := range list {
		if strings.HasPrefix(v, prefix) {
			return i
		}
	}
	return -1
}

func passFail(pass bool) string {
	if pass {
		return "PASS"
	}
	return "FAIL"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// indent 在每行前加前缀，用于格式化日志输出。
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
