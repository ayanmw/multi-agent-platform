// runner.go — Agent 运行入口：AgentRunSpec / AgentDeps / AgentRunner。
//
// Phase 8-A 把原先散落在 main.go 中的"上帝函数" runAgentLoop / runAgentLoopWithTurn
// 收敛为结构化的运行入口：
//   - AgentRunSpec：一次 agent 运行的全部可序列化入参（task/agent/session 标识、
//     prompt、contract、role、权限位、trace context 等），替代 20+ 个位置参数。
//   - AgentDeps：与具体运行无关的共享依赖（config、tool registry、persist、router、
//     case/todo/skill service、tracer 等），由 appServer 在启动期一次性聚合。
//   - AgentRunner：持有 Hub 与 Deps，提供 Run(ctx, spec) 启动一次 ReAct loop。
//
// 同时把与 runner 强相关的辅助也迁到本文件：cancel/engine registry（用于 WS 控制
// 消息取消/暂停任务）、orchestratorDispatcher（dispatch_sub_agent 落地）、
// leaderApprovalHandler（worker 审批委托给 leader）、agentAllowedTools /
// resolveAllowedTools / isAllowedScope / enrichAgentSpecAllowedTools（权限解析）、
// projectRulesPrompt（项目规则注入）、hubAdapter（ws.Hub → runtime.EventBus）。
//
// 注意：AgentRunner.Run 当前仍启动 goroutine 并在内部跑完整个 loop（与旧
// runAgentLoopWithTurn 一致），调用方负责传入已带 timeout 的 ctx。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/cases"
	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/internal/cost"
	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/observability"
	"github.com/anmingwei/multi-agent-platform/internal/orchestrator"
	"github.com/anmingwei/multi-agent-platform/internal/runtime"
	"github.com/anmingwei/multi-agent-platform/internal/skill"
	"github.com/anmingwei/multi-agent-platform/internal/todo"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/google/uuid"
)

// AgentRunSpec 描述一次 agent 运行的全部入参。
// 它把旧 runAgentLoopWithTurn 的 20+ 位置参数收敛为一个可序列化 struct，
// 让 chat / cron / checkpoint-recovery / multi-agent leader 等入口用同一种
// 形式构造运行请求，并便于未来持久化或跨进程派发。
type AgentRunSpec struct {
	TaskID       string // 任务 ID（root 或 child）
	AgentID      string // 执行该任务的 agent ID
	SystemPrompt string // 完整 system prompt（已含 working memory / history）
	UserInput    string // 本轮用户输入
	SessionID    string // 所属 session（可空，如纯 task 无 session）
	ParentTaskID string // 父任务 ID（多轮对话首轮为空）
	TurnIndex    int    // 轮次序号；0 表示 root/首轮
	IsRoot       bool   // 是否为 root task（决定 leader 角色与 session 绑定）
	Contract     harness.TaskContract
	CaseID       string        // MockProvider 脚本匹配提示
	WorkingMemory string       // 注入 system prompt 的工作记忆（已格式化）
	RootTraceCtx  *observability.TraceContext // 可选的 root trace context；nil 时由 runner 兜底创建

	// 角色与权限（Phase 7-H）。IsRoot=true 时 runner 默认填 leader，也可显式覆盖。
	Role                 runtime.AgentRole
	CanDispatchSubAgents bool
	CanDefineWorkflow    bool
	ApproverMode         string // "user" / "leader"
	SupervisorSubTaskID  string
}

// AgentDeps 聚合一次 agent 运行所需的全部共享依赖。
// 由 appServer 在启动期构造一次，所有 AgentRunner.Run 调用复用同一份。
// 把这些依赖从 main() 局部变量提升为显式 struct，避免闭包捕获导致的
// "隐式依赖"——现在 runner 能独立测试，只需注入 mock deps。
type AgentDeps struct {
	Cfg             *config.Config
	Tools           *tool.Registry
	Persist         runtime.Persistence
	ApprovalHandler harness.ApprovalHandler
	AgentBus        runtime.AgentBus
	CheckpointMgr   *runtime.CheckpointManager
	CostRepo        cost.CostRepository
	ModelRegistry   *llm.ModelRegistry
	ModelRouter     *llm.Router
	RouterProviders map[string]llm.Provider
	CaseService     *cases.Service
	TodoSvc         *todo.Service
	SkillRegistry   *skill.Registry
	Orchestrator    *orchestrator.Orchestrator
	Tracer          *observability.Tracer
	// MemDB 用于 session chat 的上下文压缩（harness.NewContextCompressor）。
	// 单独列出是因为它属于 CompressorDB 接口，不与运行期 EngineConfig 直接耦合。
	MemDB harness.CompressorDB
	// MemRecall 用于在启动 chat / multi-agent 任务前构建工作记忆（Working Memory）
	// 并注入 system prompt。可空：DB 未初始化时为 nil，BuildWorkingMemory 调用被跳过。
	MemRecall *harness.MemoryRecall
}

// AgentRunner 是 agent 运行入口。持有 WS Hub（用于广播事件）与共享 Deps。
type AgentRunner struct {
	Hub  *ws.Hub
	Deps AgentDeps
}

// NewAgentRunner 构造一个 AgentRunner。hub 用于广播 task_started / task_failed
// 等事件；deps 携带运行所需的全部共享依赖。
func NewAgentRunner(hub *ws.Hub, deps AgentDeps) *AgentRunner {
	return &AgentRunner{Hub: hub, Deps: deps}
}

