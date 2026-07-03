package main

import (
	"fmt"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/runtime"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
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