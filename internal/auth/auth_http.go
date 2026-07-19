// auth_http.go — API key 认证相关的 HTTP middleware、context 辅助函数与路由 handler。
//
// # Auth 流程
//
//  1. 从 Authorization header 中提取 Bearer token
//  2. 通过 APIKeyStore.Verify 校验(prefix 预过滤 + bcrypt)
//  3. 通过 WithUserID 将用户 ID 注入到请求 context
//  4. 下游 handler 通过 UserIDFromContext 读取用户 ID
//
// 当 REQUIRE_AUTH 为 false 时,middleware 仍会注入 seed user ID,
// 使得 API key 管理端点在没有 Authorization header 的情况下也能正常工作。
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

// contextKey 是用于 context value key 的私有类型,以避免 key 冲突。
type contextKey string

const userIDKey contextKey = "user_id"
const roleKey contextKey = "role"

// WithUserID 将用户 ID 注入到 context 中。
// 由 auth middleware 在校验成功后使用。
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// UserIDFromContext 从请求 context 中提取用户 ID。
// 如果存在则返回用户 ID 和 true,否则返回 ("", false)。
func UserIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(userIDKey).(string)
	return v, ok
}

// WithRole 将 role 注入到 context 中。由 auth middleware 在解析出已认证用户
// 的记录后使用。下游 RBAC 辅助函数通过 RoleFromContext 读取 role。
func WithRole(ctx context.Context, role Role) context.Context {
	return context.WithValue(ctx, roleKey, role)
}

// RoleFromContext 从请求 context 中提取 RBAC role。
// 如果已注入则返回 Role 和 true,否则返回 ("", false)。
func RoleFromContext(ctx context.Context) (Role, bool) {
	v, ok := ctx.Value(roleKey).(Role)
	return v, ok
}

// DefaultPublicRoutes 返回即使在 REQUIRE_AUTH 启用时也应保持公开的 GET 路由。
// 这些通常是健康检查、metrics 以及只读的发现类端点。
func DefaultPublicRoutes() []string {
	return []string{
		"GET /healthz",
		"GET /metrics",
		"GET /health",
	}
}

// DefaultAdminRoutes 返回仅限 admin 用户访问的 METHOD + path 前缀组合列表。
// 格式:"METHOD /path/prefix"。
// 这些路由执行特权写操作:修改平台配置、管理 users/agents/cases/tools,
// 以及安装 marketplace 包。
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

// DefaultProtectedRoutes 返回当 REQUIRE_AUTH 启用时需要认证的
// METHOD + path 前缀组合列表。
// 格式:"METHOD /path/prefix" — 例如 "DELETE /api/sessions/"
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
		// Model price 编辑属于运行时写操作(覆盖 ModelRegistry 条目),
		// 因此在 REQUIRE_AUTH 启用时需要 Bearer token。GET 保持公开可读。
		"PUT /api/models/prices/",
		// Case 的变更操作是写操作;GET 保持公开可读。
		"POST /api/cases",
		"PUT /api/cases/",
		"DELETE /api/cases/",
	}
}

