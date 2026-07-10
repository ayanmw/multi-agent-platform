package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/google/uuid"
)

// RegisterMockRoutes registers the mock script management API endpoints on mux.
// It exposes CRUD operations for mock scripts and a reset endpoint that reloads
// the built-in scripts. These endpoints are intended for development and testing
// so operators can inspect and modify mock LLM behavior without restarting the
// server.
func RegisterMockRoutes(mux *http.ServeMux, store llm.MockScriptStore, builtinScripts []llm.MockScript) {
	// GET /api/mock/scripts — list all scripts
	// POST /api/mock/scripts — create or update a script
	mux.HandleFunc("/api/mock/scripts", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			listScripts(w, r, store)
		case http.MethodPost:
			saveScript(w, r, store)
		default:
			http.Error(w, "GET, POST only", http.StatusMethodNotAllowed)
		}
	})

	// GET /api/mock/scripts/{id} — get a script
	// DELETE /api/mock/scripts/{id} — delete a script
	mux.HandleFunc("/api/mock/scripts/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			http.Error(w, "GET, DELETE only", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/mock/scripts/")
		if id == "" {
			http.Error(w, "script id required", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodGet:
			getScript(w, r, store, id)
		case http.MethodDelete:
			deleteScript(w, r, store, id)
		}
	})

	// POST /api/mock/reset — clear store and reload built-in scripts
	mux.HandleFunc("/api/mock/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		resetScripts(w, r, store, builtinScripts)
	})
}

// listScripts returns all mock scripts in the store.
// GET /api/mock/scripts
func listScripts(w http.ResponseWriter, _ *http.Request, store llm.MockScriptStore) {
	scripts, err := store.List()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if scripts == nil {
		scripts = []llm.MockScript{}
	}
	respondJSON(w, http.StatusOK, map[string]any{"scripts": scripts})
}

// getScript returns a single mock script by ID.
// GET /api/mock/scripts/{id}
func getScript(w http.ResponseWriter, _ *http.Request, store llm.MockScriptStore, id string) {
	script, err := store.Get(id)
	if err != nil {
		respondJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"script": script})
}

// saveScript creates or updates a mock script. If the request body omits an ID,
// a new UUID is generated and returned in the saved script.
// POST /api/mock/scripts
func saveScript(w http.ResponseWriter, r *http.Request, store llm.MockScriptStore) {
	var script llm.MockScript
	if err := json.NewDecoder(r.Body).Decode(&script); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if script.ID == "" {
		script.ID = uuid.New().String()
	}
	saved, err := store.Save(script)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"script": saved})
}

// deleteScript removes a mock script from the store.
// DELETE /api/mock/scripts/{id}
func deleteScript(w http.ResponseWriter, _ *http.Request, store llm.MockScriptStore, id string) {
	if err := store.Delete(id); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// resetScripts clears the store and reloads the built-in scripts.
// POST /api/mock/reset
func resetScripts(w http.ResponseWriter, _ *http.Request, store llm.MockScriptStore, builtinScripts []llm.MockScript) {
	if err := store.LoadBuiltin(builtinScripts); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"reset": true})
}

// respondJSON encodes v as JSON and writes it to w with the given status code.
func respondJSON(w http.ResponseWriter, status int, v map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