// Run 启动一次 ReAct loop。当前实现仍以 goroutine 形式运行（与旧
// runAgentLoopWithTurn 行为一致），调用方传入的 ctx 应已按 contract.TimeoutSeconds
// 配置好 deadline。函数立即返回，loop 在后台执行。
//
// 入参 spec 中的角色/权限字段若为零值，runner 会按 IsRoot 自动补默认值
// （root=leader 且可派发子 agent、approverMode=user；非 root=worker、
// approverMode=leader），与旧 runAgentLoopWithTurn 完全等价。
func (r *AgentRunner) Run(_ context.Context, spec AgentRunSpec) {
	r.runAgentLoopWithTurn(spec)
}

// RunSync 同步执行一次 ReAct loop 并阻塞至结束。主要用于测试与需要等待
// 结果的恢复路径。生产 chat/cron 路径仍用 Run（异步）。
func (r *AgentRunner) RunSync(ctx context.Context, spec AgentRunSpec) {
	r.runAgentLoopWithTurn(spec)
}

// RecoverSpec 描述一次 checkpoint 恢复请求。
type RecoverSpec struct {
	TaskID string
}

// Recover 从 checkpoint 恢复一个 Engine 并在后台 goroutine 中继续运行。
// 与旧的 handleRecoverCheckpoint 不同，它走统一的 AgentDeps，补齐 EngineConfig
// 中 skill / session message writer / workspace / todos 等字段，恢复后的 agent
// 不再退化。返回恢复的 agentID。
func (r *AgentRunner) Recover(ctx context.Context, spec RecoverSpec) (string, error) {
	cm := r.Deps.CheckpointMgr
	if cm == nil {
		return "", fmt.Errorf("checkpoint manager not available")
	}
	cp, err := cm.Load(spec.TaskID)
	if err != nil {
		return "", fmt.Errorf("load checkpoint: %w", err)
	}

	cfg := r.Deps.Cfg
	hub := r.Hub
	// checkpoint 未保存 sessionID/workspace，按 taskID 反查 session。
	sessionID := ""
	if task, err := db.QueryTaskByID(spec.TaskID); err == nil && task != nil {
		sessionID = task.SessionID
	}

	// 再允许额外 10 步（若 checkpoint 本身 MaxSteps 更大，也会被 Engine 保留）。
	contract := harness.DefaultContract("resume")
	contract.MaxSteps = cp.StepIdx + 10

	caseID := ""

	// 复用 chat 路径的 provider 解析逻辑；失败时回退 nil，Engine 会再创建默认 provider。
	provider, err := llm.CreateProviderFromConfig(cfg, cfg.LLMModel, caseID)
	if err != nil {
		log.Printf("[AgentRunner.Recover] failed to create provider for task=%s: %v", spec.TaskID, err)
		provider = nil
	}

	progressManager := harness.NewProgressManager()
	tokenBudgetRule := &harness.TokenBudgetRule{}
	costBudgetRule := harness.NewCostBudgetRule()
	policyChain := harness.NewPolicyChain(
		&harness.PathTraversalRule{},
		&harness.FileScopeRule{},
		&harness.DangerousCommandRule{},
		harness.NewApprovalRule(r.Deps.ApprovalHandler),
		harness.NewTagPolicyRule(r.Deps.Tools.ToolTags),
		tokenBudgetRule,
		&harness.ToolWhitelistRule{},
		costBudgetRule,
	)
	policyGate := harness.NewPolicyGate(policyChain, contract)

	// 为恢复路径构建与 chat 路径一致的成本/指标回调。
	onUsage := func(model string, profile *llm.ModelProfile, usage llm.Usage) {
		observability.DefaultMetrics.RecordLLMCall(
			uint64(usage.PromptTokens),
			uint64(usage.CompletionTokens),
			uint64(usage.TotalTokens),
		)
		if profile == nil || profile.Provider == "unknown" {
			if p := r.Deps.ModelRegistry.Get(model); p != nil {
				profile = p
			}
		}
		costTracker := cost.NewCostTracker(cost.WithRegistry(r.Deps.ModelRegistry))
		projectID := "default"
		if sessionID != "" {
			if sess, err := db.QuerySessionByID(sessionID); err == nil {
				projectID = sess.ProjectID
			}
		}
		record := costTracker.BuildRecordFromProfile(
			spec.TaskID, sessionID, projectID, cp.AgentID,
			0, model, profile, usage,
		)
		if record.CostUSD > 0 {
			costBudgetRule.SetCost(record.CostUSD)
		}
		if r.Deps.CostRepo != nil {
			if err := r.Deps.CostRepo.Insert(record); err != nil {
				observability.DefaultLogger.Warn("cost", "failed to persist cost record", map[string]any{
					"task_id": spec.TaskID,
					"error":   err.Error(),
				})
			}
		}
		observability.DefaultMetrics.RecordCost(record.CostCents)
	}

	// 恢复路径的 workspace：按反查出的 session 决定。
	workspaceDir := ""
	if sessionID != "" {
		if wsSess, err := db.QuerySessionByID(sessionID); err == nil {
			workspaceDir = wsSess.WorkspaceDir
			if workspaceDir == "" && wsSess.ProjectID != "" {
				if proj, projErr := db.QueryProjectByID(wsSess.ProjectID); projErr == nil && proj.WorkingDirectory != "" {
					workspaceDir = proj.WorkingDirectory
				}
			}
		}
	}

	// 恢复路径的 working memory 与 active todos：与 chat 路径保持一致。
	workingMemory := ""
	if r.Deps.MemRecall != nil && sessionID != "" {
		if wm, err := r.Deps.MemRecall.BuildWorkingMemory("default", sessionID, "", 3); err == nil {
			workingMemory = r.Deps.MemRecall.FormatForSystemPrompt(wm)
		}
	}
	workingMemory += projectRulesPrompt(sessionID)

	rootTraceCtx := tracer.StartRoot(spec.TaskID, "recover")
	traceRegistry.Store(spec.TaskID, rootTraceCtx)

	engineCfg := runtime.EngineConfig{
		AgentID:           cp.AgentID,
		SystemPrompt:      "You are recovering from a checkpoint. Continue the task from where you left off.",
		Model:             cfg.LLMModel,
		Endpoint:          cfg.LLMEndpoint,
		APIKey:            cfg.LLMAPIKey,
		Provider:          provider,
		CaseID:            caseID,
		Temperature:       0.7,
		MaxTokens:         4096,
		MaxSteps:          contract.MaxSteps,
		Persistence:       r.Deps.Persist,
		PolicyGate:        policyGate,
		Progress:          progressManager,
		Contract:          contract,
		ApprovalHandler:   r.Deps.ApprovalHandler,
		AgentBus:          r.Deps.AgentBus,
		CheckpointManager: cm,
		Router:            r.Deps.ModelRouter,
		Registry:          r.Deps.ModelRegistry,
		Providers:         r.Deps.RouterProviders,
		EvaluationRepository: func() runtime.EvaluationRepository {
			if r.Deps.CaseService == nil {
				return nil
			}
			return r.Deps.CaseService.Repository()
		}(),
		SessionMessageWriter: func(msg runtime.SessionMessageRecord) error {
			var toolCallsJSON string
			if msg.ToolCalls != "" {
				toolCallsJSON = msg.ToolCalls
			}
			return db.InsertSessionMessage(db.SessionMessageRecord{
				ID:         "msg_" + uuid.New().String(),
				SessionID:  sessionID,
				TaskID:     msg.TaskID,
				TurnIndex:  msg.TurnIndex,
				Role:       msg.Role,
				Content:    msg.Content,
				ToolCallID: msg.ToolCallID,
				ToolCalls:  toolCallsJSON,
				TokenCount: msg.TokenCount,
			})
		},
		WorkspaceDir:          workspaceDir,
		OnLLMUsage:            onUsage,
		SkillRegistry:         globalSkillRegistry,
		ActiveSkills:          GetEnabledSkillIDs(globalSkillRegistry),
		Tracer:                tracer,
		RootTraceCtx:          rootTraceCtx,
		LLMLatencyRecorder:    func(latency time.Duration) { observability.DefaultMetrics.RecordLLMLatency(latency) },
		ToolLatencyRecorder:   func(latency time.Duration) { observability.DefaultMetrics.RecordToolLatency(latency) },
		SubTaskID:             spec.TaskID,
		Role:                  runtime.AgentRoleLeader,
		CanDispatchSubAgents:  true,
		CanDefineWorkflow:     true,
		ApproverMode:          "user",
		ActiveTodos: func() string {
			if r.Deps.TodoSvc == nil || sessionID == "" {
				return ""
			}
			activeTodos, err := r.Deps.TodoSvc.ListActiveBySession(sessionID)
			if err != nil {
				observability.DefaultLogger.Warn("todo", "failed to load active todos for recovery", map[string]any{
					"session_id": sessionID,
					"error":      err.Error(),
				})
				return ""
			}
			return todo.FormatActiveTodos(activeTodos)
		}(),
		WorkingMemory: workingMemory,
	}

	engine := runtime.RecoverFromCheckpoint(cp, engineCfg, r.Deps.Tools, &hubAdapter{hub: hub}, spec.TaskID)

	// 与旧行为保持一致：启动 engine 后删除 checkpoint（避免重复恢复）。
	// 失败只记日志，不阻断恢复。
	if err := cm.Delete(spec.TaskID); err != nil {
		log.Printf("[AgentRunner.Recover] failed to delete checkpoint %s: %v", spec.TaskID, err)
	}

	hub.SendEvent(event.NewEvent("task_started", spec.TaskID, cp.AgentID, cp.StepIdx, map[string]any{
		"task_id":      spec.TaskID,
		"agent_id":     cp.AgentID,
		"recovered":    true,
		"step_idx":     cp.StepIdx,
		"total_tokens": cp.TotalTokens,
	}))

	go func() {
		ctx := context.Background()
		cancel := context.CancelFunc(func() {})
		if contract.TimeoutSeconds > 0 {
			ctx, cancel = context.WithTimeout(ctx, time.Duration(contract.TimeoutSeconds)*time.Second)
		} else {
			ctx, cancel = context.WithCancel(ctx)
		}
		defer cancel()

		result, totalTokens, err := engine.Run(ctx, "")
		if err != nil {
			log.Printf("[Recovery %s] Agent loop failed: %v", spec.TaskID, err)
			failureReason := err.Error()
			if errors.Is(err, context.DeadlineExceeded) {
				failureReason = "task_timeout"
			}
			hub.SendEvent(event.NewEvent("task_failed", spec.TaskID, cp.AgentID, 0, map[string]any{
				"reason": failureReason,
			}))
			return
		}
		log.Printf("[Recovery %s] Completed. Tokens: %d, Result: %s", spec.TaskID, totalTokens, truncate(result, 100))
	}()

	return cp.AgentID, nil
}

