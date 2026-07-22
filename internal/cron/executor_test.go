package cron

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// mockRunner 是 ActionRunner mock，可配置返回结果/错误。
type mockRunner struct {
	mu       sync.Mutex
	calls    []Cron
	result   ActionResult
	err      error
	delayFn  func() // 可选：模拟执行耗时
}

func (m *mockRunner) Run(ctx context.Context, c Cron, payload map[string]any) (ActionResult, error) {
	m.mu.Lock()
	m.calls = append(m.calls, c)
	res, err := m.result, m.err
	m.mu.Unlock()
	if m.delayFn != nil {
		m.delayFn()
	}
	return res, err
}

// TestExecutorCompleted 验证正常完成流程：发 triggered/started/completed 事件，更新 cron meta。
func TestExecutorCompleted(t *testing.T) {
	store := newFakeStore()
	store.crons["c1"] = Cron{ID: "c1", Name: "N", ActionType: ActionStartTask, Status: StatusEnabled,
		ActionPayload: map[string]any{"agent_id": "agent_default", "input": "hi {{.Count}}"}}
	bus := &fakeEventBus{}
	runner := &mockRunner{result: ActionResult{TaskID: "task_1", SessionID: "sess_1", Summary: "ok"}}
	exec := NewExecutor(store, runner, bus, 100)

	res, err := exec.ExecuteOnce(context.Background(), "c1", "")
	if err != nil {
		t.Fatalf("ExecuteOnce: %v", err)
	}
	if res.Status != ExecCompleted || res.TaskID != "task_1" {
		t.Fatalf("result wrong: %+v", res)
	}

	// 事件序列：triggered, started, completed
	types := []string{}
	for _, e := range bus.events {
		types = append(types, e.Type)
	}
	wantTypes := []string{event.EventCronTriggered, event.EventCronExecutionStarted, event.EventCronExecutionCompleted}
	if len(types) != 3 || types[0] != wantTypes[0] || types[1] != wantTypes[1] || types[2] != wantTypes[2] {
		t.Fatalf("event sequence wrong: %v want %v", types, wantTypes)
	}

	// cron meta 更新
	c, _ := store.GetCron("c1")
	if c.TriggerCount != 1 || c.LastExecutionID == "" || c.LastTriggeredAt == nil {
		t.Fatalf("cron meta not updated: %+v", c)
	}

	// execution 存 DB
	execs, _ := store.ListExecutions(ExecListFilter{CronID: "c1"})
	if len(execs) != 1 || execs[0].Status != ExecCompleted {
		t.Fatalf("execution not stored: %+v", execs)
	}
	// rendered_input 应含渲染后的 "hi 1"
	if !strings.Contains(execs[0].RenderedInput, "hi 1") {
		t.Fatalf("rendered_input wrong: %q", execs[0].RenderedInput)
	}
}

