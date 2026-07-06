package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

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
	if taskID == "" {
		http.Error(w, "id query parameter required", http.StatusBadRequest)
		return
	}

	task, err := db.QueryTaskByID(taskID)
	if err != nil {
		http.Error(w, "task not found: "+err.Error(), http.StatusNotFound)
		return
	}

	steps, err := db.QueryStepsByTask(taskID)
	if err != nil {
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
		if err := db.InsertAgent(id, req.Name, req.Description, req.SystemPrompt, req.Model, req.Endpoint, req.APIKey, req.Temperature, req.MaxTokens, req.Tools); err != nil {
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