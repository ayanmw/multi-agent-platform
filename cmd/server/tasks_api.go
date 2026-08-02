// tasks_api.go — /api/tasks 根路由与 chat task 启动逻辑。
//
// Phase 8-B: 把原来 main.go 中的 handleTasksRoot 与 startChatTask 闭包迁移到本文件，
// 并改为 appServer 方法，彻底消除包级闭包变量；任务 action 由 appServer.taskActions
// 注册表分发，保持 handler 方法化。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/ayanmw/multi-agent-platform/internal/cases"
	"github.com/ayanmw/multi-agent-platform/internal/harness"
	"github.com/ayanmw/multi-agent-platform/internal/orchestrator"
	"github.com/ayanmw/multi-agent-platform/pkg/db"
	"github.com/ayanmw/multi-agent-platform/pkg/event"
)

// taskRequest 是 POST /api/tasks 的请求体。
type taskRequest struct {
	Action            string                   `json:"action"`
	AgentID           string                   `json:"agent_id"`
	Input             string                   `json:"input"`
	SystemPrompt      string                   `json:"system_prompt"`
	CaseType          string                   `json:"case_type"`
	MaxSteps          int                      `json:"max_steps"`
	TimeoutSeconds    int                      `json:"timeout_seconds"`
	SessionID         string                   `json:"session_id"`
	Agents            []orchestrator.AgentSpec `json:"agents"`
	Scope             string                   `json:"scope"`
	AllowedTools      []string                 `json:"allowed_tools"`
	TokenBudget       int                      `json:"token_budget"`
	CostBudgetUSD     float64                  `json:"cost_budget_usd"`
	// C1: 前置 invoke 返回的临时 command skill ID 列表；只对当前 run 注入。
	TemporarySkillIDs []string                   `json:"temporary_skill_ids,omitempty"`
}

// taskAction 是 /api/tasks POST action 的处理函数签名。
// 方法值注册到 appServer.taskActions 后，receiver 已被绑定，签名中不再含 *appServer。
type taskAction func(w http.ResponseWriter, r *http.Request, req taskRequest, caseID string)

// initTaskActions 初始化 appServer 的 taskActions 注册表，供 handleTasksRoot 按需分发。
// 在 appServer 构造后调用一次（如 registerRoutes 中），保持 handler 方法化的同时获得
// 注册表的可扩展性。
func (s *appServer) initTaskActions() {
	if s.taskActions != nil {
		return
	}
	s.taskActions = map[string]taskAction{
		"chat":        s.actionChat,
		"multi-agent": s.actionMultiAgent,
		"stream-demo": s.actionStreamDemo,
	}
}

// handleTasksRoot 是 /api/tasks 的 POST 入口（chat / multi-agent / stream-demo
// action）。Phase 8-B 改为 appServer 方法，所有依赖从 s 获取，并通过
// s.taskActions 注册表分发具体 action。
func (s *appServer) handleTasksRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	s.initTaskActions()

	lookupCase := func(caseID string) *cases.Case {
		if caseID == "" {
			return nil
		}
		if s.caseService != nil {
			c, err := s.caseService.Get(caseID)
			if err != nil || c == nil {
				return nil
			}
			return c
		}
		return cases.Get(caseID)
	}

	var req taskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	caseID := r.URL.Query().Get("case")
	if caseID != "" {
		if c := lookupCase(caseID); c != nil {
			if req.Input == "" {
				req.Input = c.DefaultInput
			}
			if req.SystemPrompt == "" {
				req.SystemPrompt = c.SystemPrompt
			}
			if req.MaxSteps <= 0 {
				req.MaxSteps = c.Contract.MaxSteps
			}
			if req.TimeoutSeconds <= 0 {
				req.TimeoutSeconds = c.Contract.TimeoutSeconds
			}
		}
	}

	if len(req.Input) > s.cfg.ContractLimits.MaxInputLength {
		http.Error(w, fmt.Sprintf("input length exceeds maximum of %d", s.cfg.ContractLimits.MaxInputLength), http.StatusBadRequest)
		return
	}

	if req.MaxSteps < 1 {
		req.MaxSteps = harness.DefaultContract(req.Input).MaxSteps
	}
	if req.MaxSteps > s.cfg.ContractLimits.MaxSteps {
		req.MaxSteps = s.cfg.ContractLimits.MaxSteps
	}
	if req.TimeoutSeconds < 0 {
		http.Error(w, "timeout_seconds must be >= 0", http.StatusBadRequest)
		return
	}
	if req.TimeoutSeconds > s.cfg.ContractLimits.MaxTimeoutSeconds {
		http.Error(w, fmt.Sprintf("timeout_seconds exceeds maximum of %d", s.cfg.ContractLimits.MaxTimeoutSeconds), http.StatusBadRequest)
		return
	}

	action, ok := s.taskActions[req.Action]
	if !ok {
		http.Error(w, "unknown action (use 'chat', 'multi-agent' or 'stream-demo')", http.StatusBadRequest)
		return
	}
	action(w, r, req, caseID)
}

