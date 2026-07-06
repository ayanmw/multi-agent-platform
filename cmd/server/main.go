package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/cases"
	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/orchestrator"
	"github.com/anmingwei/multi-agent-platform/internal/runtime"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/internal/version"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
	"github.com/anmingwei/multi-agent-platform/web"
)

func main() {
	port := flag.String("port", "8080", "HTTP server port")
	flag.Parse()

	// Load configuration from .env and environment
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	if *port != "8080" || cfg.ServerPort == "" {
		cfg.ServerPort = *port
	}

	// Initialize WebSocket hub
	hub := ws.NewHub()
	go hub.Run()

	// Initialize approval handler for Phase 5 Harness
	approvalHandler := harness.NewWebSocketApprovalHandler(hub)

	// Register control handler for client-side pause/resume/cancel and approval decisions
	hub.SetControlHandler(func(msg ws.ClientControlMsg) {
		log.Printf("[Control] Received: action=%s task=%s agent=%s approval_id=%s", msg.Action, msg.TaskID, msg.AgentID, msg.ApprovalID)
		// Phase 5: 路由审批决定到 ApprovalHandler
		switch msg.Action {
		case "approve":
			if msg.ApprovalID != "" {
				approvalHandler.HandleDecision(msg.ApprovalID, true)
			}
		case "deny":
			if msg.ApprovalID != "" {
				approvalHandler.HandleDecision(msg.ApprovalID, false)
			}
		}
		// TODO: Phase 4+ — implement actual engine control via context cancellation
	})

	// Initialize database
	if err := db.Init(cfg.DBPath); err != nil {
		log.Printf("Warning: DB init failed: %v (continuing without persistence)", err)
	} else {
		log.Println("Database initialized")
	}

	// Initialize Memory infrastructure — Heartbeat for episode consolidation
	memDB := &harness.SqliteMemoryDB{}
	heartbeat := harness.NewHeartbeat(memDB)
	go heartbeat.Start(context.Background())
	log.Println("Memory Heartbeat started (5min interval, adaptive)")

	// Initialize MemoryRecall for working memory injection on new tasks
	memRecall := harness.NewMemoryRecall(memDB)

	// Initialize persistence adapter
	persist := &DBPersistence{}

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
			Action       string                       `json:"action"`
			AgentID      string                       `json:"agent_id"`
			Input        string                       `json:"input"`
			SystemPrompt string                       `json:"system_prompt"`
			CaseType     string                       `json:"case_type"`
			MaxSteps     int                          `json:"max_steps"`
			SessionID    string                       `json:"session_id"`
			Agents       []orchestrator.AgentSpec     `json:"agents"`
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
				if wm, err := memRecall.BuildWorkingMemory("default", req.Input, 3); err == nil {
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

			// Build Working Memory from past experiences for this task
			workingMemory := ""
			if wm, err := memRecall.BuildWorkingMemory("default", req.Input, 3); err == nil {
				workingMemory = memRecall.FormatForSystemPrompt(wm)
			}

			taskID := "task_" + time.Now().Format("20060102150405")
			go runAgentLoop(hub, taskID, agentID, systemPrompt, req.Input, cfg, toolRegistry, persist, contract, req.SessionID, approvalHandler, workingMemory, agentBusAdapter, checkpointMgr)

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
		handleSessionByID(w, r)
	})

	// Health check
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	// Version API: returns the current version from version.txt
	http.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		json.NewEncoder(w).Encode(map[string]string{
			"version": version.Version,
		})
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
			Input     string                       `json:"input"`
			CaseType  string                       `json:"case_type"` // "multi_agent", "code_gen", or empty
			MaxSteps  int                          `json:"max_steps"` // override max steps for all agents
			SessionID string                       `json:"session_id"`
			Agents    []orchestrator.AgentSpec     `json:"agents"`    // direct agent specs (optional)
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

			// Build Working Memory for all agents in this orchestration
			if wm, err := memRecall.BuildWorkingMemory("default", req.Input, 3); err == nil {
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
		}

		// Emit orchestrator task started event
		hub.SendEvent(event.NewEvent("task_started", taskID, "orchestrator", 0, map[string]any{
			"task_id":    taskID,
			"session_id": sessionID,
			"input":      req.Input,
			"agent_ids":  agentIDs,
			"agent_count": len(specs),
		}))

		// Launch agents concurrently
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
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

