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

	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/internal/runtime"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
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

	// Register control handler for client-side pause/resume/cancel
	hub.SetControlHandler(func(msg ws.ClientControlMsg) {
		log.Printf("[Control] Received: action=%s task=%s agent=%s", msg.Action, msg.TaskID, msg.AgentID)
		// TODO: Phase 4+ — implement actual engine control via context cancellation
		// For now, we just log the control message
	})

	// Initialize database
	if err := db.Init(cfg.DBPath); err != nil {
		log.Printf("Warning: DB init failed: %v (continuing without persistence)", err)
	} else {
		log.Println("Database initialized")
	}

	// Initialize persistence adapter
	persist := &DBPersistence{}

	// Initialize tool registry with built-in tools
	toolRegistry := tool.NewRegistry()
	tool.RegisterBuiltins(toolRegistry)
	log.Printf("Registered %d built-in tools", len(toolRegistry.List()))

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
			Action       string `json:"action"`
			AgentID      string `json:"agent_id"`
			Input        string `json:"input"`
			SystemPrompt string `json:"system_prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch req.Action {
		case "stream-demo":
			taskID := "task_" + time.Now().Format("20060102150405")
			go streamTask(hub, taskID)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"task_id": taskID})

		case "chat":
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

			taskID := "task_" + time.Now().Format("20060102150405")
			go runAgentLoop(hub, taskID, agentID, systemPrompt, req.Input, cfg, toolRegistry, persist)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"task_id":  taskID,
				"agent_id": agentID,
				"action":   "chat",
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

	// Health check
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
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
	log.Printf("Multi-Agent Platform v0.3 (Phase 2)")
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
func runAgentLoop(hub *ws.Hub, taskID, agentID, systemPrompt, userInput string, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence) {
	// Persist task creation
	if persist != nil {
		persist.SaveTask(taskID, userInput, []string{agentID})
	}

	engine := runtime.NewEngine(runtime.EngineConfig{
		AgentID:      agentID,
		SystemPrompt: systemPrompt,
		Model:        cfg.LLMModel,
		Endpoint:     cfg.LLMEndpoint,
		APIKey:       cfg.LLMAPIKey,
		Temperature:  0.7,
		MaxTokens:    4096,
		MaxSteps:     10,
		Persistence:  persist,
	}, tools, &hubAdapter{hub: hub}, taskID)

	hub.SendEvent(event.NewEvent("task_started", taskID, agentID, 0, map[string]any{
		"task_id":  taskID,
		"agent_id": agentID,
		"input":    userInput,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, totalTokens, err := engine.Run(ctx, userInput)
	if err != nil {
		log.Printf("[Task %s] Agent loop failed: %v", taskID, err)
		if result == "" {
			hub.SendEvent(event.NewEvent("task_failed", taskID, agentID, 0, map[string]any{
				"reason": err.Error(),
			}))
		}
		return
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