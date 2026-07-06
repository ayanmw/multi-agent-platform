package db

import (
	"encoding/json"
	"fmt"
	"time"
)

// SessionRecord mirrors the sessions table
type SessionRecord struct {
	ID          string
	Name        string
	RootTaskID  string
	Status      string
	UserInput   string
	TotalTokens int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TaskRecord mirrors the tasks table
type TaskRecord struct {
	ID           string
	UserInput    string
	Status       string
	AgentIDs     []string
	FinalResult  string
	TotalTokens  int
	StartedAt    time.Time
	CompletedAt  *time.Time
	SessionID    string
	ParentTaskID string
	IsRoot       bool
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
		`INSERT INTO tasks (id, user_input, status, agent_ids, started_at, session_id, parent_task_id, is_root)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserInput, t.Status, string(agentIDsJSON), t.StartedAt, t.SessionID, t.ParentTaskID, t.IsRoot,
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

// UpdateTaskSession updates a task's session and parent relationships
func UpdateTaskSession(id, sessionID, parentTaskID string, isRoot bool) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`UPDATE tasks SET session_id=?, parent_task_id=?, is_root=? WHERE id=?`,
		sessionID, parentTaskID, isRoot, id,
	)
	return err
}

// InsertSession creates a new session record
func InsertSession(s SessionRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`INSERT INTO sessions (id, name, root_task_id, status, user_input, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.RootTaskID, s.Status, s.UserInput, s.CreatedAt, s.UpdatedAt,
	)
	return err
}

// UpdateSession updates a session's root task and status
func UpdateSession(id, rootTaskID, status, userInput string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`UPDATE sessions SET root_task_id=?, status=?, user_input=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		rootTaskID, status, userInput, id,
	)
	return err
}

// UpdateSessionStatus updates a session's status and timestamp
func UpdateSessionStatus(id, status string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`UPDATE sessions SET status=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		status, id,
	)
	return err
}

// UpdateSessionName updates a session's display name
func UpdateSessionName(id, name string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`UPDATE sessions SET name=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		name, id,
	)
	return err
}

// DeleteSession deletes a session by removing its root task first,
// which cascades to steps, conversations, files, and the session row.
func DeleteSession(id string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	sess, err := QuerySessionByID(id)
	if err != nil {
		return err
	}
	_, err = DB.Exec(`DELETE FROM tasks WHERE session_id=?`, id)
	if err != nil {
		return err
	}
	// Guard: if this session has no tasks, delete it directly
	if sess.RootTaskID == "" {
		_, err = DB.Exec(`DELETE FROM sessions WHERE id=?`, id)
	}
	return err
}

// QuerySessions returns recent sessions ordered by updated_at DESC
func QuerySessions(limit int) ([]SessionRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, name, COALESCE(root_task_id,''), status, COALESCE(user_input,''), created_at, updated_at
		 FROM sessions ORDER BY updated_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionRecord
	for rows.Next() {
		var s SessionRecord
		if err := rows.Scan(&s.ID, &s.Name, &s.RootTaskID, &s.Status, &s.UserInput, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// QuerySessionByID returns a single session by ID
func QuerySessionByID(id string) (*SessionRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var s SessionRecord
	err := DB.QueryRow(
		`SELECT id, name, COALESCE(root_task_id,''), status, COALESCE(user_input,''), created_at, updated_at
		 FROM sessions WHERE id=?`, id,
	).Scan(&s.ID, &s.Name, &s.RootTaskID, &s.Status, &s.UserInput, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// QueryTasksBySession returns all tasks belonging to a session (root + children),
// ordered by is_root DESC then created_at ASC.
func QueryTasksBySession(sessionID string) ([]TaskRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, user_input, status, agent_ids, COALESCE(final_result,''), COALESCE(total_tokens,0), started_at, completed_at, session_id, parent_task_id, is_root
		 FROM tasks WHERE session_id=? ORDER BY is_root DESC, started_at ASC`, sessionID,
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
		if err := rows.Scan(&t.ID, &t.UserInput, &t.Status, &agentIDsJSON, &t.FinalResult, &t.TotalTokens, &t.StartedAt, &completedAt, &t.SessionID, &t.ParentTaskID, &t.IsRoot); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(agentIDsJSON), &t.AgentIDs)
		t.CompletedAt = completedAt
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// QueryChildTasks returns all child tasks for a parent task
func QueryChildTasks(parentTaskID string) ([]TaskRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, user_input, status, agent_ids, COALESCE(final_result,''), COALESCE(total_tokens,0), started_at, completed_at, session_id, parent_task_id, is_root
		 FROM tasks WHERE parent_task_id=? ORDER BY started_at ASC`, parentTaskID,
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
		if err := rows.Scan(&t.ID, &t.UserInput, &t.Status, &agentIDsJSON, &t.FinalResult, &t.TotalTokens, &t.StartedAt, &completedAt, &t.SessionID, &t.ParentTaskID, &t.IsRoot); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(agentIDsJSON), &t.AgentIDs)
		t.CompletedAt = completedAt
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// AggregateSessionTokens sums total tokens across all tasks in a session
func AggregateSessionTokens(sessionID string) (int, error) {
	if DB == nil {
		return 0, fmt.Errorf("db not initialized")
	}
	var total int
	err := DB.QueryRow(
		`SELECT COALESCE(SUM(total_tokens),0) FROM tasks WHERE session_id=?`, sessionID,
	).Scan(&total)
	return total, err
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
		`SELECT id, user_input, status, agent_ids, COALESCE(final_result,''), COALESCE(total_tokens,0), started_at, completed_at, session_id, parent_task_id, is_root
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
		if err := rows.Scan(&t.ID, &t.UserInput, &t.Status, &agentIDsJSON, &t.FinalResult, &t.TotalTokens, &t.StartedAt, &completedAt, &t.SessionID, &t.ParentTaskID, &t.IsRoot); err != nil {
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
		`SELECT id, user_input, status, agent_ids, COALESCE(final_result,''), COALESCE(total_tokens,0), started_at, completed_at, session_id, parent_task_id, is_root
		 FROM tasks WHERE id=?`, id,
	).Scan(&t.ID, &t.UserInput, &t.Status, &agentIDsJSON, &t.FinalResult, &t.TotalTokens, &t.StartedAt, &completedAt, &t.SessionID, &t.ParentTaskID, &t.IsRoot)
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