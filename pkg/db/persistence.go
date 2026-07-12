package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SessionRecord mirrors the sessions table.
// It tracks the high-level state of a multi-turn conversation session,
// including turn count and token/context-size statistics used for compression decisions.
type SessionRecord struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	RootTaskID   string    `json:"root_task_id"`
	Status       string    `json:"status"`
	UserInput    string    `json:"user_input"`
	ProjectID    string    `json:"project_id"`
	TurnCount    int       `json:"turn_count"`
	TotalTokens  int       `json:"total_tokens"`
	ContextSize  int       `json:"context_size"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TaskRecord mirrors the tasks table
type TaskRecord struct {
	ID           string     `json:"id"`
	UserInput    string     `json:"user_input"`
	Status       string     `json:"status"`
	AgentIDs     []string   `json:"agent_ids"`
	FinalResult  string     `json:"final_result"`
	TotalTokens  int        `json:"total_tokens"`
	DurationMs   int        `json:"duration_ms"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	SessionID    string     `json:"session_id"`
	ParentTaskID string     `json:"parent_task_id"`
	IsRoot       bool       `json:"is_root"`
}

// StepRecord mirrors the steps table
type StepRecord struct {
	ID         string         `json:"id"`
	TaskID     string         `json:"task_id"`
	AgentID    string         `json:"agent_id"`
	StepIndex  int            `json:"step_index"`
	Type       string         `json:"type"`
	Status     string         `json:"status"`
	Content    string         `json:"content"`
	ToolName   string         `json:"tool_name"`
	ToolInput  map[string]any `json:"tool_input"`
	ToolOutput string         `json:"tool_output"`
	DurationMs int            `json:"duration_ms"`
	TokenUsed  int            `json:"token_used"`
}

