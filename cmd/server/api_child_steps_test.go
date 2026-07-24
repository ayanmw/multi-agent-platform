package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/db"
	_ "modernc.org/sqlite"
)

func setupTaskAPITestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test_task_api.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		db.DB = nil
	})
}

// TestHandleGetTaskChildSteps 验证 root task 详情返回的 child_tasks 每个元素
// 都附带自己的 steps 数组（Phase 7-H2 MA5）。
func TestHandleGetTaskChildSteps(t *testing.T) {
	setupTaskAPITestDB(t)

	sessionID := "sess_child_steps"
	rootID := "task_root"
	childID := "task_root_agent_worker"

	if err := db.InsertSession(db.SessionRecord{
		ID:        sessionID,
		UserInput: "root input",
		Status:    "running",
		Name:      "test session",
	}); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	now := time.Now()
	if err := db.InsertTask(db.TaskRecord{
		ID:           rootID,
		UserInput:    "root input",
		Status:       "completed",
		AgentIDs:     []string{"leader"},
		SessionID:    sessionID,
		ParentTaskID: "",
		IsRoot:       true,
		StartedAt:    now,
	}); err != nil {
		t.Fatalf("InsertTask root: %v", err)
	}
	if err := db.InsertTask(db.TaskRecord{
		ID:           childID,
		UserInput:    "child input",
		Status:       "completed",
		AgentIDs:     []string{"agent_worker"},
		SessionID:    sessionID,
		ParentTaskID: rootID,
		IsRoot:       false,
		StartedAt:    now,
	}); err != nil {
		t.Fatalf("InsertTask child: %v", err)
	}

	// 给子任务插入一个 step
	if err := db.InsertStep(db.StepRecord{
		ID:        "step_1",
		TaskID:    childID,
		AgentID:   "agent_worker",
		StepIndex: 0,
		Type:      "think",
		Status:    "completed",
		Content:   "child thinking",
	}); err != nil {
		t.Fatalf("InsertStep: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/tasks?id="+rootID, nil)
	rr := httptest.NewRecorder()
	s := &appServer{}
	s.handleGetTask(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var payload struct {
		Task       db.TaskRecord `json:"task"`
		Steps      []db.StepRecord `json:"steps"`
		ChildTasks []struct {
			db.TaskRecord
			Steps []db.StepRecord `json:"steps"`
		} `json:"child_tasks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(payload.ChildTasks) != 1 {
		t.Fatalf("expected 1 child task, got %d", len(payload.ChildTasks))
	}
	if payload.ChildTasks[0].ID != childID {
		t.Fatalf("expected child id %s, got %s", childID, payload.ChildTasks[0].ID)
	}
	if len(payload.ChildTasks[0].Steps) != 1 {
		t.Fatalf("expected 1 child step, got %d", len(payload.ChildTasks[0].Steps))
	}
	if payload.ChildTasks[0].Steps[0].AgentID != "agent_worker" {
		t.Fatalf("expected agent_worker step, got %s", payload.ChildTasks[0].Steps[0].AgentID)
	}
}
