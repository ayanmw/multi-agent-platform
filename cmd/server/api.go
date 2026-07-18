package main

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/cases"
	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/internal/cost"
	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/memory"
	"github.com/anmingwei/multi-agent-platform/internal/observability"
	"github.com/anmingwei/multi-agent-platform/internal/runtime"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
	"github.com/google/uuid"
)

// handleGetTaskContextWindow returns the current context-window snapshot for a
// task or a specific sub-task (GET /api/tasks/:id/context_window[?sub_task_id=xxx]).
// The URL path id is the root task ID. If sub_task_id is provided as a query
// parameter, the snapshot for that specific agent execution instance is returned;
// otherwise the root task (leader agent) snapshot is returned.
//
// For live tasks the snapshot is read from the in-memory runtime store written by
// Engine.think(). For persisted/idle tasks the snapshot is reconstructed from the
// task's own session_messages plus the agent's system prompt. Returns 404 if the
// task does not exist.
func handleGetTaskContextWindow(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	// Determine target sub-task identity. When omitted, default to the root task
	// so existing API consumers continue to work.
	subTaskID := r.URL.Query().Get("sub_task_id")
	targetID := subTaskID
	if targetID == "" {
		targetID = id
	}
	isSubTask := subTaskID != ""

	log.Printf("[ContextWindow] request task=%s sub_task_id=%s", id, subTaskID)

	// 1. Prefer the live in-memory snapshot when the engine is thinking.
	if snapshot, ok := runtime.GetTaskContextSnapshot(targetID); ok {
		log.Printf("[ContextWindow] task=%s sub_task_id=%s served from live runtime store", id, subTaskID)
		encodeContextWindowSnapshot(w, snapshot)
		return
	}

	// 2. Otherwise, reconstruct from persistence if the task exists.
	//    For sub-tasks we load the child task row; for the root we load the root.
	queryID := targetID
	if !isSubTask {
		queryID = id
	}
	task, err := db.QueryTaskByID(queryID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("[ContextWindow] task=%s not found", queryID)
			http.Error(w, "task not found", http.StatusNotFound)
			return
		}
		log.Printf("[ContextWindow] task=%s db error: %v", queryID, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if task == nil {
		log.Printf("[ContextWindow] task=%s not found (nil)", queryID)
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	log.Printf("[ContextWindow] task=%s found, session_id=%s agent_ids=%v", queryID, task.SessionID, task.AgentIDs)

	var messages []llm.Message

	// Reconstruct the system prompt from the task's primary agent. This is more
	// faithful than using the session name, which is just a user-facing label.
	systemPrompt := ""
	model := "unknown"
	if len(task.AgentIDs) > 0 {
		if agent, err := db.QueryAgentByID(task.AgentIDs[0]); err == nil && agent != nil {
			systemPrompt = agent.SystemPrompt
			model = agent.Model
			log.Printf("[ContextWindow] task=%s resolved model=%s from agent=%s", queryID, model, task.AgentIDs[0])
		} else if err != nil {
			log.Printf("[ContextWindow] task=%s QueryAgentByID failed: %v", queryID, err)
		}
	}
	if systemPrompt == "" {
		// 当 agent 系统提示不可用时，使用一段显式占位文本，而不是用 session.Name
		// 这种替代虽然不如原始 prompt 精确，但避免了用用户会话名充当系统提示的语义失真。
		systemPrompt = "[system prompt unavailable for historical task]"
		if task.SessionID != "" {
			if s, err := db.QuerySessionByID(task.SessionID); err != nil {
				log.Printf("[ContextWindow] task=%s 查询 session 失败，无法作为 system prompt fallback: %v", queryID, err)
			} else if s == nil || s.Name == "" {
				log.Printf("[ContextWindow] task=%s session 不存在或名称为空，system prompt 使用占位符", queryID)
			} else {
				log.Printf("[ContextWindow] task=%s 原始系统提示不可用，已用占位符代替；可归属 session=%s", queryID, s.Name)
			}
		}
	}

	// 3. Reconstruct from session_messages persisted during execution.
	//    session_messages is keyed by task_id, so for sub-tasks we look up
	//    the child task's own messages; for the root task we look up root.
	msgs, err := db.QuerySessionMessagesByTask(queryID)
	if err != nil {
		log.Printf("[ContextWindow] task=%s QuerySessionMessagesByTask failed: %v", queryID, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[ContextWindow] task=%s loaded session_messages count=%d", queryID, len(msgs))

	if len(msgs) > 0 {
		// The first persisted message is normally the system prompt. If the DB
		// already contains a system message, prepend with our recovered prompt
		// only when it differs, otherwise avoid duplicate system messages.
		hasSystem := false
		for _, m := range msgs {
			if m.Role == "system" {
				hasSystem = true
				break
			}
		}
		if !hasSystem && systemPrompt != "" {
			messages = append(messages, llm.Message{Role: "system", Content: systemPrompt})
		}

		for _, m := range msgs {
			var toolCalls []llm.ToolCall
			if m.ToolCalls != "" {
				if err := json.Unmarshal([]byte(m.ToolCalls), &toolCalls); err != nil {
					// ToolCalls 损坏时记录日志但继续返回其他字段，保持 API 可用。
					log.Printf("[ContextWindow] task=%s 解析 ToolCalls 失败: %v", queryID, err)
				}
			}
			messages = append(messages, llm.Message{
				Role:       m.Role,
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
				ToolCalls:  toolCalls,
			})
		}
	} else if systemPrompt != "" {
		// No persisted messages: the task existed but wrote nothing to
		// session_messages. Return a snapshot containing just the system prompt
		// so the UI is not stuck on 404.
		messages = append(messages, llm.Message{Role: "system", Content: systemPrompt})
	} else {
		log.Printf("[ContextWindow] task=%s no snapshot and no reconstructable messages", queryID)
		http.Error(w, "context window snapshot not available", http.StatusNotFound)
		return
	}

	if model == "unknown" || model == "" {
		// Try model from task's first agent if we failed earlier.
		if len(task.AgentIDs) > 0 {
			if agent, err := db.QueryAgentByID(task.AgentIDs[0]); err == nil && agent != nil && agent.Model != "" {
				model = agent.Model
			}
		}
	}

	maxTokens := llm.EstimateModelContextWindow(nil, model)
	snapshot := llm.BuildContextWindowSnapshot(model, maxTokens, messages)
	log.Printf("[ContextWindow] task=%s reconstructed snapshot model=%s messages=%d tokens=%d ratio=%.4f",
		queryID, snapshot.Model, len(snapshot.Messages), snapshot.EstimatedTotalTokens, snapshot.EstimatedUsageRatio)
	encodeContextWindowSnapshot(w, snapshot)
}

// encodeContextWindowSnapshot writes a snapshot as JSON. It reuses the same
// field names the WebSocket event emits so the frontend can treat both sources
// identically. Errors during encoding are logged but cannot be returned once
// the response has started.
func encodeContextWindowSnapshot(w http.ResponseWriter, snapshot llm.ContextWindowSnapshot) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snapshot); err != nil {
		log.Printf("[ContextWindow] 编码快照响应失败: %v", err)
	}
}

// handleGetAgentMessages returns the AgentBus message history for a task
// (GET /api/tasks/:id/agent-messages). It always returns a non-nil
// `messages` array — empty when the task has no inter-agent traffic — so the
// frontend can render the timeline without a null check.
func handleGetAgentMessages(w http.ResponseWriter, r *http.Request, taskID string) {
	msgs, err := db.QueryAgentMessages(taskID)
	if err != nil {
		log.Printf("[AgentMessages] query failed for task=%s: %v", taskID, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if msgs == nil {
		msgs = []db.AgentBusMessage{}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"task_id":  taskID,
		"messages": msgs,
	}); err != nil {
		log.Printf("[AgentMessages] encode response failed: %v", err)
	}
}

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

// handleGetTask returns a single task with its steps (GET /api/tasks?id=xxx).
// When an associated case evaluation exists, it is included under the
// "evaluation" key in a backward-compatible way.
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

	// For multi-agent root tasks, merge steps from child sub-tasks so the
	// root task detail view shows the complete execution history of all agents.
	stepIDs := make(map[string]bool)
	for _, s := range steps {
		stepIDs[s.ID] = true
	}
	for _, ct := range childTasks {
		childSteps, cErr := db.QueryStepsByTask(ct.ID)
		if cErr != nil {
			log.Printf("[API] GET /api/tasks?id=%s: child steps query error for %s: %v", taskID, ct.ID, cErr)
			continue
		}
		for _, cs := range childSteps {
			if !stepIDs[cs.ID] {
				steps = append(steps, cs)
				stepIDs[cs.ID] = true
			}
		}
	}
	// Sort merged steps by step_index for coherent ordering
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].StepIndex < steps[j].StepIndex })

	// Load the task's most recent evaluation (if any) to surface case pass/fail
	// state in the task detail view. case_evaluations is keyed by task_id; we
	// pick the latest row regardless of case_id so the API works for all tasks.
	eval, evalErr := queryLatestCaseEvaluation(taskID)
	if evalErr != nil {
		log.Printf("[API] GET /api/tasks?id=%s: evaluation query error: %v", taskID, evalErr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"task":        task,
		"steps":       steps,
		"child_tasks": childTasks,
		"evaluation":  eval,
	})
}