// Memory API (Phase 6)
	// GET /api/memories?tier=consolidated&project=default — list memories
	// POST /api/memories/promote — manually trigger promotion
	memGateway := harness.NewPromotionGate(memDB)
	http.HandleFunc("/api/memories", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		handleListMemories(w, r)
	})
	http.HandleFunc("/api/memories/promote", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		handlePromoteMemories(w, r, memGateway)
		})
		// GET /api/memories/recall?task=xxx&project=default&max=3 — preview what would be recalled
		http.HandleFunc("/api/memories/recall", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			handleRecallPreview(w, r, memRecall)
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

	log.Printf("========================================")
	log.Printf("Multi-Agent Platform %s", version.Version)
	log.Printf("========================================")
	log.Printf("Server:      http://localhost:%s", cfg.ServerPort)
	log.Printf("WebSocket:   ws://localhost:%s/ws", cfg.ServerPort)
	log.Printf("API:         http://localhost:%s/api/tasks", cfg.ServerPort)
	log.Printf("Health:      http://localhost:%s/health", cfg.ServerPort)
	log.Printf("LLM:         %s (%s)", cfg.LLMEndpoint, cfg.LLMModel)
	log.Printf("Tools:       %d built-in", len(toolRegistry.List()))
	log.Printf("========================================")

	if err := http.ListenAndServe(":"+cfg.ServerPort, nil); err != nil {
		log.Fatal(err)
	}
}

