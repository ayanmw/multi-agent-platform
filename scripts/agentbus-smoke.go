// AgentBus smoke test —— 端到端验证 agent 之间的消息传递。
//
// 策略：
//   1. 在隔离端口 + 临时 DB 上以 mock 模式启动 server。
//   2. 注入两个 mock 脚本：
//      - agent-a：单条文本响应，内容为 "AGENT_A_RESULT_MARK"
//      - agent-b：期望收到的 user input 中包含 "AGENT_A_RESULT_MARK"
//   3. POST /api/multi-agent，带两个 AgentSpec，其中 agent_a 的 OutputTo=["agent_b"]。
//   4. 轮询直到 root task 完成。
//   5. 断言 agent_b 的最终结果或 steps 中包含 AGENT_A_RESULT_MARK。
//
// 运行：go run scripts/agentbus-smoke.go
//go:build abusmoke || ignore

// AgentBus smoke test —— 端到端验证 agent 之间的消息传递。
//
// 策略：
//   1. 在隔离端口 + 临时 DB 上以 mock 模式启动 server。
//   2. 注入两个 mock 脚本：
//      - agent-a：单条文本响应，内容为 "AGENT_A_RESULT_MARK"
//      - agent-b：期望收到的 user input 中包含 "AGENT_A_RESULT_MARK"
//   3. POST /api/multi-agent，带两个 AgentSpec，其中 agent_a 的 OutputTo=["agent_b"]。
//   4. 轮询直到 root task 完成。
//   5. 断言 agent_b 的最终结果或 steps 中包含 AGENT_A_RESULT_MARK。
//
// 本文件使用 `//go:build abusmoke || ignore` 构建约束隔离，避免与同目录其它
// package main 文件在默认 `go test ./...` 构建时因 main 重复声明而冲突。
// 需要运行本脚本时显式指定 tag: `go run -tags abusmoke scripts/agentbus-smoke.go`。
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
	"time"
)

const (
	abusPort    = "18104"
	abusBaseURL = "http://localhost:" + abusPort
)

type eventData struct {
	Type       string `json:"type"`
	FromAgent  string `json:"from_agent,omitempty"`
	ToAgent    string `json:"to_agent,omitempty"`
	MsgType    string `json:"msg_type,omitempty"`
	Content    string `json:"content,omitempty"`
}

type event struct {
	Type    string                 `json:"type"`
	AgentID string                 `json:"agent_id"`
	Data    map[string]interface{} `json:"data"`
}

type taskResponse struct {
	Task   taskInfo   `json:"task"`
	Steps  []stepInfo `json:"steps"`
}

type taskInfo struct {
	ID      string   `json:"id"`
	Status  string   `json:"status"`
	Result  string   `json:"result"`
	AgentIDs []string `json:"agent_ids"`
}

