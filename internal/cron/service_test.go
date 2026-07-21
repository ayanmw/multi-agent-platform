package cron

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// fakeSched 记录 Add/Update/Remove 调用。
type fakeSched struct {
	mu       sync.Mutex
	adds     []string
	updates  []string
	removes  []string
	addErr   error
	updateErr error
}

func (s *fakeSched) AddNoCtx(c Cron) error {
	s.mu.Lock()
	s.adds = append(s.adds, c.ID)
	err := s.addErr
	s.mu.Unlock()
	return err
}
func (s *fakeSched) UpdateNoCtx(c Cron) error {
	s.mu.Lock()
	s.updates = append(s.updates, c.ID)
	err := s.updateErr
	s.mu.Unlock()
	return err
}
func (s *fakeSched) Remove(id string) {
	s.mu.Lock()
	s.removes = append(s.removes, id)
	s.mu.Unlock()
}

// fakeExec2 是 ExecutorPort2 mock。
type fakeExec2 struct {
	calls    atomic.Int32
	override string
	err      error
}

func (e *fakeExec2) ExecuteOnce(cronID, overrideInput string) (*Execution, error) {
	e.calls.Add(1)
	e.override = overrideInput
	if e.err != nil {
		return &Execution{ID: "ex", CronID: cronID, Status: ExecFailed}, e.err
	}
	return &Execution{ID: "ex", CronID: cronID, Status: ExecCompleted}, nil
}

func newSvc(t *testing.T) (*Service, *fakeStore, *fakeSched, *fakeExec2, *fakeEventBus) {
	t.Helper()
	store := newFakeStore()
	sched := &fakeSched{}
	exec := &fakeExec2{}
	bus := &fakeEventBus{}
	svc := NewService(NewStore(store), sched, exec, bus)
	return svc, store, sched, exec, bus
}

