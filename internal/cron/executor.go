// executor.go — 单次触发的执行编排。
//
// Executor 是 Scheduler 到点回调的落点。它负责：
//   1. 读取 cron 记录，校验 status（仅 enabled 触发，手动触发除外）。
//   2. 串行判定：若 !AllowConcurrent 且该 cron 正在执行，记 skipped + 发事件，return。
//   3. 取上次 execution，构造 TemplateContext，渲染 action_payload。
//   4. 建一条 running execution 存 DB，发 cron_triggered + cron_execution_started。
//   5. 调 ActionRunner.Run 执行；成功/失败分别更新 execution + 发对应事件。
//   6. 更新 cron 的调度元数据（last_triggered_at / trigger_count / last_execution_id）。
//
// 串行 skip 用进程内 running map 判定（单进程 scheduler），不依赖 DB 锁。
package cron

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// ActionRunnerPort 是 Executor 依赖的 ActionRunner 接口（仅 Run）。
type ActionRunnerPort interface {
	Run(ctx context.Context, c Cron, renderedPayload map[string]any) (ActionResult, error)
}

// Executor 编排单次触发。
type Executor struct {
	store           DBStore
	runner          ActionRunnerPort
	bus             EventBus
	maxResultChars  int
	mu              sync.Mutex
	running         map[string]bool // cron_id -> 是否在跑
}

// NewExecutor 创建 Executor。maxResultChars<=0 时用默认 2000。
func NewExecutor(store DBStore, runner ActionRunnerPort, bus EventBus, maxResultChars int) *Executor {
	if maxResultChars <= 0 {
		maxResultChars = 2000
	}
	return &Executor{
		store:          store,
		runner:         runner,
		bus:            bus,
		maxResultChars: maxResultChars,
		running:        make(map[string]bool),
	}
}

// Execute 是 Scheduler 到点回调的入口。
func (e *Executor) Execute(ctx context.Context, cronID string) {
	e.doExecute(ctx, cronID, "", false)
}

// ExecuteOnce 是手动触发入口。
// overrideInput 非空时覆盖 start_task 的 input（渲染前）。
// 手动触发忽略串行 skip 与 status 校验（disabled 的 cron 也可手动触发）。
// 失败时也返回已创建的 Execution 记录（含 status=failed/error），便于调用方展示。
func (e *Executor) ExecuteOnce(ctx context.Context, cronID, overrideInput string) (*Execution, error) {
	return e.doExecute(ctx, cronID, overrideInput, true)
}
// doExecute 是统一的执行流程。manual=true 表示手动触发。
// 返回创建的 Execution（已完成态，非 nil）与 error（失败时两者都返回）。
func (e *Executor) doExecute(ctx context.Context, cronID, overrideInput string, manual bool) (*Execution, error) {
	c, err := e.store.GetCron(cronID)
	if err != nil {
		return nil, fmt.Errorf("get cron: %w", err)
	}

	// 自动触发时校验 status；手动触发放行。
	if !manual && c.Status != StatusEnabled {
		return nil, nil
	}

	// 串行 skip 判定（手动触发不受限）。
	if !manual && !c.AllowConcurrent {
		e.mu.Lock()
		if e.running[cronID] {
			e.mu.Unlock()
			return e.recordSkipped(c, "previous execution still running")
		}
		e.running[cronID] = true
		e.mu.Unlock()
		defer func() {
			e.mu.Lock()
			delete(e.running, cronID)
			e.mu.Unlock()
		}()
	}

	// 取上次 execution 构造模板上下文。
	prev := e.lastExecution(cronID)
	ctx2 := TemplateContext{
		Now:         time.Now().Format(time.RFC3339),
		Count:       c.TriggerCount + 1,
		CronID:      c.ID,
		CronName:    c.Name,
		PrevStatus:  string(prev.Status),
		PrevResult:  prev.ResultSummary,
	}
	if !prev.TriggeredAt.IsZero() {
		ctx2.PrevTrigger = prev.TriggeredAt.Format(time.RFC3339)
	}

	// 渲染 action_payload；手动触发且 overrideInput 非空时覆盖 start_task.input。
	payload := c.ActionPayload
	if payload == nil {
		payload = map[string]any{}
	}
	payloadCopy := copyMap(payload)
	if manual && overrideInput != "" && c.ActionType == ActionStartTask {
		payloadCopy["input"] = overrideInput
	}
	rendered := RenderMap(payloadCopy, ctx2)
	renderedInput := describeRenderedInput(c, rendered)

	// 创建 running execution 记录。
	exec := Execution{
		ID:            "cronexec_" + fmt.Sprintf("%d", time.Now().UnixNano()),
		CronID:        c.ID,
		TriggeredAt:   time.Now(),
		Status:        ExecRunning,
		RenderedInput: renderedInput,
		CreatedAt:     time.Now(),
	}
	if err := e.store.InsertExecution(exec); err != nil {
		return nil, fmt.Errorf("insert execution: %w", err)
	}
	// 发触发事件。
	if e.bus != nil {
		e.bus.SendEvent(newCronEvent(event.EventCronTriggered, c.ID, agentIDFor(c, rendered), map[string]any{
			"execution_id":   exec.ID,
			"triggered_at":   exec.TriggeredAt.UnixMilli(),
			"count":          ctx2.Count,
			"rendered_input": truncate(renderedInput, 500),
		}))
		e.bus.SendEvent(newCronEvent(event.EventCronExecutionStarted, c.ID, agentIDFor(c, rendered), map[string]any{
			"execution_id": exec.ID,
		}))
	}

	// 执行 action。
	start := time.Now()
	res, runErr := e.runner.Run(ctx, c, rendered)
	duration := time.Since(start)

	exec.DurationMS = int(duration / time.Millisecond)
	if runErr != nil {
		exec.Status = ExecFailed
		exec.Error = truncate(runErr.Error(), e.maxResultChars)
		exec.TaskID = res.TaskID
		exec.SessionID = res.SessionID
	} else {
		exec.Status = ExecCompleted
		exec.ResultSummary = truncate(res.Summary, e.maxResultChars)
		exec.TaskID = res.TaskID
		exec.SessionID = res.SessionID
	}
	_ = e.store.UpdateExecution(exec)

	// 发终态事件 + 更新 cron 调度元数据。
	if e.bus != nil {
		evtType := event.EventCronExecutionCompleted
		data := map[string]any{
			"execution_id": exec.ID,
			"duration_ms":  exec.DurationMS,
			"task_id":      exec.TaskID,
			"session_id":   exec.SessionID,
		}
		if runErr != nil {
			evtType = event.EventCronExecutionFailed
			data["error"] = exec.Error
		} else {
			data["result_summary"] = exec.ResultSummary
		}
		e.bus.SendEvent(newCronEvent(evtType, c.ID, agentIDFor(c, rendered), data))
	}
	e.updateCronMeta(c, exec.ID)

	if runErr != nil {
		return &exec, runErr
	}
	return &exec, nil
}

