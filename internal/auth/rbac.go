// rbac.go —— 基于资源-动作(resource-action)矩阵的 RBAC 权限守卫。
//
// # 设计哲学（白盒）
//
// 平台采用三级 RBAC 角色（见 auth.go）：
//   - viewer：只读。对任意资源的写操作(read 之外)一律拒绝。
//   - developer（对应代码中的 RoleUser）：可对"运营类"资源执行写操作
//     （创建/更新/删除），但不能触碰"特权类"资源。
//   - admin：全部操作，含特权类资源与运维面。
//
// 角色与本项目 PLAN 文档中 "viewer/developer/admin" 的对应关系：
// 代码实现以 RoleUser 表示 developer（历史命名），二者语义等价。
//
// # 权限矩阵
//
// 每个 (Resource, Action) 组合映射到一个允许的最小角色集合：
//   - read：viewer / developer / admin 全员可读。
//   - 写操作（create/update/delete/write）按资源分级：
//       * 特权类（agents/cases/tools/mcp_servers/api_keys/observability）：
//         仅 admin。
//       * 运营类（providers/models/sessions/projects/cron/todos/skills/
//         memories/checkpoints/tasks）：admin + developer。
//
// 守卫分两种形态，覆盖项目两套路由风格：
//   - RequirePermissionFunc：供 http.HandleFunc 闭包直接内联调用，
//     拒绝时写入 403 JSON 并返回 false（与 RequireRoleFunc 语义一致）。
//   - RequirePermission：链式的 http.Handler 中间件版本。
package auth

import "net/http"

// Action 表示对资源执行的操作类别。
type Action string

const (
	// ActionRead 表示只读访问（GET 类）。
	ActionRead Action = "read"
	// ActionCreate 表示创建资源（POST 类）。
	ActionCreate Action = "create"
	// ActionUpdate 表示更新资源（PUT/PATCH 类）。
	ActionUpdate Action = "update"
	// ActionDelete 表示删除资源（DELETE 类）。
	ActionDelete Action = "delete"
	// ActionWrite 是 create/update/delete 的便捷聚合，用于守卫写接口。
	ActionWrite Action = "write"
)

// Resource 表示受 RBAC 保护的资源域。
type Resource string

const (
	// ResourceProviders 是 LLM Provider 管理（发现/同步）。
	ResourceProviders Resource = "providers"
	// ResourceModels 是 LLM Model 画像管理（价格/能力编辑）。
	ResourceModels Resource = "models"
	// ResourceSessions 是会话（session）生命周期管理。
	ResourceSessions Resource = "sessions"
	// ResourceAgents 是 Agent 配置（特权类）。
	ResourceAgents Resource = "agents"
	// ResourceCases 是预置/自定义 case（特权类）。
	ResourceCases Resource = "cases"
	// ResourceTools 是动态工具注册（特权类）。
	ResourceTools Resource = "tools"
	// ResourceMCPServers 是 MCP server 安装/管理（特权类）。
	ResourceMCPServers Resource = "mcp_servers"
	// ResourceAPIKeys 是 API key 管理（特权类）。
	ResourceAPIKeys Resource = "api_keys"
	// ResourceMemories 是记忆（memory）管理。
	ResourceMemories Resource = "memories"
	// ResourceProjects 是项目管理。
	ResourceProjects Resource = "projects"
	// ResourceCron 是定时任务管理。
	ResourceCron Resource = "cron"
	// ResourceTodos 是 TODO 管理。
	ResourceTodos Resource = "todos"
	// ResourceSkills 是 Skill 管理。
	ResourceSkills Resource = "skills"
	// ResourceObservability 是审计/全量 trace 等运维面（特权类）。
	ResourceObservability Resource = "observability"
	// ResourceCheckpoints 是 checkpoint 恢复管理。
	ResourceCheckpoints Resource = "checkpoints"
	// ResourceTasks 是任务（task）管理。
	ResourceTasks Resource = "tasks"
)

// privilegedResources 是仅 admin 可写的特权资源集合。
// 写操作（ActionWrite/Create/Update/Delete）命中其中任一即要求 admin。
var privilegedResources = map[Resource]struct{}{
	ResourceAgents:        {},
	ResourceCases:         {},
	ResourceTools:         {},
	ResourceMCPServers:    {},
	ResourceAPIKeys:       {},
	ResourceObservability: {},
}

// allowedRolesFor 返回允许执行 (resource, action) 的最小角色集合。
//
// 规则（注释即规则）：
//   - read 对所有角色开放。
//   - 写操作：特权资源仅 admin；运营资源 admin + developer(=RoleUser)。
func allowedRolesFor(resource Resource, action Action) []Role {
	// read 全员可读。
	if action == ActionRead {
		return []Role{RoleAdmin, RoleUser, RoleViewer}
	}
	// 写操作：按资源分级。
	if _, ok := privilegedResources[resource]; ok {
		return []Role{RoleAdmin}
	}
	return []Role{RoleAdmin, RoleUser}
}

// roleAllowed 判断给定角色是否在允许集合内。
// 角色缺失（空串）一律按 RoleViewer 处理，fail-closed（最小权限）。
func roleAllowed(role Role, allowed []Role) bool {
	if role == "" {
		role = RoleViewer
	}
	for _, a := range allowed {
		if role == a {
			return true
		}
	}
	return false
}

// RequirePermissionFunc 是闭包兼容的守卫：在 handler 闭包内调用，
// 当前 role 不在 (resource,action) 允许集合时写入 403 并返回 false。
// 与 RequireRoleFunc 行为一致，但语义更清晰（资源-动作矩阵）。
func RequirePermissionFunc(w http.ResponseWriter, r *http.Request, resource Resource, action Action) bool {
	role, _ := RoleFromContext(r.Context())
	allowed := allowedRolesFor(resource, action)
	if !roleAllowed(role, allowed) {
		writeJSONError(w, "forbidden: insufficient permission for "+string(resource)+" "+string(action), http.StatusForbidden)
		return false
	}
	return true
}

// RequirePermission 是 http.Handler 中间件版本，供需要链式的路由使用。
func RequirePermission(resource Resource, action Action) func(http.Handler) http.Handler {
	allowed := allowedRolesFor(resource, action)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, _ := RoleFromContext(r.Context())
			if !roleAllowed(role, allowed) {
				writeJSONError(w, "forbidden: insufficient permission for "+string(resource)+" "+string(action), http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
