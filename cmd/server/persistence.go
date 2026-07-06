package main

import (
	"fmt"
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

func (p *DBPersistence) SaveStep(s runtime.StepRecord) error {
	return db.InsertStep(db.StepRecord{
		ID:         fmt.Sprintf("step_%s_%d", s.TaskID, s.StepIndex),
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

	return sessionID, taskID, nil
}

// deriveSessionStatus computes the session status from all its tasks.
// "running" if any task is running, otherwise derived from root task status,
// falling back to "empty".
func deriveSessionStatus(sessionID string) string {
	tasks, err := db.QueryTasksBySession(sessionID)
	if err != nil || len(tasks) == 0 {
		return "empty"
	}
	hasRunning := false
	for _, t := range tasks {
		if t.Status == "running" {
			hasRunning = true
		}
	}
	if hasRunning {
		return "running"
	}
	for _, t := range tasks {
		if t.IsRoot {
			if t.Status == "" || t.Status == "empty" {
				return "empty"
			}
			return t.Status
		}
	}
	return "empty"
}