// runAgentLoop executes the full ReAct loop for a chat request
func runAgentLoop(hub *ws.Hub, taskID, agentID, systemPrompt, userInput string, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, contract harness.TaskContract, sessionID string, approvalHandler harness.ApprovalHandler, workingMemory string, agentBus runtime.AgentBus, checkpointMgr *runtime.CheckpointManager) {
	// Persist task creation
	if persist != nil {
		persist.SaveTask(taskID, userInput, []string{agentID})
		persist.SaveTaskMeta(taskID, sessionID, "", true)
	}

	// Build Harness policy gate with all safety rules:
	//   PathTraversalRule      — blocks ".." in file paths
	//   FileScopeRule          — restricts file ops to contract scope
	//   DangerousCommandRule   — blocks dangerous shell commands (Phase 5)
	//   ApprovalRule           — requires frontend approval for high-risk ops (Phase 5)
	//   TokenBudgetRule        — blocks tool calls when token budget exceeded
	//   ToolWhitelistRule      — only allows tools listed in the contract
	//
	// Rules are checked in order. The first rule that blocks stops the chain.
	tokenBudgetRule := &harness.TokenBudgetRule{}
	policyChain := harness.NewPolicyChain(
		&harness.PathTraversalRule{},
		&harness.FileScopeRule{},
		&harness.DangerousCommandRule{},
		harness.NewApprovalRule(approvalHandler),
		tokenBudgetRule,
		&harness.ToolWhitelistRule{},
	)
	policyGate := harness.NewPolicyGate(policyChain, contract)

	// Set up progress tracking for the task
	progressManager := harness.NewProgressManager()

	engine := runtime.NewEngine(runtime.EngineConfig{
		AgentID:          agentID,
		SystemPrompt:     systemPrompt,
		Model:            cfg.LLMModel,
		Endpoint:         cfg.LLMEndpoint,
		APIKey:           cfg.LLMAPIKey,
		Temperature:      0.7,
		MaxTokens:        4096,
		MaxSteps:         contract.MaxSteps,
		Persistence:      persist,
		PolicyGate:       policyGate,
		Progress:         progressManager,
		Contract:         contract,
		SessionID:        sessionID,
		IsRoot:           true,
		ApprovalHandler:  approvalHandler,  // Phase 5: 审批处理器
		WorkingMemory:    workingMemory,     // Phase 6: 工作记忆注入
		AgentBus:         agentBus,          // Phase 5: 多Agent通信
		CheckpointManager: checkpointMgr,    // Phase 5: 崩溃恢复
	}, tools, &hubAdapter{hub: hub}, taskID)

	hub.SendEvent(event.NewEvent("task_started", taskID, agentID, 0, map[string]any{
		"task_id":    taskID,
		"agent_id":   agentID,
		"session_id": sessionID,
		"input":      userInput,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, totalTokens, err := engine.Run(ctx, userInput)
	if err != nil {
		log.Printf("[Task %s] Agent loop failed: %v", taskID, err)
		if sessionID != "" {
			db.UpdateSessionStatus(sessionID, deriveSessionStatus(sessionID))
		}
		if result == "" {
			hub.SendEvent(event.NewEvent("task_failed", taskID, agentID, 0, map[string]any{
				"reason": err.Error(),
			}))
		}
		return
	}

	if sessionID != "" {
		db.UpdateSessionStatus(sessionID, deriveSessionStatus(sessionID))
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
func handleListCheckpoints(w http.ResponseWriter, r *http.Request, cm *runtime.CheckpointManager) {
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

	progressManager := harness.NewProgressManager()
	tokenBudgetRule := &harness.TokenBudgetRule{}
	policyChain := harness.NewPolicyChain(
		&harness.PathTraversalRule{},
		&harness.FileScopeRule{},
		&harness.DangerousCommandRule{},
		harness.NewApprovalRule(approvalHandler),
		tokenBudgetRule,
		&harness.ToolWhitelistRule{},
	)
	policyGate := harness.NewPolicyGate(policyChain, contract)

	cfg_ := runtime.EngineConfig{
		AgentID:          cp.AgentID,
		SystemPrompt:     "You are recovering from a checkpoint. Continue the task from where you left off.",
		Model:            cfg.LLMModel,
		Endpoint:         cfg.LLMEndpoint,
		APIKey:           cfg.LLMAPIKey,
		Temperature:      0.7,
		MaxTokens:        4096,
		MaxSteps:         contract.MaxSteps,
		Persistence:      persist,
		PolicyGate:       policyGate,
		Progress:         progressManager,
		Contract:         contract,
		ApprovalHandler:  approvalHandler,
		AgentBus:         agentBus,
		CheckpointManager: cm,
	}

	engine := runtime.RecoverFromCheckpoint(cp, cfg_, tools, &hubAdapter{hub: hub}, req.TaskID)

	// Emit recovery event for the frontend.
	hub.SendEvent(event.NewEvent("task_started", req.TaskID, cp.AgentID, cp.StepIdx, map[string]any{
		"task_id":    req.TaskID,
		"agent_id":   cp.AgentID,
		"recovered":  true,
		"step_idx":   cp.StepIdx,
		"total_tokens": cp.TotalTokens,
	}))

	// Run the engine in a goroutine. The input is empty because the conversation
	// history already has the last user message.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		result, totalTokens, err := engine.Run(ctx, "")
		if err != nil {
			log.Printf("[Recovery %s] Agent loop failed: %v", req.TaskID, err)
			hub.SendEvent(event.NewEvent("task_failed", req.TaskID, cp.AgentID, 0, map[string]any{
				"reason": err.Error(),
			}))
			return
		}

		// Delete the checkpoint after successful completion.
		cm.Delete(req.TaskID)
		log.Printf("[Recovery %s] Completed. Tokens: %d, Result: %s", req.TaskID, totalTokens, truncate(result, 100))
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"task_id":    req.TaskID,
		"agent_id":   cp.AgentID,
		"step_idx":   cp.StepIdx,
		"status":     "recovering",
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