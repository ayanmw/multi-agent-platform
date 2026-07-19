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
const roleKey contextKey = "role"

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

// WithRole injects a role into the context. Used by the auth middleware after
// resolving the authenticated user's record. Downstream RBAC helpers read the
// role via RoleFromContext.
func WithRole(ctx context.Context, role Role) context.Context {
	return context.WithValue(ctx, roleKey, role)
}

// RoleFromContext extracts the RBAC role from the request context.
// Returns the Role and true if it has been injected; otherwise ("", false).
func RoleFromContext(ctx context.Context) (Role, bool) {
	v, ok := ctx.Value(roleKey).(Role)
	return v, ok
}

// DefaultPublicRoutes returns GET routes that should remain public even when
// REQUIRE_AUTH is enabled. These are typically health, metrics, and read-only
// discovery endpoints.
func DefaultPublicRoutes() []string {
	return []string{
		"GET /healthz",
		"GET /metrics",
		"GET /health",
	}
}

// DefaultAdminRoutes returns the list of METHOD + path prefix combinations
// that are restricted to admin users only. Format: "METHOD /path/prefix".
// These routes perform privileged writes: mutating platform configuration,
// managing users/agents/cases/tools, and installing marketplace packages.
func DefaultAdminRoutes() []string {
	return []string{
		"POST /api/agents",
		"PUT /api/agents/",
		"DELETE /api/agents/",
		"PUT /api/models/prices/",
		"POST /api/cases",
		"PUT /api/cases/",
		"DELETE /api/cases/",
		"POST /api/tools",
		"PUT /api/tools",
		"DELETE /api/tools",
		"POST /api/mcp/servers",
		"POST /api/mcp/servers/",
		"DELETE /api/mcp/servers/",
		"POST /api/mcp/markets/",
		"POST /api/auth/api-keys",
	}
}

// DefaultProtectedRoutes returns the list of METHOD + path prefix combinations
// that require authentication when REQUIRE_AUTH is enabled.
// Format: "METHOD /path/prefix" — e.g., "DELETE /api/sessions/"
func DefaultProtectedRoutes() []string {
	return []string{
		"GET /api/tasks",
		"GET /api/tasks/",
		"GET /api/agents",
		"GET /api/agents/",
		"GET /api/sessions",
		"GET /api/sessions/",
		"GET /api/projects",
		"GET /api/projects/",
		"GET /api/memories",
		"GET /api/memories/",
		"GET /api/cases",
		"GET /api/cases/",
		"GET /api/tools",
		"GET /api/tools/",
		"GET /api/models",
		"GET /api/models/",
		"GET /api/costs",
		"GET /api/costs/",
		"GET /api/auth/api-keys",
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

// NewAuthMiddleware creates an HTTP middleware that enforces authentication
// on protected routes. When requireAuth is false, all requests pass through with
// the fallback user ID injected, so auth management endpoints still work.
//
// Protected routes are matched by METHOD + path prefix. For example,
// "DELETE /api/sessions/" matches DELETE requests to /api/sessions/anything.
// Public routes listed in publicRoutes are always exempt from authentication.
func NewAuthMiddleware(store APIKeyStore, fallbackUserID string, requireAuth bool, protectedRoutes, publicRoutes []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// When auth is disabled, inject the fallback user and pass through.
		if !requireAuth {
			ctx := WithUserID(r.Context(), fallbackUserID)
			ctx = injectRole(ctx, store, fallbackUserID)
			// REQUIRE_AUTH 关闭时，如果 seed user 角色是 viewer，则仍然要阻止写操作；
			// admin/user 继续放行，保持原有行为。
			if role, ok := RoleFromContext(ctx); ok && role == RoleViewer && isViewerWriteOperation(r.Method) {
				writeJSONError(w, "forbidden: viewer role is read-only", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Public routes are always allowed without authentication.
		if isPublicRoute(r.Method, r.URL.Path, publicRoutes) {
			ctx := WithUserID(r.Context(), fallbackUserID)
			ctx = injectRole(ctx, store, fallbackUserID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Check if this route requires authentication.
		requiresAuth := isProtectedRoute(r.Method, r.URL.Path, protectedRoutes)
		if !requiresAuth {
			// Unprotected non-public route — inject fallback user.
			ctx := WithUserID(r.Context(), fallbackUserID)
			ctx = injectRole(ctx, store, fallbackUserID)
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
		ctx = injectRole(ctx, store, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// injectRole resolves the user's role and injects it into the context.
// If the store cannot resolve the user, RoleViewer is used as a safe default
// to avoid accidentally elevating a broken session to admin privileges.
// The caller must have already validated that userID is non-empty.
func injectRole(ctx context.Context, store APIKeyStore, userID string) context.Context {
	if store == nil {
		return WithRole(ctx, RoleViewer)
	}
	user, err := store.GetUser(userID)
	if err != nil || user == nil {
		return WithRole(ctx, RoleViewer)
	}
	return WithRole(ctx, user.Role)
}

// isViewerWriteOperation returns true for authenticated write requests made by
// a viewer role. Viewers are restricted to read-only access across the API.
func isViewerWriteOperation(method string) bool {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return false
	}
	return true
}

// RequireRole is an HTTP middleware guard that responds with 403 Forbidden when
// the context role is not in the allowed set. It should be placed after the
// auth middleware so that role has already been injected. If role is missing,
// it is treated as RoleViewer for safety.
func RequireRole(allowed ...Role) func(http.Handler) http.Handler {
	allowedSet := make(map[Role]struct{}, len(allowed))
	for _, r := range allowed {
		allowedSet[r] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, _ := RoleFromContext(r.Context())
			if role == "" {
				role = RoleViewer
			}
			if _, ok := allowedSet[role]; !ok {
				writeJSONError(w, "forbidden: insufficient role", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireRoleForRequest is a convenience helper for handlers registered with
// http.HandleFunc. It checks that the current request's role is in the allowed
// set and writes a 403 JSON response if not. Returns true when access is granted.
func requireRoleForRequest(w http.ResponseWriter, r *http.Request, allowed ...Role) bool {
	role, _ := RoleFromContext(r.Context())
	if role == "" {
		role = RoleViewer
	}
	for _, allowedRole := range allowed {
		if role == allowedRole {
			return true
		}
	}
	writeJSONError(w, "forbidden: insufficient role", http.StatusForbidden)
	return false
}

// RequireRoleFunc provides a direct handler-level check compatible with
// http.HandleFunc closures. It is equivalent to requireRoleForRequest but
// exported for use outside this package.
func RequireRoleFunc(w http.ResponseWriter, r *http.Request, allowed ...Role) bool {
	return requireRoleForRequest(w, r, allowed...)
}

// maskAPIKey returns a display-only form of a key prefix suitable for list
// responses. It keeps the first 4 and last 4 characters of the prefix and
// masks the middle with "****", so enumerating /api/auth/api-keys does not
// expose the real credential prefix.
func maskAPIKey(prefix string) string {
	if len(prefix) <= 8 {
		return prefix[:min(len(prefix), 4)] + "****"
	}
	return prefix[:4] + "****" + prefix[len(prefix)-4:]
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func isPublicRoute(method, path string, publicRoutes []string) bool {
	for _, route := range publicRoutes {
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
			"prefix":     maskAPIKey(k.Prefix),
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
