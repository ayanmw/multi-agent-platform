// service.go — Cron 业务服务，对外的统一门面。
//
// Service 封装 CRUD + 状态机 + 手动触发 + 执行历史查询，并在每次写操作后
// 通知 Scheduler 做增量同步、通过 EventBus 广播对应 cron_* 事件。
// 它是 REST API 与 Agent Tools 共同的底层调用对象。
//
// 校验规则集中在这里：
//   - schedule_type / action_type 合法性
//   - cron_expr 能被 robfig/cron 秒级 parser 解析
//   - action_payload 的必填字段（start_task.agent_id / notify_session.session_id / webhook.url）
//   - once_at 能被 RFC3339 解析（schedule_type=once 时）
package cron

import (
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/event"
	"github.com/robfig/cron/v3"
)

// SchedulerPort 是 Service 依赖的 Scheduler 接口。
// 用无 ctx 版本（Scheduler 内部自管理 context）。
type SchedulerPort interface {
	AddNoCtx(c Cron) error
	UpdateNoCtx(c Cron) error
	Remove(cronID string)
}


// ExecutorPort2 是 Service 依赖的 Executor 手动触发接口。
type ExecutorPort2 interface {
	ExecuteOnce(cronID, overrideInput string) (*Execution, error)
}

// Service 是 Cron 子系统的业务入口。
type Service struct {
	store    *Store
	sched    SchedulerPort
	exec     ExecutorPort2
	bus      EventBus
	now      func() time.Time
}

// NewService 创建 Service。sched/exec 可为 nil（CRON_ENABLED=false 时）。
func NewService(store *Store, sched SchedulerPort, exec ExecutorPort2, bus EventBus) *Service {
	return &Service{
		store: store,
		sched: sched,
		exec:  exec,
		bus:   bus,
		now:   time.Now,
	}
}

// Create 创建一个新 cron。
// 默认 status=enabled、source=user、display_type=schedule_type。
// 创建后通知 Scheduler.Add 并广播 cron_created。
func (s *Service) Create(in CreateInput) (*Cron, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	if !in.ScheduleType.IsValid() {
		return nil, fmt.Errorf("invalid schedule_type: %q", in.ScheduleType)
	}
	if !in.ActionType.IsValid() {
		return nil, fmt.Errorf("invalid action_type: %q", in.ActionType)
	}
	// 校验调度表达式
	if err := validateSchedule(in.ScheduleType, in.CronExpr, in.OnceAt); err != nil {
		return nil, err
	}
	// 校验 action payload 必填字段
	if err := validateActionPayload(in.ActionType, in.ActionPayload); err != nil {
		return nil, err
	}

	now := s.now()
	c := Cron{
		ID:            "cron_" + genID(),
		Name:          strings.TrimSpace(in.Name),
		Description:   in.Description,
		ScheduleType:  in.ScheduleType,
		CronExpr:      in.CronExpr,
		DisplayType:   in.DisplayType,
		Timezone:      in.Timezone,
		OnceAt:        in.OnceAt,
		ActionType:    in.ActionType,
		ActionPayload: in.ActionPayload,
		Status:        StatusEnabled,
		AllowConcurrent: in.AllowConcurrent,
		Source:        in.Source,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if c.DisplayType == "" {
		c.DisplayType = string(c.ScheduleType)
	}
	if c.Timezone == "" {
		c.Timezone = "UTC"
	}
	if c.Source == "" {
		c.Source = "user"
	}
	if err := s.store.InsertCron(c); err != nil {
		return nil, fmt.Errorf("insert cron: %w", err)
	}
	if s.sched != nil {
		if err := s.sched.AddNoCtx(c); err != nil {
			// 调度注册失败不回滚 DB（cron 仍在，可手动修复后 enable），
			// 但记日志并通过事件暴露。
			if s.bus != nil {
				s.bus.SendEvent(newCronEvent(event.EventCronMissed, c.ID, "cron", map[string]any{
					"reason": "scheduler add failed on create: " + err.Error(),
				}))
			}
		}
	}
	if s.bus != nil {
		s.bus.SendEvent(newCronEvent(event.EventCronCreated, c.ID, "cron", map[string]any{"cron": c}))
	}
	return &c, nil
}

// Update 按 UpdateInput 部分更新一个 cron。nil 字段不改。
// 若改了调度相关字段，重新注册到 Scheduler。
func (s *Service) Update(id string, in UpdateInput) (*Cron, error) {
	c, err := s.store.GetCron(id)
	if err != nil {
		return nil, fmt.Errorf("get cron: %w", err)
	}
	scheduleChanged := false
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return nil, fmt.Errorf("name cannot be empty")
		}
		c.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		c.Description = *in.Description
	}
	if in.ScheduleType != nil {
		if !(*in.ScheduleType).IsValid() {
			return nil, fmt.Errorf("invalid schedule_type")
		}
		c.ScheduleType = *in.ScheduleType
		scheduleChanged = true
	}
	if in.CronExpr != nil {
		c.CronExpr = *in.CronExpr
		scheduleChanged = true
	}
	if in.OnceAt != nil {
		c.OnceAt = *in.OnceAt
		scheduleChanged = true
	}
	if in.Timezone != nil {
		c.Timezone = *in.Timezone
	}
	if in.DisplayType != nil {
		c.DisplayType = *in.DisplayType
	}
	if in.ActionType != nil {
		if !(*in.ActionType).IsValid() {
			return nil, fmt.Errorf("invalid action_type")
		}
		c.ActionType = *in.ActionType
	}
	if in.ActionPayload != nil {
		if err := validateActionPayload(c.ActionType, *in.ActionPayload); err != nil {
			return nil, err
		}
		c.ActionPayload = *in.ActionPayload
	}
	if in.AllowConcurrent != nil {
		c.AllowConcurrent = *in.AllowConcurrent
	}
	// 若调度相关字段变了，重新校验表达式
	if scheduleChanged {
		if err := validateSchedule(c.ScheduleType, c.CronExpr, c.OnceAt); err != nil {
			return nil, err
		}
	}
	c.UpdatedAt = s.now()
	if err := s.store.UpdateCron(c); err != nil {
		return nil, fmt.Errorf("update cron: %w", err)
	}
	// 若 enabled 且调度变了，重新注册
	if scheduleChanged && s.sched != nil && c.Status == StatusEnabled {
		if err := s.sched.UpdateNoCtx(c); err != nil {
			if s.bus != nil {
				s.bus.SendEvent(newCronEvent(event.EventCronMissed, c.ID, "cron", map[string]any{
					"reason": "scheduler update failed: " + err.Error(),
				}))
			}
		}
	}
	if s.bus != nil {
		s.bus.SendEvent(newCronEvent(event.EventCronUpdated, c.ID, "cron", map[string]any{"cron": c}))
	}
	return &c, nil
}

