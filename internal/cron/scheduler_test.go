package cron

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// fakeStore 是测试用的 DBStore mock。
type fakeStore struct {
	mu          sync.Mutex
	crons       map[string]Cron
	executions  []Execution
	listCronsFn func(ListFilter) ([]Cron, error) // 可注入覆盖
}

func newFakeStore() *fakeStore {
	return &fakeStore{crons: make(map[string]Cron)}
}

func (s *fakeStore) InsertCron(c Cron) error                 { s.crons[c.ID] = c; return nil }
func (s *fakeStore) UpdateCron(c Cron) error                 { s.crons[c.ID] = c; return nil }
func (s *fakeStore) UpdateCronScheduleMeta(c Cron) error     { s.crons[c.ID] = c; return nil }
func (s *fakeStore) DeleteCron(id string) error              { delete(s.crons, id); return nil }
func (s *fakeStore) GetCron(id string) (Cron, error) {
	c, ok := s.crons[id]
	if !ok {
		return Cron{}, ErrNotFound
	}
	return c, nil
}
func (s *fakeStore) ListCrons(f ListFilter) ([]Cron, error) {
	if s.listCronsFn != nil {
		return s.listCronsFn(f)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Cron
	for _, c := range s.crons {
		if f.Status != "" && string(c.Status) != f.Status {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}
func (s *fakeStore) InsertExecution(e Execution) error    { s.executions = append(s.executions, e); return nil }
func (s *fakeStore) UpdateExecution(e Execution) error    { return nil }
func (s *fakeStore) GetExecution(id string) (Execution, error) { return Execution{}, nil }
func (s *fakeStore) ListExecutions(f ExecListFilter) ([]Execution, error) { return s.executions, nil }
func (s *fakeStore) CleanExecutions(f CleanFilter) (int, error) { return 0, nil }

// fakeExec 计数 Execute 调用，供断言"到点触发"。
type fakeExec struct {
	count     atomic.Int32
	triggered chan string // 收到的 cronID
}

func newFakeExec() *fakeExec {
	return &fakeExec{triggered: make(chan string, 32)}
}
func (e *fakeExec) Execute(ctx context.Context, cronID string) {
	e.count.Add(1)
	select {
	case e.triggered <- cronID:
	default:
	}
}

// TestSchedulerAddCron 验证 cron 表达式类型被注册并可触发。
func TestSchedulerAddCron(t *testing.T) {
	store := newFakeStore()
	exec := newFakeExec()
	bus := &fakeEventBus{}
	s := NewScheduler(store, exec, bus)
	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	// 每 200ms 触发一次
	c := Cron{ID: "c1", ScheduleType: ScheduleCron, CronExpr: "*/1 * * * * *", Status: StatusEnabled}
	// 用更短的间隔：robfig 秒级最小粒度 1s，用 1s
	c.CronExpr = "* * * * * *"
	if err := s.Add(ctx, c); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// 等待触发（最多 2s）
	select {
	case got := <-exec.triggered:
		if got != "c1" {
			t.Fatalf("triggered wrong id: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cron did not trigger within 2s")
	}
}

// TestSchedulerInvalidCronExpr 验证非法表达式返回错误。
func TestSchedulerInvalidCronExpr(t *testing.T) {
	store := newFakeStore()
	exec := newFakeExec()
	s := NewScheduler(store, exec, &fakeEventBus{})
	ctx := context.Background()
	s.Start(ctx)
	defer s.Stop()
	err := s.Add(ctx, Cron{ID: "bad", ScheduleType: ScheduleCron, CronExpr: "not a cron", Status: StatusEnabled})
	if err == nil {
		t.Fatal("expected error for invalid expr")
	}
}

// TestSchedulerOnce 验证 once 类型在指定时刻触发一次。
func TestSchedulerOnce(t *testing.T) {
	store := newFakeStore()
	exec := newFakeExec()
	s := NewScheduler(store, exec, &fakeEventBus{})
	ctx := context.Background()
	s.Start(ctx)
	defer s.Stop()
	when := time.Now().Add(1 * time.Second).Format(time.RFC3339)
	c := Cron{ID: "once1", ScheduleType: ScheduleOnce, OnceAt: when, Status: StatusEnabled}
	if err := s.Add(ctx, c); err != nil {
		t.Fatalf("Add: %v", err)
	}
	select {
	case got := <-exec.triggered:
		if got != "once1" {
			t.Fatalf("once triggered wrong: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("once did not trigger")
	}
}

// TestSchedulerOncePassed 验证已过期的 once_at 记 missed。
func TestSchedulerOncePassed(t *testing.T) {
	store := newFakeStore()
	exec := newFakeExec()
	bus := &fakeEventBus{}
	s := NewScheduler(store, exec, bus)
	ctx := context.Background()
	s.Start(ctx)
	defer s.Stop()
	when := time.Now().Add(-time.Hour).Format(time.RFC3339)
	c := Cron{ID: "once-old", ScheduleType: ScheduleOnce, OnceAt: when, Status: StatusEnabled}
	if err := s.Add(ctx, c); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// 应记一条 missed 事件
	found := false
	for _, e := range bus.events {
		if e.Type == event.EventCronMissed {
			found = true
		}
	}
	if !found {
		t.Fatal("expected cron_missed event for passed once_at")
	}
}

// TestSchedulerRemove 验证 Remove 移除后不再触发。
func TestSchedulerRemove(t *testing.T) {
	store := newFakeStore()
	exec := newFakeExec()
	s := NewScheduler(store, exec, &fakeEventBus{})
	ctx := context.Background()
	s.Start(ctx)
	defer s.Stop()
	c := Cron{ID: "rm1", ScheduleType: ScheduleInterval, CronExpr: "1s", Status: StatusEnabled}
	if err := s.Add(ctx, c); err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.Remove("rm1")
	// 等待一段时间，确认不再触发
	time.Sleep(1500 * time.Millisecond)
	if exec.count.Load() > 0 {
		t.Fatalf("removed cron still triggered: %d", exec.count.Load())
	}
}

// TestSchedulerUpdate 验证 Update = Remove + Add。
func TestSchedulerUpdate(t *testing.T) {
	store := newFakeStore()
	exec := newFakeExec()
	s := NewScheduler(store, exec, &fakeEventBus{})
	ctx := context.Background()
	s.Start(ctx)
	defer s.Stop()
	c := Cron{ID: "u1", ScheduleType: ScheduleInterval, CronExpr: "1s", Status: StatusEnabled}
	s.Add(ctx, c)
	// 改成 disabled 后 Update 应移除
	c.Status = StatusDisabled
	if err := s.Update(ctx, c); err != nil {
		t.Fatalf("Update: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	if exec.count.Load() > 0 {
		t.Fatalf("disabled cron still triggered after update: %d", exec.count.Load())
	}
}

// TestSchedulerStartLoadsEnabled 验证 Start 只加载 enabled cron。
func TestSchedulerStartLoadsEnabled(t *testing.T) {
	store := newFakeStore()
	store.crons["enabled1"] = Cron{ID: "enabled1", ScheduleType: ScheduleInterval, CronExpr: "1s", Status: StatusEnabled}
	store.crons["disabled1"] = Cron{ID: "disabled1", ScheduleType: ScheduleInterval, CronExpr: "1s", Status: StatusDisabled}
	exec := newFakeExec()
	s := NewScheduler(store, exec, &fakeEventBus{})
	s.Start(context.Background())
	defer s.Stop()
	time.Sleep(1500 * time.Millisecond)
	// 只应触发 enabled1
	got := map[string]bool{}
	for i := 0; i < int(exec.count.Load()); i++ {
		select {
		case id := <-exec.triggered:
			got[id] = true
		default:
		}
	}
	if !got["enabled1"] {
		t.Fatal("enabled1 should have triggered")
	}
	if got["disabled1"] {
		t.Fatal("disabled1 should not have triggered")
	}
}

// TestNormalizeInterval 验证 interval 规范化。
func TestNormalizeInterval(t *testing.T) {
	if got := normalizeInterval("30s"); got != "@every 30s" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeInterval("@every 5m"); got != "@every 5m" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeInterval("  2h "); got != "@every 2h" {
		t.Fatalf("got %q", got)
	}
}

// fakeEventBus 用于 scheduler 测试记录事件。
type fakeEventBus struct {
	mu     sync.Mutex
	events []event.Event
}

func (b *fakeEventBus) SendEvent(e event.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}
