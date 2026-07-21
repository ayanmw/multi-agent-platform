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
	"github.com/anmingwei/multi-agent-platform/internal/todo"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
	"github.com/google/uuid"
)

// handleGetTaskContextWindow returns the current context-window snapshot for a
// task or a specific sub-task (GET /api/tasks/:id/context_window[?sub_task_id=xxx]).
// URL path 中的 id 是 root task ID。如果提供了 query 参数 sub_task_id，
// 则返回该具体 agent 执行实例的 snapshot；否则返回 root task（leader agent）
// 的 snapshot。
//
// 对于 live task，snapshot 读取自 Engine.think() 写入的内存 runtime store。
// 对于已持久化/idle 的 task，snapshot 由该 task 自身的 session_messages
// 加上 agent 的 system prompt 重建。task 不存在时返回 404。
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
		// 第一条持久化 message 通常是 system prompt。如果 DB 已包含
		// system message，仅在恢复出的 prompt 与之不同时才前置，避免
		// 出现重复的 system message。
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

// encodeContextWindowSnapshot 把快照写成 JSON。它复用 WebSocket 事件使用的
// 字段名，让前端可以把两个来源同等对待。响应已开始后，编码错误只能记录
// 而无法再返回。
func encodeContextWindowSnapshot(w http.ResponseWriter, snapshot llm.ContextWindowSnapshot) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snapshot); err != nil {
		log.Printf("[ContextWindow] 编码快照响应失败: %v", err)
	}
}

// handleGetAgentMessages 返回某任务的 AgentBus 消息历史
// （GET /api/tasks/:id/agent-messages）。它始终返回非 nil 的
// `messages` 数组 —— 任务没有跨 agent 流量时为空 —— 让前端
// 渲染时间线时无需做 null 检查。
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

// handleListTasks 返回最近的任务（GET /api/tasks）
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

// ChildTaskDetail 在 TaskRecord 基础上附带子任务的 steps，用于 root task
// 详情一次性返回完整的子任务执行历史。
type ChildTaskDetail struct {
	db.TaskRecord
	Steps []db.StepRecord `json:"steps"`
}

// handleGetTask 返回单个任务及其步骤（GET /api/tasks?id=xxx）。
// 若存在关联的 case evaluation，则以向后兼容的方式放在 "evaluation" key 下。
// Phase 7-H2 MA5: 返回的 child_tasks 每个元素都附带自己的 steps 数组，
// 让前端在刷新/历史回放时能把子 agent 的步骤回填到对应 worker lane。
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

	// 对 multi-agent root 任务，合并子任务的步骤，让 root 任务详情页
	// 能展示所有 agent 的完整执行历史。
	stepIDs := make(map[string]bool)
	for _, s := range steps {
		stepIDs[s.ID] = true
	}

	// Phase 7-H2 MA5: 为每个 child task 查询并附加 steps；同时把 root 尚未收集
	// 到的子步骤合并进 root steps，保证既有 UI（只看 root.steps）也能完整展示。
	childDetails := make([]ChildTaskDetail, 0, len(childTasks))
	for _, ct := range childTasks {
		childSteps, cErr := db.QueryStepsByTask(ct.ID)
		if cErr != nil {
			log.Printf("[API] GET /api/tasks?id=%s: child steps query error for %s: %v", taskID, ct.ID, cErr)
			childSteps = []db.StepRecord{}
		}
		for _, cs := range childSteps {
			if !stepIDs[cs.ID] {
				steps = append(steps, cs)
				stepIDs[cs.ID] = true
			}
		}
		childDetails = append(childDetails, ChildTaskDetail{
			TaskRecord: ct,
			Steps:      childSteps,
		})
	}
	// 合并后的步骤按 step_index 排序以保证连贯顺序
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].StepIndex < steps[j].StepIndex })

	// 加载任务最近的一次 evaluation（若有），在任务详情页展示 case 通过/失败
	// 状态。case_evaluations 以 task_id 为键；这里不区分 case_id 取最新一行，
	// 让 API 对所有任务都可用。
	eval, evalErr := queryLatestCaseEvaluation(taskID)
	if evalErr != nil {
		log.Printf("[API] GET /api/tasks?id=%s: evaluation query error: %v", taskID, evalErr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"task":        task,
		"steps":       steps,
		"child_tasks": childDetails,
		"evaluation":  eval,
	})
}

