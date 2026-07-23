// server.go — appServer：HTTP 路由聚合体与注册入口。
//
// Phase 8-A 把原先全部堆在 main() 中的 30+ 个 http.HandleFunc / registerXxxRoutes
// 调用收敛到 appServer.registerRoutes()。appServer 本身只持有路由 handler 需要
// 的依赖（cfg / hub / toolRegistry / persist / 各 service …），不负责子系统初始化
// —— 初始化仍按顺序留在 main()，因为它们之间有严格的命令式依赖链（DB → cost →
// auth → memory → mock → modelRegistry → router → orchestrator → tools → skill →
// todo → cron → agentBus → checkpoint）。
//
// 拆分边界（刻意保留在 main()、不迁入 registerRoutes 的部分）：
//   - WS SetControlHandler 闭包：深度捕获 hub/approvalHandler，且与 cancelRegistry/
//     engineRegistry 包级状态紧耦合，迁移收益低、风险高。
//   - startChatTask / handleTasksRoot 闭包：捕获 main() 十几个局部变量，是 chat
//     action 与 cron start_task 的共享启动链路；Phase 8-A 后续 Task 9 会把它改为
//     appServer 方法 + AgentRunner，届时再随调用点一起迁移。
//   - serveVersionedUI：注册嵌入式 SPA 文件服务，无依赖、调用一次，留在 main() 即可。
//
// 因此 registerRoutes 聚合的是"纯路由表"：每个 handler 要么是无参包级函数，要么
// 显式接收 appServer 字段。这让 main() 从"初始化 + 路由 + 启动"三段式瘦身为
// "初始化 + 构造 appServer + registerRoutes + 启动"，路由总表首次有了一个可读的
// 单一视图。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/auth"
	"github.com/anmingwei/multi-agent-platform/internal/cases"
	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/internal/cost"
	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/memory"
	"github.com/anmingwei/multi-agent-platform/internal/observability"
	"github.com/anmingwei/multi-agent-platform/internal/orchestrator"
	"github.com/anmingwei/multi-agent-platform/internal/runtime"
	"github.com/anmingwei/multi-agent-platform/internal/skill"
	"github.com/anmingwei/multi-agent-platform/internal/todo"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp"
	"github.com/anmingwei/multi-agent-platform/internal/version"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// appServer 聚合 HTTP 路由注册所需的全部依赖。
//
// 字段对应 main() 中初始化的局部变量；registerRoutes 读取这些字段把路由挂到
// http.DefaultServeMux。保留 http.DefaultServeMux 是为了与 auth middleware、
// serveVersionedUI 以及既有的 registerXxxRoutes（都默认写 DefaultServeMux）兼容；
// 后续若要支持多实例/测试隔离，可再引入 mux 字段。
type appServer struct {
	cfg             *config.Config
	hub             *ws.Hub
	toolRegistry    *tool.Registry
	persist         runtime.Persistence
	approvalHandler harness.ApprovalHandler
	memRecall       *harness.MemoryRecall
	agentBusAdapter runtime.AgentBus
	checkpointMgr   *runtime.CheckpointManager
	memDB           *harness.SqliteMemoryDB
	costRepo        cost.CostRepository
	modelRegistry   *llm.ModelRegistry
	modelRouter     *llm.Router
	routerProviders map[string]llm.Provider
	caseService     *cases.Service
	todoSvc         *todo.Service
	skillRegistry   *skill.Registry
	skillStore      *skill.Store
	mcpManager      *mcp.Manager
	orch            *orchestrator.Orchestrator
	mockStore       llm.MockScriptStore
	authAPI         *auth.AuthAPI
	authStore       auth.APIKeyStore
	vectorStore     memory.VectorStore
	embedProvider   llm.EmbeddingProvider
	routerClassifier llm.Provider

	// runner 是 agent 运行入口。本任务（Task 7）暂未让所有 handler 改走 runner；
	// Task 9 会把 handleSessionChat / handleRunCase / handleTasksRoot chat action /
	// cron startChatTask 等逐步切到 runner.Run(spec)。这里先持有，供后续任务使用。
	runner *AgentRunner
}

