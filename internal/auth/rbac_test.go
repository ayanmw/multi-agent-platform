package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAllowedRolesFor 锁定 RBAC 权限矩阵的核心规则（白盒：注释即规则）。
func TestAllowedRolesFor(t *testing.T) {
	// read 对全员开放。
	for _, res := range allResources() {
		got := allowedRolesFor(res, ActionRead)
		if len(got) != 3 {
			t.Errorf("read %s 应开放给 3 个角色，实际 %v", res, got)
		}
	}

	// 特权资源写操作仅 admin。
	for _, res := range []Resource{ResourceAgents, ResourceCases, ResourceTools, ResourceMCPServers, ResourceAPIKeys, ResourceObservability} {
		got := allowedRolesFor(res, ActionWrite)
		if len(got) != 1 || got[0] != RoleAdmin {
			t.Errorf("特权资源 %s 写操作应仅 admin，实际 %v", res, got)
		}
	}

	// 运营资源写操作 admin + developer(=RoleUser)。
	for _, res := range []Resource{ResourceProviders, ResourceModels, ResourceSessions, ResourceMemories, ResourceProjects, ResourceCron, ResourceTodos, ResourceSkills, ResourceCheckpoints, ResourceTasks} {
		got := allowedRolesFor(res, ActionWrite)
		if len(got) != 2 {
			t.Errorf("运营资源 %s 写操作应开放 admin+developer，实际 %v", res, got)
		}
		hasAdmin := false
		hasUser := false
		for _, r := range got {
			if r == RoleAdmin {
				hasAdmin = true
			}
			if r == RoleUser {
				hasUser = true
			}
		}
		if !hasAdmin || !hasUser {
			t.Errorf("运营资源 %s 写操作缺 admin 或 developer: %v", res, got)
		}
	}
}

// TestRequirePermissionFunc 验证 RequirePermissionFunc 在闭包内的 403/通过行为。
func TestRequirePermissionFunc(t *testing.T) {
	cases := []struct {
		name     string
		role     Role
		resource Resource
		action   Action
		wantPass bool
	}{
		{"viewer 读 providers 放行", RoleViewer, ResourceProviders, ActionRead, true},
		{"viewer 写 agents 拒绝", RoleViewer, ResourceAgents, ActionWrite, false},
		{"developer 写 models 放行", RoleUser, ResourceModels, ActionWrite, true},
		{"developer 写 api_keys 拒绝", RoleUser, ResourceAPIKeys, ActionWrite, false},
		{"admin 写 api_keys 放行", RoleAdmin, ResourceAPIKeys, ActionWrite, true},
		{"admin 写 agents 放行", RoleAdmin, ResourceAgents, ActionWrite, true},
		{"空 role fail-closed 拒绝写", Role(""), ResourceSessions, ActionDelete, false},
		{"空 role 读放行", Role(""), ResourceSessions, ActionRead, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			req = req.WithContext(WithRole(req.Context(), c.role))
			w := httptest.NewRecorder()

			got := RequirePermissionFunc(w, req, c.resource, c.action)
			if got != c.wantPass {
				t.Fatalf("RequirePermissionFunc = %v, want %v", got, c.wantPass)
			}
			if !c.wantPass && w.Code != http.StatusForbidden {
				t.Fatalf("拒绝时应为 403，实际 %d", w.Code)
			}
			if c.wantPass && w.Code != http.StatusOK {
				t.Fatalf("放行时应为 200，实际 %d", w.Code)
			}
		})
	}
}

// allResources 返回所有受保护资源，用于矩阵全量校验。
func allResources() []Resource {
	return []Resource{
		ResourceProviders, ResourceModels, ResourceSessions, ResourceAgents,
		ResourceCases, ResourceTools, ResourceMCPServers, ResourceAPIKeys,
		ResourceMemories, ResourceProjects, ResourceCron, ResourceTodos,
		ResourceSkills, ResourceObservability, ResourceCheckpoints, ResourceTasks,
	}
}