// AgentRecord mirrors the agents table
type AgentRecord struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	SystemPrompt string         `json:"system_prompt"`
	Model        string         `json:"model"`
	Temperature  float64        `json:"temperature"`
	MaxTokens    int            `json:"max_tokens"`
	APIEndpoint  string         `json:"api_endpoint"`
	APIKey       string         `json:"api_key"`
	Tools        []string       `json:"tools"`
	Config       map[string]any `json:"config"`
	IsDefault    bool           `json:"is_default"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// InsertTask persists a task record. If a task with the same ID already exists,
// it updates the mutable fields (agent_ids, user_input, status, started_at) so
// that callers can safely re-save a root task after its real agent list is known.
// This is intentionally idempotent: it keeps the existing row and only mutates
// the fields passed in the record.
func InsertTask(t TaskRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	agentIDsJSON, _ := json.Marshal(t.AgentIDs)

	// Try to update an existing row first. This avoids DELETE+INSERT side effects
	// (rowid churn, foreign-key ON DELETE behavior) that INSERT OR REPLACE has.
	res, err := DB.Exec(
		`UPDATE tasks SET agent_ids=?, user_input=?, status=?, started_at=? WHERE id=?`,
		string(agentIDsJSON), t.UserInput, t.Status, t.StartedAt, t.ID,
	)
	if err == nil {
		if rows, _ := res.RowsAffected(); rows > 0 {
			return nil
		}
	}

	// No existing row: create a new one.
	_, err = DB.Exec(
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

// UpdateTaskDuration updates a task's elapsed time.
func UpdateTaskDuration(id string, durationMs int) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`UPDATE tasks SET duration_ms=? WHERE id=?`,
		durationMs, id,
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

// InsertSession creates a new session record.
func InsertSession(s SessionRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	projectID := s.ProjectID
	if projectID == "" {
		projectID = "default"
	}
	_, err := DB.Exec(
		`INSERT INTO sessions (id, name, root_task_id, status, user_input, project_id, turn_count, total_tokens, context_size, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.RootTaskID, s.Status, s.UserInput, projectID, s.TurnCount, s.TotalTokens, s.ContextSize, s.CreatedAt, s.UpdatedAt,
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

// DeleteSession deletes a session and all its associated data.
// It cleans up in order: conversations → steps → files → tasks → session.
// This manual cascade is needed because SQLite foreign keys may not be enforced.
func DeleteSession(id string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}

	// Verify session exists before attempting deletion
	_, err := QuerySessionByID(id)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// Delete all data associated with tasks in this session, in dependency order:
	// 1. Delete conversations for tasks in this session
	_, _ = DB.Exec(`DELETE FROM conversations WHERE task_id IN (SELECT id FROM tasks WHERE session_id=?)`, id)
	// 2. Delete steps for tasks in this session
	_, _ = DB.Exec(`DELETE FROM steps WHERE task_id IN (SELECT id FROM tasks WHERE session_id=?)`, id)
	// 3. Delete files for tasks in this session
	_, _ = DB.Exec(`DELETE FROM files WHERE task_id IN (SELECT id FROM tasks WHERE session_id=?)`, id)
	// 4. Delete child tasks (those whose parent is in this session) first
	_, _ = DB.Exec(`DELETE FROM tasks WHERE parent_task_id IN (SELECT id FROM tasks WHERE session_id=?)`, id)
	// 5. Delete all tasks in this session
	_, err = DB.Exec(`DELETE FROM tasks WHERE session_id=?`, id)
	if err != nil {
		return fmt.Errorf("delete tasks: %w", err)
	}
	// 6. Delete the session itself
	_, err = DB.Exec(`DELETE FROM sessions WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// QuerySessions returns recent sessions ordered by updated_at DESC.
// If projectID is non-empty, filters by project_id.
func QuerySessions(limit int, projectID string) ([]SessionRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var rows *sql.Rows
	var err error
	if projectID != "" {
		rows, err = DB.Query(
			`SELECT id, name, COALESCE(root_task_id,''), status, COALESCE(user_input,''), COALESCE(project_id,'default'), COALESCE(turn_count,0), COALESCE(total_tokens,0), COALESCE(context_size,0), created_at, updated_at
			 FROM sessions WHERE project_id=? ORDER BY updated_at DESC LIMIT ?`, projectID, limit,
		)
	} else {
		rows, err = DB.Query(
			`SELECT id, name, COALESCE(root_task_id,''), status, COALESCE(user_input,''), COALESCE(project_id,'default'), COALESCE(turn_count,0), COALESCE(total_tokens,0), COALESCE(context_size,0), created_at, updated_at
			 FROM sessions ORDER BY updated_at DESC LIMIT ?`, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionRecord
	for rows.Next() {
		var s SessionRecord
		if err := rows.Scan(&s.ID, &s.Name, &s.RootTaskID, &s.Status, &s.UserInput, &s.ProjectID, &s.TurnCount, &s.TotalTokens, &s.ContextSize, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// QuerySessionByID returns a single session by ID.
func QuerySessionByID(id string) (*SessionRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var s SessionRecord
	err := DB.QueryRow(
		`SELECT id, name, COALESCE(root_task_id,''), status, COALESCE(user_input,''), COALESCE(project_id,'default'), COALESCE(turn_count,0), COALESCE(total_tokens,0), COALESCE(context_size,0), created_at, updated_at
		 FROM sessions WHERE id=?`, id,
	).Scan(&s.ID, &s.Name, &s.RootTaskID, &s.Status, &s.UserInput, &s.ProjectID, &s.TurnCount, &s.TotalTokens, &s.ContextSize, &s.CreatedAt, &s.UpdatedAt)
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
		`SELECT id, user_input, status, agent_ids, COALESCE(final_result,''), COALESCE(total_tokens,0), COALESCE(duration_ms,0), started_at, completed_at, session_id, parent_task_id, is_root
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
		if err := rows.Scan(&t.ID, &t.UserInput, &t.Status, &agentIDsJSON, &t.FinalResult, &t.TotalTokens, &t.DurationMs, &t.StartedAt, &completedAt, &t.SessionID, &t.ParentTaskID, &t.IsRoot); err != nil {
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
		`SELECT id, user_input, status, agent_ids, COALESCE(final_result,''), COALESCE(total_tokens,0), COALESCE(duration_ms,0), started_at, completed_at, session_id, parent_task_id, is_root
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
		if err := rows.Scan(&t.ID, &t.UserInput, &t.Status, &agentIDsJSON, &t.FinalResult, &t.TotalTokens, &t.DurationMs, &t.StartedAt, &completedAt, &t.SessionID, &t.ParentTaskID, &t.IsRoot); err != nil {
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

// AggregateSessionDuration sums task durations across all tasks in a session.
func AggregateSessionDuration(sessionID string) (int, error) {
	if DB == nil {
		return 0, fmt.Errorf("db not initialized")
	}
	var total int
	err := DB.QueryRow(
		`SELECT COALESCE(SUM(duration_ms),0) FROM tasks WHERE session_id=?`, sessionID,
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
		`SELECT id, user_input, status, agent_ids, COALESCE(final_result,''), COALESCE(total_tokens,0), COALESCE(duration_ms,0), started_at, completed_at, session_id, parent_task_id, is_root
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
		if err := rows.Scan(&t.ID, &t.UserInput, &t.Status, &agentIDsJSON, &t.FinalResult, &t.TotalTokens, &t.DurationMs, &t.StartedAt, &completedAt, &t.SessionID, &t.ParentTaskID, &t.IsRoot); err != nil {
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
		`SELECT id, user_input, status, agent_ids, COALESCE(final_result,''), COALESCE(total_tokens,0), COALESCE(duration_ms,0), started_at, completed_at, session_id, parent_task_id, is_root
		 FROM tasks WHERE id=?`, id,
	).Scan(&t.ID, &t.UserInput, &t.Status, &agentIDsJSON, &t.FinalResult, &t.TotalTokens, &t.DurationMs, &t.StartedAt, &completedAt, &t.SessionID, &t.ParentTaskID, &t.IsRoot)
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
func InsertAgent(id, name, description, systemPrompt, model, endpoint, apiKey string, temperature float64, maxTokens int, tools []string, isDefault bool) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	toolsJSON, _ := json.Marshal(tools)
	_, err := DB.Exec(
		`INSERT INTO agents (id, name, description, system_prompt, model, temperature, max_tokens, api_endpoint, api_key, tools, is_default)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, name, description, systemPrompt, model, temperature, maxTokens, endpoint, apiKey, string(toolsJSON), isDefault,
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
		        COALESCE(tools,'[]'), COALESCE(config,'{}'), COALESCE(is_default,0), created_at, updated_at
		 FROM agents ORDER BY is_default DESC, created_at ASC`,
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
			&toolsJSON, &configJSON, &a.IsDefault, &a.CreatedAt, &a.UpdatedAt); err != nil {
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
		        COALESCE(tools,'[]'), COALESCE(config,'{}'), COALESCE(is_default,0), created_at, updated_at
		 FROM agents WHERE id=?`, id,
	).Scan(&a.ID, &a.Name, &a.Description, &a.SystemPrompt, &a.Model,
		&a.Temperature, &a.MaxTokens, &a.APIEndpoint, &a.APIKey,
		&toolsJSON, &configJSON, &a.IsDefault, &a.CreatedAt, &a.UpdatedAt)
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

// SeedDefaultAgent ensures a default agent exists in the database.
// If no agent with is_default=true exists, it creates one.
// This is called at startup so the system always has a usable agent.
func SeedDefaultAgent() error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}

	// Check if a default agent already exists
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM agents WHERE is_default=1").Scan(&count)
	if err != nil {
		return fmt.Errorf("check default agent: %w", err)
	}
	if count > 0 {
		return nil // Default agent already exists
	}

	// Create the default agent
	toolsJSON := `["run_shell","write_file","read_file"]`
	_, err = DB.Exec(
		`INSERT INTO agents (id, name, description, system_prompt, model, temperature, max_tokens, api_endpoint, api_key, tools, is_default)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"agent_default", "Default Agent", "The default agent for general-purpose tasks",
		"You are a helpful AI assistant with access to tools. When you need to run commands, read files, or write files, use the available tools. Always explain your reasoning before using tools.",
		"deepseek-v4-flash", 0.7, 4096, "", "", toolsJSON, true,
	)
	if err != nil {
		return fmt.Errorf("create default agent: %w", err)
	}
	return nil
}

// ToolRecord mirrors the tools table for dynamic tool registration.
type ToolRecord struct {
	Name        string
	Description string
	Schema      map[string]any
	Enabled     bool
	CreatedAt   time.Time
}

// InsertTool persists a new dynamic tool into the tools table.
func InsertTool(name, description string, schema map[string]any, enabled bool) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	schemaJSON, _ := json.Marshal(schema)
	_, err := DB.Exec(
		`INSERT INTO tools (name, description, schema, enabled) VALUES (?, ?, ?, ?)`,
		name, description, string(schemaJSON), enabled,
	)
	return err
}

// DeleteTool removes a dynamic tool from the tools table by name.
func DeleteTool(name string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`DELETE FROM tools WHERE name=?`, name)
	return err
}

