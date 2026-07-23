package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SessionRecord 对应 sessions 表。
// 它追踪多轮对话 session 的高层状态，包括轮次计数和 token/context 大小
// 统计，用于压缩决策。
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
	WorkspaceDir string    `json:"workspace_dir"`
	WorkspaceAuto bool    `json:"workspace_auto"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TaskRecord 对应 tasks 表
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

// StepRecord 对应 steps 表
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

// AgentRecord 对应 agents 表
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

// InsertTask 持久化一条 task 记录。如果相同 ID 的 task 已存在，
// 会更新其中的可变字段（agent_ids、user_input、status、started_at），
// 这样调用方在 root task 的真实 agent 列表确定后可以安全地重新保存。
// 该操作是幂等的：保留已有行，仅修改 record 中传入的字段。
func InsertTask(t TaskRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	agentIDsJSON, _ := json.Marshal(t.AgentIDs)

	// 先尝试更新已有行。这样可以避免 INSERT OR REPLACE 带来的
	// DELETE+INSERT 副作用（rowid 抖动、外键 ON DELETE 行为）。
	res, err := DB.Exec(
		`UPDATE tasks SET agent_ids=?, user_input=?, status=?, started_at=? WHERE id=?`,
		string(agentIDsJSON), t.UserInput, t.Status, t.StartedAt, t.ID,
	)
	if err == nil {
		if rows, _ := res.RowsAffected(); rows > 0 {
			return nil
		}
	}

	// 没有已有行：新建一条。
	_, err = DB.Exec(
		`INSERT INTO tasks (id, user_input, status, agent_ids, started_at, session_id, parent_task_id, is_root)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserInput, t.Status, string(agentIDsJSON), t.StartedAt, t.SessionID, t.ParentTaskID, t.IsRoot,
	)
	return err
}

// UpdateTask 更新 task 的状态与结果
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

// UpdateTaskDuration 更新 task 的耗时。
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

// UpdateTaskSession 更新 task 的 session 和父子关系
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

// InsertSession 创建一条新的 session 记录。
func InsertSession(s SessionRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	projectID := s.ProjectID
	if projectID == "" {
		projectID = "default"
	}
	_, err := DB.Exec(
		`INSERT INTO sessions (id, name, root_task_id, status, user_input, project_id, turn_count, total_tokens, context_size, workspace_dir, workspace_auto, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.RootTaskID, s.Status, s.UserInput, projectID, s.TurnCount, s.TotalTokens, s.ContextSize, s.WorkspaceDir, s.WorkspaceAuto, s.CreatedAt, s.UpdatedAt,
	)
	return err
}

// UpdateSession 更新 session 的 root task 和状态
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

// UpdateSessionStatus 更新 session 的状态与时间戳
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

// UpdateSessionName 更新 session 的显示名称
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

// UpdateSessionMeta 更新 session 的可编辑元数据：显示名称与 workspace 目录。
//
// workspace_dir 的语义：
//   - 非空：用户显式指定的路径，workspace_auto 置为 false，运行时工具链直接使用该目录
//   - 为空：回退到 project.working_directory 或自动生成的 ./workspace/session-{id}/，
//     workspace_auto 置为 true
//
// 注意：本函数只更新 DB 指针，不负责在磁盘上创建或迁移目录。新自定义路径的目录
// 创建由 API handler 在调用前完成（参考 resolveWorkspaceDir 的 MkdirAll 兜底），
// 旧 workspace 目录的物理迁移/清理不在本函数职责内——变更 workspace 只切换指针。
func UpdateSessionMeta(id, name, workspaceDir string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	isAuto := workspaceDir == ""
	_, err := DB.Exec(
		`UPDATE sessions SET name=?, workspace_dir=?, workspace_auto=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		name, workspaceDir, isAuto, id,
	)
	return err
}

