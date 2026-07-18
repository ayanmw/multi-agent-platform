package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// ApprovalDelegationStages 描述 leader 委托审批流程中的关键事件类型。
// 这些类型用于 system_info 事件，方便前端在 worker 和 leader 时间轴上展示审批委托。
const (
	// EventApprovalDelegated 表示 worker 把高风险审批请求转发给 leader。
	EventApprovalDelegated = "approval_delegated"
	// EventApprovalDecidedByLeader 表示 leader 对审批请求做出了批准/拒绝决定。
	EventApprovalDecidedByLeader = "approval_decided_by_leader"
)

// DelegatedApprovalRequest 是 worker 向 supervisor leader 提交的审批委托请求。
// 所有字段都持久化到 approvals 表，便于审计与回放。
type DelegatedApprovalRequest struct {
	// ApprovalID 是审批请求唯一标识，与 ErrApprovalRequired 生成的一致。
	ApprovalID string `json:"approval_id"`

	// Tool 是需要审批的工具名称。
	Tool string `json:"tool"`

	// Reason 是需要审批的原因描述。
	Reason string `json:"reason"`

	// Input 是工具调用的原始参数。
	Input map[string]any `json:"input"`

	// WorkerSubTaskID 是发起委托的 worker 的子任务 ID。
	WorkerSubTaskID string `json:"worker_sub_task_id"`

	// WorkerAgentID 是发起委托的 worker 的 agent ID。
	WorkerAgentID string `json:"worker_agent_id"`

	// SupervisorSubTaskID 是负责审批的 leader 的子任务 ID。
	SupervisorSubTaskID string `json:"supervisor_sub_task_id"`
}

// DelegatedApprovalDecision 记录 supervisor leader 对委托审批请求的决定。
type DelegatedApprovalDecision struct {
	// ApprovalID 是审批请求唯一标识。
	ApprovalID string `json:"approval_id"`

	// Approved 为 true 表示批准，false 表示拒绝。
	Approved bool `json:"approved"`

	// Reason 是 leader 做出决定的原因。
	Reason string `json:"reason"`
}

// ApprovalDelegationHandler 是 worker Engine 把审批请求委托给 supervisor leader 的回调接口。
// 实现者（cmd/server）负责通过 engineRegistry 找到 supervisor Engine 并把请求转交给它。
// 返回 decided=false 时 worker 会回退到用户审批或拒绝。
type ApprovalDelegationHandler interface {
	// RequestDelegatedApproval 向 supervisor leader 发起审批委托。
	RequestDelegatedApproval(req DelegatedApprovalRequest) (approved bool, decided bool, err error)
}

// approvalRegistry 是全局 leader 委托审批等待表。
// key 是 approvalID，value 是用于接收 leader 决定的 channel。
// 该表只保存"等待中"的请求，决定到达后或超时后会被立即删除。
var approvalRegistry = struct {
	mu    sync.RWMutex
	table map[string]chan DelegatedApprovalDecision
}{table: make(map[string]chan DelegatedApprovalDecision)}

// RegisterDelegatedApproval 注册一个等待 leader 决定的审批请求。
// 调用者负责在不再需要时调用 UnregisterDelegatedApproval 清理。
func RegisterDelegatedApproval(approvalID string, ch chan DelegatedApprovalDecision) {
	approvalRegistry.mu.Lock()
	defer approvalRegistry.mu.Unlock()
	approvalRegistry.table[approvalID] = ch
}

// UnregisterDelegatedApproval 从等待表中移除一个审批请求。
func UnregisterDelegatedApproval(approvalID string) {
	approvalRegistry.mu.Lock()
	defer approvalRegistry.mu.Unlock()
	delete(approvalRegistry.table, approvalID)
}

// ResolveDelegatedApproval 把 leader 的审批决定写入对应等待 channel。
// 如果审批请求已不存在（可能已超时），返回一个可识别的错误。
func ResolveDelegatedApproval(decision DelegatedApprovalDecision) error {
	approvalRegistry.mu.Lock()
	defer approvalRegistry.mu.Unlock()

	ch, ok := approvalRegistry.table[decision.ApprovalID]
	if !ok {
		return fmt.Errorf("approval %s: 委托审批请求已超时或被清理", decision.ApprovalID)
	}
	ch <- decision
	delete(approvalRegistry.table, decision.ApprovalID)
	return nil
}

// WaitForDelegatedApproval 阻塞等待 leader 对指定审批请求做出决定。
// 超时返回错误，调用者通常应在此情况下回退到用户审批或拒绝操作。
func WaitForDelegatedApproval(approvalID string, timeout time.Duration) (DelegatedApprovalDecision, error) {
	approvalRegistry.mu.RLock()
	ch, ok := approvalRegistry.table[approvalID]
	approvalRegistry.mu.RUnlock()

	if !ok {
		return DelegatedApprovalDecision{}, errors.New("委托审批请求未注册")
	}

	select {
	case decision := <-ch:
		return decision, nil
	case <-time.After(timeout):
		UnregisterDelegatedApproval(approvalID)
		return DelegatedApprovalDecision{}, fmt.Errorf("等待 leader 审批决定超时（%v）", timeout)
	}
}

