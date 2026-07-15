// auth_http.go — HTTP middleware, context helpers, and route handlers for API
// key authentication.
//
// # Auth Flow
//
//  1. Extract Bearer token from Authorization header
//  2. Verify via APIKeyStore.Verify (prefix pre-filter + bcrypt)
//  3. Inject user ID into request context via WithUserID
//  4. Downstream handlers read user ID via UserIDFromContext
//
// When REQUIRE_AUTH is false, the middleware still injects the seed user ID so
// that API key management endpoints can function without an Authorization header.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

// contextKey is a private type for context value keys to avoid collisions.
type contextKey string

const userIDKey contextKey = "user_id"

// WithUserID injects a user ID into the context.
// Used by the auth middleware after successful verification.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// UserIDFromContext extracts the user ID from the request context.
// Returns the user ID and true if present, otherwise ("", false).
func UserIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(userIDKey).(string)
	return v, ok
}

// DefaultProtectedRoutes returns the list of METHOD + path prefix combinations
// that require authentication when REQUIRE_AUTH is enabled.
// Format: "METHOD /path/prefix" — e.g., "DELETE /api/sessions/"
func DefaultProtectedRoutes() []string {
	return []string{
		"POST /api/tasks",
		"DELETE /api/tasks/",
		"POST /api/agents",
		"PUT /api/agents/",
		"DELETE /api/agents/",
		"POST /api/sessions",
		"POST /api/sessions/",
		"DELETE /api/sessions/",
		"POST /api/projects",
		"PUT /api/projects/",
		"DELETE /api/projects/",
		"POST /api/multi-agent",
		"POST /api/checkpoints/",
		"DELETE /api/memories/",
		"PUT /api/memories/",
		"POST /api/memories/promote",
		"POST /api/tools",
		"PUT /api/tools",
		"DELETE /api/tools",
		"POST /api/auth/api-keys",
		"DELETE /api/auth/api-keys/",
		// Model price edits are runtime-only writes (overwrite ModelRegistry entry),
		// so they require a Bearer token when REQUIRE_AUTH is enabled. GET is public-read.
		"PUT /api/models/prices/",
		// Case mutations are writes; GET remains public-read.
		"POST /api/cases",
		"PUT /api/cases/",
		"DELETE /api/cases/",
	}
}