// DeleteSession 删除一个 session 及其全部关联数据。
// 清理顺序为：conversations → steps → files → tasks → session。
// 之所以手动 cascade，是因为 SQLite 的外键可能未被强制启用。
func DeleteSession(id string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}

	// 删除前先确认 session 存在
	_, err := QuerySessionByID(id)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// 按依赖顺序删除该 session 下 task 的所有关联数据：
	// 1. 删除该 session 下 task 的 conversations
	_, _ = DB.Exec(`DELETE FROM conversations WHERE task_id IN (SELECT id FROM tasks WHERE session_id=?)`, id)
	// 2. 删除该 session 下 task 的 steps
	_, _ = DB.Exec(`DELETE FROM steps WHERE task_id IN (SELECT id FROM tasks WHERE session_id=?)`, id)
	// 3. 删除该 session 下 task 的 files
	_, _ = DB.Exec(`DELETE FROM files WHERE task_id IN (SELECT id FROM tasks WHERE session_id=?)`, id)
	// 4. 先删除 parent 在该 session 中的子 task
	_, _ = DB.Exec(`DELETE FROM tasks WHERE parent_task_id IN (SELECT id FROM tasks WHERE session_id=?)`, id)
	// 5. 删除该 session 下的全部 task
	_, err = DB.Exec(`DELETE FROM tasks WHERE session_id=?`, id)
	if err != nil {
		return fmt.Errorf("delete tasks: %w", err)
	}
	// 6. 删除 session 自身
	_, err = DB.Exec(`DELETE FROM sessions WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// QuerySessions 返回按 updated_at 倒序排列的近期 session。
// projectID 非空时按 project_id 过滤。
func QuerySessions(limit int, projectID string) ([]SessionRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var rows *sql.Rows
	var err error
	if projectID != "" {
		rows, err = DB.Query(
			`SELECT id, name, COALESCE(root_task_id,''), status, COALESCE(user_input,''), COALESCE(project_id,'default'), COALESCE(turn_count,0), COALESCE(total_tokens,0), COALESCE(context_size,0), COALESCE(workspace_dir,''), COALESCE(workspace_auto,1), created_at, updated_at
			 FROM sessions WHERE project_id=? ORDER BY updated_at DESC LIMIT ?`, projectID, limit,
		)
	} else {
		rows, err = DB.Query(
			`SELECT id, name, COALESCE(root_task_id,''), status, COALESCE(user_input,''), COALESCE(project_id,'default'), COALESCE(turn_count,0), COALESCE(total_tokens,0), COALESCE(context_size,0), COALESCE(workspace_dir,''), COALESCE(workspace_auto,1), created_at, updated_at
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
		if err := rows.Scan(&s.ID, &s.Name, &s.RootTaskID, &s.Status, &s.UserInput, &s.ProjectID, &s.TurnCount, &s.TotalTokens, &s.ContextSize, &s.WorkspaceDir, &s.WorkspaceAuto, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// QuerySessionByID 按 ID 返回单个 session。
func QuerySessionByID(id string) (*SessionRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var s SessionRecord
	err := DB.QueryRow(
		`SELECT id, name, COALESCE(root_task_id,''), status, COALESCE(user_input,''), COALESCE(project_id,'default'), COALESCE(turn_count,0), COALESCE(total_tokens,0), COALESCE(context_size,0), COALESCE(workspace_dir,''), COALESCE(workspace_auto,1), created_at, updated_at
		 FROM sessions WHERE id=?`, id,
	).Scan(&s.ID, &s.Name, &s.RootTaskID, &s.Status, &s.UserInput, &s.ProjectID, &s.TurnCount, &s.TotalTokens, &s.ContextSize, &s.WorkspaceDir, &s.WorkspaceAuto, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// QueryTasksBySession 返回某个 session 下的全部 task（root + 子 task），
// 按 is_root 倒序、然后 created_at 升序排列。
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

// QueryChildTasks 返回某个父 task 的全部子 task
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

// AggregateSessionTokens 汇总某个 session 下所有 task 的 total_tokens 总和
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

// AggregateSessionDuration 汇总某个 session 下所有 task 的 duration_ms 总和。
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

// InsertStep 创建一条新的 step 记录
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

// InsertConversation 向对话历史追加一条消息
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

// QueryTasks 列出近期的 task（最新优先），按 limit 截断
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

// QueryTaskByID 按 ID 返回单个 task
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

// QueryStepsByTask 返回某个 task 的全部 step，按创建时间排序
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

// InsertAgent 创建一条新的 agent 记录
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

// QueryAgents 返回所有 agent，按创建时间排序
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

// QueryAgentByID 按 ID 返回单个 agent
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

// UpdateAgent 更新一条已有 agent 记录
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

// DeleteAgent 按 ID 删除一个 agent
func DeleteAgent(id string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`DELETE FROM agents WHERE id=?`, id)
	return err
}

// SeedDefaultAgent 确保数据库中存在一个 default agent。
// 如果不存在 is_default=true 的 agent，则创建一个。
// 启动时调用，使系统始终有可用 agent。
func SeedDefaultAgent() error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}

	// 检查是否已存在 default agent
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM agents WHERE is_default=1").Scan(&count)
	if err != nil {
		return fmt.Errorf("check default agent: %w", err)
	}
	if count > 0 {
		return nil // default agent 已存在
	}

	// 创建 default agent。
	// 默认 tools 为空数组：空白名单表示允许使用注册表中的全部工具，
	// 这样新增工具后无需再手动编辑 default agent。
	var toolsJSON = `[]`
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

// ToolRecord / InsertTool / DeleteTool / QueryTools 已迁移至 pkg/db/tool.go
// （v27 tools 表 + 多版本/来源 schema）。保留此处空注释作为导航锚点。

// ProjectRecord 对应 projects 表。
// project 是顶层组织单元——每个 session 都隶属于某个 project。
type ProjectRecord struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	WorkingDirectory string         `json:"working_directory"`
	Config           map[string]any `json:"config"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// InsertProject 创建一条新的 project 记录。
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

// QueryProjects 返回所有 project，按 updated_at 倒序排列。
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

// QueryProjectByID 按 ID 返回单个 project。
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

// UpdateProject 更新已有 project 的元数据。
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

// DeleteProject 删除一个 project 及其全部关联数据。
// cascade 顺序：session_messages → conversations → steps → files → tasks
//   → sessions → memories（scope=project 且 project_id 匹配）→ project。
// 之所以手动 cascade，是因为 SQLite 的外键可能未被强制启用。
func DeleteProject(id string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}

	// 删除前先确认 project 存在
	_, err := QueryProjectByID(id)
	if err != nil {
		return fmt.Errorf("project not found: %w", err)
	}

	// 删除该 project 下 session 的 session_messages
	_, _ = DB.Exec(`DELETE FROM session_messages WHERE session_id IN (SELECT id FROM sessions WHERE project_id=?)`, id)
	// 删除该 project 下 session 中 task 的 conversations
	_, _ = DB.Exec(`DELETE FROM conversations WHERE task_id IN (SELECT t.id FROM tasks t JOIN sessions s ON t.session_id=s.id WHERE s.project_id=?)`, id)
	// 删除该 project 下 session 中 task 的 steps
	_, _ = DB.Exec(`DELETE FROM steps WHERE task_id IN (SELECT t.id FROM tasks t JOIN sessions s ON t.session_id=s.id WHERE s.project_id=?)`, id)
	// 删除该 project 下 session 中 task 的 files
	_, _ = DB.Exec(`DELETE FROM files WHERE task_id IN (SELECT t.id FROM tasks t JOIN sessions s ON t.session_id=s.id WHERE s.project_id=?)`, id)
	// 先删除 parent 在该 project session 中的子 task
	_, _ = DB.Exec(`DELETE FROM tasks WHERE parent_task_id IN (SELECT t.id FROM tasks t JOIN sessions s ON t.session_id=s.id WHERE s.project_id=?)`, id)
	// 删除该 project 下 session 中的全部 task
	_, _ = DB.Exec(`DELETE FROM tasks WHERE session_id IN (SELECT id FROM sessions WHERE project_id=?)`, id)
	// 删除该 project 下的全部 session
	_, _ = DB.Exec(`DELETE FROM sessions WHERE project_id=?`, id)
	// 删除该 project 的 project 作用域 memory
	_, _ = DB.Exec(`DELETE FROM memories WHERE scope='project' AND project_id=?`, id)
	// 删除 project 自身
	_, err = DB.Exec(`DELETE FROM projects WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}

// SessionMessageRecord 对应 session_messages 表。
// 每行表示 session 内多轮对话中的一条消息。
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

// InsertSessionMessage 创建一条新的 session message 记录。
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

// QuerySessionMessages 返回某个 session 的全部消息，按 turn_index 升序、created_at 升序排列。
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

// QuerySessionMessagesByTask 返回某个 task 的全部消息，按 turn_index 升序、
// created_at 升序排列。这是重建某个已持久化 task 的 LLM context window 的主数据源。
func QuerySessionMessagesByTask(taskID string) ([]SessionMessageRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, session_id, task_id, turn_index, role, content, COALESCE(tool_call_id,''), COALESCE(tool_calls,''), COALESCE(token_count,0), created_at
		 FROM session_messages WHERE task_id=? ORDER BY turn_index ASC, created_at ASC`, taskID,
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

// DeleteSessionMessages 删除某个 session 的全部消息。
func DeleteSessionMessages(sessionID string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`DELETE FROM session_messages WHERE session_id=?`, sessionID)
	return err
}

// DeleteSessionMessagesBeforeTurn 删除 turn_index 小于给定值的全部消息。
// 压缩完成后用于移除已被摘要的旧消息。
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

// UpdateSessionTurnCount 将 session 的 turn_count 加 1。
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

// UpdateSessionContextSize 更新 session 的 context_size 和 total_tokens。
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

// QuerySessionsByProject 返回按 project_id 过滤的 session，按 updated_at 倒序排列。
func QuerySessionsByProject(projectID string, limit int) ([]SessionRecord, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, name, COALESCE(root_task_id,''), status, COALESCE(user_input,''), COALESCE(project_id,'default'), COALESCE(turn_count,0), COALESCE(total_tokens,0), COALESCE(context_size,0), COALESCE(workspace_dir,''), COALESCE(workspace_auto,1), created_at, updated_at
		 FROM sessions WHERE project_id=? ORDER BY updated_at DESC LIMIT ?`, projectID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionRecord
	for rows.Next() {
		var s SessionRecord
		if err := rows.Scan(&s.ID, &s.Name, &s.RootTaskID, &s.Status, &s.UserInput, &s.ProjectID, &s.TurnCount, &s.TotalTokens, &s.ContextSize, &s.WorkspaceDir, &s.WorkspaceAuto, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// SeedDefaultProject 确保数据库中存在 default project。
// 如果不存在 id='default' 的 project，则创建一个。
// 启动时调用，使系统始终有可用 project。
func SeedDefaultProject() error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}

	// 检查 default project 是否已存在
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM projects WHERE id='default'").Scan(&count)
	if err != nil {
		return fmt.Errorf("check default project: %w", err)
	}
	if count > 0 {
		return nil // default project 已存在
	}

	// 创建 default project
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

// AgentBusMessage 对应 agent_messages 表。
//
// 每条经由 AgentBus（internal/orchestrator.AgentBus）路由的消息都会被
// 持久化到这里，前端可通过 GET /api/tasks/:id/agent-messages 渲染某个 task
// 的完整 inter-agent 通信时间线。
//
// Metadata 以 JSON 编码的 TEXT 列存储——保持 opaque 可在新增 metadata key
// （如 correlation ID、发送方时间戳、trace context）时避免 schema 频繁变动。
//
// SubTaskID 是目标子任务（接收方）；FromSubTaskID 是发送方子任务。两者均
// 由 Phase 7-J 数据库迁移加入，用于前端按子任务时间线精确展示消息流向。
type AgentBusMessage struct {
	ID            int               `json:"id"`
	TaskID        string            `json:"task_id"`
	SubTaskID     string            `json:"sub_task_id,omitempty"`      // Phase 7-I: 支持子任务路由
	FromSubTaskID string            `json:"from_sub_task_id,omitempty"` // Phase 7-J: 发送方子任务
	FromAgentID   string            `json:"from_agent_id"`
	ToAgentID     string            `json:"to_agent_id"`
	Type          string            `json:"type"`
	Content       string            `json:"content"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
}

// ApprovalRecord 对应 approvals 表（Phase 7-I）。
// 记录每个高风险工具调用的审批请求及其最终决议（包括委托给 leader 的情况）。
type ApprovalRecord struct {
	ID                   string         `json:"id"`
	TaskID               string         `json:"task_id"`
	SubTaskID            string         `json:"sub_task_id"`
	AgentID              string         `json:"agent_id"`
	Tool                 string         `json:"tool"`
	Reason               string         `json:"reason"`
	Input                map[string]any `json:"input"`
	DelegatedToLeader    bool           `json:"delegated_to_leader"`
	LeaderSubTaskID      string         `json:"leader_sub_task_id"`
	LeaderDecisionStepID string         `json:"leader_decision_step_id"`
	Approved             *bool          `json:"approved"`
	CreatedAt            time.Time      `json:"created_at"`
	DecidedAt            *time.Time     `json:"decided_at"`
}

// InsertApproval 持久化一条新的审批请求记录。
// Approved 字段在记录决议前为 nil。
func InsertApproval(r ApprovalRecord) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	inputJSON, _ := json.Marshal(r.Input)
	_, err := DB.Exec(
		`INSERT INTO approvals (id, task_id, sub_task_id, agent_id, tool, reason, input, delegated_to_leader, leader_sub_task_id, leader_decision_step_id, approved)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TaskID, r.SubTaskID, r.AgentID, r.Tool, r.Reason, string(inputJSON),
		r.DelegatedToLeader, r.LeaderSubTaskID, r.LeaderDecisionStepID, r.Approved,
	)
	return err
}

// UpdateApprovalLeaderDecision 更新某次审批的 leader 决策字段。
func UpdateApprovalLeaderDecision(approvalID string, approved bool, reason string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now()
	_, err := DB.Exec(
		`UPDATE approvals SET approved=?, decided_at=? WHERE id=?`,
		approved, now, approvalID,
	)
	return err
}

// InsertAgentMessage 持久化单条 AgentBus 消息。
//
// Metadata JSON 序列化失败的错误被有意吞掉：空的 metadata blob 仍是有效行，
// 我们宁可丢一个 metadata key，也不愿因为序列化小故障而丢掉整条消息。
func InsertAgentMessage(m AgentBusMessage) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	metadataJSON, _ := json.Marshal(m.Metadata)
	_, err := DB.Exec(
		`INSERT INTO agent_messages (task_id, sub_task_id, from_sub_task_id, from_agent_id, to_agent_id, msg_type, content, metadata)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.TaskID, m.SubTaskID, m.FromSubTaskID, m.FromAgentID, m.ToAgentID, m.Type, m.Content, string(metadataJSON),
	)
	return err
}

// QueryAgentMessages 返回某个 task 的全部 AgentBus 消息，按 created_at 升序、
// id 升序排列（id 用于同一 SQLite CURRENT_TIMESTAMP 秒内写入的消息作为并列排序的 tie-breaker）。
//
// 若 task 没有消息，返回空切片（非 nil），便于调用方直接 JSON 编码而无需 null 判断。
func QueryAgentMessages(taskID string) ([]AgentBusMessage, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := DB.Query(
		`SELECT id, task_id, COALESCE(sub_task_id,''), COALESCE(from_sub_task_id,''), from_agent_id, to_agent_id, msg_type, content, COALESCE(metadata,''), created_at
		 FROM agent_messages WHERE task_id=? ORDER BY created_at ASC, id ASC`, taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	msgs := []AgentBusMessage{}
	for rows.Next() {
		var m AgentBusMessage
		var metadataJSON string
		if err := rows.Scan(&m.ID, &m.TaskID, &m.SubTaskID, &m.FromSubTaskID, &m.FromAgentID, &m.ToAgentID, &m.Type, &m.Content, &metadataJSON, &m.CreatedAt); err != nil {
			return nil, err
		}
		// 老行的 Metadata 可能为 NULL 或空字符串；容忍反序列化失败。
		if metadataJSON != "" {
			_ = json.Unmarshal([]byte(metadataJSON), &m.Metadata)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}