// BuildApprovalDelegationContent 把审批委托请求序列化为适合传给 leader LLM 作为 tool call 决策的内容。
// 返回的值会被 Engine.sendAgentMessage 放入 AgentMessage.Content。
func BuildApprovalDelegationContent(req DelegatedApprovalRequest) (string, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// approveSubAgentAction 是 leader Engine 处理 approve_sub_agent_action tool 时调用的内部方法。
// 它把批准决定写入委托审批表，并在 worker 和 leader 时间轴上发射审计事件。
// 该方法被 Engine.executeTool 调用；tool 包只负责把 tool call 映射成 (approvalID, reason)。
func (e *Engine) approveSubAgentAction(approvalID, reason string) error {
	return e.resolveDelegation(approvalID, true, reason)
}

// rejectSubAgentAction 是 leader Engine 处理 reject_sub_agent_action tool 时调用的内部方法。
func (e *Engine) rejectSubAgentAction(approvalID, reason string) error {
	return e.resolveDelegation(approvalID, false, reason)
}

// resolveDelegation 写入 leader 决定并广播审计事件。
func (e *Engine) resolveDelegation(approvalID string, approved bool, reason string) error {
	decision := DelegatedApprovalDecision{
		ApprovalID: approvalID,
		Approved:   approved,
		Reason:     reason,
	}
	return ResolveDelegatedApproval(decision)
}

// eventWithSubTask 是本包内发送带 SubTaskID 事件的便捷封装。保持与 pkg/event.NewEventWithSubTask 一致。
func eventWithSubTask(eventType, taskID, subTaskID, agentID string, stepIndex int, data map[string]any) event.Event {
	if data == nil {
		data = make(map[string]any)
	}
	return event.Event{
		EventID:   "", // 仅内部使用，实际 harness 使用 pkg/event 自动生成
		TaskID:    taskID,
		SubTaskID: subTaskID,
		AgentID:   agentID,
		StepIndex: stepIndex,
		Type:      eventType,
		Timestamp: time.Now().UnixMilli(),
		Data:      data,
	}
}

// handleApprovalDelegation 处理 worker 的 leader 委托审批逻辑。
// 返回 "approved=true" 时调用者继续执行原工具；返回错误时调用者应把错误当作 observation 反馈给 LLM。
func (e *Engine) handleApprovalDelegation(tc llm.ToolCall, approvalErr *ApprovalError, args map[string]any, duration int64) (string, error) {
	// 没有审批委托处理器或没有 supervisor，直接返回错误让上层走默认审批/失败流程。
	if e.cfg.SupervisorDecisionHandler == nil || e.cfg.SupervisorSubTaskID == "" {
		return "", fmt.Errorf("worker 未配置 supervisor，无法委托审批")
	}

	req := DelegatedApprovalRequest{
		ApprovalID:          approvalErr.ApprovalID,
		Tool:                approvalErr.Tool,
		Reason:              approvalErr.Reason,
		Input:               approvalErr.Input,
		WorkerSubTaskID:     e.cfg.SubTaskID,
		WorkerAgentID:       e.cfg.AgentID,
		SupervisorSubTaskID: e.cfg.SupervisorSubTaskID,
	}

	// 在 worker 时间轴上发射委托事件。
	e.bus.SendEvent(eventWithSubTask("system_info", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type":                   EventApprovalDelegated,
		"approval_id":            req.ApprovalID,
		"tool":                   req.Tool,
		"supervisor_sub_task_id": req.SupervisorSubTaskID,
		"worker_sub_task_id":     req.WorkerSubTaskID,
	}))

	// 持久化审批记录。
	if err := e.insertApprovalRecord(req); err != nil {
		log.Printf("[Engine] 插入审批记录失败: %v", err)
	}

	// 同时通过 AgentBus 把请求以 approval_request 类型发送给 leader。
	// leader 的 AgentBus listener 会把它当作 user message 追加到对话，
	// 触发 leader 在下一轮 think 时调用 approve/reject 工具。
	content, err := BuildApprovalDelegationContent(req)
	if err == nil {
		e.sendAgentMessageWithSubTask(e.cfg.SupervisorSubTaskID, "approval_request", content)
	}

	// 调用注册在 cmd/server 层的委托处理器，等待 leader 决定。
	approved, decided, decideErr := e.cfg.SupervisorDecisionHandler.RequestDelegatedApproval(req)
	if decideErr != nil || !decided {
		// 委托失败或 leader 未决定：回退到用户审批。
		if e.approvalHandler != nil {
			return e.handleApprovalRequired(tc, approvalErr.toHarness(), args, duration)
		}
		return "", fmt.Errorf("leader 委托失败: %w", decideErr)
	}

	if !approved {
		// leader 拒绝。
		e.bus.SendEvent(eventWithSubTask("system_info", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type":                 EventApprovalDecidedByLeader,
			"approval_id":          req.ApprovalID,
			"tool":                 req.Tool,
			"approved":             false,
			"reason":               "supervisor 拒绝了此操作",
			"leader_sub_task_id":   e.cfg.SupervisorSubTaskID,
		}))
		e.bus.SendEvent(eventWithSubTask("tool_call_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"tool":        tc.Function.Name,
			"error":       "supervisor 拒绝了高风险操作",
			"duration_ms": duration,
			"args":        args,
		}))
		e.bus.SendEvent(eventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", fmt.Errorf("supervisor denied approval for %s: %s", approvalErr.Tool, approvalErr.Reason)
	}

	// leader 批准：直接执行原工具。
	log.Printf("[Engine] leader 审批通过: %s (%s), 执行工具调用", approvalErr.Tool, approvalErr.ApprovalID)
	e.bus.SendEvent(eventWithSubTask("system_info", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type":                 EventApprovalDecidedByLeader,
		"approval_id":          req.ApprovalID,
		"tool":                 req.Tool,
		"approved":             true,
		"reason":               "supervisor 批准了此操作",
		"leader_sub_task_id":   e.cfg.SupervisorSubTaskID,
	}))

	execStart := time.Now()
	result, execErr := e.tools.Execute(tc.Function.Name, args)
	execDuration := time.Since(execStart).Milliseconds()

	if execErr != nil {
		e.bus.SendEvent(eventWithSubTask("tool_call_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"tool":        tc.Function.Name,
			"error":       execErr.Error(),
			"duration_ms": execDuration,
			"args":        args,
		}))
		e.bus.SendEvent(eventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", execErr
	}

	resultJSON, _ := json.Marshal(result)
	resultStr := string(resultJSON)

	e.bus.SendEvent(eventWithSubTask("tool_call_output", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":   tc.Function.Name,
		"result": result,
	}))
	e.bus.SendEvent(eventWithSubTask("tool_call_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":        tc.Function.Name,
		"duration_ms": execDuration,
	}))
	e.bus.SendEvent(eventWithSubTask("observation", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"content": resultStr,
	}))
	e.bus.SendEvent(eventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "tool_call",
	}))

	e.saveStep(StepRecord{
		TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
		Type: "tool_call", Status: "completed",
		ToolName: tc.Function.Name, ToolInput: args, ToolOutput: resultStr,
		DurationMs: int(execDuration),
	})
	e.saveStep(StepRecord{
		TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
		Type: "observation", Status: "completed",
		Content: resultStr,
	})

	return resultStr, nil
}

