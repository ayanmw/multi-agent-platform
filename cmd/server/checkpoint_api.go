// checkpoint_api.go — Checkpoint 恢复与列表的 REST API。
//
// Phase 8-B: 把原来 main.go 中的 handleRecoverCheckpoint / handleListCheckpoints
// 迁移到本文件，并改为 appServer 方法，统一走 AgentRunner.Recover。
package main

import (
	"encoding/json"
	"net/http"
)

// recoverRequest 是 POST /api/checkpoints/recover 的请求体。
type recoverRequest struct {
	TaskID string `json:"task_id"`
}

// handleListCheckpoints 返回所有可用 checkpoint task ID 的 JSON 数组。
// GET /api/checkpoints
func (s *appServer) handleListCheckpoints(w http.ResponseWriter, _ *http.Request) {
	cm := s.checkpointMgr
	if cm == nil {
		http.Error(w, "checkpoint manager not available", http.StatusServiceUnavailable)
		return
	}
	taskIDs, err := cm.List()
	if err != nil {
		http.Error(w, "Failed to list checkpoints: "+err.Error(), http.StatusInternalServerError)
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

// handleRecoverCheckpoint 从 checkpoint 恢复任务。
// POST /api/checkpoints/recover
// Body: {"task_id": "task_xxx"}
func (s *appServer) handleRecoverCheckpoint(w http.ResponseWriter, r *http.Request) {
	var req recoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	agentID, err := s.newRunner().Recover(r.Context(), RecoverSpec{TaskID: req.TaskID})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"task_id":  req.TaskID,
		"agent_id": agentID,
		"status":   "recovering",
	})
}