// QueryTools returns all tools from the tools table, ordered by creation time.
func QueryTools() ([]ToolRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT name, COALESCE(description,''), COALESCE(schema,'{}'), COALESCE(enabled,1), created_at
		 FROM tools ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tools []ToolRecord
	for rows.Next() {
		var tr ToolRecord
		var schemaJSON string
		if err := rows.Scan(&tr.Name, &tr.Description, &schemaJSON, &tr.Enabled, &tr.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(schemaJSON), &tr.Schema)
		tools = append(tools, tr)
	}
	return tools, rows.Err()
}

// ProjectRecord mirrors the projects table.
// Projects are the top-level organizational unit — each session belongs to a project.
type ProjectRecord struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	WorkingDirectory string         `json:"working_directory"`
	Config           map[string]any `json:"config"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// InsertProject creates a new project record.
func InsertProject(p ProjectRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	configJSON, _ := json.Marshal(p.Config)
	_, err := DB.Exec(
		`INSERT INTO projects (id, name, description, working_directory, config)
		 VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, p.WorkingDirectory, string(configJSON),
	)
	return err
}

// QueryProjects returns all projects ordered by updated_at DESC.
func QueryProjects() ([]ProjectRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, name, COALESCE(description,''), COALESCE(working_directory,''), COALESCE(config,'{}'), created_at, updated_at
		 FROM projects ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []ProjectRecord
	for rows.Next() {
		var p ProjectRecord
		var configJSON string
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.WorkingDirectory, &configJSON, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(configJSON), &p.Config)
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// QueryProjectByID returns a single project by ID.
func QueryProjectByID(id string) (*ProjectRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var p ProjectRecord
	var configJSON string
	err := DB.QueryRow(
		`SELECT id, name, COALESCE(description,''), COALESCE(working_directory,''), COALESCE(config,'{}'), created_at, updated_at
		 FROM projects WHERE id=?`, id,
	).Scan(&p.ID, &p.Name, &p.Description, &p.WorkingDirectory, &configJSON, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(configJSON), &p.Config)
	return &p, nil
}

// UpdateProject updates an existing project's metadata.
func UpdateProject(id, name, description, workingDirectory string, config map[string]any) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	configJSON, _ := json.Marshal(config)
	_, err := DB.Exec(
		`UPDATE projects SET name=?, description=?, working_directory=?, config=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		name, description, workingDirectory, string(configJSON), id,
	)
	return err
}

// DeleteProject deletes a project and all associated data.
// Cascade order: session_messages → conversations → steps → files → tasks
//   → sessions → memories (scope=project with matching project_id) → project.
// This manual cascade is needed because SQLite foreign keys may not be enforced.
func DeleteProject(id string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}

	// Verify project exists before attempting deletion
	_, err := QueryProjectByID(id)
	if err != nil {
		return fmt.Errorf("project not found: %w", err)
	}

	// Delete session_messages for sessions in this project
	_, _ = DB.Exec(`DELETE FROM session_messages WHERE session_id IN (SELECT id FROM sessions WHERE project_id=?)`, id)
	// Delete conversations for tasks in sessions in this project
	_, _ = DB.Exec(`DELETE FROM conversations WHERE task_id IN (SELECT t.id FROM tasks t JOIN sessions s ON t.session_id=s.id WHERE s.project_id=?)`, id)
	// Delete steps for tasks in sessions in this project
	_, _ = DB.Exec(`DELETE FROM steps WHERE task_id IN (SELECT t.id FROM tasks t JOIN sessions s ON t.session_id=s.id WHERE s.project_id=?)`, id)
	// Delete files for tasks in sessions in this project
	_, _ = DB.Exec(`DELETE FROM files WHERE task_id IN (SELECT t.id FROM tasks t JOIN sessions s ON t.session_id=s.id WHERE s.project_id=?)`, id)
	// Delete child tasks first (those whose parent is in sessions in this project)
	_, _ = DB.Exec(`DELETE FROM tasks WHERE parent_task_id IN (SELECT t.id FROM tasks t JOIN sessions s ON t.session_id=s.id WHERE s.project_id=?)`, id)
	// Delete all tasks in sessions in this project
	_, _ = DB.Exec(`DELETE FROM tasks WHERE session_id IN (SELECT id FROM sessions WHERE project_id=?)`, id)
	// Delete all sessions in this project
	_, _ = DB.Exec(`DELETE FROM sessions WHERE project_id=?`, id)
	// Delete project-scoped memories for this project
	_, _ = DB.Exec(`DELETE FROM memories WHERE scope='project' AND project_id=?`, id)
	// Delete the project itself
	_, err = DB.Exec(`DELETE FROM projects WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}

// SessionMessageRecord mirrors the session_messages table.
// Each row represents one message in a multi-turn conversation within a session.
type SessionMessageRecord struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	TaskID     string    `json:"task_id"`
	TurnIndex  int       `json:"turn_index"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	ToolCallID string    `json:"tool_call_id"`
	ToolCalls  string    `json:"tool_calls"`
	TokenCount int       `json:"token_count"`
	CreatedAt  time.Time `json:"created_at"`
}

// InsertSessionMessage creates a new session message record.
func InsertSessionMessage(m SessionMessageRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`INSERT INTO session_messages (id, session_id, task_id, turn_index, role, content, tool_call_id, tool_calls, token_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.SessionID, m.TaskID, m.TurnIndex, m.Role, m.Content, m.ToolCallID, m.ToolCalls, m.TokenCount,
	)
	return err
}