// deps 构造 appServer 当前的 AgentDeps 快照。所有运行入口（chat / cron /
// checkpoint recovery / multi-agent leader）共用同一份依赖，避免重复拼装。
// SkillRegistry / Orchestrator / Tracer 仍以包级 global* 变量为权威来源——
// Engine 构建期也读这些全局——这里同步引用，保持单一事实源。
func (s *appServer) deps() AgentDeps {
	return AgentDeps{
		Cfg:             s.cfg,
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
		MemDB:           s.memDB,
		MemRecall:       s.memRecall,
	}
}

// newRunner 构造一个绑定当前 appServer 依赖的 AgentRunner。
func (s *appServer) newRunner() *AgentRunner {
	return NewAgentRunner(s.hub, s.deps())
}

// registerRoutes 把全部 HTTP 路由挂到 http.DefaultServeMux。
//
// 调用顺序与旧 main() 一致（Go ServeMux 对无重叠前缀的路由不依赖注册顺序，
// 但保持原序便于 diff 审阅与 /api/tasks vs /api/tasks/ 的可读性）。
func (s *appServer) registerRoutes() {
	cfg := s.cfg
	hub := s.hub
	toolRegistry := s.toolRegistry
	persist := s.persist
	approvalHandler := s.approvalHandler
	memRecall := s.memRecall
	agentBusAdapter := s.agentBusAdapter
	checkpointMgr := s.checkpointMgr
	memDB := s.memDB
	costRepo := s.costRepo
	modelRegistry := s.modelRegistry
	modelRouter := s.modelRouter
	routerProviders := s.routerProviders
	caseService := s.caseService
	todoSvc := s.todoSvc
	mcpManager := s.mcpManager
	skillStore := s.skillStore
	skillRegistry := s.skillRegistry
	mockStore := s.mockStore
	authAPI := s.authAPI
	authStore := s.authStore
	vectorStore := s.vectorStore
	embedProvider := s.embedProvider

	// WebSocket 入口
	http.HandleFunc("/ws", ws.ServeWS(hub))

	// Phase 7: Todo REST API（从 main() 迁入；todoSvc 可能为 nil，registerTodoRoutes
	// 内部对 nil service 返回 503）。
	registerTodoRoutes(http.DefaultServeMux, todoSvc)

	// Phase 7-cron: Cron REST API。globalCronService 在 main() 中初始化；为 nil
	// 时 RegisterCronAPI 直接 return（不注册任何端点）。
	RegisterCronAPI(http.DefaultServeMux, globalCronService)

	// /api/tasks/ —— 子资源路由（context_window / agent-messages / 单任务详情）。
	// 注册在 /api/tasks 之前，便于更具体的子路径优先匹配。
	http.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
		if path == "" {
			http.Error(w, "task ID required", http.StatusNotFound)
			return
		}

		if strings.HasSuffix(path, "/context_window") {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			id := strings.TrimSuffix(path, "/context_window")
			handleGetTaskContextWindow(w, r, id)
			return
		}

		// Phase 7-B: GET /api/tasks/:id/agent-messages —— 任务的 AgentBus 历史。
		if strings.HasSuffix(path, "/agent-messages") {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			id := strings.TrimSuffix(path, "/agent-messages")
			handleGetAgentMessages(w, r, id)
			return
		}

		// GET /api/tasks/:id —— 单个任务详情
		if r.Method == http.MethodGet {
			r.URL.RawQuery = "id=" + path
			handleGetTask(w, r)
			return
		}
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
	})

	http.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		// 精确的 /api/tasks （或 /api/tasks/）是根入口。
		if r.URL.Path == "/api/tasks" || r.URL.Path == "/api/tasks/" {
			if r.Method == http.MethodGet {
				if r.URL.Query().Get("id") != "" {
					handleGetTask(w, r)
					return
				}
				handleListTasks(w, r)
				return
			}
			// POST /api/tasks —— chat / multi-agent / stream-demo action。
			// 仍委托给 main() 中定义的 handleTasksRoot 闭包（捕获 main 局部依赖）。
			handleTasksRoot(w, r)
			return
		}
		http.Error(w, "task ID required", http.StatusNotFound)
	})

	// Phase 7-C: 可观测性 REST endpoint。
	http.HandleFunc("/api/audit", handleAudit)
	http.HandleFunc("/api/traces", handleTraces)
	http.HandleFunc("/api/replay/tasks/", handleReplay)
	http.HandleFunc("/api/replay/events", func(w http.ResponseWriter, r *http.Request) {
		handleReplayEvents(w, r, hub)
	})

	// Contract 限制 endpoint：暴露服务端强制的 task contract 边界。
	http.HandleFunc("/api/contract-limits", handleContractLimits(cfg))

	// Agent CRUD API
	http.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
			return
		}
		handleAgents(w, r)
	})
	http.HandleFunc("/api/agents/", func(w http.ResponseWriter, r *http.Request) {
		if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
			return
		}
		handleAgentByID(w, r)
	})

	// Session API
	http.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		handleSessions(w, r)
	})
	http.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// POST /api/sessions/{id}/chat —— 一个 session 内的多轮对话
		if strings.HasSuffix(path, "/chat") {
			handleSessionChat(w, r, hub, cfg, toolRegistry, persist, approvalHandler, memRecall, agentBusAdapter, checkpointMgr, memDB, costRepo, modelRegistry, modelRouter, routerProviders, caseService, todoSvc)
			return
		}
		// GET /api/sessions/{id}/messages —— session 消息历史
		if strings.HasSuffix(path, "/messages") {
			sessionID := strings.TrimSuffix(path, "/messages")
			sessionID = strings.TrimPrefix(sessionID, "/api/sessions/")
			handleSessionMessages(w, r, sessionID)
			return
		}
		// GET /api/sessions/{id}/workspace/dir —— 返回 workspace 路径与 auto 标志
		if strings.HasSuffix(path, "/workspace/dir") {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			sessionID := strings.TrimSuffix(path, "/workspace/dir")
			sessionID = strings.TrimPrefix(sessionID, "/api/sessions/")
			sess, err := db.QuerySessionByID(sessionID)
			if err != nil {
				http.Error(w, "session not found: "+err.Error(), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"session_id":     sessionID,
				"workspace_dir":  sess.WorkspaceDir,
				"workspace_auto": sess.WorkspaceAuto,
			})
			return
		}
		// GET /api/sessions/{id}/workspace-browse —— 供前端使用的 workspace 浏览信息
		if strings.HasSuffix(path, "/workspace-browse") {
			sessionID := strings.TrimSuffix(path, "/workspace-browse")
			sessionID = strings.TrimPrefix(sessionID, "/api/sessions/")
			handleSessionWorkspaceBrowse(w, r, sessionID)
			return
		}
		// GET /api/sessions/{id}/workspace-tree?path=<rel>
		if strings.HasSuffix(path, "/workspace-tree") {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			sessionID := strings.TrimSuffix(path, "/workspace-tree")
			sessionID = strings.TrimPrefix(sessionID, "/api/sessions/")
			handleSessionWorkspaceTree(w, r, sessionID)
			return
		}
		handleSessionByID(w, r)
	})

	// Project API
	http.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		handleProjects(w, r)
	})
	http.HandleFunc("/api/projects/", func(w http.ResponseWriter, r *http.Request) {
		handleProjectByID(w, r)
	})

	// Phase 6-D: Cost 查询 API（task/session/project/daily 聚合）。
	http.HandleFunc("/api/costs", func(w http.ResponseWriter, r *http.Request) {
		handleCostQuery(w, r, costRepo)
	})

	// Phase 6-D: Health check endpoint（JSON，检查 DB + WS hub）。
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		status := map[string]any{
			"status":    "ok",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"checks": map[string]any{
				"websocket": map[string]string{"status": "ok"},
			},
		}
		dbStatus := "ok"
		if db.DB != nil {
			if err := db.DB.Ping(); err != nil {
				dbStatus = "error: " + err.Error()
				status["status"] = "degraded"
			}
		} else {
			dbStatus = "not initialized"
		}
		status["checks"].(map[string]any)["database"] = map[string]string{"status": dbStatus}

		w.Header().Set("Content-Type", "application/json")
		if status["status"] == "ok" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(status)
	})

	// Phase 6-D: Prometheus 文本格式的 Metrics endpoint。
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprint(w, observability.DefaultMetrics.PrometheusText())
	})

	// 保留旧的纯文本 health check 以向后兼容。
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	// Auth API endpoint（API key 管理）
	if authAPI != nil && authStore != nil {
		authAPI.RegisterRoutes(http.DefaultServeMux)
	}

	// Mock 脚本管理 API（Phase 6 mock provider）。
	RegisterMockRoutes(http.DefaultServeMux, mockStore, llm.BuiltinMockScripts())

	// 模型价格管理 API —— 查看/更新 ModelRegistry 价格。
	RegisterModelPriceRoutes(http.DefaultServeMux, modelRegistry)

	// Version API：从 version.txt 返回当前版本号
	http.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		json.NewEncoder(w).Encode(map[string]string{
			"version": version.Version,
		})
	})

	// Session workspace 静态文件服务 —— /s/{session_id}/...
	http.HandleFunc("/s/", func(w http.ResponseWriter, r *http.Request) {
		s.serveSessionWorkspace(w, r)
	})

	// Cases API：对预设与自定义 case 的完整 CRUD。
	http.HandleFunc("/api/cases", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if caseService == nil {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(cases.All())
				return
			}
			handleListCases(w, r, caseService)
		case http.MethodPost:
			if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
				return
			}
			handleCreateCase(w, r, caseService)
		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})
	http.HandleFunc("/api/cases/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/cases/")
		if path == "" || path == "/" {
			http.Error(w, "case ID required", http.StatusBadRequest)
			return
		}

		parts := strings.Split(path, "/")
		id := parts[0]

		// GET /api/cases/{id}/evaluations/{task_id}
		if len(parts) >= 2 && parts[1] == "evaluations" {
			handleGetCaseEvaluation(w, r, id, caseService)
			return
		}

		if len(parts) > 1 {
			http.Error(w, "invalid case resource", http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if caseService == nil {
				c := cases.Get(id)
				if c == nil {
					http.Error(w, "case not found", http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(c)
				return
			}
			handleGetCase(w, r, id, caseService)
		case http.MethodPut:
			if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
				return
			}
			handleUpdateCase(w, r, id, caseService)
		case http.MethodDelete:
			if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
				return
			}
			handleDeleteCase(w, r, id, caseService)
		default:
			http.Error(w, "GET, PUT, or DELETE only", http.StatusMethodNotAllowed)
		}
	})

	// Run Case 代理：POST /api/run-case
	http.HandleFunc("/api/run-case", func(w http.ResponseWriter, r *http.Request) {
		handleRunCase(w, r, hub, cfg, toolRegistry, persist, approvalHandler, memRecall, agentBusAdapter, checkpointMgr, memDB, costRepo, modelRegistry, modelRouter, routerProviders, caseService, todoSvc)
	})

	// MCP 管理 API：动态 add / enable / disable / remove。
	registerMCPRoutes(http.DefaultServeMux, mcpManager)

	// Phase skill: 注册 Skill REST API。
	registerSkillRoutes(http.DefaultServeMux, hub, skillStore, skillRegistry)

	// 动态 Tool 注册 API (Phase 2+)
	http.HandleFunc("/api/tools", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
				return
			}
			s.handleRegisterTool(w, r)
		case http.MethodGet:
			handleListTools(w, r, toolRegistry)
		case http.MethodDelete:
			if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
				return
			}
			s.handleDeleteTool(w, r)
		default:
			http.Error(w, "GET, POST, or DELETE only", http.StatusMethodNotAllowed)
		}
	})

	// Multi-Agent orchestration endpoint (Phase 4)
	// POST /api/multi-agent —— 并发运行多个 agent
	http.HandleFunc("/api/multi-agent", func(w http.ResponseWriter, r *http.Request) {
		s.handleMultiAgent(w, r)
	})

	// Phase 5: 任务恢复的 Checkpoint API endpoint
	http.HandleFunc("/api/checkpoints", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		handleListCheckpoints(w, r, checkpointMgr)
	})
	http.HandleFunc("/api/checkpoints/recover", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		handleRecoverCheckpoint(w, r, hub, cfg, toolRegistry, persist, approvalHandler, agentBusAdapter, checkpointMgr, modelRegistry, modelRouter, routerProviders)
	})

	// Memory API (Phase 6 / Phase 5-B)
	memGateway := harness.NewPromotionGate(memDB)
	http.HandleFunc("/api/memories", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleListMemories(w, r)
		case http.MethodPost:
			handleCreateMemory(w, r, hub, vectorStore, embedProvider)
		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})
	http.HandleFunc("/api/memories/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/memories/")
		if path == "promote" {
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			handlePromoteMemories(w, r, memGateway)
			return
		}
		if path == "recall" {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			handleRecallPreview(w, r, memRecall)
			return
		}
		if path == "stats" {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			handleMemoryStats(w, r)
			return
		}
		parts := strings.Split(path, "/")
		id := parts[0]
		if id == "" {
			http.Error(w, "memory ID required", http.StatusBadRequest)
			return
		}
		switch {
		case len(parts) == 2 && parts[1] == "scope" && r.Method == http.MethodPut:
			handleUpdateMemoryScope(w, r, id)
		case len(parts) == 2 && parts[1] == "embed" && r.Method == http.MethodPost:
			handleMemoryEmbed(w, r, id, hub, vectorStore, embedProvider)
		case len(parts) == 1:
			handleMemoryByID(w, r, id, hub, vectorStore, embedProvider)
		default:
			http.Error(w, "unsupported memory operation", http.StatusMethodNotAllowed)
		}
	})
}

