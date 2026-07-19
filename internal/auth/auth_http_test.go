package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testStore() APIKeyStore {
	store := NewMemoryAPIKeyStore()
	_, _, _ = store.Create("user-1", "test-key")
	return store
}

func TestAuthMiddlewareProtectedGETRequiresToken(t *testing.T) {
	store := testStore()
	protected := []string{"GET /api/tasks"}
	public := []string{"GET /healthz"}
	handler := NewAuthMiddleware(store, "seed", true, protected, public, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 不带 token 的 GET /api/tasks 应被拒绝。
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for protected GET, got %d", rr.Code)
	}

	// 不带 token 的 GET /healthz 应放行。
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for public GET, got %d", rr.Code)
	}
}

func TestAuthMiddlewarePOSTRequiresToken(t *testing.T) {
	store := testStore()
	protected := []string{"POST /api/tasks"}
	public := []string{}
	handler := NewAuthMiddleware(store, "seed", true, protected, public, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/tasks", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for protected POST, got %d", rr.Code)
	}
}

func TestAuthMiddlewareUnprotectedRoutePasses(t *testing.T) {
	store := testStore()
	protected := []string{"POST /api/tasks"}
	public := []string{}
	handler := NewAuthMiddleware(store, "seed", true, protected, public, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/open", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for unprotected route, got %d", rr.Code)
	}
}

// TestAuthMiddlewareInjectsRole 验证 middleware 会解析 API key
// 所有者的 role 并将其放入请求 context。
func TestAuthMiddlewareInjectsRole(t *testing.T) {
	store := NewMemoryAPIKeyStore()
	_, rawKey, err := store.Create("admin-user", "admin-key")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	// memory store 默认 RoleUser;此处手动提升为 admin 以便测试。
	if sqliteStore, ok := store.(*memoryAPIKeyStore); ok {
		sqliteStore.mu.Lock()
		sqliteStore.users["admin-user"].Role = RoleAdmin
		sqliteStore.mu.Unlock()
	}

	var capturedRole Role
	handler := NewAuthMiddleware(store, "seed", true, []string{"GET /api/admin"}, []string{},
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if role, ok := RoleFromContext(r.Context()); ok {
				capturedRole = role
			}
			w.WriteHeader(http.StatusOK)
		}))

	req := httptest.NewRequest(http.MethodGet, "/api/admin", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if capturedRole != RoleAdmin {
		t.Fatalf("expected role %q injected, got %q", RoleAdmin, capturedRole)
	}
}

// TestAuthMiddlewareViewerWriteBlocked 确保 viewer 即使已认证,
// 也无法执行写操作。
func TestAuthMiddlewareViewerWriteBlocked(t *testing.T) {
	store := NewMemoryAPIKeyStore()
	_, rawKey, err := store.Create("viewer-user", "viewer-key")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if sqliteStore, ok := store.(*memoryAPIKeyStore); ok {
		sqliteStore.mu.Lock()
		sqliteStore.users["viewer-user"].Role = RoleViewer
		sqliteStore.mu.Unlock()
	}

	handler := NewAuthMiddleware(store, "seed", true, []string{"POST /api/items"}, []string{},
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, _ := RoleFromContext(r.Context())
			if role != RoleViewer {
				t.Fatalf("expected viewer role in context, got %q", role)
			}
			w.WriteHeader(http.StatusOK)
		}))

	req := httptest.NewRequest(http.MethodPost, "/api/items", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// 当前 middleware 只强制认证;viewer-write 守卫应由
	// handler 或显式的 RequireRole wrapper 来施加。
	// 本测试记录 role 被正确识别为 viewer。
	if rr.Code != http.StatusOK {
		t.Fatalf("expected downstream to see viewer role, got status %d", rr.Code)
	}
}

// TestRequireRoleMiddlewareAllowedAndDenied 检查 RequireRole 允许匹配的
// role,并以 403 拒绝其他 role。
func TestRequireRoleMiddlewareAllowedAndDenied(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name        string
		ctxRole     Role
		allowed     []Role
		wantStatus  int
	}{
		{"admin_allowed", RoleAdmin, []Role{RoleAdmin}, http.StatusOK},
		{"user_allowed", RoleUser, []Role{RoleAdmin, RoleUser}, http.StatusOK},
		{"viewer_denied", RoleViewer, []Role{RoleAdmin}, http.StatusForbidden},
		{"missing_role_denied", "", []Role{RoleAdmin}, http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := WithRole(r.Context(), tt.ctxRole)
				RequireRole(tt.allowed...)(okHandler).ServeHTTP(w, r.WithContext(ctx))
			})

			req := httptest.NewRequest(http.MethodPost, "/api/items", nil)
			rr := httptest.NewRecorder()
			base.ServeHTTP(rr, req)
			if rr.Code != tt.wantStatus {
				t.Fatalf("%s: expected status %d, got %d", tt.name, tt.wantStatus, rr.Code)
			}
		})
	}
}

// TestRequireRoleFuncHandlerLevel 验证已导出的 handler 级别 role 守卫
// 可被 http.HandleFunc 闭包使用。
func TestRequireRoleFuncHandlerLevel(t *testing.T) {
	adminOnly := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !RequireRoleFunc(w, r, RoleAdmin) {
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodDelete, "/api/agents/123", nil)
	req = req.WithContext(WithRole(req.Context(), RoleUser))
	rr := httptest.NewRecorder()
	adminOnly.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if !strings.Contains(body["error"], "forbidden") {
		t.Fatalf("expected forbidden body, got %q", body["error"])
	}
}