// TestExecutorFailed 验证失败流程发 failed 事件、execution.status=failed。
func TestExecutorFailed(t *testing.T) {
	store := newFakeStore()
	store.crons["c1"] = Cron{ID: "c1", ActionType: ActionStartTask, Status: StatusEnabled,
		ActionPayload: map[string]any{"agent_id": "a"}}
	bus := &fakeEventBus{}
	runner := &mockRunner{err: errors.New("session not found")}
	exec := NewExecutor(store, runner, bus, 100)

	res, err := exec.ExecuteOnce(context.Background(), "c1", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if res == nil {
		t.Fatal("expected non-nil execution record even on failure")
	}
	if res.Status != ExecFailed || res.Error == "" {
		t.Fatalf("failed exec wrong: %+v", res)
	}
	last := bus.events[len(bus.events)-1]
	if last.Type != event.EventCronExecutionFailed {
		t.Fatalf("last event should be failed: %v", last.Type)
	}
	if last.Data["error"] == "" {
		t.Fatal("failed event missing error")
	}
}

// TestExecutorAutoTriggerRespectsStatus 验证自动触发只对 enabled 生效。
func TestExecutorAutoTriggerRespectsStatus(t *testing.T) {
	store := newFakeStore()
	store.crons["c1"] = Cron{ID: "c1", ActionType: ActionStartTask, Status: StatusDisabled,
		ActionPayload: map[string]any{"agent_id": "a"}}
	bus := &fakeEventBus{}
	runner := &mockRunner{}
	exec := NewExecutor(store, runner, bus, 100)

	exec.Execute(context.Background(), "c1")
	if len(runner.calls) != 0 {
		t.Fatal("disabled cron should not execute (auto)")
	}
	if len(bus.events) != 0 {
		t.Fatal("disabled cron should not emit events (auto)")
	}
}

// TestExecutorManualTriggerIgnoresStatus 验证手动触发可对 disabled 执行。
func TestExecutorManualTriggerIgnoresStatus(t *testing.T) {
	store := newFakeStore()
	store.crons["c1"] = Cron{ID: "c1", ActionType: ActionStartTask, Status: StatusDisabled,
		ActionPayload: map[string]any{"agent_id": "a"}}
	bus := &fakeEventBus{}
	runner := &mockRunner{result: ActionResult{Summary: "ok"}}
	exec := NewExecutor(store, runner, bus, 100)

	if _, err := exec.ExecuteOnce(context.Background(), "c1", ""); err != nil {
		t.Fatalf("manual trigger disabled cron: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatal("manual trigger should run disabled cron")
	}
}

// TestExecutorSkipOnConcurrent 验证串行 skip。
// 第一次执行通过一个手动调用 doExecute 占住 running 标记（用阻塞 runner），
// 第二次自动触发进来时应直接 skip。
func TestExecutorSkipOnConcurrent(t *testing.T) {
	store := newFakeStore()
	store.crons["c1"] = Cron{ID: "c1", ActionType: ActionScript, Status: StatusEnabled,
		AllowConcurrent: false, ActionPayload: map[string]any{"tool_calls": []any{}}}
	bus := &fakeEventBus{}

	block := make(chan struct{})
	started := make(chan struct{})
	runner := &mockRunner{result: ActionResult{Summary: "ok"}, delayFn: func() {
		close(started)
		<-block
	}}
	exec := NewExecutor(store, runner, bus, 100)

	// 第一次：自动触发，会阻塞在 runner
	go exec.Execute(context.Background(), "c1")

	// 等第一次真正进入 running（runner 已被调用）
	<-started

	// 第二次自动触发：应 skip（不调 runner，直接返回）
	exec.Execute(context.Background(), "c1")

	// 放行第一次
	close(block)

	// 第二次应记 skipped
	execs, _ := store.ListExecutions(ExecListFilter{CronID: "c1"})
	skipped := 0
	for _, e := range execs {
		if e.Status == ExecSkipped {
			skipped++
		}
	}
	if skipped == 0 {
		t.Fatalf("expected at least 1 skipped execution, got %+v", execs)
	}
}

// TestExecutorOverrideInput 验证手动触发 overrideInput 覆盖 start_task input。
func TestExecutorOverrideInput(t *testing.T) {
	store := newFakeStore()
	store.crons["c1"] = Cron{ID: "c1", ActionType: ActionStartTask, Status: StatusEnabled,
		ActionPayload: map[string]any{"agent_id": "a", "input": "original"}}
	bus := &fakeEventBus{}
	var captured map[string]any
	// 用自定义 runner 捕获 payload
	exec := NewExecutor(store, &captureRunner{fn: func(p map[string]any) { captured = p }}, bus, 100)

	if _, err := exec.ExecuteOnce(context.Background(), "c1", "overridden"); err != nil {
		t.Fatalf("ExecuteOnce: %v", err)
	}
	if captured["input"] != "overridden" {
		t.Fatalf("override not applied: %v", captured["input"])
	}
}

// captureRunner 捕获渲染后的 payload。
type captureRunner struct {
	fn func(map[string]any)
}

func (r *captureRunner) Run(ctx context.Context, c Cron, payload map[string]any) (ActionResult, error) {
	r.fn(payload)
	return ActionResult{Summary: "ok"}, nil
}

// TestExecutorCronNotFound 验证 cron 不存在时报错。
func TestExecutorCronNotFound(t *testing.T) {
	store := newFakeStore()
	exec := NewExecutor(store, &mockRunner{}, &fakeEventBus{}, 100)
	if _, err := exec.ExecuteOnce(context.Background(), "nope", ""); err == nil {
		t.Fatal("expected error for missing cron")
	}
}

// TestExecutorTemplatePrevResult 验证第二次触发时 PrevResult 被注入。
func TestExecutorTemplatePrevResult(t *testing.T) {
	store := newFakeStore()
	store.crons["c1"] = Cron{ID: "c1", ActionType: ActionStartTask, Status: StatusEnabled,
		ActionPayload: map[string]any{"agent_id": "a", "input": "prev={{.PrevResult}} count={{.Count}}"}}
	bus := &fakeEventBus{}
	var captured map[string]any
	exec := NewExecutor(store, &captureRunner{fn: func(p map[string]any) { captured = p }}, bus, 100)

	// 第一次
	exec.ExecuteOnce(context.Background(), "c1", "")
	first := captured["input"].(string)
	if !strings.Contains(first, "prev= count=1") {
		t.Fatalf("first render wrong: %q", first)
	}

	// 第二次：prev 应为上次 summary "ok"
	exec.ExecuteOnce(context.Background(), "c1", "")
	second := captured["input"].(string)
	if !strings.Contains(second, "prev=ok count=2") {
		t.Fatalf("second render should include prev result: %q", second)
	}
}