// serveSessionWorkspace 处理 /s/{session_id}/... 静态文件服务。
// 让前端一键访问生成的 HTML/图片等资源。从 main() 迁移而来，逻辑不变。
func (s *appServer) serveSessionWorkspace(w http.ResponseWriter, r *http.Request) {
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/s/"), "/")
	if len(pathParts) == 0 || pathParts[0] == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}
	sessionID := pathParts[0]

	sess, err := db.QuerySessionByID(sessionID)
	if err != nil {
		http.Error(w, "session not found or no workspace", http.StatusNotFound)
		return
	}

	workspaceDir := sess.WorkspaceDir
	if workspaceDir == "" && sess.ProjectID != "" {
		if proj, projErr := db.QueryProjectByID(sess.ProjectID); projErr == nil && proj.WorkingDirectory != "" {
			workspaceDir = proj.WorkingDirectory
		}
	}
	if workspaceDir == "" {
		http.Error(w, "session not found or no workspace", http.StatusNotFound)
		return
	}

	// 安全：确保解析后的路径仍位于 workspace dir 内
	requestPath := filepath.Join(workspaceDir, filepath.Join(pathParts[1:]...))
	cleanPath := filepath.Clean(requestPath)
	workspaceRoot := filepath.Clean(workspaceDir)
	if !strings.HasPrefix(cleanPath, workspaceRoot) {
		http.Error(w, "path traversal detected", http.StatusForbidden)
		return
	}

	http.ServeFile(w, r, cleanPath)
}

