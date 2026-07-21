// action.go — ActionRunner：按 action_type 执行定时器的具体动作。
//
// 四种 action_type：
//   - start_task     调用注入的 TaskStarter 启动一个 Agent task（复用 chat 链路）
//   - script         按顺序执行一组白名单 tool 调用，复用现有 run_shell 等 tool 的 sandbox/policy
//   - webhook        向外部 URL 发起 HTTP 请求，支持模板化的 method/headers/body
//   - notify_session 向指定 session 广播 cron_notification 事件并写入一条系统消息
//
// 设计原则：ActionRunner 不直接依赖 pkg/db / runtime / cmd/server，所有外部能力
// 通过接口/函数注入（TaskStarter、SessionMessageWriter、EventBus、tool.Registry），
// 使其可在单元测试中用 mock 隔离。
package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// TaskStarter 是 start_task action 用来启动一个 Agent task 的回调。
// 由 cmd/server 注入，实际复用 startChatTask 链路。
// input 已渲染好（含 [cron:<id>:<name>] 前缀），sessionID 为空表示新建 session。
// 返回新 task 的 taskID 与 sessionID（可能与传入不同——新建 session 时）。
type TaskStarter func(ctx context.Context, params StartTaskParams) (taskID, sessionID string, err error)

// StartTaskParams 是 TaskStarter 的入参，与 cmd/server.StartTaskOpts 字段对齐。
// Input 已经过模板渲染并加上 cron 前缀。
type StartTaskParams struct {
	AgentID        string
	Input          string
	SystemPrompt   string
	SessionID      string
	MaxSteps       int
	TimeoutSeconds int
	Scope          string
	AllowedTools   []string
	TokenBudget    int
	CostBudgetUSD  float64
	CaseID         string
	CronID         string
	CronName       string
}

// SessionMessageWriter 是 notify_session action 用来写入 session 系统消息的回调。
// 由 cmd/server 注入（实现里调 db 写 session_messages），避免本包直接依赖 db。
type SessionMessageWriter interface {
	InsertSystemMessage(sessionID, content string) error
}

// ActionResult 是单次 action 执行的结果，供 Executor 写入 execution 记录。
type ActionResult struct {
	TaskID    string
	SessionID string
	Summary   string
}

// ActionRunner 执行四种 action。
type ActionRunner struct {
	tools          *tool.Registry
	allowedTools   map[string]bool
	webhookTimeout time.Duration
	maxResultChars int
	bus            EventBus
	startTask      TaskStarter
	msgWriter      SessionMessageWriter
}

// ActionRunnerConfig 是 ActionRunner 的构造参数。
type ActionRunnerConfig struct {
	Tools           *tool.Registry
	AllowedTools    []string
	WebhookTimeout  time.Duration
	MaxResultChars  int
	Bus             EventBus
	StartTask       TaskStarter
	MessageWriter   SessionMessageWriter
}

// NewActionRunner 创建 ActionRunner。
func NewActionRunner(cfg ActionRunnerConfig) *ActionRunner {
	allowed := make(map[string]bool, len(cfg.AllowedTools))
	for _, t := range cfg.AllowedTools {
		allowed[t] = true
	}
	timeout := cfg.WebhookTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	maxChars := cfg.MaxResultChars
	if maxChars <= 0 {
		maxChars = 2000
	}
	return &ActionRunner{
		tools:          cfg.Tools,
		allowedTools:   allowed,
		webhookTimeout: timeout,
		maxResultChars: maxChars,
		bus:            cfg.Bus,
		startTask:      cfg.StartTask,
		msgWriter:      cfg.MessageWriter,
	}
}

// Run 按 cron 的 action_type 执行已渲染好的 payload。
// renderedPayload 是经过 RenderMap 渲染后的 action_payload。
func (r *ActionRunner) Run(ctx context.Context, c Cron, renderedPayload map[string]any) (ActionResult, error) {
	switch c.ActionType {
	case ActionStartTask:
		return r.runStartTask(ctx, c, renderedPayload)
	case ActionScript:
		return r.runScript(ctx, c, renderedPayload)
	case ActionWebhook:
		return r.runWebhook(ctx, c, renderedPayload)
	case ActionNotifySession:
		return r.runNotifySession(ctx, c, renderedPayload)
	default:
		return ActionResult{}, fmt.Errorf("unknown action_type: %s", c.ActionType)
	}
}