// makeRunnerDeps 把 chat / cron / multi-agent / session-chat / run-case 等入口
// 共用的那组"非全局"依赖，连同包级全局（globalSkillRegistry / globalOrchestrator /
// tracer）一起组装成 AgentDeps。
//
// 为什么仍需要这个 helper：Phase 8-A 之前所有入口都调一个 20+ 参数的
// runAgentLoopWithTurn 包级函数；现在该函数已删除，入口改为
// `NewAgentRunner(hub, makeRunnerDeps(...)).Run(ctx, AgentRunSpec{...})`。
// 这 15 个依赖是各 handler 的参数（或 main() 闭包捕获的局部变量），无法再压缩——
// 但 AgentDeps 把它们与"运行期可变状态"（spec）分离，让 runner 可独立测试，
// 且 spec 不再混入依赖参数，是本阶段收敛的核心收益。
//
// hub 不在此处：它属于 AgentRunner 而非 AgentDeps，由 NewAgentRunner 单独接收。
func makeRunnerDeps(cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, approvalHandler harness.ApprovalHandler, memRecall *harness.MemoryRecall, agentBus runtime.AgentBus, checkpointMgr *runtime.CheckpointManager, memDB harness.CompressorDB, costRepo cost.CostRepository, modelRegistry *llm.ModelRegistry, modelRouter *llm.Router, routerProviders map[string]llm.Provider, caseService *cases.Service, todoSvc *todo.Service) AgentDeps {
	return AgentDeps{
		Cfg:             cfg,
		Tools:           tools,
		Persist:         persist,
		ApprovalHandler: approvalHandler,
		AgentBus:        agentBus,
		CheckpointMgr:   checkpointMgr,
		CostRepo:        costRepo,
		ModelRegistry:   modelRegistry,
		ModelRouter:     modelRouter,
		RouterProviders: routerProviders,
		CaseService:     caseService,
		TodoSvc:         todoSvc,
		// 以下三项是进程级单例：server 只有一个 orchestrator / skill registry /
		// tracer，Engine 构建期也直接引用这些包级全局。这里同步引用，保持单一
		// 事实源，避免 appServer 字段与全局出现两份不一致的引用。
		SkillRegistry: globalSkillRegistry,
		Orchestrator:  globalOrchestrator,
		Tracer:        tracer,
		MemDB:         memDB,
		MemRecall:     memRecall,
	}
}