// QuerySessionMessages returns all messages for a session, ordered by turn_index ASC, created_at ASC.
func QuerySessionMessages(sessionID string) ([]SessionMessageRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, session_id, task_id, turn_index, role, content, COALESCE(tool_call_id,''), COALESCE(tool_calls,''), COALESCE(token_count,0), created_at
		 FROM session_messages WHERE session_id=? ORDER BY turn_index ASC, created_at ASC`, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []SessionMessageRecord
	for rows.Next() {
		var m SessionMessageRecord
		if err := rows.Scan(&m.ID, &m.SessionID, &m.TaskID, &m.TurnIndex, &m.Role, &m.Content, &m.ToolCallID, &m.ToolCalls, &m.TokenCount, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// DeleteSessionMessages deletes all messages for a session.
func DeleteSessionMessages(sessionID string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`DELETE FROM session_messages WHERE session_id=?`, sessionID)
	return err
}

// DeleteSessionMessagesBeforeTurn deletes all messages with turn_index < the given value.
// Used after compression to remove old messages that have been summarized.
func DeleteSessionMessagesBeforeTurn(sessionID string, turnIndex int) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`DELETE FROM session_messages WHERE session_id=? AND turn_index >= 0 AND turn_index < ?`,
		sessionID, turnIndex,
	)
	return err
}

// UpdateSessionTurnCount increments the turn_count for a session.
func UpdateSessionTurnCount(sessionID string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`UPDATE sessions SET turn_count = turn_count + 1, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		sessionID,
	)
	return err
}

