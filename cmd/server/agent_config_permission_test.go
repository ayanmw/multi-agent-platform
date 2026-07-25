package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/pkg/db"

	_ "modernc.org/sqlite"
)

// setupAgentConfigTestDB 初始化一个带有完整 schema 的新 SQLite 数据库用于 agent config 测试。
func setupAgentConfigTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		db.DB = nil
	})
}

// TestAgentRequestConfigPersisted 验证创建/更新 agent 时 config 被持久化并返回。
func TestAgentRequestConfigPersisted(t *testing.T) {
	setupAgentConfigTestDB(t)
	s := &appServer{cfg: nil}

	createBody := map[string]any{
		"name": "Test Agent",
		"config": map[string]any{
			"permissions": map[string]any{
				"allow_network": true,
			},
		},
	}
	data, _ := json.Marshal(createBody)
	req := httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewReader(data))
	rr := httptest.NewRecorder()
	s.handleAgents(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var createResp db.AgentRecord
	if err := json.Unmarshal(rr.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createResp.Config == nil {
		t.Fatalf("expected config in create response, got nil")
	}
	perms, ok := createResp.Config["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("expected config.permissions map, got %T", createResp.Config["permissions"])
	}
	if perms["allow_network"] != true {
		t.Fatalf("expected allow_network=true, got %v", perms["allow_network"])
	}

	agentID := createResp.ID

	// Update with allow_shell true.
	updateBody := map[string]any{
		"name": "Updated Agent",
		"config": map[string]any{
			"permissions": map[string]any{
				"allow_network": true,
				"allow_shell":   true,
			},
		},
	}
	data, _ = json.Marshal(updateBody)
	req = httptest.NewRequest(http.MethodPut, "/api/agents/"+agentID, bytes.NewReader(data))
	rr = httptest.NewRecorder()
	s.handleAgentByID(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var updateResp db.AgentRecord
	if err := json.Unmarshal(rr.Body.Bytes(), &updateResp); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	updatedPerms, ok := updateResp.Config["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("expected updated config.permissions map")
	}
	if updatedPerms["allow_network"] != true || updatedPerms["allow_shell"] != true {
		t.Fatalf("expected allow_network=true and allow_shell=true, got %v", updatedPerms)
	}

	// GET should return same config.
	req = httptest.NewRequest(http.MethodGet, "/api/agents/"+agentID, nil)
	rr = httptest.NewRecorder()
	s.handleAgentByID(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 on get, got %d: %s", rr.Code, rr.Body.String())
	}
	var getResp db.AgentRecord
	if err := json.Unmarshal(rr.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	gotPerms, ok := getResp.Config["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("expected config.permissions in get response")
	}
	if gotPerms["allow_network"] != true || gotPerms["allow_shell"] != true {
		t.Fatalf("GET did not return persisted permissions: %v", gotPerms)
	}
}

// TestApplyAgentPermissionsMergesNetwork 验证 agent config 权限按 OR 合并到 contract。
func TestApplyAgentPermissionsMergesNetwork(t *testing.T) {
	contract := harness.TaskContract{
		Goal:        "test",
		Permissions: harness.TaskPermissions{AllowFileWrite: true},
	}
	cfg := map[string]any{
		"permissions": map[string]any{
			"allow_network":         true,
			"allow_file_write":      false, // must be ignored (OR semantics)
			"allow_shell_dangerous": true,
		},
	}

	applyAgentPermissions(&contract, cfg)

	if !contract.Permissions.AllowNetwork {
		t.Errorf("expected AllowNetwork=true after merge")
	}
	if !contract.Permissions.AllowShellDangerous {
		t.Errorf("expected AllowShellDangerous=true after merge")
	}
	if !contract.Permissions.AllowFileWrite {
		t.Errorf("expected AllowFileWrite to stay true (OR semantics)")
	}
}

// TestApplyAgentPermissionsIgnoresEmptyConfig 验证空 config 不改变 contract。
func TestApplyAgentPermissionsIgnoresEmptyConfig(t *testing.T) {
	contract := harness.TaskContract{
		Goal:        "test",
		Permissions: harness.TaskPermissions{AllowFileWrite: true},
	}
	applyAgentPermissions(&contract, map[string]any{})
	applyAgentPermissions(&contract, nil)

	if !contract.Permissions.AllowFileWrite {
		t.Errorf("expected AllowFileWrite to remain true")
	}
	if contract.Permissions.AllowNetwork {
		t.Errorf("expected AllowNetwork to remain false")
	}
}
