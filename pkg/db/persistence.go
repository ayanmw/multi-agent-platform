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

// AgentRecord mirrors the agents table
type AgentRecord struct {
	ID           string
	Name         string
	Description  string
	SystemPrompt string
	Model        string
	Temperature  float64
	MaxTokens    int
	APIEndpoint  string
	APIKey       string
	Tools        []string
	Config       map[string]any
	CreatedAt    time.Time
	UpdatedAt    time.Time
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

// InsertAgent creates a new agent record
func InsertAgent(id, name, description, systemPrompt, model, endpoint, apiKey string, temperature float64, maxTokens int, tools []string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	toolsJSON, _ := json.Marshal(tools)
	_, err := DB.Exec(
		`INSERT INTO agents (id, name, description, system_prompt, model, temperature, max_tokens, api_endpoint, api_key, tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, name, description, systemPrompt, model, temperature, maxTokens, endpoint, apiKey, string(toolsJSON),
	)
	return err
}

// QueryAgents returns all agents ordered by creation time
func QueryAgents() ([]AgentRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, name, COALESCE(description,''), COALESCE(system_prompt,''), COALESCE(model,''),
		        COALESCE(temperature,0.7), COALESCE(max_tokens,4096), COALESCE(api_endpoint,''), COALESCE(api_key,''),
		        COALESCE(tools,'[]'), COALESCE(config,'{}'), created_at, updated_at
		 FROM agents ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []AgentRecord
	for rows.Next() {
		var a AgentRecord
		var toolsJSON, configJSON string
		if err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.SystemPrompt, &a.Model,
			&a.Temperature, &a.MaxTokens, &a.APIEndpoint, &a.APIKey,
			&toolsJSON, &configJSON, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(toolsJSON), &a.Tools)
		json.Unmarshal([]byte(configJSON), &a.Config)
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// QueryAgentByID returns a single agent by ID
func QueryAgentByID(id string) (*AgentRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var a AgentRecord
	var toolsJSON, configJSON string
	err := DB.QueryRow(
		`SELECT id, name, COALESCE(description,''), COALESCE(system_prompt,''), COALESCE(model,''),
		        COALESCE(temperature,0.7), COALESCE(max_tokens,4096), COALESCE(api_endpoint,''), COALESCE(api_key,''),
		        COALESCE(tools,'[]'), COALESCE(config,'{}'), created_at, updated_at
		 FROM agents WHERE id=?`, id,
	).Scan(&a.ID, &a.Name, &a.Description, &a.SystemPrompt, &a.Model,
		&a.Temperature, &a.MaxTokens, &a.APIEndpoint, &a.APIKey,
		&toolsJSON, &configJSON, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(toolsJSON), &a.Tools)
	json.Unmarshal([]byte(configJSON), &a.Config)
	return &a, nil
}

// UpdateAgent updates an existing agent record
func UpdateAgent(id, name, description, systemPrompt, model, endpoint, apiKey string, temperature float64, maxTokens int, tools []string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	toolsJSON, _ := json.Marshal(tools)
	_, err := DB.Exec(
		`UPDATE agents SET name=?, description=?, system_prompt=?, model=?, temperature=?,
		     max_tokens=?, api_endpoint=?, api_key=?, tools=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		name, description, systemPrompt, model, temperature, maxTokens, endpoint, apiKey, string(toolsJSON), id,
	)
	return err
}

// DeleteAgent removes an agent by ID
func DeleteAgent(id string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`DELETE FROM agents WHERE id=?`, id)
	return err
}