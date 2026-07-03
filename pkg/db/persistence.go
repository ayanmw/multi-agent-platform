package db

import (
	"encoding/json"
	"fmt"
	"time"
)

// TaskRecord mirrors the tasks table
type TaskRecord struct {
	ID          string
	UserInput   string
	Status      string
	AgentIDs    []string
	FinalResult string
	TotalTokens int
	StartedAt   time.Time
	CompletedAt *time.Time
}

// StepRecord mirrors the steps table
type StepRecord struct {
	ID        string
	TaskID    string
	AgentID   string
	StepIndex int
	Type      string
	Status    string
	Content   string
	ToolName  string
	ToolInput map[string]any
	ToolOutput string
	DurationMs int
	TokenUsed int
}

// InsertTask creates a new task record
func InsertTask(t TaskRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	agentIDsJSON, _ := json.Marshal(t.AgentIDs)
	_, err := DB.Exec(
		`INSERT INTO tasks (id, user_input, status, agent_ids, started_at)
		 VALUES (?, ?, ?, ?, ?)`,
		t.ID, t.UserInput, t.Status, string(agentIDsJSON), t.StartedAt,
	)
	return err
}

// UpdateTask updates a task's status and result
func UpdateTask(id string, status string, finalResult string, totalTokens int) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now()
	_, err := DB.Exec(
		`UPDATE tasks SET status=?, final_result=?, total_tokens=?, completed_at=? WHERE id=?`,
		status, finalResult, totalTokens, now, id,
	)
	return err
}

// InsertStep creates a new step record
func InsertStep(s StepRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	toolInputJSON, _ := json.Marshal(s.ToolInput)
	_, err := DB.Exec(
		`INSERT INTO steps (id, task_id, agent_id, step_index, type, status, content, tool_name, tool_input, tool_output, duration_ms, token_used)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.TaskID, s.AgentID, s.StepIndex, s.Type, s.Status,
		s.Content, s.ToolName, string(toolInputJSON), s.ToolOutput, s.DurationMs, s.TokenUsed,
	)
	return err
}

// InsertConversation adds a message to the conversation history
func InsertConversation(id, taskID, role, content string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`INSERT INTO conversations (id, task_id, role, content) VALUES (?, ?, ?, ?)`,
		id, taskID, role, content,
	)
	return err
}

// QueryTasks lists recent tasks (newest first), limited
func QueryTasks(limit int) ([]TaskRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, user_input, status, agent_ids, COALESCE(final_result,''), COALESCE(total_tokens,0), started_at, completed_at
		 FROM tasks ORDER BY started_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []TaskRecord
	for rows.Next() {
		var t TaskRecord
		var agentIDsJSON string
		var completedAt *time.Time
		if err := rows.Scan(&t.ID, &t.UserInput, &t.Status, &agentIDsJSON, &t.FinalResult, &t.TotalTokens, &t.StartedAt, &completedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(agentIDsJSON), &t.AgentIDs)
		t.CompletedAt = completedAt
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// QueryTaskByID returns a single task by ID
func QueryTaskByID(id string) (*TaskRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var t TaskRecord
	var agentIDsJSON string
	var completedAt *time.Time
	err := DB.QueryRow(
		`SELECT id, user_input, status, agent_ids, COALESCE(final_result,''), COALESCE(total_tokens,0), started_at, completed_at
		 FROM tasks WHERE id=?`, id,
	).Scan(&t.ID, &t.UserInput, &t.Status, &agentIDsJSON, &t.FinalResult, &t.TotalTokens, &t.StartedAt, &completedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(agentIDsJSON), &t.AgentIDs)
	t.CompletedAt = completedAt
	return &t, nil
}

// QueryStepsByTask returns all steps for a task, ordered by creation time
func QueryStepsByTask(taskID string) ([]StepRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, task_id, agent_id, step_index, type, status, COALESCE(content,''), COALESCE(tool_name,''), COALESCE(tool_input,'{}'), COALESCE(tool_output,''), COALESCE(duration_ms,0), COALESCE(token_used,0)
		 FROM steps WHERE task_id=? ORDER BY step_index ASC`, taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []StepRecord
	for rows.Next() {
		var s StepRecord
		var toolInputJSON string
		if err := rows.Scan(&s.ID, &s.TaskID, &s.AgentID, &s.StepIndex, &s.Type, &s.Status, &s.Content, &s.ToolName, &toolInputJSON, &s.ToolOutput, &s.DurationMs, &s.TokenUsed); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(toolInputJSON), &s.ToolInput)
		steps = append(steps, s)
	}
	return steps, rows.Err()
}