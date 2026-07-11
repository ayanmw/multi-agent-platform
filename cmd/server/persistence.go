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

func (p *DBPersistence) SaveStep(s runtime.StepRecord) error {
	return db.InsertStep(db.StepRecord{
		ID:         fmt.Sprintf("step_%s_%s_%d_%s", s.TaskID, s.AgentID, s.StepIndex, s.Type),
		// Step ID now includes agent_id so multiple agents sharing the same root
		// task cannot collide. Each agent has its own step namespace.
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