// handleMultiAgent 处理 POST /api/multi-agent —— 并发运行多个 agent。
// 从 main() 迁移而来，逻辑不变；提取为方法以释放 main() 体积。
func (s *appServer) handleMultiAgent(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg
	hub := s.hub
	persist := s.persist
	memRecall := s.memRecall
	orch := s.orch

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Input          string                   `json:"input"`
		CaseType       string                   `json:"case_type"`
		MaxSteps       int                      `json:"max_steps"`
		TimeoutSeconds int                      `json:"timeout_seconds"`
		SessionID      string                   `json:"session_id"`
		Agents         []orchestrator.AgentSpec `json:"agents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.Input) > cfg.ContractLimits.MaxInputLength {
		http.Error(w, fmt.Sprintf("input length exceeds maximum of %d", cfg.ContractLimits.MaxInputLength), http.StatusBadRequest)
		return
	}
	if req.MaxSteps < 1 {
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
	if len(req.Agents) > cfg.ContractLimits.MaxSubAgents {
		http.Error(w, fmt.Sprintf("agents count exceeds maximum of %d", cfg.ContractLimits.MaxSubAgents), http.StatusBadRequest)
		return
	}

	if req.Input == "" && len(req.Agents) == 0 {
		http.Error(w, "input or agents is required", http.StatusBadRequest)
		return
	}

	// 把任务分解为 agent spec。当 result.Workflow 非 nil 时，使用 DAG 调度。
	var specs []orchestrator.AgentSpec
	strategy := "parallel"
	var workflow *orchestrator.AgentWorkflow
	if len(req.Agents) > 0 {
		specs = enrichAgentSpecAllowedTools(req.Agents)
	} else {
		var decomposer orchestrator.Decomposer
		if cfg.LLMUseMock {
			decomposer = orchestrator.NewTaskDecomposer()
		} else {
			decomposer = orchestrator.NewLLMDecomposer(cfg, s.routerClassifier)
		}
		result, err := decomposer.Decompose(req.Input, req.CaseType)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		specs = result.Agents
		strategy = result.Strategy
		workflow = result.Workflow
		if workflow != nil {
			strategy = "dag"
		}
	}

	// 若提供了全局 MaxSteps 覆盖则应用
	if req.MaxSteps > 0 {
		if req.MaxSteps > cfg.ContractLimits.MaxSteps {
			req.MaxSteps = cfg.ContractLimits.MaxSteps
		}
		for i := range specs {
			if specs[i].Contract == nil {
				contract := harness.DefaultContract(specs[i].Input)
				specs[i].Contract = &contract
			}
			specs[i].Contract.MaxSteps = req.MaxSteps
		}
	}

	// 若提供了全局 TimeoutSeconds 覆盖则应用。
	if req.TimeoutSeconds > 0 {
		for i := range specs {
			if specs[i].Contract == nil {
				contract := harness.DefaultContract(specs[i].Input)
				specs[i].Contract = &contract
			}
			specs[i].Contract.TimeoutSeconds = req.TimeoutSeconds
		}
	}

	// 为本次 orchestration 中的所有 agent 构建 Working Memory。
	workingMemory := ""
	if memRecall != nil {
		if wm, err := memRecall.BuildWorkingMemory("default", req.SessionID, req.Input, 3); err == nil {
			workingMemory = memRecall.FormatForSystemPrompt(wm)
		}
	}
	workingMemory += projectRulesPrompt(req.SessionID)
	for i := range specs {
		specs[i].WorkingMemory = workingMemory
	}

	// 解析或创建 session
	sessionID, taskID, err := resolveSession(req.SessionID, req.Input, persist)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	agentIDs := make([]string, len(specs))
	for i, sp := range specs {
		agentIDs[i] = sp.AgentID
	}

	// 持久化 orchestrator task
	if persist != nil {
		persist.SaveTask(taskID, req.Input, agentIDs)
		persist.SaveTaskMeta(taskID, sessionID, "", true)
		if sessionID != "" {
			sess, err := db.QuerySessionByID(sessionID)
			if err == nil && sess.RootTaskID == "" {
				db.UpdateSession(sessionID, taskID, sess.Status, sess.UserInput)
			}
		}
	}

	// 发送 orchestrator task started 事件
	hub.SendEvent(event.NewEvent("task_started", taskID, "orchestrator", 0, map[string]any{
		"task_id":     taskID,
		"session_id":  sessionID,
		"input":       req.Input,
		"agent_ids":   agentIDs,
		"agent_count": len(specs),
		"strategy":    strategy,
	}))

	// 按请求的协调策略启动 agent。workflow 存在时使用 DAG 调度。
	go func() {
		var timeout time.Duration = 10 * time.Minute
		minTimeout := 0
		for _, sp := range specs {
			if sp.Contract != nil && sp.Contract.TimeoutSeconds > 0 {
				if minTimeout == 0 || sp.Contract.TimeoutSeconds < minTimeout {
					minTimeout = sp.Contract.TimeoutSeconds
				}
			}
		}
		if minTimeout > 0 {
			timeout = time.Duration(minTimeout) * time.Second
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		storeCancel(taskID, "orchestrator", cancel)
		defer removeCancel(taskID, "orchestrator")
		defer cancel()
		if strategy == "dag" && workflow != nil {
			orch.RunBlockingDAG(ctx, taskID, workflow)
		} else {
			orch.RunBlocking(ctx, taskID, strategy, specs)
		}
		db.UpdateSessionStatus(sessionID, deriveSessionStatus(sessionID))
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"session_id":  sessionID,
		"task_id":     taskID,
		"agent_count": len(specs),
		"agent_ids":   agentIDs,
		"max_steps":   req.MaxSteps,
		"status":      "started",
	})
}
