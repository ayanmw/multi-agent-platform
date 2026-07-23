package main

import (
	"fmt"
	"log"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/runtime"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/google/uuid"
)

// DBPersistence 基于 SQLite 实现 runtime.Persistence
type DBPersistence struct{}

func (p *DBPersistence) SaveTask(taskID string, userInput string, agentIDs []string) error {
	return db.InsertTask(db.TaskRecord{
		ID:        taskID,
		UserInput: userInput,
		Status:    "running",
		AgentIDs:  agentIDs,
		StartedAt: time.Now(),
	})
}

func (p *DBPersistence) SaveTaskMeta(taskID string, sessionID string, parentTaskID string, isRoot bool) error {
	return db.UpdateTaskSession(taskID, sessionID, parentTaskID, isRoot)
}

func (p *DBPersistence) UpdateTask(taskID string, status string, finalResult string, totalTokens int) error {
	return db.UpdateTask(taskID, status, finalResult, totalTokens)
}

func (p *DBPersistence) UpdateTaskDuration(taskID string, durationMs int) error {
	return db.UpdateTaskDuration(taskID, durationMs)
}

// newTaskID 返回一个人类可读、按时间排序的 task 标识。
// 它把时间戳前缀与 UUID 的前 8 个字符组合，确保并发任务创建
//（multi-agent 扇出、UI 快速点击、root + child 任务）永不冲突。
func newTaskID() string {
	return "task_" + time.Now().Format("20060102150405") + "_" + uuid.New().String()[:8]
}

func (p *DBPersistence) SaveStep(s runtime.StepRecord) error {
	// Step ID = 可读前缀 + uuid 后缀。
	//
	// 为什么不用纯四元组 step_{taskID}_{agentID}_{stepIdx}_{type} 作主键：
	// 真实 LLM 多步 ReAct 下，同一 (taskID, agentID, stepIdx, type) 四元组会被
	// 多次保存——典型场景是 engine.go 在 `for _, tc := range toolCalls` 循环里，
	// 每个 tool call 执行前都先 saveStep 一次 think（stepIdx 未自增），若一次 LLM
	// 响应带 N 个 tool_calls，think step 就会被重复保存 N 次，主键完全相同 →
	// `UNIQUE constraint failed: steps.id (1555)`，导致部分 step 记录被丢弃、
	// 历史回放不完整。
	//
	// 加 uuid 后缀后，无论同一四元组保存多少次都不再碰撞。保留 taskID/stepIdx/type
	// 前缀是为了日志和 DB 直查时可读（一眼看出属于哪个 task 的第几步）。前端按
	// step_index 排序、api.go 按 ID 做子任务去重，都不依赖 ID 的精确格式，所以
	// 加随机后缀是安全的。
	id := fmt.Sprintf("step_%s_%s_%d_%s_%s",
		s.TaskID, s.AgentID, s.StepIndex, s.Type, uuid.New().String())
	return db.InsertStep(db.StepRecord{
		ID:         id,
		TaskID:     s.TaskID,
		AgentID:    s.AgentID,
		StepIndex:  s.StepIndex,
		Type:       s.Type,
		Status:     s.Status,
		Content:    s.Content,
		ToolName:   s.ToolName,
		ToolInput:  s.ToolInput,
		ToolOutput: s.ToolOutput,
		DurationMs: s.DurationMs,
		TokenUsed:  s.TokenUsed,
	})
}

func (p *DBPersistence) SaveConversation(c runtime.ConversationRecord) error {
	return db.InsertConversation(
		fmt.Sprintf("conv_%s_%s_%d", c.TaskID, c.Role, time.Now().UnixNano()),
		c.TaskID, c.Role, c.Content,
	)
}

// QueryTaskSessionID 从 SQLite 返回某 task 的 session_id。
// 若 task 不存在或 DB 不可用，返回空字符串。
func (p *DBPersistence) QueryTaskSessionID(taskID string) string {
	t, err := db.QueryTaskByID(taskID)
	if err != nil {
		return ""
	}
	return t.SessionID
}

// SaveAgentMessage 持久化一条经 AgentBus 路由的 agent 间消息。
// runtime.AgentBusMessage 是一个轻量 DTO，已经带有 agent_messages 表所需的
// 全部字段（TaskID、FromAgentID、Type、Content、Metadata）；持久化层只需
// 转发给 db.InsertAgentMessage helper。
//
// Phase 7-I: 同时转发 SubTaskID，使 agent_messages 表能记录精确路由目标。
// Phase 7-J: 同时转发 FromSubTaskID，记录发送方子任务。
func (p *DBPersistence) SaveAgentMessage(msg runtime.AgentBusMessage) error {
	return db.InsertAgentMessage(db.AgentBusMessage{
		TaskID:        msg.TaskID,
		SubTaskID:     msg.SubTaskID,
		FromSubTaskID: msg.FromSubTaskID,
		FromAgentID:   msg.FromAgentID,
		ToAgentID:     msg.ToAgentID,
		Type:          msg.Type,
		Content:       msg.Content,
		Metadata:      msg.Metadata,
	})
}

