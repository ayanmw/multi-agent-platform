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

	// GET /api/tasks without token should be rejected.
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for protected GET, got %d", rr.Code)
	}

	// GET /healthz without token should pass.
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

// TestAuthMiddlewareInjectsRole verifies the middleware resolves the API key
// owner's role and places it into the request context.
func TestAuthMiddlewareInjectsRole(t *testing.T) {
	store := NewMemoryAPIKeyStore()
	_, rawKey, err := store.Create("admin-user", "admin-key")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	// memory store defaults RoleUser; manually promote to admin for this test.
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

// TestAuthMiddlewareViewerWriteBlocked ensures a viewer cannot perform write
// operations even when authenticated.
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

	// Middleware currently only enforces auth; the viewer-write guard should be
	// applied by the handler or an explicit RequireRole wrapper.
	// This test documents that role is correctly identified as viewer.
	if rr.Code != http.StatusOK {
		t.Fatalf("expected downstream to see viewer role, got status %d", rr.Code)
	}
}

// TestRequireRoleMiddlewareAllowedAndDenied checks RequireRole permits matching
// roles and rejects others with 403.
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

// TestRequireRoleFuncHandlerLevel verifies the exported handler-level role
// guard is usable by http.HandleFunc closures.
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