// queryLatestCaseEvaluation returns the most recent case evaluation for a
// task, or nil when none exists. Errors are returned so the caller can log
// them without failing the task detail request.
func queryLatestCaseEvaluation(taskID string) (map[string]any, error) {
	if db.DB == nil {
		return nil, errors.New("database not initialized")
	}
	var caseID string
	var passed int
	var score float64
	var reason string
	var evaluatedAt time.Time
	err := db.DB.QueryRow(`
		SELECT case_id, passed, score, reason, evaluated_at
		FROM case_evaluations
		WHERE task_id = ?
		ORDER BY evaluated_at DESC, id DESC
		LIMIT 1`, taskID).Scan(&caseID, &passed, &score, &reason, &evaluatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return map[string]any{
		"case_id":      caseID,
		"passed":       passed != 0,
		"score":        score,
		"reason":       reason,
		"evaluated_at": evaluatedAt,
	}, nil
}

// === Case API ===

// handleListCases handles GET /api/cases with optional tag and category filters.
// Multiple tags use OR semantics: a case matches if it contains ANY listed tag.
func handleListCases(w http.ResponseWriter, r *http.Request, svc *cases.Service) {
	tags := parseTagFilter(r.URL.Query().Get("tag"))
	category := strings.TrimSpace(r.URL.Query().Get("category"))

	all, err := svc.List(tags, category)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if all == nil {
		all = []cases.Case{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(all)
}

// parseTagFilter splits a comma-separated tag query parameter into normalized tags.
func parseTagFilter(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// handleGetCase handles GET /api/cases/{id}.
func handleGetCase(w http.ResponseWriter, r *http.Request, id string, svc *cases.Service) {
	c, err := svc.Get(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "case not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if c == nil {
		http.Error(w, "case not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c)
}

// handleCreateCase handles POST /api/cases.
func handleCreateCase(w http.ResponseWriter, r *http.Request, svc *cases.Service) {
	if svc == nil {
		http.Error(w, "case service unavailable", http.StatusServiceUnavailable)
		return
	}
	var req cases.CreateCaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	created, err := svc.Create(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(created)
}

// handleUpdateCase handles PUT /api/cases/{id}. Built-in cases are rejected.
func handleUpdateCase(w http.ResponseWriter, r *http.Request, id string, svc *cases.Service) {
	if svc == nil {
		http.Error(w, "case service unavailable", http.StatusServiceUnavailable)
		return
	}
	// Reject built-in cases explicitly so the 403 reason is clear.
	existing, err := svc.Get(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "case not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if existing == nil || existing.IsBuiltin {
		http.Error(w, "cannot modify built-in case", http.StatusForbidden)
		return
	}

	var req cases.UpdateCaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	updated, err := svc.Update(id, req)
	if err != nil {
		if errors.Is(err, cases.ErrBuiltinImmutable) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

// handleDeleteCase handles DELETE /api/cases/{id}. Built-in cases are rejected.
func handleDeleteCase(w http.ResponseWriter, r *http.Request, id string, svc *cases.Service) {
	if svc == nil {
		http.Error(w, "case service unavailable", http.StatusServiceUnavailable)
		return
	}
	existing, err := svc.Get(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "case not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if existing == nil || existing.IsBuiltin {
		http.Error(w, "cannot delete built-in case", http.StatusForbidden)
		return
	}

	if err := svc.Delete(id); err != nil {
		if errors.Is(err, cases.ErrBuiltinImmutable) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetCaseEvaluation returns the persisted evaluation for a specific
// task+case pair (GET /api/cases/{id}/evaluations/{task_id}).
// If no evaluation exists, it returns {"evaluation": null} with HTTP 200 so
// non-case tasks are handled gracefully.
func handleGetCaseEvaluation(w http.ResponseWriter, r *http.Request, id string, svc *cases.Service) {
	if svc == nil {
		http.Error(w, "case service unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	// Extract task_id from the trailing path segment.
	path := strings.TrimPrefix(r.URL.Path, "/api/cases/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[1] != "evaluations" {
		http.Error(w, "invalid evaluation URL", http.StatusBadRequest)
		return
	}
	taskID := strings.Join(parts[2:], "/")

	eval, err := svc.Repository().GetEvaluation(taskID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"evaluation": nil})
			return
		}
		log.Printf("[API] GET /api/cases/%s/evaluations/%s: db error: %v", id, taskID, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"evaluation": eval})
}

// === Session API ===

type createSessionRequest struct {
	Name         string `json:"name"`
	UserInput    string `json:"user_input"`
	ProjectID    string `json:"project_id"`
	WorkspaceDir string `json:"workspace_dir"` // optional: user-specified path; empty = auto
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

		// Resolve workspace directory according to the fallback rules:
		// 1. Explicit user path (validated/created) -> use it, isAuto=false
		// 2. Project's working_directory/session-{id} -> use it, isAuto=false
		// 3. ./workspace/session-{id}/ -> use it, isAuto=true
		workspaceDir, workspaceAuto := resolveWorkspaceDir(req.WorkspaceDir, req.ProjectID, sessionID)

		now := time.Now()
		sess := db.SessionRecord{
			ID:            sessionID,
			Name:          name,
			Status:        "empty",
			UserInput:     req.UserInput,
			ProjectID:     req.ProjectID,
			WorkspaceDir:  workspaceDir,
			WorkspaceAuto: workspaceAuto,
			CreatedAt:     now,
			UpdatedAt:     now,
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

		// Aggregate session duration alongside tokens so the frontend can
		// show session-level elapsed time without summing every task client-side.
		aggregateTokens, _ := db.AggregateSessionTokens(id)
		totalDuration, _ := db.AggregateSessionDuration(id)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"session":      sess,
			"tasks":        tasks,
			"total_tokens": aggregateTokens,
			"duration_ms":  totalDuration,
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
		// Fetch session first to get workspace_dir before deleting the DB record
		sessToDelete, sessErr := db.QuerySessionByID(id)
		if sessErr != nil {
			http.Error(w, "session not found: "+sessErr.Error(), http.StatusNotFound)
			return
		}
		// 删除 session 下所有 task 的上下文窗口快照缓存，避免内存泄漏。
		tasks, err := db.QueryTasksBySession(id)
		if err == nil {
			for _, t := range tasks {
				runtime.DeleteTaskContextSnapshot(t.ID)
			}
		} else {
			log.Printf("[API] DELETE /api/sessions/%s: 查询 tasks 失败，无法清理上下文快照: %v", id, err)
		}

		if err := db.DeleteSession(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Phase 7-C: audit log for destructive write operation.
		observability.DefaultAuditor.Record(observability.AuditRecord{
			Actor:  currentActor(r),
			Action: "delete_session",
			Target: id,
			Before: map[string]any{"id": id, "workspace_dir": sessToDelete.WorkspaceDir},
			After:  map[string]any{"deleted": true},
		})
		// Clean up workspace directory after DB deletion
		if sessToDelete.WorkspaceDir != "" {
			if rmErr := os.RemoveAll(sessToDelete.WorkspaceDir); rmErr != nil {
				log.Printf("[API] DELETE /api/sessions/%s: workspace cleanup failed: %v", id, rmErr)
			}
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

// resolveWorkspaceDir determines the workspace directory for a new session
// following the fallback rules:
//  1. Explicit user-specified path — validate or create it; isAuto=false
//  2. Project working_directory/session-{id}/ — isAuto=false
//  3. ./workspace/session-{id}/ — isAuto=true (default)
func resolveWorkspaceDir(specifiedPath, projectID, sessionID string) (workspaceDir string, isAuto bool) {
	// 1. Explicit user path: validate existence, or try to create it
	if specifiedPath != "" {
		if info, err := os.Stat(specifiedPath); err == nil && info.IsDir() {
			return specifiedPath, false
		}
		if err := os.MkdirAll(specifiedPath, 0755); err == nil {
			return specifiedPath, false
		}
		// Creation failed — fall through to default
	}

	// 2. Project working_directory: create session subdirectory
	if projectID != "" {
		proj, projErr := db.QueryProjectByID(projectID)
		if projErr == nil && proj.WorkingDirectory != "" {
			wsPath := filepath.Join(proj.WorkingDirectory, "workspace", "session-"+sessionID)
			if err := os.MkdirAll(wsPath, 0755); err == nil {
				return wsPath, false
			}
		}
	}

	// 3. Default: <cwd>/workspace/session-{id}/
	// Use an absolute path based on the current working directory so it is
	// independent of the directory the server binary was launched from.
	cwd, _ := os.Getwd()
	wsPath := filepath.Join(cwd, "workspace", "session-"+sessionID)
	if err := os.MkdirAll(wsPath, 0755); err == nil {
		return wsPath, true
	}
	return "", true // best-effort; empty path tolerated
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

// handleListMemories returns memory records filtered by scope, tier, type,
// status, and project with pagination.
// GET /api/memories?scope=session&tier=consolidated&type=rule&status=active&project=default&limit=20&offset=0
func handleListMemories(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project")
	if projectID == "" {
		projectID = "default"
	}
	scope := r.URL.Query().Get("scope")
	tier := r.URL.Query().Get("tier")
	memType := r.URL.Query().Get("type")
	status := r.URL.Query().Get("status")

	limit := 20
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if n, err := parseInt(limitStr); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if n, err := parseInt(offsetStr); err == nil && n >= 0 {
			offset = n
		}
	}

	items, total, err := db.ListMemoriesPaged(projectID, scope, tier, status, memType, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []db.MemoryRecord{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// memoryCreateRequest is the JSON body for POST /api/memories.
type memoryCreateRequest struct {
	ProjectID  string   `json:"project_id"`
	Scope      string   `json:"scope"` // session | project | global
	SessionID  string   `json:"session_id"`
	Type       string   `json:"type"` // preference | rule | fact | lesson | reflection
	Tier       string   `json:"tier"` // consolidated | semantic
	Content    string   `json:"content"`
	Confidence float64  `json:"confidence"`
	Status     string   `json:"status"`
	Tags       []string `json:"tags,omitempty"` // only used in metadata, may be ignored
}

// memoryUpdateRequest is the JSON body for PUT /api/memories/{id}.
type memoryUpdateRequest struct {
	Content    string  `json:"content"`
	Confidence float64 `json:"confidence"`
	Status     string  `json:"status"`
}

// handleMemoryByID handles GET / PUT / DELETE /api/memories/{id} and
// POST /api/memories/{id}/embed.
func handleMemoryByID(w http.ResponseWriter, r *http.Request, id string, hub *ws.Hub, vectorStore memory.VectorStore, embedProvider llm.EmbeddingProvider) {
	switch r.Method {
	case http.MethodGet:
		record, err := db.QueryMemoryByID(id)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "memory not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(record)

	case http.MethodPut:
		var req memoryUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Content == "" && req.Status == "" && req.Confidence == 0 {
			http.Error(w, "at least one field must be provided", http.StatusBadRequest)
			return
		}
		// Verify the memory exists before mutating.
		existing, err := db.QueryMemoryByID(id)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "memory not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fieldsChanged := []string{}
		if req.Content != "" && req.Content != existing.Content {
			if err := db.UpdateMemoryContent(id, req.Content); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			fieldsChanged = append(fieldsChanged, "content")
		}
		if req.Confidence != 0 && req.Confidence != existing.Confidence {
			if err := db.UpdateMemoryConfidence(id, req.Confidence); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			fieldsChanged = append(fieldsChanged, "confidence")
		}
		if req.Status != "" && req.Status != existing.Status {
			if err := db.UpdateMemoryStatus(id, req.Status); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			fieldsChanged = append(fieldsChanged, "status")
		}
		record, err := db.QueryMemoryByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Phase 7-C: audit log memory update.
		observability.DefaultAuditor.Record(observability.AuditRecord{
			Actor:  currentActor(r),
			Action: "update_memory",
			Target: id,
			Before: map[string]any{"content": existing.Content, "confidence": existing.Confidence, "status": existing.Status},
			After:  map[string]any{"content": record.Content, "confidence": record.Confidence, "status": record.Status, "fields_changed": fieldsChanged},
		})
		if hub != nil {
			hub.SendEvent(event.NewEvent(event.EventMemoryUpdated, "", "server", 0, map[string]any{
				"memory_id":      id,
				"fields_changed": fieldsChanged,
			}))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(record)

	case http.MethodDelete:
		// Reuse the existing delete handler logic so we don't diverge, then
		// broadcast the deletion event with the deleted memory details.
		record, lookupErr := db.QueryMemoryByID(id)
		if lookupErr != nil {
			if lookupErr == sql.ErrNoRows {
				http.Error(w, "memory not found", http.StatusNotFound)
				return
			}
			http.Error(w, lookupErr.Error(), http.StatusInternalServerError)
			return
		}
		handleDeleteMemory(w, r, id)
		// Phase 7-C: audit log memory deletion.
		observability.DefaultAuditor.Record(observability.AuditRecord{
			Actor:  currentActor(r),
			Action: "delete_memory",
			Target: id,
			Before: map[string]any{"id": id, "content": record.Content, "scope": record.Scope, "tier": record.Tier},
			After:  map[string]any{"deleted": true},
		})
		if hub != nil {
			hub.SendEvent(event.NewEvent(event.EventMemoryDeleted, "", "server", 0, map[string]any{
				"memory_id": id,
				"tier":      record.Tier,
				"scope":     record.Scope,
			}))
		}

	default:
		http.Error(w, "GET, PUT, or DELETE only", http.StatusMethodNotAllowed)
	}
}

// handleMemoryEmbed handles POST /api/memories/{id}/embed. It embeds the
// memory content and stores the vector in the configured VectorStore.
func handleMemoryEmbed(w http.ResponseWriter, r *http.Request, id string, hub *ws.Hub, vectorStore memory.VectorStore, embedProvider llm.EmbeddingProvider) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	record, err := db.QueryMemoryByID(id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "memory not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	vec, err := embedProvider.Embed(record.Content)
	if err != nil {
		// Degrade gracefully: the memory exists but its embedding could not be
		// computed. Return a 422 so the frontend notices without retry storms.
		log.Printf("[API] embed memory %s failed: %v", id, err)
		http.Error(w, "embedding failed: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	model := resolveProviderNameForAPI(embedProvider)
	dims := len(vec)
	metadata := map[string]any{
		"memory_id": record.ID,
		"type":      record.Type,
		"tier":      record.Tier,
		"scope":     record.Scope,
	}
	if err := vectorStore.Upsert(id, vec, metadata); err != nil {
		log.Printf("[API] vector store upsert for memory %s failed: %v", id, err)
		http.Error(w, "vector store upsert failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	encoded, err := encodeFloat32ToBytes(vec)
	if err != nil {
		log.Printf("[API] encode embedding for memory %s failed: %v", id, err)
	} else {
		if err := db.InsertOrReplaceMemoryEmbedding(db.DB, id, encoded, model, dims); err != nil {
			log.Printf("[API] persist embedding for memory %s failed: %v", id, err)
			// Embedding is in VectorStore already; DB persistence is best-effort.
		}
	}

	if hub != nil {
		hub.SendEvent(event.NewEvent(event.EventMemoryUpdated, "", "server", 0, map[string]any{
			"memory_id":       id,
			"fields_changed":  []string{"embedding_dims", "embedding_model"},
			"embedding_dims":  dims,
			"embedding_model": model,
		}))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"memory_id": id,
		"dims":      dims,
		"model":     model,
	})
}

// resolveProviderNameForAPI returns a human-readable model name for an
// embedding provider. Falls back to "unknown" when the provider is nil.
func resolveProviderNameForAPI(provider llm.EmbeddingProvider) string {
	if provider == nil {
		return "unknown"
	}
	return fmt.Sprintf("%T", provider)
}

// encodeFloat32ToBytes serializes a []float32 vector into a little-endian
// byte slice suitable for the memory_embeddings table.
func encodeFloat32ToBytes(vec []float32) ([]byte, error) {
	buf := make([]byte, 4*len(vec))
	for i, v := range vec {
		bits := math.Float32bits(v)
		binary.LittleEndian.PutUint32(buf[i*4:], bits)
	}
	return buf, nil
}

// handleUpdateMemoryScope updates the scope (and optional session_id) of a memory.
// PUT /api/memories/{id}/scope
// Body: {"scope": "project", "session_id": ""}
func handleUpdateMemoryScope(w http.ResponseWriter, r *http.Request, id string) {
	// Verify the memory exists before attempting to update.
	if _, err := db.QueryMemoryByID(id); err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "memory not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var req struct {
		Scope     string `json:"scope"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Scope != "session" && req.Scope != "project" && req.Scope != "global" {
		http.Error(w, "scope must be session, project, or global", http.StatusBadRequest)
		return
	}
	// Clear session_id when scope is not session so we don't leave stale values.
	sessionID := req.SessionID
	if req.Scope != "session" {
		sessionID = ""
	}
	if err := db.UpdateMemoryScope(id, req.Scope, sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":         id,
		"scope":      req.Scope,
		"session_id": sessionID,
		"message":    "Scope updated successfully",
	})
}

// handleDeleteMemory removes a memory record by ID.
// DELETE /api/memories/{id}
func handleDeleteMemory(w http.ResponseWriter, r *http.Request, id string) {
	// Verify the memory exists before attempting to delete.
	record, err := db.QueryMemoryByID(id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "memory not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := db.DeleteMemory(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Phase 7-C: audit log memory deletion at the lowest-level helper too,
	// so direct callers of handleDeleteMemory still produce an audit record.
	observability.DefaultAuditor.Record(observability.AuditRecord{
		Actor:  currentActor(r),
		Action: "delete_memory",
		Target: id,
		Before: map[string]any{"id": id, "content": record.Content, "scope": record.Scope, "tier": record.Tier},
		After:  map[string]any{"deleted": true},
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":      id,
		"tier":    record.Tier,
		"scope":   record.Scope,
		"message": "Memory deleted successfully",
	})
}

// handleCreateMemory creates a new memory from a user request.
// POST /api/memories
func handleCreateMemory(w http.ResponseWriter, r *http.Request, hub *ws.Hub, vectorStore memory.VectorStore, embedProvider llm.EmbeddingProvider) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req memoryCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Content == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}
	if req.Scope == "" {
		req.Scope = "project"
	}
	if req.Scope != "session" && req.Scope != "project" && req.Scope != "global" {
		http.Error(w, "scope must be session, project, or global", http.StatusBadRequest)
		return
	}
	if req.Type == "" {
		req.Type = "fact"
	}
	if !db.IsValidMemoryType(req.Type) {
		http.Error(w, "invalid memory type: "+req.Type, http.StatusBadRequest)
		return
	}
	if req.Tier == "" {
		req.Tier = "consolidated"
	}
	if req.Tier != "consolidated" && req.Tier != "semantic" {
		http.Error(w, "tier must be consolidated or semantic", http.StatusBadRequest)
		return
	}
	if req.ProjectID == "" {
		req.ProjectID = "default"
	}
	if req.Status == "" {
		req.Status = "active"
	}
	if req.Confidence == 0 {
		req.Confidence = 1.0
	}
	now := time.Now()
	id := uuid.New().String()
	record := db.MemoryRecord{
		ID:         id,
		ProjectID:  req.ProjectID,
		Scope:      req.Scope,
		SessionID:  req.SessionID,
		Type:       req.Type,
		Tier:       req.Tier,
		Content:    req.Content,
		Confidence: req.Confidence,
		Status:     req.Status,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := db.InsertMemory(record); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Best-effort embedding: do not fail the create API if embedding/vector
	// persistence is unavailable.
	if vectorStore != nil && embedProvider != nil {
		if vec, err := embedProvider.Embed(record.Content); err == nil {
			metadata := map[string]any{
				"memory_id": record.ID,
				"type":      record.Type,
				"tier":      record.Tier,
				"scope":     record.Scope,
			}
			if upsertErr := vectorStore.Upsert(id, vec, metadata); upsertErr != nil {
				log.Printf("[API] vector upsert for new memory %s failed: %v", id, upsertErr)
			}
			if encoded, encErr := encodeFloat32ToBytes(vec); encErr == nil {
				model := resolveProviderNameForAPI(embedProvider)
				if dbErr := db.InsertOrReplaceMemoryEmbedding(db.DB, id, encoded, model, len(vec)); dbErr != nil {
					log.Printf("[API] persist embedding for new memory %s failed: %v", id, dbErr)
				}
			}
		} else {
			log.Printf("[API] embed new memory %s failed: %v", id, err)
		}
	}
	if hub != nil {
		hub.SendEvent(event.NewEvent(event.EventMemoryCreated, "", "server", 0, map[string]any{
			"memory_id":  record.ID,
			"project_id": record.ProjectID,
			"scope":      record.Scope,
			"type":       record.Type,
			"tier":       record.Tier,
			"source":     "user",
		}))
	}
	created, err := db.QueryMemoryByID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(created)
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

// handleMemoryStats returns aggregate memory statistics for a project.
// GET /api/memories/stats?project=default
func handleMemoryStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	projectID := r.URL.Query().Get("project")
	if projectID == "" {
		projectID = "default"
	}
	grouped, err := db.CountMemoriesGrouped(projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if grouped == nil {
		grouped = map[string]int{}
	}
	// Add a computed total field for convenience.
	grouped["total"] = 0
	for k, v := range grouped {
		if strings.HasPrefix(k, "tier_") {
			grouped["total"] += v
		}
	}

	top, err := db.TopAccessedMemories(projectID, 10)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if top == nil {
		top = []db.MemoryRecord{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"project_id":   projectID,
		"counts":       grouped,
		"top_accessed": top,
	})
}

// handleRecallPreview previews what memories would be recalled for a given task.
// GET /api/memories/recall?task=xxx&project=default&max=3
// GET /api/memories/recall?query=xxx&project=default&max=3 — pure vector search
// This is a debugging endpoint — it shows the WorkingMemory that would be injected
// for a task without actually running the agent.
func handleRecallPreview(w http.ResponseWriter, r *http.Request, recall *harness.MemoryRecall) {
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

	// Vector query mode: GET /api/memories/recall?query=xxx
	// Performs pure vector similarity search and returns ranked MemoryItems.
	if queryParam := r.URL.Query().Get("query"); queryParam != "" {
		items, err := recall.RecallWithQuery(projectID, "", queryParam, maxEpisodes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"results": items,
			"query":   queryParam,
			"method":  "vector",
		})
		return
	}

	taskGoal := r.URL.Query().Get("task")
	if taskGoal == "" {
		http.Error(w, "task or query parameter required", http.StatusBadRequest)
		return
	}

	wm, err := recall.BuildWorkingMemory(projectID, "", taskGoal, maxEpisodes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Detect conflicts among the recalled memories
	allMemories := append(wm.ProjectRules, wm.ProjectEpisodes...)
	allMemories = append(allMemories, wm.SessionMemories...)
	allMemories = append(allMemories, wm.GlobalRules...)
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

		// Phase 7-C: audit log project deletion.
		observability.DefaultAuditor.Record(observability.AuditRecord{
			Actor:  currentActor(r),
			Action: "delete_project",
			Target: id,
			Before: map[string]any{"id": id},
			After:  map[string]any{"deleted": true},
		})

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
func handleSessionChat(w http.ResponseWriter, r *http.Request, hub *ws.Hub, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, approvalHandler harness.ApprovalHandler, memRecall *harness.MemoryRecall, agentBus runtime.AgentBus, checkpointMgr *runtime.CheckpointManager, memDB harness.CompressorDB, costRepo cost.CostRepository, modelRegistry *llm.ModelRegistry, modelRouter *llm.Router, routerProviders map[string]llm.Provider, caseService *cases.Service) {
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
		Input          string `json:"input"`
		AgentID        string `json:"agent_id"`
		SystemPrompt   string `json:"system_prompt"`
		MaxSteps       int    `json:"max_steps"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		// TaskContract optional overrides — when >0 / non-empty, override the
		// default contract so frontend can drive PolicyChain.
		Scope         string   `json:"scope"`
		AllowedTools  []string `json:"allowed_tools"`
		TokenBudget   int      `json:"token_budget"`
		CostBudgetUSD float64  `json:"cost_budget_usd"`
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

	// 上下文压缩：在创建新 Task 前检查是否需要压缩
	// Phase 6-F: pass the same LLM summarizer used by Heartbeat so summaries are
	// high quality. nil summarizer falls back to keyword path inside Compressor.
	compressor := harness.NewContextCompressor(memDB, nil)
	if result, err := compressor.CompressIfNeeded(id); err != nil {
		log.Printf("[SessionChat] Compression failed: %v", err)
	} else if result.Compressed {
		log.Printf("[SessionChat] Compressed %d turns for session %s, kept %d messages",
			result.TurnsCompressed, id, result.MessagesKept)
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
	if wm, err := memRecall.BuildWorkingMemory(projectID, id, req.Input, 3); err == nil {
		workingMemory = memRecall.FormatForSystemPrompt(wm)
	}

	// 创建新 Task
	taskID := newTaskID()
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
	// Override TaskContract fields from request body when provided —
	// lets the frontend drive PolicyChain (scope, tools, budgets) and timeout.
	if req.TimeoutSeconds > 0 {
		contract.TimeoutSeconds = req.TimeoutSeconds
	}
	if req.Scope != "" {
		contract.Scope = req.Scope
	}
	if len(req.AllowedTools) > 0 {
		contract.AllowedTools = req.AllowedTools
	}
	if req.TokenBudget > 0 {
		contract.TokenBudget = req.TokenBudget
	}
	if req.CostBudgetUSD > 0 {
		contract.CostBudgetUSD = req.CostBudgetUSD
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

		runAgentLoopWithTurn(hub, taskID, agentID, fullSystemPrompt, req.Input, cfg, tools, persist, contract, id, approvalHandler, workingMemory, agentBus, checkpointMgr, turnIndex, sess.RootTaskID, "", costRepo, modelRegistry, modelRouter, routerProviders, caseService)
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"session_id": id,
		"task_id":    taskID,
		"agent_id":   agentID,
		"turn_index": turnIndex,
		"status":     "started",
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

// handleCostQuery handles GET /api/costs with dimension filtering.
// Supported query parameters: task_id, session_id, project_id, days.
func handleCostQuery(w http.ResponseWriter, r *http.Request, repo cost.CostRepository) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	report := costReportFromRecords(func() []cost.CostRecord {
		if taskID := r.URL.Query().Get("task_id"); taskID != "" {
			records, _ := repo.QueryByTask(taskID)
			return records
		}
		if sessionID := r.URL.Query().Get("session_id"); sessionID != "" {
			records, _ := repo.QueryBySession(sessionID)
			return records
		}
		if projectID := r.URL.Query().Get("project_id"); projectID != "" {
			records, _ := repo.QueryByProject(projectID)
			return records
		}
		records, _ := repo.QueryRecent(100)
		return records
	}())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}

// costReportFromRecords builds a JSON-friendly cost summary from a slice of records.
func costReportFromRecords(records []cost.CostRecord) map[string]any {
	if records == nil {
		records = []cost.CostRecord{}
	}
	var totalCostUSD float64
	var totalCents int64
	var totalTokens, totalInput, totalOutput int
	byModel := make(map[string]float64)
	byAgent := make(map[string]float64)
	for _, rec := range records {
		totalCostUSD += rec.CostUSD
		totalCents += rec.CostCents
		totalTokens += rec.TotalTokens
		totalInput += rec.InputTokens
		totalOutput += rec.OutputTokens
		byModel[rec.Model] += rec.CostUSD
		byAgent[rec.AgentID] += rec.CostUSD
	}
	return map[string]any{
		"record_count":     len(records),
		"total_cost_usd":   totalCostUSD, // primary, full float64 precision (no /100 truncation)
		"total_cost_cents": totalCents,   // derived, backward-compatible integer sum
		"total_tokens":     totalTokens,
		"input_tokens":     totalInput,
		"output_tokens":    totalOutput,
		"by_model":         byModel, // float64 USD
		"by_agent":         byAgent, // float64 USD
		"records":          records,
	}
}

// truncateContent truncates a message content to maxLen characters.
// If the content is longer than maxLen, it appends "...".
func truncateContent(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "..."
}

// handleRunCase is a thin proxy for the CaseCard frontend.
// POST /api/run-case
// Body: {"input": "...", "agent_id": "...", "max_steps": N, "case": "code-gen", "session_id": "..."}
// It extracts the case identifier (from "case" or "case_id" field), then executes
// the same chat action logic as POST /api/tasks?case=<caseID>, with the case's
// default input and system prompt applied when the body does not override them.
func handleRunCase(w http.ResponseWriter, r *http.Request, hub *ws.Hub, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, approvalHandler harness.ApprovalHandler, memRecall *harness.MemoryRecall, agentBus runtime.AgentBus, checkpointMgr *runtime.CheckpointManager, memDB harness.CompressorDB, costRepo cost.CostRepository, modelRegistry *llm.ModelRegistry, modelRouter *llm.Router, routerProviders map[string]llm.Provider, caseService *cases.Service) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	// Parse the request body — accepts both "case" and "case_id" for the case identifier.
	var body struct {
		Input          string `json:"input"`
		AgentID        string `json:"agent_id"`
		SystemPrompt   string `json:"system_prompt"`
		MaxSteps       int    `json:"max_steps"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		Case           string `json:"case"`
		CaseID         string `json:"case_id"`
		SessionID      string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Resolve caseID: prefer "case", fall back to "case_id".
	caseID := body.Case
	if caseID == "" {
		caseID = body.CaseID
	}

	req := body
	if req.Input == "" {
		req.Input = body.Input
	}
	if req.AgentID == "" {
		req.AgentID = body.AgentID
	}
	if req.SystemPrompt == "" {
		req.SystemPrompt = body.SystemPrompt
	}
	if req.MaxSteps == 0 {
		req.MaxSteps = body.MaxSteps
	}
	if req.TimeoutSeconds == 0 {
		req.TimeoutSeconds = body.TimeoutSeconds
	}
	if req.SessionID == "" {
		req.SessionID = body.SessionID
	}

	// Mirror the chat-action logic from POST /api/tasks: apply case defaults,
	// then run the agent loop.
	var contract harness.TaskContract
	if caseID != "" {
		if c := cases.Get(caseID); c != nil {
			contract = c.Contract
			if req.Input == "" {
				req.Input = c.DefaultInput
			}
			if req.SystemPrompt == "" {
				req.SystemPrompt = c.SystemPrompt
			}
		}
	}

	if req.Input == "" {
		http.Error(w, "input is required", http.StatusBadRequest)
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

	if contract.Goal == "" {
		contract = harness.DefaultContract(req.Input)
	}
	if req.MaxSteps > 0 {
		contract.MaxSteps = req.MaxSteps
	}
	if req.TimeoutSeconds > 0 {
		contract.TimeoutSeconds = req.TimeoutSeconds
	}

	workingMemory := ""
	if wm, err := memRecall.BuildWorkingMemory("default", req.SessionID, req.Input, 3); err == nil {
		workingMemory = memRecall.FormatForSystemPrompt(wm)
	}

	taskID := newTaskID()
	go runAgentLoop(hub, taskID, agentID, systemPrompt, req.Input, cfg, tools, persist, contract, req.SessionID, approvalHandler, workingMemory, agentBus, checkpointMgr, caseID, costRepo, modelRegistry, modelRouter, routerProviders, caseService)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"task_id":    taskID,
		"agent_id":   agentID,
		"case_id":    caseID,
		"session_id": req.SessionID,
		"status":     "started",
	})
}

// handleAudit returns recent audit records from the default auditor.
// GET /api/audit?limit=N
func handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}
	records := observability.DefaultAuditor.List(limit)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(records)
}

// handleTraces returns all buffered trace spans from the process-level tracer.
// GET /api/traces
func handleTraces(w http.ResponseWriter, r *http.Request) {
	data, _ := tracer.JSON()
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// handleReplay replays task execution events from persistent storage.
// GET /api/replay/tasks/{task_id}
func handleReplay(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/replay/tasks/"), "/")
	taskID := parts[0]
	if taskID == "" {
		http.Error(w, "task_id required", http.StatusBadRequest)
		return
	}
	events := buildReplayEvents(taskID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

// buildReplayEvents reconstructs a flat event sequence from steps and conversations.
func buildReplayEvents(taskID string) []map[string]any {
	var events []map[string]any
	steps, _ := db.QueryStepsByTask(taskID)
	for _, s := range steps {
		events = append(events, map[string]any{
			"type":        s.Type,
			"task_id":     s.TaskID,
			"agent_id":    s.AgentID,
			"step_index":  s.StepIndex,
			"content":     s.Content,
			"tool_name":   s.ToolName,
			"tool_input":  s.ToolInput,
			"tool_output": s.ToolOutput,
			"timestamp":   s.ID, // steps table has no created_at; use id as stable ordering proxy
		})
	}
	convs, _ := db.QueryConversationsByTask(taskID)
	for _, c := range convs {
		events = append(events, map[string]any{
			"type":      c.Role + "_message",
			"task_id":   c.TaskID,
			"content":   c.Content,
			"timestamp": c.CreatedAt.UnixMilli(),
		})
	}
	return events
}
