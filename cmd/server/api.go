package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/harness"
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
}

type renameSessionRequest struct {
	Name string `json:"name"`
}

// handleSessions handles GET/POST /api/sessions
func handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sessions, err := db.QuerySessions(50)
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