// Delete 删除一个 cron：Scheduler.Remove + DB.Delete + 广播。
func (s *Service) Delete(id string) error {
	if _, err := s.store.GetCron(id); err != nil {
		return fmt.Errorf("get cron: %w", err)
	}
	if s.sched != nil {
		s.sched.Remove(id)
	}
	if err := s.store.DeleteCron(id); err != nil {
		return fmt.Errorf("delete cron: %w", err)
	}
	if s.bus != nil {
		s.bus.SendEvent(newCronEvent(event.EventCronDeleted, id, "cron", map[string]any{"cron_id": id}))
	}
	return nil
}

// Get 读取单条 cron。
func (s *Service) Get(id string) (*Cron, error) {
	c, err := s.store.GetCron(id)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// List 按过滤列出 cron。
func (s *Service) List(filter ListFilter) ([]Cron, error) {
	return s.store.ListCrons(filter)
}

// SetStatus 切换 cron 状态：enable/disable/pause/resume。
// enable/resume → StatusEnabled + Scheduler.Add；
// disable       → StatusDisabled + Scheduler.Remove；
// pause         → StatusPaused + Scheduler.Remove。
func (s *Service) SetStatus(id string, status Status) (*Cron, error) {
	if !status.IsValid() {
		return nil, fmt.Errorf("invalid status: %q", status)
	}
	c, err := s.store.GetCron(id)
	if err != nil {
		return nil, fmt.Errorf("get cron: %w", err)
	}
	old := c.Status
	c.Status = status
	c.UpdatedAt = s.now()
	// 用 UpdateCronScheduleMeta 只更 status（不改 action_payload），
	// 但 ScheduleMeta 当前不含 status——见 store.go，它确实含 status。
	if err := s.store.UpdateCronScheduleMeta(c); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}

	var evtType string
	switch status {
	case StatusEnabled:
		evtType = event.EventCronEnabled
		if s.sched != nil {
			if err := s.sched.AddNoCtx(c); err != nil && s.bus != nil {
				s.bus.SendEvent(newCronEvent(event.EventCronMissed, c.ID, "cron", map[string]any{
					"reason": "scheduler add failed on enable: " + err.Error(),
				}))
			}
		}
	case StatusDisabled:
		evtType = event.EventCronDisabled
		if s.sched != nil {
			s.sched.Remove(c.ID)
		}
	case StatusPaused:
		evtType = event.EventCronPaused
		if s.sched != nil {
			s.sched.Remove(c.ID)
		}
	}
	if s.bus != nil {
		s.bus.SendEvent(newCronEvent(evtType, c.ID, "cron", map[string]any{
			"old_status": string(old), "new_status": string(status),
		}))
	}
	return &c, nil
}