// runStartTask 解析 StartTaskPayload 并调用 TaskStarter。
func (r *ActionRunner) runStartTask(ctx context.Context, c Cron, payload map[string]any) (ActionResult, error) {
	p, err := decodeStartTaskPayload(payload)
	if err != nil {
		return ActionResult{}, err
	}
	if p.AgentID == "" {
		return ActionResult{}, fmt.Errorf("start_task payload: agent_id is required")
	}
	// 给 input 加 cron 溯源前缀，便于在 task 列表 / trace 中识别来源。
	input := p.Input
	prefix := fmt.Sprintf("[cron:%s:%s] ", c.ID, c.Name)
	if input != "" && !strings.HasPrefix(input, prefix) {
		input = prefix + input
	}
	taskID, sessionID, err := r.startTask(ctx, StartTaskParams{
		AgentID:        p.AgentID,
		Input:          input,
		SystemPrompt:   p.SystemPrompt,
		SessionID:      p.SessionID,
		MaxSteps:       p.MaxSteps,
		TimeoutSeconds: p.TimeoutSeconds,
		Scope:          p.Scope,
		AllowedTools:   p.AllowedTools,
		TokenBudget:    p.TokenBudget,
		CostBudgetUSD:  p.CostBudgetUSD,
		CaseID:         p.CaseID,
		CronID:         c.ID,
		CronName:       c.Name,
	})
	if err != nil {
		return ActionResult{}, fmt.Errorf("start_task: %w", err)
	}
	return ActionResult{
		TaskID:    taskID,
		SessionID: sessionID,
		Summary:   fmt.Sprintf("task started: %s", taskID),
	}, nil
}

// decodeStartTaskPayload 把 map[string]any 解码为 StartTaskPayload。
// 兼容 JSON 数字被解码为 float64 的情况。
func decodeStartTaskPayload(payload map[string]any) (StartTaskPayload, error) {
	var p StartTaskPayload
	if payload == nil {
		return p, fmt.Errorf("start_task payload is empty")
	}
	p.AgentID, _ = payload["agent_id"].(string)
	p.SessionID, _ = payload["session_id"].(string)
	p.Input, _ = payload["input"].(string)
	p.SystemPrompt, _ = payload["system_prompt"].(string)
	p.Scope, _ = payload["scope"].(string)
	p.CaseID, _ = payload["case_id"].(string)
	p.MaxSteps = toInt(payload["max_steps"])
	p.TimeoutSeconds = toInt(payload["timeout_seconds"])
	p.TokenBudget = toInt(payload["token_budget"])
	if v, ok := payload["cost_budget_usd"].(float64); ok {
		p.CostBudgetUSD = v
	}
	if raw, ok := payload["allowed_tools"].([]any); ok {
		for _, item := range raw {
			if s, ok := item.(string); ok {
				p.AllowedTools = append(p.AllowedTools, s)
			}
		}
	}
	return p, nil
}

// toInt 从 map 值提取 int，兼容 float64/int/int64。
func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// runScript 顺序执行白名单 tool 调用，汇总结果。
func (r *ActionRunner) runScript(ctx context.Context, c Cron, payload map[string]any) (ActionResult, error) {
	if r.tools == nil {
		return ActionResult{}, fmt.Errorf("script action: tool registry not configured")
	}
	var sp ScriptPayload
	if raw, ok := payload["tool_calls"].([]any); ok {
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				return ActionResult{}, fmt.Errorf("script: tool_call entry must be object")
			}
			tc := ScriptToolCall{}
			tc.Tool, _ = m["tool"].(string)
			if in, ok := m["input"].(map[string]any); ok {
				tc.Input = in
			}
			if b, ok := m["approval"].(bool); ok {
				tc.Approval = b
			}
			sp.ToolCalls = append(sp.ToolCalls, tc)
		}
	}
	if len(sp.ToolCalls) == 0 {
		return ActionResult{}, fmt.Errorf("script: tool_calls is empty")
	}
	var summaries []string
	for i, tc := range sp.ToolCalls {
		if tc.Tool == "" {
			return ActionResult{}, fmt.Errorf("script: tool_calls[%d].tool is empty", i)
		}
		if !r.allowedTools[tc.Tool] {
			return ActionResult{}, fmt.Errorf("script: tool %q not allowed", tc.Tool)
		}
		res, err := r.tools.Execute(tc.Tool, tc.Input)
		if err != nil {
			return ActionResult{}, fmt.Errorf("script: tool %q failed: %w", tc.Tool, err)
		}
		summaries = append(summaries, fmt.Sprintf("%s -> %v", tc.Tool, res))
	}
	combined := strings.Join(summaries, "; ")
	return ActionResult{Summary: truncate(combined, r.maxResultChars)}, nil
}

