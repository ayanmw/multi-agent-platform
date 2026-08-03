package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ayanmw/multi-agent-platform/pkg/db"

	_ "modernc.org/sqlite"
)

// setupAuditTestDB 初始化测试 DB（含完整 schema + audit_records 表）。
func setupAuditTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "audit_test.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		db.DB = nil
	})
}

// TestMutationProducesAuditRecord 验证一次写操作后，审计轨迹中出现对应记录，
// 且 GET /api/audit 可查询。覆盖 N1-06 验收标准：resource mutation 落地审计
// （actor + timestamp + scope），并经持久化表/内存合并返回。
func TestMutationProducesAuditRecord(t *testing.T) {
	setupAuditTestDB(t)
	s := &appServer{cfg: nil}

	// 创建一个 agent —— 应产生 create_agent 审计（scope = agents/<id>）。
	body := map[string]any{"name": "Audit Test Agent"}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewReader(data))
	rr := httptest.NewRecorder()
	s.handleAgents(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create agent: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var created db.AgentRecord
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	agentID := created.ID

	// 更新该 agent —— 应产生 update_agent 审计。
	upd := map[string]any{"name": "Audit Test Agent (edited)"}
	data, _ = json.Marshal(upd)
	req = httptest.NewRequest(http.MethodPut, "/api/agents/"+agentID, bytes.NewReader(data))
	rr = httptest.NewRecorder()
	s.handleAgentByID(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("update agent: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// 删除该 agent —— 应产生 delete_agent 审计（含 Before 快照）。
	req = httptest.NewRequest(http.MethodDelete, "/api/agents/"+agentID, nil)
	rr = httptest.NewRecorder()
	s.handleAgentByID(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete agent: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// 查询 /api/audit，断言三条记录均存在且 scope(target) 正确。
	req = httptest.NewRequest(http.MethodGet, "/api/audit?limit=1000", nil)
	rr = httptest.NewRecorder()
	s.handleAudit(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("audit: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var records []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &records); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}

	want := map[string]bool{
		"create_agent":  false,
		"update_agent":  false,
		"delete_agent":  false,
	}
	for _, rec := range records {
		action, _ := rec["action"].(string)
		target, _ := rec["target"].(string)
		if action == "create_agent" && target == "agents/"+agentID {
			want["create_agent"] = true
		}
		if action == "update_agent" && target == "agents/"+agentID {
			want["update_agent"] = true
		}
		if action == "delete_agent" && target == "agents/"+agentID {
			want["delete_agent"] = true
		}
	}
	for action, ok := range want {
		if !ok {
			t.Errorf("expected %s audit record for agents/%s", action, agentID)
		}
	}
}