// Trigger 手动触发一次执行。overrideInput 可空。
func (s *Service) Trigger(id, overrideInput string) (*Execution, error) {
	if s.exec == nil {
		return nil, fmt.Errorf("executor not available (cron disabled)")
	}
	return s.exec.ExecuteOnce(id, overrideInput)
}

// ListExecutions 查询执行历史。
func (s *Service) ListExecutions(filter ExecListFilter) ([]Execution, error) {
	return s.store.ListExecutions(filter)
}

// CleanExecutions 手动清理执行历史，返回删除条数。
func (s *Service) CleanExecutions(filter CleanFilter) (int, error) {
	return s.store.CleanExecutions(filter)
}

// validateSchedule 校验调度表达式合法性。
func validateSchedule(st ScheduleType, cronExpr, onceAt string) error {
	switch st {
	case ScheduleCron:
		p := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		if _, err := p.Parse(cronExpr); err != nil {
			return fmt.Errorf("invalid cron_expr %q: %w", cronExpr, err)
		}
	case ScheduleInterval:
		if cronExpr == "" {
			return fmt.Errorf("interval: cron_expr is required")
		}
		// 用 robfig 的 @every descriptor 校验
		p := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		if _, err := p.Parse("@every " + strings.TrimSpace(cronExpr)); err != nil {
			return fmt.Errorf("invalid interval %q: %w", cronExpr, err)
		}
	case ScheduleOnce:
		if _, err := time.Parse(time.RFC3339, onceAt); err != nil {
			return fmt.Errorf("invalid once_at %q: %w", onceAt, err)
		}
	}
	return nil
}

// validateActionPayload 校验 action payload 的必填字段。
func validateActionPayload(at ActionType, payload map[string]any) error {
	switch at {
	case ActionStartTask:
		if payload == nil {
			return fmt.Errorf("start_task: payload is required")
		}
		if a, _ := payload["agent_id"].(string); a == "" {
			return fmt.Errorf("start_task: agent_id is required")
		}
	case ActionNotifySession:
		if payload == nil {
			return fmt.Errorf("notify_session: payload is required")
		}
		if sid, _ := payload["session_id"].(string); sid == "" {
			return fmt.Errorf("notify_session: session_id is required")
		}
	case ActionWebhook:
		if payload == nil {
			return fmt.Errorf("webhook: payload is required")
		}
		if u, _ := payload["url"].(string); u == "" {
			return fmt.Errorf("webhook: url is required")
		}
	case ActionScript:
		if payload == nil {
			return fmt.Errorf("script: payload is required")
		}
		if tcs, ok := payload["tool_calls"].([]any); !ok || len(tcs) == 0 {
			return fmt.Errorf("script: tool_calls must be a non-empty array")
		}
	}
	return nil
}

// genID 生成短随机 ID。用时间纳秒 + 计数避免冲突。
// 不用 crypto/rand 是为了保持轻量；ID 冲突概率极低且 DB 主键约束兜底。
// fakeStore 的 UpdateCron 在 test 里基于内存 map，ActionPayload 用 map 直接存；
// 但真实 DB 侧 UpdateCron 用 marshalPayload(c.ActionPayload) 重新序列化，
// GetCron 再 decodePayload 还原。两端一致。
// Service.Update 调 store.UpdateCron(c) 时 c.ActionPayload 已是新 map，无问题。

// genID 生成短随机 ID。用纳秒时间戳，冲突概率极低且 DB 主键约束兜底。
func genID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