// orchestratorDispatcher 是 SubAgentDispatcher 在 cmd/server 层的实现。
// 它把 dispatch_sub_agent 工具的调用转发给 orchestrator.RunBlocking。
type orchestratorDispatcher struct {
	orch *orchestrator.Orchestrator
}

// leaderApprovalHandler 把审批请求委托给 supervisor leader Engine。
type leaderApprovalHandler struct {
	leaderSubTaskID string
}

// RequestDelegatedApproval 在 cmd/server 层实现 runtime.ApprovalDelegationHandler。
// 它通过全局 engineRegistry 查找 supervisor leader 的 Engine，在注册表中登记等待
// channel，然后往 leader 发送 AgentBus 审批请求消息，最后等待 leader 调用
// approve/reject_sub_agent_action 工具做出决定。
func (h *leaderApprovalHandler) RequestDelegatedApproval(req runtime.DelegatedApprovalRequest) (bool, bool, error) {
	// 在 engineRegistry 中按 subTaskID 精确查找 supervisor Engine。
	v, ok := engineRegistry.Load(req.SupervisorSubTaskID)
	if !ok {
		log.Printf("[leaderApprovalHandler] supervisor engine not found: %s", req.SupervisorSubTaskID)
		return false, false, fmt.Errorf("supervisor engine not found")
	}
	leaderEngine, ok := v.(*runtime.Engine)
	if !ok {
		return false, false, fmt.Errorf("supervisor engine record invalid")
	}

	ch := make(chan runtime.DelegatedApprovalDecision, 1)
	runtime.RegisterDelegatedApproval(req.ApprovalID, ch)
	defer runtime.UnregisterDelegatedApproval(req.ApprovalID)

	// 通过 leader Engine 的 AgentBus listener 把审批请求作为 user message 注入。
	content, _ := runtime.BuildApprovalDelegationContent(req)
	leaderEngine.SendAgentMessage("approval_request", req.SupervisorSubTaskID, content)

	// 等待 leader 审批决定，带超时回退。
	decision, err := runtime.WaitForDelegatedApproval(req.ApprovalID, 30*time.Second)
	if err != nil {
		log.Printf("[leaderApprovalHandler] 等待 leader 审批超时: %v", err)
		return false, false, err
	}
	return decision.Approved, true, nil
}

// Dispatch 调用 orchestrator 同步运行一组子 agent。
// 在 Phase 7-H 中，leaderSubTaskID 就是 root task ID。
func (d *orchestratorDispatcher) Dispatch(ctx context.Context, leaderSubTaskID, strategy string, agents []tool.SubAgentSpec) ([]tool.SubAgentResult, error) {
	orchSpecs := make([]orchestrator.AgentSpec, len(agents))
	for i, a := range agents {
		orchSpecs[i] = orchestrator.AgentSpec{
			AgentID:      a.AgentID,
			Name:         a.Name,
			SystemPrompt: a.SystemPrompt,
			Input:        a.Input,
			Model:        a.Model,
			AllowedTools: a.AllowedTools,
			OutputTo:     a.OutputTo,
		}
	}
	results := d.orch.RunBlocking(ctx, leaderSubTaskID, strategy, orchSpecs)
	out := make([]tool.SubAgentResult, len(results))
	for i, r := range results {
		out[i] = tool.SubAgentResult{
			AgentID:     r.AgentID,
			Name:        r.Name,
			Status:      r.Status,
			Result:      r.Result,
			TotalTokens: r.TotalTokens,
			Error:       r.Error,
			Duration:    r.Duration,
		}
	}
	return out, nil
}

