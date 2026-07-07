// Package cost provides HTTP handlers for cost tracking queries.
//
// These handlers expose cost data via simple HTTP endpoints so the frontend
// and other services can query aggregated cost reports without direct access
// to the CostTracker instance.
//
// The handlers are deliberately simple — they accept query parameters for
// the dimension (task, session, project) and return a JSON-encoded CostReport.
package cost

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// HandleCostTask returns an HTTP handler that queries the cost report for a
// specific task.
// Query parameter: task_id (required)
func (ct *CostTracker) HandleCostTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	taskID := r.URL.Query().Get("task_id")
	if taskID == "" {
		http.Error(w, "missing task_id parameter", http.StatusBadRequest)
		return
	}

	report, err := ct.TaskCost(taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	respondJSON(w, report)
}

// HandleCostSession returns an HTTP handler that queries the cost report for a
// specific session.
// Query parameter: session_id (required)
func (ct *CostTracker) HandleCostSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "missing session_id parameter", http.StatusBadRequest)
		return
	}

	report, err := ct.SessionCost(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	respondJSON(w, report)
}

// HandleCostProject returns an HTTP handler that queries the cost report for a
// specific project.
// Query parameter: project_id (required)
func (ct *CostTracker) HandleCostProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		http.Error(w, "missing project_id parameter", http.StatusBadRequest)
		return
	}

	report, err := ct.ProjectCost(projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	respondJSON(w, report)
}

// HandleCostDaily returns an HTTP handler that queries the cost report for the
// last N days.
// Query parameter: days (optional, default 7)
func (ct *CostTracker) HandleCostDaily(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	daysStr := r.URL.Query().Get("days")
	days := 7 // default to 7 days
	if daysStr != "" {
		parsed, err := strconv.Atoi(daysStr)
		if err != nil || parsed < 0 {
			http.Error(w, "invalid days parameter (must be non-negative integer)", http.StatusBadRequest)
			return
		}
		days = parsed
	}

	report, err := ct.DailyReport(days)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	respondJSON(w, report)
}

// respondJSON writes the given value as a JSON response with 200 status.
func respondJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
