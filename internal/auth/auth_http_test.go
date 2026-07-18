package auth

import (
	"net/http"
	"net/http/httptest"
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