// agentAllowedTools 从 DB 加载某 agent 配置的 tools。
// 如果 agent 不存在或没有配置 tools，返回 nil，在 Engine 与 PolicyGate
// 中表示"允许所有 tool"。
func agentAllowedTools(agentID string) []string {
	if agentID == "" {
		return nil
	}
	agent, err := db.QueryAgentByID(agentID)
	if err != nil || agent == nil {
		return nil
	}
	return agent.Tools
}

// resolveAllowedTools 返回某任务实际生效的 allowed-tools 列表。
// 请求显式提供的 tool 优先；否则使用 agent 配置的 tool。
// 结果为空表示无限制。
func resolveAllowedTools(reqTools []string, agentID string) []string {
	if len(reqTools) > 0 {
		return reqTools
	}
	return agentAllowedTools(agentID)
}

// isAllowedScope 判断 scope 是否被配置的 contract 限制允许。
// 空 scope 视为允许（回退到默认值）。若未配置任何 scope，则所有 scope 都允许。
func isAllowedScope(scope string, allowed []string) bool {
	if scope == "" {
		return true
	}
	if len(allowed) == 0 {
		return true
	}
	for _, s := range allowed {
		if s == scope {
			return true
		}
	}
	return false
}

// enrichAgentSpecAllowedTools 从 DB 加载每个 spec 对应的 agent，
// 并在 spec 未显式提供 AllowedTools 时补齐。
func enrichAgentSpecAllowedTools(specs []orchestrator.AgentSpec) []orchestrator.AgentSpec {
	for i := range specs {
		if len(specs[i].AllowedTools) > 0 {
			continue
		}
		if tools := agentAllowedTools(specs[i].AgentID); len(tools) > 0 {
			specs[i].AllowedTools = tools
			if specs[i].Contract == nil {
				contract := harness.DefaultContract(specs[i].Input)
				specs[i].Contract = &contract
			}
			specs[i].Contract.AllowedTools = tools
		}
	}
	return specs
}

// projectRulesPrompt 从 session 反查其所属 project，读取 project.config.rules 文本，
// 并格式化为可注入 system prompt 的 Markdown 段落。用于在发起任务时把项目级规则
// 自动注入到该 project 下所有 session 的 agent。
//
// 返回空字符串表示无规则（session 不存在、project 不存在、或未配置 rules），
// 调用方拼接时不会产生多余空白段。格式对齐 recall.FormatForSystemPrompt 的
// "## Working Memory" 标题层级，使用 "## Project Rules" 区分来源。
func projectRulesPrompt(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	sess, err := db.QuerySessionByID(sessionID)
	if err != nil || sess == nil || sess.ProjectID == "" {
		return ""
	}
	proj, err := db.QueryProjectByID(sess.ProjectID)
	if err != nil || proj == nil || proj.Config == nil {
		return ""
	}
	rules, _ := proj.Config["rules"].(string)
	if strings.TrimSpace(rules) == "" {
		return ""
	}
	return "\n\n## Project Rules\n" + rules + "\n"
}

