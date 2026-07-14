package main

import (
	"fmt"
	"log"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/runtime"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/google/uuid"
)

// DBPersistence implements runtime.Persistence using SQLite
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

// QueryTaskSessionID returns the session_id for a task from SQLite.
// Returns empty string if the task does not exist or the DB is unavailable.
func (p *DBPersistence) QueryTaskSessionID(taskID string) string {
	t, err := db.QueryTaskByID(taskID)
	if err != nil {
		return ""
	}
	return t.SessionID
}

// resolveSession either uses an existing session ID or creates a new empty session.
// It then creates a new root task bound to that session.
// Returns (sessionID, taskID, error).
func resolveSession(sessionID, userInput string, persist runtime.Persistence) (string, string, error) {
	if sessionID == "" {
		newID := "sess_" + uuid.New().String()
		sess := db.SessionRecord{
			ID:        newID,
			Name:      extractSessionName(userInput),
			Status:    "empty",
			UserInput: userInput,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := db.InsertSession(sess); err != nil {
			return "", "", fmt.Errorf("create session: %w", err)
		}
		sessionID = newID
	}

	taskID := "task_" + time.Now().Format("20060102150405")
	if persist != nil {
		if err := persist.SaveTask(taskID, userInput, []string{}); err != nil {
			return "", "", fmt.Errorf("create task: %w", err)
		}
		if err := persist.SaveTaskMeta(taskID, sessionID, "", true); err != nil {
			return "", "", fmt.Errorf("bind task to session: %w", err)
		}
	}
	// Bind the root task to the session so the frontend can load it after page refresh
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

// deriveSessionStatus computes the session status from all its tasks.
// Returns the status of the latest task that has a meaningful (non-empty/idle) status,
// falling back to "empty" if no task has one.
// ORDER BY is_root DESC, started_at ASC puts root first, so the last element
// with a non-empty/idle status is the latest meaningful task.
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