// actionMultiAgent 处理 action=multi-agent：启动一个 leader-driven multi-agent 任务。
func (s *appServer) actionMultiAgent(w http.ResponseWriter, r *http.Request, req taskRequest, _ string) {
	if len(req.Agents) > s.cfg.ContractLimits.MaxSubAgents {
		http.Error(w, fmt.Sprintf("agents count exceeds maximum of %d", s.cfg.ContractLimits.MaxSubAgents), http.StatusBadRequest)
		return
	}

	sessionID, taskID, err := resolveSession(req.SessionID, req.Input, s.persist)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.persist != nil {
		s.persist.SaveTaskMeta(taskID, sessionID, "", true)
		if sessionID != "" {
			sess, err := db.QuerySessionByID(sessionID)
			if err == nil && sess.RootTaskID == "" {
				db.UpdateSession(sessionID, taskID, sess.Status, sess.UserInput)
			}
		}
	}

	s.hub.SendEvent(event.NewEvent("task_started", taskID, "leader", 0, map[string]any{
		"task_id":    taskID,
		"session_id": sessionID,
		"input":      req.Input,
		"mode":       "leader-driven",
	}))

	leaderInput := req.Input
	if len(req.Agents) > 0 {
		strategy := "parallel"
		for i := range req.Agents {
			if req.Agents[i].Name == "" {
				req.Agents[i].Name = req.Agents[i].AgentID
			}
		}
		workflowJSON, _ := json.Marshal(req.Agents)
		leaderInput += fmt.Sprintf("\n\n[MANDATORY WORKFLOW] You must use the dispatch_sub_agent tool with strategy=%q and agents=%s to complete this task.", strategy, string(workflowJSON))
	}

	leaderSystemPrompt := "You are the Leader agent. You coordinate sub-agents to solve complex tasks. Use the dispatch_sub_agent tool when you need to delegate work to multiple sub-agents. Each sub-agent runs independently; their results are returned as observations. If the task is simple enough, you may answer directly."
	if s.cfg.LLMUseMock {
		leaderSystemPrompt = "You are the Leader agent. Use dispatch_sub_agent when delegation is needed."
	}

	cfg := s.cfg
	hub := s.hub
	go func() {
		runner := NewAgentRunner(hub, AgentDeps{
			Cfg:             cfg,
			Tools:           s.toolRegistry,
			Persist:         s.persist,
			ApprovalHandler: s.approvalHandler,
			AgentBus:        s.agentBusAdapter,
			CheckpointMgr:   s.checkpointMgr,
			CostRepo:        s.costRepo,
			ModelRegistry:   s.modelRegistry,
			ModelRouter:     s.modelRouter,
			RouterProviders: s.routerProviders,
			CaseService:     s.caseService,
			TodoSvc:         s.todoSvc,
			SkillRegistry:   globalSkillRegistry,
			Orchestrator:    globalOrchestrator,
			Tracer:          tracer,
		})
		runner.Run(context.Background(), AgentRunSpec{
			TaskID:       taskID,
			AgentID:      "leader",
			SystemPrompt: leaderSystemPrompt,
			UserInput:    leaderInput,
			SessionID:    sessionID,
			IsRoot:       true,
			Contract:     harness.DefaultContract(leaderInput),
		})
		removeCancel(taskID, "leader")
		removeEngine(taskID, "leader")
		db.UpdateSessionStatus(sessionID, deriveSessionStatus(sessionID))
		log.Infof("server", "[Multi-Agent] Leader task %s completed", taskID)
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"session_id":  sessionID,
		"task_id":     taskID,
		"agent_count": 1,
		"agent_ids":   []string{"leader"},
		"status":      "started",
	})
}

// actionStreamDemo 处理 action=stream-demo：发射一组演示事件。
func (s *appServer) actionStreamDemo(w http.ResponseWriter, r *http.Request, req taskRequest, _ string) {
	sessionID, taskID, err := resolveSession(req.SessionID, "", s.persist)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go streamTask(s.hub, taskID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"session_id": sessionID,
		"task_id":    taskID,
	})
}

