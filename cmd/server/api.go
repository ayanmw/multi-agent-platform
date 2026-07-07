package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/runtime"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/google/uuid"
)

// === Task History API ===

// handleListTasks returns recent tasks (GET /api/tasks)
func handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := db.QueryTasks(50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tasks == nil {
		tasks = []db.TaskRecord{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

// handleGetTask returns a single task with its steps (GET /api/tasks?id=xxx)
func handleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	log.Printf("[API] GET /api/tasks?id=%s", taskID)
	if taskID == "" {
		http.Error(w, "id query parameter required", http.StatusBadRequest)
		return
	}

	task, err := db.QueryTaskByID(taskID)
	if err != nil {
		log.Printf("[API] GET /api/tasks?id=%s: task not found: %v", taskID, err)
		http.Error(w, "task not found: "+err.Error(), http.StatusNotFound)
		return
	}

	steps, err := db.QueryStepsByTask(taskID)
	if err != nil {
		log.Printf("[API] GET /api/tasks?id=%s: steps query error: %v", taskID, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if steps == nil {
		steps = []db.StepRecord{}
	}

	childTasks, err := db.QueryChildTasks(taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if childTasks == nil {
		childTasks = []db.TaskRecord{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"task":          task,
		"steps":         steps,
		"child_tasks":   childTasks,
	})
}

// === Session API ===

type createSessionRequest struct {
	Name      string `json:"name"`
	UserInput string `json:"user_input"`
	ProjectID string `json:"project_id"`
}

type renameSessionRequest struct {
	Name string `json:"name"`
}

// handleSessions handles GET/POST /api/sessions
func handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projectID := r.URL.Query().Get("project_id")
			sessions, err := db.QuerySessions(50, projectID)
		if err != nil {
			log.Printf("[API] GET /api/sessions error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if sessions == nil {
			sessions = []db.SessionRecord{}
		}
		for i := range sessions {
			total, err := db.AggregateSessionTokens(sessions[i].ID)
			if err == nil {
				sessions[i].TotalTokens = total
			}
			// Fallback: if root_task_id is empty, discover it from the session's tasks.
			// This handles sessions created before the root_task_id binding was implemented.
			if sessions[i].RootTaskID == "" {
				tasks, tErr := db.QueryTasksBySession(sessions[i].ID)
				if tErr == nil {
					for _, t := range tasks {
						if t.IsRoot {
							sessions[i].RootTaskID = t.ID
							// Persist the discovered root_task_id so we don't need to rediscover
							db.UpdateSession(sessions[i].ID, t.ID, sessions[i].Status, sessions[i].UserInput)
							log.Printf("[API] GET /api/sessions: auto-discovered root_task_id=%s for session %s", t.ID, sessions[i].ID)
							break
						}
					}
				}
			}
		}
		log.Printf("[API] GET /api/sessions: returning %d sessions", len(sessions))
		for _, s := range sessions {
			log.Printf("[API]   session: id=%s name=%s root_task_id=%q status=%s", s.ID, s.Name, s.RootTaskID, s.Status)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sessions)

	case http.MethodPost:
		var req createSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		sessionID := "sess_" + uuid.New().String()
		name := req.Name
		if name == "" {
			name = extractSessionName(req.UserInput)
		}

		now := time.Now()
		sess := db.SessionRecord{
			ID:        sessionID,
			Name:      name,
			Status:    "empty",
			UserInput: req.UserInput,
			ProjectID: req.ProjectID,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := db.InsertSession(sess); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"session_id": sessionID,
			"status":     "empty",
		})

	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

// handleSessionByID handles GET/PUT/DELETE /api/sessions/{id}
func handleSessionByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if id == "" || id == "/" {
		http.Error(w, "session ID required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		sess, err := db.QuerySessionByID(id)
		if err != nil {
			http.Error(w, "session not found: "+err.Error(), http.StatusNotFound)
			return
		}

		tasks, err := db.QueryTasksBySession(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if tasks == nil {
			tasks = []db.TaskRecord{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"session": sess,
			"tasks":   tasks,
		})

	case http.MethodPut:
		var req renameSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		if err := db.UpdateSessionName(id, req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sess, err := db.QuerySessionByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sess)

	case http.MethodDelete:
		if err := db.DeleteSession(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      id,
			"message": "Session deleted successfully",
		})

	default:
		http.Error(w, "GET, PUT, or DELETE only", http.StatusMethodNotAllowed)
	}
}

// extractSessionName generates a display name from user input.
func extractSessionName(input string) string {
	if input == "" {
		return "New Session"
	}
	// Remove newlines and extra spaces
	clean := strings.Join(strings.Fields(input), " ")
	if len(clean) > 30 {
		return clean[:30] + "..."
	}
	return clean
}

// === Agent CRUD API ===

// agentRequest is the JSON body for agent create/update
type agentRequest struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	SystemPrompt string   `json:"system_prompt"`
	Model        string   `json:"model"`
	Endpoint     string   `json:"api_endpoint"`
	APIKey       string   `json:"api_key"`
	Temperature  float64  `json:"temperature"`
	MaxTokens    int      `json:"max_tokens"`
	Tools        []string `json:"tools"`
}

// handleAgents handles GET/POST /api/agents
func handleAgents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		agents, err := db.QueryAgents()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if agents == nil {
			agents = []db.AgentRecord{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agents)

	case http.MethodPost:
		var req agentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		id := uuid.New().String()
		if err := db.InsertAgent(id, req.Name, req.Description, req.SystemPrompt, req.Model, req.Endpoint, req.APIKey, req.Temperature, req.MaxTokens, req.Tools, false); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		agent, err := db.QueryAgentByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(agent)

	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

// handleAgentByID handles GET/PUT/DELETE /api/agents/{id}
func handleAgentByID(w http.ResponseWriter, r *http.Request) {
	// Extract agent ID from URL path: /api/agents/{id}
	id := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	if id == "" || id == "/" {
		http.Error(w, "agent ID required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		agent, err := db.QueryAgentByID(id)
		if err != nil {
			http.Error(w, "agent not found: "+err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agent)

	case http.MethodPut:
		var req agentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := db.UpdateAgent(id, req.Name, req.Description, req.SystemPrompt, req.Model, req.Endpoint, req.APIKey, req.Temperature, req.MaxTokens, req.Tools); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		agent, err := db.QueryAgentByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agent)

	case http.MethodDelete:
		if err := db.DeleteAgent(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      id,
			"message": "Agent deleted successfully",
		})

	default:
		http.Error(w, "GET, PUT, or DELETE only", http.StatusMethodNotAllowed)
	}
}

// === Memory API (Phase 6) ===

// handleListMemories returns memory records filtered by tier and project.
// GET /api/memories?tier=consolidated&project=default
func handleListMemories(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project")
	if projectID == "" {
		projectID = "default"
	}
	tier := r.URL.Query().Get("tier")

	var memories []db.MemoryRecord
	var err error
	if tier != "" {
		memories, err = db.QueryMemoriesByTier(projectID, tier)
	} else {
		memories, err = db.QueryMemoriesByProject(projectID)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if memories == nil {
		memories = []db.MemoryRecord{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(memories)
}

// handlePromoteMemories triggers the promotion pipeline manually.
// POST /api/memories/promote
// Body: {"project_id": "default"}
func handlePromoteMemories(w http.ResponseWriter, r *http.Request, gate *harness.PromotionGate) {
	var req struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Accept empty body — use default project
		req.ProjectID = "default"
	}
	if req.ProjectID == "" {
		req.ProjectID = "default"
	}

	report, err := gate.PromoteCandidates(req.ProjectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}

// handleRecallPreview previews what memories would be recalled for a given task.
// GET /api/memories/recall?task=xxx&project=default&max=3
// This is a debugging endpoint — it shows the WorkingMemory that would be injected
// for a task without actually running the agent.
func handleRecallPreview(w http.ResponseWriter, r *http.Request, recall *harness.MemoryRecall) {
	taskGoal := r.URL.Query().Get("task")
	if taskGoal == "" {
		http.Error(w, "task query parameter required", http.StatusBadRequest)
		return
	}

	projectID := r.URL.Query().Get("project")
	if projectID == "" {
		projectID = "default"
	}

	maxEpisodes := 3
	if maxStr := r.URL.Query().Get("max"); maxStr != "" {
		if n, err := parseInt(maxStr); err == nil && n > 0 {
			maxEpisodes = n
		}
	}

	wm, err := recall.BuildWorkingMemory(projectID, taskGoal, maxEpisodes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Detect conflicts among the recalled memories
	allMemories := append(wm.StableRules, wm.RelatedEpisodes...)
	conflicts := recall.DetectConflicts(allMemories)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"working_memory": wm,
		"formatted":      recall.FormatForSystemPrompt(wm),
		"conflicts":      conflicts,
	})
}

// parseInt parses a simple integer string. Used for URL query parameter parsing
// where we don't need the full strconv import for a single value.
func parseInt(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not a valid integer")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// === Project API ===

// projectRequest is the JSON body for project create/update
type projectRequest struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	WorkingDirectory string `json:"working_directory"`
}

// projectSummary is the compact view returned in list endpoints.
// It includes counts computed from related tables.
type projectSummary struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	WorkingDirectory string `json:"working_directory"`
	SessionCount     int    `json:"session_count"`
	MemoryCount      int    `json:"memory_count"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

// handleProjects handles GET/POST /api/projects
// GET: 列出所有项目（含 sessions 计数和记忆统计）
// POST: 创建新项目
func handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projects, err := db.QueryProjects()
		if err != nil {
			log.Printf("[API] GET /api/projects error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if projects == nil {
			projects = []db.ProjectRecord{}
		}

		// Build summaries with session and memory counts
		summaries := make([]projectSummary, 0, len(projects))
		for _, p := range projects {
			summary := projectSummary{
				ID:               p.ID,
				Name:             p.Name,
				Description:      p.Description,
				WorkingDirectory: p.WorkingDirectory,
				CreatedAt:        p.CreatedAt.Format(time.RFC3339),
				UpdatedAt:        p.UpdatedAt.Format(time.RFC3339),
			}

			// Count sessions for this project
			sessions, err := db.QuerySessionsByProject(p.ID, 1000)
			if err == nil {
				summary.SessionCount = len(sessions)
			}

			// Count memories for this project
			memories, err := db.QueryMemoriesByProject(p.ID)
			if err == nil {
				summary.MemoryCount = len(memories)
			}

			summaries = append(summaries, summary)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(summaries)

	case http.MethodPost:
		var req projectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		id := uuid.New().String()
		now := time.Now()
		proj := db.ProjectRecord{
			ID:               id,
			Name:             req.Name,
			Description:      req.Description,
			WorkingDirectory: req.WorkingDirectory,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if err := db.InsertProject(proj); err != nil {
			log.Printf("[API] POST /api/projects error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		created, err := db.QueryProjectByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)

	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

// handleProjectByID handles GET/PUT/DELETE /api/projects/{id}
// GET: 返回项目详情（含 sessions 列表、记忆统计）
// PUT: 更新项目（名称、工作目录、描述）
// DELETE: 删除项目（级联删除所有关联数据）
func handleProjectByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	if id == "" || id == "/" {
		http.Error(w, "project ID required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		project, err := db.QueryProjectByID(id)
		if err != nil {
			http.Error(w, "project not found: "+err.Error(), http.StatusNotFound)
			return
		}

		sessions, err := db.QuerySessionsByProject(id, 100)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if sessions == nil {
			sessions = []db.SessionRecord{}
		}

		// Compute memory stats: total, consolidated, semantic
		memories, err := db.QueryMemoriesByProject(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		totalMem := 0
		consolidated := 0
		semantic := 0
		for _, m := range memories {
			totalMem++
			switch m.Tier {
			case "consolidated":
				consolidated++
			case "semantic":
				semantic++
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"project":  project,
			"sessions": sessions,
			"memory_stats": map[string]int{
				"total":        totalMem,
				"consolidated": consolidated,
				"semantic":     semantic,
			},
		})

	case http.MethodPut:
		var req projectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		// Fetch existing project to preserve its config
		existing, err := db.QueryProjectByID(id)
		if err != nil {
			http.Error(w, "project not found: "+err.Error(), http.StatusNotFound)
			return
		}

		if err := db.UpdateProject(id, req.Name, req.Description, req.WorkingDirectory, existing.Config); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		updated, err := db.QueryProjectByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updated)

	case http.MethodDelete:
		// Protect the default project from deletion
		if id == "default" {
			http.Error(w, "cannot delete the default project", http.StatusBadRequest)
			return
		}

		if err := db.DeleteProject(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      id,
			"message": "Project deleted successfully",
		})

	default:
		http.Error(w, "GET, PUT, or DELETE only", http.StatusMethodNotAllowed)
	}
}

// === Multi-Turn Chat API ===

// handleSessionChat handles POST /api/sessions/{id}/chat
// 在已有 Session 中发起新轮对话，自动注入历史消息上下文
func handleSessionChat(w http.ResponseWriter, r *http.Request, hub *ws.Hub, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, approvalHandler harness.ApprovalHandler, memRecall *harness.MemoryRecall, agentBus runtime.AgentBus, checkpointMgr *runtime.CheckpointManager) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	// 提取 session ID
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	id = strings.TrimSuffix(id, "/chat")
	if id == "" || id == "/" {
		http.Error(w, "session ID required", http.StatusBadRequest)
		return
	}

	// 解析请求
	var req struct {
		Input        string `json:"input"`
		AgentID      string `json:"agent_id"`
		SystemPrompt string `json:"system_prompt"`
		MaxSteps     int    `json:"max_steps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Input == "" {
		http.Error(w, "input is required", http.StatusBadRequest)
		return
	}

	// 查询 session
	sess, err := db.QuerySessionByID(id)
	if err != nil {
		http.Error(w, "session not found: "+err.Error(), http.StatusNotFound)
		return
	}

	agentID := req.AgentID
	if agentID == "" {
		agentID = "agent_default"
	}

	systemPrompt := req.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a helpful AI assistant with access to tools. " +
			"When you need to run commands, read files, or write files, use the available tools. " +
			"Always explain your reasoning before using tools. " +
			"After using tools, analyze the results and continue until the task is complete."
	}

	// 加载历史消息
	historyMessages, err := db.QuerySessionMessages(id)
	if err != nil {
		log.Printf("[SessionChat] Failed to load history messages: %v", err)
		historyMessages = nil
	}

	// 构建历史上下文文本
	var historyContext string
	if len(historyMessages) > 0 {
		historyContext = buildHistoryContext(historyMessages)
	}

	// 加载 Memory Recall
	workingMemory := ""
	projectID := sess.ProjectID
	if projectID == "" {
		projectID = "default"
	}
	if wm, err := memRecall.BuildWorkingMemory(projectID, req.Input, 3); err == nil {
		workingMemory = memRecall.FormatForSystemPrompt(wm)
	}

	// 创建新 Task
	taskID := "task_" + time.Now().Format("20060102150405")
	turnIndex := sess.TurnCount // 当前轮次（0-based）

	// 持久化 Task
	if persist != nil {
		persist.SaveTask(taskID, req.Input, []string{agentID})
		persist.SaveTaskMeta(taskID, id, sess.RootTaskID, false) // 非 root task，parent = root
	}

	// 启动 Agent Loop
	contract := harness.DefaultContract(req.Input)
	if req.MaxSteps > 0 {
		contract.MaxSteps = req.MaxSteps
	}

	go func() {
		// 构建完整的 system prompt（Working Memory + 历史上下文 + 原始 system prompt）
		fullSystemPrompt := systemPrompt
		if workingMemory != "" {
			fullSystemPrompt = workingMemory + "\n\n" + fullSystemPrompt
		}
		if historyContext != "" {
			fullSystemPrompt = historyContext + "\n\n" + fullSystemPrompt
		}

		runAgentLoopWithTurn(hub, taskID, agentID, fullSystemPrompt, req.Input, cfg, tools, persist, contract, id, approvalHandler, workingMemory, agentBus, checkpointMgr, turnIndex, sess.RootTaskID)
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"session_id":  id,
		"task_id":     taskID,
		"agent_id":    agentID,
		"turn_index":  turnIndex,
		"status":      "started",
	})
}

// handleSessionMessages handles GET /api/sessions/{id}/messages
// 返回指定 Session 的所有历史消息（按 turn_index ASC, created_at ASC）
func handleSessionMessages(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	msgs, err := db.QuerySessionMessages(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if msgs == nil {
		msgs = []db.SessionMessageRecord{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(msgs)
}

// buildHistoryContext 将历史消息格式化为上下文文本，按轮次分组
func buildHistoryContext(msgs []db.SessionMessageRecord) string {
	var sb strings.Builder
	sb.WriteString("## Previous Conversation History\n\n")
	currentTurn := -1
	for _, m := range msgs {
		if m.TurnIndex != currentTurn {
			currentTurn = m.TurnIndex
			sb.WriteString(fmt.Sprintf("### Turn %d\n\n", currentTurn+1))
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", m.Role, truncateContent(m.Content, 500)))
	}
	sb.WriteString("## Current Task\n\n")
	return sb.String()
}

// truncateContent truncates a message content to maxLen characters.
// If the content is longer than maxLen, it appends "...".
func truncateContent(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "..."
}