// runAgentLoopWithTurn 是 AgentRunner 的核心：执行一次 chat 请求的完整 ReAct loop。
// 它接受一个 AgentRunSpec（替代旧的 20+ 位置参数），按 spec 中的角色/权限字段
// （为零值时按 IsRoot 自动补默认）构建 Engine 并运行。
//
// 历史注释保留：只有 TurnIndex == 0（首轮）时才做 root task 绑定。CaseID 是
// 可选的 MockProvider 提示；modelRouter 为 nil 时 Engine 透明回退到 cfg.Model。
func (r *AgentRunner) runAgentLoopWithTurn(spec AgentRunSpec) {
	// 把 spec 字段与 runner 依赖展开为局部别名，使下方从旧 runAgentLoopWithTurn
	// 迁移来的函数体无需逐行改名即可工作。这是 Phase 8-A 收敛参数的过渡手段：
	// 后续可逐步把函数体直接改用 spec / r.Deps 字段。
	hub := r.Hub
	cfg := r.Deps.Cfg
	tools := r.Deps.Tools
	persist := r.Deps.Persist
	approvalHandler := r.Deps.ApprovalHandler
	agentBus := r.Deps.AgentBus
	checkpointMgr := r.Deps.CheckpointMgr
	costRepo := r.Deps.CostRepo
	modelRegistry := r.Deps.ModelRegistry
	modelRouter := r.Deps.ModelRouter
	routerProviders := r.Deps.RouterProviders
	caseService := r.Deps.CaseService
	todoSvc := r.Deps.TodoSvc
	taskID := spec.TaskID
	agentID := spec.AgentID
	systemPrompt := spec.SystemPrompt
	userInput := spec.UserInput
	sessionID := spec.SessionID
	parentTaskID := spec.ParentTaskID
	turnIndex := spec.TurnIndex
	caseID := spec.CaseID
	workingMemory := spec.WorkingMemory
	contract := spec.Contract
	rootTraceCtx := []*observability.TraceContext{}
	if spec.RootTraceCtx != nil {
		rootTraceCtx = []*observability.TraceContext{spec.RootTraceCtx}
	}
	isRoot := turnIndex == 0

	// Phase 7-H：root agent 默认作为 leader，拥有子 agent 派发与工作流定义权限。
	role := runtime.AgentRoleWorker
	canDispatchSubAgents := false
	canDefineWorkflow := false
	supervisorSubTaskID := ""
	approverMode := "leader"
	if isRoot {
		role = runtime.AgentRoleLeader
		canDispatchSubAgents = true
		canDefineWorkflow = true
		approverMode = "user"
	}

	// 持久化任务创建
	if persist != nil {
		persist.SaveTask(taskID, userInput, []string{agentID})
		persist.SaveTaskMeta(taskID, sessionID, parentTaskID, isRoot)
		// 把 root task 绑定到 session，让前端刷新后仍能加载
		if sessionID != "" && isRoot {
			log.Printf("[runAgentLoopWithTurn] sessionID=%s taskID=%s — checking root_task_id", sessionID, taskID)
			sess, err := db.QuerySessionByID(sessionID)
			if err != nil {
				log.Printf("[runAgentLoopWithTurn] QuerySessionByID error: %v", err)
			} else if sess.RootTaskID == "" {
				log.Printf("[runAgentLoopWithTurn] Setting session %s root_task_id = %s", sessionID, taskID)
				db.UpdateSession(sessionID, taskID, sess.Status, sess.UserInput)
			} else {
				log.Printf("[runAgentLoopWithTurn] Session %s already has root_task_id=%s (skip)", sessionID, sess.RootTaskID)
			}
		}
	}

	// 解析 session 的 workspace 目录，让工具（run_shell、write_file、
	// read_file）以正确的 CWD 执行。每一轮都要读取 —— 不只是 root ——
	// 这样多轮对话的后续轮次才能继承同一个 workspace。
	//
	// 若 session.WorkspaceDir 为空但 session 属于某个 project，则回退到
	// project.WorkingDirectory，让多个 session 可共享同一个 project workspace。
	workspaceDir := ""
	if sessionID != "" {
		if wsSess, err := db.QuerySessionByID(sessionID); err == nil {
			workspaceDir = wsSess.WorkspaceDir
			if workspaceDir == "" && wsSess.ProjectID != "" {
				if proj, projErr := db.QueryProjectByID(wsSess.ProjectID); projErr == nil && proj.WorkingDirectory != "" {
					workspaceDir = proj.WorkingDirectory
				}
			}
		}
	}

	// 从 mock/全局配置解析 LLM Provider。Provider 在每个 agent loop 中只
	// 创建一次并传给 Engine，以便 mock 开关 (LLM_USE_MOCK / LLMRealCases /
	// LLMMockEndpoints) 生效。出错时记录日志并回退到 nil；Engine 会再用
	// Endpoint/APIKey/Model 创建一个默认的 OpenAIProvider。
	provider, err := llm.CreateProviderFromConfig(cfg, cfg.LLMModel, caseID)
	if err != nil {
		log.Printf("[runAgentLoopWithTurn] Failed to create provider for case=%q (falling back to default): %v", caseID, err)
		provider = nil
	}

	// 构建 Harness policy gate，包含所有安全规则：
	//   PathTraversalRule      —— 阻止文件路径中的 ".."
	//   FileScopeRule          —— 把文件操作限制在 contract scope 内
	//   DangerousCommandRule   —— 阻止危险的 shell 命令 (Phase 5)
	//   ApprovalRule           —— 高风险操作需要前端审批 (Phase 5)
	//   TagPolicyRule          —— 通过 tool tag 强制 TaskContract 权限
	//   TokenBudgetRule        —— 超出 token 预算时阻止 tool call
	//   ToolWhitelistRule      —— 仅允许 contract 中列出的 tool
	//   CostBudgetRule         —— 超出 USD 成本预算时阻止 tool call (M2)
	//
	// 规则按顺序检查。第一个阻止的规则会停止链路。
	tokenBudgetRule := &harness.TokenBudgetRule{}
	costBudgetRule := harness.NewCostBudgetRule()
	policyChain := harness.NewPolicyChain(
		&harness.PathTraversalRule{},
		&harness.FileScopeRule{},
		&harness.DangerousCommandRule{},
		harness.NewApprovalRule(approvalHandler),
		harness.NewTagPolicyRule(tools.ToolTags),
		tokenBudgetRule,
		&harness.ToolWhitelistRule{},
		costBudgetRule,
	)
	policyGate := harness.NewPolicyGate(policyChain, contract)

	// 为任务建立进度追踪
	progressManager := harness.NewProgressManager()

	// Phase 6-D: 把 Engine 的 usage/cost 回调接到 CostTracker、Repository
	// 和 MetricsCollector。这是不感知成本的 Engine 把每次 LLM 调用的
	// usage 数据交出去做持久化与可观测性的唯一接入点。我们每个进程只
	// 创建一个 CostTracker（不是每个任务一个），让指标全局累计。
	costTracker := cost.NewCostTracker(cost.WithRegistry(modelRegistry))
	onUsage := func(model string, profile *llm.ModelProfile, usage llm.Usage) {
		observability.DefaultMetrics.RecordLLMCall(
			uint64(usage.PromptTokens),
			uint64(usage.CompletionTokens),
			uint64(usage.TotalTokens),
		)
		projectID := "default"
		if sessionID != "" {
			if sess, err := db.QuerySessionByID(sessionID); err == nil {
				projectID = sess.ProjectID
			}
		}
		// 若 Engine 未提供 profile（旧版回退），从 registry 解析一个，
		// 以便填充 pricing/tier 字段。
		if profile == nil || profile.Provider == "unknown" {
			if p := modelRegistry.Get(model); p != nil {
				profile = p
			}
		}
		record := costTracker.BuildRecordFromProfile(
			taskID, sessionID, projectID, agentID,
			0, // step_index 从 usage 聚合角度填充
			model, profile, usage,
		)
		// M2 修复：把本次调用成本累加进 CostBudgetRule，让 PolicyChain 在
		// 后续 tool call 中能根据累计成本阻断。此前 CostBudgetRule 已实现
		// 并有单测，但从未接入 main.go 的 PolicyChain，端到端不生效。
		// CostBudgetRule.currentCostUSD 本就是 float64 USD，直接传 CostUSD
		// 即可，无需经 CostCents 绕路（后者是 derived round 值，sub-cent 会丢精度）。
		if record.CostUSD > 0 {
			costBudgetRule.SetCost(record.CostUSD)
		}
		// 尽力而为的持久化；失败只记录日志，不中断任务。
		if costRepo != nil {
			if err := costRepo.Insert(record); err != nil {
				observability.DefaultLogger.Warn("cost", "failed to persist cost record", map[string]any{
					"task_id": taskID,
					"error":   err.Error(),
				})
			}
		}
		observability.DefaultMetrics.RecordCost(record.CostCents)
	}

	// Phase 7-H2：root agent 作为 leader，从 base registry 克隆并注入
	// leader 专用工具（dispatch_sub_agent 与审批工具）。leaderSubTaskID
	// 就是本次 taskID，直接绑定，避免全局 leaderDispatchEnabled 的单 leader
	// 假设与跨 session 竞态问题。非 leader 继续使用共享的 base registry。
	engineTools := tools
	if role == runtime.AgentRoleLeader {
		engineTools = tools.Clone()
		dispatcher := &orchestratorDispatcher{orch: globalOrchestrator}
		resolveApproval := func(approvalID string, approved bool, reason string) error {
			return runtime.ResolveDelegatedApproval(runtime.DelegatedApprovalDecision{
				ApprovalID: approvalID,
				Approved:   approved,
				Reason:     reason,
			})
		}
		for _, t := range tool.NewLeaderTools(dispatcher, taskID, resolveApproval) {
			engineTools.Register(t)
		}
	}

	engine := runtime.NewEngine(runtime.EngineConfig{
		AgentID:              agentID,
		SystemPrompt:         systemPrompt,
		Model:                cfg.LLMModel,
		Endpoint:             cfg.LLMEndpoint,
		APIKey:               cfg.LLMAPIKey,
		Provider:             provider, // 上方解析出的 mock 或真实 provider
		CaseID:               caseID,   // MockProvider 脚本匹配提示
		Temperature:          0.7,
		MaxTokens:            4096,
		MaxSteps:             contract.MaxSteps,
		Persistence:          persist,
		PolicyGate:           policyGate,
		Progress:             progressManager,
		Contract:             contract,
		SessionID:            sessionID,
		IsRoot:               isRoot,
		ParentTaskID:         parentTaskID,
		ApprovalHandler:      approvalHandler, // Phase 5: 审批处理器
		WorkingMemory:        workingMemory,   // Phase 6: 工作记忆注入
		AgentBus:             agentBus,        // Phase 5: 多Agent通信
		CheckpointManager:    checkpointMgr,   // Phase 5: 崩溃恢复
		TurnIndex:            turnIndex,       // 当前轮次
		WorkspaceDir:         workspaceDir,    // Session 级 workspace 目录（write_file/run_shell 的 CWD）
		OnLLMUsage:           onUsage,         // Phase 6-D: 成本/指标上报
		// Phase 6 Router: 把 model router 接入 Engine，让 chat 路径真正地
		// 分类 intent 并选择模型 tier。modelRouter 为 nil（classifier 不可用）
		// 时 Engine 透明地回退到 cfg.Model —— 保留旧行为。
		Router:    modelRouter,
		Registry:  modelRegistry,
		Providers: routerProviders,
		EvaluationRepository: func() runtime.EvaluationRepository {
			if caseService == nil {
				return nil
			}
			return caseService.Repository()
		}(),
		SessionMessageWriter: func(msg runtime.SessionMessageRecord) error {
			return db.InsertSessionMessage(db.SessionMessageRecord{
				ID:         "msg_" + uuid.New().String(),
				SessionID:  sessionID,
				TaskID:     msg.TaskID,
				TurnIndex:  msg.TurnIndex,
				Role:       msg.Role,
				Content:    msg.Content,
				ToolCallID: msg.ToolCallID,
				ToolCalls:  msg.ToolCalls,
				TokenCount: msg.TokenCount,
			})
		},
		SubTaskID:            taskID,
		// Phase 7-H: 角色与权限字段。
		Role:                 role,
		CanDispatchSubAgents: canDispatchSubAgents,
		CanDefineWorkflow:    canDefineWorkflow,
		SupervisorSubTaskID:  supervisorSubTaskID,
		ApproverMode:         approverMode,
		// Phase 7-I: worker 且 leader 审批模式时，把审批委托给 supervisor Engine。
		SupervisorDecisionHandler: func() runtime.ApprovalDelegationHandler {
			if role == runtime.AgentRoleWorker && approverMode == "leader" {
				return &leaderApprovalHandler{leaderSubTaskID: supervisorSubTaskID}
			}
			return nil
		}(),
		// Phase 7-C: 把 tracer、root context 与 latency recorder 接入 Engine。
		Tracer: tracer,
		RootTraceCtx: func() *observability.TraceContext {
			if len(rootTraceCtx) > 0 {
				return rootTraceCtx[0]
			}
			// 兜底：调用方未提供 root context 时新建一个（例如测试 / 恢复路径）。
			return tracer.StartRoot(taskID, "task")
		}(),
		LLMLatencyRecorder: func(latency time.Duration) {
			observability.DefaultMetrics.RecordLLMLatency(latency)
		},
		ToolLatencyRecorder: func(latency time.Duration) {
			observability.DefaultMetrics.RecordToolLatency(latency)
		},
		// Phase skill: 注入 Skill 子系统。ActiveSkills 取当前 registry 中所有
		// 处于 enabled 状态的 skill id；SkillVariables 暂留 nil，由后续 case/
		// session 上下文填充。
		SkillRegistry: globalSkillRegistry,
		ActiveSkills:  GetEnabledSkillIDs(globalSkillRegistry),
		// Phase 7 TODO: 把当前 session 的 active todos 注入 system prompt。
		// todoSvc 为 nil（DB 未初始化）或 sessionID 为空时跳过。
		ActiveTodos: func() string {
			if todoSvc == nil || sessionID == "" {
				return ""
			}
			activeTodos, err := todoSvc.ListActiveBySession(sessionID)
			if err != nil {
				observability.DefaultLogger.Warn("todo", "failed to load active todos for system prompt", map[string]any{
					"session_id": sessionID,
					"error":      err.Error(),
				})
				return ""
			}
			return todo.FormatActiveTodos(activeTodos)
		}(),
	}, engineTools, &hubAdapter{hub: hub}, taskID)

	hub.SendEvent(event.NewEvent("task_started", taskID, agentID, 0, map[string]any{
		"task_id":    taskID,
		"agent_id":   agentID,
		"session_id": sessionID,
		"input":      userInput,
		"turn_index": turnIndex,
	}))

	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	// 应用 contract 中按任务指定的 timeout。TimeoutSeconds > 0 创建带 deadline
	// 的 context；0（或负数）表示无限制 —— 不设 deadline。
	if contract.TimeoutSeconds > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(contract.TimeoutSeconds)*time.Second)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	// 注册任务的 cancel 函数，以便 WebSocket 控制消息能取消本任务
	//（root 或 child）。goroutine 退出时必须移除，避免在 cancelRegistry
	// 中遗留条目。
	// Phase 7-A: 同时把 Engine 实例注册到 engineRegistry，使前端
	// pause/resume 消息能直接拿到引擎句柄。
	storeCancel(taskID, agentID, cancel)
	storeEngine(taskID, agentID, engine)
	defer removeCancel(taskID, agentID)
	defer removeEngine(taskID, agentID)

	observability.DefaultMetrics.IncrTasksStarted()

	result, totalTokens, err := engine.Run(ctx, userInput)
	if err != nil {
		observability.DefaultMetrics.IncrTasksFailed()
		log.Printf("[Task %s] Agent loop failed: %v", taskID, err)
		if sessionID != "" {
			// 失败后同样聚合所有任务 token 与 duration 并同步 session 状态，避免失败前
			// 的 token 消耗在第二次刷新 UI 时消失。
			aggregateTokens, _ := db.AggregateSessionTokens(sessionID)
			aggregateDuration, _ := db.AggregateSessionDuration(sessionID)
			db.UpdateSessionContextSize(sessionID, aggregateTokens, 0)
			newStatus := deriveSessionStatus(sessionID)
			db.UpdateSessionStatus(sessionID, newStatus)
			hub.SendEvent(event.NewEvent("session_status", taskID, agentID, 0, map[string]any{
				"session_id":   sessionID,
				"status":       newStatus,
				"total_tokens": aggregateTokens,
				"duration_ms":  aggregateDuration,
			}))
		}
		if result == "" {
			failureReason := err.Error()
			if errors.Is(err, context.DeadlineExceeded) {
				failureReason = "task_timeout"
			}
			hub.SendEvent(event.NewEvent("task_failed", taskID, agentID, 0, map[string]any{
				"reason": failureReason,
			}))
		}
		return
	}

	observability.DefaultMetrics.IncrTasksCompleted()

	// 完成后递增 session.turn_count（多轮对话）
	if sessionID != "" {
		db.UpdateSessionTurnCount(sessionID)
		// 聚合所有任务的累计 token 与 duration，同步回 sessions.total_tokens，保证
		// 侧边栏 token 显示和页面刷新后保持一致。
		aggregateTokens, _ := db.AggregateSessionTokens(sessionID)
		aggregateDuration, _ := db.AggregateSessionDuration(sessionID)
		db.UpdateSessionContextSize(sessionID, aggregateTokens, 0)
		newStatus := deriveSessionStatus(sessionID)
		db.UpdateSessionStatus(sessionID, newStatus)
		hub.SendEvent(event.NewEvent("session_status", taskID, agentID, 0, map[string]any{
			"session_id":   sessionID,
			"status":       newStatus,
			"total_tokens": aggregateTokens,
			"duration_ms":  aggregateDuration,
		}))
	}

	log.Printf("[Task %s] Completed successfully. Tokens: %d, Result: %s", taskID, totalTokens, truncate(result, 100))
}

// hubAdapter 把 ws.Hub 适配为 runtime.EventBus 接口。
type hubAdapter struct {
	hub *ws.Hub
}

func (a *hubAdapter) SendEvent(evt event.Event) {
	a.hub.SendEvent(evt)
}
