package cron

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ayanmw/multi-agent-platform/pkg/event"
)

// fakeStoreWithUpdateError 是一个在 UpdateExecution/UpdateCronScheduleMeta 上
// 制造持久化错误的 store，用于验证 Executor 的错误传播与事件标注。
type fakeStoreWithUpdateError struct {
	*fakeStore
	updateExecErr         error
	updateScheduleMetaErr error
	insertExecutionErr    error
}

func (s *fakeStoreWithUpdateError) InsertExecution(e Execution) error {
	if s.insertExecutionErr != nil {
		return s.insertExecutionErr
	}
	return s.fakeStore.InsertExecution(e)
}

func (s *fakeStoreWithUpdateError) UpdateExecution(e Execution) error {
	if s.updateExecErr != nil {
		return s.updateExecErr
	}
	return s.fakeStore.UpdateExecution(e)
}

func (s *fakeStoreWithUpdateError) UpdateCronScheduleMeta(c Cron) error {
	if s.updateScheduleMetaErr != nil {
		return s.updateScheduleMetaErr
	}
	return s.fakeStore.UpdateCronScheduleMeta(c)
}

// TestExecutorPersistErrorEmitsEvent 验证 UpdateExecution 失败时会发
// cron_persist_failed 且 completed 事件带 persisted=false。
func TestExecutorPersistErrorEmitsEvent(t *testing.T) {
	base := newFakeStore()
	base.crons["c1"] = Cron{ID: "c1", ActionType: ActionStartTask, Status: StatusEnabled,
		ActionPayload: map[string]any{"agent_id": "a"}}
	store := &fakeStoreWithUpdateError{
		fakeStore:     base,
		updateExecErr: errors.New("disk full"),
	}
	bus := &fakeEventBus{}
	runner := &mockRunner{result: ActionResult{Summary: "ok"}}
	exec := NewExecutor(store, runner, bus, 100)

	if _, err := exec.ExecuteOnce(context.Background(), "c1", ""); err != nil {
		// runErr 为空，运行本身是成功的。
		t.Fatalf("ExecuteOnce: %v", err)
	}

	var completed, persistFailed event.Event
	for _, e := range bus.events {
		if e.Type == event.EventCronExecutionCompleted {
			completed = e
		}
		if e.Type == "cron_persist_failed" {
			persistFailed = e
		}
	}
	if completed.Type == "" {
		t.Fatal("expected completed event")
	}
	if completed.Data["persisted"] != false {
		t.Fatalf("expected persisted=false, got %v", completed.Data["persisted"])
	}
	if completed.Data["persist_error"] == "" {
		t.Fatal("expected persist_error in completed event")
	}
	if persistFailed.Type == "" {
		t.Fatal("expected cron_persist_failed event")
	}
}

// TestExecutorMetaPersistErrorEmitsEvent 验证 UpdateCronScheduleMeta 失败时
// 会发 cron_persist_failed。
func TestExecutorMetaPersistErrorEmitsEvent(t *testing.T) {
	base := newFakeStore()
	base.crons["c1"] = Cron{ID: "c1", ActionType: ActionStartTask, Status: StatusEnabled,
		ActionPayload: map[string]any{"agent_id": "a"}}
	store := &fakeStoreWithUpdateError{
		fakeStore:             base,
		updateScheduleMetaErr: errors.New("meta write failed"),
	}
	bus := &fakeEventBus{}
	runner := &mockRunner{result: ActionResult{Summary: "ok"}}
	exec := NewExecutor(store, runner, bus, 100)

	if _, err := exec.ExecuteOnce(context.Background(), "c1", ""); err != nil {
		t.Fatalf("ExecuteOnce: %v", err)
	}

	found := false
	for _, e := range bus.events {
		if e.Type == "cron_persist_failed" && strings.Contains(e.Data["error"].(string), "meta write failed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected cron_persist_failed for meta error, events=%v", bus.events)
	}
}

// TestExecutorBothRunAndPersistError 验证运行错误 + 持久化失败同时发生时，
// 仍能看到 failed 与 persist_failed 两类事件。
func TestExecutorBothRunAndPersistError(t *testing.T) {
	base := newFakeStore()
	base.crons["c1"] = Cron{ID: "c1", ActionType: ActionStartTask, Status: StatusEnabled,
		ActionPayload: map[string]any{"agent_id": "a"}}
	store := &fakeStoreWithUpdateError{
		fakeStore:             base,
		updateExecErr:         errors.New("disk full"),
		updateScheduleMetaErr: errors.New("meta write failed"),
	}
	bus := &fakeEventBus{}
	runner := &mockRunner{err: errors.New("boom")}
	exec := NewExecutor(store, runner, bus, 100)

	if _, err := exec.ExecuteOnce(context.Background(), "c1", ""); err == nil {
		t.Fatal("expected error")
	}

	var failed, persistFailed event.Event
	for _, e := range bus.events {
		if e.Type == event.EventCronExecutionFailed {
			failed = e
		}
		if e.Type == "cron_persist_failed" {
			persistFailed = e
		}
	}
	if failed.Type == "" {
		t.Fatal("expected failed event")
	}
	if persistFailed.Type == "" {
		t.Fatal("expected persist_failed event")
	}
}