// recordSkipped 记一条 skipped execution 并发事件，返回该 execution。
func (e *Executor) recordSkipped(c Cron, reason string) (*Execution, error) {
	exec := Execution{
		ID:          "cronexec_skip_" + fmt.Sprintf("%d", time.Now().UnixNano()),
		CronID:      c.ID,
		TriggeredAt: time.Now(),
		Status:      ExecSkipped,
		Reason:      reason,
		CreatedAt:   time.Now(),
	}
	_ = e.store.InsertExecution(exec)
	if e.bus != nil {
		e.bus.SendEvent(newCronEvent(event.EventCronExecutionSkipped, c.ID, "cron", map[string]any{
			"execution_id": exec.ID,
			"reason":       reason,
		}))
	}
	e.updateCronMeta(c, exec.ID)
	return &exec, nil
}

// lastExecution 取某 cron 最近一次 execution（用于模板上下文）。无则零值。
func (e *Executor) lastExecution(cronID string) Execution {
	list, err := e.store.ListExecutions(ExecListFilter{CronID: cronID, Limit: 1})
	if err != nil || len(list) == 0 {
		return Execution{}
	}
	return list[0]
}

// updateCronMeta 更新 cron 的 last_triggered_at / trigger_count / last_execution_id。
// 不改 status，使用 UpdateCronScheduleMeta。
func (e *Executor) updateCronMeta(c Cron, execID string) {
	now := time.Now()
	c.LastTriggeredAt = &now
	c.LastExecutionID = execID
	c.TriggerCount = c.TriggerCount + 1
	// next_trigger_at 由 scheduler 侧维护，这里不更新，保持 nil/旧值。
	_ = e.store.UpdateCronScheduleMeta(c)
}

// agentIDFor 从渲染后的 start_task payload 取 agent_id，用作事件的 AgentID 字段。
// 非 start_task 或取不到时返回 "cron"。
func agentIDFor(c Cron, rendered map[string]any) string {
	if c.ActionType != ActionStartTask {
		return "cron"
	}
	if a, ok := rendered["agent_id"].(string); ok && a != "" {
		return a
	}
	return "cron"
}

// describeRenderedInput 生成 execution.rendered_input 的人类可读摘要。
func describeRenderedInput(c Cron, rendered map[string]any) string {
	switch c.ActionType {
	case ActionStartTask:
		if s, ok := rendered["input"].(string); ok {
			return s
		}
	case ActionNotifySession:
		if s, ok := rendered["message"].(string); ok {
			return s
		}
	case ActionWebhook:
		if s, ok := rendered["url"].(string); ok {
			return fmt.Sprintf("webhook %s %s", rendered["method"], s)
		}
	case ActionScript:
		if tcs, ok := rendered["tool_calls"].([]any); ok {
			return fmt.Sprintf("script: %d tool calls", len(tcs))
		}
	}
	return MarshalPayloadForLog(rendered)
}

// copyMap 浅拷贝 map，避免渲染覆盖原始 payload。
func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