// NewAuthMiddleware 创建一个在受保护路由上强制认证的 HTTP middleware。
// 当 requireAuth 为 false 时,所有请求都会放行并注入兜底用户 ID,
// 因此 auth 管理端点仍能正常工作。
//
// 受保护路由通过 METHOD + path 前缀进行匹配。例如,
// "DELETE /api/sessions/" 匹配所有对 /api/sessions/anything 的 DELETE 请求。
// publicRoutes 中列出的公开路由始终豁免认证。
func NewAuthMiddleware(store APIKeyStore, fallbackUserID string, requireAuth bool, protectedRoutes, publicRoutes []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 当认证关闭时,注入兜底用户并放行。
		if !requireAuth {
			ctx := WithUserID(r.Context(), fallbackUserID)
			ctx = injectRole(ctx, store, fallbackUserID)
			// REQUIRE_AUTH 关闭时,如果 seed user 角色是 viewer,则仍然要阻止写操作;
			// admin/user 继续放行,保持原有行为。
			if role, ok := RoleFromContext(ctx); ok && role == RoleViewer && isViewerWriteOperation(r.Method) {
				writeJSONError(w, "forbidden: viewer role is read-only", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 公开路由始终允许在无认证情况下访问。
		if isPublicRoute(r.Method, r.URL.Path, publicRoutes) {
			ctx := WithUserID(r.Context(), fallbackUserID)
			ctx = injectRole(ctx, store, fallbackUserID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 检查该路由是否需要认证。
		requiresAuth := isProtectedRoute(r.Method, r.URL.Path, protectedRoutes)
		if !requiresAuth {
			// 非受保护且非公开的路由 — 注入兜底用户。
			ctx := WithUserID(r.Context(), fallbackUserID)
			ctx = injectRole(ctx, store, fallbackUserID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 受保护路由 — 校验 API key。
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

// injectRole 解析用户的 role 并将其注入到 context 中。
// 如果 store 无法解析该用户,则使用 RoleViewer 作为安全默认值,
// 以避免将一个损坏的 session 意外提升为 admin 权限。
// 调用方必须已校验过 userID 非空。
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

// isViewerWriteOperation 在 viewer role 发起认证写请求时返回 true。
// viewer 在整个 API 上被限制为只读访问。
func isViewerWriteOperation(method string) bool {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return false
	}
	return true
}

// RequireRole 是一个 HTTP middleware 守卫,当 context 中的 role 不在
// 允许集合内时返回 403 Forbidden。它应放在 auth middleware 之后,
// 以便 role 已被注入。如果 role 缺失,出于安全考虑会被视为 RoleViewer。
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

// requireRoleForRequest 是为通过 http.HandleFunc 注册的 handler 提供的便利辅助函数。
// 它检查当前请求的 role 是否在允许集合内,如不在则写入 403 JSON 响应。
// 访问被允许时返回 true。
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

// RequireRoleFunc 提供与 http.HandleFunc 闭包兼容的直接 handler 级别检查。
// 它等价于 requireRoleForRequest,但已导出以供包外使用。
func RequireRoleFunc(w http.ResponseWriter, r *http.Request, allowed ...Role) bool {
	return requireRoleForRequest(w, r, allowed...)
}

// maskAPIKey 返回 key prefix 的仅用于展示的形式,适合用于列表响应。
// 它保留 prefix 的前 4 个和后 4 个字符,中间用 "****" 遮蔽,
// 因此枚举 /api/auth/api-keys 不会暴露真实的凭据 prefix。
func maskAPIKey(prefix string) string {
	if len(prefix) <= 8 {
		return prefix[:min(len(prefix), 4)] + "****"
	}
	return prefix[:4] + "****" + prefix[len(prefix)-4:]
}

// min 返回 a 和 b 中较小的一个。
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

// isProtectedRoute 检查给定的 method+path 是否匹配任一受保护路由。
// 当 method 等于路由的 method 且 path 以路由的 path 前缀开头时,即视为匹配。
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

// authenticateRequest 从 Authorization header 中提取 Bearer token,
// 通过 store 校验后返回关联的用户 ID。
func authenticateRequest(r *http.Request, store APIKeyStore) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", ErrInvalidKey
	}

	// 期望格式为 "Bearer <key>"
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

// currentUserID 从请求 context 中提取用户 ID。
// 当认证关闭时返回已配置的 seed user ID。
func (a *AuthAPI) currentUserID(r *http.Request) string {
	if userID, ok := UserIDFromContext(r.Context()); ok && userID != "" {
		return userID
	}
	return a.seedUserID
}

// SetSeedUserIDFromStore 从 store 中解析并设置 seed user ID。
// 启动时使用,用于在 REQUIRE_AUTH 关闭时建立稳定的兜底用户。
func (a *AuthAPI) SetSeedUserIDFromStore(store APIKeyStore) {
	if sqliteStore, ok := store.(*SqliteAPIKeyStore); ok {
		if u, err := sqliteStore.GetFirstUser(); err == nil && u != nil {
			a.seedUserID = u.ID
		}
	}
}

// writeJSONError 写入带有指定状态码的 JSON 错误响应。
func writeJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// handleAPIKeys 处理 /api/auth/api-keys 的 GET(列表)和 POST(创建)请求
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

// handleAPIKeyByID 处理 /api/auth/api-keys/{id} 的 DELETE(吊销)请求
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

	// 在吊销前校验该 key 是否属于当前用户。
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

// handleCreateAPIKey 为已认证用户创建一个新的 API key。
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

// handleListAPIKeys 返回当前用户的 API key,且不暴露哈希值。
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

// formatTime 将时间格式化为 RFC3339。用于保持 JSON 响应格式统一。
func formatTime(t time.Time) string {
	return t.Format(time.RFC3339)
}