// queryLatestCaseEvaluation 返回某任务最近的 case evaluation，没有则返回 nil。
// 错误返回给调用方，让其记录日志但不让任务详情请求失败。
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

// handleListCases 处理 GET /api/cases，支持可选的 tag 与 category 过滤。
// 多个 tag 采用 OR 语义：case 只要包含任一所列 tag 即匹配。
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

// parseTagFilter 把逗号分隔的 tag 查询参数切成规整化的 tag 列表。
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

// handleGetCase 处理 GET /api/cases/{id}。
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

// handleCreateCase 处理 POST /api/cases。
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

// handleUpdateCase 处理 PUT /api/cases/{id}。内置 case 会被拒绝。
func handleUpdateCase(w http.ResponseWriter, r *http.Request, id string, svc *cases.Service) {
	if svc == nil {
		http.Error(w, "case service unavailable", http.StatusServiceUnavailable)
		return
	}
	// 显式拒绝内置 case，让 403 原因清晰。
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

// handleDeleteCase 处理 DELETE /api/cases/{id}。内置 case 会被拒绝。
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

// handleGetCaseEvaluation 返回某 task+case 配对的持久化 evaluation
// （GET /api/cases/{id}/evaluations/{task_id}）。
// 若没有 evaluation，则返回 {"evaluation": null} 与 HTTP 200，
// 让非 case 任务也能被优雅处理。
func handleGetCaseEvaluation(w http.ResponseWriter, r *http.Request, id string, svc *cases.Service) {
	if svc == nil {
		http.Error(w, "case service unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	// 从末尾 path 段提取 task_id。
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
	WorkspaceDir string `json:"workspace_dir"` // 可选：用户指定的路径；为空则自动
}

// updateSessionRequest 是 PUT /api/sessions/{id} 的 JSON body。
//
// workspace_dir 用指针类型：区分"未提供"（nil，走旧"仅重命名"路径，保留
// 既有 workspace）与"显式空串"（清空 workspace，回退 auto/project）。空
// 字符串在 JSON 里是合法值，必须用指针才能与"字段缺省"区分开，否则旧
// 客户端只发 {name} 会被误解成"清空 workspace"。
type updateSessionRequest struct {
	Name         string  `json:"name"`
	WorkspaceDir *string `json:"workspace_dir"`
}

// handleSessions 处理 GET/POST /api/sessions
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
			// 兜底：若 root_task_id 为空，则从 session 的任务中重新发现它。
			// 这覆盖了 root_task_id 绑定实现之前创建的 session。
			if sessions[i].RootTaskID == "" {
				tasks, tErr := db.QueryTasksBySession(sessions[i].ID)
				if tErr == nil {
					for _, t := range tasks {
						if t.IsRoot {
							sessions[i].RootTaskID = t.ID
							// 把发现的 root_task_id 持久化，避免下次再重新发现
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

		// 按以下兜底规则确定 workspace 目录：
		// 1. 用户显式路径（校验/创建）-> 使用它，isAuto=false
		// 2. Project 的 working_directory/session-{id} -> 使用它，isAuto=false
		// 3. ./workspace/session-{id}/ -> 使用它，isAuto=true
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

// handleSessionByID 处理 GET/PUT/DELETE /api/sessions/{id}
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

		// 同时聚合 session 时长与 token，让前端无需逐任务求和即可显示
		// session 级别的耗时。
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
		var req updateSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		// workspace_dir 字段未提供（指针为 nil）→ 旧客户端的"仅重命名"路径，
		// 只更新 name，保留既有 workspace_dir/workspace_auto。
		// workspace_dir 字段被显式提供（指针非 nil，值可为空串）→ 完整元数据更新，
		// 同时改 name 与 workspace。
		if req.WorkspaceDir == nil {
			if err := db.UpdateSessionName(id, req.Name); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			ws := strings.TrimSpace(*req.WorkspaceDir)
			// 自定义路径需要确保目录存在；空串表示回退 auto，无需创建。
			if ws != "" {
				if info, err := os.Stat(ws); err == nil {
					if !info.IsDir() {
						http.Error(w, "workspace_dir is not a directory", http.StatusBadRequest)
						return
					}
				} else {
					// 目录不存在则尝试创建，失败则报错让前端明确知道。
					if err := os.MkdirAll(ws, 0755); err != nil {
						http.Error(w, "cannot create workspace_dir: "+err.Error(), http.StatusBadRequest)
						return
					}
				}
			}
			if err := db.UpdateSessionMeta(id, req.Name, ws); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		sess, err := db.QuerySessionByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sess)

	case http.MethodDelete:
		// 删 DB 记录前先取出 session，以拿到 workspace_dir
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
		// Phase 7-C：破坏性写操作的 audit log。
		observability.DefaultAuditor.Record(observability.AuditRecord{
			Actor:  currentActor(r),
			Action: "delete_session",
			Target: id,
			Before: map[string]any{"id": id, "workspace_dir": sessToDelete.WorkspaceDir},
			After:  map[string]any{"deleted": true},
		})
		// DB 删除后再清理 workspace 目录
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

// extractSessionName 从用户输入生成展示名称。
func extractSessionName(input string) string {
	if input == "" {
		return "New Session"
	}
	// 去除换行与多余空格
	clean := strings.Join(strings.Fields(input), " ")
	if len(clean) > 30 {
		return clean[:30] + "..."
	}
	return clean
}

// resolveWorkspaceDir 按以下兜底规则为新 session 决定 workspace 目录：
//  1. 用户显式路径 —— 校验或创建；isAuto=false
//  2. Project working_directory —— 当 session.workspace_dir 为空时，
//     运行时工具链回退到 project.working_directory。因此创建 session 时
//     不再自动在 project 下创建 session 子目录，而是保持空字符串，
//     让多个 session 共享同一个 project workspace。
//  3. ./workspace/session-{id}/ —— project 无 working_directory 或用户
//     显式选择 auto 模式时使用，isAuto=true（默认）
func resolveWorkspaceDir(specifiedPath, projectID, sessionID string) (workspaceDir string, isAuto bool) {
	// 1. 用户显式路径：校验存在性，否则尝试创建
	if specifiedPath != "" {
		if info, err := os.Stat(specifiedPath); err == nil && info.IsDir() {
			return specifiedPath, false
		}
		if err := os.MkdirAll(specifiedPath, 0755); err == nil {
			return specifiedPath, false
		}
		// 创建失败 —— 落到默认值
	}

	// 2. Project working_directory：保持 session.WorkspaceDir 为空，
	//    由运行时根据 session.ProjectID 回退到 project.WorkingDirectory。
	//    这样多个 session 可共享同一个 project workspace。
	if projectID != "" {
		proj, projErr := db.QueryProjectByID(projectID)
		if projErr == nil && proj.WorkingDirectory != "" {
			return "", false
		}
	}

	// 3. 默认：<cwd>/workspace/session-{id}/
	// 用基于当前工作目录的绝对路径，使其不依赖 server binary 启动时的目录。
	cwd, _ := os.Getwd()
	wsPath := filepath.Join(cwd, "workspace", "session-"+sessionID)
	if err := os.MkdirAll(wsPath, 0755); err == nil {
		return wsPath, true
	}
	return "", true // 尽力而为；空路径也能容忍
}

// === Agent CRUD API ===

// agentRequest 是 agent create/update 的 JSON body
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

// handleAgents 处理 GET/POST /api/agents
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

// handleAgentByID 处理 GET/PUT/DELETE /api/agents/{id}
func handleAgentByID(w http.ResponseWriter, r *http.Request) {
	// 从 URL path 提取 agent ID：/api/agents/{id}
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

// handleListMemories 返回按 scope、tier、type、status、project 过滤并分页的
// memory 记录。
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

// memoryCreateRequest 是 POST /api/memories 的 JSON body。
type memoryCreateRequest struct {
	ProjectID  string   `json:"project_id"`
	Scope      string   `json:"scope"` // session | project | global
	SessionID  string   `json:"session_id"`
	Type       string   `json:"type"` // preference | rule | fact | lesson | reflection
	Tier       string   `json:"tier"` // consolidated | semantic
	Content    string   `json:"content"`
	Confidence float64  `json:"confidence"`
	Status     string   `json:"status"`
	Tags       []string `json:"tags,omitempty"` // 仅用于 metadata，可能被忽略
}

// memoryUpdateRequest 是 PUT /api/memories/{id} 的 JSON body。
type memoryUpdateRequest struct {
	Content    string  `json:"content"`
	Confidence float64 `json:"confidence"`
	Status     string  `json:"status"`
}

// handleMemoryByID 处理 GET / PUT / DELETE /api/memories/{id} 以及
// POST /api/memories/{id}/embed。
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
		// 修改前先确认 memory 存在。
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
		// Phase 7-C：memory 更新的 audit log。
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
		// 复用已有的删除 handler 逻辑避免分叉，然后再广播带被删 memory 详情的
		// 删除事件。
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
		// Phase 7-C：memory 删除的 audit log。
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

// handleMemoryEmbed 处理 POST /api/memories/{id}/embed。它对 memory 内容
// 做 embedding，并把向量存入配置的 VectorStore。
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
		// 优雅降级：memory 存在但其 embedding 无法计算。返回 422 让前端
		// 注意到而不触发重试风暴。
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
			// Embedding 已在 VectorStore 中；DB 持久化是尽力而为。
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

// resolveProviderNameForAPI 返回某 embedding provider 的人类可读模型名。
// provider 为 nil 时回退到 "unknown"。
func resolveProviderNameForAPI(provider llm.EmbeddingProvider) string {
	if provider == nil {
		return "unknown"
	}
	return fmt.Sprintf("%T", provider)
}

// encodeFloat32ToBytes 把 []float32 向量序列化为小端字节切片，
// 适合写入 memory_embeddings 表。
func encodeFloat32ToBytes(vec []float32) ([]byte, error) {
	buf := make([]byte, 4*len(vec))
	for i, v := range vec {
		bits := math.Float32bits(v)
		binary.LittleEndian.PutUint32(buf[i*4:], bits)
	}
	return buf, nil
}

// handleUpdateMemoryScope 更新 memory 的 scope（以及可选的 session_id）。
// PUT /api/memories/{id}/scope
// Body: {"scope": "project", "session_id": ""}
func handleUpdateMemoryScope(w http.ResponseWriter, r *http.Request, id string) {
	// 更新前先确认 memory 存在。
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
	// scope 不是 session 时清空 session_id，避免遗留过期值。
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

// handleDeleteMemory 按 ID 删除 memory 记录。
// DELETE /api/memories/{id}
func handleDeleteMemory(w http.ResponseWriter, r *http.Request, id string) {
	// 删除前先确认 memory 存在。
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
	// Phase 7-C：在最底层 helper 也记录 memory 删除的 audit log，
	// 这样直接调用 handleDeleteMemory 的地方同样会留下 audit 记录。
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

// handleCreateMemory 根据用户请求创建新 memory。
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
	// 尽力而为的 embedding：若 embedding / 向量持久化不可用，
	// 也不要让 create API 失败。
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

// handlePromoteMemories 手动触发 promotion pipeline。
// POST /api/memories/promote
// Body: {"project_id": "default"}
func handlePromoteMemories(w http.ResponseWriter, r *http.Request, gate *harness.PromotionGate) {
	var req struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// 接受空 body —— 使用默认 project
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

// handleMemoryStats 返回某项目的聚合 memory 统计。
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
	// 为方便调用方，额外计算一个 total 字段。
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

// handleRecallPreview 预览某个任务会召回哪些 memory。
// GET /api/memories/recall?task=xxx&project=default&max=3
// GET /api/memories/recall?query=xxx&project=default&max=3 —— 纯向量检索
// 这是一个调试 endpoint —— 它展示某任务会被注入的 WorkingMemory，
// 但不会真正运行 agent。
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

	// 向量查询模式：GET /api/memories/recall?query=xxx
	// 执行纯向量相似度检索，返回按相关性排序的 MemoryItem。
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

	// 检测召回 memory 之间的冲突
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

// parseInt 解析简单的整数字符串。用于 URL 查询参数解析，
// 这里不需要为单个值引入完整的 strconv 包。
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

// projectRequest 是 project create/update 的 JSON body。
// Rules 是可选的 project 级规则文本，归属于此 project 的所有 session 在发起任务时
// 会自动注入到 system prompt（类似项目级记忆），存入 project.config.rules。
type projectRequest struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	WorkingDirectory string `json:"working_directory"`
	Rules            string `json:"rules"`
}

// projectSummary 是 list endpoint 返回的紧凑视图。
// 包含从关联表计算出的计数。Config 透传 project.config JSON（含 rules），
// 供前端 ProjectConfig 回显已保存的规则文本。
type projectSummary struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	WorkingDirectory string         `json:"working_directory"`
	Config           map[string]any `json:"config"`
	SessionCount     int            `json:"session_count"`
	MemoryCount      int            `json:"memory_count"`
	CreatedAt        string         `json:"created_at"`
	UpdatedAt        string         `json:"updated_at"`
}

// buildProjectConfig 在已有 config 之上合并前端传入的 rules 文本。
//   - rules 非空：写入 config["rules"] = rules（覆盖旧值）
//   - rules 为空：删除 config["rules"]，使规则可被清空
//   - base 为 nil 时初始化为空 map，避免写入 nil 导致 JSON 序列化为 null
//
// 这样 project.config 既能承载 rules，也为未来其它扩展字段（如默认模型、超时）
// 保留空间，不会被 rules 字段覆盖时整体丢失。
func buildProjectConfig(base map[string]any, rules string) map[string]any {
	cfg := make(map[string]any, len(base)+1)
	for k, v := range base {
		cfg[k] = v
	}
	if strings.TrimSpace(rules) != "" {
		cfg["rules"] = rules
	} else {
		delete(cfg, "rules")
	}
	return cfg
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

		// 构建 summary，附带 session 与 memory 计数
		summaries := make([]projectSummary, 0, len(projects))
		for _, p := range projects {
			summary := projectSummary{
				ID:               p.ID,
				Name:             p.Name,
				Description:      p.Description,
				WorkingDirectory: p.WorkingDirectory,
				Config:           p.Config,
				CreatedAt:        p.CreatedAt.Format(time.RFC3339),
				UpdatedAt:        p.UpdatedAt.Format(time.RFC3339),
			}

			// 统计该项目的 session 数量
			sessions, err := db.QuerySessionsByProject(p.ID, 1000)
			if err == nil {
				summary.SessionCount = len(sessions)
			}

			// 统计该项目的 memory 数量
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
			Config:           buildProjectConfig(nil, req.Rules),
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

		// 计算 memory 统计：total、consolidated、semantic
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

		// 取出已有 project 以保留其 config，并在其上覆盖 rules 字段
		existing, err := db.QueryProjectByID(id)
		if err != nil {
			http.Error(w, "project not found: "+err.Error(), http.StatusNotFound)
			return
		}

		mergedConfig := buildProjectConfig(existing.Config, req.Rules)
		if err := db.UpdateProject(id, req.Name, req.Description, req.WorkingDirectory, mergedConfig); err != nil {
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
		// 保护 default project 不被删除
		if id == "default" {
			http.Error(w, "cannot delete the default project", http.StatusBadRequest)
			return
		}

		if err := db.DeleteProject(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Phase 7-C：project 删除的 audit log。
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

// handleSessionChat 处理 POST /api/sessions/{id}/chat
// 在已有 Session 中发起新一轮对话，自动注入历史消息上下文
func handleSessionChat(w http.ResponseWriter, r *http.Request, hub *ws.Hub, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, approvalHandler harness.ApprovalHandler, memRecall *harness.MemoryRecall, agentBus runtime.AgentBus, checkpointMgr *runtime.CheckpointManager, memDB harness.CompressorDB, costRepo cost.CostRepository, modelRegistry *llm.ModelRegistry, modelRouter *llm.Router, routerProviders map[string]llm.Provider, caseService *cases.Service, todoSvc *todo.Service) {
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
		// TaskContract 可选覆盖项 —— 大于 0 / 非空时覆盖默认 contract，
		// 让前端能驱动 PolicyChain。
		Scope         string   `json:"scope"`
		AllowedTools  []string `json:"allowed_tools"`
		TokenBudget   int      `json:"token_budget"`
		CostBudgetUSD float64  `json:"cost_budget_usd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 按服务端强制 contract 限制校验请求。
	if len(req.Input) > cfg.ContractLimits.MaxInputLength {
		http.Error(w, fmt.Sprintf("input length exceeds maximum of %d", cfg.ContractLimits.MaxInputLength), http.StatusBadRequest)
		return
	}
	if req.MaxSteps < 1 {
		// 未显式指定 max_steps 也没有 case 上下文 —— 回退到服务端默认值，
		// 让普通 chat 请求无需客户端总是提供正值也能用。
		req.MaxSteps = harness.DefaultContract(req.Input).MaxSteps
	}
	if req.MaxSteps > cfg.ContractLimits.MaxSteps {
		req.MaxSteps = cfg.ContractLimits.MaxSteps
	}
	if req.TimeoutSeconds < 0 {
		http.Error(w, "timeout_seconds must be >= 0", http.StatusBadRequest)
		return
	}
	if req.TimeoutSeconds > cfg.ContractLimits.MaxTimeoutSeconds {
		http.Error(w, fmt.Sprintf("timeout_seconds exceeds maximum of %d", cfg.ContractLimits.MaxTimeoutSeconds), http.StatusBadRequest)
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
	// Phase 6-F：传入与 Heartbeat 相同的 LLM summarizer，保证 summary 质量。
	// nil summarizer 会让 Compressor 回退到关键词路径。
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
	// 请求体提供时覆盖 TaskContract 字段 ——
	// 让前端能驱动 PolicyChain（scope、tools、预算）与 timeout。
	if req.TimeoutSeconds > 0 {
		contract.TimeoutSeconds = req.TimeoutSeconds
	}
	if req.Scope != "" {
		if !isAllowedScope(req.Scope, cfg.ContractLimits.Scopes) {
			http.Error(w, fmt.Sprintf("scope %q is not allowed", req.Scope), http.StatusBadRequest)
			return
		}
		contract.Scope = req.Scope
	}
	if len(req.AllowedTools) > 0 {
		contract.AllowedTools = req.AllowedTools
	} else if tools := agentAllowedTools(agentID); len(tools) > 0 {
		contract.AllowedTools = tools
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

		runAgentLoopWithTurn(hub, taskID, agentID, fullSystemPrompt, req.Input, cfg, tools, persist, contract, id, approvalHandler, workingMemory, agentBus, checkpointMgr, turnIndex, sess.RootTaskID, "", costRepo, modelRegistry, modelRouter, routerProviders, caseService, todoSvc)
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

// handleSessionMessages 处理 GET /api/sessions/{id}/messages
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

// handleCostQuery 处理 GET /api/costs，支持按维度过滤。
// 支持的查询参数：task_id、session_id、project_id、days。
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

// costReportFromRecords 从一组 record 构建适合 JSON 输出的 cost 汇总。
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
		"total_cost_usd":   totalCostUSD, // 主字段，完整 float64 精度（不做 /100 截断）
		"total_cost_cents": totalCents,   // 派生字段，向后兼容的整数和
		"total_tokens":     totalTokens,
		"input_tokens":     totalInput,
		"output_tokens":    totalOutput,
		"by_model":         byModel, // float64 USD
		"by_agent":         byAgent, // float64 USD
		"records":          records,
	}
}

// truncateContent 把消息内容截断到 maxLen 字符。
// 内容超过 maxLen 时追加 "..."。
func truncateContent(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "..."
}

// handleRunCase 是 CaseCard 前端使用的薄代理。
// POST /api/run-case
// Body: {"input": "...", "agent_id": "...", "max_steps": N, "case": "code-gen", "session_id": "..."}
// 它提取 case 标识（从 "case" 或 "case_id" 字段），然后执行与
// POST /api/tasks?case=<caseID> 相同的 chat action 逻辑，body 未覆盖时
// 使用 case 的默认 input 与 system prompt。
func handleRunCase(w http.ResponseWriter, r *http.Request, hub *ws.Hub, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, approvalHandler harness.ApprovalHandler, memRecall *harness.MemoryRecall, agentBus runtime.AgentBus, checkpointMgr *runtime.CheckpointManager, memDB harness.CompressorDB, costRepo cost.CostRepository, modelRegistry *llm.ModelRegistry, modelRouter *llm.Router, routerProviders map[string]llm.Provider, caseService *cases.Service, todoSvc *todo.Service) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	// 解析请求体 —— 同时接受 "case" 与 "case_id" 作为 case 标识。
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

	// 解析 caseID：优先用 "case"，回退到 "case_id"。
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

	// 复刻 POST /api/tasks 中的 chat-action 逻辑：先应用 case 默认值，
	// 再运行 agent loop。
	// 通过 caseService 解析 case，让 SQLite 中持久化的自定义 case 也能
	// 生效。cases.Get 只认识内置 case；回退到它会丢失自定义 case 的
	// default_input/system_prompt/max_steps，并在 chat 路径以
	// 400 "input is required" 返回。
	resolveCase := func(caseID string) *cases.Case {
		if caseID == "" {
			return nil
		}
		if caseService != nil {
			c, err := caseService.Get(caseID)
			if err != nil || c == nil {
				return nil
			}
			return c
		}
		return cases.Get(caseID)
	}

	var contract harness.TaskContract
	if caseID != "" {
		if c := resolveCase(caseID); c != nil {
			contract = c.Contract
			if req.Input == "" {
				req.Input = c.DefaultInput
			}
			if req.SystemPrompt == "" {
				req.SystemPrompt = c.SystemPrompt
			}
			// 客户端未覆盖时继承 case 级别的 step/timeout 默认值，
			// 否则未显式指定 max_steps 的请求会校验失败。
			if req.MaxSteps <= 0 {
				req.MaxSteps = c.Contract.MaxSteps
			}
			if req.TimeoutSeconds <= 0 {
				req.TimeoutSeconds = c.Contract.TimeoutSeconds
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
	go runAgentLoop(hub, taskID, agentID, systemPrompt, req.Input, cfg, tools, persist, contract, req.SessionID, approvalHandler, workingMemory, agentBus, checkpointMgr, caseID, costRepo, modelRegistry, modelRouter, routerProviders, caseService, todoSvc)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"task_id":    taskID,
		"agent_id":   agentID,
		"case_id":    caseID,
		"session_id": req.SessionID,
		"status":     "started",
	})
}

// handleContractLimits 返回服务端强制 task contract 限制。
// GET /api/contract-limits
func handleContractLimits(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg.ContractLimits)
	}
}

// handleAudit 返回默认 auditor 最近的 audit 记录。
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

// handleTraces 返回进程级 tracer 中所有缓存的 trace span。
// GET /api/traces
func handleTraces(w http.ResponseWriter, r *http.Request) {
	data, _ := tracer.JSON()
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// handleReplayEvents 返回给定 event_id 之后的缓存 WebSocket 事件。
// GET /api/replay/events?since_event_id=<id>&limit=<n>
// 前端重连后用它补齐断连期间错过的事件。当 since_event_id 已不在内存
// 缓冲区中时返回 410 Gone，让客户端回退到完整任务回放。
func handleReplayEvents(w http.ResponseWriter, r *http.Request, hub *ws.Hub) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if hub == nil {
		http.Error(w, "event replay unavailable", http.StatusServiceUnavailable)
		return
	}

	sinceEventID := r.URL.Query().Get("since_event_id")
	if sinceEventID == "" {
		http.Error(w, "since_event_id required", http.StatusBadRequest)
		return
	}

	limit := 100
	if limStr := r.URL.Query().Get("limit"); limStr != "" {
		if n, err := strconv.Atoi(limStr); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	evts, err := hub.ReplayEvents(sinceEventID, limit)
	if err != nil {
		if errors.Is(err, ws.ErrEventIDNotFound) {
			w.WriteHeader(http.StatusGone)
			json.NewEncoder(w).Encode(map[string]any{
				"error":   "event_id not found in replay buffer",
				"since":   sinceEventID,
				"message": "disconnected too long or server restarted; please reload task",
			})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"events": evts,
		"since":  sinceEventID,
		"count":  len(evts),
	})
}

// handleReplay 从持久化存储回放任务执行事件。
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

// buildReplayEvents 从 steps 与 conversations 重建一条扁平的事件序列。
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
			"timestamp":   s.ID, // steps 表没有 created_at；用 id 作为稳定排序代理
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
