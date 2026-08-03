package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ayanmw/multi-agent-platform/internal/auth"
	"github.com/ayanmw/multi-agent-platform/pkg/db"
)

// TestAgentAndSessionWriteRoutesRBAC 验证 N1-03 落地到 agents / sessions 路由的
// RBAC 守卫在真实 HTTP 路径下生效：
//
//   - agents 写（POST/PUT/DELETE）属特权类资源，仅 admin 可操作；
//     viewer 与 developer 均被拒绝 403（fail-closed）。
//   - session 删除（DELETE /api/sessions/{id}）属运营类资源，admin 与 developer
//     均可操作；viewer 被拒绝 403。
//
// 守卫闭包与 server.go registerRoutes 的实现逐字一致，通过注入角色的中间件驱动
// 真实的 auth.RequirePermissionFunc + 真实的 handler 方法，覆盖生产同一套语义。
func TestAgentAndSessionWriteRoutesRBAC(t *testing.T) {
	// 复用 agent config 测试的 DB 初始化（临时 SQLite + 自动清理）。
	setupAgentConfigTestDB(t)
	server := &appServer{}

	// 复刻 server.go 的守卫闭包：agents 写 + session 删除。
	mux := http.NewServeMux()
	mux.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && !auth.RequirePermissionFunc(w, r, auth.ResourceAgents, auth.ActionWrite) {
			return
		}
		server.handleAgents(w, r)
	})
	mux.HandleFunc("/api/agents/", func(w http.ResponseWriter, r *http.Request) {
		if !auth.RequirePermissionFunc(w, r, auth.ResourceAgents, auth.ActionWrite) {
			return
		}
		server.handleAgentByID(w, r)
	})
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		server.handleSessions(w, r)
	})
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && !auth.RequirePermissionFunc(w, r, auth.ResourceSessions, auth.ActionDelete) {
			return
		}
		server.handleSessionByID(w, r)
	})

	// 以 admin 角色预先建好一个 agent 与一个 session，供写/删断言复用。
	adminTS := httptest.NewServer(withRole(mux, auth.RoleAdmin))
	t.Cleanup(adminTS.Close)
	adminClient := adminTS.Client()

	agentID := rbacCreateAgent(t, adminClient, adminTS.URL)
	sessionID := rbacCreateSession(t, adminClient, adminTS.URL)

	// --- viewer 角色：agents 写与 session 删除都必须 403（最小权限 fail-closed）---
	viewerTS := httptest.NewServer(withRole(mux, auth.RoleViewer))
	t.Cleanup(viewerTS.Close)
	viewerClient := viewerTS.Client()

	rbacExpect403(t, viewerClient, http.MethodPut, viewerTS.URL+"/api/agents/"+agentID, bytes.NewReader([]byte(`{"name":"x"}`)))
	rbacExpect403(t, viewerClient, http.MethodDelete, viewerTS.URL+"/api/agents/"+agentID, nil)
	rbacExpect403(t, viewerClient, http.MethodDelete, viewerTS.URL+"/api/sessions/"+sessionID, nil)

	// --- developer 角色：agents 写仍 403（特权类仅 admin），session 删除放行（运营类）---
	devTS := httptest.NewServer(withRole(mux, auth.RoleUser))
	t.Cleanup(devTS.Close)
	devClient := devTS.Client()

	rbacExpect403(t, devClient, http.MethodPut, devTS.URL+"/api/agents/"+agentID, bytes.NewReader([]byte(`{"name":"x"}`)))
	rbacExpectStatus(t, devClient, http.MethodDelete, devTS.URL+"/api/sessions/"+sessionID, nil, http.StatusOK)

	// --- admin 角色：agents 写与 session 删除都应成功 ---
	rbacExpectStatus(t, adminClient, http.MethodPut, adminTS.URL+"/api/agents/"+agentID, bytes.NewReader([]byte(`{"name":"renamed"}`)), http.StatusOK)
	rbacExpectStatus(t, adminClient, http.MethodDelete, adminTS.URL+"/api/agents/"+agentID, nil, http.StatusOK)
	// 注意：上面的 developer 分支已把 session 删除，这里只验证 admin 对 agent 的写权限；
	// session 删除的 admin 放行已由 developer 分支间接证明（同一守卫、admin 也在允许集合）。
}

// withRole 返回注入固定角色的中间件，模拟认证后 context 中的 role。
func withRole(next http.Handler, role auth.Role) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(auth.WithRole(r.Context(), role)))
	})
}

// rbacCreateAgent 以当前 client 创建 agent，返回其 ID。
func rbacCreateAgent(t *testing.T, client *http.Client, base string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": "rbac-agent"})
	resp, err := client.Post(base+"/api/agents", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating agent, got %d: %s", resp.StatusCode, readBodyString(resp))
	}
	var rec db.AgentRecord
	if err := json.Unmarshal([]byte(readBodyString(resp)), &rec); err != nil {
		t.Fatalf("decode agent: %v", err)
	}
	if rec.ID == "" {
		t.Fatalf("empty agent ID from create response")
	}
	return rec.ID
}

// rbacCreateSession 以当前 client 创建 session，返回其 session_id。
func rbacCreateSession(t *testing.T, client *http.Client, base string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": "rbac-session", "user_input": "hi"})
	resp, err := client.Post(base+"/api/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating session, got %d: %s", resp.StatusCode, readBodyString(resp))
	}
	var rec struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(readBodyString(resp)), &rec); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if rec.SessionID == "" {
		t.Fatalf("empty session_id from create response")
	}
	return rec.SessionID
}

// rbacExpect403 断言给定请求返回 403。
func rbacExpect403(t *testing.T, client *http.Client, method, url string, body io.Reader) {
	t.Helper()
	resp, err := client.Do(rbacNewReq(t, method, url, body))
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("%s %s expected 403, got %d: %s", method, url, resp.StatusCode, readBodyString(resp))
	}
}

// rbacExpectStatus 断言给定请求返回指定状态码。
func rbacExpectStatus(t *testing.T, client *http.Client, method, url string, body io.Reader, want int) {
	t.Helper()
	resp, err := client.Do(rbacNewReq(t, method, url, body))
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s expected %d, got %d: %s", method, url, want, resp.StatusCode, readBodyString(resp))
	}
}

// rbacNewReq 构造带 JSON Content-Type 的请求（body 非空时）。
func rbacNewReq(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}