// ApprovalError 是 runtime 内部使用的审批请求错误抽象。
// 它从 harness.ErrApprovalRequired 中提取关键字段，避免 runtime 包依赖 harness 包内部的细节。
type ApprovalError struct {
	ApprovalID string
	Tool       string
	Reason     string
	Input      map[string]any
}

func (a *ApprovalError) toHarness() *harness.ErrApprovalRequired {
	return &harness.ErrApprovalRequired{
		ApprovalID: a.ApprovalID,
		Tool:       a.Tool,
		Reason:     a.Reason,
		Input:      a.Input,
	}
}

// insertApprovalRecord 把审批请求持久化到 approvals 表。
// 通过 runtime.Persistence 接口解耦 runtime 与 pkg/db。
func (e *Engine) insertApprovalRecord(req DelegatedApprovalRequest) error {
	if e.persist == nil {
		return nil
	}
	ar, ok := e.persist.(ApprovalRepository)
	if !ok {
		return nil
	}
	return ar.InsertApproval(ApprovalRecord{
		ApprovalID:          req.ApprovalID,
		TaskID:              e.taskID,
		SubTaskID:           e.cfg.SubTaskID,
		AgentID:             e.cfg.AgentID,
		Tool:                req.Tool,
		Reason:              req.Reason,
		Input:               req.Input,
		DelegatedToLeader:   true,
		LeaderSubTaskID:     req.SupervisorSubTaskID,
	})
}

// ApprovalRecord 是 runtime 包暴露给 Persistence 实现的审批记录。
// db 包负责把 ApprovalRecord 映射到 approvals 表。
type ApprovalRecord struct {
	ApprovalID          string
	TaskID              string
	SubTaskID           string
	AgentID             string
	Tool                string
	Reason              string
	Input               map[string]any
	DelegatedToLeader   bool
	LeaderSubTaskID     string
	LeaderDecisionStepID string
	Approved            *bool
}

// ApprovalRepository 扩展 Persistence 接口，支持审批记录的读写。
// db.Persistence 可选择实现该接口；runtime 通过类型断言调用，保持向后兼容。
type ApprovalRepository interface {
	InsertApproval(record ApprovalRecord) error
	UpdateApprovalLeaderDecision(approvalID string, approved bool, reason string) error
}
