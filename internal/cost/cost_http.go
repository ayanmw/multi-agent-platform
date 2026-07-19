// Package cost 提供 cost 跟踪查询的 HTTP handler。
//
// 这些 handler 通过简单的 HTTP 端点暴露 cost 数据，使前端和其它服务
// 可以在无需直接访问 CostTracker 实例的情况下查询聚合的 cost 报告。
//
// 这些 handler 被刻意保持简单——它们接收查询参数指定维度（task、session、project），
// 并返回 JSON 编码的 CostReport。
package cost

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// HandleCostTask 返回一个 HTTP handler，用于查询特定 task 的 cost 报告。
// Query parameter: task_id（必填）
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

// HandleCostSession 返回一个 HTTP handler，用于查询特定 session 的 cost 报告。
// Query parameter: session_id（必填）
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

// HandleCostProject 返回一个 HTTP handler，用于查询特定 project 的 cost 报告。
// Query parameter: project_id（必填）
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

// HandleCostDaily 返回一个 HTTP handler，用于查询最近 N 天的 cost 报告。
// Query parameter: days（可选，默认 7）
func (ct *CostTracker) HandleCostDaily(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	daysStr := r.URL.Query().Get("days")
	days := 7 // 默认 7 天
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

// respondJSON 以 200 状态码将给定值作为 JSON 响应写入。
func respondJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
