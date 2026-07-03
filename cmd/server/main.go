package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/agent"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

func main() {
	port := flag.String("port", "8080", "HTTP server port")
	flag.Parse()

	hub := ws.NewHub()
	go hub.Run()

	// Register hardcoded test agent
	_ = &agent.Agent{
		ID:           "agent_test_001",
		Name:         "Test Agent",
		SystemPrompt: "You are a test agent",
		Model:        "deepseek-v4-flash",
		Endpoint:     "https://aicoding.dobest.com/v1",
	}

	// WebSocket endpoint
	http.HandleFunc("/ws", ws.ServeWS(hub))

	// API: start demo stream task
	http.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Action != "stream-demo" {
			http.Error(w, "unknown action", http.StatusBadRequest)
			return
		}

		taskID := "task_" + time.Now().Format("20060102150405")
		go streamTask(hub, taskID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"task_id": taskID})
	})

	// Health check
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	// Serve Vue static files in dev, or dist in production
	webDir := "./web/dist"
	if _, err := os.Stat(webDir); err == nil {
		http.Handle("/", http.FileServer(http.Dir(webDir)))
	}

	log.Printf("Server starting on :%s", *port)
	log.Printf("WebSocket endpoint: ws://localhost:%s/ws", *port)
	log.Printf("API endpoint: http://localhost:%s/api/tasks", *port)
	log.Printf("Health check: http://localhost:%s/health", *port)

	if err := http.ListenAndServe(":"+*port, nil); err != nil {
		log.Fatal(err)
	}
}

// streamTask emits a demo sequence of events simulating a multi-step agent task
func streamTask(hub *ws.Hub, taskID string) {
	agentID := "agent_test_001"

	sequence := []struct {
		eType  string
		data   map[string]any
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