// on protected routes. When requireAuth is false, all requests pass through with
// the fallback user ID injected, so auth management endpoints still work.
//
// Protected routes are matched by METHOD + path prefix. For example,
// "DELETE /api/sessions/" matches DELETE requests to /api/sessions/anything.
// GET requests are treated as public-read and are not protected.
func NewAuthMiddleware(store APIKeyStore, fallbackUserID string, requireAuth bool, protectedRoutes []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// When auth is disabled, inject the fallback user and pass through.
		if !requireAuth {
			ctx := WithUserID(r.Context(), fallbackUserID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Check if this route requires authentication.
		requiresAuth := isProtectedRoute(r.Method, r.URL.Path, protectedRoutes)

		if !requiresAuth || r.Method == http.MethodGet {
			// Public route — inject fallback user so /api/auth/api-keys still works.
			ctx := WithUserID(r.Context(), fallbackUserID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Protected route — verify the API key.
		userID, err := authenticateRequest(r, store)
		if err != nil {
			log.Printf("[Auth] authentication failed: %v (path=%s, method=%s)", err, r.URL.Path, r.Method)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		ctx := WithUserID(r.Context(), userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// isProtectedRoute checks if the given method+path matches any protected route.
// A match occurs when the method equals the route's method AND the path starts
// with the route's path prefix.
func isProtectedRoute(method, path string, protectedRoutes []string) bool {
	for _, route := range protectedRoutes {
		parts := strings.SplitN(route, " ", 2)
		if len(parts) != 2 {
			continue
		}
		routeMethod, routePrefix := parts[0], parts[1]
		if method == routeMethod && strings.HasPrefix(path, routePrefix) {
			return true
		}
	}
	return false
}

// authenticateRequest extracts the Bearer token from the Authorization header,
// verifies it against the store, and returns the associated user ID.
func authenticateRequest(r *http.Request, store APIKeyStore) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", ErrInvalidKey
	}

	// Expect "Bearer <key>"
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", ErrInvalidKey
	}

	rawKey := strings.TrimPrefix(authHeader, "Bearer ")
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return "", ErrInvalidKey
	}

	key, err := store.Verify(rawKey)
	if err != nil {
		return "", err
	}
	if key.IsRevoked() {
		return "", ErrRevokedKey
	}

	return key.UserID, nil
}

// currentUserID extracts the user ID from the request context.
// It returns the configured seed user ID when auth is disabled.
func (a *AuthAPI) currentUserID(r *http.Request) string {
	if userID, ok := UserIDFromContext(r.Context()); ok && userID != "" {
		return userID
	}
	return a.seedUserID
}

// SetSeedUserIDFromStore resolves and sets the seed user ID from the store.
// Used at startup to establish a stable fallback user when REQUIRE_AUTH is off.
func (a *AuthAPI) SetSeedUserIDFromStore(store APIKeyStore) {
	if sqliteStore, ok := store.(*SqliteAPIKeyStore); ok {
		if u, err := sqliteStore.GetFirstUser(); err == nil && u != nil {
			a.seedUserID = u.ID
		}
	}
}

// writeJSONError writes a JSON error response with the given status code.
func writeJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// handleAPIKeys handles GET (list) and POST (create) for /api/auth/api-keys
func (a *AuthAPI) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.handleListAPIKeys(w, r)
	case http.MethodPost:
		a.handleCreateAPIKey(w, r)
	default:
		writeJSONError(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

// handleAPIKeyByID handles DELETE (revoke) for /api/auth/api-keys/{id}
func (a *AuthAPI) handleAPIKeyByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/auth/api-keys/")
	if id == "" || strings.Contains(id, "/") {
		writeJSONError(w, "key ID required", http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodDelete {
		writeJSONError(w, "DELETE only", http.StatusMethodNotAllowed)
		return
	}

	userID := a.currentUserID(r)
	if userID == "" {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Verify the key belongs to the current user before revoking.
	keys, err := a.store.List(userID)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	owned := false
	for _, k := range keys {
		if k.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		writeJSONError(w, "key not found", http.StatusNotFound)
		return
	}

	if err := a.store.Revoke(id); err != nil {
		if errors.Is(err, errors.New("api key not found")) || strings.Contains(err.Error(), "not found") {
			writeJSONError(w, "key not found", http.StatusNotFound)
			return
		}
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":      id,
		"message": "API key revoked successfully",
	})
}

// handleCreateAPIKey creates a new API key for the authenticated user.
func (a *AuthAPI) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	userID := a.currentUserID(r)
	if userID == "" {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = "unnamed"
	}

	key, rawKey, err := a.store.Create(userID, req.Name)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"id":         key.ID,
		"user_id":    key.UserID,
		"name":       key.Name,
		"prefix":     key.Prefix,
		"key":        rawKey,
		"created_at": key.CreatedAt.Format(time.RFC3339),
	})
}

// handleListAPIKeys returns the current user's API keys without exposing hashes.
func (a *AuthAPI) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	userID := a.currentUserID(r)
	if userID == "" {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	keys, err := a.store.List(userID)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if keys == nil {
		keys = []APIKey{}
	}

	items := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		item := map[string]any{
			"id":         k.ID,
			"user_id":    k.UserID,
			"name":       k.Name,
			"prefix":     k.Prefix,
			"created_at": formatTime(k.CreatedAt),
		}
		if k.LastUsedAt != nil {
			item["last_used_at"] = formatTime(*k.LastUsedAt)
		}
		if k.RevokedAt != nil {
			item["revoked_at"] = formatTime(*k.RevokedAt)
		}
		items = append(items, item)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items": items,
	})
}

// formatTime renders a time as RFC3339. Used to keep JSON responses uniform.
func formatTime(t time.Time) string {
	return t.Format(time.RFC3339)
}