// TestServiceCreate 验证创建成功 + 调度注册 + 事件。
func TestServiceCreate(t *testing.T) {
	svc, store, sched, _, bus := newSvc(t)
	c, err := svc.Create(CreateInput{
		Name:         "Daily",
		ScheduleType: ScheduleInterval,
		CronExpr:     "1h",
		ActionType:   ActionStartTask,
		ActionPayload: map[string]any{"agent_id": "a", "input": "hi"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ID == "" || c.Status != StatusEnabled || c.Source != "user" {
		t.Fatalf("defaults wrong: %+v", c)
	}
	if len(sched.adds) != 1 || sched.adds[0] != c.ID {
		t.Fatalf("scheduler not Add-ed: %v", sched.adds)
	}
	if _, err := store.GetCron(c.ID); err != nil {
		t.Fatalf("not persisted: %v", err)
	}
	if len(bus.events) != 1 || bus.events[0].Type != event.EventCronCreated {
		t.Fatalf("created event not sent: %+v", bus.events)
	}
}

// TestServiceCreateValidation 验证各类校验失败。
func TestServiceCreateValidation(t *testing.T) {
	svc, _, _, _, _ := newSvc(t)
	cases := []struct {
		name string
		in   CreateInput
	}{
		{"empty name", CreateInput{ScheduleType: ScheduleCron, ActionType: ActionStartTask}},
		{"bad schedule_type", CreateInput{Name: "x", ScheduleType: "nope", ActionType: ActionStartTask}},
		{"bad action_type", CreateInput{Name: "x", ScheduleType: ScheduleCron, ActionType: "nope"}},
		{"bad cron_expr", CreateInput{Name: "x", ScheduleType: ScheduleCron, CronExpr: "zzz", ActionType: ActionStartTask, ActionPayload: map[string]any{"agent_id": "a"}}},
		{"missing agent_id", CreateInput{Name: "x", ScheduleType: ScheduleCron, CronExpr: "* * * * * *", ActionType: ActionStartTask}},
		{"missing webhook url", CreateInput{Name: "x", ScheduleType: ScheduleCron, CronExpr: "* * * * * *", ActionType: ActionWebhook, ActionPayload: map[string]any{}}},
		{"missing notify session", CreateInput{Name: "x", ScheduleType: ScheduleCron, CronExpr: "* * * * * *", ActionType: ActionNotifySession, ActionPayload: map[string]any{}}},
		{"empty script tool_calls", CreateInput{Name: "x", ScheduleType: ScheduleCron, CronExpr: "* * * * * *", ActionType: ActionScript, ActionPayload: map[string]any{"tool_calls": []any{}}}},
		{"bad once_at", CreateInput{Name: "x", ScheduleType: ScheduleOnce, OnceAt: "not-a-time", ActionType: ActionStartTask, ActionPayload: map[string]any{"agent_id": "a"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Create(tc.in); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

// TestServiceSetStatus 验证状态机：enable/disable/pause。
func TestServiceSetStatus(t *testing.T) {
	svc, _, sched, _, bus := newSvc(t)
	c, _ := svc.Create(CreateInput{
		Name: "x", ScheduleType: ScheduleCron, CronExpr: "* * * * * *",
		ActionType: ActionStartTask, ActionPayload: map[string]any{"agent_id": "a"},
	})
	// 初始 add 一次
	if len(sched.adds) != 1 {
		t.Fatalf("initial add: %v", sched.adds)
	}

	// disable
	if _, err := svc.SetStatus(c.ID, StatusDisabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if len(sched.removes) != 1 {
		t.Fatalf("disable should remove: %v", sched.removes)
	}
	lastEvt := bus.events[len(bus.events)-1]
	if lastEvt.Type != event.EventCronDisabled {
		t.Fatalf("expected disabled event: %v", lastEvt.Type)
	}

	// pause
	if _, err := svc.SetStatus(c.ID, StatusPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if len(sched.removes) != 2 {
		t.Fatalf("pause should remove again: %v", sched.removes)
	}

	// enable (resume)
	addsBefore := len(sched.adds)
	if _, err := svc.SetStatus(c.ID, StatusEnabled); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if len(sched.adds) != addsBefore+1 {
		t.Fatalf("enable should add: %v", sched.adds)
	}
}

// TestServiceDelete 验证删除：Scheduler.Remove + DB.Delete + 事件。
func TestServiceDelete(t *testing.T) {
	svc, store, sched, _, bus := newSvc(t)
	c, _ := svc.Create(CreateInput{
		Name: "x", ScheduleType: ScheduleCron, CronExpr: "* * * * * *",
		ActionType: ActionStartTask, ActionPayload: map[string]any{"agent_id": "a"},
	})
	if err := svc.Delete(c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(sched.removes) != 1 {
		t.Fatalf("delete should remove from scheduler: %v", sched.removes)
	}
	if _, err := store.GetCron(c.ID); err == nil {
		t.Fatal("cron should be deleted from DB")
	}
	lastEvt := bus.events[len(bus.events)-1]
	if lastEvt.Type != event.EventCronDeleted {
		t.Fatalf("expected deleted event: %v", lastEvt.Type)
	}
	// 删除不存在的
	if err := svc.Delete(c.ID); err == nil {
		t.Fatal("expected error deleting missing cron")
	}
}

// TestServiceTrigger 验证手动触发调用 executor。
func TestServiceTrigger(t *testing.T) {
	svc, _, _, exec, _ := newSvc(t)
	c, _ := svc.Create(CreateInput{
		Name: "x", ScheduleType: ScheduleCron, CronExpr: "* * * * * *",
		ActionType: ActionStartTask, ActionPayload: map[string]any{"agent_id": "a"},
	})
	if _, err := svc.Trigger(c.ID, "override"); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if exec.calls.Load() != 1 || exec.override != "override" {
		t.Fatalf("executor not called correctly: calls=%d override=%q", exec.calls.Load(), exec.override)
	}
}

// TestServiceTriggerNoExecutor 验证无 executor 时报错。
func TestServiceTriggerNoExecutor(t *testing.T) {
	store := newFakeStore()
	svc := NewService(NewStore(store), &fakeSched{}, nil, &fakeEventBus{})
	_, err := svc.Trigger("any", "")
	if err == nil {
		t.Fatal("expected error when executor is nil")
	}
}

// TestServiceUpdate 验证部分更新 + 调度重注册。
func TestServiceUpdate(t *testing.T) {
	svc, _, sched, _, bus := newSvc(t)
	c, _ := svc.Create(CreateInput{
		Name: "x", ScheduleType: ScheduleCron, CronExpr: "* * * * * *",
		ActionType: ActionStartTask, ActionPayload: map[string]any{"agent_id": "a"},
	})
	updatesBefore := len(sched.updates)
	newName := "Renamed"
	newExpr := "*/2 * * * * *"
	if _, err := svc.Update(c.ID, UpdateInput{Name: &newName, CronExpr: &newExpr}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := svc.Get(c.ID)
	if got.Name != "Renamed" || got.CronExpr != "*/2 * * * * *" {
		t.Fatalf("update not applied: %+v", got)
	}
	// 改了 cron_expr → 应触发 scheduler.Update
	if len(sched.updates) != updatesBefore+1 {
		t.Fatalf("scheduler should be updated on schedule change: %v", sched.updates)
	}
	lastEvt := bus.events[len(bus.events)-1]
	if lastEvt.Type != event.EventCronUpdated {
		t.Fatalf("expected updated event: %v", lastEvt.Type)
	}
}

// TestServiceUpdateInvalidExpr 验证更新为非法表达式报错。
func TestServiceUpdateInvalidExpr(t *testing.T) {
	svc, _, _, _, _ := newSvc(t)
	c, _ := svc.Create(CreateInput{
		Name: "x", ScheduleType: ScheduleCron, CronExpr: "* * * * * *",
		ActionType: ActionStartTask, ActionPayload: map[string]any{"agent_id": "a"},
	})
	bad := "zzz"
	if _, err := svc.Update(c.ID, UpdateInput{CronExpr: &bad}); err == nil {
		t.Fatal("expected error for invalid expr on update")
	}
}

// TestServiceListExecutions 验证执行历史查询透传。
func TestServiceListExecutions(t *testing.T) {
	store := newFakeStore()
	store.executions = []Execution{
		{ID: "e1", CronID: "c1", Status: ExecCompleted},
		{ID: "e2", CronID: "c1", Status: ExecFailed},
	}
	svc := NewService(NewStore(store), &fakeSched{}, &fakeExec2{}, &fakeEventBus{})
	list, err := svc.ListExecutions(ExecListFilter{CronID: "c1"})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d, want 2", len(list))
	}
}

// TestServiceCleanExecutions 验证清理透传返回计数。
func TestServiceCleanExecutions(t *testing.T) {
	store := &cleanTestStore{}
	svc := NewService(NewStore(store), &fakeSched{}, &fakeExec2{}, &fakeEventBus{})
	n, err := svc.CleanExecutions(CleanFilter{CronID: "c1"})
	if err != nil || n != 5 {
		t.Fatalf("CleanExecutions: n=%d err=%v", n, err)
	}
}

// cleanTestStore 只实现 CleanExecutions 返回固定值。
type cleanTestStore struct {
	fakeStore
}

func (s *cleanTestStore) CleanExecutions(f CleanFilter) (int, error) { return 5, nil }

// 确保未被使用的 import 不报错（errors 在某些断言中可能用到）。
var _ = errors.New