// actionChat 处理 action=chat：调用 startChatTask 并返回任务信息。
func (s *appServer) actionChat(w http.ResponseWriter, r *http.Request, req taskRequest, caseID string) {
	if caseID != "" {
		lookupCase := func(caseID string) *cases.Case {
			if caseID == "" {
				return nil
			}
			if s.caseService != nil {
				c, err := s.caseService.Get(caseID)
				if err != nil || c == nil {
					return nil
				}
				return c
			}
			return cases.Get(caseID)
		}
		if c := lookupCase(caseID); c != nil {
			if req.Input == "" {
				req.Input = c.DefaultInput
			}
			if req.SystemPrompt == "" {
				req.SystemPrompt = c.SystemPrompt
			}
			if req.MaxSteps <= 0 {
				req.MaxSteps = c.Contract.MaxSteps
			}
			if req.TimeoutSeconds <= 0 {
				req.TimeoutSeconds = c.Contract.TimeoutSeconds
			}
		}
	}

	if req.Input == "" {
		http.Error(w, "input is required for chat action", http.StatusBadRequest)
		return
	}

	sessionID, taskID, err := s.startChatTask(startChatTaskOpts{
		AgentID:            req.AgentID,
		Input:              req.Input,
		SystemPrompt:       req.SystemPrompt,
		SessionID:          req.SessionID,
		MaxSteps:           req.MaxSteps,
		TimeoutSeconds:     req.TimeoutSeconds,
		Scope:              req.Scope,
		AllowedTools:       req.AllowedTools,
		TokenBudget:        req.TokenBudget,
		CostBudgetUSD:      req.CostBudgetUSD,
		CaseID:             caseID,
		TemporarySkillIDs: req.TemporarySkillIDs,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"session_id": sessionID,
		"task_id":    taskID,
		"agent_id": func() string {
			if req.AgentID != "" {
				return req.AgentID
			}
			return "agent_default"
		}(),
		"action": "chat",
	})
}

// startChatTask 执行 chat action 的核心：校验、构建 contract、启动 AgentRunner。
// Phase 8-B 改为 appServer 方法，消除 main() 闭包。
func (s *appServer) startChatTask(opts startChatTaskOpts) (sessionID, taskID string, err error) {
	if opts.Input == "" {
		return "", "", errors.New("input is required for chat action")
	}

	agentID := opts.AgentID
	if agentID == "" {
		agentID = "agent_default"
	}

	systemPrompt := opts.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a helpful AI assistant with access to tools. " +
			"When you need to run commands, read files, or write files, use the available tools. " +
			"Always explain your reasoning before using tools. " +
			"After using tools, analyze the results and continue until the task is complete."
	}

	lookupCase := func(caseID string) *cases.Case {
		if caseID == "" {
			return nil
		}
		if s.caseService != nil {
			c, err := s.caseService.Get(caseID)
			if err != nil || c == nil {
				return nil
			}
			return c
		}
		return cases.Get(caseID)
	}

	var contract harness.TaskContract
	if opts.CaseID != "" {
		if c := lookupCase(opts.CaseID); c != nil {
			contract = c.Contract
			if opts.SystemPrompt == "" {
				systemPrompt = c.SystemPrompt
			}
			if opts.MaxSteps <= 0 {
				opts.MaxSteps = c.Contract.MaxSteps
			}
			if opts.TimeoutSeconds <= 0 {
				opts.TimeoutSeconds = c.Contract.TimeoutSeconds
			}
		}
	}

	if contract.Goal == "" {
		contract = harness.DefaultContract(opts.Input)
	}
	if opts.MaxSteps > 0 {
		contract.MaxSteps = opts.MaxSteps
	}
	if opts.TimeoutSeconds > 0 {
		contract.TimeoutSeconds = opts.TimeoutSeconds
	}
	if opts.Scope != "" {
		if !isAllowedScope(opts.Scope, s.cfg.ContractLimits.Scopes) {
			return "", "", fmt.Errorf("scope %q is not allowed", opts.Scope)
		}
		contract.Scope = opts.Scope
	}
	if len(opts.AllowedTools) > 0 {
		contract.AllowedTools = opts.AllowedTools
	} else if tools := agentAllowedTools(agentID); len(tools) > 0 {
		contract.AllowedTools = tools
	}
	if opts.TokenBudget > 0 {
		contract.TokenBudget = opts.TokenBudget
	}
	if opts.CostBudgetUSD > 0 {
		contract.CostBudgetUSD = opts.CostBudgetUSD
	}

	// 合并 Agent 级默认权限（OR 语义）。
	if agentID != "" {
		if ag, err := db.QueryAgentByID(agentID); err == nil && ag != nil {
			applyAgentPermissions(&contract, ag.Config)
		}
	}

	sid, tid, err := resolveSession(opts.SessionID, opts.Input, s.persist)
	if err != nil {
		return "", "", fmt.Errorf("resolve session: %w", err)
	}

	workingMemory := ""
	if s.memRecall != nil {
		if wm, err := s.memRecall.BuildWorkingMemory("default", sid, opts.Input, 3); err == nil {
			workingMemory = s.memRecall.FormatForSystemPrompt(wm)
		}
	}
	workingMemory += projectRulesPrompt(sid)

	rootTraceCtx := tracer.StartRoot(tid, "task")
	traceRegistry.Store(tid, rootTraceCtx)

	runner := s.newRunner()
	spec := AgentRunSpec{
		TaskID:             tid,
		AgentID:            agentID,
		SystemPrompt:       systemPrompt,
		UserInput:          opts.Input,
		SessionID:          sid,
		IsRoot:             true,
		Contract:           contract,
		CaseID:             opts.CaseID,
		WorkingMemory:      workingMemory,
		RootTraceCtx:       rootTraceCtx,
		TemporarySkillIDs: opts.TemporarySkillIDs,
	}
	go runner.Run(context.Background(), spec)

	return sid, tid, nil
}