type stepInfo struct {
	AgentID  string `json:"agent_id"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	ToolName string `json:"tool_name,omitempty"`
}

func main() {
	serverBin := filepath.Join(os.TempDir(), fmt.Sprintf("agentbus-server-%d.exe", os.Getpid()))
	logPath := filepath.Join(os.TempDir(), fmt.Sprintf("agentbus-server-%d.log", os.Getpid()))
	dbPath := filepath.Join(os.TempDir(), fmt.Sprintf("agentbus-smoke-%d.db", os.Getpid()))

	cleanup := func() {
		os.Remove(serverBin)
		os.Remove(logPath)
		os.Remove(dbPath)
	}
	defer cleanup()

	fmt.Println("[setup] 编译后端服务...")
	if err := exec.Command("go", "build", "-o", serverBin, "./cmd/server").Run(); err != nil {
		log.Fatalf("[FATAL] 编译失败: %v", err)
	}
	fmt.Println("[setup] 编译成功")

	cmd := exec.Command(serverBin)
	cmd.Env = append(os.Environ(),
		"LLM_USE_MOCK=true",
		"REQUIRE_AUTH=false",
		"SERVER_PORT="+abusPort,
		"DB_PATH="+dbPath,
	)
	logFile, _ := os.Create(logPath)
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		log.Fatalf("[FATAL] 启动服务失败: %v", err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()

	fmt.Println("[setup] 等待 /healthz 就绪...")
	if !waitForHealth(30) {
		log.Fatalf("[FATAL] 服务 30s 内未就绪")
	}
	fmt.Println("[setup] 服务就绪 OK")

	fmt.Println("\n===== 注入 Mock 脚本 =====")
	injectMockScript("agent-a", "agent_a_input", "AGENT_A_RESULT_MARK: research summary from agent A")
	injectMockScript("agent-b", "AGENT_A_RESULT_MARK", "AGENT_B_RECEIVED_MARK: agent B saw the AgentBus message")

	fmt.Println("\n===== 启动 multi-agent 任务（agent_a -> agent_b 通过 AgentBus） =====")
	reqBody := map[string]interface{}{
		"action": "multi-agent",
		"input":  "agent_a_input",
		"agents": []map[string]interface{}{
			{
				"agent_id":     "agent_a",
				"name":         "Agent A",
				"system_prompt": "You are agent A. Reply with the exact text response.",
				"input":        "agent_a_input",
				"output_to":    []string{"agent_b"},
			},
			{
				"agent_id":     "agent_b",
				"name":         "Agent B",
				"system_prompt": "You are agent B. Confirm if you received the message from agent A.",
				"input":        "agent_b_input",
			},
		},
	}
	resp, err := postJSON(abusBaseURL+"/api/multi-agent", reqBody)
	if err != nil {
		log.Fatalf("[FATAL] POST /api/multi-agent 失败: %v", err)
	}
	taskID := resp["task_id"].(string)
	fmt.Printf("  task_id=%s, response agent_count=%v agent_ids=%v\n", taskID, resp["agent_count"], resp["agent_ids"])

	fmt.Println("\n===== 轮询 root task 直到完成（最多 60s） =====")
	rootStatus := pollTaskStatus(taskID, 60)
	fmt.Printf("  root task final status=%s\n", rootStatus)

	fmt.Println("\n===== 检查 AgentBus 结果传递 =====")
	childB := getTaskDetail(taskID + "_agent_b")
	fmt.Printf("  agent_b result: %s\n", childB.Task.Result)

	pass, fail := 0, 0
	findings := []string{}

	if strings.Contains(childB.Task.Result, "AGENT_B_RECEIVED_MARK") {
		fmt.Println("[PASS] AgentBus message delivery    agent_b result contains AGENT_B_RECEIVED_MARK")
		pass++
	} else {
		stepsContent := ""
		for _, s := range childB.Steps {
			stepsContent += " " + s.Content
		}
		if strings.Contains(stepsContent, "AGENT_A_RESULT_MARK") {
			fmt.Println("[PASS] AgentBus message delivery    agent_b steps contain AGENT_A_RESULT_MARK")
			pass++
		} else {
			fmt.Printf("[FAIL] AgentBus message delivery    agent_b did not receive AGENT_A_RESULT_MARK (result=%s steps=%s)\n", childB.Task.Result, stepsContent)
			fail++
			findings = append(findings, "agent_b did not receive or react to AGENT_A_RESULT_MARK")
		}
	}

	fmt.Println("\n===== 汇总 =====")
	fmt.Printf("----------------------------------------\n")
	fmt.Printf("  PASS : %d\n", pass)
	fmt.Printf("  FAIL : %d\n", fail)
	fmt.Printf("  SKIP : 0\n")
	fmt.Printf("----------------------------------------\n")
	if len(findings) > 0 {
		fmt.Println("\n===== 发现的问题清单 =====")
		for _, f := range findings {
			fmt.Printf("  - %s\n", f)
		}
	}
	if fail > 0 {
		fmt.Printf("\n[agentbus-smoke] 存在 FAIL 项，服务日志：%s\n", logPath)
		os.Exit(1)
	}
	fmt.Println("\n[agentbus-smoke] 评测完成 (PASS=1, SKIP=0, FAIL=0)")
}

func waitForHealth(maxSeconds int) bool {
	for i := 0; i < maxSeconds*2; i++ {
		resp, err := http.Get(abusBaseURL + "/healthz")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return true
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func injectMockScript(id, matchInput, response string) {
	mock := map[string]interface{}{
		"id":          id,
		"case_id":     id,
		"priority":    200,
		"match_input": []string{matchInput},
		"responses": []map[string]interface{}{
			{
				"type":    "text",
				"content": response,
			},
		},
	}
	if _, err := postJSON(abusBaseURL+"/api/mock/scripts", mock); err != nil {
		log.Fatalf("[FATAL] 注入 mock %s 失败: %v", id, err)
	}
	fmt.Printf("  注入 mock script: %s\n", id)
}

func postJSON(url string, body interface{}) (map[string]interface{}, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode error: %w body=%s", err, string(b))
	}
	return out, nil
}

func pollTaskStatus(taskID string, maxSeconds int) string {
	for i := 0; i < maxSeconds; i++ {
		detail := getTaskDetail(taskID)
		if detail.Task.Status != "running" && detail.Task.Status != "" {
			return detail.Task.Status
		}
		time.Sleep(1 * time.Second)
	}
	return "timeout"
}

func getTaskDetail(taskID string) taskResponse {
	resp, err := http.Get(abusBaseURL + "/api/tasks?id=" + taskID)
	if err != nil {
		log.Printf("[WARN] GET /api/tasks?id=%s failed: %v", taskID, err)
		return taskResponse{}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out taskResponse
	if err := json.Unmarshal(b, &out); err != nil {
		log.Printf("[WARN] parse task detail failed: %v body=%s", err, string(b))
	}
	return out
}