// UpdateSessionContextSize updates context_size and total_tokens for a session.
func UpdateSessionContextSize(sessionID string, totalTokens int, contextSize int) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(
		`UPDATE sessions SET total_tokens = ?, context_size = ?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		totalTokens, contextSize, sessionID,
	)
	return err
}

// QuerySessionsByProject returns sessions filtered by project_id, ordered by updated_at DESC.
func QuerySessionsByProject(projectID string, limit int) ([]SessionRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, name, COALESCE(root_task_id,''), status, COALESCE(user_input,''), COALESCE(project_id,'default'), COALESCE(turn_count,0), COALESCE(total_tokens,0), COALESCE(context_size,0), created_at, updated_at
		 FROM sessions WHERE project_id=? ORDER BY updated_at DESC LIMIT ?`, projectID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionRecord
	for rows.Next() {
		var s SessionRecord
		if err := rows.Scan(&s.ID, &s.Name, &s.RootTaskID, &s.Status, &s.UserInput, &s.ProjectID, &s.TurnCount, &s.TotalTokens, &s.ContextSize, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// SeedDefaultProject ensures a default project exists in the database.
// If no project with id='default' exists, it creates one.
// This is called at startup so the system always has a usable project.
func SeedDefaultProject() error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}

	// Check if the default project already exists
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM projects WHERE id='default'").Scan(&count)
	if err != nil {
		return fmt.Errorf("check default project: %w", err)
	}
	if count > 0 {
		return nil // Default project already exists
	}

	// Create the default project
	_, err = DB.Exec(
		`INSERT INTO projects (id, name, description, working_directory, config)
		 VALUES (?, ?, ?, ?, ?)`,
		"default", "Default Project", "Default project created by seed", "", "{}",
	)
	if err != nil {
		return fmt.Errorf("create default project: %w", err)
	}
	return nil
}