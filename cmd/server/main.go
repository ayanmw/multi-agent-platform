package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/internal/version"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
	"github.com/anmingwei/multi-agent-platform/web"
	"github.com/google/uuid"
)

// cancelRegistry maps task_id to the context.CancelFunc for currently running
// agent loops. WebSocket control messages can look up and invoke these
// functions to cancel a task. Access is synchronized by sync.Map.
var cancelRegistry sync.Map

func main() {
	port := flag.String("port", "8080", "HTTP server port")
	flag.Parse()

	// First, register the WebSocket control handler. It uses the package-level
	// cancelRegistry so WebSocket control messages can cancel running tasks.
	var _ = cancelRegistry

	// Phase 6-D: Initialize structured logging level from configuration.
	observability.DefaultLogger.SetLevel(observability.ParseLogLevel(os.Getenv("LOG_LEVEL")))

	// Load configuration from .env and environment
	cfg, err := config.Load()
	if err != nil {
		observability.DefaultLogger.Error("server", "failed to load config", map[string]any{"error": err.Error()})
		log.Fatalf("Failed to load config: %v", err)
	}
	if *port != "8080" || cfg.ServerPort == "" {
		cfg.ServerPort = *port
	}

	// Initialize WebSocket hub
	hub := ws.NewHub()
	go hub.Run()

	approvalHandler := harness.NewWebSocketApprovalHandler(hub)

	observability.DefaultLogger.Info("server", "initializing subsystems", map[string]any{
		"port":      cfg.ServerPort,
		"db_path":   cfg.DBPath,
		"llm_model": cfg.LLMModel,
	})

	// Register control handler for client-side pause/resume/cancel and approval decisions
	hub.SetControlHandler(func(msg ws.ClientControlMsg) {
		observability.DefaultLogger.Debug("control", "received client control message", map[string]any{
			"action":      msg.Action,
			"task_id":     msg.TaskID,
			"agent_id":    msg.AgentID,
			"approval_id": msg.ApprovalID,
		})

		// Phase 5: route approval decisions to ApprovalHandler
		switch msg.Action {
		case "approve":
			if msg.ApprovalID != "" {
				approvalHandler.HandleDecision(msg.ApprovalID, true)
			}
		case "deny":
			if msg.ApprovalID != "" {
				approvalHandler.HandleDecision(msg.ApprovalID, false)
			}
		case "cancel":
			if msg.TaskID == "" {
				observability.DefaultLogger.Warn("control", "cancel received without task_id", nil)
				hub.SendEvent(event.NewEvent("system_info", "", "server", 0, map[string]any{
					"message": "cancel requires task_id",
				}))
				return
			}
			if cancelFn, ok := cancelRegistry.Load(msg.TaskID); ok {
				observability.DefaultLogger.Info("control", "cancelling task", map[string]any{"task_id": msg.TaskID})
				cancelFn.(context.CancelFunc)()
			} else {
				observability.DefaultLogger.Warn("control", "cancel received for unknown task", map[string]any{"task_id": msg.TaskID})
			}
		case "pause", "resume":
			observability.DefaultLogger.Info("control", "not implemented control action", map[string]any{"action": msg.Action})
			hub.SendEvent(event.NewEvent("system_info", msg.TaskID, "server", 0, map[string]any{
				"message":     msg.Action + " not implemented",
				"action":      msg.Action,
				"status_code": 501,
			}))
		default:
			observability.DefaultLogger.Warn("control", "unknown control action", map[string]any{"action": msg.Action})
		}
	})

	// Initialize cost repository (SQLite if available, else in-memory fallback).
	var costRepo cost.CostRepository = cost.NewInMemoryCostRepository()
	_ = costRepo

	// Auth store and API — initialized after DB setup (API key authentication).
	var authStore auth.APIKeyStore
	var authAPI *auth.AuthAPI

	// Initialize database
	if err := db.Init(cfg.DBPath); err != nil {
		observability.DefaultLogger.Warn("database", "db init failed, continuing without persistence", map[string]any{"error": err.Error()})
	} else {
		observability.DefaultLogger.Info("database", "initialized", map[string]any{"path": cfg.DBPath})
		var repoErr error
		costRepo, repoErr = cost.NewSqliteCostRepository(db.DB)
		if repoErr != nil {
			observability.DefaultLogger.Warn("cost", "failed to create sqlite cost repository, falling back to memory", map[string]any{"error": repoErr.Error()})
			costRepo = cost.NewInMemoryCostRepository()
		}
		// Initialize auth store and seed default admin + API key on first startup.
		if db.DB != nil {
			authStore = auth.NewSqliteAPIKeyStore(db.DB)
			authAPI = auth.NewAuthAPI(authStore)
			seedDefaultAdminIfNeeded(authStore)
			// Establish a stable fallback user ID for unauthenticated mode. The
			// seed user is used by the auth middleware and /api/auth/api-keys when
			// REQUIRE_AUTH is disabled.
			authAPI.SetSeedUserIDFromStore(authStore)
		}

		// Seed default agent if not exists
		if err := db.SeedDefaultAgent(); err != nil {
			observability.DefaultLogger.Warn("database", "failed to seed default agent", map[string]any{"error": err.Error()})
		}
		// Seed default project if not exists
		if err := db.SeedDefaultProject(); err != nil {
			observability.DefaultLogger.Warn("database", "failed to seed default project", map[string]any{"error": err.Error()})
		}
	}

	// Initialize Memory infrastructure — Heartbeat for episode consolidation
	memDB := &harness.SqliteMemoryDB{}
	heartbeat := harness.NewHeartbeat(memDB)
	go heartbeat.Start(context.Background())
	log.Println("Memory Heartbeat started (5min interval, adaptive)")

	// Initialize MemoryRecall with vector store for semantic memory recall.
	// The local embedding provider (TF-IDF/one-hot hash) and in-memory vector
	// store enable cosine-similarity search over consolidated and semantic memories.
	embedProvider := llm.NewLocalEmbeddingProvider(2048)
	vectorStore := memory.NewInMemoryVectorStore(embedProvider)
	memRecall := harness.NewMemoryRecallWithVectorStore(memDB, embedProvider, vectorStore)

	// Build the vector index from existing memories (best-effort — failures
	// only degrade recall quality, they do not break startup).
	if err := memRecall.BuildVectorIndex(); err != nil {
		log.Printf("MemoryRecall: failed to build vector index: %v", err)
	}

	// Initialize persistence adapter
	persist := &DBPersistence{}

	// Phase mock: Initialize mock script store and load built-in scripts.
	// The in-memory store is always available; if DB is initialized, also create
	// a SQLite-backed store and load dynamic scripts into the default store so
	// both management API and MockProvider share the same scripts.
	mockStore := llm.DefaultMockStore
	if db.DB != nil {
		sqliteMockStore := llm.NewSqliteMockScriptStore(db.DB)
		if err := sqliteMockStore.LoadBuiltin(llm.BuiltinMockScripts()); err != nil {
			observability.DefaultLogger.Warn("mock", "failed to seed builtin mock scripts into sqlite", map[string]any{"error": err.Error()})
		}
		dynamicScripts, err := sqliteMockStore.List()
		if err != nil {
			observability.DefaultLogger.Warn("mock", "failed to load dynamic mock scripts", map[string]any{"error": err.Error()})
		} else {
			if err := mockStore.LoadBuiltin(dynamicScripts); err != nil {
				observability.DefaultLogger.Warn("mock", "failed to load dynamic scripts into default store", map[string]any{"error": err.Error()})
			}
		}
	} else {
		if err := mockStore.LoadBuiltin(llm.BuiltinMockScripts()); err != nil {
			observability.DefaultLogger.Warn("mock", "failed to load builtin mock scripts", map[string]any{"error": err.Error()})
		}
	}
	observability.DefaultLogger.Info("mock", "mock provider initialized", map[string]any{
		"use_mock":       cfg.LLMUseMock,
		"real_cases":     cfg.LLMRealCases,
		"mock_endpoints": cfg.LLMMockEndpoints,
	})

	// Phase 6-D: Initialize model registry with default profiles for cost tracking
	// and future multi-provider routing. The registry is used by the CostTracker
	// to resolve tier/pricing information when building CostRecords.
	modelRegistry := llm.NewModelRegistry()
	for _, profile := range llm.DefaultProfiles() {
		modelRegistry.Register(profile)
	}
	log.Printf("ModelRegistry: loaded %d default profiles", len(llm.DefaultProfiles()))

	// Initialize tool registry with built-in tools
	toolRegistry := tool.NewRegistry()
	tool.RegisterBuiltins(toolRegistry)

	// Phase 5: Docker sandbox for run_shell tool.
	// Check Docker availability at startup. If available, wrap the run_shell tool
	// in a SandboxedShellTool. If not available, log a warning and use direct execution.
	sandboxCfg := tool.DefaultSandboxConfig()
	sandbox := tool.NewSandboxExecutor(sandboxCfg)
	if sandbox.IsAvailable() {
		log.Println("Docker sandbox: enabled — run_shell executes in isolated containers")
		// Replace the built-in run_shell with the sandboxed version.
		// First, unregister the original run_shell tool.
		toolRegistry.Unregister("run_shell")
		// Register the sandboxed version with the original as fallback.
		sandboxedShell := tool.NewSandboxedShellTool(sandbox, tool.NewRunShellTool())
		toolRegistry.Register(sandboxedShell)
	} else {
		log.Println("Docker sandbox: disabled — Docker not available, using direct execution")
	}
	log.Printf("Registered %d built-in tools", len(toolRegistry.List()))

	// Phase 5: AgentBus for inter-agent communication.
	// The AgentBus is shared across all agents and allows agents to send messages
	// to each other during execution.
	agentBus := orchestrator.NewAgentBus()
	agentBusAdapter := orchestrator.NewAgentBusAdapter(agentBus)

	// Phase 5: CheckpointManager for task recovery after crashes.
	// Checkpoints are saved at the end of each ReAct loop iteration.
	checkpointMgr := runtime.NewCheckpointManager("data/checkpoints")
	log.Println("CheckpointManager: initialized (data/checkpoints)")

	// Initialize multi-agent orchestrator
	orch := orchestrator.New(hub, cfg, toolRegistry, persist, agentBusAdapter, checkpointMgr)

	// WebSocket endpoint
	http.HandleFunc("/ws", ws.ServeWS(hub))

	// API: Start a chat task with real Agent Loop
	http.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		// GET /api/tasks — list recent tasks
		if r.Method == http.MethodGet {
			// GET /api/tasks?id=xxx — single task detail
			if r.URL.Query().Get("id") != "" {
				handleGetTask(w, r)
				return
			}
			handleListTasks(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Action       string                   `json:"action"`
			AgentID      string                   `json:"agent_id"`
			Input        string                   `json:"input"`
			SystemPrompt string                   `json:"system_prompt"`
			CaseType     string                   `json:"case_type"`
			MaxSteps     int                      `json:"max_steps"`
			TimeoutSeconds int                  `json:"timeout_seconds"`
			SessionID    string                   `json:"session_id"`
			Agents       []orchestrator.AgentSpec `json:"agents"`
			// TaskContract optional overrides — when >0 / non-empty, override the
			// default (or case-provided) contract so frontend can drive PolicyChain.
			Scope         string   `json:"scope"`
			AllowedTools  []string `json:"allowed_tools"`
			TokenBudget   int      `json:"token_budget"`
			CostBudgetUSD float64  `json:"cost_budget_usd"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch req.Action {

		case "multi-agent":
			// Multi-agent orchestration: decompose task and run agents concurrently
			specs := req.Agents
			if len(specs) == 0 {
				decomposer := &orchestrator.TaskDecomposer{}
				result := decomposer.Decompose(req.Input, req.CaseType)
				specs = result.Agents
			}

			// Build Working Memory for all agents in this orchestration
			if wm, err := memRecall.BuildWorkingMemory("default", req.SessionID, req.Input, 3); err == nil {
				workingMemory := memRecall.FormatForSystemPrompt(wm)
				for i := range specs {
					specs[i].WorkingMemory = workingMemory
				}
			}

			// Resolve or create session
			sessionID, taskID, err := resolveSession(req.SessionID, req.Input, persist)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			agentIDs := make([]string, len(specs))
			for i, s := range specs {
				agentIDs[i] = s.AgentID
			}

			if persist != nil {
				persist.SaveTaskMeta(taskID, sessionID, "", true)
				// Bind the root task to the session so the frontend can load it after refresh
				if sessionID != "" {
					sess, err := db.QuerySessionByID(sessionID)
					if err == nil && sess.RootTaskID == "" {
						db.UpdateSession(sessionID, taskID, sess.Status, sess.UserInput)
					}
				}
			}

			hub.SendEvent(event.NewEvent("task_started", taskID, "orchestrator", 0, map[string]any{
				"task_id":     taskID,
				"session_id":  sessionID,
				"input":       req.Input,
				"agent_ids":   agentIDs,
				"agent_count": len(specs),
			}))

			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				cancelRegistry.Store(taskID, cancel)
				defer cancelRegistry.Delete(taskID)
				defer cancel()
				orch.RunBlocking(ctx, taskID, specs)
				db.UpdateSessionStatus(sessionID, deriveSessionStatus(sessionID))
				log.Printf("[Multi-Agent] Task %s: all agents completed", taskID)
			}()

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"session_id":  sessionID,
				"task_id":     taskID,
				"agent_count": len(specs),
				"agent_ids":   agentIDs,
				"status":      "started",
			})
		case "stream-demo":
			sessionID, taskID, err := resolveSession(req.SessionID, "", persist)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			go streamTask(hub, taskID)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"session_id": sessionID,
				"task_id":    taskID,
			})

		case "chat":
			// Check if a preset case was specified — load its contract,
			// default input, and system prompt before validating the request.
			var contract harness.TaskContract
			caseID := r.URL.Query().Get("case")
			if caseID != "" {
				if c := cases.Get(caseID); c != nil {
					contract = c.Contract
					// Use case's default input if none provided in request
					if req.Input == "" {
						req.Input = c.DefaultInput
					}
					// Use case's system prompt if none provided in request
					if req.SystemPrompt == "" {
						req.SystemPrompt = c.SystemPrompt
					}
				}
			}

			if req.Input == "" {
				http.Error(w, "input is required for chat action", http.StatusBadRequest)
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
			// Override MaxSteps from request if provided (>0)
			if req.MaxSteps > 0 {
				contract.MaxSteps = req.MaxSteps
			}
			// Override timeout from request if provided (>0).
			if req.TimeoutSeconds > 0 {
				contract.TimeoutSeconds = req.TimeoutSeconds
			}
			// Override TaskContract fields from request body when provided —
			// lets the frontend drive PolicyChain (scope, tools, budgets, timeout).
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

			// Build Working Memory from past experiences for this task
			workingMemory := ""
			if wm, err := memRecall.BuildWorkingMemory("default", req.SessionID, req.Input, 3); err == nil {
				workingMemory = memRecall.FormatForSystemPrompt(wm)
			}

			taskID := "task_" + time.Now().Format("20060102150405")
			go runAgentLoop(hub, taskID, agentID, systemPrompt, req.Input, cfg, toolRegistry, persist, contract, req.SessionID, approvalHandler, workingMemory, agentBusAdapter, checkpointMgr, caseID, costRepo, modelRegistry)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"session_id": req.SessionID,
				"task_id":    taskID,
				"agent_id":   agentID,
				"action":     "chat",
			})

		default:
			http.Error(w, "unknown action (use 'stream-demo' or 'chat')", http.StatusBadRequest)
		}
	})

	// Agent CRUD API
	http.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		handleAgents(w, r)
	})
	http.HandleFunc("/api/agents/", func(w http.ResponseWriter, r *http.Request) {
		handleAgentByID(w, r)
	})

	// Session API
	http.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		handleSessions(w, r)
	})
	http.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// POST /api/sessions/{id}/chat — multi-turn chat within a session
		if strings.HasSuffix(path, "/chat") {
			handleSessionChat(w, r, hub, cfg, toolRegistry, persist, approvalHandler, memRecall, agentBusAdapter, checkpointMgr, memDB, costRepo, modelRegistry)
			return
		}
		// GET /api/sessions/{id}/messages — session message history
		if strings.HasSuffix(path, "/messages") {
			sessionID := strings.TrimSuffix(path, "/messages")
			sessionID = strings.TrimPrefix(sessionID, "/api/sessions/")
			handleSessionMessages(w, r, sessionID)
			return
		}
		// GET /api/sessions/{id}/workspace/dir — returns workspace path and auto flag
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
				"session_id":      sessionID,
				"workspace_dir":   sess.WorkspaceDir,
				"workspace_auto":  sess.WorkspaceAuto,
			})
			return
		}
		// GET /api/sessions/{id}/workspace-browse — workspace browse info for frontend
		if strings.HasSuffix(path, "/workspace-browse") {
			sessionID := strings.TrimSuffix(path, "/workspace-browse")
			sessionID = strings.TrimPrefix(sessionID, "/api/sessions/")
			handleSessionWorkspaceBrowse(w, r, sessionID)
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

	// Phase 6-D: Cost query API (task/session/project/daily aggregation).
	// Data is read from the CostRepository so persisted records are included.
	http.HandleFunc("/api/costs", func(w http.ResponseWriter, r *http.Request) {
		handleCostQuery(w, r, costRepo)
	})

	// Phase 6-D: Health check endpoint (JSON, checks DB + WS hub).
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

	// Phase 6-D: Metrics endpoint in Prometheus text format.
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprint(w, observability.DefaultMetrics.PrometheusText())
	})

	// Legacy plaintext health check retained for backward compatibility.
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	// Auth API endpoints (API key management)
	if authAPI == nil {
		authAPI = auth.NewAuthAPI(authStore)
	}
	if authStore != nil {
		authAPI.RegisterRoutes(http.DefaultServeMux)
	}

	// Mock script management API (Phase 6 mock provider).
	// RegisterMockRoutes is called after the mock store is initialized above
	// (see the "Phase mock" block). The store is shared between the management
	// API and the MockProvider via llm.DefaultMockStore.
	RegisterMockRoutes(http.DefaultServeMux, mockStore, llm.BuiltinMockScripts())

	// Version API: returns the current version from version.txt
	http.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		json.NewEncoder(w).Encode(map[string]string{
			"version": version.Version,
		})
	})

	// Session workspace static file serving — /s/{session_id}/...
	// Allows frontend one-click access to generated HTML/image assets.
	http.HandleFunc("/s/", func(w http.ResponseWriter, r *http.Request) {
		// Extract session_id from /s/{session_id}/...
		pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/s/"), "/")
		if len(pathParts) == 0 || pathParts[0] == "" {
			http.Error(w, "session_id required", http.StatusBadRequest)
			return
		}
		sessionID := pathParts[0]

		// Look up session to verify it exists and get workspace_dir
		sess, err := db.QuerySessionByID(sessionID)
		if err != nil || sess.WorkspaceDir == "" {
			http.Error(w, "session not found or no workspace", http.StatusNotFound)
			return
		}

		// Security: ensure the resolved path is within the workspace dir
		requestPath := filepath.Join(sess.WorkspaceDir, filepath.Join(pathParts[1:]...))
		cleanPath := filepath.Clean(requestPath)
		workspaceRoot := filepath.Clean(sess.WorkspaceDir)
		if !strings.HasPrefix(cleanPath, workspaceRoot) {
			http.Error(w, "path traversal detected", http.StatusForbidden)
			return
		}

		// Serve the file
		http.ServeFile(w, r, cleanPath)
	})

	// Cases API: list preset task cases
	http.HandleFunc("/api/cases", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cases.All())
	})

	// Run Case proxy: POST /api/run-case
	// Thin proxy used by the CaseCard frontend. Delegates to the same chat-action
	// logic as POST /api/tasks with the case_id extracted from the request body.
	http.HandleFunc("/api/run-case", func(w http.ResponseWriter, r *http.Request) {
		handleRunCase(w, r, hub, cfg, toolRegistry, persist, approvalHandler, memRecall, agentBusAdapter, checkpointMgr, memDB, costRepo, modelRegistry)
	})

	// Dynamic Tool Registration API (Phase 2+)
	http.HandleFunc("/api/tools", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			handleRegisterTool(w, r, toolRegistry)
		case http.MethodGet:
			handleListTools(w, r, toolRegistry)
		case http.MethodDelete:
			handleDeleteTool(w, r, toolRegistry)
		default:
			http.Error(w, "GET, POST, or DELETE only", http.StatusMethodNotAllowed)
		}
	})

	// Multi-Agent orchestration endpoint (Phase 4)
	// POST /api/multi-agent — runs multiple agents concurrently
	http.HandleFunc("/api/multi-agent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Input          string                   `json:"input"`
			CaseType       string                   `json:"case_type"` // "multi_agent", "code_gen", or empty
			MaxSteps       int                      `json:"max_steps"` // override max steps for all agents
			TimeoutSeconds int                      `json:"timeout_seconds"` // override timeout for all agents
			SessionID      string                   `json:"session_id"`
			Agents         []orchestrator.AgentSpec `json:"agents"` // direct agent specs (optional)
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Input == "" && len(req.Agents) == 0 {
			http.Error(w, "input or agents is required", http.StatusBadRequest)
			return
		}

		// Decompose task into agent specs
		var specs []orchestrator.AgentSpec
		if len(req.Agents) > 0 {
			specs = req.Agents
		} else {
			decomposer := &orchestrator.TaskDecomposer{}
			result := decomposer.Decompose(req.Input, req.CaseType)
			specs = result.Agents
		}

		// Apply global MaxSteps override if provided
		if req.MaxSteps > 0 {
			for i := range specs {
				if specs[i].Contract == nil {
					contract := harness.DefaultContract(specs[i].Input)
					specs[i].Contract = &contract
				}
				specs[i].Contract.MaxSteps = req.MaxSteps
			}
		}

		// Apply global TimeoutSeconds override if provided.
		if req.TimeoutSeconds > 0 {
			for i := range specs {
				if specs[i].Contract == nil {
					contract := harness.DefaultContract(specs[i].Input)
					specs[i].Contract = &contract
				}
				specs[i].Contract.TimeoutSeconds = req.TimeoutSeconds
			}
		}

		// Build Working Memory for all agents in this orchestration
		if wm, err := memRecall.BuildWorkingMemory("default", req.SessionID, req.Input, 3); err == nil {
			workingMemory := memRecall.FormatForSystemPrompt(wm)
			for i := range specs {
				specs[i].WorkingMemory = workingMemory
			}
		}

		// Resolve or create session
		sessionID, taskID, err := resolveSession(req.SessionID, req.Input, persist)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		agentIDs := make([]string, len(specs))
		for i, s := range specs {
			agentIDs[i] = s.AgentID
		}

		// Persist orchestrator task
		if persist != nil {
			persist.SaveTask(taskID, req.Input, agentIDs)
			persist.SaveTaskMeta(taskID, sessionID, "", true)
			// Bind the root task to the session so the frontend can load it after refresh
			if sessionID != "" {
				sess, err := db.QuerySessionByID(sessionID)
				if err == nil && sess.RootTaskID == "" {
					db.UpdateSession(sessionID, taskID, sess.Status, sess.UserInput)
				}
			}
		}

		// Emit orchestrator task started event
		hub.SendEvent(event.NewEvent("task_started", taskID, "orchestrator", 0, map[string]any{
			"task_id":     taskID,
			"session_id":  sessionID,
			"input":       req.Input,
			"agent_ids":   agentIDs,
			"agent_count": len(specs),
		}))

		// Launch agents concurrently
		go func() {
			// Multi-agent orchestration timeouts default to 10 minutes. If every
			// spec has the same TimeoutSeconds override, derive a single deadline
			// from the smallest positive value so tasks fail predictably; otherwise
			// fall back to the hardcoded 10 minute default.
			var timeout time.Duration = 10 * time.Minute
			minTimeout := 0
			for _, s := range specs {
				if s.Contract != nil && s.Contract.TimeoutSeconds > 0 {
					if minTimeout == 0 || s.Contract.TimeoutSeconds < minTimeout {
						minTimeout = s.Contract.TimeoutSeconds
					}
				}
			}
			if minTimeout > 0 {
				timeout = time.Duration(minTimeout) * time.Second
			}

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			cancelRegistry.Store(taskID, cancel)
			defer cancelRegistry.Delete(taskID)
			defer cancel()
			orch.RunBlocking(ctx, taskID, specs)
			db.UpdateSessionStatus(sessionID, deriveSessionStatus(sessionID))
			log.Printf("[Multi-Agent] Task %s: all agents completed", taskID)
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
	})

	// Phase 5: Checkpoint API endpoints for task recovery
	// GET /api/checkpoints — list all recoverable tasks
	http.HandleFunc("/api/checkpoints", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		handleListCheckpoints(w, r, checkpointMgr)
	})
	// POST /api/checkpoints/recover — resume a task from a checkpoint
	http.HandleFunc("/api/checkpoints/recover", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		handleRecoverCheckpoint(w, r, hub, cfg, toolRegistry, persist, approvalHandler, agentBusAdapter, checkpointMgr)
	})

	// Memory API (Phase 6 / Phase 5-B)
	// GET /api/memories?scope=...&tier=...&project=... — list memories
	// PUT /api/memories/{id}/scope — update memory scope
	// DELETE /api/memories/{id} — delete memory
	// POST /api/memories/promote — manually trigger promotion
	// GET /api/memories/recall?task=xxx&project=default&max=3 — preview what would be recalled
	memGateway := harness.NewPromotionGate(memDB)
	http.HandleFunc("/api/memories", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		handleListMemories(w, r)
	})
	http.HandleFunc("/api/memories/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/memories/")
		// POST /api/memories/promote — manually trigger promotion
		if path == "promote" {
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			handlePromoteMemories(w, r, memGateway)
			return
		}
		// GET /api/memories/recall?task=xxx&project=default&max=3
		if path == "recall" {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			handleRecallPreview(w, r, memRecall)
			return
		}
		// /api/memories/{id}/scope or /api/memories/{id}
		parts := strings.Split(path, "/")
		id := parts[0]
		if id == "" {
			http.Error(w, "memory ID required", http.StatusBadRequest)
			return
		}
		switch {
		case len(parts) == 2 && parts[1] == "scope" && r.Method == http.MethodPut:
			handleUpdateMemoryScope(w, r, id)
		case len(parts) == 1 && r.Method == http.MethodDelete:
			handleDeleteMemory(w, r, id)
		default:
			http.Error(w, "unsupported memory operation", http.StatusMethodNotAllowed)
		}
	})
	// Serve Vue SPA from embedded filesystem (production mode).
	// In dev mode, users can run `cd web && npm run dev` to use Vite's dev server
	// with HMR. The embedded dist/ is used when building the Go binary.
	distFS, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		log.Printf("Warning: embedded frontend dist not found: %v", err)
	} else {
		// Create a file server that serves the embedded dist/ directory
		fileServer := http.FileServer(http.FS(distFS))

		// SPA fallback: any request that doesn't match an API route or a static file
		// should serve index.html (Vue Router handles client-side routing).
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// API and WebSocket routes are handled by their own handlers registered above
			if r.URL.Path == "/" || r.URL.Path == "/index.html" || !fileExists(distFS, r.URL.Path) {
				// Serve index.html for SPA client-side routing (e.g., /agents, /tasks/123)
				// But only if the path doesn't match a real file in dist/
				indexFile, err := distFS.Open("index.html")
				if err != nil {
					http.NotFound(w, r)
					return
				}
				defer indexFile.Close()
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				http.ServeContent(w, r, "index.html", time.Time{}, indexFile.(io.ReadSeeker))
				return
			}
			fileServer.ServeHTTP(w, r)
		})
		log.Println("Frontend embedded: serving from embedded dist/")
	}

	requireAuth := os.Getenv("REQUIRE_AUTH") == "true"

	log.Printf("========================================")
	log.Printf("Multi-Agent Platform %s", version.Version)
	log.Printf("========================================")
	log.Printf("Server:      http://localhost:%s", cfg.ServerPort)
	log.Printf("WebSocket:   ws://localhost:%s/ws", cfg.ServerPort)
	log.Printf("API:         http://localhost:%s/api/tasks", cfg.ServerPort)
	log.Printf("Health:      http://localhost:%s/health", cfg.ServerPort)
	log.Printf("LLM:         %s (global mock=%t, model=%s)", cfg.LLMEndpoint, cfg.LLMUseMock, cfg.LLMModel)
	log.Printf("Auth:        %s", map[bool]string{true: "enabled", false: "disabled"}[requireAuth])
	log.Printf("Tools:       %d built-in", len(toolRegistry.List()))
	log.Printf("========================================")

	// Wrap the default mux with auth middleware. It protects state-changing routes
	// when REQUIRE_AUTH is true and injects a seed user ID for all routes otherwise.
	handler := auth.NewAuthMiddleware(authStore, authAPI.SeedUserID(), requireAuth, auth.DefaultProtectedRoutes(), http.DefaultServeMux)

	if err := http.ListenAndServe(":"+cfg.ServerPort, handler); err != nil {
		log.Fatal(err)
	}
}

// runAgentLoop executes the full ReAct loop for a chat request.
// It is a convenience wrapper around runAgentLoopWithTurn for the initial (root) turn.
// caseID is used by MockProvider for deterministic script matching; it is ignored
// when LLM_USE_MOCK is false or when the request does not target a preset case.
func runAgentLoop(hub *ws.Hub, taskID, agentID, systemPrompt, userInput string, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, contract harness.TaskContract, sessionID string, approvalHandler harness.ApprovalHandler, workingMemory string, agentBus runtime.AgentBus, checkpointMgr *runtime.CheckpointManager, caseID string, costRepo cost.CostRepository, modelRegistry *llm.ModelRegistry) {
	runAgentLoopWithTurn(hub, taskID, agentID, systemPrompt, userInput, cfg, tools, persist, contract, sessionID, approvalHandler, workingMemory, agentBus, checkpointMgr, 0, "", caseID, costRepo, modelRegistry)
}

// runAgentLoopWithTurn executes the full ReAct loop for a chat request within a
// multi-turn session. It accepts turnIndex and parentTaskID to support subsequent
// turns in a conversation (turnIndex >= 0). The root task binding is only done
// when turnIndex == 0 (first turn).
// caseID is an optional hint for the MockProvider to select a mock script by
// exact case match; when empty the provider falls back to keyword matching.
func runAgentLoopWithTurn(hub *ws.Hub, taskID, agentID, systemPrompt, userInput string, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, contract harness.TaskContract, sessionID string, approvalHandler harness.ApprovalHandler, workingMemory string, agentBus runtime.AgentBus, checkpointMgr *runtime.CheckpointManager, turnIndex int, parentTaskID string, caseID string, costRepo cost.CostRepository, modelRegistry *llm.ModelRegistry) {
	isRoot := turnIndex == 0

	// Persist task creation
	if persist != nil {
		persist.SaveTask(taskID, userInput, []string{agentID})
		persist.SaveTaskMeta(taskID, sessionID, parentTaskID, isRoot)
		// Bind the root task to the session so the frontend can load it after refresh
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

	// Resolve the session workspace directory so that tools (run_shell,
	// write_file, read_file) execute with the correct CWD. This is read for
	// EVERY turn — not just the root — so subsequent turns in a multi-turn
	// conversation inherit the same workspace.
	//
	// Without this, EngineConfig.WorkspaceDir stays empty and the Engine
	// (engine.go:1330) never injects "workdir" into tool args. write_file
	// then resolves relative paths against the server's CWD and treats
	// absolute paths verbatim (e.g. "/tmp/x" writes to /tmp/x instead of
	// the session workspace), so files never land in
	// <cwd>/workspace/session-<id>/ as intended.
	workspaceDir := ""
	if sessionID != "" {
		if wsSess, err := db.QuerySessionByID(sessionID); err == nil {
			workspaceDir = wsSess.WorkspaceDir
		}
	}

	// Resolve the LLM Provider from mock/global configuration. The provider is
	// created once per agent loop and passed to the Engine so that the mock
	// switch (LLM_USE_MOCK / LLMRealCases / LLMMockEndpoints) is honored.
	// Errors are logged and fall back to nil; the Engine will then create a
	// default OpenAIProvider from Endpoint/APIKey/Model.
	provider, err := llm.CreateProviderFromConfig(cfg, cfg.LLMModel, caseID)
	if err != nil {
		log.Printf("[runAgentLoopWithTurn] Failed to create provider for case=%q (falling back to default): %v", caseID, err)
		provider = nil
	}

	// Build Harness policy gate with all safety rules:
	//   PathTraversalRule      — blocks ".." in file paths
	//   FileScopeRule          — restricts file ops to contract scope
	//   DangerousCommandRule   — blocks dangerous shell commands (Phase 5)
	//   ApprovalRule           — requires frontend approval for high-risk ops (Phase 5)
	//   TokenBudgetRule        — blocks tool calls when token budget exceeded
	//   ToolWhitelistRule      — only allows tools listed in the contract
	//   CostBudgetRule         — blocks tool calls when USD cost budget exceeded (M2)
	//
	// Rules are checked in order. The first rule that blocks stops the chain.
	tokenBudgetRule := &harness.TokenBudgetRule{}
	costBudgetRule := harness.NewCostBudgetRule()
	policyChain := harness.NewPolicyChain(
		&harness.PathTraversalRule{},
		&harness.FileScopeRule{},
		&harness.DangerousCommandRule{},
		harness.NewApprovalRule(approvalHandler),
		tokenBudgetRule,
		&harness.ToolWhitelistRule{},
		costBudgetRule,
	)
	policyGate := harness.NewPolicyGate(policyChain, contract)

	// Set up progress tracking for the task
	progressManager := harness.NewProgressManager()

	// Phase 6-D: Wire engine usage/cost callback to CostTracker, Repository
	// and MetricsCollector. This is the single integration point where the
	// cost-agnostic Engine hands off per-LLM-call usage data for persistence
	// and observability. We create one CostTracker per process (not per task)
	// so metrics accumulate globally.
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
		// If the Engine did not provide a profile (legacy fallback), resolve one
		// from the registry so pricing/tier fields are populated.
		if profile == nil || profile.Provider == "unknown" {
			if p := modelRegistry.Get(model); p != nil {
				profile = p
			}
		}
		record := costTracker.BuildRecordFromProfile(
			taskID, sessionID, projectID, agentID,
			0, // step_index is populated from usage aggregation perspective
			model, profile, usage,
		)
		// M2 修复：把本次调用成本累加进 CostBudgetRule，让 PolicyChain 在
		// 后续 tool call 中能根据累计成本阻断。此前 CostBudgetRule 已实现
		// 并有单测，但从未接入 main.go 的 PolicyChain，端到端不生效。
		if record.CostCents > 0 {
			costBudgetRule.SetCost(float64(record.CostCents) / 100.0)
		}
		// Best-effort persistence; failures are logged but don't break the task.
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

	engine := runtime.NewEngine(runtime.EngineConfig{
		AgentID:           agentID,
		SystemPrompt:      systemPrompt,
		Model:             cfg.LLMModel,
		Endpoint:          cfg.LLMEndpoint,
		APIKey:            cfg.LLMAPIKey,
		Provider:          provider, // mock or real provider resolved above
		CaseID:            caseID,   // hint for MockProvider script matching
		Temperature:       0.7,
		MaxTokens:         4096,
		MaxSteps:          contract.MaxSteps,
		Persistence:       persist,
		PolicyGate:        policyGate,
		Progress:          progressManager,
		Contract:          contract,
		SessionID:         sessionID,
		IsRoot:            isRoot,
		ParentTaskID:      parentTaskID,
		ApprovalHandler:   approvalHandler, // Phase 5: 审批处理器
		WorkingMemory:     workingMemory,   // Phase 6: 工作记忆注入
		AgentBus:          agentBus,        // Phase 5: 多Agent通信
		CheckpointManager: checkpointMgr,   // Phase 5: 崩溃恢复
		TurnIndex:         turnIndex,       // 当前轮次
		WorkspaceDir:       workspaceDir,    // Session-level workspace directory (write_file/run_shell CWD)
		OnLLMUsage:        onUsage,         // Phase 6-D: 成本/指标上报
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
	}, tools, &hubAdapter{hub: hub}, taskID)

	hub.SendEvent(event.NewEvent("task_started", taskID, agentID, 0, map[string]any{
		"task_id":    taskID,
		"agent_id":   agentID,
		"session_id": sessionID,
		"input":      userInput,
		"turn_index": turnIndex,
	}))

	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	// Apply the per-task timeout from the contract. TimeoutSeconds > 0 creates
	// a context with deadline; 0 (or negative) means unlimited — no deadline.
	if contract.TimeoutSeconds > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(contract.TimeoutSeconds)*time.Second)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	// Register the task's cancel function so WebSocket control messages can
	// cancel this task (root or child). Always remove it when the goroutine
	// exits to avoid leaking entries in cancelRegistry.
	cancelRegistry.Store(taskID, cancel)
	defer cancelRegistry.Delete(taskID)

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

// hubAdapter adapts ws.Hub to the runtime.EventBus interface
type hubAdapter struct {
	hub *ws.Hub
}

func (a *hubAdapter) SendEvent(evt event.Event) {
	a.hub.SendEvent(evt)
}

// streamTask emits a demo sequence of events simulating a multi-step agent task
func streamTask(hub *ws.Hub, taskID string) {
	agentID := "agent_test_001"

	sequence := []struct {
		eType   string
		data    map[string]any
		delayMs int
	}{
		{"agent_ready", nil, 100},
		{"task_started", map[string]any{"task_id": taskID}, 100},
		{"step_started", map[string]any{"step": 0, "type": "think"}, 100},
		{"llm_thinking", map[string]any{"content": "Starting analysis..."}, 200},
		{"llm_delta", map[string]any{"content": "I need to search for the latest "}, 50},
		{"llm_delta", map[string]any{"content": "AI developments in 2026. "}, 50},
		{"llm_delta", map[string]any{"content": "Let me use the web_search tool first."}, 100},
		{"llm_message_complete", nil, 200},
		{"step_complete", map[string]any{"step": 0}, 100},
		{"step_started", map[string]any{"step": 1, "type": "tool_call"}, 100},
		{"tool_call_started", map[string]any{"tool": "web_search", "args": map[string]any{"query": "AI developments 2026"}}, 100},
		{"tool_call_output", map[string]any{"result": "Found 5 relevant articles about AI agents, multimodal models, and safety research."}, 200},
		{"tool_call_complete", map[string]any{"tool": "web_search", "duration_ms": 1230}, 100},
		{"observation", map[string]any{"content": "Search returned 5 results about AI in 2026"}, 200},
		{"step_complete", map[string]any{"step": 1}, 100},
		{"step_started", map[string]any{"step": 2, "type": "think"}, 100},
		{"llm_delta", map[string]any{"content": "# AI in 2026\n\nBased on my research, "}, 50},
		{"llm_delta", map[string]any{"content": "here are the key developments:\n\n"}, 50},
		{"llm_delta", map[string]any{"content": "## 1. Multimodal AI Agents\n\n"}, 50},
		{"llm_delta", map[string]any{"content": "## 2. AI Safety frameworks\n\n"}, 50},
		{"llm_delta", map[string]any{"content": "## 3. Open Source Models\n\n"}, 50},
		{"llm_message_complete", nil, 200},
		{"step_complete", map[string]any{"step": 2}, 100},
		{"task_completed", map[string]any{"result": "Research completed successfully. Found 3 key insights."}, 100},
	}

	for _, item := range sequence {
		data := item.data
		if data == nil {
			data = make(map[string]any)
		}
		data["agent_id"] = agentID

		evt := event.NewEvent(item.eType, taskID, agentID, 0, data)
		hub.SendEvent(evt)

		if item.delayMs > 0 {
			time.Sleep(time.Duration(item.delayMs) * time.Millisecond)
		}
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// handleListCheckpoints returns a JSON array of all available checkpoint task IDs.
// GET /api/checkpoints
func handleListCheckpoints(w http.ResponseWriter, _ *http.Request, cm *runtime.CheckpointManager) {
	taskIDs, err := cm.List()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list checkpoints: %v", err), http.StatusInternalServerError)
		return
	}
	if taskIDs == nil {
		taskIDs = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"checkpoints": taskIDs,
	})
}

// handleRecoverCheckpoint resumes a task from a checkpoint.
// POST /api/checkpoints/recover
// Body: {"task_id": "task_xxx"}
func handleRecoverCheckpoint(w http.ResponseWriter, r *http.Request, hub *ws.Hub, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, approvalHandler harness.ApprovalHandler, agentBus runtime.AgentBus, cm *runtime.CheckpointManager) {
	var req struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	// Load the checkpoint from disk.
	cp, err := cm.Load(req.TaskID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load checkpoint: %v", err), http.StatusNotFound)
		return
	}

	// Build the engine config from the checkpoint's agent ID and restore state.
	// The system prompt is set to a generic recovery prompt since the original
	// prompt is in the conversation history.
	contract := harness.DefaultContract("resume")
	contract.MaxSteps = cp.StepIdx + 10 // allow 10 more steps

	// Recover case ID from checkpoint if available in the engine config (the
	// engine's own caseID is not persisted separately, so keyword fallback is
	// used when no case metadata is present).
	caseID := ""

	// Resolve the LLM Provider from mock/global configuration for recovery.
	// Errors are logged and fall back to nil; the Engine will create a default
	// OpenAIProvider from Endpoint/APIKey/Model.
	provider, err := llm.CreateProviderFromConfig(cfg, cfg.LLMModel, caseID)
	if err != nil {
		log.Printf("[handleRecoverCheckpoint] Failed to create provider for case=%q (falling back to default): %v", caseID, err)
		provider = nil
	}

	progressManager := harness.NewProgressManager()
	tokenBudgetRule := &harness.TokenBudgetRule{}
	costBudgetRule := harness.NewCostBudgetRule()
	policyChain := harness.NewPolicyChain(
		&harness.PathTraversalRule{},
		&harness.FileScopeRule{},
		&harness.DangerousCommandRule{},
		harness.NewApprovalRule(approvalHandler),
		tokenBudgetRule,
		&harness.ToolWhitelistRule{},
		costBudgetRule,
	)
	policyGate := harness.NewPolicyGate(policyChain, contract)

	cfg_ := runtime.EngineConfig{
		AgentID:           cp.AgentID,
		SystemPrompt:      "You are recovering from a checkpoint. Continue the task from where you left off.",
		Model:             cfg.LLMModel,
		Endpoint:          cfg.LLMEndpoint,
		APIKey:            cfg.LLMAPIKey,
		Provider:          provider, // mock or real provider resolved above
		CaseID:            caseID,   // hint for MockProvider script matching
		Temperature:       0.7,
		MaxTokens:         4096,
		MaxSteps:          contract.MaxSteps,
		Persistence:       persist,
		PolicyGate:        policyGate,
		Progress:          progressManager,
		Contract:          contract,
		ApprovalHandler:   approvalHandler,
		AgentBus:          agentBus,
		CheckpointManager: cm,
	}

	engine := runtime.RecoverFromCheckpoint(cp, cfg_, tools, &hubAdapter{hub: hub}, req.TaskID)

	// Emit recovery event for the frontend.
	hub.SendEvent(event.NewEvent("task_started", req.TaskID, cp.AgentID, cp.StepIdx, map[string]any{
		"task_id":      req.TaskID,
		"agent_id":     cp.AgentID,
		"recovered":    true,
		"step_idx":     cp.StepIdx,
		"total_tokens": cp.TotalTokens,
	}))

	// Run the engine in a goroutine. The input is empty because the conversation
	// history already has the last user message.
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
			log.Printf("[Recovery %s] Agent loop failed: %v", req.TaskID, err)
			failureReason := err.Error()
			if errors.Is(err, context.DeadlineExceeded) {
				failureReason = "task_timeout"
			}
			hub.SendEvent(event.NewEvent("task_failed", req.TaskID, cp.AgentID, 0, map[string]any{
				"reason": failureReason,
			}))
			return
		}

		// Delete the checkpoint after successful completion.
		cm.Delete(req.TaskID)
		log.Printf("[Recovery %s] Completed. Tokens: %d, Result: %s", req.TaskID, totalTokens, truncate(result, 100))
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"task_id":  req.TaskID,
		"agent_id": cp.AgentID,
		"step_idx": cp.StepIdx,
		"status":   "recovering",
	})
}

// seedDefaultAdminIfNeeded creates a default admin user and API key when no
// users exist in the database. The raw API key is printed once to the console.
func seedDefaultAdminIfNeeded(store auth.APIKeyStore) {
	sqliteStore, ok := store.(*auth.SqliteAPIKeyStore)
	if !ok {
		return
	}
	count, err := sqliteStore.CountUsers()
	if err != nil || count > 0 {
		return
	}
	admin, err := sqliteStore.AddUser("Admin", auth.RoleAdmin)
	if err != nil {
		log.Printf("Auth: failed to create default admin user: %v", err)
		return
	}
	_, rawKey, err := sqliteStore.Create(admin.ID, "default")
	if err != nil {
		log.Printf("Auth: failed to create default API key: %v", err)
		return
	}
	log.Printf("========================================")
	log.Printf("DEFAULT ADMIN API KEY: %s", rawKey)
	log.Printf("  (save this key — it will not be shown again)")
	log.Printf("========================================")
}

// handleSessionWorkspaceBrowse returns JSON metadata for the session's workspace
// directory, including the browse URL for frontend one-click navigation.
// GET /api/sessions/{id}/workspace-browse
func handleSessionWorkspaceBrowse(w http.ResponseWriter, r *http.Request, sessionID string) {
	sess, err := db.QuerySessionByID(sessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"session_id":    sessionID,
		"workspace_dir": sess.WorkspaceDir,
		"browse_url":    "/s/" + sessionID + "/",
	})
}

// fileExists checks if a path exists in the embedded filesystem.
// It strips the leading "/" because fs.FS paths are relative.
func fileExists(fsys fs.FS, path string) bool {
	// Strip leading slash for fs.FS compatibility
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	if path == "" {
		path = "index.html"
	}
	f, err := fsys.Open(path)
	if err != nil {
		return false
	}
	f.Close()
	return true
}
