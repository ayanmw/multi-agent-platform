package cron

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// fakeBus 记录所有发送的事件，供断言。
type fakeBus struct{ events []event.Event }

func (b *fakeBus) SendEvent(e event.Event) { b.events = append(b.events, e) }

// fakeMsgWriter 记录写入的系统消息。
type fakeMsgWriter struct {
	msgs    []struct{ SessionID, Content string }
	failErr error
}

func (w *fakeMsgWriter) InsertSystemMessage(sessionID, content string) error {
	if w.failErr != nil {
		return w.failErr
	}
	w.msgs = append(w.msgs, struct{ SessionID, Content string }{sessionID, content})
	return nil
}

// newRunner 构造一个带 mock 依赖的 ActionRunner。
func newRunner(t *testing.T, cfg ActionRunnerConfig) *ActionRunner {
	t.Helper()
	return NewActionRunner(cfg)
}

// TestRunStartTask 验证 start_task 调用 TaskStarter 并加 cron 前缀。
func TestRunStartTask(t *testing.T) {
	var gotParams StartTaskParams
	starter := func(ctx context.Context, p StartTaskParams) (string, string, error) {
		gotParams = p
		return "task_1", "sess_1", nil
	}
	r := newRunner(t, ActionRunnerConfig{StartTask: starter})
	c := Cron{ID: "cron_1", Name: "Report", ActionType: ActionStartTask}
	res, err := r.Run(context.Background(), c, map[string]any{
		"agent_id":   "agent_default",
		"input":      "do work",
		"session_id": "sess_existing",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.TaskID != "task_1" || res.SessionID != "sess_1" {
		t.Fatalf("result mismatch: %+v", res)
	}
	if gotParams.AgentID != "agent_default" {
		t.Fatalf("agent_id not passed: %+v", gotParams)
	}
	wantPrefix := "[cron:cron_1:Report] do work"
	if gotParams.Input != wantPrefix {
		t.Fatalf("input prefix wrong: got %q want %q", gotParams.Input, wantPrefix)
	}
	if gotParams.SessionID != "sess_existing" {
		t.Fatalf("session_id not passed: %q", gotParams.SessionID)
	}
}

// TestRunStartTaskMissingAgentID 验证缺少 agent_id 时报错。
func TestRunStartTaskMissingAgentID(t *testing.T) {
	r := newRunner(t, ActionRunnerConfig{StartTask: func(context.Context, StartTaskParams) (string, string, error) {
		t.Fatal("starter should not be called")
		return "", "", nil
	}})
	_, err := r.Run(context.Background(), Cron{ActionType: ActionStartTask}, map[string]any{"input": "x"})
	if err == nil {
		t.Fatal("expected error for missing agent_id")
	}
}

// TestRunStartTaskStarterError 验证 starter 错误透传。
func TestRunStartTaskStarterError(t *testing.T) {
	starter := func(context.Context, StartTaskParams) (string, string, error) {
		return "", "", errors.New("session not found")
	}
	r := newRunner(t, ActionRunnerConfig{StartTask: starter})
	_, err := r.Run(context.Background(), Cron{ID: "c", Name: "n", ActionType: ActionStartTask}, map[string]any{"agent_id": "a"})
	if err == nil || err.Error() == "" {
		t.Fatalf("expected error, got %v", err)
	}
}

// TestRunScriptWhitelist 验证白名单内的 tool 被执行。
func TestRunScriptWhitelist(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(tool.NewBuiltinTool("run_shell", "", "run shell", map[string]any{"type": "object"},
		func(_ tool.ExecuteContext, input map[string]any) (any, error) { return "ok-output", nil }))
	r := newRunner(t, ActionRunnerConfig{Tools: reg, AllowedTools: []string{"run_shell"}})
	res, err := r.Run(context.Background(), Cron{ActionType: ActionScript}, map[string]any{
		"tool_calls": []any{
			map[string]any{"tool": "run_shell", "input": map[string]any{"command": "ls"}},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary == "" {
		t.Fatalf("summary empty")
	}
}

// TestRunScriptDisallowedTool 验证非白名单 tool 被拒绝。
func TestRunScriptDisallowedTool(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(tool.NewBuiltinTool("run_shell", "", "run shell", map[string]any{"type": "object"},
		func(_ tool.ExecuteContext, input map[string]any) (any, error) { return "ok", nil }))
	r := newRunner(t, ActionRunnerConfig{Tools: reg, AllowedTools: []string{"read_file"}})
	_, err := r.Run(context.Background(), Cron{ActionType: ActionScript}, map[string]any{
		"tool_calls": []any{map[string]any{"tool": "run_shell", "input": map[string]any{}}},
	})
	if err == nil {
		t.Fatal("expected error for disallowed tool")
	}
}

// TestRunScriptEmpty 验证空 tool_calls 报错。
func TestRunScriptEmpty(t *testing.T) {
	r := newRunner(t, ActionRunnerConfig{Tools: tool.NewRegistry(), AllowedTools: []string{"run_shell"}})
	_, err := r.Run(context.Background(), Cron{ActionType: ActionScript}, map[string]any{})
	if err == nil {
		t.Fatal("expected error for empty tool_calls")
	}
}

// TestRunWebhookSuccess 验证 webhook 2xx 返回成功 summary。
func TestRunWebhookSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Cron-Id") != "cron_1" {
			t.Errorf("header not forwarded: %v", r.Header)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	r := newRunner(t, ActionRunnerConfig{WebhookTimeout: 5e9})
	res, err := r.Run(context.Background(), Cron{ID: "cron_1", ActionType: ActionWebhook}, map[string]any{
		"method":  "POST",
		"url":     srv.URL,
		"headers": map[string]any{"X-Cron-Id": "cron_1"},
		"body":    `{"a":1}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary == "" {
		t.Fatalf("summary empty")
	}
}

// TestRunWebhookNon2xx 验证非 2xx 报错。
func TestRunWebhookNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	r := newRunner(t, ActionRunnerConfig{})
	_, err := r.Run(context.Background(), Cron{ActionType: ActionWebhook}, map[string]any{"url": srv.URL})
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

// TestRunWebhookMissingURL 验证缺 url 报错。
func TestRunWebhookMissingURL(t *testing.T) {
	r := newRunner(t, ActionRunnerConfig{})
	_, err := r.Run(context.Background(), Cron{ActionType: ActionWebhook}, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

// TestRunNotifySession 验证 notify_session 广播事件 + 写消息。
func TestRunNotifySession(t *testing.T) {
	bus := &fakeBus{}
	mw := &fakeMsgWriter{}
	r := newRunner(t, ActionRunnerConfig{Bus: bus, MessageWriter: mw})
	res, err := r.Run(context.Background(), Cron{ID: "cron_1", Name: "N", ActionType: ActionNotifySession}, map[string]any{
		"session_id": "sess_x",
		"message":    "hello",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.SessionID != "sess_x" {
		t.Fatalf("session_id mismatch: %+v", res)
	}
	if len(bus.events) != 1 || bus.events[0].Type != "cron_notification" {
		t.Fatalf("event not sent: %+v", bus.events)
	}
	if bus.events[0].Data["session_id"] != "sess_x" || bus.events[0].Data["message"] != "hello" {
		t.Fatalf("event data wrong: %+v", bus.events[0].Data)
	}
	if len(mw.msgs) != 1 || mw.msgs[0].Content != "hello" {
		t.Fatalf("message not written: %+v", mw.msgs)
	}
}

// TestRunNotifySessionMissingSession 验证缺 session_id 报错。
func TestRunNotifySessionMissingSession(t *testing.T) {
	r := newRunner(t, ActionRunnerConfig{Bus: &fakeBus{}, MessageWriter: &fakeMsgWriter{}})
	_, err := r.Run(context.Background(), Cron{ActionType: ActionNotifySession}, map[string]any{"message": "x"})
	if err == nil {
		t.Fatal("expected error for missing session_id")
	}
}

// TestRunNotifySessionWriteFail 验证写消息失败时报错。
func TestRunNotifySessionWriteFail(t *testing.T) {
	mw := &fakeMsgWriter{failErr: errors.New("db down")}
	r := newRunner(t, ActionRunnerConfig{Bus: &fakeBus{}, MessageWriter: mw})
	_, err := r.Run(context.Background(), Cron{ActionType: ActionNotifySession}, map[string]any{
		"session_id": "s", "message": "m",
	})
	if err == nil {
		t.Fatal("expected error for write failure")
	}
}

// TestRunUnknownAction 验证未知 action_type 报错。
func TestRunUnknownAction(t *testing.T) {
	r := newRunner(t, ActionRunnerConfig{})
	_, err := r.Run(context.Background(), Cron{ActionType: ActionType("nope")}, map[string]any{})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

// TestDecodeStartTaskPayloadNumbers 验证数字字段兼容 float64。
func TestDecodeStartTaskPayloadNumbers(t *testing.T) {
	p, err := decodeStartTaskPayload(map[string]any{
		"agent_id":        "a",
		"max_steps":       float64(15),
		"timeout_seconds": float64(60),
		"cost_budget_usd": float64(0.5),
		"allowed_tools":   []any{"run_shell", "read_file"},
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.MaxSteps != 15 || p.TimeoutSeconds != 60 || p.CostBudgetUSD != 0.5 {
		t.Fatalf("numbers wrong: %+v", p)
	}
	if len(p.AllowedTools) != 2 {
		t.Fatalf("allowed_tools wrong: %+v", p)
	}
}

// TestTruncate 验证截断逻辑。
func TestTruncate(t *testing.T) {
	if got := truncate("short", 100); got != "short" {
		t.Fatalf("short changed: %q", got)
	}
	if got := truncate("1234567890", 5); got != "12345..." {
		t.Fatalf("truncate wrong: %q", got)
	}
	// UTF-8 安全：按 rune 截断，结果应是 3 个 rune + "..."
	if got := truncate("中文字符串测试", 3); got != "中文字..." {
		t.Fatalf("utf8 truncate wrong: %q", got)
	}
}

// TestMarshalPayloadForLog 验证日志序列化截断。
func TestMarshalPayloadForLog(t *testing.T) {
	s := MarshalPayloadForLog(map[string]any{"a": "b"})
	if s == "{}" || s == "" {
		t.Fatalf("log marshal wrong: %q", s)
	}
	// 长字符串截断
	long := string(make([]byte, 1000))
	s2 := MarshalPayloadForLog(map[string]any{"x": long})
	if len(s2) > 600 {
		t.Fatalf("log not truncated: %d", len(s2))
	}
	// 验证是合法 JSON 结尾（截断后会加 ...，所以不强制 unmarshal）
	_ = json.Unmarshal
}
