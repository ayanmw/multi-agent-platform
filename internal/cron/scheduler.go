// scheduler.go — 基于 robfig/cron/v3 的秒级调度器。
//
// Scheduler 维护内存中的调度项，提供启动加载与增量同步：
//   - Start(ctx): 从 DB 加载所有 enabled 的 cron，逐条注册到 cron 库；
//     解析失败的 cron 不阻塞其它，记一条 missed 事件。
//   - Add/Update/Remove: CRUD 后由 Service 调用，做增量同步，不全量 reload。
//
// 三种 schedule_type 的处理：
//   - cron      → robfig/cron AddFunc（6 域秒级表达式）
//   - interval  → cron_expr 形如 "30s"/"5m"，转成 robfig 的 "@every 30s"
//   - once      → 不进 robfig，用单独的 time.AfterFunc map 管理，触发后自动移除
//
// Scheduler 不直接执行业务逻辑——到点时只回调 Executor.Execute(ctx, cronID)。
// 串行 skip / 事件广播 / execution 记录都在 Executor 里。
package cron

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/event"
	"github.com/robfig/cron/v3"
)

// robfigCron 是 robfig/cron 库的别名，避免与本包名 cron 冲突。
type robfigCron = cron.Cron
type robfigEntryID = cron.EntryID

// ExecutorPort 是 Scheduler 依赖的 Executor 接口（仅 Execute 一个方法）。
// 用接口而非具体类型，避免 Scheduler ↔ Executor 之间的循环构造依赖。
type ExecutorPort interface {
	Execute(ctx context.Context, cronID string)
}

// Scheduler 维护在内存中的调度项。
type Scheduler struct {
	cronLib *robfigCron
	store   DBStore
	exec    ExecutorPort
	bus     EventBus

	mu       sync.Mutex
	entries  map[string]robfigEntryID // cron_id -> robfig entry（cron/interval）
	timers   map[string]*time.Timer   // cron_id -> once 类型的 timer
	cancel   context.CancelFunc
	started  bool
}

// NewScheduler 创建 Scheduler。exec 不可为 nil。
func NewScheduler(store DBStore, exec ExecutorPort, bus EventBus) *Scheduler {
	return &Scheduler{
		store:   store,
		exec:    exec,
		bus:     bus,
		entries: make(map[string]cron.EntryID),
		timers:  make(map[string]*time.Timer),
	}
}

// Start 从 DB 加载所有 enabled 的 cron 并启动调度。
// 已禁用/暂停的 cron 不加载。解析失败的 cron 记 missed 事件但不阻塞。
// 重复调用 Start 是 no-op。
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.cronLib = cron.New(cron.WithSeconds(), cron.WithLogger(cron.DiscardLogger))
	s.cronLib.Start()
	ctx2, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.started = true
	s.mu.Unlock()
	// 加载所有 enabled cron。
	list, err := s.store.ListCrons(ListFilter{Status: string(StatusEnabled)})
	if err != nil {
		return fmt.Errorf("scheduler load: %w", err)
	}
	for _, c := range list {
		if err := s.Add(ctx2, c); err != nil {
			// 解析失败：记 missed 事件，不阻塞其它 cron。
			s.emitMissed(c, "invalid schedule on load: "+err.Error())
		}
	}
	return nil
}

// emitMissed 广播一条 cron_missed 事件并写一条 missed execution 记录。
func (s *Scheduler) emitMissed(c Cron, reason string) {
	if s.bus != nil {
		s.bus.SendEvent(newCronEvent(event.EventCronMissed, c.ID, "cron", map[string]any{
			"cron_id": c.ID, "reason": reason,
		}))
	}
	if s.store != nil {
		_ = s.store.InsertExecution(Execution{
			ID:          "cronexec_missed_" + c.ID + "_" + fmt.Sprintf("%d", time.Now().UnixNano()),
			CronID:      c.ID,
			TriggeredAt: time.Now(),
			Status:      ExecMissed,
			Reason:      reason,
			CreatedAt:   time.Now(),
		})
	}
}

// Add 注册一个 cron 到调度器。若 status 非 enabled 则跳过。
// 对 once 类型：用 time.AfterFunc 在 once_at 时刻触发一次后自动移除；
// 若 once_at 已过期则记 missed。
func (s *Scheduler) Add(ctx context.Context, c Cron) error {
	if c.Status != StatusEnabled {
		return nil
	}
	if s.cronLib == nil && c.ScheduleType != ScheduleOnce {
		// 调度器未启动（CRON_ENABLED=false），仅 once 用 timer 也可独立工作，
		// 但 cron/interval 依赖 robfig 实例。此时直接跳过，不报错。
		return nil
	}
	switch c.ScheduleType {
	case ScheduleCron:
		s.mu.Lock()
		id, err := s.cronLib.AddFunc(c.CronExpr, func() {
			s.exec.Execute(ctx, c.ID)
		})
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("parse cron expr %q: %w", c.CronExpr, err)
		}
		s.entries[c.ID] = id
		s.mu.Unlock()
	case ScheduleInterval:
		expr := normalizeInterval(c.CronExpr)
		s.mu.Lock()
		id, err := s.cronLib.AddFunc(expr, func() {
			s.exec.Execute(ctx, c.ID)
		})
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("parse interval %q: %w", c.CronExpr, err)
		}
		s.entries[c.ID] = id
		s.mu.Unlock()
	case ScheduleOnce:
		when, err := time.Parse(time.RFC3339, c.OnceAt)
		if err != nil {
			return fmt.Errorf("parse once_at %q: %w", c.OnceAt, err)
		}
		dur := time.Until(when)
		if dur <= 0 {
			// 已过期：在锁外调 emitMissed，避免它间接重入锁。
			s.emitMissed(c, "once_at already passed")
			return nil
		}
		t := time.AfterFunc(dur, func() {
			s.exec.Execute(ctx, c.ID)
			s.mu.Lock()
			delete(s.timers, c.ID)
			s.mu.Unlock()
		})
		s.mu.Lock()
		s.timers[c.ID] = t
		s.mu.Unlock()
	default:
		return fmt.Errorf("unknown schedule_type: %s", c.ScheduleType)
	}
	return nil
}

// normalizeInterval 把 interval 形如 "30s"/"5m"/"1h" 转成 robfig 的 "@every 30s"。
// 若已带 @every 前缀则原样返回。
func normalizeInterval(expr string) string {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "@every") {
		return expr
	}
	return "@every " + expr
}

// Remove 从调度器移除一个 cron（cron/interval/once 统一处理）。
func (s *Scheduler) Remove(cronID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.entries[cronID]; ok && s.cronLib != nil {
		s.cronLib.Remove(id)
		delete(s.entries, cronID)
	}
	if t, ok := s.timers[cronID]; ok {
		t.Stop()
		delete(s.timers, cronID)
	}
}

// Update 更新一个 cron 的调度项：先 Remove 再 Add。
// 调用方应确保已先更新 DB。
func (s *Scheduler) Update(ctx context.Context, c Cron) error {
	s.Remove(c.ID)
	return s.Add(ctx, c)
}

// Stop 停止调度器并清理所有定时项。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.started = false
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.cronLib != nil {
		s.cronLib.Stop()
	}
	for _, t := range s.timers {
		t.Stop()
	}
	s.timers = make(map[string]*time.Timer)
	s.entries = make(map[string]cron.EntryID)
	s.cronLib = nil
	s.mu.Unlock()
}