// runWebhook 向外部 URL 发起 HTTP 请求。
func (r *ActionRunner) runWebhook(ctx context.Context, c Cron, payload map[string]any) (ActionResult, error) {
	var wp WebhookPayload
	wp.Method, _ = payload["method"].(string)
	wp.URL, _ = payload["url"].(string)
	wp.Body, _ = payload["body"].(string)
	wp.TimeoutSeconds = toInt(payload["timeout_seconds"])
	if hdrs, ok := payload["headers"].(map[string]any); ok {
		wp.Headers = make(map[string]string, len(hdrs))
		for k, v := range hdrs {
			if s, ok := v.(string); ok {
				wp.Headers[k] = s
			}
		}
	}
	if wp.URL == "" {
		return ActionResult{}, fmt.Errorf("webhook: url is required")
	}
	if wp.Method == "" {
		wp.Method = "POST"
	}
	timeout := r.webhookTimeout
	if wp.TimeoutSeconds > 0 {
		timeout = time.Duration(wp.TimeoutSeconds) * time.Second
	}
	httpCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var body io.Reader
	if wp.Body != "" {
		body = strings.NewReader(wp.Body)
	}
	req, err := http.NewRequestWithContext(httpCtx, wp.Method, wp.URL, body)
	if err != nil {
		return ActionResult{}, fmt.Errorf("webhook: build request: %w", err)
	}
	for k, v := range wp.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" && wp.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return ActionResult{}, fmt.Errorf("webhook: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, int64(r.maxResultChars)))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ActionResult{}, fmt.Errorf("webhook: non-2xx status %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	return ActionResult{Summary: fmt.Sprintf("webhook %s -> %d", wp.URL, resp.StatusCode)}, nil
}

// runNotifySession 向 session 广播通知事件并写入系统消息。
func (r *ActionRunner) runNotifySession(ctx context.Context, c Cron, payload map[string]any) (ActionResult, error) {
	var np NotifySessionPayload
	np.SessionID, _ = payload["session_id"].(string)
	np.Message, _ = payload["message"].(string)
	np.EventType, _ = payload["event_type"].(string)
	if np.SessionID == "" {
		return ActionResult{}, fmt.Errorf("notify_session: session_id is required")
	}
	if np.EventType == "" {
		np.EventType = "cron_notification"
	}
	// 广播事件：TaskID 填 cron_id，让前端按 cron 维度路由；
	// data 里也带 session_id，便于 dock 面板按 session 过滤。
	if r.bus != nil {
		r.bus.SendEvent(event.Event{
			EventID:   event.NewEvent("", "", "", 0, nil).EventID,
			TaskID:    c.ID,
			AgentID:   "cron",
			Type:      np.EventType,
			Timestamp: time.Now().UnixMilli(),
			Data: map[string]any{
				"cron_id":    c.ID,
				"cron_name":  c.Name,
				"session_id": np.SessionID,
				"message":    np.Message,
			},
		})
	}
	// 写入 session 系统消息，让下次会话仍可见。
	if r.msgWriter != nil {
		if err := r.msgWriter.InsertSystemMessage(np.SessionID, np.Message); err != nil {
			return ActionResult{}, fmt.Errorf("notify_session: write message: %w", err)
		}
	}
	return ActionResult{SessionID: np.SessionID, Summary: fmt.Sprintf("notified session %s", np.SessionID)}, nil
}

// truncate 按字节截断到 maxChars 上限，UTF-8 安全（按 rune 边界）。
func truncate(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	// 按 rune 截断避免切断多字节字符。
	r := []rune(s)
	if len(r) <= maxChars {
		return s
	}
	return string(r[:maxChars]) + "..."
}

// MarshalPayloadForLog 把 payload 序列化为紧凑 JSON，供日志/调试用。
func MarshalPayloadForLog(payload map[string]any) string {
	b, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return truncate(string(b), 500)
}