// LoadAgentMessages 返回某 task 的完整 AgentBus 消息历史，
// 按时间从旧到新排序。task 无消息时返回空 slice。
func (p *DBPersistence) LoadAgentMessages(taskID string) ([]runtime.AgentBusMessage, error) {
	rows, err := db.QueryAgentMessages(taskID)
	if err != nil {
		return nil, err
	}
	out := make([]runtime.AgentBusMessage, 0, len(rows))
	for _, r := range rows {
		out = append(out, runtime.AgentBusMessage{
			TaskID:        r.TaskID,
			SubTaskID:     r.SubTaskID,
			FromSubTaskID: r.FromSubTaskID,
			FromAgentID:   r.FromAgentID,
			ToAgentID:     r.ToAgentID,
			Type:          r.Type,
			Content:       r.Content,
			Metadata:      r.Metadata,
		})
	}
	return out, nil
}

// InsertApproval 实现 runtime.ApprovalRepository，转发给
// db.InsertApproval。Phase 7-I: 把 database schema 细节留在 pkg/db。
func (p *DBPersistence) InsertApproval(record runtime.ApprovalRecord) error {
	return db.InsertApproval(db.ApprovalRecord{
		ID:                   record.ApprovalID,
		TaskID:               record.TaskID,
		SubTaskID:            record.SubTaskID,
		AgentID:              record.AgentID,
		Tool:                 record.Tool,
		Reason:               record.Reason,
		Input:                record.Input,
		DelegatedToLeader:    record.DelegatedToLeader,
		LeaderSubTaskID:      record.LeaderSubTaskID,
		LeaderDecisionStepID: record.LeaderDecisionStepID,
		Approved:             record.Approved,
	})
}

// UpdateApprovalLeaderDecision 实现 runtime.ApprovalRepository，
// 转发给 db.UpdateApprovalLeaderDecision。
func (p *DBPersistence) UpdateApprovalLeaderDecision(approvalID string, approved bool, reason string) error {
	return db.UpdateApprovalLeaderDecision(approvalID, approved, reason)
}

// resolveSession 使用既有 session ID 或创建一个新的空 session，
// 然后在该 session 下创建一个新的 root task。
// 返回 (sessionID, taskID, error)。
//
// 当新建 session 时，会同步绑定一个默认 workspace 目录（<cwd>/workspace/session-<id>/），
// 让后续 write_file/run_shell 等工具的相对路径有明确落点，而非回退到 server CWD
// 污染仓库根目录。这是所有"无 session 入口"（/api/tasks chat、leader、cron
// start_task 等）的统一兜底，与 handleRunCase 的匿名 session 兜底形成双保险。
func resolveSession(sessionID, userInput string, persist runtime.Persistence) (string, string, error) {
	if sessionID == "" {
		newID := "sess_" + uuid.New().String()
		// 为新 session 绑定默认 workspace（auto 模式）。resolveWorkspaceDir 会
		// 创建 <cwd>/workspace/session-<id>/ 目录。失败时 workspaceDir 为空，
		// runner 仍能运行（工具回退到 CWD），只是产物落点不可预期 —— 记日志提示。
		workspaceDir, _ := resolveWorkspaceDir("", "", newID)
		sess := db.SessionRecord{
			ID:            newID,
			Name:          extractSessionName(userInput),
			Status:        "empty",
			UserInput:     userInput,
			WorkspaceDir:  workspaceDir,
			WorkspaceAuto: true,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		if err := db.InsertSession(sess); err != nil {
			return "", "", fmt.Errorf("create session: %w", err)
		}
		if workspaceDir != "" {
			log.Printf("[resolveSession] 新建 session=%s 绑定默认 workspace=%s", newID, workspaceDir)
		} else {
			log.Printf("[resolveSession] 新建 session=%s workspace 创建失败，产物将落在 server CWD", newID)
		}
		sessionID = newID
	}

	taskID := newTaskID()
	if persist != nil {
		if err := persist.SaveTask(taskID, userInput, []string{}); err != nil {
			return "", "", fmt.Errorf("create task: %w", err)
		}
		if err := persist.SaveTaskMeta(taskID, sessionID, "", true); err != nil {
			return "", "", fmt.Errorf("bind task to session: %w", err)
		}
	}
	// 把 root task 绑定到 session，让前端刷新后仍能加载
	if sessionID != "" {
		log.Printf("[resolveSession] sessionID=%s taskID=%s — checking root_task_id", sessionID, taskID)
		sess, err := db.QuerySessionByID(sessionID)
		if err != nil {
			log.Printf("[resolveSession] QuerySessionByID error: %v", err)
		} else if sess.RootTaskID == "" {
			log.Printf("[resolveSession] Setting session %s root_task_id = %s", sessionID, taskID)
			db.UpdateSession(sessionID, taskID, sess.Status, sess.UserInput)
		} else {
			log.Printf("[resolveSession] Session %s already has root_task_id=%s (skip)", sessionID, sess.RootTaskID)
		}
	}

	return sessionID, taskID, nil
}

// deriveSessionStatus 根据某 session 的所有 task 计算其状态。
// 返回最后一个拥有有意义（非空/非 idle）状态的 task 状态；
// 若没有 task 拥有这样的状态，则回退到 "empty"。
// ORDER BY is_root DESC, started_at ASC 让 root 排在前，
// 因此最后一个非空/非 idle 状态的元素就是最新的有意义 task。
func deriveSessionStatus(sessionID string) string {
	tasks, err := db.QueryTasksBySession(sessionID)
	if err != nil || len(tasks) == 0 {
		return "empty"
	}
	var lastMeaningful string
	for _, t := range tasks {
		if t.Status != "" && t.Status != "empty" {
			lastMeaningful = t.Status
		}
	}
	if lastMeaningful != "" {
		return lastMeaningful
	}
	return "empty"
}
